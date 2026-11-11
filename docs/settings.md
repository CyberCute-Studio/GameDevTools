# Local settings and diagnostics

## Locations

GameDevTools resolves per-user directories through the operating system. On Windows, the default locations are:

- Settings: `%APPDATA%\GameDevTools\settings.json`
- Diagnostics: `%LOCALAPPDATA%\GameDevTools\Logs\gamedevtools.log`

The application does not store configuration beside input or output images. If a per-user location cannot be resolved or accessed, the application continues with in-memory defaults and displays a warning.

## Settings format version 1

Settings are UTF-8 JSON. Unknown fields, unsupported versions, invalid module identifiers, malformed module payloads, and invalid window dimensions are rejected rather than partially accepted.

```json
{
  "version": 1,
  "shell": {
    "selectedModule": "screenshot",
    "window": {
      "x": 0,
      "y": 0,
      "width": 1100,
      "height": 700,
      "maximized": false,
      "hasPosition": false
    }
  },
  "modules": {
    "screenshot": {
      "format": "png"
    }
  }
}
```

- `version` is the schema version. The current version is `1`.
- `shell.selectedModule` is `screenshot`, `image-resizer`, or `settings`.
- `shell.window` stores normal-window physical-pixel bounds and whether the window is maximized. `hasPosition` distinguishes saved coordinates from the default operating-system placement.
- `modules` contains independent JSON objects owned by feature modules. Module records are never shared implicitly. Module settings are decoded and validated by their owning module.

New versions are implemented through the `settings.Migration` interface. A migration consumes one complete versioned JSON document and returns the next document. The store applies registered migrations until it reaches the current version, backs up the original using a `settings.json.migration-<timestamp>` suffix, and writes the migrated document.

## Recovery and writes

Writes use a temporary file in the settings directory. The file is flushed and closed before it replaces the destination, and temporary files are removed after failure. A failed write leaves the current in-memory session intact and produces a visible shell warning.

Malformed settings are copied byte-for-byte to `settings.json.corrupt-<timestamp>` before defaults are written. The warning reports the backup path. If the backup cannot be created, or an existing settings file cannot be read, saving is disabled for that store so a later close or navigation cannot overwrite the original. Recovery and migration never inspect or modify saved images.

## Diagnostic redaction and rotation

Diagnostic events contain only a UTC timestamp, severity, a fixed operation identifier, and a fixed result code. The logging API has no message, path, filename, image, clipboard, or arbitrary-metadata field. User-facing local error details may contain a settings or backup path when needed for recovery; those details are not copied into the diagnostic event.

The active log rotates before it exceeds 1 MiB. Up to three numbered backups are retained (`gamedevtools.log.1` through `.3`), with older events discarded first. Logging is local and does not transmit data.

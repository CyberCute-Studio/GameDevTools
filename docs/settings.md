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
      "capture": {
        "version": 1,
        "mode": "preset-rectangle",
        "format": "png",
        "jpegQuality": 90,
        "outputFolder": "C:\\Users\\Example\\Pictures\\GameDevTools\\Screenshots"
      },
      "presets": {
        "version": 1,
        "custom": [
          {
            "id": "custom.64e35e29d28b49f8a13c06a20949a319",
            "name": "Store thumbnail",
            "width": 640,
            "height": 480
          }
        ],
        "selectedPresetId": "custom.64e35e29d28b49f8a13c06a20949a319"
      }
    },
    "image-resizer": {
      "presets": {
        "version": 1,
        "custom": [
          {
            "id": "custom.03497c466a434d7c80c325f97e56d52f",
            "name": "Icon",
            "width": 512,
            "height": 512,
            "resizeMode": "fit"
          }
        ]
      }
    }
  }
}
```

- `version` is the schema version. The current version is `1`.
- `shell.selectedModule` is `screenshot`, `image-resizer`, or `settings`.
- `shell.window` stores normal-window physical-pixel bounds and whether the window is maximized. `hasPosition` distinguishes saved coordinates from the default operating-system placement.
- `modules` contains independent JSON objects owned by feature modules. Module records are never shared implicitly. Module settings are decoded and validated by their owning module.

New versions are implemented through the `settings.Migration` interface. A migration consumes one complete versioned JSON document and returns the next document. The store applies registered migrations until it reaches the current version, backs up the original using a `settings.json.migration-<timestamp>` suffix, and writes the migrated document.

## Module presets

Each feature module stores custom presets under its own `presets` object. The optional `selectedPresetId` records the last selected built-in or custom preset in that module; an omitted value selects the first built-in. The nested preset format version is currently `1`; unsupported versions, unknown fields, duplicate identifiers or names, unknown selected identifiers, invalid module options, and invalid dimensions are rejected. Saving presets preserves unrelated options in the same module section.

Built-in definitions are code-defined and are not serialized into `custom`, so they cannot be edited or deleted through stored data. The initial built-ins are the Screenshot and Image Resizer lists in [PRD section 8.2](../PRD.md#8-application-shell-and-shared-behavior). Every Image Resizer built-in uses Fit.

Custom identifiers are generated locally and remain unchanged across renames. Names are trimmed and must be non-empty, cannot contain null characters, and must be unique within their module using a case-insensitive comparison. Screenshot and Image Resizer may reuse the same name without affecting one another.

Width and height must each be whole numbers from 1 through 16,384 pixels. Image Resizer targets must additionally contain no more than 100,000,000 pixels. Screenshot custom records have no fitting-mode field; Image Resizer records require `resizeMode` to be `fit`, `stretch`, or `crop-to-fill`.

The application registers preset validators for both module sections before loading settings. Malformed preset data therefore follows the same backup-and-recovery path as every other settings error: built-ins remain available, a warning reports the backup, and the original bytes are preserved rather than silently discarded.

## Screenshot capture options

The Screenshot module stores capture configuration under its own `capture` object, beside and independent of `presets`. Saving either one preserves the other, and each section owner registers its own validator for the shared module section.

- `capture.version` is the nested format version, currently `1`.
- `capture.mode` is `free-rectangle`, `preset-rectangle`, or `full-display`. The default is `free-rectangle`.
- `capture.format` is `png` or `jpeg`. The default is `png`.
- `capture.jpegQuality` is a whole number from 1 through 100. The default is `90`. It is stored even while PNG is selected so the last quality is retained when switching formats.
- `capture.outputFolder` is an absolute local folder path. The default is the user's Pictures folder under `GameDevTools/Screenshots`, resolved through the Windows known-folder API. The folder is created when a capture is saved, not when it is configured.

Unsupported versions, unknown fields, duplicate fields, invalid enumerations, out-of-range quality values, and missing, relative, or invalid-text folders are rejected. The whole settings file then follows the standard backup-and-recovery flow, so malformed capture options cannot silently discard presets or window state.

The filename pattern is fixed at `Screenshot_yyyyMMdd_HHmmssfff` with a `.png` or `.jpg` extension following the selected format; the Screenshot view displays the pattern but does not edit it.

## Recovery and writes

Writes use a temporary file in the settings directory. The file is flushed and closed before it replaces the destination, and temporary files are removed after failure. A failed write leaves the current in-memory session intact and produces a visible shell warning.

Malformed settings are copied byte-for-byte to `settings.json.corrupt-<timestamp>` before defaults are written. The warning reports the backup path. If the backup cannot be created, or an existing settings file cannot be read, saving is disabled for that store so a later close or navigation cannot overwrite the original. Recovery and migration never inspect or modify saved images.

## Diagnostic redaction and rotation

Diagnostic events contain only a UTC timestamp, severity, a fixed operation identifier, and a fixed result code. The logging API has no message, path, filename, image, clipboard, or arbitrary-metadata field. User-facing local error details may contain a settings or backup path when needed for recovery; those details are not copied into the diagnostic event.

The active log rotates before it exceeds 1 MiB. Up to three numbered backups are retained (`gamedevtools.log.1` through `.3`), with older events discarded first. Logging is local and does not transmit data.

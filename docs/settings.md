# Local settings and diagnostics

## Locations

CyberCute Dev Tools resolves per-user directories through the operating system. On Windows, the default locations are:

- Settings: `%APPDATA%\CyberCute Dev Tools\settings.json`
- Diagnostics: `%LOCALAPPDATA%\CyberCute Dev Tools\Logs\cybercute-dev-tools.log`

Builds from before the rename kept their settings in `%APPDATA%\GameDevTools\settings.json`. While the settings file above does not exist, loading reads that file through the same validation, migration, and recovery as the settings file and writes the result to the new location. The old file is never changed, and is not read again once the new file exists; if the new file is later deleted while the old one remains, the old settings are taken again, so removing the settings for good means deleting both. Taking them only ever creates the new file: should a new file turn out to exist, as when a redirected folder's share reported it missing while reconnecting, it is kept and loaded instead. If they could not be written at launch, later saves in that session still only create the file; one that finds a new file there keeps it, disables saving for the session, and the next launch loads it. A first run that finds neither file saves the same way until a save has reached the new file. If the old file exists but cannot be read, the defaults are used and saving stays disabled, with a warning naming it. The same happens, with a warning to reconnect the drive or share, when neither file is found and the nearest existing folder on their path does not answer, as when a redirected folder's share or a junction's target is down, so a save cannot replace or abandon a file that was only reported missing. A folder that refuses to be listed has answered, so a profile that denies listing `%APPDATA%` still has a normal first run. Once that folder answers, both files are looked for again, so a share that returned meanwhile gives its files rather than a first run. That lasts until a later launch can read the files, or Restore Default Settings creates the new file once the old one can be read or has been moved away from a folder that can be reached. Output folders it stores stay selected. Changing any capture or resize option stores the folder then in use, so a module whose options were changed keeps saving to `Pictures\GameDevTools` until another folder is chosen, as a stored default cannot be told from a folder chosen on purpose. A module with no stored options uses its default folder under `Pictures\CyberCute Dev Tools`. Images already saved stay where they are. Its logs and backups are not copied.

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
        "outputFolder": "C:\\Users\\Example\\Pictures\\CyberCute Dev Tools\\Screenshots",
        "shortcut": "Ctrl+Alt+S"
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
      "resize": {
        "version": 1,
        "format": "keep-source",
        "collision": "rename",
        "jpegQuality": 90,
        "background": "#FFFFFF",
        "outputFolder": "C:\\Users\\Example\\Pictures\\CyberCute Dev Tools\\Resized"
      },
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
- `shell.window` stores normal-window physical-pixel bounds, in screen coordinates, and whether the window is maximized; a window saved while minimized is saved as maximized when it was maximized before it was minimized. `hasPosition` distinguishes saved coordinates from the default operating-system placement.
- `modules` contains independent JSON objects owned by feature modules. Module records are never shared implicitly. Module settings are decoded and validated by their owning module.

New versions are implemented through the `settings.Migration` interface. A migration consumes one complete versioned JSON document and returns the next document. The store applies registered migrations until it reaches the current version, backs up the original using a `settings.json.migration-<timestamp>` suffix, and writes the migrated document.

Every build so far has written version `1`, so the application registers no migration: the builds that changed the format added optional fields and sections, which version `1` loads with their defaults when they are absent. The file each of those builds wrote is kept in `internal/app/testdata/settings`, and `app.TestOpenSessionUpgradesEverySettingsFormat` loads every one with all section validators registered, as the application starts, and through a restart; see [Settings, recovery, and shutdown verification](reliability-verification.md). A change that removes or renames a field, or changes what one means, needs a new version, a migration, and a fixture of the version it replaces.

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
- `capture.outputFolder` is an absolute local folder path. The default is the user's Pictures folder under `CyberCute Dev Tools/Screenshots`, resolved through the Windows known-folder API, with the Pictures folder's path resolved as Windows resolves it, so a trailing space or period Windows drops from it is dropped. When the known folder cannot be resolved, or gives a default a capture would refuse, the Pictures folder in the user's profile is used, and when that gives none either, no folder is selected and a startup warning says why. The folder is created when a capture is saved, not when it is configured.
- `capture.shortcut` is the global capture-invocation shortcut in its canonical `Modifier+Key` form, for example `Ctrl+Alt+S`. At least one of Ctrl, Alt, Shift, or Win is required, plus one letter (A–Z), digit (0–9), or function key (F1–F24). The default is `Ctrl+Alt+S`. Sections written before this field existed omit it and load with the default.

Unsupported versions, unknown fields, duplicate fields, invalid enumerations, out-of-range quality values, unparseable or modifier-less shortcuts, and missing, relative, or invalid-text folders are rejected. The whole settings file then follows the standard backup-and-recovery flow, so malformed capture options cannot silently discard presets or window state. A stored folder that only the save rule refuses, one through a name ending in a space or a period, a reserved device name such as `COM1`, a colon that names a file stream, a `..` Windows reads differently from Go, or a path that names only a drive, volume, device, or server (a bare `C:` is a relative path, so it is rejected as above), leads through another device, such as `\\.\pipe\x` or a server's `pipe`, `mailslot`, or `IPC$` share, or has no plain server and share name, such as `\\server\..\x` or a path with three leading separators, is not treated as malformed: it stays selected, a startup warning names it until another folder is chosen, and until then each capture is still copied to the clipboard but not saved.

The filename pattern is fixed at `Screenshot_yyyyMMdd_HHmmssfff` with a `.png` or `.jpg` extension following the selected format; the Screenshot view displays the pattern but does not edit it.

## Image Resizer resize options

The Image Resizer module stores job configuration under its own `resize` object, beside and independent of `presets`. Saving either one preserves the other, and each section owner registers its own validator for the shared module section.

- `resize.version` is the nested format version, currently `1`.
- `resize.format` is `keep-source`, `png`, or `jpeg`. The default is `keep-source`, which encodes each output in its source file's format.
- `resize.collision` is `rename`, `skip`, or `overwrite`. The default is `rename`. The policy applies to destination files when a job runs; it never changes how source files are treated.
- `resize.jpegQuality` is a whole number from 1 through 100. The default is `90`. It applies whenever JPEG output is produced, including Keep Source Format jobs with JPEG sources, and is stored even while PNG is selected so the last quality is retained when switching formats.
- `resize.background` is the JPEG transparency flattening color in canonical `#RRGGBB` form. The default is `#FFFFFF`. Transparent pixels are flattened against it whenever an image with alpha is encoded as JPEG; PNG output preserves alpha instead.
- `resize.outputFolder` is an absolute local folder path. The default is the user's Pictures folder under `CyberCute Dev Tools/Resized`, resolved through the Windows known-folder API, with the Pictures folder's path resolved as Windows resolves it, so a trailing space or period Windows drops from it is dropped. When the known folder cannot be resolved, or gives a default the folder rules below refuse, such as one whose path leads through a device or holds a colon, the Pictures folder in the user's profile is used instead, and when that gives none either, no default folder is selected. The folder is created when a job starts and its destinations are planned, before any result is written and whether or not one is, not when it is configured. A path that Windows would resolve to another path, as written or with its `.` and `..` elements removed as jobs remove them, is refused unless it is an extended-length path marked `\\?\` with backslashes, which Windows takes verbatim (Windows resolves `//?/` like any other path): one whose own name ends in a space or a period, which Windows removes from the last name of a path, one through a name that ends in a single period, which Windows removes from a name a path leads through, or one that ends in a device name such as `NUL`. A path with a colon after its drive or share is refused however it is written, because NTFS reads such a name as a file stream: `Resized::$INDEX_ALLOCATION` names the folder `Resized`, and `x:y` cannot be created; a colon in a share's server or share name, which neither may hold, is refused too. So is a path that names only a drive, volume, device, or server rather than a folder on it, such as `\\?\C:` without its root separator, and a device path that leads to no drive, volume, or share, such as the named pipe `\\.\pipe\x`, which creating the folder would open. The same goes for a server's `pipe`, `mailslot`, or `IPC$` share, such as `\\localhost\pipe\x`. The share is judged by its name alone, so a WebDAV folder with one of those names at a server's web root, such as `\\server@SSL\pipe`, is refused too, while `\\server@SSL\DavWWWRoot\pipe` is not. So is any network path whose server or share is not a plain name, such as `\\server\..\x`, a path with three leading separators, or one with a `;` network provider name. A path may lead through a name Windows keeps there, such as a folder whose name ends in a space, and a job creates any missing folder in it under the name the path gives.

Unsupported versions, unknown fields, duplicate fields, invalid enumerations, out-of-range quality values, malformed background colors, and missing, relative, or invalid-text folders are rejected. The whole settings file then follows the standard backup-and-recovery flow, so malformed resize options cannot silently discard presets or window state. A stored folder that only the folder-name, stream, volume, device, server-share, and plain-name rules refuse, which builds before output planning accepted, and for a server's `pipe`, `mailslot`, or `IPC$` share or a network path with no plain server and share name, such as `\\server\..\x`, builds up to QLT-03C, is not treated as malformed: every other setting loads as stored, the default output folder is selected in its place, and a startup warning names the stored folder, which the file keeps until the next save of the resize options. When the default folder cannot be resolved either, no folder is selected, and the warning says why the default folder is unavailable and asks the user to choose one. Once the user chooses a folder, either warning leaves the shell's note at once.

## Recovery and writes

Writes use a temporary file in the settings directory. The file is flushed and closed before it replaces the destination, and temporary files are removed after failure. A failed write leaves the current in-memory session intact and produces a visible shell warning. A save that is interrupted before its rename — by a crash, a forced exit, or the end of the Windows session — can leave its `.settings-*.tmp` file behind; loading the settings removes such files once they are more than a day old, older than any save in flight, even one by an instance in another of the user's Windows sessions on a settings folder redirected to a network share, whose server's clock can be minutes off this computer's.

Malformed settings are copied byte-for-byte to `settings.json.corrupt-<timestamp>` before defaults are written. The warning reports the backup path and stays in the shell's note for the session, through later saves and Restore Default Settings, as that backup alone holds the malformed file's settings. A warning that the defaults, or migrated settings, could not then be written leaves the note once a later save succeeds. If the backup cannot be created, or an existing settings file cannot be read, saving is disabled for that store so a later close or navigation cannot overwrite the original. Every backup — `corrupt`, `migration`, and `reset` — is read back and compared with the bytes it copies before the original is replaced; a copy that differs or cannot be read is removed and treated as a backup that could not be created. Recovery and migration never inspect or modify saved images.

The settings are saved, with the window's placement, when the main window closes and when Windows ends the user's session — signing out, restarting, or shutting down — which asks nothing and only logs a save that fails, as no one is left to read a warning. At the session's end the save runs once the window is destroyed and its work stopped, and is waited for at most three seconds, with that work, so a settings folder on a network share that has stopped responding does not hold the session's end up; a field whose edit the window's destruction commits, as it takes the focus from it, is saved with the rest rather than on its own, and a save the end of the process interrupts leaves the settings file as it was. Should the process outlive the session's end, as when an installer closes the application, it exits only once that save has finished, so an instance started after it loads the settings the save wrote.

## Restoring default settings

Restore Default Settings in the Settings view (SET-005) asks first; its safe answer, No, is the default. It then copies the settings file byte-for-byte to `settings.json.reset-<timestamp>`, verified as every backup is, and replaces it with the default settings, keeping:

- each module's `presets` object, exactly as stored: its custom presets and its `selectedPresetId`. A custom preset is deleted only with Delete in its module, which asks first (PRESET-002). The section of a module this build does not know, such as one a newer build added, keeps its `presets` object as stored too, even when another of its fields is given twice, which no check of this build refuses as the file loads; only a `presets` object given twice makes the restore change nothing.
- `shell`: the window placement and current module, which describe the running window and are saved again as it closes.

Everything else in the module sections is removed and loads as its default: the Screenshot `capture` options — capture mode, format, JPEG quality, output folder, and global shortcut — and the Image Resizer `resize` options — format, collision policy, JPEG quality, transparency background, and output folder. The running session takes the restored settings at once. Screenshot registers the default global shortcut in place of the one registered and releases that one, reporting a default another application holds as at startup, in the Screenshot view and in the restore's outcome. An output folder that has no default once the restore has reset it is reported in the shell's note, with the reason startup found, as at startup, until a folder is chosen. A module's status area drops the outcome of an option change it still shows, such as an output folder saved, or Start Capture's or Start Resize's refusal of the options or of JPEG text, as the restore replaced that option, and keeps any other notice, such as the last capture's result or job's summary. Saved images, source images, the images in the resize list, a running resize job, which runs on the options it started with, and unsaved edits in the preset forms are not touched.

When the settings file cannot be read or backed up, nothing changes and the outcome says so. When the backup exists but the restored settings cannot be written, the session keeps them, as it keeps any setting change whose save fails (SET-004), the shell shows the failed save's warning, the outcome names the backup, and the file keeps the previous settings until the next successful save. When there was no settings file and none can be written, the outcome says that there was nothing to back up. Without a settings path, the session keeps the restored settings in memory, and the outcome says that they cannot be saved.

A session whose saving was disabled because recovery could not read or back up the original file saves again once the restore has backed that file up, and only then: when the restore finds that file missing, it changes nothing, as it found an unreadable one, and saving stays disabled, as a file on a network share that is down is reported missing too, and a later save would otherwise replace it with no copy made. The exception is a session disabled by the `%APPDATA%\GameDevTools` file while no settings file exists, which the restore does not replace: once that file can be read, the restore backs it up, keeps it in place, and creates the settings file without replacing one, so saving works again. While it exists but still cannot be read, or is reported missing while the nearest existing folder on its path does not answer, the restore changes nothing and names it, as the presets the restore keeps may be in it and a new settings file would stop it ever being taken; closing whatever holds it, reconnecting its share, or moving it out of that folder to go on without its presets, lets the next restore proceed. If a settings file turns out to exist, or the new file cannot be created, nothing changes and saving stays disabled. A session that did load that file, and was disabled because the backup its migration needs failed, already holds its settings, so the restore keeps the session's presets, and one the user renamed or deleted stays renamed or deleted. When it started from the defaults because the file could not be read — held open by another program, say — the presets kept are the ones the file stores, with its `selectedPresetId`, once the restore can read it, not the defaults the session started from, joined by the presets created in that session, which could not be saved — one whose name a preset in the file already uses is kept under the name Duplicate would give its copy, such as `Icon copy` — and both modules then show them. A preset the session holds unchanged from the `%APPDATA%\GameDevTools` file, because its settings never reached the settings file, is not one it created: a settings file found later without it had it deleted, so it stays deleted. From then on the session's presets are the ones a later restore keeps, even after a restore whose write failed: a preset the user then renames or deletes stays renamed or deleted, rather than returning from the file. Of a malformed file that still does not decode, only the backup is kept, as recovery from it keeps only its backup, and the restore keeps the session's own presets. Once the file is backed up, the startup warnings about it — saving disabled, settings that could not be written, a stored output folder replaced — leave the shell's note, which keeps only the warnings that still apply, such as an output folder that has no default, and the warning naming the backup of a malformed file the session started without, which still holds that file's settings.

## Diagnostic redaction and rotation

Diagnostic events contain only a UTC timestamp, severity, a fixed operation identifier, and a fixed result code. The logging API has no message, path, filename, image, clipboard, or arbitrary-metadata field. User-facing local error details may contain a settings or backup path when needed for recovery; those details are not copied into the diagnostic event.

The settings events are `settings.load` with `recovered-with-warnings`, `settings.save` with `write-failed`, and `settings.restore` with `restored`, `not-restored`, or `write-failed`; the end of the Windows session records `app.end-session` with `stopped`. `audit.TestDiagnosticEventsAreFixed` fails on any event outside this list or whose operation or code is not a constant; see [Offline, privacy, file access, and dependency verification](privacy-verification.md#log-privacy-review).

The active log rotates before it exceeds 1 MiB. Up to three numbered backups are retained (`cybercute-dev-tools.log.1` through `.3`), with older events discarded first. Logging is local and does not transmit data.

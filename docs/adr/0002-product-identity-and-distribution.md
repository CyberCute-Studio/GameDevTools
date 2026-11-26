# ADR-0002: Product identity and portable distribution

- **Date:** 2026-11-22
- **Status:** Accepted. Amended 2026-11-26 by REL-04, which added the arm64 ZIP (PRD 1.12)
- **Requirements:** TECH-010, NFR-033, NFR-004, PRD section 19 (resolved in PRD 1.5, section 20)

## Context

PRD section 19 left four decisions open until public distribution: the final name, the package format, code signing, and automatic updates. The application is a single Go executable that needs no installation, works offline, and runs without administrator rights.

## Decision

| Decision | Choice |
|---|---|
| Product name | **CyberCute Dev Tools** |
| Executable | `cybercute-dev-tools.exe` |
| First version | 1.0.0, in `internal/release` |
| Publisher in the version resource | CyberCute Studio |
| Package | Portable ZIP for each 64-bit Windows architecture, x64 and arm64; no installer. The first release, 1.0.0, publishes the arm64 ZIP alone, as a GitHub pre-release (PRD 1.12) |
| Code signing | None. Each ZIP has a published SHA-256 checksum |
| Updates | Manual. The application makes no network access to look for one |
| License | MIT, chosen by the project owner in REL-03 (PRD 1.7), in `LICENSE` and as `LICENSE.txt` in the ZIP |

### The rename

Per-user data moves to `%APPDATA%\CyberCute Dev Tools` and `%LOCALAPPDATA%\CyberCute Dev Tools\Logs`, and the default output folders to `Pictures\CyberCute Dev Tools`.

While the new settings file does not exist, the first launch reads `%APPDATA%\GameDevTools\settings.json` and writes its settings to the new location. The old file is left in place, so an older build still works, and its settings are taken again if the new file is later deleted while it remains. Adoption is all-or-nothing at startup: when loading returns, either the new file exists or the session saves nothing. Taking the settings only ever creates the new file, so one a redirected folder merely reported missing is kept and loaded instead. If that write fails for any other reason, the session shows the old settings with saving blocked, says that its changes will not be saved and that the old file still holds the settings, and the next launch tries again. As a new settings folder that stays unusable fails every launch the same way, the warning also says to reconnect it or, if it is gone for good, to delete the link or junction there, which gives up any settings stored through it, or to move away a file that holds its name. An old file that cannot be read blocks saving, as an unreadable settings file already does, because a save would create the new file and so abandon the old settings. Its warning says to repair access to it or, if those settings are no longer needed, to delete or move it, which gives them up. So does finding neither file while either of their folders, or the target of a link at either file's own name, cannot be reached, as a share that is down reports its file missing. The warning says to reconnect it or, if it is gone for good, to delete the link or junction leading to it, which gives up any settings stored there. A first run that finds neither file in folders that answer saves normally, its writes only creating the new file until one lands. While saving is blocked and no new file exists, Restore Default Settings refuses and says what to do; it does not create the new file from the old one. We chose this over letting a session run on adopted settings that were never written, because Restore would then have to infer which presets the user deleted from file timestamps, and each attempt to do that lost data in another combination of states. Output folders stored in the settings stay as they are. Changing any capture or resize option stores the folder in use, so a module whose options were changed keeps `Pictures\GameDevTools` until another folder is chosen: a stored default cannot be told from a folder chosen on purpose, and a chosen folder must stay (PRD section 20). A module with no stored options follows the default (SCR-063, RSZ-021), so its new images go to `Pictures\CyberCute Dev Tools`. Images already saved stay where they are, and old logs are not copied.

These identifiers keep their old names on purpose:

- The single-instance mutex and the main window class, so an older build and this one cannot run side by side in one Windows session. Holding the mutex is what makes reading the older build's settings safe there. An older build in another of the user's sessions is not excluded, but it replaces its file whole, so a read gets one complete version or fails and blocks saving; what it changes after the new file exists stays in the old file.
- The `.gamedevtools-` prefix of temporary and set-aside files in output folders, so the documented recovery of a file an older build left behind stays true.
- The overlay window classes, the `GAMEDEVTOOLS_VALIDATION*` test variables, and the repository and Go module path, none of which users see.

### Resources without a new dependency

The icon and version resource are linked from `cmd/gamedevtools/resource_windows_<arch>.syso`, COFF objects that `internal/release` writes with the standard library. No C toolchain or resource compiler is needed, and plain `go build` includes them. A test fails if they are stale. After changing `assets/icon.ico` or the version, run `go generate ./cmd/gamedevtools`.

### Reproducible builds

The release build uses `-trimpath`, `-buildvcs=false`, and an empty build ID, and `scripts/package.ps1` pins the toolchain from `go.mod`, turns off the user's `go env -w` file and Go workspace mode, forces module mode, keeping from it only where modules come from and where they and the build's temporary files are kept, which cannot change the bytes, and fixes `GOAMD64` at `v1` and `GOARM64` at `v8.0` with no `GOEXPERIMENT` or `GOFLAGS`. ZIP entries are sorted and carry a fixed date. The same commit and architecture give the same executable and ZIP bytes from any checkout path and Go configuration.

### One ZIP per architecture

`scripts/package.ps1 -Architecture amd64` builds the x64 ZIP, the default, and `-Architecture arm64` the arm64 ZIP; both carry the same documents. `cmd/release` names the ZIP from the machine type in its executable's PE header, so the name cannot disagree with what it holds, and `scripts/verify-package.ps1` checks that the executable is built for the architecture the ZIP is named for. An arm64 executable runs only on Windows on Arm, so the package check of an arm64 ZIP runs there; CI, on an x64 runner, builds the arm64 ZIP and checks its toolchain without running it.

## Alternatives considered

- **Installer.** Adds a Start menu entry and an uninstall record, but also a build tool, an upgrade path, and an uninstaller to verify, for an application with nothing to install.
- **Code signing.** Removes the SmartScreen warning, but needs a verified publisher identity, CI secrets, and a recurring cost. It can be added later without changing the package.
- **Update check.** Would be the application's first network access, against its offline design (NFR-030 through NFR-032).
- **`go-winres` or `rsrc`.** Established tools, but each adds a build dependency and license to review for about 300 lines of format code.

## Consequences

- SmartScreen may warn on first run. The packaged README explains how to check the download and continue.
- Users update by replacing the extracted folder's files with those from the new ZIP, and learn of new versions outside the application.
- The repository and Go module keep the name `GameDevTools`. Renaming them is a separate decision.
- The application is released under the MIT License, so the package carries that license beside the third-party notices. A test holds `LICENSE` and the packaged copy to one text, under the copyright line in the executable's version resource.

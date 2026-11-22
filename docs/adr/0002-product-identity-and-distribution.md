# ADR-0002: Product identity and portable distribution

- **Date:** 2026-11-22
- **Status:** Accepted
- **Requirements:** TECH-010, NFR-033, PRD section 19 (resolved in PRD 1.5, section 20)

## Context

PRD section 19 left four decisions open until public distribution: the final name, the package format, code signing, and automatic updates. The application is a single Go executable that needs no installation, works offline, and runs without administrator rights.

## Decision

| Decision | Choice |
|---|---|
| Product name | **CyberCute Dev Tools** |
| Executable | `cybercute-dev-tools.exe` |
| First version | 1.0.0, in `internal/release` |
| Publisher in the version resource | CyberCute Studio |
| Package | Portable ZIP for 64-bit Intel/AMD Windows; no installer |
| Code signing | None. Each ZIP has a published SHA-256 checksum |
| Updates | Manual. The application makes no network access to look for one |

### The rename

Per-user data moves to `%APPDATA%\CyberCute Dev Tools` and `%LOCALAPPDATA%\CyberCute Dev Tools\Logs`, and the default output folders to `Pictures\CyberCute Dev Tools`.

While the new settings file does not exist, the first launch reads `%APPDATA%\GameDevTools\settings.json` and writes its settings to the new location. The old file is left in place, so an older build still works, and its settings are taken again if the new file is later deleted while it remains. Taking them only ever creates the new file, and so does every later save until one has reached it, so one a redirected folder merely reported missing is not replaced. An old file that cannot be read blocks saving, as an unreadable settings file already does, because a save would create the new file and so abandon the old settings; once it can be read or has been moved away, Restore Default Settings lifts that block by creating the new file and leaving any old one in place. Output folders stored in the settings stay as they are. A user who never chose a folder has none stored and follows the default (SCR-063, RSZ-021), so new images go to `Pictures\CyberCute Dev Tools` while those already saved stay in `Pictures\GameDevTools`. Old logs are not copied.

These identifiers keep their old names on purpose:

- The single-instance mutex and the main window class, so an older build and this one cannot run side by side in one Windows session. Holding the mutex is what makes reading the older build's settings safe there. An older build in another of the user's sessions is not excluded, but it replaces its file whole, so a read gets one complete version or fails and blocks saving; what it changes after the new file exists stays in the old file.
- The `.gamedevtools-` prefix of temporary and set-aside files in output folders, so the documented recovery of a file an older build left behind stays true.
- The overlay window classes, the `GAMEDEVTOOLS_VALIDATION*` test variables, and the repository and Go module path, none of which users see.

### Resources without a new dependency

The icon and version resource are linked from `cmd/gamedevtools/resource_windows_<arch>.syso`, COFF objects that `internal/release` writes with the standard library. No C toolchain or resource compiler is needed, and plain `go build` includes them. A test fails if they are stale. After changing `assets/icon.ico` or the version, run `go generate ./cmd/gamedevtools`.

### Reproducible builds

The release build uses `-trimpath`, `-buildvcs=false`, and an empty build ID, and `scripts/package.ps1` pins the toolchain from `go.mod`, turns off the user's `go env -w` file and Go workspace mode, and fixes `GOAMD64` at `v1` with no `GOEXPERIMENT` or `GOFLAGS`. ZIP entries are sorted and carry a fixed date. The same commit gives the same executable and ZIP bytes from any checkout path and Go configuration.

## Alternatives considered

- **Installer.** Adds a Start menu entry and an uninstall record, but also a build tool, an upgrade path, and an uninstaller to verify, for an application with nothing to install.
- **Code signing.** Removes the SmartScreen warning, but needs a verified publisher identity, CI secrets, and a recurring cost. It can be added later without changing the package.
- **Update check.** Would be the application's first network access, against its offline design (NFR-030 through NFR-032).
- **`go-winres` or `rsrc`.** Established tools, but each adds a build dependency and license to review for about 300 lines of format code.

## Consequences

- SmartScreen may warn on first run. The packaged README explains how to check the download and continue.
- Users update by replacing the executable, and learn of new versions outside the application.
- The repository and Go module keep the name `GameDevTools`. Renaming them is a separate decision.
- The project has no license of its own, so the package carries third-party notices only. One must be chosen before any public distribution.

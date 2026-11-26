# ADR-0003: Automatic updates from GitHub releases

- **Date:** 2026-11-26
- **Status:** Accepted. Replaces the Updates row of [ADR-0002](0002-product-identity-and-distribution.md)
- **Requirements:** APP-005, APP-006, APP-007, APP-008, NFR-030, NFR-031, NFR-032, AC-A02, AC-A06 (PRD 1.13, section 8.3)

## Context

REL-01 decided that the application never checks for updates: users learned of a new version outside it and replaced the extracted folder's files by hand. The project owner asked in REL-05 for the application to update itself as it starts, the way a game launcher such as Steam's does, and chose the details below. Until then the application made no network access at all, and `internal/audit` pinned that: no HTTP or TLS package, no code that connects, no URL, and no program started but File Explorer.

## Decision

| Decision | Choice |
|---|---|
| When | At every start, after the single-instance check and before the settings load or the main window appears, except the start of the version an update has just installed, which that update has just looked for. No setting turns it off |
| Where | The GitHub releases of `CyberCute-Studio/GameDevTools`, which the project owner made public so that the application reads them without a token. Drafts are skipped; pre-releases count, as every release so far is one |
| What | The newest release later than the running version whose tag is `v<major>.<minor>.<patch>` and that publishes `cybercute-dev-tools-<version>-windows-<arch>.zip` and its `.sha256` for the running architecture |
| Trust | HTTPS to GitHub, with Windows' certificate store, and the ZIP's SHA-256 against the release's checksum file: what a manual download asks of the user. No signature of its own |
| Install | The ZIP's files, checked to be plain files in the release's folder with an executable for the running machine type, replace those of the folder the executable was extracted to; the running executable is renamed aside, which Windows allows, and the new one takes its name. A document the folder lacks, as one a newer release adds, is put in place first, as the new version takes the folder for a release only with all its documents. The new version then starts and the old process exits |
| Scope | Only a folder that holds the release's executable name and the documents a release ZIP carries (`update.Folder`). A developer's or a test's build, run from anywhere else, never updates |
| Failure | Without a network, or when GitHub does not answer within five seconds, the start goes on as it is and says nothing. A release found and not installed is reported in the main window, and the folder is left with a working version |

### The start

The check runs in `internal/app` before anything else the start does, in a window of its own class that appears only once the check has taken a second, which a check that finds nothing newer typically ends well within, or at once when an update is found, and then stays at least half a second so that it never flashes: it shows what the update is doing, a progress bar, and Skip Update, which stops the update until it starts to install. It runs before the settings load because loading can back up and replace a malformed settings file; the process that loaded them would then exit for the new version, whose load would find a sound file and warn of nothing.

Once the update has returned, the window stays in the foreground until the main window has appeared, or the new version has been started: closed before, it would hand the foreground to another program, and the window that follows would open behind it. The window is shown with `SetWindowPos` rather than `ShowWindow`, whose first call in a process applies the show state the process was started with, such as a shortcut's Run: Minimized, which is meant for the main window. A folder that cannot be written is found before anything is reported, so that it shows no window for a download that never begins.

The new version starts while the update window is in the foreground, so that Windows lets its main window come to the front, with the show state the old process was started with, the old process's right to the foreground (`AllowSetForegroundWindow`), and the argument `--updated` (`update.RelaunchArgument`). It starts while the old process still holds the single-instance mutex, and its claim waits for a holder with no main window (`claimInstance`), so it takes the mutex as the old one exits. The argument tells it that it follows an update: its claim does not take the old process's update window for a running instance's, and its start does not look for a newer release again, which the old process has just done, so that a release whose executable is not the version its tag names cannot install itself at every start in a loop. That start removes the staging folder the update left, which holds the replaced executable, retrying for two seconds while the exiting process still holds it, and leaves what it cannot remove to a later start, which tries once without waiting. A second launch during an update brings the update window forward, as it would the main window, and exits.

### Network access, reviewed

`internal/update` is the only package that imports `net`, `net/http`, `crypto/tls`, or `crypto/x509`, and the audit pins that. Its two addresses are GitHub's API for the release list and the prefix of an asset's download; a download follows redirects to HTTPS addresses only. The executable holds no code that listens, accepts, or serves a connection. The requests carry the running version in their User-Agent, as GitHub's API asks every client to name itself, and nothing else of the user's. A proxy named by `HTTPS_PROXY` is used, as Go's HTTP client does; the Windows proxy settings are not. The connection is closed once the update ends, so none stays open while the application runs.

### Files, reviewed

The update writes only in the folder it updates: a staging folder named `.cybercute-update-*`, the downloaded ZIP and its extracted files in it, and the renames that put them in place, each tried again for two seconds while another program holds the file, as a virus scanner can hold one it has just seen written or run. NFR-032 names that folder beside the settings and log folders.

## Alternatives considered

- **A setting to turn the check off, or asking before installing.** The project owner chose an always-on update that installs without asking, as a game launcher does, with Skip Update for the one start.
- **A signed update.** An Ed25519 signature checked against a key built into the executable would protect users from a release published through a compromised GitHub account, but needs a key kept outside the repository, whose loss would end automatic updates. The project owner chose the checksum file; a signature can be added in a later release, which its predecessor installs as it installs any other.
- **A releases-only public repository, or a token in the executable.** A token anyone could read out of the executable would leak; a second repository would split the releases from the source they are built from.
- **WinHTTP or WinINet.** They would follow the Windows proxy settings, but bind a network library the audit has to review function by function. Go's HTTP client needs none and is covered by the toolchain's own maintenance.
- **Checking after the main window appears.** The user could then start work that an update would interrupt; a launcher updates first.

## Consequences

- A start waits for the check, which typically takes well under a second and at most five seconds before it gives up. NFR-010's launch time is measured on builds that do not update, as a test's build is not in a release folder.
- A start from a folder the user cannot write, such as one under Program Files, reports the newer version at every start and keeps running the old one.
- `scripts/verify-package.ps1` runs the extracted package with `HTTPS_PROXY` pointing at a closed port, so a newer release on GitHub cannot change the folder it checks.
- Version 1.0.0 has no update check, so its users update to 1.1.0 by hand, and from 1.1.0 on each release installs itself.

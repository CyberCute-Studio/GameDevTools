# Offline, privacy, file access, and dependency verification

This document is the QLT-03 record for SET-007, NFR-030 through NFR-033, TECH-007, and the shared acceptance scenario AC-A02 (offline use) from [PRD section 15.3](../PRD.md#153-shared-acceptance). It records how the checks run, what the application can reach, where it writes, what its logs can hold, what it is built from, what was found and fixed, the result for each requirement, and what remains to check by hand.

## How to run the checks

From the repository root on Windows:

```bash
go fmt ./...
go vet ./...
go test -count=1 ./...
```

`go test` runs every check below except the vulnerability scan. To run only this record's own tests:

```bash
go test -count=1 -v ./internal/audit
go test -count=1 -v -run 'TestCoreWorkflowsRunOffline' ./internal/app
go test -count=1 -v -run 'TestSetClipboardImageOptsOutOfCloudUploadOnly' ./internal/screenshot/output
go test -count=1 -v -run 'TestExplorerPath|TestShellPath' ./internal/ui
```

The offline test opens the real main window, captures the real screen, and replaces the clipboard, so run it in an interactive desktop session, as the module suites are run. It needs no administrator rights and leaves the machine's network untouched.

The vulnerability scan reads Go's public vulnerability database, so it needs a network connection and is not part of `go test`. It scans a build that keeps its symbol table, because a scan of the stripped release executable cannot tell which functions were linked:

```bash
go build -o bin/cybercute-dev-tools-audit.exe ./cmd/gamedevtools
go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 -mode=binary bin/cybercute-dev-tools-audit.exe
```

## Environment of record

Results below were recorded on 2026-11-21:

| Property | Value |
|---|---|
| OS | Windows 11 Home 26H2, build 26300 (ARM64) |
| Device | HP OmniBook 5 Laptop 14-he0xxx, Snapdragon X Plus X1P42100, 32 GB RAM |
| Go | go1.27.4 windows/arm64, the toolchain `go.mod` pins; the audit's package and symbol checks examine the windows/amd64 release target |
| Vulnerability scanner | govulncheck v1.10.0, database updated 2026-11-11 |

## Offline operation

`app.TestCoreWorkflowsRunOffline` runs both core workflows with no network available. Switching the machine's adapters off would need administrator rights and would cut off everything else on it, so the test starts a child process inside a Windows **AppContainer** that holds no capabilities. Windows denies such a process every network connection, loopback included, and every write outside the folders granted to it and the container's own profile folder. The test grants it one temporary workspace, checks that the run left the profile folder unchanged, and removes the profile afterward.

| Step in the sandbox | Result |
|---|---|
| The process token is an AppContainer with zero capabilities | Pass |
| A TCP connection to `192.0.2.1`, an address reserved for documentation that routes nowhere, fails with the sandbox's access error (the test also accepts a no-route error, which a machine with no network may report first; that case has not been run) | Pass |
| A TCP connection to a listener the parent test holds on loopback is dropped by the sandbox, which shows as a timed-out connection (the test also accepts an access error), and the listener accepts nothing | Pass |
| Full Display capture through Start Capture on the real main window saves a complete PNG and reports the clipboard copy as successful. On the hosted CI runner, which refuses the sandbox the clipboard, the test requires the saved PNG and a process outside the sandbox able to open the clipboard | Pass |
| A real Image Resizer job through Start Resize saves every result and leaves every source byte-identical | Pass |
| The window closes through the end-of-session path and saves its settings | Pass |
| The log holds only well-formed events and no file name, folder, or workspace path | Pass |
| The settings file records no source image's name | Pass |
| Every file in the workspace is in the settings folder, the log folder, an output folder, or the test's own sources, with no temporary file left, and no folder was made anywhere else | Pass |
| The container's own profile folder, the only other place it can write, holds no file the run added or changed and no folder it added | Pass |

## Network, account, and telemetry audit

The application has no network code, account, sign-in, activation, update check, crash upload, or telemetry. `internal/audit` keeps that true:

| Check | Test | Result |
|---|---|---|
| No HTTP, mail, RPC, or TLS client, no package for running another program or loading code, and no profiling endpoint is linked | `audit.TestApplicationLinksNoNetworkPackages` | Pass |
| `net` is imported only by `golang.org/x/sys/windows`, for a few types | `audit.TestApplicationLinksNoNetworkPackages` | Pass |
| The built executable holds no function, in `net`, `syscall`, or `x/sys/windows`, that opens, connects, or listens on a socket or resolves a name | `audit.TestExecutableContainsNoNetworkCode` | Pass |
| No application code calls a socket or name-resolution function, or starts a program through `os`, `syscall`, or `x/sys/windows`. The only program it starts is File Explorer, through one reviewed `ShellExecuteExW` binding, for Open Folder and Reveal in Explorer when the user asks | `audit.TestApplicationCallsNoNetworkOrProgramFunction` | Pass |
| The application's own code loads only nine reviewed Windows libraries, each from the system folder, none of them a network API, and binds no function by address | `audit.TestApplicationLoadsOnlyReviewedLibraries` | Pass |
| Every Windows function the application's own code binds by name is on a reviewed list, so a new binding fails until it is reviewed | `audit.TestApplicationBindsOnlyReviewedProcedures` | Pass |
| Every function of `syscall` and `x/sys/windows` the application's own code uses is on a reviewed list, so a new one fails until it is reviewed | `audit.TestApplicationUsesOnlyReviewedSystemFunctions` | Pass |
| No application file dot-imports `os`, `io/ioutil`, `syscall`, or `x/sys/windows`, which would hide its calls from these source checks | `audit.TestAuditedPackagesAreNotDotImported` | Pass |
| No string in the application is a URL | `audit.TestApplicationNamesNoNetworkEndpoint` | Pass |

The nine libraries are `comdlg32`, `dwmapi`, `gdi32`, `kernel32`, `ole32`, `oleacc`, `shcore`, `shell32`, and `user32`. The process also holds libraries the Go runtime and standard library load for themselves, which this check does not list. Among them is the Winsock library `ws2_32.dll`, loaded because `golang.org/x/sys/windows` imports `net`; the checks above show that nothing in the application can open a socket through it.

### What Windows does with a copied screenshot

Windows can sync the clipboard to a user's other devices through their Microsoft account, when the user has turned that on. A screenshot placed on the clipboard without an opt-out would be uploaded by that feature, which NFR-031 does not allow. Each screenshot now carries the documented `CanUploadToCloudClipboard` format with the value zero. It stays in the local clipboard history (Win+V), as decided in [PRD section 20](../PRD.md#20-confirmed-product-decisions). If the marker cannot be placed, the image is not placed either: the outcome reports a clipboard failure and the saved file is kept. A failure while preparing the marker or image leaves what the user had copied in place. Placing them has to follow emptying the clipboard, so a failure at that last step leaves the clipboard empty.

## Filesystem access review

`audit.TestFileChangesStayAtReviewedSites` checks every use of the file-changing functions it lists from `os`, `io/ioutil`, `syscall`, and `x/sys/windows`, and fails on any site not reviewed here. Its `os` and `io/ioutil` lists are complete. Any other function of `syscall` or `x/sys/windows`, file-changing or not, fails `audit.TestApplicationUsesOnlyReviewedSystemFunctions` until it is reviewed. The test also fails if code binds a common file-changing Windows function by name around those lists, binds any Windows function by a name it cannot read, or uses the system's temporary folder. Any other Windows function the code binds by name, file-changing or not, fails `audit.TestApplicationBindsOnlyReviewedProcedures` until it is reviewed. These are source checks of ordinary code, not proof against code written to evade them.

| Location | What is written | Code |
|---|---|---|
| `%APPDATA%\CyberCute Dev Tools` | `settings.json`, its `.settings-*.tmp` while saving, and `settings.json.corrupt-*`, `.migration-*`, and `.reset-*` backups | `internal/settings/store.go`, `internal/settings/rename_windows.go` |
| `%LOCALAPPDATA%\CyberCute Dev Tools\Logs` | `cybercute-dev-tools.log` and up to three rotated copies | `internal/diagnostics/logger.go` |
| The Screenshot output folder the user chose, by default `Pictures\CyberCute Dev Tools\Screenshots` (SCR-063) | Captures, through a `.gamedevtools-*.tmp` file in the same folder, given their final name by a hard link or, on a volume without hard links, a rename that never replaces a file | `internal/screenshot/output/output.go`, `internal/screenshot/output/finalize_windows.go` |
| The Image Resizer output folder the user chose, by default `Pictures\CyberCute Dev Tools\Resized` (RSZ-021) | Results, through a `.gamedevtools-*.tmp` file in the same folder; a confirmed Overwrite briefly renames the file it replaces to `.gamedevtools-replaced-*` | `internal/imageresizer/output` |

The application reads only the settings file, the images the user added or dropped, and the output folder's entries, which it inspects to plan names. A dropped folder contributes the files directly inside it. Source images are opened read-only. One reviewed `CreateFile` site, `output.open`, requests no access to a file's contents and reads only its identity.

The file picker asks Windows not to add picked files to Recent Items (`OFN_DONTADDTORECENT`) and not to change the process's current folder. The folder picker and Open Folder act only on the user's request.

## Log privacy review

Every diagnostic event is a UTC timestamp, a severity, a fixed operation, and a fixed result code. The logging API has no field that could hold a message, path, file name, or image or clipboard data, and it rejects an operation or code that is not short lowercase metadata. `audit.TestDiagnosticEventsAreFixed` fails unless every call to the logger passes an event literal whose operation and code are constants in this catalog:

| Operation | Codes |
|---|---|
| `settings.load` | `recovered-with-warnings` |
| `settings.save` | `write-failed` |
| `settings.restore` | `restored`, `not-restored`, `write-failed` |
| `app.end-session` | `stopped` |

The offline test checks a real run's log against this format. User-facing error details may show a path where recovery needs it, as SET-007 allows, and are never copied into the log.

The application also reads `%APPDATA%\GameDevTools\settings.json`, the settings of a build from before the rename, while its own settings file does not exist, at launch only. It never writes to that file. When that file is reported missing too, the application opens its folder, or the nearest folder above it that exists, asks for one entry, and checks whether a link holds the file's name, only to tell a file that is gone from a share that is down; it keeps nothing it reads there.

## Dependency and license inventory

`audit.TestDependenciesAreInventoried` fails if the application links a module that is not listed here, a different version, a module `go.mod` replaces with other code, or a changed license text.

| Component | Version | License | Why it is used |
|---|---|---|---|
| `golang.org/x/sys` | v0.47.0 | BSD-3-Clause | Win32 bindings for the native windows, capture, clipboard, and file operations; the stack decision is in [ADR-0001](adr/0001-native-windows-stack.md) |
| Go standard library and runtime | go1.27.4 | BSD-3-Clause | Linked into the executable; includes the vendored `golang.org/x/net/dns/dnsmessage`, which `net` imports and the same license covers |
| JPEG test fixtures | Go 1.26.5 distribution | BSD-3-Clause | Test data only, with the license in [`internal/imageresizer/decode/testdata/LICENSE-go`](../internal/imageresizer/decode/testdata/LICENSE-go) |

There is no other module, no cgo, and no unjustified dependency to remove. The BSD-3-Clause license requires its notice to accompany binary distributions, so the REL-01 package carries the Go and `x/sys` notices in `THIRD-PARTY-NOTICES.txt`, and `release.TestNoticesCarryTheLinkedLicenses` fails if the Go license text is missing from it.

## Vulnerability review

| Scan | Result |
|---|---|
| Symbol scan of the windows/amd64 build, go1.27.4 with `x/sys` v0.47.0 | No vulnerabilities found |
| Symbol scan of the windows/arm64 build, go1.27.4 with `x/sys` v0.47.0 | No vulnerabilities found |

Before this item, the release was built with Go 1.24.0, a release line Go no longer supports, and `x/sys` v0.38.0 carried GO-2026-5024 (CVE-2026-39824), an integer overflow in `windows.NewNTUnicodeString`. The application never called that function and it was not linked, so the application was not affected. The fixed `x/sys` releases need `go 1.25.0` or newer in `go.mod`, so the module moved to `go 1.25.0` with `x/sys` v0.47.0, the newest release that does not need Go 1.26.

## Findings and fixes

| Finding | Fix |
|---|---|
| A screenshot copied to the clipboard could be uploaded by Windows clipboard sync when the user had it on, against NFR-031. | Each copy carries a zero `CanUploadToCloudClipboard` marker and stays in the local history. |
| The release was built with Go 1.24.0, which no longer receives security fixes. | `go.mod` pins the go1.27.4 toolchain, and a CI step fails if the release executable was built with any other. |
| `x/sys` v0.38.0 carried GO-2026-5024, in a function the application does not link. | Moved to `x/sys` v0.47.0 and `go 1.25.0`. |
| Reveal in Explorer ran `explorer.exe` by its bare name, which Windows looks up through a search path that can include the application's and the current folder. | It now runs Explorer by its full path in the Windows folder. |
| Reveal in Explorer passed a file saved under an extended-length or device output folder to Explorer as written. The shell's parser refuses that spelling, so Explorer would not select the file. | It names the file by its ordinary drive or UNC spelling whenever that spelling reaches the same file. |
| The shell drops trailing spaces and periods from every name in a path, so Reveal in Explorer and Open Folder could show a different file or folder for a path through a name such as `v2..` or `Photos `, and Reveal could open an unrelated folder for a path through a volume GUID, each without reporting a failure. Open Folder also failed for a `\??\` folder and opened nothing for a `\\.\` one. | Both give Explorer a spelling it reads as the same place, and report an error when there is none, as for a path through a reserved name such as `COM1`, which the shell cannot parse. Open Folder still opens a folder through a volume GUID, which the explore verb accepts; Reveal reports an error for one. A server's fully qualified name may end in its one root period, as in `\\nas.example.com.\share`, since that names the same server. |
| A Screenshot output folder that Windows reads as another place could put a capture somewhere the user did not choose: a folder named `Real.` sent captures to a new `Real` beside it, `x .\Shots` could land in `x \Shots`, a last name ending in a space left a stray folder and failed the save, and `\\.\UNC\server\share\..\x` could split the temporary and final files between two places. | A save now refuses, before creating anything, any path through a name ending in a space or a period, a network server's or share's name included but for the one period that ends a fully qualified server name, a reserved device name such as `NUL` or `COM1`, a colon that names a file stream, or a `..` Windows reads differently from Go (`pathform.NamesOneFolder`). This follows the Image Resizer's output folder rules and is stricter: the Image Resizer still accepts a name Windows keeps where it falls, such as `Photos ` or `COM1` before another name. The error asks the user to choose another folder, a stored folder of this kind is reported at startup, and a default folder of this kind falls back to the profile's Pictures folder or is reported as unavailable. An extended-length `\\?\` path, which Windows takes verbatim, can still name such a folder, except through a colon. A path with an ordinary `..` or a doubled separator is saved through its cleaned form (`pathform.SaveFolder`), so no stray folder is left. |
| The clipboard was opened with no owner window, for which Microsoft documents that `SetClipboardData` fails after `EmptyClipboard`. It worked on the environment of record. | The clipboard is opened for a hidden message-only window that lasts only for the copy. |
| The dependency check accepted a module `go.mod` replaced with other code at the same version. | It fails on any replaced module. |
| The source checks missed some file-changing and process-starting Windows functions, a library handle built without a loader call, and calls through a dot import. | Every bound Windows function, and every `syscall` and `x/sys/windows` function the code uses, must be on a reviewed list, library handles can be made only through the checked loader, and dot imports of the audited packages fail. |
| The clipboard calls for a copied screenshot could move between OS threads, which could fail the copy and leave the clipboard open, so no other application could copy or paste until GameDevTools exited. | The copy stays on one OS thread from opening the clipboard through closing it. |
| Moving to Go 1.27 broke the output-folder rules of both modules. Go 1.27's `path/filepath` takes the server and share of a `\\?\UNC` or `\??\UNC` path into its volume name, so every share was refused as naming only a volume, and `Clean` appended a period to a `\??\UNC` share's root, which would have named another folder. It also calls a path with `..` where a server or share belongs, such as `\\server\..\x`, relative, so a stored folder of either kind would have failed the settings file's validation and reset every module, and `Join` would have moved such a folder to the current drive. | Both modules read output folders, and the Image Resizer its source names, through `internal/pathform`, whose `VolumeName`, `IsAbs`, `Clean`, `Dir`, `Base`, and `Join` give the same answer on Go 1.26 and 1.27 (`pathform.TestPathsReadAlikeOnEveryGoRelease`). |
| A clipboard marker that could not be prepared left the clipboard already emptied, losing what the user had copied. | The marker is prepared before the clipboard is opened. |
| A hand-edited settings file could point Screenshot at a device, volume, or server path, such as `\\.\pipe\x`, `\\localhost\pipe\x`, `\\?\C:`, or `\\server`, and a save would try to create it, which for a named pipe connects to the program serving it (QLT-03B). | Such a path is refused when chosen and before a save opens anything (`pathform.NamesNoFolder`, the Image Resizer's rule moved to a shared package, plus, for Screenshot, a server's `pipe`, `mailslot`, and `IPC$` shares and any path with more than two leading separators; QLT-03C widens that refusal to any network path whose server or share is not a plain name and applies both to the Image Resizer), and reported at startup when stored. It still passes validation, a `\??\` path that names only a volume or device such as `\??\C:` included, so it cannot reset the settings file; a bare `C:` is a relative path and is still rejected as malformed. |
| The Image Resizer's output folder rule accepted a server's `pipe`, `mailslot`, or `IPC$` share, such as `\\localhost\pipe\x`, so a job would try to create the folder there, which for a pipe connects to the program serving it (QLT-03C). | The rule now refuses such a share through every UNC prefix and in any letter case, before anything is created. It also refuses any network path whose server or share is not a plain name, such as `\\\localhost\pipe\x`, `\\a\..\localhost\pipe\x`, or `\\;LanmanRedirector\server\pipe\x`, because what Windows would connect to cannot be judged from the text. Screenshot shares this rule, so it now refuses such a path too, including one it accepted before this item, such as `\\;x\nas\photos`. The check reads the path's text only and opens nothing. The share is judged by its name alone, so a WebDAV folder with one of those names at a server's web root, such as `\\server@SSL\pipe`, is refused as well, while `\\server@SSL\DavWWWRoot\pipe` is not. A stored folder of this kind does not reset the settings file: the Image Resizer selects the default folder with a startup warning, and Screenshot keeps it selected with one. |
| A stored Image Resizer folder that named only a volume or device in the `\??\` spelling, such as `\??\C:`, counted as a relative path, so it failed the settings file's validation and reset every module's settings (QLT-03D). | It is now refused as naming no folder, like its `\\?\` spelling, so the other settings load, the default folder is selected, and a startup warning names the stored folder. A relative path, including a bare `C:`, is still rejected as malformed. |
| The folder picker could store another folder than the one chosen, because the shell reports a folder named `v2..` as `v2` and one named `...` as its parent (QLT-03B). | The picker, which both modules' Browse use, refuses a choice whose reported path does not exist, or ends in a separator after a name, as the shell reports a folder named only periods, and asks for another folder. The check runs off the GUI thread, which waits for it at most two seconds and then refuses the choice as not responding. |
| Reveal in Explorer reported success for a saved file since moved or deleted, and opened some other folder (QLT-03B). | It reports that the file could not be found, which may mean it was moved or deleted or that its drive or network share is not available, since Windows reports those alike. It runs off the GUI thread, so a drive that does not respond cannot freeze the window. A repeated click starts no second reveal of the same file, and a late failure does not replace a newer capture's result. |
| A Screenshot save claimed its final name only with a hard link, so a capture to a volume without hard links, such as FAT32 or exFAT, failed (QLT-03A). | The save still tries the hard link first and otherwise renames the finished file with `MoveFileEx`, which refuses an existing name. The rename and a failed save's cleanup share one wait of up to about 775 ms for a program holding the file, and a temporary file that still cannot be removed is named in the error. The tests simulate the refused link on NTFS; saving to a real FAT32 or exFAT drive remains a manual pre-release check. |

The logs and the dependency set needed no change.

## Requirement results

| Requirement | Result | Evidence |
|---|---|---|
| SET-007 | Pass | The fixed event catalog and a real run's log. |
| NFR-030 | Pass | Both workflows complete in a sandbox with no network, and no network code is linked. |
| NFR-031 | Pass | No account, telemetry, or upload code exists, and copied screenshots opt out of clipboard sync. |
| NFR-032 | Pass | Every file-changing site is reviewed, and a real run wrote only to approved folders. |
| NFR-033 | Pass | The inventory above, enforced by version, license text, and the absence of any replacement. |
| TECH-007 | Pass | `x/sys` is maintained, compatible with Go and Windows, and BSD-3-Clause; the scan is clean. |
| AC-A02 | Pass | `app.TestCoreWorkflowsRunOffline`. |

## What these results do not cover

- **A machine with its adapters off.** The offline test denies the network to the application's process, and has not itself been run with the adapters off. Before release, turn on airplane mode and run a capture and a resize job by hand.
- **The clipboard copy in the sandbox on CI.** The hosted runner refuses the sandboxed process the clipboard, in the runs observed either when opening it or when writing to it, with or without a foreground window, while a process outside the sandbox can open it. CI's offline run therefore covers the save but not the copy, and excuses a failed copy only on that runner. The sandboxed copy was verified on the desktop environment of record, and CI covers clipboard delivery outside the sandbox through the `internal/screenshot/output` tests.
- **Name resolution inside the sandbox.** Windows resolves names in a system service, so the sandbox is not shown to block a lookup. That path is covered by the executable holding no name-resolution code.
- **A write the sandbox refused.** A denied write outside the workspace leaves no trace the test can read, so the review of every file-changing site is what covers it.
- **Real clipboard sync.** The marker is the documented opt-out, and the test reads it back, but no signed-in account was used to observe that nothing was uploaded. A third-party clipboard manager may ignore the marker.
- **What Windows itself records.** Windows can keep its own history of dialogs and of folders opened in File Explorer. Those records were not measured.
- **A folder the picker cannot tell from its neighbor.** When `v2` and `v2..` stand side by side, or a folder named only periods sits directly on the root of a drive or network share, the shell reports the other folder's path, which exists, so the picker cannot detect it. File Explorer cannot create such names.
- **A missing parent such as `con.d` on Windows 10.** `os.MkdirAll` may read such a name as a device there, failing the save with an error. It does not reproduce on the Windows 11 build of record, so it was not changed, and QLT-04 closed it when Windows 10 left the supported platforms.
- **Future advisories.** The scan reflects the database's date above. Run it again on the release candidate (REL-03).
- **Performance on the new toolchain.** Earlier performance figures were measured on go1.26.5. The [platform record](platform-verification.md#performance) has the figures on go1.27.4.

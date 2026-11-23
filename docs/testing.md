# Test and verification guide

This guide says how to check CyberCute Dev Tools: the commands that gate a merge, the full validation run, where each result is recorded, and the checks that still need a person or other hardware. [Requirement traceability](traceability.md) maps every PRD requirement to this evidence.

## Before you run anything

Run the tests on Windows 11 in an interactive desktop session, without administrator rights. Many tests open real windows, capture the real screen, and **replace the clipboard**, so leave the desktop alone while they run. In a locked or headless session the capture-content tests skip.

The `go` command fetches the toolchain `go.mod` names. No C compiler is needed.

## The checks that gate a merge

[CI](../.github/workflows/ci.yml) runs these on every pull request. Run them from the repository root:

```powershell
go fmt ./...
go vet ./...
go test -count=1 ./...
./scripts/package.ps1
./scripts/verify-package.ps1 -Package (Get-ChildItem bin/release/*.zip).FullName
```

`go fmt` must change nothing. The last command expects one ZIP in `bin/release`, so delete any from an earlier version first. `verify-package.ps1` needs the application closed. It checks the ZIP's checksum, contents, x64 architecture, version metadata, and icon, then runs the extracted executable with no Go on its `PATH`, as a first run and as an upgrade. CI also fails a release executable built with any toolchain other than the one `go.mod` pins.

`go test -race` is not available on Windows on Arm, the development machine's platform.

`go test ./internal/release` includes the documentation checks. They fail when the traceability matrix misses a requirement or names a test that does not exist, when a row that waits on an open hardware run or manual check claims to pass, when its lists of blockers disagree with its rows, when the user guide stops naming one of the application's controls, or when the release notes name another version.

## The full validation run

With the application closed and nothing else using the desktop:

```powershell
./scripts/validate.ps1
```

It moves windows, takes the foreground, and replaces the clipboard. It runs the whole suite, the release executable, and the release build, measures NFR-010 through NFR-014, and writes a JSON result file to `bin/validation`. `-Architecture amd64` runs the x64 release target, and `-ReferenceMachine` asserts that this is the PRD's reference machine. The [platform record](platform-verification.md#how-to-run-the-validation) describes both.

The performance benchmarks do not run in `go test` because their timings depend on the machine. The validation script runs them, and each record lists its own.

## Where results are recorded

| Area | Record | Covers |
|---|---|---|
| Screenshot | [Screenshot verification](screenshot-verification.md) | AC-S01 to AC-S12, NFR-011, NFR-012 |
| Image Resizer | [Image Resizer verification](image-resizer-verification.md) | AC-R01 to AC-R15, AC-A04, NFR-013, NFR-014 |
| Accessibility | [Accessibility audit](accessibility-verification.md) | UX-001 to UX-010, AC-A03, SCR-015 |
| Settings and shutdown | [Reliability verification](reliability-verification.md) | APP-004, SET-004 to SET-006, NFR-020 to NFR-024, AC-A01, AC-A05 |
| Privacy and dependencies | [Privacy verification](privacy-verification.md) | SET-007, NFR-030 to NFR-033, TECH-007, AC-A02 |
| Platform and performance | [Platform verification](platform-verification.md) | NFR-001 to NFR-004, NFR-010 to NFR-014, the combined run |
| Package | [ADR-0002](adr/0002-product-identity-and-distribution.md) and `scripts/verify-package.ps1` | TECH-010, the package's contents and integrity |

## Hardware runs

QLT-04A owns what needs hardware this project's environment does not have: x64 Windows 11, including the PRD's reference machine, other Windows 11 releases, display scaling above 100%, mixed scaling, negative coordinates, a rotated display, and a display disconnected during a selection. The [platform record](platform-verification.md#what-these-results-do-not-cover) lists them, and every requirement that waits on them is **Partial** in the [traceability matrix](traceability.md).

## Manual pre-release checks

No automated test can stand in for these. None has been done yet, and REL-03 owns them. Run them on the exact release candidate. M8, M9, and M10 are the only runs of part of what their requirements say, and M12 of AC-A04's moving the window during a job, so those rows are **Partial** in the [traceability matrix](traceability.md) until they pass. The other checks, and M12 for RSZ-041, add confidence to requirements the automated checks already cover.

| Check | What to do | Passes when | Covers |
|---|---|---|---|
| M1 | On a clean Windows 11 x64 machine, as a standard user with no Go installed, follow `USER-GUIDE.txt` from the ZIP through the [walkthrough](#user-guide-walkthrough) below | Every step works as written | APP-002, TECH-010 |
| M2 | Use both modules with the keyboard alone and with Narrator, and NVDA where available | Every control is reachable and announced sensibly | UX-006, AC-A03 |
| M3 | Turn on each Windows contrast theme and look at both modules and the capture overlay. Also set Windows apps to dark | Everything stays legible | UX-007, UX-008 |
| M4 | Sign out of Windows with a resize job running, then sign back in | No temporary file in the output folder, and the window opens where it was | NFR-022, AC-A05 |
| M5 | Save screenshots and resized images to a FAT32 or exFAT drive, a network share, and a OneDrive folder | Files are complete, and nothing is left behind or replaced | SCR-066, RSZ-027, NFR-023 |
| M6 | Turn on airplane mode, then run a capture and a resize job | Both complete | NFR-030, AC-A02 |
| M7 | With clipboard sync on and a signed-in account, capture a screenshot | It stays in Win+V and does not reach another device | NFR-031 |
| M8 | Paste a capture into Paint and into an Office application | The image arrives at full size | SCR-062 |
| M9 | Open a preset frame over a running application, click controls inside the frame, switch away and back, then drag the frame by its border with the mouse | The application reacts to the clicks, the frame stays in place at its size until dragged, and the drag moves it without changing its size | SCR-021, SCR-022, SCR-023, SCR-055, AC-S03, AC-S04 |
| M10 | Press the global shortcut while another application has the focus. Then start a Preset Rectangle selection, click in the other application, and press the shortcut again | The selected capture mode starts, and the second press brings the toolbar forward with the keyboard focus and no second selection | SCR-002, SCR-005 |
| M11 | Run a job and a capture against a drive that fills up | The error says the drive is full, and no partial file has a final name | RSZ-030, SET-004, AC-R14 |
| M12 | Drag the window with the mouse during a 100-image job | The window follows smoothly | RSZ-041, AC-A04 |
| M13 | Run the [vulnerability scan](privacy-verification.md#how-to-run-the-checks) on the release candidate | No finding that affects the application | NFR-033, TECH-007 |
| M14 | Start the `Validation matrix` workflow on GitHub | It passes on the hosted x64 Windows Server runners, which are not supported platforms but do run the x64 build | — |

## Recorded runs

Add a row here when a QLT-04A run or a manual check passes, naming the build it ran on and exactly the requirements it passed. The documentation test keeps a gated row **Partial** until this table names it for its run, so the pull request that records a run can also mark the row **Pass**, before the plan marks its item Done.

| Date | Run | Build | Passed |
|---|---|---|---|

## User guide walkthrough

This is the script for M1. Follow `USER-GUIDE.txt` rather than this list, and record any step where the guide and the application disagree.

1. **Download.** Check the ZIP's SHA-256 as `README.txt` describes, extract it, and start the application past SmartScreen.
2. **Single instance.** Start it again. The existing window comes forward.
3. **Free Rectangle.** Draw, move, resize, and type a size, then capture. Paste the image and open the saved file. Repeat without the mouse.
4. **Preset Rectangle.** Capture the HD preset over another application, clicking through the frame, dragging it by its border, and nudging it with the arrow keys first. The file is exactly 1280 × 720.
5. **Full Display.** Capture each display. Each file has that display's native resolution.
6. **Cancel.** Cancel a selection. No file appears, and the clipboard is unchanged.
7. **Global shortcut.** Start a capture from another application, change the shortcut, and restart to confirm that it was kept.
8. **Add images.** Add PNG and JPEG files by picker and by drop, a folder, a duplicate, a corrupt file, and an unsupported file. Each shows the status the guide lists.
9. **Fitting modes.** Run Fit, Stretch, and Crop to Fill on a 1000 × 500 image with a 512 × 512 preset. The sizes match the guide's table.
10. **Collisions.** Re-run a job under Rename, Skip, and Overwrite, and confirm that source images do not change.
11. **Cancel a job.** Cancel a large job. Finished results stay, and the rest show Cancelled.
12. **Presets.** Create, rename, duplicate, and delete a custom preset in each module, then restart. Each module keeps only its own.
13. **Restore defaults.** Restore Default Settings. Options reset, while presets and images stay, and a backup file appears.
14. **Limitations and troubleshooting.** Read sections 10 and 11 against what you saw. Nothing they say should be wrong, and no P0 limitation should be missing.
15. **Exit.** Close the application. The global shortcut no longer responds, and no overlay remains.

## Keeping the documents true

When behavior changes, the same pull request updates what describes it:

- `packaging/USER-GUIDE.txt` for anything a user sees, and `packaging/RELEASE-NOTES.txt` for what a version contains or leaves open.
- [The traceability matrix](traceability.md) when a requirement's evidence or status changes. A row that waits on a run becomes **Pass** only once [Recorded runs](#recorded-runs) names it.
- The verification record that owns the result, and the [PRD](../PRD.md) and [implementation plan](../IMPLEMENTATION_PLAN.md) if approved behavior changed.

# Release candidate validation (REL-03)

This is REL-03's record of the 1.0.0 release candidate: what was built, the checklist it was held to, each result, the defects the validation found, and the explicit list of what still blocks a release. [Requirement traceability](traceability.md) holds the per-requirement status, and [the test guide](testing.md#recorded-runs) holds the recorded runs.

**Result: the candidate is not releasable yet.** It passes every automated gate that has run, the pull request's CI included. Of the manual checks this environment can run, M9, M10, M13, M14, M15, M16, M17, M18, and M19 passed, M16 once the project owner decided after the run that its Open Folder condition holds (PRD 1.10), M12 failed in one of its three drags, and M8's Paint half passed while its Office half could not run. The packaged user guide does not match the candidate in three places, and its known limitations leave out the other defects found and left for later. The P0 requirements and acceptance scenarios listed under [Release blockers](#release-blockers) are still open, and every run recorded here counts for this candidate only.

## The candidate

| Property | Value |
|---|---|
| Version | 1.0.0 |
| Commit | `c646471` |
| ZIP | `cybercute-dev-tools-1.0.0-windows-x64.zip`, SHA-256 `bbcbdc13161d52c1cae16f58aed17dee32300aecef4891fb2cdeb307d271fe02` |
| Executable | `cybercute-dev-tools.exe`, SHA-256 `342b5f8289b41b6b8740d8c937fd278abc0dc35c84574aee8b97c53c96563a52` |
| Build | `scripts/package.ps1`. Rebuilt from the commit with identical bytes |
| Machine | Windows 11 Home 26H2, build 26300.9974, on ARM64 (Snapdragon X Plus X1P42100, 32 GB). The x64 executable ran under Windows' x64 emulation |
| Displays | 3440 × 1440 primary at (0, 0) and 1920 × 1200 at (3440, 623), both at 100% |

Later commits on the REL-03 branch change the records, the PRD's list of open decisions, and the documentation test only, not the ZIP's contents.

## Checklist

| Check | Result | Evidence |
|---|---|---|
| Formatting, static analysis, and the full suite (`go test -p 1`) | Pass | Run on an unlocked desktop at `c646471`. The suite also ran inside the validation below |
| Package integrity: checksum, contents (including `LICENSE.txt`), x64, version metadata, icon, unsigned, first run without Go, upgrade, portable | Pass | `scripts/verify-package.ps1`, all nine checks |
| Reproducible build | Pass | A second build from the commit gave the same ZIP bytes |
| x64 validation, `validate.ps1 -Architecture amd64` | Pass | `validation-amd64-26300-20261124-064118.json`; every target met, listed below |
| Offline operation | Pass (automated) | `app.TestCoreWorkflowsRunOffline` in the suite. M6 with real adapters off was not run |
| Clean exit | Pass (automated) | The release executable test in the validation and the [exit matrix](reliability-verification.md) |
| Documentation matches the candidate | **Fail** | The documentation tests in the suite pass. The user guide's keyboard reference and known limitations do not match the candidate in the three ways [blocker 8](#release-blockers) lists, the known limitations leave out the other defects that blocker names, and the release notes do not give M12's result. M1, which follows the guide on a clean machine, was not run |
| M13, vulnerability scan | Pass | govulncheck v1.10.0 on the packaged executable found no vulnerabilities, scanning on 2026-11-24. The run did not record the vulnerability database's date |
| M14, `Validation matrix` workflow | Pass | [Run 45341616501](https://github.com/CyberCute-Studio/GameDevTools/actions/runs/45341616501) on windows-2025 and windows-2022. On both, the Preset Rectangle overlay benchmark skipped, as the runner's 1024 × 768 desktop cannot hold a 1280 × 720 frame, so NFR-011's Preset Rectangle figures come only from the x64 validation above |
| CI on the pull request | Pass | [Run 45446606332](https://github.com/CyberCute-Studio/GameDevTools/actions/runs/45446606332) on the branch head `1b42414`, whose commits after `c646471` change only the records and the documentation test |

Performance in the x64 validation, under emulation:

| Requirement | Measured | Limit |
|---|---:|---:|
| NFR-010, warm launch, mean and slowest of 20 | 319.6 ms, 370.4 ms | 2,000 ms |
| NFR-011, Free Rectangle overlay, mean and slowest of 30 | 83.6 ms, 114.1 ms | 300 ms |
| NFR-011, Preset Rectangle overlay, mean and slowest of 30 | 30.5 ms, 36.3 ms | 300 ms |
| NFR-012, 3840 × 2160 capture to clipboard, estimated from the desktop capture as the [platform record](platform-verification.md#performance) describes | 168.5 ms | 1,000 ms |
| NFR-013, 100 mixed 12 MP JPEGs, slowest of 3 | 15.4 s | 120 s |
| NFR-014, peak heap during the batch | 220.3 MiB | 1,024 MiB |

## Manual checks

The project owner decided that a scripted run counts as a manual check when it drives the release build with real Windows input and measures every condition, as [the test guide](testing.md#manual-pre-release-checks) states. Every run below apart from M13 and M14 was scripted that way, on the candidate's own executable, with its settings and output redirected into a scratch workspace. M13 scanned that executable, and M14 built and tested the candidate's commit on GitHub's runners.

These limits applied to every run:
- The "other application" was the script's own separate process, with real windows and controls.
- `Ctrl+Alt+S` was already registered by another program on this machine, so the runs used `Ctrl+Shift+F9`.
- The window was minimized with Alt+Space, N.

The script also had guards. It refused a click on any window that was not its own, the application's, or a window it had opened. It refused any key unless one of those windows was in front. It stopped at the first input it had not sent itself. The first attempt on this candidate stopped that way, when another program injected a keyboard event during M19. It was discarded, and the run was repeated with nothing else using the machine.

The first M15 run on this candidate failed on one of the 19 samples taken as Enter pressed Capture, which found the overlay above the toolbar. The script could not then tell whether the capture had already hidden one of the windows. It was changed to count only samples with both windows visible, and M15 was run again. In that run every sample of that phase had a window hidden, so neither run settled whether the overlay was above the toolbar as Enter pressed Capture. The z-order is not one of M15's conditions, and in both runs the image of that capture differed from the pattern in 0 pixels, so M15 passed. Apart from the discarded first attempt, M15 is the only check that was run twice.

| Check | Result | What was measured |
|---|---|---|
| M1 | Not run | Needs a clean x64 Windows 11 machine, which this environment lacks |
| M2 | Not run | Needs a person listening to Narrator or NVDA |
| M3 | Not run | Needs the contrast themes switched on, a system setting |
| M4 | Not run | Needs a real sign-out during a job |
| M5 | Not run | No FAT32 or exFAT drive or network share was available, and OneDrive was left alone so as not to sync test files |
| M6 | Not run | Airplane mode is a system setting |
| M7 | Not run | Needs clipboard sync with a signed-in account |
| M8 | Paint passed; Office not run | A 1920 × 1080 capture pasted into Paint as a 1920 × 1080 selection. Word and Excel here need a sign-in, so no Office application was tried |
| M9 | Pass | Clicks inside the HD frame reached the application beneath. The frame kept its place and size and stayed visible while a second application had the foreground and after a click back in the first. It was still the window under its border, above the application in front, when a border drag moved it by exactly (137, 61). With the pointer parked at the frame's centre, the image was 1280 × 720, with 0 of 921,600 pixels differing from the known pattern beneath |
| M10 | Pass | Each capture mode started from another focused application. A second press brought the toolbar forward with the focus, with no second selection |
| M11 | Not run | Needs a drive that fills up |
| M12 | **Fail** | Three drags of the window during a 100-image job, a number fixed before the runs. The script took "follows smoothly" to mean every pointer position reflected within 100 ms, a limit it set, as the check names none. In one drag, 6 of 240 positions were reflected after more than 100 ms, the worst at 153.3 ms. The other two stayed within 69 ms. The job ran throughout every drag |
| M13 | Pass | Listed above |
| M14 | Pass | Listed above |
| M15 | Pass | During a real draw the toolbar's fields held the live size at every step. The overlay was above the toolbar while the button was down, so the size showed through its tint. Moving, resizing, and a right-click reached nothing beneath. Sampled about every 2 ms, the overlay was never above the toolbar, while both were visible, once a drag or press had ended. No sample after the press on Capture, or after the Enter that pressed it, had both visible. A real click on the size field after the three drags, and on Capture after the right-click, reached the toolbar. Both 640 × 360 images differed from the pattern in 0 pixels: one captured by a click on Capture, with the pointer outside the rectangle, and one by Enter, with the pointer parked inside it. This was the second run, as described above |
| M16 | Pass | The real file picker added files. Files and a folder dropped from File Explorer were listed. Browse chose the output folder in each module, and Open Output Folder opened it. Open Folder opened the right folder with the saved file focused, but not highlighted, the first time Explorer showed that folder. A second press highlighted it. On candidate 1, `explorer /select` without the application did the same in 3 of 3 tries. With a file picker open, the shortcut captured nothing and the picker's title bar flashed. The project owner decided that the focused file meets the Open Folder condition (PRD 1.10) |
| M17 | Pass | From behind another application and from minimized, a second launch brought the window forward, and the second process exited |
| M18 | Pass | With the window minimized and with the Image Resizer showing: a saved capture, an unwritable folder, a held clipboard, and an oversized preset each reached the result card. The focus never left the other application |
| M19 | Pass | Each display captured at its native size, with 0 pixels differing from what that display showed, with the pointer on the other display |

M16's Open Folder condition reads "Explorer opens at the saved file". The script required the file to be highlighted after the first press, so its own result was a failure. The project owner decided that a focused file that is not highlighted meets the condition, as Explorer's own select command does the same (PRD 1.10), so M16 is recorded as passing SCR-069 with the check's other requirements.

## Defects found and fixed

Each defect below was already on `main`, was reproduced on a candidate, and has a test that failed before the fix.

| Defect | Found on | Fix |
|---|---|---|
| Dragging inside a Free Rectangle selection did not move it, and the press reached the application beneath | Candidate 1, every drag inside a selection: 4 of 4 in its recorded results | The interior pixel was alpha 0, which Windows hit-tests as transparent. It is now alpha 1 |
| Add Images never opened its picker, in any build | Candidate 1, every press: its recorded M16 run's press reported "invalid argument" | The filter held NUL characters, which the UTF-16 conversion always refused. Each string is now converted separately |
| After a real press on the Free Rectangle overlay, it covered the toolbar, and a click meant for Capture or a size field started a new selection | Candidate 2, 4 of 4 runs. Candidate 1's M15 run had also found the toolbar covered after its drags and brought it forward with the shortcut, as the guide says | The toolbar is put back in front of the overlay whenever it is placed, including after a right, middle, or side-button press |

Putting the toolbar back after a right, middle, or side-button press also moves it beside the selection, so such a press undoes a drag of the toolbar aside. Raising it without moving it needed an executable change, which REL-02C made on `main` after this candidate.

The global shortcut could also fire while one of the application's modal dialogs was open, which would put the dialog in the image. That was already on `main`, behind the Browse folder pickers and the application's yes-or-no questions. The working picker added one more such dialog, which is how REL-03 found it. The shortcut now captures nothing while a dialog of the application is open. Instead it flashes that dialog and beeps. While the application is minimized the dialog is hidden too, so only the beep can be noticed, as the user guide says. Flashing the taskbar button then instead needs an executable change. PRD 1.7 recorded this narrowing of SCR-002 as awaiting the project owner's confirmation, which PRD 1.10 records.

| Candidate | Commit | ZIP SHA-256 | Outcome |
|---|---|---|---|
| 1 | `6794d82` | `cda0323f…` | Gates passed. M15 and M16 failed on the first two defects |
| 2 | `be2d299` | `89bdd408…` | Gates passed. The third defect was found. M12 had one position at 104.2 ms in one of three runs |
| 3 | `c646471` | `bbcbdc13…` | This record |

Results on candidates 1 and 2 do not count for the release. On both, Open Folder left the saved file focused but not highlighted, as on this one. The first two x64 validation runs on candidate 1 failed only while deleting an x64 test executable that had just run. On this Arm machine the `XtaCache` service holds such a file for a moment after it exits: 80 of 80 probe runs could not delete one at once. The third run passed.

## Release blockers

A release needs each of these:

1. **The hardware runs QLT-04A owns:** x64 Windows 11, including the reference machine; display scaling above 100% and mixed scaling; negative coordinates; a rotated display; and a display disconnected during a selection. They hold SCR-020, SCR-026, SCR-042, SCR-043, SCR-054, NFR-001, NFR-003, TECH-002, TECH-003, TECH-009, AC-S02, and AC-S12, and the P1 SCR-028 and NFR-010 to NFR-013.
2. **M2**, keyboard and screen reader, which holds UX-006 and AC-A03.
3. **M8's Office half**, which holds SCR-062.
4. **M12**, which holds AC-A04. It failed on this candidate under emulation, and has to pass on x64 hardware. The check names no limit for following smoothly, so the run that passes it has to state the limit it held the drags to, as this one did. RSZ-041 names M12 only for added confidence and stays Pass, as [the matrix](traceability.md#status) explains.
5. **M1**, the clean-machine walkthrough, which REL-01 and REL-02 also wait on.
6. **The project owner's decisions** on the shortcut's dialog suspension, on which SCR-002's Pass rests, and on M16's Open Folder condition, which held the P1 SCR-069. **Met:** the project owner confirmed the suspension and decided that Open Folder meets the condition (PRD 1.10).
7. **A candidate with the fix for the dialog that could appear in a Full Display image**, the defect listed [below](#found-and-left-for-later), which held SCR-044. PRD section 20 counts the application's own dialog in the image against SCR-044. REL-02C fixed it on `main`, where SCR-044 is Pass on its automated checks alone: the runs recorded here passed SCR-044's checks on this candidate, which still has the defect, so they predate the fix. The fix changes the executable, so it holds for a release only on a candidate built with it, whose runs blocker 9 requires.
8. **Packaged documentation that matches the next candidate.** This candidate's release notes and user guide stay exactly as the runs used them, and do not match it. The next candidate's notes must add M12's result and reflect the owner's decisions about the dialog suspension and Open Folder (PRD 1.10), which they already do: the user guide describes the suspension, and the guide and notes list Open Folder's first-open behavior as a known limitation. The guide's keyboard reference says Enter captures after you draw a rectangle, which after a draw with the mouse holds only once the toolbar is clicked, as listed below. Its known limitations name neither the live size showing through the tint during the first draw nor a right, middle, or side-button press moving a toolbar dragged aside. Nor do they name the other defects [left for later](#found-and-left-for-later): the shortcut starting a capture during a keyboard move or size or with the system menu open, a second launch bringing forward a disabled window, a dialog in a Full Display image, and the activating press applying a typed size. The next candidate must fix each of these, or its guide must state it. REL-02C fixed each of them on `main` and updated the guide where the fixes change what the application does, so its Enter after a draw holds again; what remains for the next candidate's documentation is M12's result.
9. **Every recorded run repeated on the next candidate.** Blockers 7 and 8 change the ZIP, and a run counts only for the build it ran on, as [the test guide](testing.md#recorded-runs) says. The checklist's gates, the x64 validation, M8's Paint half, M9, M10, and M13 to M19 have to run again on the next candidate, and the rows those runs turned Pass hold for it only once its own runs are recorded against its commit.

M3 to M7 and M11 add confidence to requirements already covered and do not block on their own. UX-008 is deferred by PRD section 20.

## Found and left for later

These were on `main` and outside the fixes REL-03 made. REL-02C fixed each of them on `main` after this candidate, as the note after each says, so this candidate still has them. The third held SCR-044, so a release waits on a candidate with its fix, as blocker 7 says.

- The shortcut can start a capture during a keyboard move or size of the main window, or with its system menu open. Whether that can put the system menu in the image, or keep Escape from cancelling a selection, the reasons PRD section 20 gives for suspending the shortcut behind a dialog, was not checked. REL-02C: the shortcut now captures nothing and beeps during a move or size or with a menu open, as the project owner decided (PRD 1.9).
- A second launch brings forward a main window that a modal dialog has disabled. REL-02C: it brings forward that dialog instead.
- A dialog opened in the instant after a Full Display capture starts can appear in the image. Add Images can now reach this. REL-02C: the application's dialogs are refused with a beep from a capture's start until it has read the screen, and during a selection, as the project owner decided (PRD 1.9). A click or key press queued for the main window is dropped then, with a beep, so that none of its menus or lists opens either, and the shortcut closes a drop-down list that is open before it captures.
- A real press on the Free Rectangle overlay makes it the active window. Enter and Tab then do nothing in the toolbar until it is clicked, although the user guide's keyboard reference says Enter captures after you draw a rectangle. The press also applies a size typed in a field but not yet applied, as the field loses the focus before any drag starts. REL-02C: a press on the overlay or the preset frame no longer activates it, so the toolbar keeps the keyboard and the field keeps its typed size.
- The press that activates the Free Rectangle overlay raises it above the toolbar until the button is released, so during the first draw the live size shows through the overlay's tint. REL-02C: the press no longer activates the overlay, and every press puts the toolbar back in front as it arrives, without moving it.

REL-02C's review found two more on `main`, which this candidate also has, and REL-02D fixed them:

- An Open Folder or Open Output Folder pressed just before a capture can open File Explorer during the screen read, as each looks at the file or folder on a goroutine of its own before it calls the shell. REL-02D: Explorer is started only once the capture's hold on the dialogs has ended, and the file or folder is looked at again then. A window the shell had already been, or was being, asked for as the capture started can still open during the read, as Explorer opens it on its own time.
- A restore from the taskbar arriving in the instant after a capture hid the main window can show it before the screen is read. REL-02D: while a capture keeps the window hidden, a restore or maximize is refused with a beep and nothing else can show it, until the capture's own restore.

## Plan status

REL-03 marked no earlier plan item Done. Rule 6 requires each item's verification to pass. FND-01's mixed-DPI verification waited on QLT-04A's hardware, and every other item depends on FND-01. FND-01's own pull request, #1, ran before CI existed.

The project owner broke that cycle in plan 1.11. FND-01's mixed-DPI check moved to QLT-04A, which now owns the blocker in [ADR-0001](adr/0001-native-windows-stack.md), and rule 6 judges each item on its own, whatever its dependencies' status. 35 of the plan's 43 items are Done. FND-05 and QLT-01 wait on M2, QLT-01 also on M3, RSZ-09 and QLT-04 on M12, and REL-01 and REL-02 on M1. QLT-04A is Pending, and REL-03 stays Blocked by the list above.

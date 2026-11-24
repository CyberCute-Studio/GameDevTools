# Windows platform and combined performance verification

This document is the QLT-04 record for the supported Windows platforms, running without administrator rights, display scaling coverage, combined performance, and the combined-module regression run: NFR-001 through NFR-004, NFR-010 through NFR-014, the shared acceptance scenarios in [PRD section 15.3](../PRD.md#153-shared-acceptance), and the remaining P0 acceptance scenarios.

## Result

Every check that could run passed, and the combined run found no cross-module defect, so QLT-04 changes no production code. **One performance target was missed:** under x64 emulation, the slowest Free Rectangle overlay exceeded NFR-011's 300 ms in 2 of 6 batches of 30, at 312 and 317 ms ([details](#the-missed-overlay-target)). **These were not run, because this environment's hardware and display arrangement do not provide them, and they are QLT-04A's scope:** x64 Windows 11 on real hardware, including the PRD's reference machine; any other supported Windows 11 release; any display scaling other than 100%; negative virtual-desktop coordinates; a rotated display; and a live display disconnect. Until QLT-04A passes, SCR-020, SCR-026, SCR-028, SCR-042 on a rotated display, SCR-043, SCR-054, NFR-001, NFR-003, NFR-011, TECH-002, TECH-003, TECH-009, AC-S02, and AC-S12 are not fully verified, and the NFR-010, NFR-012, and NFR-013 figures below are indicative rather than results on the reference machine.

## Support policy

The initial release supports 64-bit **Windows 11** only, in the releases Microsoft currently supports (NFR-001, PRD 1.4, section 20). Windows 10 is not supported: Microsoft ended its general support in October 2025. The application does not refuse to start there, and no test covers it. Code that guards against how Windows 10 reads a path, such as treating `NUL.png` as a device, stays in place because it is harmless on Windows 11 and refuses an unsafe write for anyone who runs the application on Windows 10 anyway. The `con.d` missing-parent defect QLT-03B left for a Windows 10 run is closed by this decision, as it does not reproduce on Windows 11.

## How to run the validation

From the repository root, in an interactive desktop session, with CyberCute Dev Tools closed and nothing else using the desktop:

```powershell
./scripts/validate.ps1
```

The script moves windows, takes the foreground, and **replaces the clipboard**. It records the machine and its displays, runs `go vet`, the whole test suite, the release executable end to end, and the release build, measures NFR-010 through NFR-014, and writes one JSON file to `bin/validation`. The file holds no computer, user, or monitor name.

- `-Architecture amd64` builds and runs the x64 release target, under emulation on Windows on Arm.
- `-ReferenceMachine` asserts this is the PRD's reference machine and refuses one that is not: 64-bit Intel or AMD by its processor, four or more cores, 16 GB RAM installed, Windows 11, not elevated, and an SSD shown to hold both the system drive and the temporary files the benchmarks use. Windows does not report a CPU's age, so whoever passes the switch confirms the CPU is from 2020 or later; the result file records its name. A target missed or not measured then fails the run. Elsewhere, performance figures are recorded without failing it.

A benchmark that reports it cannot run on a machine, such as the 1280 × 720 preset on a smaller desktop, is recorded as skipped and its target as not measured. A benchmark that produces no result and reports no skip, such as one that was renamed, fails the run. A capture region the test could not check is listed as skipped. If no region could be checked, the coverage check reads skipped rather than passed, and a `-ReferenceMachine` run fails if any region was skipped.

The result file names the commit and working-tree state from before the first step, and the run fails if either changes before the last, including the content of a file that was already changed or untracked. That check leaves out the output folder, so `-OutputDirectory` refuses a folder holding tracked files.

The `Validation matrix` workflow (`.github/workflows/validation-matrix.yml`) runs the same script on x64 Windows Server. It is started by hand, or by a pull request that changes the script, the workflow, the build script, the Go module files, or a file holding a test or benchmark the script runs by name. A change to any other test file does not start it, although the script runs the whole suite.

## Environment of record

Results below were recorded on 2026-11-22 from commit `8c06406`, with a clean working tree:

| Property | Value |
|---|---|
| OS | Windows 11 Home 26H2, build 26300.9974 (ARM64) |
| Device | HP OmniBook 5 Laptop 14-he0xxx, Snapdragon X Plus X1P42100, 8 cores, 32 GB RAM |
| Go | go1.27.4, the release toolchain `go.mod` pins |
| Rights | Standard, not elevated |
| Displays | 3440 × 1440 primary at (0, 0) and 1920 × 1200 at (3440, 623), both at 100% scaling |

## Windows matrix

| Configuration | Status |
|---|---|
| Windows 11 26H2 on ARM64, native `windows/arm64` | **Passed.** The development host; Windows on Arm is not a release target (NFR-004) |
| Windows 11 26H2 on ARM64, `windows/amd64` under x64 emulation | **Passed.** The release target's code, but not on x64 hardware |
| x64 Windows 11, on the reference machine | **Not run** (QLT-04A) |
| Each other Windows 11 release Microsoft supports | **Not run** (QLT-04A) |
| x64 Windows Server 2025 and 2022, GitHub-hosted | **Passed** on the release candidate `c646471` ([run 45341616501](https://github.com/CyberCute-Studio/GameDevTools/actions/runs/45341616501)), as the [release candidate record](release-candidate.md) says. Not supported targets: they exercise the x64 executable and the suite, and do not stand in for Windows 11 |
| Windows 10 22H2 | **Not supported** |

## Running without administrator rights

`app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` builds the executable with the release flags and runs it with its per-user folders in a temporary workspace. It confirms the process has no enabled Administrators group, its window appears and answers, a second launch exits and leaves the first window in place (APP-005), its settings are written, it exits with code 0 and releases the single-instance mutex (AC-A05), and, with the file then changed to select the other module and a custom preset, it restarts and keeps both, with no recovery or temporary file (AC-A01). The runs recorded below, from `8c06406`, predate the check that those changed values are kept. On an elevated host, such as a GitHub-hosted runner, the test is written to drop first to a restricted, medium-integrity token. This machine's runs were not elevated. The hosted `Validation matrix` [run 45341616501](https://github.com/CyberCute-Studio/GameDevTools/actions/runs/45341616501) on the release candidate ran that path on two elevated runners, and the test passed on both.

## Performance

Each figure comes from the validation script's benchmarks on the environment of record. **This is not the PRD's reference machine**, so these figures indicate the margin rather than establish the target.

| Requirement | Measure | Target | Native ARM64 | x64 under emulation |
|---|---|---|---|---|
| NFR-010 | Warm launch to a responsive main window, mean of 20 | 2 s | 176.7 ms | 262.1 ms |
| NFR-010 | Warm launch, slowest of 20 | 2 s | 210.3 ms | 769.8 ms |
| NFR-011 | Free Rectangle overlay appears, 5360 × 1823 desktop, mean of 30 | 300 ms | 93 ms | 166.7 ms |
| NFR-011 | Free Rectangle overlay appears, slowest of 30 | 300 ms | 151.9 ms | **317.2 ms (missed)** |
| NFR-011 | 1280 × 720 Preset Rectangle frame appears, mean of 30 | 300 ms | 21.5 ms | 33.2 ms |
| NFR-011 | 1280 × 720 Preset Rectangle frame appears, slowest of 30 | 300 ms | 48.6 ms | 57 ms |
| NFR-012 | 3840 × 2160 capture to clipboard | 1 s | 131 ms | 195.9 ms |
| NFR-013 | 100 mixed 12 MP JPEGs to 1024 × 1024, slowest of 3 | 120 s | 7.6 s | 22.9 s |
| NFR-014 | Peak heap during that batch | 1 GiB budget | 220.2 MiB | 220.3 MiB |
| NFR-014 | Peak runtime footprint during that batch | — | 244.2 MiB | 260.5 MiB |

Launch is timed from just before the process is created until its main window is visible and its message loop answers, after an untimed first launch. NFR-012 adds the clipboard benchmark at 3840 × 2160 to the capture benchmark's time, scaled to that region by the pixels the displays hold, 7.3 MP of the 9.8 MP virtual desktop here, because parts no display covers are cheaper to read. A desktop whose displays hold more than 3840 × 2160 is not scaled down, so the estimate can only err high.

### The missed overlay target

The x64 build's slowest Free Rectangle overlay took 317.2 ms in the full run, over NFR-011's 300 ms. Five more batches of 30 at the same commit, run to see how often this happens, gave a slowest overlay of 250.2, 312.0, 245.0, 252.8, and 264.7 ms as x64, so 2 of the 6 x64 batches went over the limit, while each batch's mean stayed between 130 and 167 ms. Natively, the slowest overlay of each of the six batches was 152 to 232 ms, with none over the limit.

These runs were under x64 emulation on a 9.8 MP virtual desktop, on a machine that is not the reference machine, so whether x64 hardware misses the target is not known. QLT-04A must measure it there. No production code was changed for it, and the target was not changed.

### Variation between runs

Across the four full runs of each architecture on 2026-11-22, at `aabff6e`, `e1a6bc3`, `54fdff4`, and `8c06406`, mean launch ranged from 163 to 268 ms natively and 195 to 395 ms as x64, the mean Free Rectangle overlay from 63 to 104 ms natively and 70 to 167 ms as x64, and the batch from 7.5 to 11.4 s natively and 10.8 to 22.9 s as x64. Slowest-of-30 overlay figures exist only from `54fdff4` on.

## Display matrix

| Configuration | Status |
|---|---|
| Two displays at 100%, side by side, capture on each and across their edge | **Passed live** |
| 125%, 150%, 175%, 200%, and up to 500% | **Not run** (QLT-04A); conversion covered by unit tests |
| Mixed scaling, including a frame moved across the boundary (AC-S02) | **Not run** (QLT-04A); conversion covered by unit tests |
| Negative virtual-desktop coordinates | **Not run** (QLT-04A); geometry covered by unit tests |
| Rotated display | **Not run** (QLT-04A) |
| Live disconnect and reconnect during a selection (AC-S12) | **Not run** (QLT-04A); simulated topology changes covered by tests |

`capture.TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries` captures a 1280 × 720 region on every connected display and across every shared edge, over four windows of known colors that meet at the region's center, and checks the size and the pixels on both sides of each seam, so a displaced or scaled capture fails. Only the validation script runs it, one package at a time, because an overlay test's full-desktop tint running beside it would change the pixels; a plain `go test ./...` skips it. The result file's `displays.captureCoverage` lists each region it checked, with the displays involved, their scaling, and whether it passed or was skipped, so a QLT-04A run on other hardware reports what it covered without changing the test.

## Results by requirement

The rows that waited on a manual check give the result of REL-03's run of it on the release candidate `c646471`, as the [release candidate record](release-candidate.md#manual-checks) says.

| Requirement | Result | Evidence |
|---|---|---|
| NFR-001 — Windows 11, 64-bit | **Partial** | Passed on Windows 11 ARM64, native and x64 under emulation; x64 hardware is QLT-04A |
| NFR-002 — No administrator rights | Pass | The release executable test, on a standard-user token |
| NFR-003 — Per-monitor DPI, 100–500%, negative coordinates, disconnect | **Partial** | Mechanism-level tests and live 100%; the rest is QLT-04A |
| NFR-004 — Arm and 32-bit are not targets | Pass | The release target is `windows/amd64`; Arm is only the development host |
| NFR-010, NFR-012, NFR-013 — Performance | Met here; **reference machine pending** | [Performance](#performance) |
| NFR-011 — Overlay within 300 ms | **Missed in 2 of 6 x64 batches under emulation**; met natively; reference machine pending | [The missed overlay target](#the-missed-overlay-target) |
| NFR-014 — Bounded batch memory | Pass | Peak heap 220 MiB, about 7% of the 3,094 MiB that decoding every source at once would take |
| AC-A01 — Persistence | Pass | [Reliability record](reliability-verification.md) and the release executable test |
| AC-A02 — Offline use | Pass | `app.TestCoreWorkflowsRunOffline`, [privacy record](privacy-verification.md) |
| AC-A03 — Keyboard access | **Partial** | [Accessibility record](accessibility-verification.md); that the focus is visible is manual check [M2](testing.md#manual-pre-release-checks), which has not run |
| AC-A04 — Responsive batch UI | **Partial** | [Image Resizer record](image-resizer-verification.md); a real drag during a job is manual check [M12](testing.md#manual-pre-release-checks), which failed on the release candidate, under emulation, in 1 of 3 drags |
| AC-A05 — Clean exit | Pass | The exit matrix in the [reliability record](reliability-verification.md) and the release executable test |
| AC-S07, AC-S10 | Pass | [Screenshot record](screenshot-verification.md), in this run's suite |
| AC-S01 — Exact preset at standard scaling | Pass | [Screenshot record](screenshot-verification.md); checking a real preset capture's image for the frame and toolbar is manual check [M9](testing.md#manual-pre-release-checks), which passed |
| AC-S06 — Full secondary display | Pass | [Screenshot record](screenshot-verification.md); that the image holds only the selected display's contents is manual check [M19](testing.md#manual-pre-release-checks), which passed |
| AC-S11 — Shortcut conflict | Pass | [Screenshot record](screenshot-verification.md) |
| AC-S08, AC-S09 — Independent output failures | Pass | [Screenshot record](screenshot-verification.md); the result card on the release candidate is manual check [M18](testing.md#manual-pre-release-checks), which passed |
| AC-S03 — Click-through interaction | Pass | [Screenshot record](screenshot-verification.md); a real click through the production frame is manual check [M9](testing.md#manual-pre-release-checks), which passed |
| AC-S04 — Precision placement | Pass | [Screenshot record](screenshot-verification.md); a real drag of the production frame's border is manual check [M9](testing.md#manual-pre-release-checks), which passed |
| AC-S05 — Free rectangle adjustment | Pass | [Screenshot record](screenshot-verification.md); drawing a selection with a real pointer is manual check [M15](testing.md#manual-pre-release-checks), which passed |
| AC-S02, AC-S12 | **Mechanism level; live run is QLT-04A** | [Screenshot record](screenshot-verification.md#hardware-gated-scenarios) |
| AC-R01 to AC-R15 | Pass | [Image Resizer record](image-resizer-verification.md), in this run's suite |

## What these results do not cover

- **x64 hardware.** The x64 build ran only under emulation on this machine. The hosted Windows Server runs passed on the release candidate, but Windows Server does not stand in for Windows 11.
- **Other Windows 11 releases.** Only build 26300 was run.
- **Any scaling but 100%.** Mixed-DPI behavior has still never been exercised live, which is the blocker [ADR-0001](adr/0001-native-windows-stack.md) recorded.
- **Other display arrangements.** No display had negative coordinates or was rotated, and none was disconnected during a selection.
- **Other drives.** Every result was written to the internal NVMe SSD.
- **Manual pre-release checks** recorded by earlier items remain open, including a screen reader, the contrast themes, a real sign-out, FAT32 or exFAT saves, a run with network adapters off, and clipboard sync.

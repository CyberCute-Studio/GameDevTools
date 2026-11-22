# Windows platform and combined performance verification

This document is the QLT-04 record for the supported Windows platforms, running without administrator rights, display scaling coverage, combined performance, and the combined-module regression run: NFR-001 through NFR-004, NFR-010 through NFR-014, the shared acceptance scenarios in [PRD section 15.3](../PRD.md#153-shared-acceptance), and the remaining P0 acceptance scenarios.

## Result

Every check that could run passed, and the combined run found no cross-module defect, so QLT-04 changes no production code. **Three things were not run, because this environment does not have them, and they are QLT-04A's scope:** x64 Windows 11 on real hardware, including the PRD's reference machine; any display scaling other than 100%; and a live display disconnect. Until QLT-04A passes, NFR-001, NFR-003, AC-S02, and AC-S12 are not fully verified, and the NFR-010 through NFR-013 figures below are indicative rather than results on the reference machine.

## Support policy

The initial release supports 64-bit **Windows 11** only, in the releases Microsoft currently supports (NFR-001, PRD 1.4, section 20). Windows 10 is not supported: Microsoft ended its general support in October 2025. The application does not refuse to start there, and no test covers it. Code that guards against how Windows 10 reads a path, such as treating `NUL.png` as a device, stays in place because it is harmless on Windows 11 and refuses an unsafe write for anyone who runs the application on Windows 10 anyway. The `con.d` missing-parent defect QLT-03B left for a Windows 10 run is closed by this decision, as it does not reproduce on Windows 11.

## How to run the validation

From the repository root, in an interactive desktop session, with GameDevTools closed and nothing else using the desktop:

```powershell
./scripts/validate.ps1
```

The script moves windows, takes the foreground, and **replaces the clipboard**. It records the machine and its displays, runs `go vet`, the whole test suite, the release executable end to end, and the release build, measures NFR-010 through NFR-014, and writes one JSON file to `bin/validation`. The file holds no computer, user, or monitor name.

- `-Architecture amd64` builds and runs the x64 release target, under emulation on Windows on Arm.
- `-ReferenceMachine` asserts this is the PRD's reference machine and refuses one that is not: 64-bit Intel or AMD by its processor, four or more cores, 16 GB RAM installed, Windows 11, not elevated, and not on a hard disk. Windows does not report a CPU's age, so whoever passes the switch confirms the CPU is from 2020 or later; the result file records its name. A target missed or not measured then fails the run. Elsewhere, performance figures are recorded without failing it.

The result file names the commit and working-tree state from before the first step, and the run fails if either changes before the last.

The `Validation matrix` workflow (`.github/workflows/validation-matrix.yml`) runs the same script on x64 Windows Server. It is started by hand, or by a pull request that changes the validation or the tests and benchmarks it runs.

## Environment of record

Results below were recorded on 2026-11-22 from commit `e1a6bc3`, with a clean working tree:

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
| x64 Windows Server 2025 and 2022, GitHub-hosted | **Not run yet.** The `Validation matrix` workflow is written to run them and has not run. Not supported targets: they exercise the x64 executable and the suite, and do not stand in for Windows 11 |
| Windows 10 22H2 | **Not supported** |

## Running without administrator rights

`app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` builds the executable with the release flags and runs it with its per-user folders in a temporary workspace. It confirms the process has no enabled Administrators group, its window appears and answers, a second launch exits and leaves the first window in place (APP-005), its settings are written, it exits with code 0 and releases the single-instance mutex (AC-A05), and it restarts from the file it wrote with no recovery or temporary file (AC-A01). On an elevated host, such as a GitHub-hosted runner, the test is written to drop first to a restricted, medium-integrity token; that path has not run yet, as this machine's runs were not elevated.

## Performance

Each figure comes from the validation script's benchmarks on the environment of record. **This is not the PRD's reference machine**, so these figures indicate the margin rather than establish the target.

| Requirement | Measure | Target | Native ARM64 | x64 under emulation |
|---|---|---|---|---|
| NFR-010 | Warm launch to a responsive main window, mean of 20 | 2 s | 163 ms | 195 ms |
| NFR-010 | Warm launch, slowest of 20 | 2 s | 235 ms | 245 ms |
| NFR-011 | Free Rectangle overlay appears, 5360 × 1823 desktop, mean of 30 | 300 ms | 63 ms | 70 ms |
| NFR-011 | 1280 × 720 Preset Rectangle frame appears, mean of 30 | 300 ms | 18 ms | 31 ms |
| NFR-012 | 3840 × 2160 capture to clipboard | 1 s | 108 ms | 128 ms |
| NFR-013 | 100 mixed 12 MP JPEGs to 1024 × 1024, slowest of 3 | 120 s | 7.5 s | 10.8 s |
| NFR-014 | Peak heap during that batch | 1 GiB budget | 220 MiB | 220 MiB |
| NFR-014 | Peak runtime footprint during that batch | — | 246 MiB | 237 MiB |

Launch is timed from just before the process is created until its main window is visible and its message loop answers, after an untimed first launch. NFR-012 adds the clipboard benchmark at 3840 × 2160 to the capture benchmark's throughput converted to that region's size.

Figures vary between runs. An earlier run of the same code, minutes before, measured native launch at 268 ms and the native batch at 11.4 s, and the x64 batch at 20.3 s. Every run met every target.

The figures above are from the runs at `e1a6bc3`. The script has since gained a slowest-of-30 target for each overlay, which those runs did not measure, so this table has no figure for it; a QLT-04A run records it.

## Display matrix

| Configuration | Status |
|---|---|
| Two displays at 100%, side by side, capture on each and across their edge | **Passed live** |
| 125%, 150%, 175%, 200%, and up to 500% | **Not run** (QLT-04A); conversion covered by unit tests |
| Mixed scaling, including a frame moved across the boundary (AC-S02) | **Not run** (QLT-04A); conversion covered by unit tests |
| Negative virtual-desktop coordinates | **Not run** (QLT-04A); geometry covered by unit tests |
| Rotated display | **Not run** (QLT-04A) |
| Live disconnect and reconnect during a selection (AC-S12) | **Not run** (QLT-04A); simulated topology changes covered by tests |

`capture.TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries` captures a 1280 × 720 region on every connected display and across every shared edge, over four windows of known colors that meet at the region's center, and checks the size and the pixels on both sides of each seam, so a displaced or scaled capture fails. It logs each display's scaling, and the result file records the layout, so a QLT-04A run on other hardware reports what it covered without changing the test.

## Results by requirement

| Requirement | Result | Evidence |
|---|---|---|
| NFR-001 — Windows 11, 64-bit | **Partial** | Passed on Windows 11 ARM64, native and x64 under emulation; x64 hardware is QLT-04A |
| NFR-002 — No administrator rights | Pass | The release executable test, on a standard-user token |
| NFR-003 — Per-monitor DPI, 100–500%, negative coordinates, disconnect | **Partial** | Mechanism-level tests and live 100%; the rest is QLT-04A |
| NFR-004 — Arm and 32-bit are not targets | Pass | The release target is `windows/amd64`; Arm is only the development host |
| NFR-010 to NFR-013 — Performance | Met here; **reference machine pending** | [Performance](#performance) |
| NFR-014 — Bounded batch memory | Pass | Peak heap 220 MiB, about 7% of the 3,094 MiB that decoding every source at once would take |
| AC-A01 — Persistence | Pass | [Reliability record](reliability-verification.md) and the release executable test |
| AC-A02 — Offline use | Pass | `app.TestCoreWorkflowsRunOffline`, [privacy record](privacy-verification.md) |
| AC-A03 — Keyboard access | Pass | [Accessibility record](accessibility-verification.md) |
| AC-A04 — Responsive batch UI | Pass | [Image Resizer record](image-resizer-verification.md) |
| AC-A05 — Clean exit | Pass | The exit matrix in the [reliability record](reliability-verification.md) and the release executable test |
| AC-S01, AC-S03 to AC-S11 | Pass | [Screenshot record](screenshot-verification.md), in this run's suite |
| AC-S02, AC-S12 | **Mechanism level; live run is QLT-04A** | [Screenshot record](screenshot-verification.md#hardware-gated-scenarios) |
| AC-R01 to AC-R15 | Pass | [Image Resizer record](image-resizer-verification.md), in this run's suite |

## What these results do not cover

- **x64 hardware.** The x64 build ran only under emulation on this machine. The hosted Windows Server runs have not happened yet.
- **Other Windows 11 releases.** Only build 26300 was run.
- **Any scaling but 100%.** Mixed-DPI behavior has still never been exercised live, which is the blocker [ADR-0001](adr/0001-native-windows-stack.md) recorded.
- **Other drives.** Every result was written to the internal NVMe SSD.
- **Manual pre-release checks** recorded by earlier items remain open, including a screen reader, the contrast themes, a real sign-out, FAT32 or exFAT saves, a run with network adapters off, and clipboard sync.

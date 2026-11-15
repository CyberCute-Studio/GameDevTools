# Screenshot verification and acceptance results

This document is the SCR-11 verification record for the Screenshot module: the repeatable test matrix for the Screenshot acceptance criteria in [PRD section 15.1](../PRD.md#151-screenshot-acceptance), the performance measurements for NFR-011 and NFR-012, the environment the results were recorded on, and the scenarios that remain gated on unavailable display hardware.

## How to run the suite

From the repository root on Windows:

```bash
go fmt ./...
go vet ./...
go test -count=1 ./...
```

`go test` runs the whole matrix below: the Windows integration tests create real overlay windows, capture real screen content, and exercise the real session-global clipboard. Run the suite in an interactive desktop session; a locked or headless session composites no display, and the capture-content tests skip there.

The performance benchmarks are on-demand rather than part of the default run, because timing assertions are machine-dependent:

```bash
go test -run '^$' -bench . -benchtime=30x ./internal/screenshot/overlay ./internal/screenshot/capture ./internal/screenshot/output
```

Windows CI (`.github/workflows/ci.yml`) runs the formatting, static-analysis, and test commands on every push and pull request and builds the AMD64 release executable. The same test commands pass there; CI desktops are smaller than the environment of record, and tests adapt to the available virtual desktop.

## Environment of record

Results below were recorded on 2026-11-15:

| Property | Value |
|---|---|
| OS | Windows 11 25H2, build 26200 (ARM64) |
| Device | HP OmniBook 5 Laptop 14, Snapdragon X Elite, Qualcomm Adreno X1-45 GPU, 32 GB RAM, NVMe SSD |
| Go | go1.26.5 windows/arm64 (test binaries run natively; the release build targets AMD64 per CI) |
| Display 1 (primary) | `\\.\DISPLAY2`, 3440 × 1440 physical pixels at (0, 0), 100% scaling (96 DPI) |
| Display 2 | `\\.\DISPLAY1`, 1920 × 1200 physical pixels at (3440, 623), 100% scaling (96 DPI) |
| Virtual desktop | 5360 × 1823 physical pixels, origin (0, 0) |

Both displays run at 100% scaling. Mixed-scaling, negative-coordinate, and rotated-display configurations were not available; see [Hardware-gated scenarios](#hardware-gated-scenarios).

## Acceptance matrix

Every scenario passes on the environment of record, verified by three consecutive clean `go test -count=1 ./...` runs. "Hardware-gated" marks scenarios whose live validation needs display equipment this environment does not have; their mechanism-level coverage is listed, and the gap is tracked under the FND-01 blocker in [ADR-0001](adr/0001-native-windows-stack.md).

| Scenario | Automated coverage | Result |
|---|---|---|
| AC-S01 — Exact preset at standard scaling | `overlay.TestPresetFrameExactSizeAndSurface`, `overlay.TestPresetFrameCaptureDeliversExactRect`, `capture.TestCaptureRectDimensionsAndContent` (real composited pixel content at 100% scaling) | Pass |
| AC-S02 — Exact preset at mixed DPI | `display.TestLogicalPhysicalConversion`, `display.TestRectConversion`, `overlay.TestDisplayChangeResolvesScalingOnUnchangedTopology`; live mixed-DPI capture is hardware-gated | Pass at mechanism level; live validation gated |
| AC-S03 — Click-through interaction | `overlay.TestPresetFrameExactSizeAndSurface` (alpha-zero click-through interior, opaque drag border), `overlay.TestPresetFrameClickOnBorderLeavesSelectionUnchanged`; live click-through into another application was validated in the FND-01 spike | Pass |
| AC-S04 — Precision placement | `overlay.TestPresetFrameBorderDragMovesWithoutResizing`, `overlay.TestPresetFrameKeyboardNudges` (one-pixel and ten-pixel nudges, exact dimensions preserved) | Pass |
| AC-S05 — Free rectangle adjustment | `overlay.TestOverlayDragDrawsAndCapturesSelection`, `overlay.TestOverlayDimensionEditingAppliesExactSize`, `overlay.TestOverlayMoveAndResizeAdjustSelection`, `overlay.TestSizedSelectionAppliesTypedDimensions` | Pass |
| AC-S06 — Full secondary display | `capture.TestCaptureDisplay` (captures every connected display at its native physical dimensions, including the 1920 × 1200 secondary), `screenshot.TestNativeViewFullDisplayCaptureSucceeds` | Pass |
| AC-S07 — Cancellation | `overlay.TestOverlayEscapeCancelsWithoutOutput`, `overlay.TestPresetFrameEscapeCancelsWithoutOutput`, `screenshot.TestNativeViewRectangleCancelProducesNoOutput`, `overlay.TestToolbarCancelCommandCancelsSession` | Pass |
| AC-S08 — Clipboard success with save failure | `output.TestDeliverSaveFailureKeepsClipboardSuccess` (unwritable destination), `execute.TestFullDisplayReportsIndependentDeliveryOutcomes` | Pass |
| AC-S09 — Save success with clipboard failure | `output.TestDeliverClipboardFailureKeepsSaveSuccess` (clipboard held by another window), `execute.TestRectangleReportsIndependentDeliveryOutcomes` | Pass |
| AC-S10 — Filename collision | `output.TestSaveAppendsNumericSuffixOnCollision`, `output.TestSaveConsecutiveSavesProduceDistinctFiles` | Pass |
| AC-S11 — Shortcut conflict | `screenshot.TestHotkeyConflictKeepsPreviousRegistration`, `screenshot.TestNativeViewShortcutConflictKeepsOldShortcut`, `screenshot.TestNativeViewStartupConflictIsReported` | Pass |
| AC-S12 — Monitor removal | `overlay.TestDisplayChangeResizesFreeOverlayToNewDesktop`, `overlay.TestDisplayChangeKeepsPresetFrameInsideNewDesktop`, `overlay.TestDisplayChangeDestroysSessionWindows`, `overlay.TestDisplayChangeEndsSessionThatCannotFit`, `screenshot.TestNativeViewDisplayChangeEndsStaleSession` (simulated topology changes); live disconnect/reconnect is hardware-gated | Pass at mechanism level; live validation gated |
| SCR-031 — Oversized rectangle blocked | `overlay.TestBeginPresetValidatesFrameSize`, `screenshot.TestNativeViewPresetRectangleTooLargeBlocked` | Pass |
| TECH-009 — Integration coverage | Clipboard (`output`), global hotkeys (`screenshot` hotkey and view tests), overlays (`overlay`), mixed-DPI conversion (`display`) | Pass |

Cross-monitor capture is covered live by `capture.TestCaptureSpanningDisplayBoundary` on the two-display environment of record, and by virtual-desktop geometry tests for arrangements the hardware cannot provide.

## Performance results

Measured on the environment of record with `go test -run '^$' -bench . -benchtime=30x` on the packages listed above. NFR targets are defined against the PRD's reference machine (2020-or-newer quad-core, 16 GB RAM, SSD, Windows 11); these measurements comfortably meet them on the environment of record and exist so regressions can be re-measured anywhere.

| Benchmark | What it measures | Target | Measured |
|---|---|---|---|
| `overlay.BenchmarkOverlayAppearFreeRectangle` | NFR-011: session start to tint overlay and toolbar on screen, full 5360 × 1823 virtual desktop | ≤ 300 ms | ~58 ms |
| `overlay.BenchmarkOverlayAppearPresetRectangle` | NFR-011: session start to a 1280 × 720 exact preset frame and toolbar on screen | ≤ 300 ms | ~18 ms |
| `capture.BenchmarkCaptureRectVirtualDesktop` | NFR-012 capture half: reading 5360 × 1823 (9.77 MP, larger than 4K) of physical pixels off the screen | — | ~68 ms (~571 MB/s) |
| `output.BenchmarkSetClipboardImage4K` | NFR-012 clipboard half: placing an exact 3840 × 2160 image on the clipboard as CF_DIB, including BGRA conversion | — | ~47 ms (~707 MB/s) |

NFR-012 allows one second for capture-to-clipboard of a 3840 × 2160 region excluding disk saving. The two halves sum to roughly 115 ms on hardware whose capture region is larger than the requirement's, so the target is met with margin. Disk saving is reported separately per NFR-012 and is covered functionally by the `output` package tests.

## Hardware-gated scenarios

The following acceptance behavior cannot be exercised live on the environment of record, continuing the FND-01 mixed-DPI validation blocker recorded in [ADR-0001](adr/0001-native-windows-stack.md). Each has mechanism-level automated coverage as listed in the matrix; live validation requires:

- **Display scaling at 125%, 150%, 175%, and 200%.** Capture a preset rectangle and a full display at each scaling level and confirm exact physical-pixel output dimensions with no scaling drift (AC-S02, SCR-042). The environment of record only offers 100%.
- **Mixed-DPI arrangements.** Move a preset frame across a DPI boundary and confirm its dimensions never change and no DPI-related drift appears (AC-S02, SCR-026).
- **Negative virtual-desktop coordinates.** Arrange a secondary display left of or above the primary and capture on it (SCR-028, SCR-043). The display service and overlay geometry handle negative coordinates in unit tests.
- **Rotated displays.** Capture a portrait-orientation display at its native dimensions (SCR-042).
- **Live monitor disconnect and reconnect during a selection.** Disconnect the display under an active selection and confirm the session ends with an explanation and no invisible input-capturing surface remains (AC-S12); reconnect and confirm a new session can start.

When suitable hardware is available, run the full suite and benchmarks above on it, update the environment table and results, and close out the corresponding rows.

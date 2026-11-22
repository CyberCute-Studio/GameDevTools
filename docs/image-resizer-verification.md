# Image Resizer verification and acceptance results

This document is the RSZ-09 verification record for the Image Resizer module: the repeatable acceptance suite for the Image Resizer scenarios in [PRD section 15.2](../PRD.md#152-image-resizer-acceptance) and the responsive-batch scenario AC-A04 from [section 15.3](../PRD.md#153-shared-acceptance), the fixtures it generates, the NFR-013 reference benchmark and the NFR-014 memory observations, the environment the results were recorded on, and what the results do not cover.

## How to run the suite

From the repository root on Windows:

```bash
go fmt ./...
go vet ./...
go test -count=1 ./...
```

`go test` runs the whole acceptance matrix below. To run only the end-to-end acceptance scenarios and the window-level ones:

```bash
go test -count=1 -v -run 'TestAcceptance|TestNativeViewStaysResponsiveDuringARealJob' ./internal/imageresizer
go test -count=1 -v -run TestNativeManagerScreenshotPresetAbsentFromImageResizer ./internal/preset
```

The window-level tests create real Win32 windows and controls, so run them in an interactive desktop session, as the Screenshot suite is run. AC-R14 changes the access control list of a temporary folder it creates, so it needs an NTFS temporary folder, as every Windows default is, and skips, saying so, on a temporary folder that has no access control list, such as one on a FAT32 or exFAT drive.

The suite verifies the Windows application. Outside Windows, where the application does not run, the tests of `acceptance_test.go` run too, but AC-R10's confirmed Overwrite skips, as replacing a file needs its identity, which only Windows lets the planner read; AC-R14 and the window-level tests are Windows-only.

The performance benchmarks are on-demand rather than part of the default run, because their timings are machine-dependent:

```bash
go test -run '^$' -bench BenchmarkRun100Mixed12MegapixelJPEGsFit1024 -benchtime=1x -count=3 ./internal/imageresizer/job
go test -run '^$' -bench BenchmarkResize12MegapixelJPEGFit1024 -benchtime=10x ./internal/imageresizer/resize
```

The first writes its 100 reference sources, 370 MiB, to a temporary folder and takes about four seconds to generate and check them before its timer starts.

Windows CI (`.github/workflows/ci.yml`) runs the formatting, static-analysis, and test commands on pushes to main and on every pull request, so the acceptance suite gates every merge; the benchmarks are not run there.

## How the acceptance suite works

`internal/imageresizer/acceptance_test.go` runs every scenario a job decides, AC-R01 through AC-R13, and `acceptance_windows_test.go` runs AC-R14, which changes a folder's Windows access control list, the way the view runs a job once Start Resize is pressed, without the window:

1. The scenario's fixtures are written to disk.
2. They are added to a source list (`Inventory.Add`) and each supported file's content is checked by the check the view's background inspector runs: `decode.Inspect`, behind the inspector's panic guard.
3. The list, the preset, and the options are checked by the view's own Start Resize checks (`jobRefusal`, `jobTarget`, `jobBackground`), which must let the job start, and the request is built by its own `newJobStart`; the sources whose content check failed are checked again first by the view's own pre-job check (`recheckInputs`, RSZ-049), and those checks are applied to the list. Only what needs the window — applying the JPEG fields' text and confirming an Overwrite job — stays in the view.
4. `job.Run` resizes every listed source with the real decoder, resizer, encoder, and writer into a real output folder.
5. Every result is decoded back from disk and checked — dimensions, pixels, alpha, format — and every item's final status is applied to the source list, so the row and detail text the view would show are checked too.

What stays in the window — the Overwrite confirmation, the refusals before a job starts, the Stretch distortion warning, Cancel Resize, the window staying usable — is pinned by the view tests listed with each scenario, which drive the real view's controls; `TestNativeViewStaysResponsiveDuringARealJob` does so with a real job.

### Fixtures

The fixtures are generated in code from fixed parameters (`internal/imageresizer/fixtures_test.go`), so every run writes the same bytes on any machine and no binary image is checked in:

| Fixture | What it is | Used by |
|---|---|---|
| `fixtureCropMarks` | 1000 × 500: a green middle half between red and blue quarters, crossed by ten-pixel black stripes at x 300 and 495 and y 50 and 245, placed so a centered, aspect-preserving crop lands them on output columns and rows 56 and 256 | AC-R01, AC-R02, AC-R04 |
| `fixtureQuadrants` | Red, green, blue, and white quadrants at any size, which tell every mirror and rotation apart | AC-R03, AC-R05–AC-R07, AC-R09, AC-R10, AC-R12–AC-R14, AC-A04 |
| `fixtureFill` | One color at any size: the red and the blue `sprite.png` of AC-R08, and the magenta files already in the output folder of AC-R09 and AC-R10 | AC-R08–AC-R10 |
| `fixtureTranslucent` | 90 × 30 PNG: a transparent third storing green under alpha 0, a half-transparent red (alpha 128) third, and an opaque blue third | AC-R06, AC-R11 |
| `fixtureStored` + `withOrientation` | The stored pixels of an upright picture under each of the eight EXIF orientations, written from the EXIF definitions independently of the decoder, in a JPEG tagged with that orientation | AC-R12 |
| `fixtureDamagedPNG`, `fixtureGIF` | A file with the PNG signature and damaged data after it, and a GIF header under a `.gif` name | AC-R06 |
| Collision layouts | Each scenario's folders: sources in the output folder itself (AC-R07), same-named sources from two folders (AC-R08), an existing destination with a back-dated modification time (AC-R09), existing destinations beside an unrelated file and an unrelated numbered name (AC-R10) | AC-R07–AC-R10 |

Every fixture's pixels are set one by one, so a translucent color is stored exactly as given, green under alpha 0 included. PNG fixtures are encoded losslessly by the standard library, and JPEG fixtures at quality 95, so solid areas decode within a few steps of their color and the pixel checks can use tight tolerances; the source-list and view tests encode their blank images the same way. The decoder package's baseline, progressive, and CMYK JPEG fixtures, copied from the Go distribution, cover the decoder's coding features (`decode.TestDecodeJPEGFixtures`).

## Environment of record

Results below were recorded on 2026-11-20:

| Property | Value |
|---|---|
| OS | Windows 11 Home 26H2, build 26300 (ARM64) |
| Device | HP OmniBook 5 Laptop 14-he0xxx, Snapdragon X Plus X1P42100 (8 Oryon cores, 8 threads, up to 3.24 GHz), 32 GB RAM |
| Storage | NVMe SSD (SN5000S, 1 TB) |
| Go | go1.26.5 windows/arm64, for every result below: the test binaries run natively, and those for the AMD64 figures were built with `GOARCH=amd64` and ran under Windows' x64 emulation. CI builds the release executable with the toolchain `go.mod` pins, go1.27.4 since QLT-03, which was not measured (see [what these results do not cover](#what-these-results-do-not-cover)) |
| Display | The primary display at 100% scaling; the acceptance suite does not depend on the display configuration |

## Acceptance matrix

Every scenario passes on the environment of record, verified by three consecutive clean `go test -count=1 ./...` runs. "End to end" is the acceptance suite above; the tests after it pin the same behavior at the unit and window level.

| Scenario | End to end | Further automated coverage | Result |
|---|---|---|---|
| AC-R01 — Stretch | `TestAcceptanceR01Stretch`: 1000 × 500 → exactly 512 × 512, width and height scaled independently (the red quarter becomes 128 columns, the stripes half as wide as tall) | `resize.TestPlanAcceptanceGeometry`, `resize.TestPlanStretchAlwaysProducesTheTarget`, `resize.TestResizeAcceptanceDimensions`; distortion disclosed in the window before a job can start: `imageresizer.TestNativeViewModeDescriptionFollowsPresetSelection` | Pass |
| AC-R02 — Fit landscape | `TestAcceptanceR02FitLandscape`: a PNG and a JPEG 1000 × 500 → 512 × 256, every corner and edge the source's own opaque content, no alpha channel | `resize.TestPlanAcceptanceGeometry`, `resize.TestResizeAcceptanceDimensions` | Pass |
| AC-R03 — Fit portrait and rounding | `TestAcceptanceR03FitPortraitAndRounding`: ten portrait, landscape, square, and extreme sources (down to 1 × 1000 and 3000 × 1), PNG and JPEG, under 512 × 512 and 1024 × 768 — each within the target, filling its limiting side, within one pixel of the exact scaled size, never under 1 × 1 | `resize.TestPlanFitProperties`, `resize.TestPlanFitRoundsToTheNearestPixel` | Pass |
| AC-R04 — Crop to Fill | `TestAcceptanceR04CropToFill`: 1000 × 500 → exactly 512 × 512; the stripes land on the same output columns and rows along both axes (one scale before cropping) and no red or blue reaches even the first or last column (a centered crop) | `resize.TestPlanCropToFillProperties`, `resize.TestPlanCropToFillRemovesTheOddPixelRightOrBottom`, `resize.TestCropToFillEqualsScalingThenCropping` | Pass |
| AC-R05 — Upscaling | `TestAcceptanceR05Upscaling`: 100 × 50 → 512 × 512 (Stretch, Crop to Fill) and 512 × 256 (Fit), content enlarged, never kept at its own size | `resize.TestPlanAcceptanceGeometry` (enlargement rows), `resize.TestResizeAcceptanceDimensions` | Pass |
| AC-R06 — Mixed batch resilience | `TestAcceptanceR06MixedBatch`: a translucent PNG and a JPEG are saved; a corrupt PNG, checked again before the job as the view's job does, and an unsupported `.gif`, which its name refuses, end Failed, their rows saying so and their details naming the category and what is wrong; no source changes | `job.TestRunMixedBatch`, `imageresizer.TestNativeViewResizesEveryReadyImageEndToEnd`, `imageresizer.TestNativeViewIdentifiesContentProblems` | Pass |
| AC-R07 — Originals preserved | `TestAcceptanceR07OriginalsPreserved`: the output folder is the sources' own folder; under Rename and a confirmed Overwrite each result is saved under a numbered name with the reason shown, under Skip each item is Skipped saying its name is a source; every source keeps its bytes and modification time | `output.TestPrepareNeverTargetsSources`, `output.TestPrepareNeverOverwritesASource` (120 randomized jobs with hard links, earlier results, and unprocessed listed files), `output.TestPrepareRecognizesSourcesUnderOtherNames`, `job.TestRunOverwriteReplacesOnlyConfirmedDestinations` | Pass |
| AC-R08 — Rename collisions | `TestAcceptanceR08RenameCollisions`: two `sprite.png` sources from different folders are saved as `sprite.png` and `sprite_2.png`, each its own source's result, and the same job into another folder gives the same names to byte-identical results | `output.TestPrepareSameNameFromDifferentFolders`, `output.TestPrepareKeepsNaturalNamesAheadOfNumberedOnes`, `job.TestRunRenamesDuplicatesAndReplansOnRerun`, `imageresizer.TestNativeViewRerunsPlanAroundEarlierResults` | Pass |
| AC-R09 — Skip collisions | `TestAcceptanceR09SkipCollisions`: the existing destination keeps its bytes and back-dated modification time, its item is Skipped (not Completed) with the reason shown, and the job's other item is saved | `output.TestPrepareExistingDestinations`, `job.TestRunSkipLeavesExistingDestinations`, `job.TestRunSkipHandsTheNameToTheNextDuplicate` | Pass |
| AC-R10 — Confirmed overwrite | `TestAcceptanceR10ConfirmedOverwrite`: confirmed, only the two existing destinations are replaced, while an unrelated file, an unrelated numbered name, and the sources are untouched; unconfirmed, `job.Run` itself refuses the job before anything is touched, a guard behind the view's own, which starts no Overwrite job the user declines (`TestNativeViewOverwriteNeedsConfirmation`) | `output.TestPrepareRequiresOverwriteConfirmation`, `output.TestPrepareOverwriteNeverReplacesNumberedNames`, `output.TestWriteReplacesTheConfirmedFile`, `output.TestWriteNeverReplacesAChangedDestination`, `imageresizer.TestNativeViewOverwriteNeedsConfirmation` | Pass |
| AC-R11 — PNG alpha | `TestAcceptanceR11PNGAlpha`: PNG output keeps alpha — 0, 128, and 255 where the source has them — and the green the source stores under alpha 0 shows in no visible pixel, at the edge with the half-transparent third either; JPEG output flattens against `#3366CC` in proportion to alpha (the half-transparent red is (153, 51, 102)) | `encode.TestPNGIsLosslessAndKeepsAlpha`, `encode.TestJPEGFlattensTransparencyAgainstTheBackground`, `job.TestProcessKeepsPNGAlpha`, `job.TestProcessFlattensForJPEG`, `resize.TestTransparentPixelsLendNoColor` | Pass |
| AC-R12 — EXIF orientation | `TestAcceptanceR12EXIFOrientation`: one upright 96 × 64 picture stored under each of the eight orientations is listed at 96 × 64 and resized upright to 192 × 128 with every quadrant in place | `decode.TestDecodeAppliesEveryEXIFOrientation`, `decode.TestOrientExactPixels`, `job.TestProcessOutputIsUpright` | Pass |
| AC-R13 — Cancellation | `TestAcceptanceR13Cancellation`: cancelled once its first result is saved, the job starts no further item, completed results decode at their size, the rest end Cancelled with nothing saved and no temporary file left; `TestNativeViewStaysResponsiveDuringARealJob` cancels a real job through Cancel Resize | `job.TestRunCancellationKeepsCompletedResults`, `job.TestRunWaitsForItemsInFlight`, `output.TestWriteObservesCancellation`, `imageresizer.TestNativeViewCancelStopsTheJob`, `imageresizer.TestNativeViewLetsGoOfAJobThatDoesNotStop`, `imageresizer.TestRecheckInputsStopsOnceCancelled` (no pre-job content check starts once the job is cancelled) | Pass |
| AC-R14 — Write failure | `TestAcceptanceR14WriteFailure`: partway through a real job the output folder's access control list stops the user creating files in it, and after the first failure it is restored; each item that could not save fails on its own with a write error naming its destination and the permission hint, the folder holds the saved results alone — no partial final file under any name, and no temporary file — and every item that starts afterwards is saved | `output.TestWriteReportsWriteFailures` (full drive, used-up quota, denied write), `job.TestRunIsolatesItemFailures`, `job.TestRunFailsTheRestOnceTheOutputFolderIsLost` | Pass |
| AC-R15 — Independent presets | `preset.TestNativeManagerScreenshotPresetAbsentFromImageResizer`: a custom 640 × 480 Screenshot preset is absent from the Image Resizer's preset list until one is created there, which survives deleting the Screenshot one — each read in the open window, its list rebuilt from its library by selecting a preset in it, which also saves that library, and after restarts: one after each Screenshot change and before the Image Resizer saves again, as that save rewrites the Image Resizer's stored section from its library and would hide a Screenshot save that dropped or changed it, and one after each Image Resizer save, which shows it kept the Screenshot's presets as they were | `preset.TestLibrariesHaveIndependentNamespaces`, `preset.TestRepositoryPersistsIndependentLibrariesAndStableIdentifiers`, `preset.TestNativeManagerCRUDIndependenceAndRestart` | Pass |
| AC-A04 — Responsive batch UI | `imageresizer.TestNativeViewStaysResponsiveDuringARealJob`: a real job runs on a thread other than the GUI thread, which the test checks, and while it is held partway the GUI thread applies its progress, moves the window, reviews every image's state and detail, and cancels the job, after which the view is ready for the next job; the move shows only that the GUI thread is free to act, as [what these results do not cover](#what-these-results-do-not-cover) explains | `imageresizer.TestNativeViewCancelStopsTheJob` (selection and review during a job, refused list changes), `imageresizer.TestNativeViewJobSectionKeepsItsHeightWhileAJobRuns`, `job.TestDefaultWorkersLeaveAProcessorFree` | Pass |
| NFR-014 — Bounded memory | `job.BenchmarkRun100Mixed12MegapixelJPEGsFit1024` fails if a sample of the heap, taken every millisecond, finds it beyond the job's 1 GiB budget; see [Memory observations](#memory-observations) | `job.TestRunBoundsWorkersAndMemory`, `job.TestRunCollectsBeforeHandingOnMemory`, `job.TestRunStartsOverASourceThatGrewSinceItsCheck`, `decode.TestOversizedSourcesAreRejectedBeforeDecoding`, `decode.TestInspectReleasesLargeDecodes`, `decode.TestDecodeWithinAnAllowance`, `resize.TestWorkingMemoryStaysBounded` | Pass |
| TECH-009 — Automated coverage | Preset validation (`preset.TestValidationBoundaries`, `resize.TestValidateAgreesWithThePresetLibrary`), resize calculations (`resize.TestPlan*`), output naming and collisions (`output.TestPrepare*`, `output.TestWrite*`), settings migration (`settings.TestStoreMigrationFixture`, `imageresizer.TestStoredFolderEarlierBuildsAcceptedFallsBackToTheDefault`, `imageresizer.TestMalformedResizeOptionsRecoverThroughStore`), and batch failure handling (`job.TestRunIsolatesItemFailures` and the AC-R06 and AC-R14 rows) | — | Pass |

## Performance results

NFR-013 asks that resizing 100 mixed 12-megapixel JPEG images to fit within 1024 × 1024 complete within two minutes on the reference machine. `job.BenchmarkRun100Mixed12MegapixelJPEGsFit1024` runs exactly that job through `job.Run`, with the job's own worker count and memory budget:

- **Sources:** 100 JPEGs, 370 MiB in all — 40 landscape 4000 × 3000, 30 stored portrait 3000 × 4000, and 30 stored landscape with an EXIF orientation that makes them upright portraits, 20 tagged 8 and 10 tagged 6, which cost the same to turn upright — 4:2:0 at quality 90 with photo-like gradients, texture, and noise. Seven distinct pictures are copied under 100 names, since decoding a copy costs what decoding the original does.
- **Settings:** Fit within 1024 × 1024, Keep Source Format (JPEG results at the default quality 90), Rename, a fresh output folder on the same NVMe SSD for each run.
- **Timed:** from `job.Run` being called to its return, every result saved. The content checks are excluded, as the source list makes them when files are added, before Start Resize. The sources were just written, so they are read from the file cache; reading 370 MiB cold from this SSD would add a fraction of a second.

| Configuration | Workers | 100-image job, per run | Per image | NFR-013 target |
|---|---:|---|---:|---|
| Native ARM64, all 8 processors (the default) | 4 | 8.8 s, 9.2 s, 9.0 s | ~90 ms | 120 s |
| Native ARM64, Go limited to 4 processors (`GOMAXPROCS=4`), as on a quad-core without simultaneous multithreading | 3 | 14.7 s, 12.5 s, 16.7 s | 125–167 ms | 120 s |
| AMD64 test binary (`GOARCH=amd64`), the release target, under Windows' x64 emulation, 8 processors, run twice (`-count=2`) rather than three times | 4 | 14.9 s, 13.2 s | 132–149 ms | 120 s |

Every configuration meets the target with at least a sevenfold margin. The variance is recorded rather than tuned away: the four-processor runs spread from 12.5 s to 16.7 s, a spread the eight-processor runs do not show, which points at the laptop's power management varying its cores' clocks between runs rather than at the job, and even the slowest is seven times faster than the target; the AMD64 figures include the cost of emulating x64 on ARM64, which the AMD64 release build on an Intel or AMD reference machine does not pay. A 2020-or-newer quad-core with simultaneous multithreading presents 8 processors to Go and so runs 4 workers, as the first row does.

The resize step alone, `resize.BenchmarkResize12MegapixelJPEGFit1024` — a 4000 × 3000 4:2:0 picture fit within 1024 × 1024 — measures 135 ms on the environment of record, as RSZ-04 measured it; the rest of each item is decoding, the upright copy of a reoriented source, encoding, and saving.

## Memory observations

NFR-014 requires that batch work never decode every input into memory at once. The same benchmark samples the Go runtime's memory every millisecond while the job runs (`runtime/metrics`, which stops nothing) and fails if a sample finds the heap beyond the job's 1 GiB budget. The peaks below are the highest samples; a peak that rose and was collected between two samples would go unseen:

| Configuration | Peak heap | Peak runtime footprint | Decoding every source at once |
|---|---:|---:|---:|
| Native ARM64, 4 workers | 217 MiB | 229 MiB | 3,094 MiB |
| Native ARM64, 3 workers | 196–200 MiB | 240–241 MiB | 3,094 MiB |
| AMD64 under emulation, 4 workers | 220 MiB | 229 MiB | 3,094 MiB |

"Peak heap" is the heap's objects, garbage not yet swept included; "peak runtime footprint" is all memory the runtime had mapped and not handed back to Windows — for this program, which has no C code, all of its memory but the executable's. "Decoding every source at once" is the sum of the sources' own decoded-memory estimates (`decode.Info.DecodedBytes`): about 17 MiB for each 4:2:0 source and 63 MiB for each reoriented one, whose upright copy is RGBA. The job held at most about 7% of that, and about a fifth of its 1 GiB budget, because only the items its workers run are decoded at a time and their memory is collected before the next items take their share of the budget.

## What these results do not cover

- **The reference machine.** NFR-013's target is defined on a 2020-or-newer quad-core with 16 GB RAM, an SSD, and Windows 11, which was not available; the environment of record is an 8-core ARM64 laptop. The four-processor and emulated AMD64 figures above indicate what a reference machine would see, with a sevenfold margin; run the benchmark on one when it is available and add its row.
- **A person's hands on the window.** The responsive-batch scenario is verified from the GUI thread while a real job is held: it applies the job's progress, moves the window, and reviews and cancels through the view's own controls. The move is a `SetWindowPos` call on the test's hidden window from the thread that owns it, which succeeds whatever the job does, so it shows that the GUI thread is free to act, not that a message loop keeps up with a drag: no test drags the window with a mouse or runs the shell's message loop. What keeps the window responsive is that the job never runs on the GUI thread, which the view's design (TECH-005) and the thread check in `TestNativeViewStaysResponsiveDuringARealJob` pin.
- **The release toolchain.** Every figure above comes from test binaries built with go1.26.5. CI builds the AMD64 release executable with the toolchain `go.mod` pins, go1.27.4 since QLT-03, whose compiler and garbage collector differ, so the release executable's duration and heap peaks were not measured here. The [platform record](platform-verification.md#performance) has the same benchmark on go1.27.4.
- **Data races under the race detector.** `go test -race` is unavailable on windows/arm64; the job's concurrency is confined to its workers, which hand every state change to `Run`'s goroutine, as `job.Run` documents and its tests pin by checking that events never overlap.
- **Other drives and file systems.** Every result was written to the internal NVMe SSD's NTFS volume. Network shares, removable FAT32 and exFAT drives, and OneDrive folders are covered at the mechanism level by the `output` package's tests, not measured.

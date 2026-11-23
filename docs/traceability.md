# Requirement traceability

This matrix is the REL-02 record of how every requirement and acceptance scenario in the [PRD](../PRD.md) is implemented and verified. It lists each one with the [implementation plan](../IMPLEMENTATION_PLAN.md) items that name it, the automated checks or records that are its evidence, its status, and what is still open. `release.TestTraceabilityCoversEveryRequirement` fails if a requirement is missing, a named test does not exist, a row that waits on an open hardware run or manual check claims to pass, or the lists of blockers below disagree with the rows.

The evidence column names representative checks, not all of them, as `package.Test`. The two `output` packages are told apart as `screenshot/output` and `imageresizer/output`. The verification records hold the full matrices: [Screenshot](screenshot-verification.md), [Image Resizer](image-resizer-verification.md), [accessibility](accessibility-verification.md), [settings and shutdown](reliability-verification.md), [privacy and dependencies](privacy-verification.md), and [platform and performance](platform-verification.md). The [test and verification guide](testing.md) says how to run them.

## Status

- **Pass.** The named checks cover the requirement and pass on the environment of record. An entry under Open is a manual check that adds confidence, such as using a real screen reader, and has not been done.
- **Partial.** Part of the requirement has not been exercised, as the Open column says. **A Partial requirement is not complete.** Those that name QLT-04A need hardware this project's environment does not have. Those that name M8, M9, M10, or M12 need a person to do what the requirement describes in a real application, as the automated checks only reach the mechanism: the clipboard's format, the frame's window style and transparent pixels, mouse messages sent directly to the frame, a hotkey message delivered directly, a foreground request whose result nothing checks, and a window move made by a direct call with no message loop running. The FND-01 spike clicked through and dragged a color-keyed window, which is not how the production frame is built. A row leaves Partial once the [recorded runs](testing.md#recorded-runs) name it.

`M1` to `M14` in the Open column are the [manual pre-release checks](testing.md#manual-pre-release-checks), which REL-03 owns. RSZ-041 names M12 only for added confidence: the job's thread and its coalesced progress are what keep the GUI responsive, and the drag itself is AC-A04's.

## Summary

| Kind | Total | Pass | Partial |
|---|---:|---:|---:|
| P0 requirements | 124 | 109 | 15 |
| P1 requirements | 20 | 12 | 8 |
| Acceptance scenarios | 32 | 27 | 5 |
| **All** | **176** | **148** | **28** |

Every P0 requirement and acceptance scenario below marked Partial is a release blocker until the hardware run or manual check it names passes or it receives an approved resolution, as PRD section 16 requires:

SCR-002, SCR-020, SCR-021, SCR-022, SCR-023, SCR-026, SCR-042, SCR-043, SCR-054, SCR-062, NFR-001, NFR-003, TECH-002, TECH-003, TECH-009, AC-S02, AC-S03, AC-S04, AC-S12, AC-A04.

The Partial P1 requirements are SCR-005, SCR-028, SCR-055, UX-008, NFR-010, NFR-011, NFR-012, NFR-013. UX-008 is deferred by the decision in PRD section 20, which the release notes state. NFR-011 is the one measured target that was missed, under x64 emulation only.

## Application shell

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| APP-001 | P0 | Navigation to both modules and Settings | FND-03 | `shell.TestNavigatorRegisterAndSelect`, `app.TestNavigationArrowKeysSwitchModules` | Pass |  |
| APP-002 | P0 | Current module and primary action are obvious | FND-03 | `app.TestTabReachesEveryControlInLayoutOrder`, `app.TestShellDisablesAnUnavailablePrimaryAction` | Pass | M1 |
| APP-003 | P1 | Restores the last module | FND-04 | `app.TestOpenSessionUpgradesEverySettingsFormat`, `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` | Pass |  |
| APP-004 | P0 | Work keeps unsaved edits and the other module's state | QLT-02 | `app.TestStartingWorkKeepsUnsavedEditsAndTheOtherModule` | Pass |  |
| APP-005 | P1 | Single instance; second launch activates the window | SCR-09 | `app.TestAcquireInstanceDetectsExisting`, `app.TestActivateExistingInstanceRestoresWindow`, `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` | Pass |  |

## Presets

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| PRESET-001 | P0 | Built-in and named custom presets | FND-06, RSZ-01 | `preset.TestBuiltInDefinitions`, `preset.TestNativeManagerCRUDIndependenceAndRestart` | Pass |  |
| PRESET-002 | P0 | Create, rename, duplicate, delete | FND-07 | `preset.TestNativeManagerCRUDIndependenceAndRestart`, `preset.TestRenameAndUpdateKeepStableIdentifier` | Pass |  |
| PRESET-003 | P0 | Built-ins are immutable and can be duplicated | FND-06, FND-07 | `preset.TestBuiltInImmutabilityAndDuplication`, `preset.TestNativeManagerValidationAndBuiltInProtection` | Pass |  |
| PRESET-004 | P0 | Presets persist across restarts | FND-06 | `preset.TestRepositoryPersistsIndependentLibrariesAndStableIdentifiers`, `preset.TestNativeManagerCRUDIndependenceAndRestart` | Pass |  |
| PRESET-005 | P0 | Rejects zero, negative, fractional, non-numeric sizes | FND-06 | `preset.TestValidationBoundaries`, `ui.TestParseDimension` | Pass |  |
| PRESET-006 | P0 | 1-16,384 px per axis; resize presets up to 100 MP | FND-06, RSZ-04 | `preset.TestValidationBoundaries`, `resize.TestValidateAgreesWithThePresetLibrary` | Pass |  |
| PRESET-007 | P1 | Restores each module's last preset | FND-07, SCR-01, RSZ-01 | `preset.TestNativeManagerCRUDIndependenceAndRestart`, `app.TestOpenSessionUpgradesEverySettingsFormat` | Pass |  |
| PRESET-008 | P0 | Malformed preset data keeps built-ins and the file | FND-06 | `preset.TestMalformedStoredPresetsAreBackedUpAndBuiltInsSurvive`, `app.TestOpenSessionRecoversFromMalformedSettings` | Pass |  |

## Screenshot

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| SCR-001 | P0 | Start the selected mode from the main window | SCR-05, SCR-09 | `screenshot.TestNativeViewFullDisplayCaptureSucceeds`, `screenshot.TestNativeViewRectangleModesStartOverlaySession` | Pass |  |
| SCR-002 | P0 | Global shortcut while another app has focus | SCR-09 | `screenshot.TestNativeHotkeyInvokesSelectedCaptureMode`, `app.TestShellWindowProcRoutesHotkeys` | **Partial** | M10: never pressed with another application focused |
| SCR-003 | P0 | Default `Ctrl+Alt+S`; conflicts reported and replaceable | SCR-09 | `screenshot.TestDefaultShortcut`, `screenshot.TestNativeViewStartupConflictIsReported`, `screenshot.TestNativeViewShortcutConflictKeepsOldShortcut` | Pass |  |
| SCR-004 | P0 | Shortcut changes persist without reinstalling | SCR-09 | `screenshot.TestNativeViewShortcutCommitAppliesAndPersists` | Pass |  |
| SCR-005 | P1 | Shortcut during a selection draws attention to it | SCR-09 | `screenshot.TestNativeHotkeyDuringSessionBringsAttention`, `overlay.TestNativeSessionAttentionForegroundsToolbar` | **Partial** | M10: the toolbar has not been seen coming back from another application |
| SCR-006 | P0 | Shortcut released on exit | SCR-09 | `screenshot.TestNativeViewDestroyReleasesHotkey`, `app.TestExitDuringACaptureSessionLeavesNoOverlayOrShortcut` | Pass |  |
| SCR-010 | P0 | Drag a rectangle of at least 1 x 1 | SCR-07 | `overlay.TestOverlayDragDrawsAndCapturesSelection`, `overlay.TestDrawRectNormalizesDragCorners` | Pass |  |
| SCR-011 | P0 | Live width and height while dragging | SCR-07 | `overlay.TestOverlayDragDrawsAndCapturesSelection`, `overlay.TestOverlayFocusedFieldTracksHandleResize` | Pass |  |
| SCR-012 | P0 | Move and resize after drawing | SCR-07 | `overlay.TestOverlayMoveAndResizeAdjustSelection`, `overlay.TestResizeSelectionAnchorsOppositeEdges` | Pass |  |
| SCR-013 | P0 | Type exact width and height | SCR-07 | `overlay.TestOverlayDimensionEditingAppliesExactSize` | Pass |  |
| SCR-014 | P0 | Output matches the rectangle, without overlay | SCR-07 | `overlay.TestOverlayDragDrawsAndCapturesSelection`, `screenshot.TestNativeViewRectangleCaptureEndToEnd` | Pass |  |
| SCR-015 | P1 | Free Rectangle without a pointer | QLT-01 | `overlay.TestFreeRectangleTypedSizeCreatesSelection`, `overlay.TestFreeRectangleArrowKeysNudgeSelection` | Pass |  |
| SCR-020 | P0 | Preset is exact at any monitor scaling | SCR-08 | `overlay.TestPresetFrameExactSizeAndSurface`, `overlay.TestPresetFrameCaptureDeliversExactRect` | **Partial** | QLT-04A: any scaling but 100% |
| SCR-021 | P0 | Frame stays on top of other windows | SCR-08 | `overlay.TestPresetFrameExactSizeAndSurface` | **Partial** | M9: not run while another application is in use |
| SCR-022 | P0 | Interior is unobscured and click-through | SCR-08 | `overlay.TestPresetFrameExactSizeAndSurface`, `overlay.TestPresetFrameClickOnBorderLeavesSelectionUnchanged` | **Partial** | M9: no real click through the production frame |
| SCR-023 | P0 | Border drags without resizing | SCR-08 | `overlay.TestPresetFrameBorderDragMovesWithoutResizing` | **Partial** | M9: no real pointer has dragged the production frame |
| SCR-024 | P0 | Toolbar has Capture, Cancel, and dimensions | SCR-07, SCR-08 | `overlay.TestToolbarCaptureCommandDeliversSelection`, `overlay.TestToolbarCancelCommandCancelsSession`, `overlay.TestPresetFrameCaptureDeliversExactRect` | Pass |  |
| SCR-025 | P0 | Toolbar near the selection and reachable at edges | SCR-07, SCR-08 | `overlay.TestToolbarPlacementFollowsTheSelection`, `overlay.TestToolbarPlacementWithoutSelectionAnchorsToCursorDisplay` | Pass |  |
| SCR-026 | P0 | Moves across displays without a size change | SCR-08 | `capture.TestCaptureSpanningDisplayBoundary`, `overlay.TestPresetFrameBorderDragMovesWithoutResizing` | **Partial** | QLT-04A: across a mixed-scaling boundary |
| SCR-027 | P0 | Arrow keys move 1 px, Shift+Arrow 10 px | SCR-08 | `overlay.TestPresetFrameKeyboardNudges` | Pass |  |
| SCR-028 | P1 | X/Y readout, including negative coordinates | SCR-08 | `overlay.TestPresetFrameBorderDragMovesWithoutResizing`, `display.TestVirtualDesktopBounds` | **Partial** | QLT-04A: a display at negative coordinates |
| SCR-029 | P0 | Ad hoc size edit leaves the saved preset alone | SCR-08 | `overlay.TestPresetFrameDimensionEditResizesExactly`, `screenshot.TestNativeViewPresetAdHocEditKeepsSavedPreset` | Pass |  |
| SCR-030 | P0 | No resize handles in Preset Rectangle | SCR-08 | `overlay.TestPresetFrameBorderDragMovesWithoutResizing`, `overlay.TestPresetFrameClickOnBorderLeavesSelectionUnchanged` | Pass |  |
| SCR-031 | P0 | Rectangle larger than the desktop is blocked | SCR-08, SCR-11 | `overlay.TestBeginPresetValidatesFrameSize`, `screenshot.TestNativeViewPresetRectangleTooLargeBlocked` | Pass |  |
| SCR-040 | P0 | Full Display captures one selected display | SCR-05 | `capture.TestCaptureDisplay`, `screenshot.TestNativeViewFullDisplayCaptureSucceeds` | Pass |  |
| SCR-041 | P0 | Picker shows number, name, resolution, primary | SCR-02, SCR-05 | `screenshot.TestDisplayListItemDescribesDisplay`, `display.TestNumberDisplays` | Pass |  |
| SCR-042 | P0 | Native pixel dimensions and orientation | SCR-03, SCR-05 | `capture.TestCaptureDisplay` | **Partial** | QLT-04A: a rotated display |
| SCR-043 | P0 | Any display, including other DPI scaling | SCR-02, SCR-05 | `screenshot.TestNativeViewDisplayPickerTracksMode`, `capture.TestCaptureDisplay` | **Partial** | QLT-04A: a secondary display at other scaling |
| SCR-044 | P0 | Main window and overlays absent from the image | SCR-03, SCR-05 | `capture.TestHideExcludesWindowFromCapture`, `execute.TestFullDisplayRunsHideCaptureRestoreDeliverInOrder` | Pass |  |
| SCR-050 | P0 | Main window hides before the overlay shows | SCR-06 | `overlay.TestBeginHidesMainWindowBeforeCreatingOverlay` | Pass |  |
| SCR-051 | P0 | Cancel changes no clipboard or file | SCR-06, SCR-07 | `overlay.TestOverlayEscapeCancelsWithoutOutput`, `screenshot.TestNativeViewRectangleCancelProducesNoOutput` | Pass |  |
| SCR-052 | P0 | Failure leaves no invisible overlay | SCR-06, SCR-10 | `overlay.TestBeginFailureRestoresMainWindow`, `screenshot.TestNativeViewOverlayFailureRestoresMainWindow` | Pass |  |
| SCR-053 | P0 | One selection session at a time | SCR-06 | `overlay.TestBeginRejectsConcurrentSession` | Pass |  |
| SCR-054 | P0 | Display change revalidates the selection | SCR-10 | `overlay.TestDisplayChangeKeepsFittingSession`, `overlay.TestDisplayChangeEndsSessionThatCannotFit` | **Partial** | QLT-04A: a live disconnect and reconnect |
| SCR-055 | P1 | Selection survives losing activation | SCR-08 | `overlay.TestToolbarRestoresFocusOnActivation` | **Partial** | M9: activation simulated, no real click in another application |
| SCR-056 | P0 | Exit removes overlays and restores input | SCR-06, SCR-10 | `overlay.TestShutdownRemovesSessionSilently`, `app.TestExitDuringACaptureSessionLeavesNoOverlayOrShortcut` | Pass |  |
| SCR-060 | P0 | Clipboard and save reported independently | SCR-04, SCR-05, SCR-10, QLT-03A | `screenshot/output.TestDeliverSaveFailureKeepsClipboardSuccess`, `screenshot/output.TestDeliverClipboardFailureKeepsSaveSuccess` | Pass |  |
| SCR-061 | P0 | PNG or JPEG, PNG by default | SCR-01, SCR-04 | `screenshot.TestDefaultOptions`, `screenshot.TestNativeViewFormatQualityAndPattern` | Pass |  |
| SCR-062 | P0 | Clipboard image is compatible and not JPEG-degraded | SCR-04 | `screenshot/output.TestDeliverJPEGDoesNotDegradeClipboard`, `screenshot/output.TestSetClipboardImageRoundTrip` | **Partial** | M8: no real application has pasted the image |
| SCR-063 | P0 | Default Pictures folder; another can be chosen | SCR-01, SCR-04, QLT-03B, QLT-03C | `screenshot.TestDefaultFolderIsOneACaptureCanUse`, `screenshot.TestNativeViewBrowseForOutputFolder` | Pass |  |
| SCR-064 | P0 | Filename pattern `Screenshot_yyyyMMdd_HHmmssfff` | SCR-01, SCR-04 | `screenshot/output.TestSaveWritesTimestampedFile`, `screenshot.TestImageFormatExtensionAndFilenameExample` | Pass |  |
| SCR-065 | P0 | Never overwrites; appends a numeric suffix | SCR-04, QLT-03A | `screenshot/output.TestSaveAppendsNumericSuffixOnCollision` | Pass |  |
| SCR-066 | P0 | Written through a temporary file | SCR-04, QLT-03A | `screenshot/output.TestSaveEncodeFailureLeavesNoFiles`, `screenshot/output.TestSaveWithoutHardLinksMovesTheFinishedFile` | Pass | M5 |
| SCR-067 | P0 | JPEG quality 1-100, default 90 | SCR-01, SCR-04 | `screenshot.TestCaptureOptionsValidateBoundaries`, `screenshot.TestParseJPEGQuality` | Pass |  |
| SCR-068 | P0 | Cursor, frame, toolbar, tint not in the output | SCR-03, SCR-07, SCR-08 | `capture.TestHideExcludesWindowFromCapture`, `overlay.TestPresetFrameCaptureDeliversExactRect` | Pass |  |
| SCR-069 | P1 | Notification with Open Folder and path or error | SCR-05, SCR-10, QLT-03B | `screenshot.TestNativeViewOpenFolderRevealsSavedFile`, `screenshot.TestNativeViewCaptureFailurePresentations` | Pass |  |

## Image Resizer

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| RSZ-001 | P0 | Multi-file picker | RSZ-02 | `imageresizer.TestNativeViewAddImagesThroughPicker`, `ui.TestParsePickedPaths` | Pass |  |
| RSZ-002 | P0 | Drag and drop multiple files | RSZ-02 | `imageresizer.TestNativeViewDropFilesAndFolder` | Pass |  |
| RSZ-003 | P0 | PNG/JPEG extensions, validated by content | RSZ-02, RSZ-03 | `imageresizer.TestSupportedExtensionIsCaseInsensitive`, `decode.TestContentDecidesFormat` | Pass |  |
| RSZ-004 | P0 | Bad files are identified and block nothing | RSZ-02, RSZ-03 | `imageresizer.TestInventoryAddClassifiesMixedBatch`, `imageresizer.TestAcceptanceR06MixedBatch` | Pass |  |
| RSZ-005 | P0 | List shows name, path, dimensions, status | RSZ-02, RSZ-03, RSZ-08 | `imageresizer.TestSourceItemRowAndDetailText`, `imageresizer.TestNativeViewSelectionShowsStatusDetail` | Pass |  |
| RSZ-006 | P0 | Remove selected and clear | RSZ-02 | `imageresizer.TestNativeViewRemoveSelectedAndClearSources`, `imageresizer.TestInventoryRemoveAndClear` | Pass |  |
| RSZ-007 | P0 | No duplicate items for one path | RSZ-02 | `imageresizer.TestInventoryAddDeduplicatesFullPathsCaseInsensitively` | Pass |  |
| RSZ-008 | P1 | Folder drop adds its direct files | RSZ-02 | `imageresizer.TestInventoryAddExpandsFoldersNonrecursively`, `imageresizer.TestNativeViewDropFilesAndFolder` | Pass |  |
| RSZ-009 | P0 | Inputs opened read-only and never modified | RSZ-02 | `decode.TestSourcesAreOpenedReadOnly`, `imageresizer.TestInventoryAddNeverModifiesSources` | Pass |  |
| RSZ-010 | P0 | Stretch, with a distortion warning | RSZ-01, RSZ-04 | `imageresizer.TestAcceptanceR01Stretch`, `imageresizer.TestNativeViewModeDescriptionFollowsPresetSelection` | Pass |  |
| RSZ-011 | P0 | Fit keeps aspect ratio, no padding | RSZ-04 | `imageresizer.TestAcceptanceR02FitLandscape` | Pass |  |
| RSZ-012 | P0 | Fit scaling, rounding, and clamping | RSZ-04 | `resize.TestPlanFitRoundsToTheNearestPixel`, `resize.TestPlanFitProperties` | Pass |  |
| RSZ-013 | P0 | Crop to Fill covers and center-crops | RSZ-04 | `imageresizer.TestAcceptanceR04CropToFill` | Pass |  |
| RSZ-014 | P0 | Even crop, odd pixel from right or bottom | RSZ-04 | `resize.TestPlanCropToFillRemovesTheOddPixelRightOrBottom` | Pass |  |
| RSZ-015 | P0 | Every mode can enlarge | RSZ-04 | `imageresizer.TestAcceptanceR05Upscaling` | Pass |  |
| RSZ-016 | P0 | Fitting mode explained before starting | RSZ-01, RSZ-08 | `imageresizer.TestNativeViewModeDescriptionFollowsPresetSelection` | Pass |  |
| RSZ-017 | P0 | High-quality, deterministic resampling | RSZ-04 | `resize.TestResizeIsDeterministic`, `resize.TestResizeMatchesTheFloatReference` | Pass |  |
| RSZ-018 | P0 | EXIF orientation applied first | RSZ-03 | `imageresizer.TestAcceptanceR12EXIFOrientation`, `decode.TestDecodeAppliesEveryEXIFOrientation` | Pass |  |
| RSZ-020 | P0 | Creates new files and preserves originals | RSZ-01, RSZ-05 | `imageresizer.TestAcceptanceR07OriginalsPreserved`, `imageresizer.TestDefaultOptions` | Pass |  |
| RSZ-021 | P0 | Default folder; last valid folder remembered | RSZ-01, RSZ-05, QLT-03D | `imageresizer.TestDefaultFolder`, `imageresizer.TestOptionsStoreRoundTripAndPresetCoexistence` | Pass |  |
| RSZ-022 | P0 | Keep Source Format, PNG, JPEG | RSZ-01, RSZ-05, RSZ-06 | `imageresizer/output.TestEncodedFormatAndExtension`, `imageresizer.TestDefaultOptions` | Pass |  |
| RSZ-023 | P0 | Keeps the base name with a matching extension | RSZ-01, RSZ-05, RSZ-06 | `imageresizer/output.TestPrepareNamesDestinationsAfterSources` | Pass |  |
| RSZ-024 | P0 | Duplicate destinations follow the policy | RSZ-05 | `imageresizer/output.TestPrepareSameNameFromDifferentFolders`, `imageresizer.TestAcceptanceR08RenameCollisions` | Pass |  |
| RSZ-025 | P0 | Rename, Skip, or confirmed Overwrite | RSZ-01, RSZ-05 | `imageresizer.TestNativeViewDefaultsAndCollisionSelection`, `imageresizer.TestNativeViewOverwriteNeedsConfirmation` | Pass |  |
| RSZ-026 | P0 | A source is never overwritten | RSZ-05 | `imageresizer/output.TestPrepareNeverTargetsSources`, `imageresizer/output.TestPrepareNeverOverwritesASource` | Pass |  |
| RSZ-027 | P0 | Temporary file, atomic finalize, cleanup | RSZ-06 | `imageresizer/output.TestWriteRemovesTheTemporaryFileWhenEncodingFails`, `imageresizer/output.TestWriteNeverReplacesAFileThatAppeared` | Pass | M5 |
| RSZ-028 | P0 | PNG alpha kept; JPEG flattened on a background | RSZ-01, RSZ-06 | `imageresizer.TestAcceptanceR11PNGAlpha`, `encode.TestJPEGFlattensTransparencyAgainstTheBackground` | Pass |  |
| RSZ-029 | P0 | JPEG quality 1-100, default 90 | RSZ-01, RSZ-05, RSZ-06 | `imageresizer.TestResizeOptionsValidateBoundaries`, `encode.TestJPEGUsesTheQuality` | Pass |  |
| RSZ-030 | P0 | Creates the folder and reports write failures | RSZ-05, RSZ-06, QLT-03C | `imageresizer/output.TestPrepareCreatesTheOutputFolder`, `imageresizer/output.TestWriteReportsWriteFailures` | Pass | M11 |
| RSZ-040 | P0 | Start validates the whole job | RSZ-07, RSZ-08 | `imageresizer.TestNativeViewRefusesJobsThatCannotStart`, `job.TestRunFailsAnInvalidJobBeforeAnything` | Pass |  |
| RSZ-041 | P0 | GUI stays responsive | RSZ-07, RSZ-08 | `imageresizer.TestNativeViewStaysResponsiveDuringARealJob` | Pass | M12 |
| RSZ-042 | P0 | Progress counts and current file | RSZ-07, RSZ-08 | `imageresizer.TestProgressMessageNamesTheCurrentFile`, `imageresizer.TestNativeViewResizesEveryReadyImageEndToEnd` | Pass |  |
| RSZ-043 | P0 | Per-item states | RSZ-07, RSZ-08 | `job.TestRunMixedBatch`, `imageresizer.TestSourceItemShowsJobResults` | Pass |  |
| RSZ-044 | P0 | One failure does not stop the rest | RSZ-07, RSZ-08 | `job.TestRunIsolatesItemFailures`, `imageresizer.TestAcceptanceR06MixedBatch` | Pass |  |
| RSZ-045 | P0 | Cancel stops new items and keeps results | RSZ-07, RSZ-08 | `job.TestRunCancellationKeepsCompletedResults`, `imageresizer.TestAcceptanceR13Cancellation` | Pass |  |
| RSZ-046 | P0 | Completion summary and per-file details | RSZ-07, RSZ-08 | `imageresizer.TestSummaryNoticeCountsEveryOutcome`, `imageresizer.TestResultDetailNamesEveryReasonAndLeftoverFile` | Pass |  |
| RSZ-047 | P0 | Output folder never opens by itself | RSZ-08 | `imageresizer.TestNativeViewResizesEveryReadyImageEndToEnd` | Pass |  |
| RSZ-048 | P1 | Open Output Folder action | RSZ-08 | `imageresizer.TestNativeViewKeepsOfferingTheLatestResults`, `imageresizer.TestNativeViewShowsAnOpenFailureOutsideTheJobSection` | Pass |  |
| RSZ-049 | P0 | Re-run re-evaluates collisions and inputs | RSZ-07, RSZ-08 | `job.TestRunRenamesDuplicatesAndReplansOnRerun`, `imageresizer.TestNativeViewRerunsPlanAroundEarlierResults` | Pass |  |
| RSZ-050 | P1 | Bounded parallelism | RSZ-07 | `job.TestRunBoundsWorkersAndMemory`, `job.TestDefaultWorkersLeaveAProcessorFree` | Pass |  |

## Settings and local data

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| SET-001 | P0 | Settings persist and reload | FND-04 | `settings.TestStoreFreshStartAndRestart` | Pass |  |
| SET-002 | P0 | Everything listed is persisted | FND-04 | `app.TestOpenSessionUpgradesEverySettingsFormat` | Pass |  |
| SET-003 | P0 | Stored in a per-user folder | FND-04 | `settings.TestLocalPathsNameTheProductAndTheLegacyFolder` | Pass |  |
| SET-004 | P0 | Write failure warns and keeps the session | FND-04, QLT-02, QLT-03D | `settings.TestStoreUnavailablePathAndFailedWrite`, `app.TestFailedSaveWarnsAndClosingAsks` | Pass | M11 |
| SET-005 | P0 | Restore defaults without deleting images | QLT-02 | `app.TestRestoreDefaultsKeepsPresetsImagesAndTheSession`, `settings.TestNativeViewRestoresOnlyOnceConfirmed` | Pass |  |
| SET-006 | P1 | Corrupt settings backed up and reported | FND-04, QLT-02, QLT-03D | `settings.TestStoreMalformedSettingsAreBackedUpAndRecovered`, `app.TestOpenSessionRecoversFromMalformedSettings` | Pass |  |
| SET-007 | P0 | Logs hold no image, clipboard, or file names | QLT-03 | `audit.TestDiagnosticEventsAreFixed`, `diagnostics.TestLoggerRejectsFreeFormMetadata` | Pass |  |

## UX, accessibility, and localization

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| UX-001 | P0 | Clear primary action labels | FND-05, QLT-01 | `app.TestTabReachesEveryControlInLayoutOrder`, [accessibility record](accessibility-verification.md) | Pass |  |
| UX-002 | P0 | Destructive actions confirm | FND-05, FND-07, QLT-01 | `preset.TestNativeManagerCRUDIndependenceAndRestart`, `imageresizer.TestNativeViewOverwriteNeedsConfirmation` | Pass |  |
| UX-003 | P0 | Validation beside the field, saying how to fix it | FND-05, FND-07, QLT-01 | `ui.TestDimensionInputPresentsValidation`, `preset.TestNativeManagerValidationAndBuiltInProtection` | Pass |  |
| UX-004 | P0 | `px` labels and `width x height` | FND-05, FND-07, QLT-01 | `ui.TestFormatDimensions` | Pass |  |
| UX-005 | P0 | Status is never color alone | FND-05, QLT-01 | `ui.TestNoticeTextUsesExplicitStatus`, `imageresizer.TestNativeViewResizesEveryReadyImageEndToEnd` | Pass |  |
| UX-006 | P0 | Keyboard navigable with visible focus | FND-05, QLT-01 | `app.TestTabReachesEveryControlInLayoutOrder`, `app.TestScreenshotOptionsKeyboardOnly`, `app.TestResizerSourcesKeyboardOnly` | Pass | M2 |
| UX-007 | P0 | WCAG 2.1 AA contrast | FND-05, QLT-01 | `overlay.TestContrastRatioFollowsWCAG`, `overlay.TestRingColorsStandOutOnAnyBackground`, [accessibility record](accessibility-verification.md) | Pass | M3 |
| UX-008 | P1 | Windows light/dark preference | QLT-01 | `app.TestStaticTextBrushUsesSystemColors`, [accessibility record](accessibility-verification.md) | **Partial** | Deferred by PRD section 20: no dark app mode. M3 |
| UX-009 | P0 | English, with centralized strings | FND-05, QLT-01 | [accessibility record](accessibility-verification.md) | Pass |  |
| UX-010 | P0 | Distinct error categories | FND-05, SCR-10, QLT-01, QLT-03B, QLT-03C | `ui.TestErrorKindsHaveDistinctPresentation`, `imageresizer.TestErrorKindClassifiesFailures` | Pass |  |

## Non-functional requirements

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| NFR-001 | P0 | 64-bit Windows 11 | QLT-04, QLT-04A | `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly`, [platform record](platform-verification.md) | **Partial** | QLT-04A: x64 hardware, other Windows 11 releases |
| NFR-002 | P0 | No administrator rights | QLT-04 | `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` | Pass |  |
| NFR-003 | P0 | Per-monitor DPI, 100-500%, negative coordinates, disconnect | SCR-02, QLT-04, QLT-04A | `display.TestLogicalPhysicalConversion`, `overlay.TestDisplayChangeResolvesScalingOnUnchangedTopology` | **Partial** | QLT-04A: all but 100%, live |
| NFR-004 | P1 | Arm and 32-bit are not targets | QLT-04 | [platform record](platform-verification.md) | Pass |  |
| NFR-010 | P1 | Launch within 2 s | QLT-04, QLT-04A | `app.BenchmarkWarmLaunchToResponsiveMainWindow`, [platform record](platform-verification.md) | **Partial** | QLT-04A: the reference machine |
| NFR-011 | P1 | Overlay within 300 ms | SCR-11, QLT-04, QLT-04A | `overlay.BenchmarkOverlayAppearFreeRectangle`, `overlay.BenchmarkOverlayAppearPresetRectangle`, [platform record](platform-verification.md) | **Partial** | QLT-04A: **missed** in 2 of 6 batches under x64 emulation; x64 hardware not measured |
| NFR-012 | P1 | 4K capture to clipboard within 1 s | SCR-11, QLT-04, QLT-04A | `capture.BenchmarkCaptureRectVirtualDesktop`, `screenshot/output.BenchmarkSetClipboardImage4K`, [platform record](platform-verification.md) | **Partial** | QLT-04A: the reference machine |
| NFR-013 | P1 | 100 12 MP JPEGs within 2 minutes | RSZ-09, QLT-04, QLT-04A | `job.BenchmarkRun100Mixed12MegapixelJPEGsFit1024`, [platform record](platform-verification.md) | **Partial** | QLT-04A: the reference machine |
| NFR-014 | P0 | Bounded batch memory | RSZ-07, RSZ-09, QLT-04 | `job.TestRunBoundsWorkersAndMemory`, `job.BenchmarkRun100Mixed12MegapixelJPEGsFit1024` | Pass |  |
| NFR-020 | P0 | Sources never silently modified | RSZ-05, QLT-02, QLT-03D | `imageresizer.TestAcceptanceR07OriginalsPreserved`, `imageresizer/output.TestPrepareNeverOverwritesASource` | Pass |  |
| NFR-021 | P0 | Item errors are contained | RSZ-07, QLT-02 | `job.TestRunIsolatesItemFailures`, `resize.TestResizeContainsASourcePanic` | Pass |  |
| NFR-022 | P0 | Shutdown unregisters, closes, stops work | SCR-10, QLT-02 | `app.TestExitDuringACaptureSessionLeavesNoOverlayOrShortcut`, `app.TestEndSessionSavesAndStopsWithoutAsking` | Pass | M4 |
| NFR-023 | P0 | No partial files at final paths | RSZ-06, QLT-02, QLT-03A | `screenshot/output.TestSaveEncodeFailureLeavesNoFiles`, `imageresizer/output.TestWriteRemovesTheTemporaryFileWhenEncodingFails` | Pass | M5 |
| NFR-024 | P1 | Logs rotate | FND-04, QLT-02 | `diagnostics.TestLoggerRotatesStructuredEvents` | Pass |  |
| NFR-030 | P0 | Works locally and offline | QLT-03 | `app.TestCoreWorkflowsRunOffline`, `audit.TestExecutableContainsNoNetworkCode` | Pass | M6 |
| NFR-031 | P0 | No account, telemetry, or upload | QLT-03 | `audit.TestApplicationLinksNoNetworkPackages`, `screenshot/output.TestSetClipboardImageOptsOutOfCloudUploadOnly` | Pass | M7 |
| NFR-032 | P0 | Files only at user-selected and own paths | QLT-03, QLT-03B, QLT-03C | `audit.TestFileChangesStayAtReviewedSites`, `app.TestCoreWorkflowsRunOffline` | Pass |  |
| NFR-033 | P0 | Dependencies inventoried and licenses reviewed | QLT-03 | `audit.TestDependenciesAreInventoried`, `release.TestNoticesCarryTheLinkedLicenses` | Pass | M13 |

## Technical guardrails

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| TECH-001 | P0 | Implemented in Go | FND-01, FND-02 | [ADR-0001](adr/0001-native-windows-stack.md) | Pass |  |
| TECH-002 | P0 | GUI stack demonstrated before commitment | FND-01 | [ADR-0001](adr/0001-native-windows-stack.md) | **Partial** | QLT-04A: the mixed-DPI blocker in ADR-0001 |
| TECH-003 | P0 | DPI-aware; explicit physical-pixel conversion | FND-01, SCR-02 | `display.TestLogicalPhysicalConversion`, `display.TestRectConversion` | **Partial** | QLT-04A: any scaling but 100%, live |
| TECH-004 | P0 | Separate modules behind a shared shell | FND-03 | `shell.TestNavigatorPreservesModuleViewState`, `preset.TestLibrariesHaveIndependentNamespaces` | Pass |  |
| TECH-005 | P0 | Heavy work runs off the GUI thread | FND-03, SCR-03, RSZ-03, RSZ-07 | `imageresizer.TestNativeViewStaysResponsiveDuringARealJob`, `capture.TestCaptureConcurrentCallsDoNotDeadlock`, `shell.TestDispatchQueueConcurrentPost` | Pass |  |
| TECH-006 | P0 | Bounded, cancellation-aware concurrency | RSZ-07 | `job.TestRunBoundsWorkersAndMemory`, `job.TestRunCancellationKeepsCompletedResults` | Pass |  |
| TECH-007 | P0 | Verified, maintained dependencies | FND-01, QLT-03 | `audit.TestDependenciesAreInventoried`, [privacy record](privacy-verification.md) | Pass | M13 |
| TECH-008 | P0 | Documented, versioned settings format | FND-04 | `settings.TestStoreMigrationFixture`, `app.TestOpenSessionUpgradesEverySettingsFormat` | Pass |  |
| TECH-009 | P0 | Automated test coverage | FND-02, SCR-11, RSZ-09, REL-02 | `preset.TestValidationBoundaries`, `resize.TestPlanFitProperties`, `imageresizer/output.TestPrepareNeverOverwritesASource`, `settings.TestStoreMigrationFixture`, `job.TestRunIsolatesItemFailures`, `capture.TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries` | **Partial** | QLT-04A: mixed-DPI capture has not run |
| TECH-010 | P1 | Executable needs no Go toolchain | FND-02, REL-01 | `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly`, [ADR-0002](adr/0002-product-identity-and-distribution.md) | Pass | M1 |

## Screenshot acceptance

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| AC-S01 | — | Exact preset at standard scaling | SCR-11 | `overlay.TestPresetFrameCaptureDeliversExactRect`, `capture.TestCaptureRectDimensionsAndContent` | Pass |  |
| AC-S02 | — | Exact preset at mixed DPI | SCR-11, QLT-04A | `capture.TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries`, `display.TestLogicalPhysicalConversion` | **Partial** | QLT-04A: mixed scaling, live |
| AC-S03 | — | Click-through interaction | SCR-11 | `overlay.TestPresetFrameExactSizeAndSurface`, `overlay.TestPresetFrameClickOnBorderLeavesSelectionUnchanged` | **Partial** | M9: no real click through the production frame |
| AC-S04 | — | Precision placement | SCR-11 | `overlay.TestPresetFrameBorderDragMovesWithoutResizing`, `overlay.TestPresetFrameKeyboardNudges` | **Partial** | M9: no real pointer has dragged the production frame |
| AC-S05 | — | Free rectangle adjustment | SCR-11 | `overlay.TestOverlayDimensionEditingAppliesExactSize`, `overlay.TestOverlayMoveAndResizeAdjustSelection` | Pass |  |
| AC-S06 | — | Full secondary display | SCR-11 | `capture.TestCaptureDisplay`, `screenshot.TestNativeViewFullDisplayCaptureSucceeds` | Pass |  |
| AC-S07 | — | Cancellation | SCR-11 | `overlay.TestOverlayEscapeCancelsWithoutOutput`, `screenshot.TestNativeViewRectangleCancelProducesNoOutput` | Pass |  |
| AC-S08 | — | Clipboard success with save failure | SCR-11 | `screenshot/output.TestDeliverSaveFailureKeepsClipboardSuccess`, `execute.TestFullDisplayReportsIndependentDeliveryOutcomes` | Pass |  |
| AC-S09 | — | Save success with clipboard failure | SCR-11 | `screenshot/output.TestDeliverClipboardFailureKeepsSaveSuccess`, `execute.TestRectangleReportsIndependentDeliveryOutcomes` | Pass |  |
| AC-S10 | — | Filename collision | SCR-11 | `screenshot/output.TestSaveAppendsNumericSuffixOnCollision` | Pass |  |
| AC-S11 | — | Shortcut conflict | SCR-11 | `screenshot.TestHotkeyConflictKeepsPreviousRegistration`, `screenshot.TestNativeViewShortcutConflictKeepsOldShortcut` | Pass |  |
| AC-S12 | — | Monitor removal | SCR-11, QLT-04A | `overlay.TestDisplayChangeEndsSessionThatCannotFit`, `screenshot.TestNativeViewDisplayChangeEndsStaleSession` | **Partial** | QLT-04A: a live disconnect |

## Image Resizer acceptance

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| AC-R01 | — | Stretch | RSZ-09 | `imageresizer.TestAcceptanceR01Stretch` | Pass |  |
| AC-R02 | — | Fit landscape | RSZ-09 | `imageresizer.TestAcceptanceR02FitLandscape` | Pass |  |
| AC-R03 | — | Fit portrait and rounding | RSZ-09 | `imageresizer.TestAcceptanceR03FitPortraitAndRounding` | Pass |  |
| AC-R04 | — | Crop to Fill | RSZ-09 | `imageresizer.TestAcceptanceR04CropToFill` | Pass |  |
| AC-R05 | — | Upscaling | RSZ-09 | `imageresizer.TestAcceptanceR05Upscaling` | Pass |  |
| AC-R06 | — | Mixed batch resilience | RSZ-09 | `imageresizer.TestAcceptanceR06MixedBatch` | Pass |  |
| AC-R07 | — | Originals preserved | RSZ-09 | `imageresizer.TestAcceptanceR07OriginalsPreserved` | Pass |  |
| AC-R08 | — | Rename collisions | RSZ-09 | `imageresizer.TestAcceptanceR08RenameCollisions` | Pass |  |
| AC-R09 | — | Skip collisions | RSZ-09 | `imageresizer.TestAcceptanceR09SkipCollisions` | Pass |  |
| AC-R10 | — | Confirmed overwrite | RSZ-09 | `imageresizer.TestAcceptanceR10ConfirmedOverwrite` | Pass |  |
| AC-R11 | — | PNG alpha | RSZ-09 | `imageresizer.TestAcceptanceR11PNGAlpha` | Pass |  |
| AC-R12 | — | EXIF orientation | RSZ-09 | `imageresizer.TestAcceptanceR12EXIFOrientation` | Pass |  |
| AC-R13 | — | Cancellation | RSZ-09 | `imageresizer.TestAcceptanceR13Cancellation` | Pass |  |
| AC-R14 | — | Write failure | RSZ-09 | `imageresizer.TestAcceptanceR14WriteFailure` | Pass | M11 |
| AC-R15 | — | Independent presets | RSZ-09 | `preset.TestNativeManagerScreenshotPresetAbsentFromImageResizer` | Pass |  |

## Shared acceptance

| ID | Priority | Requirement | Plan items | Evidence | Status | Open |
|---|---|---|---|---|---|---|
| AC-A01 | — | Persistence | QLT-02, QLT-04 | `app.TestOpenSessionUpgradesEverySettingsFormat`, `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` | Pass |  |
| AC-A02 | — | Offline use | QLT-03, QLT-04 | `app.TestCoreWorkflowsRunOffline` | Pass | M6 |
| AC-A03 | — | Keyboard access | QLT-01, QLT-04 | `app.TestTabReachesEveryControlInLayoutOrder` | Pass | M2 |
| AC-A04 | — | Responsive batch UI | QLT-04 | `imageresizer.TestNativeViewStaysResponsiveDuringARealJob` | **Partial** | M12: the window has not been dragged during a job |
| AC-A05 | — | Clean exit | QLT-02, QLT-04 | `app.TestExitDuringACaptureSessionLeavesNoOverlayOrShortcut`, `app.TestReleaseExecutableRunsAsStandardUserAndExitsCleanly` | Pass | M4 |

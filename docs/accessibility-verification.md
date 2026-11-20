# Accessibility, keyboard, contrast, and localization audit

This document is the QLT-01 record for the shared UX requirements in [PRD section 12](../PRD.md#12-ux-accessibility-and-localization), UX-001 through UX-010, and the keyboard-access scenario AC-A03 from [section 15.3](../PRD.md#153-shared-acceptance). It covers both modules and the application shell. It records how the audit was run, what it found and fixed, the result for each requirement, the tab order and access keys of every view, what screen readers are given, the measured contrast, the theme decision that resolved the PRD's open decision on light and dark themes, and what the results do not cover.

## How to run the checks

From the repository root on Windows:

```bash
go fmt ./...
go vet ./...
go test -count=1 ./...
```

`go test` runs every check below. To run only the audit's own tests:

```bash
go test -count=1 -v -run 'TestTabReaches|TestNavigationArrowKeys|TestScreenshotOptionsKeyboardOnly|TestResizerSourcesKeyboardOnly|TestMnemonics|TestShellRestoresFocus|TestShellLeavesAClick|TestShellAnnounces' ./internal/app
go test -count=1 -v -run 'TestRadioButtons|TestLiveLabels' ./internal/ui
go test -count=1 -v -run 'TestFreeRectangle|TestCancelCommitsNoTypedSize|TestOverlayDragDrawsAndCapturesSelection|TestToolbarRestoresFocus|TestToolbarInstructions|TestRingColors|TestContrastRatio|TestTypedSelection' ./internal/screenshot/overlay
go test -count=1 -v -run TestNativeViewAnnouncesItsStartupNotice ./internal/screenshot
go test -count=1 -v -run TestNativeViewAnnouncesAJobsStartAndSummaryOnly ./internal/imageresizer
```

The window tests create real Win32 windows and the capture overlay, so run them in an interactive desktop session, as the module suites are run. The keyboard tests in `internal/app` build the real main window with every production module on a temporary settings file. They register the Screenshot global shortcut as Ctrl+Alt+Shift+F24 rather than the default Ctrl+Alt+S, so a running copy of the application keeps its shortcut. `internal/ui/uiatest` is a test-only package: it reads controls through a UI Automation client on a thread of its own, as screen readers read them, and counts live-region announcements through a WinEvent hook.

Windows CI (`.github/workflows/ci.yml`) runs the same commands on every pull request, so the audit's tests gate every merge.

## Environment of record

Results below were recorded on 2026-11-20:

| Property | Value |
|---|---|
| OS | Windows 11 Home 26H2, build 26300 (ARM64) |
| Device | HP OmniBook 5 Laptop 14-he0xxx, Snapdragon X Plus X1P42100, 32 GB RAM |
| Go | go1.26.5 windows/arm64; the `internal/ui`, `internal/app`, `internal/screenshot`, `internal/screenshot/overlay`, and `internal/imageresizer` tests also ran as AMD64 binaries built with `GOARCH=amd64` under Windows' x64 emulation, as the live-region call passes its arguments differently on the two architectures |
| Theme | The default Windows light theme, with the system colors in [Contrast](#contrast); the contrast themes were measured from their theme files, not switched on (see [what these results do not cover](#what-these-results-do-not-cover)) |
| Display | The primary display at 100% scaling |

## Method

- **Keyboard.** Each view of the real main window was walked with Tab and Shift+Tab through the dialog manager (`IsDialogMessageW`), the path the shell's message loop takes. The walk recorded every control reached, the name a screen reader announces for it, and whether anything changed on the way. The keyboard-only workflows then configured each module from the keyboard alone: arrow keys in radio groups and combo boxes, typing, Enter on buttons, and the labels' access keys. Every visible control that is a tab stop must be reached, and no two visible controls in a view may share an access key.
- **Screen readers.** The names, live-region settings, and announcements were read through UI Automation, the interface Narrator, NVDA, and JAWS read standard Win32 controls through. No screen reader was listened to (see [what these results do not cover](#what-these-results-do-not-cover)).
- **Contrast.** WCAG 2.1 relative luminance and contrast ratios were computed from the system colors: those `GetSysColor` returns on the environment of record, and those the four Windows 11 contrast themes set in `%SystemRoot%\Resources\Ease of Access Themes`. The application draws its windows only in system colors, which `app.TestStaticTextBrushUsesSystemColors` pins for the shell's text, so these are the colors it shows.
- **Strings.** Every package was searched for user-facing text outside `internal/ui/strings.go`.

## Findings and fixes

| Finding | Requirement | Fix | Pinned by |
|---|---|---|---|
| Tab could not reach a module's controls. Every navigation radio button was a tab stop, and a radio button that gains the focus unchecked reports a click, so Tab switched to Image Resizer, then to Settings, then back to Screenshot, never leaving the navigation. The capture-mode and collision-policy radio groups changed their selection the same way as Tab passed through, and their labels' access keys (Alt+C, Alt+X) selected the group's first button. | UX-006, AC-A03 | A radio button is a tab stop exactly while it is checked (`ui.Button.SetChecked`), so Tab enters a group at its selection and the arrow keys move within it. The shell's navigation buttons are set through it too. | `app.TestTabReachesEveryControlInLayoutOrder`, `app.TestScreenshotOptionsKeyboardOnly`, `app.TestResizerSourcesKeyboardOnly`, `ui.TestRadioButtonsAreTabStopsOnlyWhileChecked` |
| The keyboard focus was lost whenever the window was activated. Windows focuses a plain window, not the control in it, at startup, on Alt+Tab, and when the window returns after a capture. No control then showed focus, and Tab restarted from the first control. The capture toolbar lost its field the same way on Alt+Tab, or when the global shortcut brought it back, so typed digits went nowhere. | UX-006, SCR-015 | The shell remembers the focused control and gives it the focus back on activation (`restoreFocus`). A control disabled meanwhile, as Cancel Resize is when a job finishes while the window is inactive, hands it to the next control in tab order; one whose module is hidden, or none at startup, leaves it to the current module's navigation button. A click that activates the window keeps the focus it gives: the focus goes back only once the click is handled, and only if the click left it on the window itself, as a click on the title bar does. The capture toolbar gives its focus back the same way, and otherwise keeps it on the toolbar itself. | `app.TestShellRestoresFocusOnActivation`, `app.TestShellLeavesAClickActivationsFocusToTheClick`, `overlay.TestToolbarRestoresFocusOnActivation` |
| Free Rectangle capture needed a pointer: the toolbar's width and height fields stayed disabled until a rectangle was dragged. | UX-006 (the PRD now adds SCR-015) | The fields are usable from the start, and the session starts in the width field. Typing a size and pressing Enter, or leaving the fields with both filled, creates a rectangle of exactly that size, centered on the cursor display; moving from one field to the other is no request, so a corrected pair is still centered. Tab out of the filled pair creates the rectangle before the focus moves, so it lands on Capture, which the rectangle enables, rather than passing it, still disabled, for Cancel. The arrow keys then nudge the rectangle by one pixel, or ten with Shift. Drawing with the pointer hands the focus from a field to the toolbar, so Enter then presses Capture, and cancelling commits nothing still typed. | `overlay.TestFreeRectangleTypedSizeCreatesSelection`, `overlay.TestFreeRectangleLeavingTypedSizeCreatesSelection`, `overlay.TestFreeRectangleTabFromFilledPairReachesCapture`, `overlay.TestFreeRectangleArrowKeysNudgeSelection`, `overlay.TestOverlayDragDrawsAndCapturesSelection`, `overlay.TestCancelCommitsNoTypedSize`, `overlay.TestTypedSelectionCentersTypedSize` |
| Results and errors were silent to screen readers. They appear only as static text, which a screen reader reads only when the user moves to it, so nothing was heard after Start Capture, Start Resize, or a refused field. | Screen-reader metadata (QLT-01) | Status areas, validation messages, the capture toolbar's message, and the Image Resizer's detail line are polite UI Automation live regions (`ui.NewLiveLabel`), announced when they change. A resize job announces its start and summary, not its count for every image (`ui.StatusArea.Update`). A notice set where no one can hear it is announced once it can be: the shell's startup warning when the window first appears, and a module's notice, such as a global shortcut another application holds at startup, the first time the module is shown in the visible window. The marking is kept in `oleacc.dll`, which COM unloaded with the apartment, losing it, until the package held the DLL loaded; the readback test found this. As `oleacc.dll` keeps markings by window handle, the capture toolbar clears its message's marking when the session ends (`ui.Label.ClearLive`). | `ui.TestLiveLabelsAreLiveRegions`, `ui.TestLiveLabelsAnnounceWhatChanged`, `imageresizer.TestNativeViewAnnouncesAJobsStartAndSummaryOnly`, `app.TestShellAnnouncesStartupNoticeOnceShown`, `app.TestShellAnnouncesEachModulesNoticeWhenFirstShown`, `screenshot.TestNativeViewAnnouncesItsStartupNotice` |
| The Image Resizer source list was announced by its long hint ("Drop PNG or JPEG files — or a folder of images —…"), as Windows names a list box after the static text just before it. | Screen-reader metadata | The hint is created ahead of the "Source images" label, though still laid out below it. | `app.TestTabReachesEveryControlInLayoutOrder` |
| The capture selection ring's blue core (`COLOR_HIGHLIGHT`, #0078D7) and black outline both contrast less than 3:1 with dark-gray content of relative luminance between about 0.03 and 0.1. One such case is a Preset Rectangle frame over a dark editor. | UX-007 | The ring and handles are drawn in two system colors that contrast at least 9:1, so one of them contrasts at least 3:1 with any background (WCAG technique C40). See [Contrast](#contrast). | `overlay.TestRingColorsStandOutOnAnyBackground` |
| The capture toolbar's message label was 46 pixels tall at 100% scaling, but two lines of its 18-pixel font need 50, so the second line of the instructions was clipped. | UX-007 (legibility) | The label is 50 logical pixels tall, and the instructions are worded to fit two lines. The font is the main window's, so the two lines fit while the main window's display and the cursor display share a scaling (see [Limitations](#limitations-and-their-resolutions)). | `overlay.TestToolbarInstructionsFitTheirTwoLines` |
| Pixel values without `px`: the display picker's resolutions, the fitting-mode descriptions, and the desktop size in the preset-too-large message. | UX-004 | Each is labeled. | `screenshot.TestDisplayListItemDescribesDisplay`, `imageresizer.TestNativeViewModeDescriptionFollowsPresetSelection` |
| User-facing text outside `strings.go`: the error-category and severity labels, the dimension validation messages, the dimension field names, the built-in preset names, and the Screenshot module's reasons for a shortcut conflict, an unavailable service, and a preset that does not fit. | UX-009 | All moved to `internal/ui/strings.go`. | `ui.TestErrorKindsHaveDistinctPresentation`, `ui.TestParseDimension`, `preset.TestBuiltInDefinitions`, and the module suites, unchanged in what they expect |

## Requirement results

| Requirement | Result | Evidence |
|---|---|---|
| UX-001 — Clear primary action labels | Pass | Start Capture, Capture, Cancel, Add Images…, Start Resize, Cancel Resize, and Open Output Folder are the buttons' own text and accessible names (the tab orders below; `overlay` toolbar tests). |
| UX-002 — Confirmation of destructive actions | Pass | Deleting a custom preset and a job under Overwrite each ask through `ui.Confirm`, whose default answer is No: `preset.TestNativeManagerCRUDIndependenceAndRestart`, `imageresizer.TestNativeViewOverwriteNeedsConfirmation`. |
| UX-003 — Validation beside the field | Pass | Each text and dimension field shows its message directly below it (`ui.TestDimensionInputPresentsValidation`, `preset.TestNativeManagerValidationAndBuiltInProtection`). The capture toolbar shows its message in its own message line, and Start Resize's refusals appear above the source list and beside the field (`imageresizer.TestNativeViewExplainsAFieldTheOptionsRefuse`). Each message says how to correct the problem and is announced (live region). |
| UX-004 — `px` and `width × height` | Pass | Dimensions use `ui.FormatDimensions` (`ui.TestFormatDimensions`), and field labels end in `(px)`. The strings that lacked `px` are fixed above. Built-in Image Resizer presets keep names such as `16 × 16`, a name followed in the list by its `16 × 16 px` dimensions. |
| UX-005 — Not by color alone | Pass | No status is a color. Notices begin with their severity in words (`ui.TestNoticeTextUsesExplicitStatus`), source and job rows end in their state in words (`imageresizer.TestNativeViewResizesEveryReadyImageEndToEnd`), and errors begin with their category (`ui.TestErrorKindsHaveDistinctPresentation`). The only custom drawing, the selection ring and handles, marks a rectangle by shape. |
| UX-006 — Keyboard navigable with visible focus | Pass | Every control of every view is reached in layout order, both ways, without changing anything; every option and list action works from the keyboard alone; focus returns on activation, to the control that had it or the next one (the tests in the findings). Focus is shown by the standard controls' own focus rectangles and caret, which follow the Windows keyboard-cues setting (see [Limitations](#limitations-and-their-resolutions)). |
| UX-007 — WCAG 2.1 AA contrast | Pass | Every text and control-boundary pair meets its target in the default theme and the four contrast themes ([Contrast](#contrast)), as does the selection ring over any background. |
| UX-008 (P1) — Windows light/dark preference | Partial, as the PRD now decides | The application follows the contrast themes and does not follow the Windows dark app mode, which the PRD records as a confirmed decision for the initial release and a future consideration ([Theme](#theme-and-high-contrast-behavior)). |
| UX-009 — Centralized strings | Pass | Every sentence and label the application shows is defined in `internal/ui/strings.go` ([Localization readiness](#localization-readiness)). |
| UX-010 — Distinct error categories | Pass | `ui.FormatError` labels validation, unsupported-format, read/decode, write/encode, clipboard, shortcut-registration, capture, and unexpected failures distinctly (`ui.TestErrorKindsHaveDistinctPresentation`, `screenshot.TestCaptureFailureMessage`, `imageresizer.TestErrorKindClassifiesFailures`, `screenshot.TestNativeViewShortcutConflictKeepsOldShortcut`). |
| AC-A03 — Keyboard access | Pass | `app.TestTabReachesEveryControlInLayoutOrder` and the keyboard-only workflow tests. Every main-window action is reachable by keyboard with visible focus. |
| SCR-015 (P1) — Free Rectangle without a pointer | Pass | `overlay.TestFreeRectangleTypedSizeCreatesSelection`, `overlay.TestFreeRectangleLeavingTypedSizeCreatesSelection`, `overlay.TestFreeRectangleTabFromFilledPairReachesCapture`, `overlay.TestFreeRectangleArrowKeysNudgeSelection`, `overlay.TestToolbarRestoresFocusOnActivation`. |

## Keyboard reference

Tab order of each view, from its navigation button, with the name a screen reader announces. Controls that do not apply are disabled and skipped until they do. Examples are Open Folder before a capture, JPEG quality while PNG is selected, Remove Selected and Clear List with nothing selected or listed, Cancel Resize outside a job, and Rename / Save and Delete for a built-in preset. `app.TestTabReachesEveryControlInLayoutOrder` pins these lists, and Shift+Tab reaches the same controls in reverse.

| Screenshot | Image Resizer | Settings |
|---|---|---|
| Screenshot (navigation) | Image Resizer (navigation) | Settings (navigation) |
| Start Capture | Start Resize | |
| The selected capture mode | Cancel Resize, Open Output Folder (when they apply) | |
| Display (Full Display only) | Source images | |
| Save format | Full path | |
| JPEG quality (1–100) (JPEG only) | Add Images…, Remove Selected, Clear List | |
| Global shortcut | Output folder | |
| Output folder | Browse… | |
| Browse… | Output format | |
| Open Folder (after a capture) | JPEG quality (1–100) | |
| Presets | JPEG transparency background (#RRGGBB) | |
| Name, Width (px), Height (px) | The selected collision policy | |
| Create Preset, Rename / Save, Duplicate, Delete | Presets, Name, Width (px), Height (px), Fitting mode | |
| | Create Preset, Rename / Save, Duplicate, Delete | |

Keys beyond Tab:

- **Navigation and radio groups.** Up and Down move between the modules, the capture modes, and the collision policies, selecting each, as in any Windows radio group.
- **Combo boxes.** Up and Down change the selection, and Alt+Down opens the list.
- **Access keys.** Alt with the underlined letter focuses a field, or the selected button of a radio group. Screenshot uses C (capture mode), D (display), F (save format), Q (JPEG quality), G (global shortcut), O (output folder), B (Browse), P (presets), N (name), W (width), and H (height). Image Resizer uses I (Add Images), S (source images), A (full path), R (Remove Selected), C (Clear List), O, B, M (output format), Q, J (JPEG transparency background), X (collision policy), P, N, W, H, and F (fitting mode). `app.TestMnemonicsAreUniqueInEachModule` pins that no two visible controls in a view share one.
- **Enter and Space.** Either presses the focused button. Enter in a field does not press Start Capture or Start Resize: Enter in the global shortcut or preset name field is easily meant as applying what was typed, so the primary action is reached with Tab, directly after the navigation.
- **Capture toolbar.** A Free Rectangle session starts in W (px), and a Preset Rectangle session on the toolbar itself. Tab moves between W (px), H (px), Capture, and Cancel, skipping Capture while it is disabled, before a selection exists; Tab out of a filled W and H creates the typed selection first and lands on Capture. Enter in a dimension field applies the size, or, with nothing drawn, creates a selection of that size. Elsewhere on the toolbar, as after a draw, which hands the focus to the toolbar, Enter presses Capture. The arrow keys nudge the selection outside the fields (Shift for ten pixels), and Escape or Alt+F4 cancels. When the toolbar is activated again, by Alt+Tab or the global shortcut, the control that had the focus gets it back.

## Screen-reader support

Standard Win32 controls are exposed to screen readers by Windows' own UI Automation and MSAA proxies. A button, radio button, or check box is named by its text. An edit field, combo box, or list box is named by the static label just before it in tab order, which every field has; the tab orders above list each control's name as UI Automation reads it. The application adds live regions, marked through Dynamic Annotation (`IAccPropServices::SetHwndProp`), for the text that reports what happened:

| Live region | Announced |
|---|---|
| Each module's status area (Screenshot, Image Resizer, both preset managers) | Every notice `Show` presents, an identical one included; a Screenshot notice set before the module was first shown in the visible window, once it is |
| The shell's status area | When its text changes, as a settings save fails or succeeds again; a repeated identical warning is not announced again. A startup warning, set while the window is hidden, once the window first appears |
| The Image Resizer's job section | A job's start, its Cancel, and its summary; the count for every image is not announced (`Update`) |
| Each field's validation message | When its text changes; a repeated or cleared message is not announced again |
| The capture toolbar's message | The instructions when a session starts, and each refused size, even a repeated one |
| The Image Resizer's detail line for the selected image | When it changes, as the selection moves or the image's job state changes |

Live regions are polite: the screen reader finishes what it is reading first.

## Contrast

All text in the application is drawn in `COLOR_WINDOWTEXT` on `COLOR_WINDOW`, or `COLOR_BTNTEXT` on `COLOR_BTNFACE` for buttons, so its contrast is the theme's own. Ratios below are WCAG 2.1 contrast ratios. Targets are 4.5:1 for text and 3:1 for control boundaries; disabled controls have none.

| Pair | Default (this machine) | Aquatic | Desert | Dusk | Night sky | Target |
|---|---|---|---|---|---|---|
| Text on window | 21.0 (#000000 on #FFFFFF) | 16.3 | 10.4 | 13.0 | 21.0 | 4.5 |
| Button text on button face | 18.4 (#000000 on #F0F0F0) | 16.3 | 15.7 | 10.8 | 17.5 | 4.5 |
| Selected list item (highlight text on highlight) | 4.5 (#FFFFFF on #0078D7) | 7.9 | 7.0 | 7.3 | 8.0 | 4.5 |
| Control edge (3D dark shadow on window) | 5.5 (#696969 on #FFFFFF) | 16.3 | 20.2 | 13.0 | 21.0 | 3.0 |
| Disabled text on window | 5.2 (#6D6D6D on #FFFFFF) | 6.7 | 5.4 | 5.3 | 8.6 | none |

The controls are drawn without visual styles, as the executable carries no Common Controls 6 manifest. Each edit, list, and button is outlined by a sunken or raised 3D edge whose dark side is the 3D dark shadow above. The focus rectangle is drawn by inverting the pixels it crosses, so it is black on white and white on black.

The capture overlay's selection ring and handles are drawn over arbitrary screen content, so they use two colors with a one-pixel edge of one on each side of the other. When the two contrast at least 9:1 with each other, one of them contrasts at least 3:1 with any background. The colors are the first pair of system colors with that contrast:

| Theme | Highlight / highlight text | Window text / window | Ring core and edge |
|---|---|---|---|
| Default | 4.5 | 21.0 | White core, black edge |
| Aquatic | 7.9 | 16.3 | #FFFFFF core, #202020 edge |
| Desert | 7.0 | 10.4 | #FFFAEF core, #3D3D3D edge |
| Dusk | 7.3 | 13.0 | #FFFFFF core, #2D3236 edge |
| Night sky | 8.0 | 21.0 | #FFFFFF core, #000000 edge |
| Windows 10 High Contrast Black | 14.4 | 21.0 | #1AEBFF (highlight) core, #000000 edge |

`overlay.TestRingColorsStandOutOnAnyBackground` checks each theme against 4,352 colors: a lattice of 16 levels per channel, and every gray. The Free Rectangle tint dims the desktop outside the selection to about 62% brightness. It is a backdrop, not a boundary, and the selection's own ring marks the edge.

## Theme and high-contrast behavior

The PRD's open decision 5, on the initial light and dark theme support, is resolved: the initial release draws every window in the Windows system colors and follows the Windows contrast themes, and it does not follow the Windows dark app mode. Standard Win32 controls have no documented dark rendering. Following the dark mode would take undocumented `uxtheme.dll` entry points that a Windows update can change. So the application stays light and legible, with the contrast above, when Windows apps are set to dark. The PRD records this in section 20 and lists a dark theme in section 18. UX-008 is P1, so the deferral needs no release blocker, and the release notes (REL-02) must say so.

Every window and control is drawn in system colors, so a contrast theme applies to all of them, the capture toolbar included. The Free Rectangle tint is translucent black in every theme, and the selection ring and handles follow the theme as the table in [Contrast](#contrast) shows.

## Localization readiness

Every sentence and label the application shows is a constant in `internal/ui/strings.go`, and feature packages format their dynamic details with those constants. A translation replaces that file. Two kinds of text are not translated there, by design:

- **Error details from Windows and Go.** An error value's text, and the operation a lower layer names around it (`enumerate displays: …`), appears only after one of the centralized sentences, as the `%v` detail. A typed error's kind, not its text, chooses the sentence and its category. Windows formats its own messages in the user's language.
- **The shortcut grammar.** The modifier names in a global shortcut (`Ctrl`, `Alt`, `Shift`, `Win`) are also the form the shortcut is stored in and typed in, so they stay a fixed grammar. A localized display would map them rather than replace them.

The layout measures every text at run time, so longer translated strings wrap rather than clip, except the capture toolbar's message line, which holds two lines (`overlay.TestToolbarInstructionsFitTheirTwoLines`).

## Limitations and their resolutions

- **Dark app mode.** Not followed, as decided above; documented for the release notes.
- **Focus rectangles and access-key underlines follow the Windows keyboard-cues setting.** Unless *Underline access keys* is on in Windows settings, Windows shows focus rectangles once the user navigates with the keyboard, and the underlines once Alt is pressed. This is standard Windows behavior for every application, and the keyboard user who needs them sees them.
- **Radio groups have no group name for screen readers.** A capture mode is announced as "Free Rectangle, radio button", without "Capture mode", as the groups use a label rather than a group box; the label is read when the user explores the view. This is left as it is.
- **The capture toolbar's fields are named "W (px)" and "H (px)".** The toolbar is too narrow for the full words. The instructions beside them say what they are.
- **The capture toolbar's message clips its second line on mixed scaling.** The toolbar is laid out at the cursor display's scaling but uses the main window's font, so with the main window on a display scaled higher than the cursor's, the second line of the instructions or a refusal is clipped. This predates the audit, a fix needs a font for each toolbar scaling, and mixed-DPI hardware is the case ADR-0001 leaves unvalidated. A screen reader still reads the whole message.
- **The image and folder pickers are Windows' own dialogs**, whose accessibility is Windows'. The Image Resizer also accepts drops; there is no keyboard alternative to the picker for adding files.
- **The capture overlay itself is not a main-window workflow.** AC-A03 covers the main window. The overlay's keyboard path (SCR-015, SCR-027) is pinned by the overlay tests.

## What these results do not cover

- **A real screen reader.** No one listened to Narrator, NVDA, or JAWS. The names and live-region settings were read through UI Automation and the announcements counted as the events Windows raises, which is what screen readers consume, but how each screen reader words and times them was not heard.
- **The contrast themes switched on.** Turning on a contrast theme changes a system setting, which the audit did not do. The themes were measured from their theme files, and the application draws only in the system colors those themes set, but no screenshot of the application in a contrast theme was taken. Before release, switch each theme on and look at both modules and the capture overlay.
- **A person at the keyboard.** The keyboard tests hand key presses to the dialog manager from the GUI thread, as the shell's message loop does, rather than typing on a keyboard. The radio-button behavior they rely on, where a radio button that gains the focus unchecked reports a click and Tab stops only at tab stops, was also confirmed with plain Win32 controls outside the application. Activation is tested by sending `WM_ACTIVATE`, whose default handling focuses the window, rather than by switching applications, and a click activation by sending its `WA_CLICKACTIVE` code and then focusing the control clicked, rather than by clicking.
- **Other scaling factors.** Text measurement and the toolbar fit were checked at 100% scaling; the layout scales every measurement by the window's DPI. Mixed scaling, where the capture toolbar's message clips, was not available (see [Limitations](#limitations-and-their-resolutions)).

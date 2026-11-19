# GameDevTools — Implementation Plan

**Document status:** Living document\
**Version:** 1.0\
**Last updated:** 2026-11-10\
**Source requirements:** [PRD.md](PRD.md)\
**Overall status:** In Progress

## 1. Purpose

This plan converts the PRD into an ordered sequence of pull requests. It is the implementation tracking document for scope, dependencies, verification, and progress.

Each numbered item represents exactly one PR. Items are listed in implementation order, and every dependency refers to an earlier item. Stable item IDs are used in dependency fields so list numbering can change without breaking references.

## 2. Living-document rules

1. Implement items from top to bottom. Do not start an item until all its dependencies are **Done**.
2. Keep exactly one PR per item. If an item is too large, split it before implementation starts, assign stable IDs, and preserve backward-only dependencies.
3. A PR must include the tests, documentation, and migrations needed for its own scope. Do not defer feature-specific correctness to a later hardening item.
4. Update **Status**, **PR**, and **Notes** whenever work changes. Add the actual PR URL or number when opened.
5. Allowed statuses are **Pending**, **In Progress**, **Blocked**, and **Done**. Record the reason and blocking item whenever Blocked is used.
6. Mark an item Done only after its PR is merged, CI is green, its verification steps pass, and relevant documentation is updated.
7. Do not silently expand an item to include a later item's scope. Revise this plan first when requirements or technical decisions change.
8. New prerequisite work must be inserted before its dependents. After any reordering, verify that no item depends on a later item.
9. Requirement IDs refer to [PRD.md](PRD.md). Update both documents when scope or acceptance behavior changes.
10. Milestone status is Done only when every item in that milestone is Done and its exit criteria are satisfied.

## 3. Status summary

| Milestone | Items | Status |
|---|---:|---|
| M0 — Technical validation and application foundation | 1–7 | Pending |
| M1 — Screenshot module | 8–18 | Pending |
| M2 — Image Resizer module | 19–27 | Pending |
| M3 — Cross-cutting quality and hardening | 28–31 | Pending |
| M4 — Distribution and release | 32–34 | Pending |

## 4. Milestone M0 — Technical validation and application foundation

**Milestone status:** Pending\
**Goal:** Select a viable Go/Windows GUI stack and establish the production application, CI, shared UI, local settings, and module-scoped preset infrastructure.

1. **FND-01 — Validate the Go/Windows GUI and native integration stack**
   - **Status:** In Progress | **PR:** [#1](https://github.com/CyberCute-Studio/GameDevTools/pull/1) | **Depends on:** None
   - **Requirements:** TECH-001, TECH-002, TECH-003, TECH-007.
   - **Deliver:** A minimal Go technical spike and an architecture decision record comparing viable GUI approaches. Demonstrate a topmost transparent overlay, click-through interior with interactive border, physical-pixel coordinates, mixed-DPI behavior, screen capture, clipboard image output, and global hotkey registration. Record selected libraries, versions, licenses, limitations, and rejected alternatives.
   - **Verify:** Run the spike on Windows with two different DPI settings where available. Confirm exact pixel coordinates, click-through hit testing, capture output, clipboard interoperability, and hotkey conflict reporting. Document any unverified capability as a blocker rather than assuming support.
   - **Notes:** Native Win32 via `golang.org/x/sys/windows` is selected. Two physical displays passed at 100% scaling; mixed-DPI runtime validation remains a blocker because different scaling settings were unavailable. See ADR-0001.

2. **FND-02 — Bootstrap the production Go project and quality gates**
   - **Status:** In Progress | **PR:** [#2](https://github.com/CyberCute-Studio/GameDevTools/pull/2) | **Depends on:** FND-01
   - **Requirements:** TECH-001, TECH-009, TECH-010.
   - **Deliver:** Go module, production package structure, Windows application entry point, debug/release build commands, unit-test setup, formatting and static-analysis checks, and Windows CI. Separate production code from the FND-01 spike.
   - **Verify:** A clean checkout builds and tests successfully on Windows using documented commands. CI runs the same checks and produces a basic Windows executable artifact.
   - **Notes:** Proceeding with the production foundation by explicit request while FND-01's mixed-DPI validation blocker remains open; dependent Screenshot capture work remains blocked.

3. **FND-03 — Create the application shell and module boundaries**
   - **Status:** In Progress | **PR:** [#3](https://github.com/CyberCute-Studio/GameDevTools/pull/3) | **Depends on:** FND-02
   - **Requirements:** APP-001, APP-002, TECH-004, TECH-005.
   - **Deliver:** Main window, navigation to Screenshot, Image Resizer, and Settings, placeholder module views, shared navigation/service interfaces, and GUI-thread dispatch mechanism. Establish module ownership so feature code does not leak into unrelated modules.
   - **Verify:** Navigate repeatedly between all views without losing in-memory view state or blocking the GUI thread. Unit-test module registration and navigation state where practical.
   - **Notes:** Placeholder views are not feature-complete module implementations.

4. **FND-04 — Add versioned settings, local data paths, and diagnostic logging**
   - **Status:** In Progress | **PR:** [#4](https://github.com/CyberCute-Studio/GameDevTools/pull/4) | **Depends on:** FND-03
   - **Requirements:** APP-003, SET-001, SET-002, SET-003, SET-004, SET-006, TECH-008, NFR-024.
   - **Deliver:** Per-user configuration and log locations, versioned settings format, safe read/write behavior, defaults, migration interface, malformed-settings backup/recovery, write-failure reporting, log redaction boundaries, and log rotation. Persist the selected module and window preferences supported by the shell.
   - **Verify:** Test fresh start, normal restart, malformed data, unavailable paths, failed writes, migration fixtures, and rotation. Confirm recovery does not silently delete the original settings file and saved images are never touched.
   - **Notes:** Feature modules add their own versioned settings sections through this shared service.

5. **FND-05 — Establish shared UI, validation, notification, and accessibility primitives**
   - **Status:** In Progress | **PR:** [#5](https://github.com/CyberCute-Studio/GameDevTools/pull/5) | **Depends on:** FND-03
   - **Requirements:** UX-001, UX-002, UX-003, UX-004, UX-005, UX-006, UX-007, UX-009, UX-010.
   - **Deliver:** Shared buttons, numeric dimension inputs, validation presentation, confirmation dialogs, status/error presentation, non-blocking notifications, keyboard focus behavior, centralized user-facing strings, and theme/contrast conventions.
   - **Verify:** Exercise shared controls using keyboard only and with invalid input. Check visible focus, readable errors, non-color-only statuses, dimension labels, and confirmation behavior.
   - **Notes:** Feature-specific validation remains in the owning module.

6. **FND-06 — Implement module-scoped preset domain and persistence**
   - **Status:** In Progress | **PR:** [#6](https://github.com/CyberCute-Studio/GameDevTools/pull/6) | **Depends on:** FND-04
   - **Requirements:** PRESET-001, PRESET-003, PRESET-004, PRESET-005, PRESET-006, PRESET-008.
   - **Deliver:** Stable preset identifiers, separate Screenshot and Image Resizer namespaces, built-in definitions, custom-preset storage, dimension limits, unique-name rules, module-specific option payloads, and corrupt-preset recovery.
   - **Verify:** Unit-test all validation boundaries, case-insensitive name conflicts, persistence, built-in immutability, independent namespaces, stable IDs after rename, and malformed stored data.
   - **Notes:** Initial PRD built-ins and Image Resizer Fit defaults are included. Domain, persistent custom storage, startup validation, and recovery are implemented; preset-management UI remains FND-07.

7. **FND-07 — Build reusable preset management UI**
   - **Status:** In Progress | **PR:** [#7](https://github.com/CyberCute-Studio/GameDevTools/pull/7) | **Depends on:** FND-05, FND-06
   - **Requirements:** PRESET-002, PRESET-003, PRESET-007, UX-002, UX-003, UX-004.
   - **Deliver:** Create, rename, duplicate, delete, and select workflows for custom presets; built-in duplication; validation and confirmation dialogs; and last-selected-preset restoration. Support module-specific fields without sharing preset records between modules.
   - **Verify:** Test every CRUD workflow and restart persistence through the public UI. Confirm deleting or renaming a Screenshot preset cannot affect Image Resizer.
   - **Notes:** Reusable native preset management is embedded in both feature modules, including module-specific fitting modes, confirmed custom deletion, and persisted last selection. Capture and resize execution remain in their later items.

**M0 exit criteria:** The selected stack has passed the native capability spike; the Windows app builds in CI; navigation, settings, logging, shared UI, and independent persistent preset libraries work; all M0 items are Done.

## 5. Milestone M1 — Screenshot module

**Milestone status:** Pending\
**Goal:** Deliver exact-pixel Free Rectangle, Preset Rectangle, and Full Display capture with clipboard/save output, global invocation, safe overlay lifecycle, and mixed-DPI support.

8. **SCR-01 — Add Screenshot settings and preset selection UI**
   - **Status:** In Progress | **PR:** [#8](https://github.com/CyberCute-Studio/GameDevTools/pull/8) | **Depends on:** FND-07
   - **Requirements:** SCR-061, SCR-063, SCR-064, SCR-067, PRESET-007.
   - **Deliver:** Screenshot module view, mode selection, Screenshot preset list/custom dimensions, PNG/JPEG selection, JPEG quality, output-folder selection, and filename-pattern display. Persist all supported options.
   - **Verify:** Validate settings and preset persistence, format/quality boundaries, output-path validation, and mode/preset selection through the UI.
   - **Notes:** The display picker is completed in SCR-05. This item does not perform captures. Capture options persist in a versioned `capture` object inside the Screenshot module section, validated independently from and preserved alongside the module's `presets` data.

9. **SCR-02 — Implement display topology and physical-pixel coordinate services**
   - **Status:** In Progress | **PR:** [#9](https://github.com/CyberCute-Studio/GameDevTools/pull/9) | **Depends on:** FND-02
   - **Requirements:** SCR-041, SCR-043, NFR-003, TECH-003.
   - **Deliver:** DPI-aware display enumeration, display identifiers/names, native resolutions, primary-display metadata, cursor-display selection, virtual-desktop bounds, negative-coordinate support, and explicit logical/physical conversion utilities. Expose display-change notifications.
   - **Verify:** Unit-test coordinate conversion and bounds calculations. Integration-test primary/secondary displays, mixed scaling, rotated displays where available, negative coordinates, and display changes.
   - **Notes:** This is a native service PR; the user-facing display picker is completed in SCR-05. The `internal/display` service enumerates physical-pixel bounds, effective DPI, device and friendly monitor names, and primary status; assigns deterministic picker numbers (primary first, then device name); resolves the cursor display with a nearest-display fallback; and forwards `WM_DISPLAYCHANGE` through the shell to subscribers. Integration-tested on two physical displays at 100% scaling; mixed-scaling and rotated-display hardware remains unavailable (see the FND-01 blocker), so those paths are covered by unit-level conversion and bounds tests only.

10. **SCR-03 — Implement raw Windows display and region capture**
    - **Status:** In Progress | **PR:** [#10](https://github.com/CyberCute-Studio/GameDevTools/pull/10) | **Depends on:** SCR-02
    - **Requirements:** SCR-042, SCR-044, SCR-068, TECH-005.
    - **Deliver:** Off-GUI-thread capture service for one display and arbitrary virtual-desktop rectangles, including regions spanning monitors. Return physical-pixel images and typed capture errors. Ensure app-owned capture surfaces can be excluded by hiding them before capture.
    - **Verify:** Validate output dimensions and representative pixel content on different DPI settings, secondary displays, display boundaries, and invalid/disconnected regions. Verify no GUI deadlock occurs.
    - **Notes:** Protected-content and graphics API limitations must be surfaced, not converted into blank success results. The `internal/screenshot/capture` service captures `internal/display` topology by rectangle or device name with typed validation/display/native errors, and excludes app-owned surfaces through a hide-flush-restore exclusion that preserves each window's prior visibility and activation. Integration-tested on two physical displays at 100% scaling, including a cross-display rectangle; mixed-scaling and negative-coordinate hardware remains unavailable (see the FND-01 blocker), and GDI composites DRM-protected content as black without an error, a limitation documented in the package and README for SCR-10 to present.

11. **SCR-04 — Implement clipboard and atomic screenshot file output**
    - **Status:** In Progress | **PR:** [#11](https://github.com/CyberCute-Studio/GameDevTools/pull/11) | **Depends on:** SCR-01, SCR-03, FND-04
    - **Requirements:** SCR-060, SCR-061, SCR-062, SCR-063, SCR-064, SCR-065, SCR-066, SCR-067.
    - **Deliver:** Clipboard image interop, PNG/JPEG encoding, pre-compression clipboard pixels, timestamped filenames, numeric collision suffixes, temporary-file writes, and independent clipboard/save result reporting.
    - **Verify:** Test PNG/JPEG dimensions and decoding, clipboard paste into a common Windows destination, filename collisions, unwritable destinations, simulated clipboard failure, partial-write cleanup, and independent success/failure combinations.
    - **Notes:** AC-S08 and AC-S09 failure cases are required in this PR, not deferred. The `internal/screenshot/output` service delivers one capture to the clipboard (original pixels, before JPEG encoding) and to a collision-free timestamped PNG/JPEG file written through a temporary file, reporting both outcomes independently. Capture execution and the completion notification are wired in SCR-05.

12. **SCR-05 — Complete Full Display capture end to end**
    - **Status:** In Progress | **PR:** [#12](https://github.com/CyberCute-Studio/GameDevTools/pull/12) | **Depends on:** SCR-04
    - **Requirements:** SCR-001, SCR-040, SCR-041, SCR-042, SCR-043, SCR-044, SCR-060, SCR-069.
    - **Deliver:** Display picker with cursor-display default, main-window hiding, automatic capture, clipboard/save execution, and non-blocking result notification with Open Folder action.
    - **Verify:** Capture primary and secondary displays at native resolution and orientation. Confirm the main window and cursor are absent, output format settings apply, and focus is not unexpectedly stolen afterward.
    - **Notes:** Global invocation is added in SCR-09; this PR completes main-window invocation. The shell's primary action button is wired to modules that implement it; the new `internal/screenshot/execute` pipeline hides the main window, captures the selected display off the GUI thread, restores it on every path without stealing focus, and delivers through `internal/screenshot/output`; shutdown waits for in-flight captures. Rectangle modes report that they arrive with later items. Multi-display and mixed-DPI hardware verification remains covered by the FND-01 blocker; picker selection, cursor default, notification outcomes, and shutdown behavior are unit-tested.

13. **SCR-06 — Build the rectangle overlay session foundation**
    - **Status:** In Progress | **PR:** [#13](https://github.com/CyberCute-Studio/GameDevTools/pull/13) | **Depends on:** SCR-03, SCR-05, FND-05
    - **Requirements:** SCR-050, SCR-051, SCR-052, SCR-053, SCR-056.
    - **Deliver:** Single-session overlay controller, transparent virtual-desktop surface, main-window hide/show coordination, floating toolbar base, Capture/Cancel command plumbing, and reliable cleanup on cancellation, failure, and exit.
    - **Verify:** Start/cancel repeatedly, attempt concurrent sessions, simulate overlay creation failure, and exit during a session. Confirm no invisible input-capturing surface or orphaned main-window state remains.
    - **Notes:** The `internal/screenshot/overlay` controller hides the main window, dims the virtual desktop with a translucent topmost overlay, and anchors a topmost toolbar (disabled Capture, Cancel) to the cursor display; the shell forwards session messages through a module filter for Escape and toolbar keyboard navigation. Cancelling, overlay-creation failure, and application exit all destroy the session windows and restore the main window. Selection drawing, exact-frame behavior, and capture execution are implemented in subsequent items.

14. **SCR-07 — Complete Free Rectangle capture end to end**
    - **Status:** In Progress | **PR:** [#14](https://github.com/CyberCute-Studio/GameDevTools/pull/14) | **Depends on:** SCR-06, SCR-04
    - **Requirements:** SCR-010, SCR-011, SCR-012, SCR-013, SCR-014, SCR-024, SCR-025, SCR-051, SCR-068.
    - **Deliver:** Drag-to-draw selection, live physical-pixel dimensions, move/resize handles, numeric dimension editing, edge-aware toolbar placement, capture/cancel actions, and output integration.
    - **Verify:** Test 1 × 1 selection, large and cross-monitor selections, moving/resizing, numeric edits, cancellation, and exact output content/dimensions without overlay artifacts.
    - **Notes:** The overlay renders through per-pixel-alpha compositing: the desktop stays dimmed while the selection interior is painted at the minimum alpha — visually clear, but hit-tested as solid so the interior keeps capturing the pointer input that moves it — and the frame ring is painted outside the captured rectangle; the handle squares astride its edges cannot reach the output because the session windows are destroyed and the desktop compositor is flushed before the pixels are read. The toolbar shows live width and height, accepts exact typed dimensions (invalid or non-fitting values are rejected and reverted), and follows the selection without covering it where practical. The Capture command destroys the session windows, reads the rectangle off the GUI thread while the main window is still hidden, then restores the window and delivers through the shared output service. AC-S05 and AC-S07 are covered by automated Windows integration tests in `internal/screenshot/overlay` and `internal/screenshot`; cross-monitor and mixed-DPI selection behavior is covered by virtual-desktop geometry unit tests, with hardware validation still gated by the FND-01 blocker.

15. **SCR-08 — Complete exact Preset Rectangle capture end to end**
    - **Status:** In Progress | **PR:** [#15](https://github.com/CyberCute-Studio/GameDevTools/pull/15) | **Depends on:** SCR-07, SCR-01
    - **Requirements:** SCR-020, SCR-021, SCR-022, SCR-023, SCR-024, SCR-025, SCR-026, SCR-027, SCR-028, SCR-029, SCR-030, SCR-031, SCR-055, SCR-068.
    - **Deliver:** Exact-size always-on-top frame, unobscured click-through interior, draggable border, fixed-size behavior, cross-monitor movement, toolbar Capture/Cancel and dimensions, X/Y readout, numeric ad hoc edits, one-pixel and ten-pixel keyboard nudges, and virtual-desktop validation.
    - **Verify:** Confirm underlying apps receive interior clicks while the frame stays in place; border and toolbar remain interactive; dimensions never drift across DPI changes; arrows move exact physical distances; overlays never appear in output; saved presets are not changed by ad hoc edits.
    - **Notes:** Include AC-S01 through AC-S04 and the oversized-region validation case. The preset floats as a per-pixel-alpha exact-size topmost frame instead of the dimmed overlay: the alpha-zero interior passes clicks to the applications beneath while the opaque border ring — painted outside the captured rectangle — is the only input-capturing surface and drags the frame anywhere in the virtual desktop without resizing it; the toolbar shows live dimensions and an X/Y readout, arrow keys nudge one physical pixel (ten with Shift), and typed dimensions resize the active frame exactly as ad hoc edits that never touch the saved preset. A preset larger than the entire virtual desktop is rejected with a validation message before anything hides. AC-S01 through AC-S04 are covered by automated Windows integration tests in `internal/screenshot/overlay` and `internal/screenshot`; live click-through into another application was validated in the FND-01 spike and is pinned here at the pixel-alpha mechanism level, and mixed-DPI hardware validation remains gated by the FND-01 blocker.

16. **SCR-09 — Add global capture invocation and single-instance behavior**
    - **Status:** In Progress | **PR:** [#16](https://github.com/CyberCute-Studio/GameDevTools/pull/16) | **Depends on:** SCR-08, SCR-05, FND-04
    - **Requirements:** APP-005, SCR-001, SCR-002, SCR-003, SCR-004, SCR-005, SCR-006.
    - **Deliver:** Configurable `Ctrl+Alt+S` default global shortcut, registration/conflict handling, persisted remapping, invocation of the selected capture mode, existing-session behavior, single-instance activation, and shortcut cleanup on exit.
    - **Verify:** Invoke every mode while another app is focused, simulate shortcut conflicts, change shortcuts without reinstalling, invoke during an active selection, launch a second instance, and verify clean unregistration.
    - **Notes:** Include AC-S11 and the shortcut portion of AC-A05. The Screenshot view registers the persisted shortcut on the shell window, which routes `WM_HOTKEY` to the module; the shortcut starts the selected mode like Start Capture, or flashes the active session's toolbar instead of stacking a second overlay. Registration conflicts keep the previous combination working while the user picks a replacement, and the registration releases on exit. A per-session named mutex gives one instance per user session; a second launch activates the existing window and exits. Live cross-application invocation, second-instance activation, and mixed-DPI behavior are tested at the registration/routing/windowing level; hardware validation remains gated by the FND-01 blocker.

17. **SCR-10 — Harden display changes, capture errors, and overlay recovery**
    - **Status:** In Progress | **PR:** [#17](https://github.com/CyberCute-Studio/GameDevTools/pull/17) | **Depends on:** SCR-09
    - **Requirements:** SCR-052, SCR-054, SCR-056, SCR-060, SCR-069, NFR-022, UX-010.
    - **Deliver:** Display-disconnect/reconnect handling, selection revalidation, protected/exclusive-content error presentation, graphics/capture failure recovery, independent output-result notifications, and shutdown cleanup for every capture state.
    - **Verify:** Disconnect the active display, change DPI/layout during selection, simulate capture/clipboard/save failures, and exit from each state. Confirm actionable errors and no invisible overlays, lost input, or false success.
    - **Notes:** The overlay controller subscribes to the display monitor — notified on both `WM_DISPLAYCHANGE` and `WM_DPICHANGED` — and revalidates the active session on every topology or scaling change: a selection that still fits keeps its exact dimensions and is moved back inside the new virtual desktop while the tint overlay or preset frame and the toolbar are resized, re-scaled, and repositioned; a selection that can no longer fit, an unreadable topology, or a native surface without display-change support ends the session with an explanatory warning, windows destroyed and main window restored. Typed capture errors present by kind with their recovery path: a fresh display selection for display changes, windowed mode for native graphics failures against full-screen exclusive applications, and the validation category for invalid geometry. Shutdown coverage pins the rectangle pipeline waiting for in-flight delivery, mirroring Full Display. AC-S12 is covered by Windows integration tests in `internal/screenshot/overlay` with simulated topology changes; AC-S08 and AC-S09 remain covered by the SCR-04 output tests and the view's independent-outcome presentation tests. Live disconnect/reconnect and mixed-DPI hardware validation remains gated by the FND-01 blocker and is owned by SCR-11.

18. **SCR-11 — Run the Screenshot acceptance and performance suite**
    - **Status:** In Progress | **PR:** [#18](https://github.com/CyberCute-Studio/GameDevTools/pull/18) | **Depends on:** SCR-10
    - **Requirements:** AC-S01 through AC-S12, NFR-011, NFR-012, TECH-009, SCR-031.
    - **Deliver:** Repeatable Windows integration test matrix, required test fixtures, performance measurements for overlay appearance and 4K capture-to-clipboard, and fixes for defects found within Screenshot scope. Record environments and results in the test artifacts or release documentation.
    - **Verify:** Pass every Screenshot acceptance scenario, including 100/125/150/175/200% scaling, secondary and negative-coordinate displays, cross-monitor capture, cancellation, collisions, shortcut conflicts, and independent output failures.
    - **Notes:** This is a verification and bug-fix PR, not a placeholder for unfinished Screenshot features. The acceptance matrix, environment of record, results, and hardware-gated scenarios are recorded in [docs/screenshot-verification.md](docs/screenshot-verification.md): every AC-S01 through AC-S12 scenario and SCR-031 passes on the two-display environment of record (both displays at 100% scaling), with mixed-DPI, negative-coordinate, rotated-display, and live-disconnect live validation still gated by the FND-01 blocker and covered at the mechanism level. New on-demand benchmarks measure NFR-011 overlay appearance (~58 ms free / ~18 ms preset against the 300 ms target) and the NFR-012 capture-to-clipboard halves (~115 ms combined against the one-second target, capturing a region larger than 4K). The SCR-10 clipboard test flake is fixed by reading the whole clipboard image back under one open with sequence-number change detection.

**M1 exit criteria:** All Screenshot P0 requirements and AC-S01 through AC-S12 pass on supported display configurations; no capture artifacts include app overlays; all M1 items are Done.

## 6. Milestone M2 — Image Resizer module

**Milestone status:** Pending\
**Goal:** Deliver safe, responsive bulk PNG/JPEG resizing with module-specific presets, deterministic fitting behavior, collision handling, and per-file outcomes.

19. **RSZ-01 — Add Image Resizer presets and job configuration UI**
    - **Status:** In Progress | **PR:** [#19](https://github.com/CyberCute-Studio/GameDevTools/pull/19) | **Depends on:** FND-07, FND-04
    - **Requirements:** PRESET-001, PRESET-007, RSZ-010, RSZ-016, RSZ-020, RSZ-021, RSZ-022, RSZ-023, RSZ-025, RSZ-028, RSZ-029.
    - **Deliver:** Resizer view, dimension presets with Stretch/Fit/Crop to Fill, plain-language mode descriptions, output folder/format/name behavior, collision policy selection, JPEG quality, transparency background color, and persistence of job settings.
    - **Verify:** Test all preset modes, built-in Fit defaults, setting validation/persistence, independent preset namespace, and clear mode explanations before a job can start.
    - **Notes:** This PR configures jobs but does not execute resizing; Start Resize stays disabled until the batch items land. Job settings persist in a versioned `resize` object inside the Image Resizer module section, validated independently from and preserved alongside the module's `presets` data. The view restates the selected preset's fitting mode in plain language — including the Stretch distortion warning — refreshed after every preset operation, and restates the selected collision policy's consequence, including that Overwrite replaces existing outputs only after job-time confirmation and never touches source files. JPEG quality and the transparency background stay enabled for Keep Source Format because JPEG sources still encode as JPEG.

20. **RSZ-02 — Implement bulk input selection and inventory**
    - **Status:** In Progress | **PR:** [#20](https://github.com/CyberCute-Studio/GameDevTools/pull/20) | **Depends on:** RSZ-01, FND-05
    - **Requirements:** RSZ-001, RSZ-002, RSZ-003, RSZ-004, RSZ-005, RSZ-006, RSZ-007, RSZ-008, RSZ-009.
    - **Deliver:** Multi-file picker, file drag-and-drop, optional nonrecursive folder drop, extension filtering, full-path deduplication, source list, removal/clear actions, read-only file handling, and per-item initial validation/status.
    - **Verify:** Add mixed supported/unsupported files, duplicates, inaccessible files, and drag-and-drop batches. Confirm originals open read-only and invalid items do not block valid inventory entries.
    - **Notes:** Full decode validation and source dimensions are completed in RSZ-03. The `internal/imageresizer` inventory validates each added path up front — case-insensitive extension filtering (RSZ-003), a read-only open that never modifies sources (RSZ-009), and case-insensitive full-path deduplication (RSZ-007) — and lists unsupported or unreadable files with their status and reason instead of rejecting them, so invalid items never block valid entries (RSZ-004). The view adds files through the multi-select image picker (`ui.PickImageFiles`, RSZ-001) or through files dropped anywhere on the module view (RSZ-002); a dropped folder contributes the supported files directly inside it without recursing (RSZ-008), enabled by the shell registering drag-and-drop only while the active module accepts drops. The multi-select list shows each file's name and status, with the selected item's full path and failure reason below it (RSZ-005), and Remove Selected / Clear List operate before any job starts (RSZ-006).

21. **RSZ-03 — Implement PNG/JPEG decoding and EXIF orientation normalization**
    - **Status:** In Progress | **PR:** [#21](https://github.com/CyberCute-Studio/GameDevTools/pull/21) | **Depends on:** RSZ-02
    - **Requirements:** RSZ-003, RSZ-004, RSZ-005, RSZ-018, TECH-005.
    - **Deliver:** Off-GUI-thread content decoding, corrupt/unsupported-content detection, source dimensions, alpha handling, EXIF orientation parsing/normalization, and typed decode errors.
    - **Verify:** Test extension/content mismatches, corrupt files, PNG alpha, baseline/progressive JPEG fixtures supported by the chosen decoder, and all standard EXIF orientations. Confirm dimension calculations use oriented pixels.
    - **Notes:** Metadata other than normalized orientation is not promised for preservation. The `internal/imageresizer/decode` package identifies content by signature (a renamed file is decoded as what it contains, and `Info.Format` records the detected format for the later output items), decodes with the Go standard library PNG and JPEG decoders — no new dependency — and applies all eight EXIF orientations, reporting upright dimensions from `Inspect` and upright pixels from `Decode`. Typed errors separate unreadable, unsupported (unrecognized content or an unimplemented coding feature), damaged, oversized, and cancelled sources; a source whose decoding would allocate more than 1 GiB of pixel memory — counting the decoder's pixel size for each PNG color type, its working buffers, a JPEG's RGB conversion even when a segment after the first scan requests it, and the upright copy of a reoriented JPEG — is rejected before allocation; and `Inspect` hands back the memory a large decode allocated before it returns, so back-to-back checks never hold two decoded sources (NFR-014); only a check abandoned while blocked in the operating system can briefly overlap the next one. The view checks each added file's content on one background worker and hands results to the GUI thread in batches through the shell dispatcher, so the source list shows upright dimensions and per-item content statuses (RSZ-004, RSZ-005) without blocking the GUI thread. Baseline, progressive, and CMYK JPEG fixtures are copied from the Go distribution's BSD-3-Clause test data; a JPEG whose entropy-coded data was cut short but that still ends with an end-of-image marker decodes as a partial picture, which the decoder cannot distinguish from an intact one.

22. **RSZ-04 — Implement deterministic resize algorithms**
    - **Status:** In Progress | **PR:** [#22](https://github.com/CyberCute-Studio/GameDevTools/pull/22) | **Depends on:** RSZ-03
    - **Requirements:** PRESET-006, RSZ-010, RSZ-011, RSZ-012, RSZ-013, RSZ-014, RSZ-015, RSZ-017.
    - **Deliver:** High-quality resampling, Stretch, Fit, center Crop to Fill, nearest-pixel rounding/clamping, consistent odd-pixel cropping, and upscaling/downscaling without an upscale-prevention option.
    - **Verify:** Unit-test landscape, portrait, square, extreme aspect ratio, 1-pixel, upscale, downscale, rounding, center-crop, and 100-megapixel validation cases. Use golden images or deterministic pixel tolerances appropriate to the selected resampler.
    - **Notes:** Include AC-R01 through AC-R05 at the algorithm level. The `internal/imageresizer/resize` package plans the geometry in exact integer arithmetic — Fit rounds halves up and clamps to one pixel; Crop to Fill rounds its scaled size the same way and removes the odd leftover pixel from the right or bottom — and resamples with a separable Catmull-Rom filter, widened when reducing, over premultiplied alpha, implemented in-house with no new dependency. Output pixels whose alpha rounds to zero are stored as transparent black, and JPEG YCbCr and CMYK pixels are converted to RGB with the same 8-bit standard-library conversion the decoder applies to a reoriented JPEG, so reorientation does not change the colors the filter starts from, which a test checks against the decoder's own output; the output carries alpha only when a pixel the filter reads is translucent, so translucency only in a margin Crop to Fill removes adds none. Output is bit-identical across 64-bit platforms (integer fixed-point filtering; weights from exact rational positions with fusion-proof float64 arithmetic, quantized by rounding their running total so each stays within one unit of its exact value), pinned by digests verified on windows/arm64 and windows/amd64 at GOAMD64=v1 and v3, and checked within one 8-bit step against an independent float64 reference; the package refuses to compile where `int` is 32 bits, since a Crop to Fill scaled axis reaches 2^34 pixels and NFR-004 excludes 32-bit Windows. The filter streams rows in whichever order a cost estimate finds cheaper — weighing how costly each source type's pixels are to read, such as a JPEG's YCbCr conversion — within a fixed budget for the rows it keeps, so working memory, weight tables included, stays under 128 MiB, which a test measures at the extreme source shapes; a context cancels a resize between source rows and before the output is allocated; a panic during a resize, such as one a malformed source image's own methods raise, is returned as a `KindFailed` error, an unexpected failure that blames neither the source nor the resizer since the two cannot be told apart, rather than unwinding into the caller, as the decoder contains its own (NFR-021), and a nil palette color reads as opaque black, as an index past the palette's end does; sources wider or taller than 1,048,576 pixels are rejected as `KindTooLarge`, which the job preflight in RSZ-07 reports per item. Targets are validated at the resize boundary with the preset library's own PRESET-006 rules, in the order the preset library applies them, which the preset validation and editor now share. Encoding and batch execution remain RSZ-06 and RSZ-07; an on-demand benchmark measures the NFR-013 resize step (a 4000 × 3000 4:2:0 source fit within 1024 × 1024) at about 130 ms on windows/arm64, where the order estimate's per-type read costs were also measured; the reference-machine figure is RSZ-09's.

23. **RSZ-05 — Implement output planning and collision policies**
    - **Status:** In Progress | **PR:** TBD | **Depends on:** RSZ-01, RSZ-02, FND-04
    - **Requirements:** RSZ-020, RSZ-021, RSZ-022, RSZ-023, RSZ-024, RSZ-025, RSZ-026, RSZ-029, RSZ-030, NFR-020.
    - **Deliver:** Destination-path planning, Keep Source Format/PNG/JPEG extension mapping, duplicate-destination detection, Rename/Skip/Overwrite behavior, overwrite confirmation input, source-path protection, and output-folder validation/creation.
    - **Verify:** Test same-name files from different folders, existing destinations, repeated runs, every collision policy, case-insensitive Windows paths, source-equals-destination, invalid paths, and permission failures. Prove no plan can overwrite a source.
    - **Notes:** Rename is the default. Overwrite always requires explicit job confirmation. The `internal/imageresizer/output` package's `Prepare` validates the job's output folder (refusing a path Windows would resolve to another path, judged by comparing it with `GetFullPathName`'s resolution rather than by modeling Windows' rules, such as one whose own name ends in a space or a period, one through a name that ends in a single period, or one that ends in a device name such as `NUL`, unless the path is an extended-length one marked `\\?\` with backslashes, which Windows takes verbatim, a path with a colon after its drive or share, which NTFS reads as a file stream, and a path that names only a drive, volume, device, or server, such as `\\?\C:`, to which `filepath.Join` would append file names without a separator; a saved folder that only these rules refuse, which earlier builds accepted, falls back to the default folder with a startup warning naming it rather than invalidating the settings file), format, policy, and sources, refuses an Overwrite job whose request lacks the user's confirmation before touching the filesystem, creates the output folder when it does not exist (RSZ-030), through its extended-length path so a name Windows keeps in a path that leads through it, such as `Photos `, is created as named, and plans one destination per source: the source base name with `.png` or `.jpg`, Keep Source Format following the detected content format (RSZ-022, RSZ-023). The package imports no other Image Resizer package but `decode` and defines the format and policy values, which the persisted options alias, and the output folder rules, which they apply, so the view and RSZ-07's job can both import it. Destinations are assigned in two passes — every source whose own name is available keeps it, then the rest are resolved in job order — so numbered names are deterministic (AC-R08) and a source never loses its name to another item's numbered one; the Rename search resumes where the previous item with the same name stopped, so thousands of same-named sources plan in linear time. Rename appends `_2`, `_3`, … past existing files, sources, and other items' destinations, passing over a name it cannot check; Skip leaves an existing destination untouched, and of two sources with one destination only one is written: the later one is planned as `ActionDefer`, recording the item it defers to, and RSZ-07 must hold it until that item ends, ending it Skipped when the file was written and otherwise creating it as `Plan.Successor` names (RSZ-024); Overwrite renames an item that collides with an earlier item of the job rather than replacing that result, and replaces only natural destinations: a renamed result takes the first unused numbered name, as under Rename, since a file with a numbered name may be an unlisted original rather than an earlier result, so re-runs add a numbered copy of each renamed result (RSZ-020, RSZ-049, AC-R10). The request lists, besides the processable sources, every other listed source the job does not process (unsupported, corrupt, too large, or inaccessible), and all of them are protected by path key, the path as Windows resolves it compared case-insensitively; before Overwrite replaces an existing file it also compares the file's Windows identity (volume serial and file index, read without requesting access) with theirs, by the file index alone wherever a match can only stop a replacement, since a Samba server reports a serial number of its own for each share, so a hard link, junction, or short-name alias counts as the source, and it renames when the destination's identity cannot be read or the destination may be a listed file whose identity cannot be read — a plain file the user may not open, in a readable folder, can only be a destination in that folder or one with several names, while one whose folder, drive, or share cannot be reached may be any file and is never taken for missing, and a listed folder or drive or share root is no file, whether or not it can be read or reached (RSZ-026, AC-R07, NFR-020). Rename and Skip never replace, so they read no identities. Files that Go 1.23's `os.Lstat` reports as irregular because of a reparse point that is no link, such as OneDrive placeholders under the default Pictures output folder, count as files; folders, links, junctions, and a file the output name reaches only as its 8.3 short name are never replaced, and an entry its folder cannot list is renamed past as `ReasonUnverified`. A request, confirmation, or folder failure fails the job with a typed error, a file in the way of the output folder as `ErrNotFolder`, found by inspecting the folder's path and its parents because Windows reports a missing drive with the same error code; an output folder that can no longer be reached, or is no longer a folder, while the plan is made, which the planner checks at the first, second, fourth, and so on destination name it cannot check, also fails the job once as a folder failure rather than every item on its own; an uncheckable natural destination in a folder still there, a name over 255 UTF-16 units, a source name with a colon, whose output would be a stream of another file, or an exhausted Rename search fails only its item; a name Windows would open as something other than a file in the output folder, as Windows 10 opens `NUL.png` as a device, counts as taken without the device being opened. A randomized test plans 120 jobs mixing sources and unprocessed listed files inside the output folder, earlier results, folders, and hard links, writes every planned result, and checks that no listed file changed. Each plan reflects the filesystem at planning time, so a re-run plans around earlier results (RSZ-049); RSZ-06's writer must still finalize created files without replacing anything that appeared afterwards, and replace an `ActionReplace` item's file only while `Item.StillReplaceable` reports, by the identity, number of names, size, and creation and last-write times the plan cleared, that the file is unchanged and no source has been moved or linked to its name since; the times and size matter where a FAT32 or exFAT volume gives a file that takes another's folder entry that file's identity. Encoding, writing, and the confirmation dialog are RSZ-06 and RSZ-08.

24. **RSZ-06 — Implement image encoding and atomic output writing**
    - **Status:** Pending | **PR:** TBD | **Depends on:** RSZ-04, RSZ-05
    - **Requirements:** RSZ-022, RSZ-023, RSZ-027, RSZ-028, RSZ-029, RSZ-030, NFR-023.
    - **Deliver:** PNG/JPEG encoding, source-format selection, JPEG quality, alpha preservation, JPEG transparency flattening, temporary destination files, atomic finalization where supported, and temporary-file cleanup.
    - **Verify:** Decode every output in tests; verify dimensions, format, alpha/background behavior, oriented appearance, collision handling, disk/write failures, and absence of partial final files.
    - **Notes:** Complete single-image processing end to end before introducing batch concurrency.

25. **RSZ-07 — Implement bounded, cancellable batch execution**
    - **Status:** Pending | **PR:** TBD | **Depends on:** RSZ-06
    - **Requirements:** RSZ-040, RSZ-041, RSZ-042, RSZ-043, RSZ-044, RSZ-045, RSZ-046, RSZ-049, RSZ-050, TECH-005, TECH-006, NFR-014, NFR-021.
    - **Deliver:** Job preflight, bounded worker execution, GUI-safe progress events, per-item state transitions, item-level error isolation, cancellation, completion summaries, rerun behavior, and bounded memory use.
    - **Verify:** Test mixed valid/invalid batches, duplicate destinations, cancellation at each stage, rerun collisions, worker errors, and large-input memory behavior. Confirm one failed item never stops remaining work.
    - **Notes:** Completed outputs survive cancellation; no new items start after cancellation.

26. **RSZ-08 — Complete Image Resizer UI and results end to end**
    - **Status:** Pending | **PR:** TBD | **Depends on:** RSZ-07, FND-05
    - **Requirements:** RSZ-005, RSZ-016, RSZ-040, RSZ-041, RSZ-042, RSZ-043, RSZ-044, RSZ-045, RSZ-046, RSZ-047, RSZ-048, RSZ-049.
    - **Deliver:** Start/Cancel workflows, live overall/current-file progress, per-file status/error details, completion counts, output-folder action, overwrite confirmation, and responsive list/job interaction.
    - **Verify:** Run mixed batches through the real UI, review every status, cancel active jobs, inspect failures, rerun jobs, move/interact with the window during work, and confirm the output folder opens only on request.
    - **Notes:** No automatic editor or output-folder opening is introduced.

27. **RSZ-09 — Run the Image Resizer acceptance and performance suite**
    - **Status:** Pending | **PR:** TBD | **Depends on:** RSZ-08
    - **Requirements:** AC-R01 through AC-R15, NFR-013, NFR-014, TECH-009.
    - **Deliver:** Repeatable format/orientation/alpha/collision test fixtures, end-to-end acceptance results, the 100-image reference benchmark, memory observations, and fixes for defects found within Image Resizer scope.
    - **Verify:** Pass every Image Resizer acceptance scenario and the responsive-batch shared scenario. Record reference hardware, settings, duration, and any justified performance variance.
    - **Notes:** This is a verification and bug-fix PR, not a placeholder for unfinished Resizer features.

**M2 exit criteria:** All Image Resizer P0 requirements and AC-R01 through AC-R15 pass; originals are protected under every policy; mixed batches are responsive and fault-isolated; all M2 items are Done.

## 7. Milestone M3 — Cross-cutting quality and hardening

**Milestone status:** Pending\
**Goal:** Validate the combined application against shared UX, reliability, privacy, compatibility, and performance requirements.

28. **QLT-01 — Complete accessibility, keyboard, contrast, and localization-readiness audit**
    - **Status:** Pending | **PR:** TBD | **Depends on:** SCR-11, RSZ-09
    - **Requirements:** UX-001, UX-002, UX-003, UX-004, UX-005, UX-006, UX-007, UX-008, UX-009, UX-010, AC-A03.
    - **Deliver:** Full keyboard navigation audit, visible-focus fixes, contrast checks, screen-reader/accessibility metadata supported by the selected stack, non-color-only status review, high-contrast/theme behavior decision, and centralized-string cleanup.
    - **Verify:** Exercise every main-window workflow keyboard-only, inspect contrast and focus, test supported Windows theme/high-contrast modes, and document any platform accessibility limitation with an approved resolution.
    - **Notes:** Resolve the PRD open decision on initial light/dark theme support.

29. **QLT-02 — Harden settings migration, recovery, reset, and application shutdown**
    - **Status:** Pending | **PR:** TBD | **Depends on:** QLT-01
    - **Requirements:** APP-004, SET-004, SET-005, SET-006, NFR-020, NFR-021, NFR-022, NFR-023, NFR-024, AC-A01, AC-A05.
    - **Deliver:** End-to-end settings/preset migration tests, user-facing restore-defaults flow separate from preset deletion, corrupt-data backup validation, unsaved-state protection, coordinated shutdown of overlays/hotkeys/jobs, and logging/recovery fixes.
    - **Verify:** Upgrade from every released settings fixture, recover from malformed data, reset settings without deleting images/presets unintentionally, and exit during every capture and batch state. Confirm cleanup and restart behavior.
    - **Notes:** Settings and preset recovery must be tested together because presets use the shared local persistence infrastructure.

30. **QLT-03 — Audit offline behavior, privacy, filesystem access, and dependencies**
    - **Status:** Pending | **PR:** TBD | **Depends on:** QLT-02
    - **Requirements:** SET-007, NFR-030, NFR-031, NFR-032, NFR-033, TECH-007, AC-A02.
    - **Deliver:** Offline test, network/account/telemetry audit, filesystem access review, log privacy review, dependency and license inventory, vulnerability review using available Go tooling, and removal or replacement of unjustified dependencies.
    - **Verify:** Run both core workflows with networking disabled; inspect logs for image/clipboard content and unnecessary filenames; verify writes stay within approved paths; record dependency licenses and review results.
    - **Notes:** No telemetry or cloud fallback may be introduced to satisfy another requirement.

31. **QLT-04 — Validate the supported Windows matrix and combined performance**
    - **Status:** Pending | **PR:** TBD | **Depends on:** QLT-03
    - **Requirements:** NFR-001, NFR-002, NFR-003, NFR-004, NFR-010 through NFR-014, AC-A01 through AC-A05, all remaining P0 acceptance scenarios.
    - **Deliver:** Windows 10 22H2 and supported Windows 11 test matrix, non-admin validation, launch/overlay/capture/resize performance report, multi-monitor DPI matrix, combined-module regression run, and fixes for cross-module defects.
    - **Verify:** Run the application and acceptance suite on each supported OS without administrator rights. Confirm 100–500% scaling coverage, reference performance targets, clean exit, offline operation, persistence, and responsive batch behavior.
    - **Notes:** Resolve the Windows 10 support-policy open decision before final release documentation.

**M3 exit criteria:** All shared acceptance criteria pass, supported Windows and DPI matrices are documented, no unapproved privacy or dependency issues remain, and all M3 items are Done.

## 8. Milestone M4 — Distribution and release

**Milestone status:** Pending\
**Goal:** Produce the chosen Windows distribution, complete user/release documentation, and validate the exact release candidate.

32. **REL-01 — Finalize product identity, packaging, and distribution**
    - **Status:** Pending | **PR:** TBD | **Depends on:** QLT-04
    - **Requirements:** TECH-010 and PRD Section 19 open decisions.
    - **Deliver:** Final product/executable name, version metadata and application icon, installer and/or portable ZIP decision, code-signing decision, update-strategy decision, reproducible release build, and selected packaging implementation.
    - **Verify:** Install/run or extract/run the packaged app in a clean non-admin Windows environment without Go installed. Confirm settings paths, shortcuts, file associations if any, uninstall behavior if applicable, and artifact integrity/signing according to the chosen strategy.
    - **Notes:** If the selected distribution or update approach cannot fit one PR, split this item before starting it and preserve backward-only dependencies.

33. **REL-02 — Complete user documentation, known limitations, and requirement traceability**
    - **Status:** Pending | **PR:** TBD | **Depends on:** REL-01
    - **Requirements:** PRD Sections 9.8, 10.6, 18, and 19; TECH-009.
    - **Deliver:** Setup and usage documentation for both modules, hotkey/configuration help, output/collision behavior, protected-content and metadata limitations, troubleshooting, test/verification guide, release notes, and PRD-to-implementation traceability update.
    - **Verify:** Follow the documentation on a clean machine to perform every core workflow. Confirm documented behavior and limitations match the packaged application and no deferred P0 requirement is presented as complete.
    - **Notes:** Update the PRD and this plan if approved behavior changed during implementation.

34. **REL-03 — Validate the release candidate and close the implementation baseline**
    - **Status:** Pending | **PR:** TBD | **Depends on:** REL-02
    - **Requirements:** All P0 requirements and all acceptance criteria in PRD Section 15.
    - **Deliver:** Release-candidate checklist and results, final clean-machine test, full requirement/status review, versioned release notes, and updates marking all completed plan items and milestones Done. Include only release-blocking fixes found during final validation.
    - **Verify:** Test the exact artifact produced by REL-01, not a development build. Complete all P0 acceptance scenarios or record an explicit release blocker. Confirm CI, package integrity, documentation, offline operation, and clean exit.
    - **Notes:** Completion of this item establishes the initial implemented baseline; subsequent features are added as new plan items under new milestones.

**M4 exit criteria:** The exact distributable artifact passes all release-blocking acceptance criteria, documentation matches behavior, every prior item is Done, and no unresolved P0 gap remains.

## 9. Requirement coverage map

This map is a planning aid, not a replacement for requirement references on individual items. Update it whenever items are split or requirements change.

| PRD area | Primary implementing items | Final validation |
|---|---|---|
| APP-001 through APP-005 | FND-03, FND-04, SCR-09, QLT-02 | QLT-04, REL-03 |
| PRESET-001 through PRESET-008 | FND-06, FND-07, SCR-01, RSZ-01 | QLT-02, QLT-04 |
| SCR-001 through SCR-069 | SCR-01 through SCR-10 | SCR-11, QLT-04, REL-03 |
| RSZ-001 through RSZ-050 | RSZ-01 through RSZ-08 | RSZ-09, QLT-04, REL-03 |
| SET-001 through SET-007 | FND-04, FND-06, module settings items, QLT-02, QLT-03 | QLT-04, REL-03 |
| UX-001 through UX-010 | FND-05 and all feature UI items | QLT-01, QLT-04 |
| NFR-001 through NFR-004 | SCR-02, REL-01 | QLT-04, REL-03 |
| NFR-010 through NFR-014 | SCR-11, RSZ-07, RSZ-09 | QLT-04, REL-03 |
| NFR-020 through NFR-024 | FND-04, SCR-10, RSZ-05 through RSZ-07, QLT-02 | QLT-04, REL-03 |
| NFR-030 through NFR-033 | FND-04 and feature implementations | QLT-03, QLT-04 |
| TECH-001 through TECH-010 | FND-01 through FND-06, SCR-02 through SCR-04, RSZ-03 through RSZ-07, REL-01 | QLT-03, QLT-04, REL-03 |
| Screenshot acceptance AC-S01 through AC-S12 | SCR-04 through SCR-10 | SCR-11, QLT-04, REL-03 |
| Resizer acceptance AC-R01 through AC-R15 | RSZ-03 through RSZ-08 | RSZ-09, QLT-04, REL-03 |
| Shared acceptance AC-A01 through AC-A05 | FND-04, SCR-09, RSZ-07, RSZ-08, QLT-01 through QLT-03 | QLT-04, REL-03 |

## 10. Change log

| Version | Date | Change |
|---|---|---|
| 1.0 | 2026-11-10 | Initial ordered, milestone-based implementation plan derived from PRD 1.0. |

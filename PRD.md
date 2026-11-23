# CyberCute Dev Tools — Product Requirements Document

**Document status:** Draft for review\
**Version:** 1.7\
**Last updated:** 2026-11-23\
**Product name:** CyberCute Dev Tools\
**Platform:** Windows desktop\
**Implementation language:** Go\
**Initial modules:** Screenshot, Image Resizer

## 1. Document purpose

This PRD is the primary product reference for the initial release and future development of CyberCute Dev Tools. It defines the problem, intended users, product behavior, scope, priorities, acceptance criteria, and high-level technical constraints.

It is intended to remain useful to developers, testers, designers, and maintainers after implementation begins. Detailed API choices, package layouts, UI mockups, and database schemas belong in separate design documents unless explicitly included here.

### 1.1 Requirement language

- **Must** / **P0**: required for the initial release.
- **Should** / **P1**: expected unless a documented trade-off requires deferral.
- **Could** / **P2**: desirable but not required.
- **Won't (initial release)**: explicitly outside the initial scope.

## 2. Product overview

CyberCute Dev Tools is a modular Windows GUI application that provides focused utilities for common game-development and general desktop workflows. The initial release contains two modules:

1. **Screenshot** — capture a free rectangle, a full display, or a rectangle with exact preset pixel dimensions. A persistent, always-on-top selection frame allows the user to interact with the application underneath and position the frame precisely before capture.
2. **Image Resizer** — resize one or many PNG or JPEG images using reusable dimension presets and configurable aspect-ratio behavior.

The application should feel like a coherent toolbox rather than two unrelated executables. Navigation, preset behavior, error presentation, settings persistence, and visual styling must be consistent across modules.

## 3. Problem statement

Existing screenshot workflows are often imprecise when a user needs an image of an exact size. Typical rectangle snipping tools require manual dragging and do not make it easy to position a fixed-size region while continuing to interact with the content underneath. This is especially inconvenient for capturing game assets, UI reference images, store assets, and fixed-size visual samples.

Image resizing presents a related problem: resizing many files individually is repetitive, and tools differ in how they handle aspect ratios, upscaling, output naming, and failures. A bulk workflow needs predictable dimensions, reusable presets, per-file status, and safe handling of original files.

CyberCute Dev Tools addresses both needs with exact, reusable pixel presets and clear, repeatable workflows.

## 4. Product vision and principles

### 4.1 Vision

Provide a dependable, extensible set of desktop tools where precision, predictability, and speed matter more than feature volume.

### 4.2 Product principles

- **Precision first:** Pixel dimensions and capture coordinates must be explicit and trustworthy.
- **Do not damage source material:** Bulk resizing preserves originals by default and never silently overwrites files.
- **Keep the user in control:** The screenshot frame stays available while the user interacts with underlying applications; bulk jobs can be reviewed and cancelled.
- **Make outcomes predictable:** Aspect-ratio behavior, output paths, naming, and errors are visible before work starts.
- **Remain responsive:** Long-running resize operations must not freeze the GUI.
- **Work locally:** Core features work offline and do not require an account.
- **Design for modules:** Shared application services must support future modules without coupling them to one another.

## 5. Target users and use cases

### 5.1 Primary users

- Game developers capturing fixed-size gameplay, UI, texture, or reference images.
- Artists and technical artists preparing batches of images at required dimensions.
- Designers and QA engineers producing reproducible screenshots and resized assets.
- General Windows users who need precise screenshots or straightforward bulk resizing.

### 5.2 Representative scenarios

1. A developer positions a 1280 × 720 frame over a running game, clicks through the frame to adjust the game camera, nudges the frame to an exact position, and captures without including the frame.
2. A QA engineer captures the entirety of a secondary display running at 150% scaling and receives an image at that display's native pixel dimensions.
3. An artist selects 80 PNG and JPEG files, applies a 512 × 512 crop-to-fill preset, and writes new files to a separate output folder.
4. A designer uses fit-to-size resizing for mixed portrait and landscape images without distortion; each output fits within the requested dimensions.
5. A user creates separate custom dimension presets for Screenshot and Image Resizer and finds them persisted after restarting the application.

## 6. Goals and success criteria

### 6.1 Initial-release goals

- Capture exact-size rectangular screenshots with no DPI-related size drift.
- Allow precise placement while preserving interaction with underlying applications.
- Capture any connected display, including mixed-DPI multi-monitor arrangements.
- Copy captures to the clipboard and save them to disk.
- Resize PNG and JPEG files in bulk with explicit stretch, fit, or crop-to-fill behavior.
- Preserve source images and provide clear per-file results.
- Persist module-specific presets and user preferences.
- Establish a modular application shell suitable for future tools.

### 6.2 Measurable success criteria

The initial release is successful when:

- All P0 acceptance criteria in this document pass on supported Windows versions.
- A preset capture has exactly the requested width and height at 100%, 125%, 150%, 175%, and 200% display scaling.
- A region can be positioned and captured on secondary displays, including displays with negative virtual-desktop coordinates.
- A mixed-format batch completes without losing or modifying any source file.
- One invalid input does not prevent other valid files in the batch from being processed.
- The GUI remains responsive during a representative 100-file resize job.
- No account, network connection, or external service is needed for either module.

## 7. Scope

### 7.1 In scope for the initial release

- One Windows desktop application with module navigation.
- Screenshot modes: free rectangle, exact preset rectangle, and full display.
- Always-on-top, draggable exact-size selection frame.
- Mouse and keyboard-assisted precision placement.
- Multi-monitor and per-monitor DPI-aware behavior.
- Clipboard copy and PNG/JPEG screenshot saving.
- Configurable global shortcut for starting a screenshot.
- Independent built-in and custom presets for each module.
- Bulk PNG/JPEG resizing by file selection and drag-and-drop.
- Stretch, fit, and center crop-to-fill modes.
- Configurable output directory and output format.
- Collision handling, progress, cancellation, and per-file errors.
- Local settings persistence and basic diagnostic logging.

### 7.2 Out of scope for the initial release

- Screenshot annotation or a full image editor.
- Scrolling capture, video recording, GIF creation, or timed burst capture.
- Active-window capture as a separate mode.
- A single screenshot spanning the entire virtual desktop as a dedicated mode.
- Optical character recognition, cloud upload, and screenshot sharing services.
- WebP, BMP, TIFF, SVG, PSD, animated PNG, or animated GIF support.
- Batch format support beyond PNG and JPEG.
- Custom crop anchors, padding/canvas modes, rotation, filters, or watermarking.
- Automatic metadata preservation guarantees.
- macOS or Linux versions.
- Accounts, cloud synchronization, telemetry, or online activation.
- Automatic updates (section 20).

## 8. Application shell and shared behavior

### 8.1 Navigation

**APP-001 (P0):** The main window must provide clear navigation to Screenshot and Image Resizer and a shared Settings area.

**APP-002 (P0):** The current module and its primary action must be visually obvious.

**APP-003 (P1):** The app should remember the last selected module and restore it on the next launch.

**APP-004 (P0):** Starting a capture or resizing job must not destroy unsaved preset edits or the other module's current state without warning.

**APP-005 (P1):** The application should use a single-instance model so that global shortcuts and settings do not conflict across instances. A second launch should activate the existing window.

### 8.2 Module presets

Screenshot and Image Resizer must have **separate preset libraries**. Creating, editing, or deleting a preset in one module must not change the other module's presets.

Each preset must have:

- A unique, non-empty name within its module, compared case-insensitively after trimming whitespace.
- A positive integer width and height.
- Module-specific settings where defined below.
- A stable local identifier so renaming does not break references.

**PRESET-001 (P0):** Each module must include useful built-in presets and support named custom presets.

**PRESET-002 (P0):** Users must be able to create, rename, duplicate, and delete custom presets.

**PRESET-003 (P0):** Built-in presets must not be edited or deleted directly; users can duplicate them into custom presets.

**PRESET-004 (P0):** Presets must persist across application restarts.

**PRESET-005 (P0):** Width and height fields must reject zero, negative, fractional, and non-numeric values before a preset can be saved.

**PRESET-006 (P0):** The initial supported dimension range is 1–16,384 pixels per axis. A resize preset must also be rejected if its target exceeds 100 megapixels. These limits must be explained in validation messages.

**PRESET-007 (P1):** The last selected preset in each module should be restored on the next launch.

**PRESET-008 (P0):** If stored preset data is malformed, the app must preserve built-ins, report the problem, and avoid silently deleting the user's data file.

Initial built-in Screenshot presets:

- 1280 × 720 — HD
- 1920 × 1080 — Full HD
- 2560 × 1440 — QHD
- 3840 × 2160 — 4K UHD

Initial built-in Image Resizer presets, all using **Fit** by default:

- 16 × 16
- 32 × 32
- 64 × 64
- 128 × 128
- 256 × 256
- 512 × 512
- 1024 × 1024
- 1920 × 1080

The exact built-in list can change during implementation without changing the requirement for independent, persistent custom presets.

## 9. Module 1 — Screenshot

### 9.1 User workflow

1. Open Screenshot from the main window or invoke the configurable global start shortcut.
2. Choose Free Rectangle, Preset Rectangle, or Full Display.
3. For Preset Rectangle, choose a saved preset or enter exact dimensions.
4. For Full Display, choose a display; the display containing the mouse cursor is the default.
5. Start capture mode. The main window hides so it does not obscure or appear in the capture. Full Display capture proceeds automatically after the window is hidden.
6. For rectangle modes, position or draw the selection. The underlying desktop remains usable as described below.
7. For rectangle modes, capture using the floating toolbar's Capture action or cancel using its Cancel action.
8. The app copies the captured pixels to the clipboard and saves the image using the configured output settings.
9. A non-blocking completion notification reports success or partial failure. It must not unexpectedly steal focus from the underlying application.

### 9.2 Capture invocation and global shortcut

**SCR-001 (P0):** Users must be able to start the currently selected screenshot mode from the main window.

**SCR-002 (P0):** Users must be able to start that mode through a configurable global shortcut while another application has focus.

**SCR-003 (P0):** The default start shortcut is `Ctrl+Alt+S`. The app must detect registration conflicts, report that the shortcut is unavailable, and allow the user to choose another.

**SCR-004 (P0):** Shortcut changes must persist and take effect without reinstalling the app.

**SCR-005 (P1):** Invoking the start shortcut while a selection is active should bring attention to the existing selection rather than stacking multiple capture overlays.

**SCR-006 (P0):** The app must release its global shortcut when exiting.

### 9.3 Free Rectangle mode

**SCR-010 (P0):** The user must be able to drag across the virtual desktop to define a rectangle of at least 1 × 1 physical pixel.

**SCR-011 (P0):** During the initial drag, the app must display the current width and height in physical pixels.

**SCR-012 (P0):** After releasing the drag, the user must be able to move the rectangle and adjust it with resize handles.

**SCR-013 (P0):** The user must be able to type exact width and height values for the current free rectangle.

**SCR-014 (P0):** Capture must produce an image matching the final rectangle's dimensions and screen contents exactly, excluding the overlay and selection controls.

**SCR-015 (P1):** The user should be able to complete Free Rectangle capture without a pointer: typing a width and height before drawing creates a rectangle of exactly that size centered on the cursor display, and the arrow keys move the rectangle by one physical pixel, or ten with Shift.

### 9.4 Preset Rectangle mode

**SCR-020 (P0):** Selecting a preset must create a rectangle with exactly the preset's physical-pixel width and height, regardless of monitor scaling.

**SCR-021 (P0):** The active rectangle must remain on top of ordinary application windows while the user interacts with applications underneath it.

**SCR-022 (P0):** The rectangle's interior must be visually unobscured and mouse-click-through. Clicks inside the rectangle must reach the underlying application rather than move the selection or capture immediately.

**SCR-023 (P0):** The selection border must provide an obvious draggable area. Dragging that area must move the rectangle without changing its dimensions.

**SCR-024 (P0):** A compact floating toolbar must provide Capture and Cancel actions and show the active width and height. The toolbar itself must not be included in the capture.

**SCR-025 (P0):** The toolbar must be positioned near the selection without covering the selected content where practical. It must remain reachable when the selection is near a display edge.

**SCR-026 (P0):** The user must be able to move the rectangle between displays and across display boundaries without a change to its pixel dimensions.

**SCR-027 (P0):** When the selection or its toolbar has keyboard focus, arrow keys must move the rectangle by one physical pixel. `Shift+Arrow` must move it by ten physical pixels.

**SCR-028 (P1):** The toolbar should show the rectangle's current X and Y coordinates in the Windows virtual-desktop coordinate space; negative coordinates must be supported.

**SCR-029 (P0):** Editing dimensions while a preset rectangle is active must update the active rectangle exactly. This ad hoc edit must not modify the saved preset unless the user explicitly saves it.

**SCR-030 (P0):** Preset Rectangle mode must not expose resize handles that accidentally change exact dimensions. Size changes occur only through explicit numeric input or preset selection.

**SCR-031 (P0):** If a requested rectangle is larger than the available virtual desktop, capture must be blocked with a clear validation message. A rectangle larger than one individual display is allowed when it fits within the virtual desktop.

### 9.5 Full Display mode

**SCR-040 (P0):** Full Display mode must capture one selected connected display, not an automatically stitched image of all displays.

**SCR-041 (P0):** The display picker must identify displays by number and, where Windows provides it, a useful display name. Resolution and primary-display status must be shown.

**SCR-042 (P0):** The selected display must be captured at its current native desktop pixel dimensions and orientation.

**SCR-043 (P0):** Any connected display must be selectable, including secondary displays using different DPI scaling.

**SCR-044 (P0):** The main app window and all capture overlays must be hidden from the resulting image.

### 9.6 Overlay lifecycle and safety

**SCR-050 (P0):** Starting rectangle capture must hide the main window before showing the selection overlay.

**SCR-051 (P0):** Cancelling must close the overlay without changing the clipboard or creating an output file.

**SCR-052 (P0):** A failed capture must not leave an invisible overlay intercepting input. The app must provide an obvious recovery path if overlay creation fails.

**SCR-053 (P0):** Only one rectangle selection session may be active at a time.

**SCR-054 (P0):** If the display arrangement changes while a selection is active, the app must revalidate the selection. It must keep valid dimensions and coordinates where possible or explain why the session must be restarted.

**SCR-055 (P1):** If the app loses activation, the selection should remain visible and positioned; clicking another application inside the frame must not cancel the session.

**SCR-056 (P0):** Exiting the application must remove overlays and restore normal input immediately.

### 9.7 Capture output

**SCR-060 (P0):** A successful capture must copy image data to the Windows clipboard and save a file. These operations are independently reported: a clipboard failure must not be described as a successful complete operation, and a save failure must not discard a successful clipboard copy.

**SCR-061 (P0):** Screenshot save format must be configurable as PNG or JPEG. PNG is the default.

**SCR-062 (P0):** Clipboard image data must be compatible with common Windows image destinations and must not be degraded by JPEG file encoding. The clipboard receives the captured image before JPEG compression.

**SCR-063 (P0):** The default output folder is the user's Pictures folder under `CyberCute Dev Tools/Screenshots`. The user must be able to choose another local folder.

**SCR-064 (P0):** The default filename pattern is `Screenshot_yyyyMMdd_HHmmssfff`, with the appropriate `.png` or `.jpg` extension.

**SCR-065 (P0):** Existing files must never be silently overwritten. If a generated filename exists, the app must append a numeric suffix.

**SCR-066 (P0):** Captured files must be written through a temporary file and finalized only after successful encoding so a failed write does not leave a seemingly valid partial image.

**SCR-067 (P0):** JPEG quality must be configurable from 1–100, defaulting to 90.

**SCR-068 (P0):** The mouse cursor, selection frame, toolbar, and overlay tint must not appear in the output.

**SCR-069 (P1):** The completion notification should offer an Open Folder action and show the saved path or a concise error.

### 9.8 Screenshot limitations and expected errors

- The Windows secure desktop, lock screen, and other protected surfaces are not supported capture targets.
- Content protected by Windows or third-party DRM may be unavailable or black. The app must not claim a complete success if Windows reports capture failure.
- Full-screen exclusive applications, remote sessions, or graphics drivers may impose capture restrictions. Errors must identify the operation that failed and suggest retrying in windowed mode where relevant.
- A capture can cross monitors. Because physical pixel output is the contract, color or sharpness differences introduced by source displays are not corrected by the app.

## 10. Module 2 — Image Resizer

### 10.1 User workflow

1. Open Image Resizer.
2. Add PNG/JPEG files through a file picker or drag-and-drop.
3. Review the input list and remove unwanted items.
4. Select a resize preset and inspect its dimensions and fitting mode.
5. Choose output folder, output format, and JPEG quality where applicable.
6. Review the collision policy and start the job.
7. Observe overall progress and per-file status; optionally cancel.
8. Review completion counts and individual failures, and open the output folder.

### 10.2 Input management

**RSZ-001 (P0):** The file picker must support selecting multiple files.

**RSZ-002 (P0):** Users must be able to drag multiple supported files onto the module.

**RSZ-003 (P0):** Supported input extensions are `.png`, `.jpg`, and `.jpeg`, case-insensitive. The app must also validate decoded content rather than relying only on extensions.

**RSZ-004 (P0):** Unsupported, corrupt, inaccessible, or undecodable files must be identified in the file list and must not block valid files from being processed.

**RSZ-005 (P0):** The input list must show filename, full path on demand, source dimensions when readable, and status.

**RSZ-006 (P0):** Users must be able to remove selected items and clear the list before starting a job.

**RSZ-007 (P0):** Adding the same full source path twice must not create duplicate job items.

**RSZ-008 (P1):** Dragging a folder may add supported files directly inside that folder. Recursive folder processing is not required in the initial release; if folder drop is not implemented, the UI must direct users to multi-file selection instead.

**RSZ-009 (P0):** Input files must be opened read-only and never modified by the resize job.

### 10.3 Resize presets and fitting behavior

Each Image Resizer preset contains width, height, and one fitting mode. Both upscaling and downscaling are supported.

**RSZ-010 (P0):** **Stretch** must scale width and height independently to produce exactly the preset dimensions. The UI must warn that different source aspect ratios can be distorted.

**RSZ-011 (P0):** **Fit** must preserve aspect ratio and scale the image uniformly so neither output dimension exceeds the corresponding preset dimension. One output dimension can be smaller than the preset; no padding is added.

**RSZ-012 (P0):** Fit scaling uses `min(targetWidth/sourceWidth, targetHeight/sourceHeight)`, with output dimensions rounded to the nearest whole pixel and clamped to at least 1 pixel.

**RSZ-013 (P0):** **Crop to Fill** must preserve aspect ratio, scale uniformly until both preset dimensions are covered, and center-crop to exactly the preset dimensions.

**RSZ-014 (P0):** Crop to Fill must crop equally from opposite sides. When an odd number of pixels must be removed, the extra pixel may be removed from the right or bottom consistently.

**RSZ-015 (P0):** All fitting modes must allow enlargement when necessary. There is no “prevent upscale” option in the initial release.

**RSZ-016 (P0):** Before starting, the selected fitting mode and its consequence must be visible in plain language.

**RSZ-017 (P0):** Resampling must use a high-quality algorithm suitable for both enlargement and reduction and must produce deterministic results for the same input and settings.

**RSZ-018 (P0):** JPEG EXIF orientation must be applied before dimension calculations and resizing so output is visually upright. The output need not retain the original EXIF orientation tag.

### 10.4 Output settings

**RSZ-020 (P0):** The default behavior must create new files in a user-selected output folder and preserve every original file.

**RSZ-021 (P0):** The default output folder is the user's Pictures folder under `CyberCute Dev Tools/Resized`. The last valid folder must be remembered.

**RSZ-022 (P0):** Output format options must be Keep Source Format, PNG, and JPEG. Keep Source Format is the default.

**RSZ-023 (P0):** Output files retain the source base filename by default, with an extension matching the encoded output format.

**RSZ-024 (P0):** If two source files would produce the same destination path, the app must detect this before or during the job and apply the selected collision policy; it must not overwrite one result with another silently.

**RSZ-025 (P0):** Before starting, the user must choose a collision policy: **Rename** (default), **Skip**, or **Overwrite**. The policy applies to destination files, not source files.

- **Rename:** append `_2`, `_3`, and so on until an unused path is found.
- **Skip:** leave the existing destination untouched and mark the item Skipped.
- **Overwrite:** replace destination files only after explicit confirmation for the job.

**RSZ-026 (P0):** A destination path equal to a source path must never be overwritten in the initial release, even under the Overwrite policy. The app must rename the result or require a different output folder.

**RSZ-027 (P0):** Each result must be encoded to a temporary file in the destination folder and atomically finalized where the filesystem supports it. Failed jobs must clean up temporary files.

**RSZ-028 (P0):** PNG output must preserve source alpha. When converting an image with alpha to JPEG, transparent pixels must be flattened against a configurable background color, defaulting to white.

**RSZ-029 (P0):** JPEG quality must be configurable from 1–100 and default to 90.

**RSZ-030 (P0):** The app must create the output folder if it does not exist and report permission, invalid-path, disk-full, and other write failures clearly.

### 10.5 Batch execution and results

**RSZ-040 (P0):** Starting a job must validate the preset, output folder, output settings, and at least one processable input.

**RSZ-041 (P0):** The main GUI must remain responsive while decoding, resizing, and encoding.

**RSZ-042 (P0):** Progress must show completed item count, total item count, and the currently processing file where practical.

**RSZ-043 (P0):** Each item must end in one of: Pending, Processing, Completed, Skipped, Failed, or Cancelled.

**RSZ-044 (P0):** Failure of one item must not stop remaining items.

**RSZ-045 (P0):** Cancel must stop starting new items, allow an in-flight file to reach a safe stopping point, retain already completed outputs, and mark unprocessed items Cancelled.

**RSZ-046 (P0):** Completion must summarize Completed, Skipped, Failed, and Cancelled counts and provide access to per-file error details.

**RSZ-047 (P0):** The output folder must not be opened automatically unless the user requests it.

**RSZ-048 (P1):** Completed results should offer an Open Output Folder action.

**RSZ-049 (P0):** Re-running a job must re-evaluate destination collisions and current input availability rather than assuming prior results.

**RSZ-050 (P1):** Resize work should use bounded parallelism to improve throughput without exhausting memory or degrading GUI responsiveness.

### 10.6 Color and metadata behavior

- PNG alpha must be preserved when outputting PNG.
- JPEG EXIF orientation must be normalized as described above.
- Preservation of EXIF, ICC profiles, comments, timestamps, and other metadata is not guaranteed in the initial release. The UI and documentation must not claim lossless metadata preservation.
- Resizing and JPEG re-encoding are not lossless operations. PNG output avoids additional lossy compression but does not undo source compression.

## 11. Settings and local data

**SET-001 (P0):** Settings must persist locally and reload automatically.

**SET-002 (P0):** Persisted settings must include global shortcuts, screenshot output options, last output folders, module preset libraries, last selected presets, and window/module preferences where applicable.

**SET-003 (P0):** Configuration must be stored in a per-user application data directory, not beside input images or in a system-wide protected directory.

**SET-004 (P0):** A settings write failure must produce a visible warning without crashing the app or discarding the current in-memory session.

**SET-005 (P0):** Users must be able to restore default application settings without deleting saved images. Preset deletion must be a separate, explicit action.

**SET-006 (P1):** Corrupt settings should be backed up before defaults are regenerated, with the backup location reported to the user.

**SET-007 (P0):** Logs must not embed image contents, clipboard contents, or full batches of user filenames by default. Diagnostic file paths may be included only in user-accessible local error details where necessary.

## 12. UX, accessibility, and localization

**UX-001 (P0):** Primary actions must have clear labels: Start Capture, Capture, Cancel, Add Images, Start Resize, and Open Output Folder as applicable.

**UX-002 (P0):** Destructive actions such as deleting a custom preset or overwriting destination files require confirmation.

**UX-003 (P0):** Validation must appear next to the relevant field where practical and must explain how to correct the problem.

**UX-004 (P0):** Pixel values must be labeled with `px`, and dimensions must use a consistent `width × height` representation.

**UX-005 (P0):** The app must not communicate job status by color alone; icons and/or text are required.

**UX-006 (P0):** Main-window workflows must be keyboard navigable with visible focus indicators.

**UX-007 (P0):** Text and essential controls must meet WCAG 2.1 AA contrast targets where applicable to desktop UI.

**UX-008 (P1):** The app should respect the Windows light/dark preference. If only one theme is implemented initially, it must remain legible under Windows high-contrast settings or document the limitation.

**UX-009 (P0):** Initial UI language is English. User-facing strings must be centralized so localization is not structurally blocked.

**UX-010 (P0):** Error messages must distinguish validation, unsupported format, read/decode, write/encode, clipboard, shortcut-registration, and capture failures.

## 13. Non-functional requirements

### 13.1 Platform compatibility

**NFR-001 (P0):** Support 64-bit Windows 11 in its currently supported releases. Windows 10 is not supported.

**NFR-002 (P0):** Operate without administrator privileges for normal use.

**NFR-003 (P0):** Support per-monitor DPI awareness, display scaling from 100% through at least 500%, negative monitor coordinates, and display disconnection/reconnection.

**NFR-004 (P1):** Windows on ARM and 32-bit Windows are not initial targets.

### 13.2 Performance targets

Performance targets are measured on a reference machine with a 2020-or-newer quad-core CPU, 16 GB RAM, an SSD, and Windows 11, with no other heavy workload.

**NFR-010 (P1):** Main window launch should complete within two seconds under warm-start conditions.

**NFR-011 (P1):** The rectangle overlay should appear within 300 ms of starting rectangle capture, excluding an unresponsive graphics subsystem.

**NFR-012 (P1):** Capture-to-clipboard should complete within one second for a 3840 × 2160 region, excluding slow disk saving. Save completion may be reported separately.

**NFR-013 (P1):** Resizing 100 mixed 12-megapixel JPEG images to fit within 1024 × 1024 should complete within two minutes on the reference machine.

**NFR-014 (P0):** Memory use must be bounded during batch work; the app must not decode every input into memory simultaneously.

### 13.3 Reliability and data safety

**NFR-020 (P0):** No screenshot or resize operation may silently modify source images.

**NFR-021 (P0):** Unexpected item-level errors must be contained and reported rather than crashing the entire batch.

**NFR-022 (P0):** Application shutdown must unregister global shortcuts, close overlays, and stop accepting new work.

**NFR-023 (P0):** Output writes must avoid leaving partial files at final paths.

**NFR-024 (P1):** Diagnostic logs should rotate by size or age to avoid unbounded disk use.

### 13.4 Security and privacy

**NFR-030 (P0):** All core operations must run locally and work offline.

**NFR-031 (P0):** The app must not require an account, collect telemetry, or upload images in the initial release.

**NFR-032 (P0):** Files must be read and written only at user-selected paths and the app's own settings/log directories.

**NFR-033 (P0):** Third-party dependencies must be inventoried and their licenses reviewed before release.

## 14. Technical guardrails

These are constraints for design and implementation, not a complete technical design.

**TECH-001 (P0):** The application must be implemented in Go using a GUI approach compatible with the supported Windows targets.

**TECH-002 (P0):** GUI technology selection must demonstrate support for topmost transparent/click-through overlay regions, precise physical-pixel coordinates, per-monitor DPI awareness, global hotkeys, Windows clipboard image formats, and screen capture before implementation is committed.

**TECH-003 (P0):** The app must use a DPI-aware Windows execution context. Capture coordinates and dimensions must be converted explicitly between logical UI coordinates and physical pixels; implicit framework scaling is not acceptable for output dimensions.

**TECH-004 (P0):** Screenshot and Image Resizer must be separate modules behind a shared application shell. Shared services may include settings, logging, notifications, and navigation; preset storage must remain module-scoped.

**TECH-005 (P0):** Image decoding, encoding, capture, and bulk resizing must run off the GUI thread. UI updates must be synchronized through the GUI framework's supported mechanism.

**TECH-006 (P0):** Batch concurrency must be bounded and cancellation-aware.

**TECH-007 (P0):** Use established, maintained image and Windows integration libraries where appropriate. A dependency must not be assumed solely because it is common; its Go/Windows compatibility and license must be verified.

**TECH-008 (P0):** Settings must use a documented, versioned local format so future releases can migrate it. The initial release does not require a database.

**TECH-009 (P0):** Automated tests must cover preset validation, resize calculations, output naming/collisions, settings migration, and batch failure handling. Windows integration tests must cover clipboard, global hotkeys, overlays, and mixed-DPI captures where automation is feasible.

**TECH-010 (P1):** The build should produce a Windows executable without requiring users to install Go or a development toolchain.

## 15. Acceptance criteria

The following scenarios supplement the requirement IDs above and form the minimum end-to-end release acceptance suite.

### 15.1 Screenshot acceptance

**AC-S01 — Exact preset at standard scaling**\
Given a 1280 × 720 preset, when the user captures a region on a 100%-scaled display, then the saved image and clipboard image are exactly 1280 × 720 pixels and contain no overlay controls.

**AC-S02 — Exact preset at mixed DPI**\
Given two displays at different scaling levels, when the same preset is captured on each display and across their boundary, then every output has exactly the preset dimensions and no unexpected scaling or cropping occurs.

**AC-S03 — Click-through interaction**\
Given an active preset frame over another application, when the user clicks a control inside the frame but not on its border or toolbar, then the underlying application receives the click and the frame retains its position and dimensions.

**AC-S04 — Precision placement**\
Given an active preset frame, when the user drags its border and uses arrow-key nudges, then the frame moves by the expected physical pixels without changing width or height.

**AC-S05 — Free rectangle adjustment**\
Given a free rectangle drawn by the user, when its dimensions are edited before capture, then the resulting image matches the edited dimensions and selected contents.

**AC-S06 — Full secondary display**\
Given a secondary display with a known native resolution, when it is selected for Full Display capture, then the output matches that resolution and includes only that display's desktop contents.

**AC-S07 — Cancellation**\
Given an active rectangle session, when the user cancels, then no file is created, the clipboard remains unchanged, and normal mouse input is restored.

**AC-S08 — Clipboard success with save failure**\
Given an unwritable save destination, when capture succeeds, then the clipboard receives the image if possible, the user is told saving failed, and the operation is not reported as fully successful.

**AC-S09 — Save success with clipboard failure**\
Given a simulated clipboard failure, when capture and saving succeed, then the file is valid and the notification explicitly identifies the clipboard failure.

**AC-S10 — Filename collision**\
Given an existing file with the generated screenshot name, when another screenshot is saved, then a numeric suffix is used and the existing file remains unchanged.

**AC-S11 — Shortcut conflict**\
Given another application owns the configured shortcut, when CyberCute Dev Tools starts or applies that shortcut, then it reports the conflict and allows a replacement without crashing.

**AC-S12 — Monitor removal**\
Given an active selection on a display that is disconnected, when Windows updates the display layout, then the app recovers or requests a restart of the session without leaving an invisible input-capturing window.

### 15.2 Image Resizer acceptance

**AC-R01 — Stretch**\
Given a 1000 × 500 source and a 512 × 512 Stretch preset, when resized, then output is exactly 512 × 512 and the UI has disclosed possible distortion.

**AC-R02 — Fit landscape**\
Given a 1000 × 500 source and a 512 × 512 Fit preset, when resized, then output is 512 × 256 with preserved aspect ratio and no padding.

**AC-R03 — Fit portrait and rounding**\
Given mixed portrait/landscape sources, when Fit is used, then each output fits within the target, preserves aspect ratio within one-pixel rounding tolerance, and has dimensions of at least 1 × 1.

**AC-R04 — Crop to Fill**\
Given a 1000 × 500 source and a 512 × 512 Crop to Fill preset, when resized, then output is exactly 512 × 512, aspect ratio is preserved before cropping, and the crop is centered.

**AC-R05 — Upscaling**\
Given a source smaller than the selected preset, when any fitting mode is used, then the defined enlargement behavior is applied without silently keeping original dimensions.

**AC-R06 — Mixed batch resilience**\
Given valid PNG/JPEG files plus one corrupt file and one unsupported file, when the job runs, then every valid file is processed and each invalid file has an actionable Failed status.

**AC-R07 — Originals preserved**\
Given any output policy and a destination equal to a source path, when the job runs, then no source is overwritten and the user must choose another folder or accept a renamed output.

**AC-R08 — Rename collisions**\
Given two sources that map to the same destination and the Rename policy, when the job runs, then both valid results exist with deterministic numeric suffixes.

**AC-R09 — Skip collisions**\
Given an existing destination and the Skip policy, when the job runs, then the existing file is unchanged and the item is marked Skipped rather than Completed.

**AC-R10 — Confirmed overwrite**\
Given existing non-source destinations and the Overwrite policy, when the user confirms, then only those destination files are replaced.

**AC-R11 — PNG alpha**\
Given a PNG with transparency and PNG output, when resized, then alpha remains present. Given JPEG output instead, transparency is flattened against the selected background color.

**AC-R12 — EXIF orientation**\
Given an orientation-tagged JPEG, when resized, then the output appears upright and dimensions correspond to the oriented image.

**AC-R13 — Cancellation**\
Given a running batch, when the user cancels, then no new items start, completed files remain valid, unfinished items are marked appropriately, and the GUI remains usable.

**AC-R14 — Write failure**\
Given a destination that becomes unwritable during a job, when an item fails, then no partial final file remains, the error is attached to that item, and remaining items continue where possible.

**AC-R15 — Independent presets**\
Given a custom 640 × 480 Screenshot preset, when Image Resizer presets are viewed, then that preset is absent unless separately created there.

### 15.3 Shared acceptance

**AC-A01 — Persistence:** Custom presets and settings survive a normal restart.\
**AC-A02 — Offline use:** Both modules complete their core workflows with networking disabled.\
**AC-A03 — Keyboard access:** All main-window actions are reachable by keyboard with visible focus.\
**AC-A04 — Responsive batch UI:** The window can be moved, inputs reviewed, and the job cancelled while resizing runs.\
**AC-A05 — Clean exit:** No global hotkey or invisible overlay remains after the app exits.

## 16. Release phases and priorities

### Phase 1 — Application foundation

- Windows GUI shell and module navigation.
- Versioned local settings and logging.
- Module-scoped preset storage and validation.
- Initial light/dark or supported system theme behavior.

### Phase 2 — Screenshot

- Free Rectangle and Full Display capture.
- Exact preset frame, click-through behavior, toolbar, and precision movement.
- PNG/JPEG save and clipboard output.
- Global shortcut and mixed-DPI validation.

### Phase 3 — Image Resizer

- Input list and drag-and-drop.
- All fitting modes and PNG/JPEG encoding.
- Output policies, progress, cancellation, and error reporting.
- Representative batch performance testing.

### Phase 4 — Release hardening

- Full acceptance matrix across supported Windows versions and monitor layouts.
- Accessibility and keyboard review.
- Dependency/license review.
- Packaging decision, installer or portable distribution, and release documentation.

All P0 items are release blockers. P1 items may be deferred only with an explicit release note and an update to this PRD.

## 17. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| GUI framework cannot support transparent click-through overlays and precise DPI behavior | Screenshot core workflow fails | Build a technical spike before committing to the framework; validate all TECH-002 capabilities on real Windows systems. |
| Mixed-DPI coordinate conversion causes wrong output sizes | Exact preset promise is broken | Use physical pixels as the capture contract; test multiple monitor arrangements and negative coordinates. |
| Click-through overlay conflicts with dragging or toolbar input | Users cannot both interact below and position frame | Restrict input capture to border and toolbar; test hit regions and focus changes explicitly. |
| Global hotkey conflicts with other software | Capture cannot be invoked globally | Detect registration failure, support remapping, and retain main-window invocation. |
| Protected or exclusive content cannot be captured | Blank or failed screenshots | Document platform limits, surface capture API errors, and suggest supported display modes. |
| Large batches exhaust memory | App becomes unstable | Bound concurrency, process streaming per item where possible, and test large inputs. |
| Duplicate destinations overwrite results | Data loss | Preflight destination mapping, apply explicit policies, and prohibit source overwrite. |
| Corrupt settings prevent startup | Presets and preferences become inaccessible | Validate/version settings, back up malformed data, and recover with defaults plus a visible warning. |
| Scope grows into an image editor | Initial release is delayed | Keep annotation and advanced editing out of scope; evaluate them as future modules only after release. |

## 18. Future considerations

These are not commitments for the initial release:

- Annotation, arrows, blur, numbering, and simple post-capture editing.
- Active-window, scrolling, delayed, and cursor-inclusive capture.
- Optional capture history and pinned screenshot references.
- WebP and additional input/output formats.
- Optional no-upscale behavior, custom crop anchors, padding, rotation, and watermarking.
- Shared or synchronized dimension presets if user demand justifies changing the initial module-scoped model.
- Watch folders, command-line batch operation, and automation profiles.
- Additional CyberCute Dev Tools modules using the same shell and settings infrastructure.
- Localization beyond English.
- Following the Windows dark app mode with a dark theme.
- Signed installer, automatic updates, and enterprise deployment controls.

## 19. Open decisions

None. REL-01 resolved the final name, the package format, code signing, and automatic updates; see section 20 and [ADR-0002](docs/adr/0002-product-identity-and-distribution.md).

## 20. Confirmed product decisions

For clarity, the following choices have been made and should not be reopened without an explicit requirements change:

- The PRD includes product requirements and high-level technical guardrails, not a detailed implementation design.
- Successful screenshots go to both clipboard and disk; no editor opens automatically.
- All connected displays and mixed-DPI arrangements are in scope.
- Screenshot supports Free Rectangle, exact Preset Rectangle, and one-display Full Display capture.
- Image Resizer supports Stretch, Fit, and Crop to Fill per preset.
- Screenshot and Image Resizer use separate preset libraries.
- Resizing writes new files and preserves originals by default.
- Initial image format support is PNG and JPEG only.
- Screenshots can be started from the main window or a configurable global shortcut. While one of the application's own modal dialogs is open, the shortcut (SCR-002) captures nothing and says so, as the dialog would be in the image (SCR-044) and Escape could not cancel a selection (SCR-052).
- The initial release draws every window in the Windows system colors, so it follows the Windows contrast (high-contrast) themes, and does not follow the Windows dark app mode, which the standard Win32 controls it uses have no documented way to render; the application stays light and legible when Windows apps are set to dark (UX-008).
- Restoring the default settings (SET-005) backs the settings file up first and keeps each module's custom presets and its selected preset, the window placement, and the current module; every other setting — the global shortcut, the capture and resize options, and both output folders — returns to its default.
- Closing the application while a resize job runs asks first, as closing cancels the job. Signing out of, restarting, or shutting down Windows asks nothing: the application saves its settings and stops all work.
- Screenshots copied to the clipboard are marked so that Windows clipboard sync does not upload them (NFR-031). They stay in the local clipboard history (Win+V). An image that cannot be marked is not placed on the clipboard, which is reported as a clipboard failure while the saved file is kept (SCR-060).
- The initial release supports Windows 11 only (NFR-001). Windows 10, whose general support from Microsoft ended in October 2025, is not supported or tested; the application does not refuse to start there.
- The product is named **CyberCute Dev Tools**, and its executable `cybercute-dev-tools.exe`. It was called GameDevTools until PRD 1.5. The first launch takes over the settings of a GameDevTools build, leaving its file in place, and output folders already selected stay selected.
- The initial release ships as a portable ZIP for 64-bit Intel/AMD Windows, with no installer.
- The initial release is not code-signed. Each ZIP has a published SHA-256 checksum, and the packaged README explains the Windows SmartScreen warning.
- The application has no automatic update mechanism and does not check for updates. Users update by replacing the extracted folder's files with those from the new ZIP.
- The ZIP carries the user documentation, as a user guide and release notes in plain text, so that it is available offline beside the executable. [docs/traceability.md](docs/traceability.md) records each requirement's implementation, evidence, and status.
- CyberCute Dev Tools is released under the MIT License. The repository's `LICENSE` and the ZIP's `LICENSE.txt` carry it.

## 21. Revision history

| Version | Date | Change |
|---|---|---|
| 1.0 | 2026-11-10 | Initial detailed PRD for the Windows Go application, Screenshot module, and Image Resizer module. |
| 1.1 | 2026-11-20 | QLT-01: resolved the theme open decision — system colors and contrast themes, no dark app mode in the initial release (section 20, a future consideration in section 18) — and added SCR-015, keyboard completion of Free Rectangle capture. |
| 1.2 | 2026-11-20 | QLT-02: confirmed what restoring the default settings keeps and resets (SET-005), that closing while a resize job runs asks first, and that the end of the Windows session asks nothing (section 20). |
| 1.3 | 2026-11-21 | QLT-03: confirmed that copied screenshots opt out of Windows clipboard sync and stay in the local clipboard history (NFR-031, section 20). |
| 1.4 | 2026-11-22 | QLT-04: resolved the Windows 10 open decision — the initial release supports Windows 11 only (NFR-001, section 20). |
| 1.5 | 2026-11-22 | REL-01: resolved the remaining open decisions — the name CyberCute Dev Tools, a portable ZIP, no code signing, and no automatic updates (sections 19 and 20). |
| 1.6 | 2026-11-23 | REL-02: recorded that the ZIP carries the user guide and release notes, changed the confirmed update decision from replacing the executable to replacing the extracted folder's files so that an update also refreshes those documents, and pointed to the requirement traceability matrix (section 20). No numbered requirement changed. |
| 1.7 | 2026-11-23 | REL-03: recorded the project owner's choice of the MIT License, carried in the repository and the ZIP, and that the global shortcut captures nothing while one of the application's own dialogs is open (section 20). No numbered requirement's text changed. |

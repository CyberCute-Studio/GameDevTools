# Shared UI and accessibility conventions

## Controls and keyboard behavior

Shared Win32 controls live in `internal/ui`. Use standard `BUTTON`, `EDIT`, and `STATIC` controls rather than custom-painted replacements. Standard controls supply keyboard operation, visible focus, accessibility metadata, and Windows high-contrast behavior. Parent windows must pass unhandled messages through `IsDialogMessageW` so Tab, Shift+Tab, mnemonics, and arrow-key navigation continue to work.

A dimension input has an adjacent label such as `Width (px)`, a keyboard-focusable edit field, and a validation message directly below it. Field labels immediately precede their edits in tab order and retain mnemonics so Windows can associate the accessible name and keyboard shortcut. Other labels display ampersands literally. Dimension displays use `width × height px` through `ui.FormatDimensions`. Feature modules own their allowed ranges and validation rules; the shared parser only requires a positive whole number representable by `int`.

Chosen paths are displayed in `ui.ReadOnlyText`: a keyboard-focusable, horizontally scrollable read-only edit that keeps long folders readable without allowing manual edits. Folder changes go through `ui.BrowseForFolder`, which wraps the Windows folder picker, returns the absolute path or a cancellation, and requires no manual path entry. `ui.Button.SetChecked` sets radio and check-box state, and feature modules commit edit-field values on the `ui.NotificationEditKillFocus` notification, the same moment shared inputs clear their validation text.

## Validation and errors

Show validation next to the relevant field and explain how to correct it. Errors use `ui.FormatError` so validation, unsupported-format, read/decode, write/encode, clipboard, shortcut-registration, and capture failures remain distinguishable. Error text is always explicit; color is never the only indication of a problem.

## Notifications and confirmation

Use `ui.StatusArea` for non-blocking results. Notices include a textual severity such as `Success`, `Warning`, or `Error`, remain in the window instead of stealing focus, and can expire through a window timer. A zero timeout keeps a notice visible until it is replaced or cleared. Handle timer-creation errors returned by `Show`; the notice remains visible if its timer cannot be created. Replace or clear obsolete results rather than stacking notifications.

Use `ui.Confirm` for destructive confirmation. Its safe answer is the default. Confirmations are modal because they require a decision before an operation; routine status must not use a message box. Result actions such as opening a saved file's folder run only on explicit user request through `ui.RevealInFolder`; the application never opens folders or editors on its own after an operation completes.

## Theme and contrast

Controls use the active Windows system colors. Do not hard-code text, background, border, or focus colors. The shell uses the system window brush, transparent static-text backgrounds, and a DPI-scaled Segoe UI font, while native buttons and edits retain their system theme rendering. This preserves legibility in light, dark, and high-contrast configurations supported by Windows. Any future custom drawing must verify WCAG 2.1 AA contrast: 4.5:1 for normal text, 3:1 for large text, and 3:1 for essential control boundaries and focus indicators.

User-facing text belongs in `internal/ui/strings.go`; shared validation and status prefixes live beside their presentation code. Feature packages may format dynamic details with those centralized strings rather than defining competing labels.

## Preset management

The reusable native preset manager in `internal/preset` is embedded independently in Screenshot and Image Resizer. Its combo box selects a preset and restores the persisted selection on startup. Create and Rename / Save validate the name, dimensions, and module-specific fitting mode; Duplicate also works for built-ins; Delete is enabled only for custom presets and requires confirmation. Validation appears below the relevant field, while persistence failures remain visible in the manager's non-blocking status area without discarding in-memory changes.

package ui

// Every sentence and label the application presents is defined here
// (UX-009), so a translation replaces this file rather than searching the
// feature packages. An error's own text is not: an error value's message —
// from Windows, the Go libraries, or the application's own lower layers,
// such as the Image Resizer's decode, encode, output, and job packages, whose
// messages are English — and the operation a layer names around it appear
// only as the detail after one of these sentences or labels. A typed
// error's kind, not its text, chooses the sentence, its category, and any
// recovery hint, so a translation can replace such a detail by its kind
// (errors.Is, errors.As) without changing the layer that reports it.

// Severity, error-category, and dimension-validation text shared by every
// module's presentation.
const (
	NoticeInfoLabel    = "Information"
	NoticeSuccessLabel = "Success"
	NoticeWarningLabel = "Warning"
	NoticeErrorLabel   = "Error"
	// LabelSeparator joins a severity or error-category label to its
	// message.
	LabelSeparator = ": "

	ErrorValidationLabel           = "Validation error"
	ErrorUnsupportedFormatLabel    = "Unsupported format"
	ErrorReadDecodeLabel           = "Read/decode error"
	ErrorWriteEncodeLabel          = "Write/encode error"
	ErrorClipboardLabel            = "Clipboard error"
	ErrorShortcutRegistrationLabel = "Shortcut registration error"
	ErrorCaptureLabel              = "Capture error"
	ErrorUnexpectedLabel           = "Unexpected error"
	ErrorOtherLabel                = "Error"

	// DimensionWidthName and DimensionHeightName name the two dimensions in
	// validation messages.
	DimensionWidthName  = "Width"
	DimensionHeightName = "Height"
	// DimensionUnitSuffix labels a dimension field's pixel unit (UX-004).
	DimensionUnitSuffix = " (px)"
	DimensionsFormat    = "%d × %d px"
	DimensionRequired   = "%s is required. Enter a whole number of pixels greater than 0."
	DimensionNotWhole   = "%s must be a whole number of pixels greater than 0."
	DimensionTooLarge   = "%s is too large. Enter a whole number no greater than %d pixels."
	DimensionZero       = "%s must be greater than 0 pixels."
)

const (
	AppName = "CyberCute Dev Tools"
	// AppFileName names the executable and the diagnostic log.
	AppFileName     = "cybercute-dev-tools"
	ToolsHeading    = "TOOLS"
	PlaceholderNote = "Placeholder view. No files or images are changed."

	ScreenshotNavigation    = "Screenshot"
	ScreenshotTitle         = "Screenshot"
	ScreenshotDescription   = "Choose the capture mode, manage exact-dimension presets, and configure the save format, JPEG quality, output folder, and global shortcut. Full Display capture runs from Start Capture; Free Rectangle opens the selection overlay to draw, adjust, and capture a region, and Preset Rectangle floats an exact-size frame you position over the content to capture. Start Capture also works from other applications through the global shortcut."
	StartCaptureAction      = "Start Capture"
	CaptureAction           = "Capture"
	CancelAction            = "Cancel"
	AddImagesAction         = "Add &Images…"
	OpenOutputFolderAction  = "Open Output Folder"
	ImageResizerNavigation  = "Image Resizer"
	ImageResizerTitle       = "Image Resizer"
	ImageResizerDescription = "Add PNG or JPEG files with Add Images or by dropping files — or a folder of images — anywhere on this view, review the source list, then create and select a resize preset with its fitting mode and configure the output folder, format, collision policy, JPEG quality, and JPEG transparency background. Start Resize resizes every ready image in the background: progress and Cancel Resize appear below, and each image's row shows its result, with the details for the selected image under its full path."
	StartResizeAction       = "Start Resize"
	SettingsNavigation      = "Settings"
	SettingsTitle           = "Settings"
	SettingsDescription     = "Application preferences are stored locally in a versioned per-user settings file. Diagnostic logs rotate automatically and exclude image, clipboard, and filename data."

	LocalSettingsUnavailable  = "Local settings are unavailable: %v. Changes will remain in memory for this session."
	LoggingUnavailable        = "Diagnostic logging is unavailable: %v."
	SettingsPathUnavailable   = "Settings are unavailable because the per-user configuration path could not be determined. Changes will remain in memory for this session."
	SettingsReadFailed        = "Settings could not be read from %s: %v. Defaults are in use and saving is disabled to protect the original file."
	SettingsFolderUnreachable = "The location of %s cannot be reached: %v. Defaults are in use and saving is disabled so that no settings stored there are replaced. Reconnect its drive or network share and start the application again. If it is gone for good, delete the link or junction at that path or its folder and start the application again, which gives up any settings stored there."
	// LegacySettingsUnreadable reports an old settings file that exists but
	// cannot be read, which blocks saving at every launch until it can be.
	LegacySettingsUnreadable = "Settings could not be read from %s: %v. Defaults are in use and saving is disabled so that those settings are not abandoned. Repair access to that old settings file and start the application again. If you no longer need it, delete or move it and start the application again, which gives up the settings in it."
	// SettingsFileAppeared is why a save fails once it has found a settings
	// file the session was only creating, as when another of the user's
	// Windows sessions saved first.
	SettingsFileAppeared          = "saving is disabled because a settings file exists that this session has not backed up (%w). Start the application again to load it, which gives up changes not yet saved, or use Restore Default Settings to back it up and replace it"
	SettingsInvalidBackupFailed   = "Settings in %s are invalid (%v), and a backup could not be created: %v. Defaults are in use and saving is disabled to protect the original file."
	SettingsInvalidRecovered      = "Settings were invalid (%v). The original was backed up to %s and defaults are now in use."
	DefaultSettingsWriteFailed    = "Default settings could not be written: %v. Defaults remain in memory."
	SettingsMigrationBackupFailed = "Settings were migrated in memory, but the original could not be backed up: %v. Saving is disabled to protect %s."
	MigratedSettingsWriteFailed   = "Migrated settings could not be written: %v. The original remains at %s."
	// LegacySettingsNotAdopted and InvalidLegacySettingsNotAdopted report a
	// first launch that could not write the new settings file, which blocks
	// saving for the session, and at every launch while that folder stays
	// unusable, so they say what ends it.
	LegacySettingsNotAdopted        = "Your settings could not be copied to the new settings file: %[1]v. Changes in this session will not be saved. Your settings remain in %[2]s, and the next launch will try again. If this keeps happening, make sure the folder %[3]s can be written: reconnect its drive or network share or, if it is gone for good, delete the link or junction at that path, which gives up any settings stored through it, or move away a file that holds its name. Then start the application again."
	InvalidLegacySettingsNotAdopted = "Settings in %[1]s are invalid (%[2]v), and the new settings file could not be written: %[3]v. Defaults are in use and changes in this session will not be saved. The original remains in %[1]s, and the next launch will try again. If this keeps happening, make sure the folder %[4]s can be written: reconnect its drive or network share or, if it is gone for good, delete the link or junction at that path, which gives up any settings stored through it, or move away a file that holds its name. Then start the application again."
	SettingsSaveFailed              = "Settings could not be saved: %v. Your current in-memory session has not been discarded."
	SettingsCloseTitle              = AppName + " — Settings not saved"
	SettingsCloseConfirmation       = "Settings could not be saved. Close anyway? These changes will not be available after the application closes."

	// The Settings view's restore of the default settings (SET-005).
	SettingsRestoreDescription  = "Restore Default Settings returns the global shortcut, the capture and resize options, and both output folders to their defaults. Custom presets, each module's selected preset, the images in the resize list, and saved images are kept; a preset is deleted only with Delete in its module. The current settings file is backed up first, in the same folder."
	RestoreDefaultsAction       = "&Restore Default Settings"
	RestoreDefaultsTitle        = AppName + " — Restore default settings"
	RestoreDefaultsConfirmation = "Restore the default settings?\n\nThe global shortcut, the capture and resize options, and both output folders return to their defaults. Custom presets, each module's selected preset, and saved images are kept.\n\nThe current settings file is backed up first."
	SettingsRestored            = "Default settings restored. The previous settings were backed up to %s."
	SettingsRestoredNoBackup    = "Default settings restored."
	SettingsRestoreFailed       = "Default settings were not restored: %v. Nothing was changed."
	// SettingsRestoreBlocked is why a restore refuses while saving is blocked
	// and there is no settings file to back up; SettingsRestoreFailed joins it.
	SettingsRestoreBlocked    = "saving is disabled for this session and no settings file was found at %s to restore (%v). Resolve the problem that disabled saving, then start the application again. If it is a drive or network share that is gone for good, delete the link or junction leading to it and start the application again, which gives up any settings stored there. If it is an old settings file that cannot be read and you no longer need it, delete or move it and start the application again, which gives up the settings in it"
	SettingsRestoreSaveFailed = "Default settings are in use for this session, but they could not be saved: %v. The settings file still holds the previous settings, which were also backed up to %s."
	// SettingsRestoreSaveFailedNoFile is the restore's failed write when
	// no settings file was found to back up, which may be one on a drive or
	// share that is down.
	SettingsRestoreSaveFailedNoFile = "Default settings are in use for this session, but they could not be saved: %v. No settings file was found to back up; a file on a drive or network share that is unavailable has not been changed."
	SettingsRestoreInMemory         = "Default settings are in use for this session, but they cannot be saved: %v. Changes will remain in memory for this session."
	// SettingsRestoreFollowUp joins what a module still needs after the
	// restore, such as a default shortcut another application holds, to the
	// restore's outcome.
	SettingsRestoreFollowUp = " %s"

	PresetNameRequired         = "Preset name is required. Enter a name."
	PresetNameInvalid          = "Preset name cannot contain a null character. Remove it and enter a valid name."
	PresetDimensionRange       = "%s must be between 1 and 16,384 pixels. Enter a whole number in that range."
	PresetResizePixelLimit     = "Resize presets must not exceed 100 megapixels (100,000,000 pixels). Reduce the width or height."
	PresetResizeMode           = "Choose Fit, Stretch, or Crop to Fill for the Image Resizer preset."
	PresetScreenshotResizeMode = "Screenshot presets do not have a resize fitting mode."

	PresetLibraryLabel       = "&Presets"
	PresetNameLabel          = "&Name"
	PresetWidthLabel         = "&Width"
	PresetHeightLabel        = "&Height"
	PresetResizeModeLabel    = "&Fitting mode"
	PresetCreateAction       = "Create Preset"
	PresetRenameAction       = "Rename / Save"
	PresetDuplicateAction    = "Duplicate"
	PresetDeleteAction       = "Delete"
	PresetBuiltInSuffix      = " (built-in)"
	PresetListFormat         = "%s — %s%s"
	PresetDeleteTitle        = AppName + " — Delete preset"
	PresetDeleteConfirmation = "Delete the custom preset %q? This cannot be undone."
	PresetCreated            = "Preset %q was created."
	PresetUpdated            = "Preset %q was updated."
	PresetDuplicated         = "Preset %q was created as a copy."
	PresetDeleted            = "Preset %q was deleted."
	PresetSelected           = "Preset %q is selected."
	PresetSelectionRequired  = "Select a preset before capturing."
	PresetSaveFailed         = "The preset change remains in memory, but settings could not be saved: %v"
	PresetOperationFailed    = "Preset operation failed: %v"

	PresetNameHD     = "HD"
	PresetNameFullHD = "Full HD"
	PresetNameQHD    = "QHD"
	PresetName4KUHD  = "4K UHD"
	// PresetNameDimensions names a built-in Image Resizer preset after its
	// width and height.
	PresetNameDimensions = "%d × %d"

	ResizeModeFit        = "Fit"
	ResizeModeStretch    = "Stretch"
	ResizeModeCropToFill = "Crop to Fill"

	CaptureModeLabel           = "&Capture mode"
	CaptureModeFreeRectangle   = "Free Rectangle"
	CaptureModePresetRectangle = "Preset Rectangle"
	CaptureModeFullDisplay     = "Full Display"
	OutputFormatLabel          = "Save &format"
	ImageFormatPNG             = "PNG"
	ImageFormatJPEG            = "JPEG"
	JPEGQualityLabel           = "JPEG &quality (1–100)"
	GlobalShortcutLabel        = "&Global shortcut"
	OutputFolderLabel          = "&Output folder"
	BrowseFolderAction         = "&Browse…"
	BrowseFolderDialogTitle    = "Choose the screenshot output folder"
	FilenamePatternLabel       = "Filename pattern"
	OutputFolderUnavailable    = "The default Pictures output folder is unavailable: %v. Choose an output folder before capturing."

	CaptureModeInvalid      = "Capture mode must be Free Rectangle, Preset Rectangle, or Full Display."
	ImageFormatInvalid      = "Screenshot save format must be PNG or JPEG."
	JPEGQualityRange        = "JPEG quality must be between 1 and 100. Enter a whole number in that range."
	ShortcutInvalid         = "Enter a shortcut such as Ctrl+Alt+S: at least one of Ctrl, Alt, Shift, or Win, plus a letter, digit, or function key (F1–F24)."
	ShortcutConflictAction  = "Choose a different shortcut."
	ShortcutConflict        = "the shortcut is already registered by another application"
	ShortcutConflictDetail  = "%w: %s. " + ShortcutConflictAction
	ShortcutRegisterFailed  = "The shortcut %s could not be registered: %v"
	OutputFolderRequired    = "An output folder is required. Choose a folder with Browse."
	OutputFolderInvalid     = "The output folder contains characters that are not valid text. Choose the folder again."
	OutputFolderNotAbsolute = "The output folder must be an absolute local path. Choose the folder again."
	OutputFolderNameInvalid = "The output folder cannot be used: " + outputFolderNameRule + ", so results would not be saved in the folder named. Choose another folder."
	OutputFolderRootInvalid = "The output folder cannot be used: " + outputFolderRootRule + ". Choose a folder with Browse."

	// OutputFolderNameReason is why the default Screenshot folder is
	// unavailable when its path breaks the folder name rule.
	OutputFolderNameReason = outputFolderNameRule
	// OutputFolderRootReason is why it is unavailable when its path names no
	// folder at all.
	OutputFolderRootReason = outputFolderRootRule
	// OutputFolderNotAbsoluteReason is why the default Screenshot folder is
	// unavailable when the Pictures folder it lies under has a relative path.
	OutputFolderNotAbsoluteReason = "its path is not absolute"
	// OutputFolderTextReason is why the default Screenshot folder is
	// unavailable when its path holds text no capture could save under.
	OutputFolderTextReason = "its path contains characters that are not valid text"
	// OutputFolderStoredRefused is the startup warning about a saved
	// Screenshot folder every capture would refuse.
	OutputFolderStoredRefused = "The saved output folder for screenshots, %s, cannot be used: " + outputFolderNameRule + ". Captures are still copied to the clipboard, but are not saved until you choose another folder with Browse."
	// OutputFolderStoredRootRefused is that warning for a saved folder that
	// names no folder at all.
	OutputFolderStoredRootRefused = "The saved output folder for screenshots, %s, cannot be used: " + outputFolderRootRule + ". Captures are still copied to the clipboard, but are not saved until you choose another folder with Browse."

	// outputFolderNameRule and outputFolderRootRule state the two output
	// folder rules, for every message that reports a folder breaking one.
	outputFolderNameRule = "Windows would change a name in its path, such as a name that ends in a space or a period, or read one as a device name such as NUL, or as a file stream because it contains a colon"
	outputFolderRootRule = "its path names a drive, volume, device, or server rather than a folder on it, leads through a server's pipe, mailslot, or IPC$ share, or has no plain server and share name"

	CaptureModeSelected    = "Capture mode is now %s."
	OutputFormatSelected   = "Screenshots are saved as %s."
	JPEGQualitySaved       = "JPEG quality is now %d."
	ShortcutSaved          = "Global screenshot shortcut is now %s."
	OutputFolderSaved      = "Screenshots are saved in %s."
	OptionsSaveFailed      = "The capture setting change remains in memory, but settings could not be saved: %v"
	OptionsOperationFailed = "Capture setting operation failed: %v"

	DisplayLabel           = "&Display"
	DisplayListItem        = "%d — %s — %d × %d px (%d%%)"
	DisplayListItemPrimary = "%d — %s — %d × %d px (%d%%, primary)"
	DisplayUnavailable     = "The display list is unavailable: %v"
	DisplayRequired        = "Choose a display before capturing."
	OpenFolderAction       = "Open Folder"
	OpenFolderFailed       = "The folder of the saved screenshot could not be opened: %v"

	// The result card that reports a capture while the Screenshot view cannot
	// be seen.
	CardWindowTitle   = AppName + " capture result"
	CardDismissAction = "Dismiss"
	// CardDismissProblemAction is the Screenshot view's keyboard-reachable
	// counterpart to the card's Dismiss.
	CardDismissProblemAction = "Dismiss Problem"
	CardEarlierResults       = "%d earlier problem(s) not yet dismissed."
	// CaptureShortcutRefused is on the card once a capture has ended, so it
	// says what happened rather than asking the user to wait.
	CaptureShortcutRefused = "The shortcut was pressed during a capture, so no second capture was taken."
	// CaptureShortcutDialogOpen refuses the shortcut while one of the
	// application's dialogs is open, as the dialog would be in the image. It
	// can outlast the dialog on the card, so it says what happened.
	CaptureShortcutDialogOpen = "The shortcut was pressed while a dialog of this application was open, so nothing was captured. Press it again once the dialog is closed."
	// CaptureTemporaryLeftBehind follows the result of a capture that was
	// saved but whose temporary name could not be removed. The temporary name
	// is a hard link to the saved image, so removing it loses nothing.
	CaptureTemporaryLeftBehind = " A temporary name for the saved image could not be removed, and can be deleted: %v"

	CaptureInProgress      = "A capture is already in progress. Wait for it to finish."
	CaptureFailed          = "The display could not be captured: %v"
	CaptureDisplayChanged  = "The display layout changed or a display disconnected before the capture could finish: %v. Check the connected displays and try again."
	CaptureNotOnDisplay    = "The selected area is not on any connected display, so nothing was captured. Select an area on a display and try again."
	CaptureGraphicsFailed  = "The graphics system could not read the screen: %v. If a full-screen exclusive application or game is running, switch it to windowed mode and try again."
	CaptureSucceeded       = "Display captured. Saved to %s and copied to the clipboard."
	CaptureSaveFailed      = "Display captured and copied to the clipboard, but saving failed: %v"
	CaptureClipboardFailed = "Display captured and saved to %s, but copying to the clipboard failed: %v"
	CaptureOutputsFailed   = "Display captured, but saving failed (%v) and copying to the clipboard failed (%v)."

	OverlayWindowTitle        = AppName + " rectangle selection overlay"
	PresetFrameWindowTitle    = AppName + " preset rectangle frame"
	OverlayToolbarTitle       = AppName + " rectangle selection toolbar"
	OverlayInstructions       = "Drag to draw a rectangle, or type its size and press Enter. Move it with a drag or the arrow keys; resize it with a handle. Escape cancels."
	OverlayPresetInstructions = "Drag the frame's border to move it, or nudge it with the arrow keys (Shift for ten pixels). Type exact dimensions to resize it. Escape cancels."
	SelectionWidthLabel       = "&W (px)"
	SelectionHeightLabel      = "&H (px)"
	SelectionPositionFormat   = "X: %d, Y: %d"
	SelectionFitInvalid       = "The typed dimensions do not fit at the selection's position. Move the selection or enter smaller dimensions."
	SelectionTooLarge         = "The typed dimensions do not fit on the desktop. Enter smaller dimensions."
	SelectionResizeFailed     = "The frame could not be resized: %v. The previous dimensions were kept."
	PresetRectangleTooLarge   = "the preset rectangle (%d × %d physical pixels) is larger than the entire virtual desktop (%d × %d physical pixels). Choose or create a smaller preset"
	CaptureOverlayFailed      = "The selection overlay could not be shown: %v"
	CaptureSessionActive      = "A rectangle selection is already on screen. Finish or cancel it before starting another."
	CaptureSelectionEnded     = "Rectangle selection cancelled. Nothing was captured."
	CaptureSessionDisplayLost = "The display layout changed and the selection could not be kept in the new layout. Nothing was captured; start the capture again."
	CaptureSessionDisplayGone = "The display layout could not be read after a display change. Nothing was captured; start the capture again."

	// The reasons the Screenshot module gives, after one of the sentences
	// above, for a session or capture that could not start.
	SelectionSessionActive    = "a rectangle selection session is already active"
	PresetRectangleNoFit      = "the preset rectangle does not fit the virtual desktop"
	PresetRectangleEmpty      = "the preset rectangle needs a width and height of at least one pixel"
	VirtualDesktopEmpty       = "the virtual desktop has no area to select from"
	DisplayServiceUnavailable = "the display service is unavailable"
	OverlayServiceUnavailable = "the overlay service is unavailable"
	CaptureServiceUnavailable = "the capture service is unavailable"

	SelectionCaptureFailed          = "The selection could not be captured: %v"
	SelectionCaptureSucceeded       = "Selection captured. Saved to %s and copied to the clipboard."
	SelectionCaptureSaveFailed      = "Selection captured and copied to the clipboard, but saving failed: %v"
	SelectionCaptureClipboardFailed = "Selection captured and saved to %s, but copying to the clipboard failed: %v"
	SelectionCaptureOutputsFailed   = "Selection captured, but saving failed (%v) and copying to the clipboard failed (%v)."

	ResizeModeSummaryLabel       = "Selected fitting mode"
	ResizeModeFitDescription     = "Fit (%d × %d px): the image is scaled uniformly to fit inside these dimensions without cropping or padding. Proportions are preserved, so one side can end up smaller than the preset."
	ResizeModeStretchDescription = "Stretch (%d × %d px): width and height are scaled independently to exactly these dimensions. Sources with a different aspect ratio will look distorted."
	ResizeModeCropDescription    = "Crop to Fill (%d × %d px): the image is scaled uniformly until it covers these dimensions, then cropped evenly from opposite sides to exactly this size. Content at the trimmed edges is lost."

	ResizeFormatLabel        = "Output for&mat"
	ResizeFormatKeepSource   = "Keep Source Format"
	ResizeCollisionLabel     = "When an output file already e&xists"
	ResizeCollisionRename    = "Rename"
	ResizeCollisionSkip      = "Skip"
	ResizeCollisionOverwrite = "Overwrite"
	ResizeBackgroundLabel    = "&JPEG transparency background (#RRGGBB)"
	ResizeNamingLabel        = "Output file names"
	ResizeNamingBehavior     = "Each output file keeps its source file's name; the extension matches the output format (.png or .jpg)."
	BrowseResizeFolderTitle  = "Choose the resized images output folder"

	ResizeFormatInvalid     = "The output format must be Keep Source Format, PNG, or JPEG."
	CollisionInvalid        = "The collision policy must be Rename, Skip, or Overwrite."
	ResizeBackgroundInvalid = "Enter a background color as # followed by six hexadecimal digits, such as #FFFFFF."

	ResizeCollisionRenameDescription    = "Rename (default): the new file is saved as name_2, name_3, and so on until an unused name is found. Existing files stay untouched."
	ResizeCollisionSkipDescription      = "Skip: the item is marked Skipped and the existing file stays untouched, as does a source image with that name. When two images in the job would produce the same file name, only one is saved: the first, or the next if the first cannot be saved."
	ResizeCollisionOverwriteDescription = "Overwrite: an existing file with the output's name is replaced, but only after you confirm when the job starts. Source images are never overwritten: when the existing item is a source image, another output of the same job, a folder or link, or a file that cannot be confirmed not to be a source image, the output is saved as name_2, name_3, and so on, at the first unused name, so no other file is replaced."

	ResizeFormatSelected     = "Resized images are saved as %s."
	ResizeFormatKeepSelected = "Resized images keep their source format."
	ResizeCollisionSelected  = "Collision policy is now %s."
	ResizeBackgroundSaved    = "JPEG transparency background is now %s."
	ResizeOutputFolderSaved  = "Resized images are saved in %s."
	ResizeOptionsSaveFailed  = "The resize setting change remains in memory, but settings could not be saved: %v"
	ResizeOperationFailed    = "Resize setting operation failed: %v"
	ResizeFolderUnavailable  = "The default Pictures output folder is unavailable: %v. Choose an output folder before resizing."
	ResizeFolderReplaced     = resizeSavedFolderRefused + " The default output folder is selected instead; choose another folder with Browse."
	ResizeFolderRefused      = resizeSavedFolderRefused + " The default Pictures output folder is unavailable too: %v. Choose an output folder with Browse before resizing."
	// ResizeDefaultFolderUnknown is the reason ResizeFolderUnavailable and
	// ResizeFolderRefused give when no folder is selected and nothing says
	// why the default folder is unavailable.
	ResizeDefaultFolderUnknown = "its location is unknown"

	// resizeSavedFolderRefused begins both startup warnings about a saved
	// output folder that only earlier builds accepted.
	resizeSavedFolderRefused = "The saved output folder for resized images, %s, can no longer be used: " + outputFolderNameRule + ", or " + outputFolderRootRule + "."

	SourceImagesLabel    = "&Source images"
	SourceImagesHint     = "Drop PNG or JPEG files — or a folder of images — anywhere on this view, or choose files with Add Images. Each file's content is checked in the background, and sources are opened read-only and never modified."
	SourcePathLabel      = "Full p&ath"
	RemoveSelectedAction = "&Remove Selected"
	ClearListAction      = "&Clear List"

	SourceStatusReady        = "Ready"
	SourceStatusUnsupported  = "Unsupported format"
	SourceStatusInaccessible = "Cannot be read"
	SourceStatusChecking     = "Checking…"
	SourceStatusCorrupt      = "Damaged or incomplete"
	SourceStatusTooLarge     = "Too large"
	SourceUnsupportedDetail  = "The extension is not .png, .jpg, or .jpeg."
	SourceListFormat         = "%s — %s"
	SourceListReadyFormat    = "%s — %s — %s"

	SourceCheckingDetail            = "The image content is being checked."
	SourceReadyDetail               = "%s image, %s"
	SourceReadyAlpha                = ", with an alpha channel"
	SourceReadyOriented             = ", upright after applying its EXIF orientation (stored as %s)"
	SourceContentMismatch           = " The content is %s data despite the %s extension, so it is processed as %s."
	SourceFormatUnknown             = "image"
	SourceContentUnrecognizedDetail = "The file content is not PNG or JPEG image data, whatever its extension."
	SourceFeatureUnsupportedDetail  = "This %s file uses a feature the image decoder does not support (%v)."
	SourceCorruptDetail             = "The %s data is damaged or incomplete and cannot be decoded (%v)."
	SourceEmptyDetail               = "The file is empty."
	SourceReadFailedDetail          = "The file could not be read."
	SourceTooLargeDetail            = "At %s this image needs more than the %d MiB of memory one source may use to decode."

	ImageFileFilterLabel    = "PNG and JPEG images"
	ImagePickerTitle        = "Choose images to resize"
	ImagePickerTooManyFiles = "Too many files were selected for the picker to list. Select fewer files at a time."
	ImagePickerFailed       = "The image file picker could not be opened: %v"

	ImagesAdded             = "Added %d image(s)."
	ImagesAddEmpty          = "No supported PNG or JPEG files were found."
	ImagesAddedDuplicates   = " %d already in the list."
	ImagesAddedUnsupported  = " %d listed with an unsupported format."
	ImagesAddedInaccessible = " %d could not be read."
	ImagesAddedChecking     = " Checking image content…"
	ImagesChecked           = "Checked %d image(s): %d ready."
	ImagesCheckedFailed     = " %d cannot be resized — select an item to see why."
	ImagesRemoved           = "Removed %d image(s)."
	ImagesCleared           = "Source list cleared."
	ImagesRemoveNone        = "Select one or more source images to remove."
	ImagesBusy              = "Images cannot be added, removed, or cleared while a resize job is running. Cancel the job or wait for it to finish."

	CancelResizeAction = "Cancel Resize"

	ResizeJobStarting       = "Starting to resize %d image(s)…"
	ResizeJobRechecking     = " First checking again the %d image(s) that could not be used: resizing starts once these checks finish, which takes longer while a drive is not responding."
	ResizeJobProgress       = "Resizing: %d of %d image(s) done — %d completed, %d skipped, %d failed."
	ResizeJobProcessing     = " Processing %s."
	ResizeJobProcessingMore = " Processing %s and %d more."
	ResizeJobCancelling     = "Cancelling: no further images start, and the images in progress stop at their next safe point. %d of %d image(s) done; completed files are kept."
	ResizeJobFinished       = "Resize finished: %d completed, %d skipped, %d failed, %d cancelled, of %d image(s). Select an image in the list to see its result."
	ResizeJobCancelled      = "Resize cancelled: %d completed, %d skipped, %d failed, %d cancelled, of %d image(s). Completed files were kept; select an image in the list to see its result."
	ResizeJobLeftBehind     = " %d image(s) left a file that could not be removed — select an image to see where."
	ResizeJobReleased       = " The job did not stop when cancelled, so it was let go: a read or write may not be responding, as on a network drive that stopped responding. You can change the list and start again."
	ResizeJobInterrupted    = "The resize job stopped unexpectedly: %v. Images that finished keep their results; the others are marked Failed."
	ResizeJobRunning        = "A resize job is already running. Cancel it or wait for it to finish."
	ResizeJobChecking       = "%d image(s) are still being checked. Wait until checking finishes, or remove those images, then start again."
	ResizeJobNoImages       = "No image in the list is ready to resize. Add PNG or JPEG images and wait until they show Ready."
	ResizeJobUnavailable    = "The resize job cannot run because the view cannot report its progress."
	ResizePresetRequired    = "Select a resize preset before starting."
	ResizePresetInvalid     = "The selected preset cannot be used: %v. Choose or edit another preset."
	ResizeJobFailed         = "The resize job could not start: %v"
	ResizeJobFolderFailed   = "The output folder could not be created or used: %v. Choose another folder with Browse, or check that its drive is connected and writable."
	ResizeJobJPEGInvalid    = "The JPEG options cannot be used: %v"
	ResizeJobNotConfirmed   = "Resize not started: existing files are replaced only after you confirm. Choose Rename or Skip to keep them."
	ResizeOverwriteTitle    = AppName + " — Replace existing files"
	ResizeOverwriteConfirm  = "Resized images will replace existing files with the same names in %s.\n\nSource images are never overwritten, and an image whose existing file cannot be confirmed safe to replace is saved under a numbered name instead.\n\nReplace existing files?"
	ResizeOpenFolderFailed  = "The output folder could not be opened: %v"
	ResizeCloseTitle        = AppName + " — Resize job running"
	ResizeCloseConfirmation = "A resize job is running. Close anyway?\n\nClosing cancels the job: images already resized are kept, and the remaining images are not resized."

	ResizeStatePending    = "Pending"
	ResizeStateProcessing = "Processing…"
	ResizeStateCompleted  = "Completed"
	ResizeStateSkipped    = "Skipped"
	ResizeStateFailed     = "Failed"
	ResizeStateCancelled  = "Cancelled"

	ResizeDetailPending           = "Pending: waiting to be resized."
	ResizeDetailProcessing        = "Processing: being resized and saved to %s."
	ResizeDetailCompleted         = "Completed: saved to %s."
	ResizeDetailRenamed           = " A numbered name was used because %s."
	ResizeDetailSkipped           = "Skipped: %s was left untouched because %s."
	ResizeDetailFailed            = "Failed: %s"
	ResizeDetailCancelled         = "Cancelled: the job was cancelled before this image finished, so nothing was saved for it."
	ResizeDetailReleased          = "Cancelled: this image had not stopped when the cancelled job was let go — a read or write of it may not be responding. Nothing is saved for it unless its result was already being given its name."
	ResizeDetailLeftBehind        = " A file could not be removed afterwards: %v"
	ResizeRenamedExists           = "a file with the image's own name already exists in the output folder"
	ResizeRenamedDuplicate        = "an earlier image in this job saves under the same name"
	ResizeRenamedSource           = "the image's own name belongs to a source image, which is never overwritten"
	ResizeRenamedUnverified       = "the existing file with the image's own name could not be confirmed safe to replace"
	ResizeRenamedDevice           = "Windows reserves the image's own name for a device"
	ResizeRenamedOther            = "the image's own name was unavailable"
	ResizeSkippedExists           = "a file with that name already exists"
	ResizeSkippedDuplicate        = "another image in this job saved that name"
	ResizeSkippedSource           = "it is a source image, which is never overwritten"
	ResizeSkippedUnverified       = "it could not be confirmed safe to replace"
	ResizeSkippedDevice           = "Windows reserves that name for a device"
	ResizeSkippedOther            = "its name was unavailable"
	ResizeHintDiskFull            = " Free space on the output drive, then run the job again."
	ResizeHintPermission          = " Check that you may write to the output folder, or choose another folder with Browse."
	ResizeHintChanged             = " Run the job again to plan around the file as it is now."
	ResizeHintFolder              = " Check that the output folder still exists and its drive is connected, then run the job again."
	ResizeSourceUnavailableDetail = "The source list could not use this image."
)

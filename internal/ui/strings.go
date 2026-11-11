package ui

const (
	AppName         = "GameDevTools"
	ToolsHeading    = "TOOLS"
	PlaceholderNote = "Placeholder view. No files or images are changed."

	ScreenshotNavigation    = "Screenshot"
	ScreenshotTitle         = "Screenshot"
	ScreenshotDescription   = "Capture a free rectangle, an exact preset rectangle, or one full display. Capture tools will be added by the Screenshot module items."
	StartCaptureAction      = "Start Capture"
	CaptureAction           = "Capture"
	CancelAction            = "Cancel"
	AddImagesAction         = "Add Images"
	OpenOutputFolderAction  = "Open Output Folder"
	ImageResizerNavigation  = "Image Resizer"
	ImageResizerTitle       = "Image Resizer"
	ImageResizerDescription = "Resize PNG and JPEG images with reusable dimensions and fitting modes. Image selection and resize tools will be added by the Image Resizer module items."
	StartResizeAction       = "Start Resize"
	SettingsNavigation      = "Settings"
	SettingsTitle           = "Settings"
	SettingsDescription     = "Application preferences are stored locally in a versioned per-user settings file. Diagnostic logs rotate automatically and exclude image, clipboard, and filename data."

	LocalSettingsUnavailable  = "Local settings are unavailable: %v. Changes will remain in memory for this session."
	LoggingUnavailable        = "Diagnostic logging is unavailable: %v."
	SettingsSaveFailed        = "Settings could not be saved: %v. Your current in-memory session has not been discarded."
	SettingsCloseTitle        = "GameDevTools — Settings not saved"
	SettingsCloseConfirmation = "Settings could not be saved. Close anyway? These changes will not be available after the application closes."
)

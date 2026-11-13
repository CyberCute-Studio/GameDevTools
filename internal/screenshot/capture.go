package screenshot

// CaptureRequest describes one Full Display capture invocation. Window is
// the app-owned window hidden for the duration of the capture (SCR-044);
// DeviceName selects the display to capture (SCR-040); Options carries the
// persisted output settings applied to the clipboard and file delivery.
type CaptureRequest struct {
	Window     uintptr
	DeviceName string
	Options    CaptureOptions
}

// CaptureResult reports the outcome of one Full Display capture. Err is set
// when the window could not be hidden or the display could not be captured,
// in which case no output was attempted. Otherwise Clipboard and Save report
// the two delivery outcomes independently (SCR-060), and Path is the saved
// file whenever the save completed.
type CaptureResult struct {
	Path      string
	Clipboard error
	Save      error
	Err       error
}

// CaptureFunc runs one Full Display capture end to end: it hides the request
// window, captures the display, delivers the outputs, and restores the
// window before returning. It runs off the GUI thread, so callers invoke it
// on a worker goroutine and deliver the result through the shell dispatcher
// (TECH-005). The window is always restored, including after failures.
type CaptureFunc func(CaptureRequest) CaptureResult

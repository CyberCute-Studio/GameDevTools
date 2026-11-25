package screenshot

import "github.com/CyberCute-Studio/GameDevTools/internal/display"

// CaptureRequest describes one Full Display capture invocation. Window is
// the app-owned window hidden for the duration of the capture (SCR-044);
// DeviceName selects the display to capture (SCR-040); Options carries the
// persisted output settings applied to the clipboard and file delivery.
// Captured, when set, runs once the display's pixels have been read and the
// window restored, before delivery, or once the capture has failed: the view
// keeps the application's dialogs closed until then (ui.HoldDialogs).
type CaptureRequest struct {
	Window     uintptr
	DeviceName string
	Options    CaptureOptions
	Captured   func()
}

// CaptureResult reports the outcome of one capture. Err is set when the
// window could not be hidden or the pixels could not be captured, in which
// case no output was attempted. Otherwise Clipboard and Save report the two
// delivery outcomes independently (SCR-060), and Path is the saved file
// whenever the save completed.
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

// RectangleCaptureFunc runs one Free Rectangle capture end to end. The
// overlay session has already hidden the main window and destroyed its own
// windows, so the rectangle's pixels are read first; restore puts the main
// window back as soon as the capture ends and must run on every path,
// including failures (SCR-044, SCR-068). It runs off the GUI thread, so
// callers invoke it on a worker goroutine and deliver the result through
// the shell dispatcher (TECH-005).
type RectangleCaptureFunc func(region display.Rect, restore func(), options CaptureOptions) CaptureResult

// Package execute runs capture sessions end to end: Full Display hides the
// application window, captures one connected display, delivers the image to
// the clipboard and the configured output folder, and restores the window;
// Rectangle captures a virtual-desktop rectangle an overlay session selected
// while every app surface was already hidden, restores the window the
// session passes in, and delivers the same way. The package composes the
// capture and output services without the Screenshot module's UI depending
// on the output package.
//
// The pipeline runs synchronously and never touches the GUI thread. Callers
// run it on a worker goroutine and deliver the result through the shell
// dispatcher, per TECH-005. The window is restored as soon as the capture
// finishes — before delivery — so the application is never left hidden while
// clipboard and file output are still in progress, and it is restored on
// every failure path as well.
package execute

import (
	"image"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/capture"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/output"
)

// Service runs capture sessions. Monitor enumerates the current display
// topology; Hide, Capture, CaptureRect, and Deliver default to the
// production native services and are replaceable in tests. Hide returns the
// restore function that puts the hidden windows back, which FullDisplay
// always calls once the capture ends, including after a capture failure.
type Service struct {
	Monitor *display.Monitor
	Hide    func(...uintptr) (restore func(), err error)
	Capture func(*display.Monitor, string) (*image.RGBA, error)
	// CaptureRect captures a virtual-desktop rectangle. The default
	// implementation flushes the desktop compositor first, so surfaces the
	// overlay session just destroyed cannot linger in the output (SCR-068).
	CaptureRect func(*display.Monitor, display.Rect) (*image.RGBA, error)
	Deliver     func(*image.RGBA, screenshot.CaptureOptions) output.Result
}

// NewService returns a Service backed by the production capture and output
// services.
func NewService(monitor *display.Monitor) *Service {
	return &Service{
		Monitor: monitor,
		Hide: func(windows ...uintptr) (func(), error) {
			exclusion, err := capture.Hide(windows...)
			if err != nil {
				return nil, err
			}
			return exclusion.Restore, nil
		},
		Capture: capture.CaptureDisplay,
		CaptureRect: func(monitor *display.Monitor, region display.Rect) (*image.RGBA, error) {
			if err := capture.FlushCompositor(); err != nil {
				return nil, err
			}
			return capture.CaptureRect(monitor, region)
		},
		Deliver: output.Deliver,
	}
}

// FullDisplay hides the request window, captures the selected display,
// restores the window, and delivers the captured image to the clipboard and
// the output folder. A hide or capture failure is reported as Err and skips
// delivery; delivery outcomes are reported independently. The window is
// restored before this function returns on every path.
func (s *Service) FullDisplay(request screenshot.CaptureRequest) screenshot.CaptureResult {
	restore, err := s.Hide(request.Window)
	if err != nil {
		return screenshot.CaptureResult{Err: err}
	}
	img, captureErr := s.Capture(s.Monitor, request.DeviceName)
	restore()
	if captureErr != nil {
		return screenshot.CaptureResult{Err: captureErr}
	}
	delivered := s.Deliver(img, request.Options)
	return screenshot.CaptureResult{
		Path:      delivered.Path,
		Clipboard: delivered.Clipboard,
		Save:      delivered.Save,
	}
}

// Rectangle captures one virtual-desktop rectangle chosen in an overlay
// session and delivers it to the clipboard and the output folder. The
// session has already hidden the main window and destroyed its own windows,
// so this method only reads the pixels; restore puts the main window back
// and runs as soon as the capture ends — before delivery, and on every
// failure path — so the application is never left hidden while clipboard
// and file output are still in progress. A capture failure is reported as
// Err and skips delivery; delivery outcomes are reported independently.
func (s *Service) Rectangle(region display.Rect, restore func(), options screenshot.CaptureOptions) screenshot.CaptureResult {
	img, captureErr := s.CaptureRect(s.Monitor, region)
	restore()
	if captureErr != nil {
		return screenshot.CaptureResult{Err: captureErr}
	}
	delivered := s.Deliver(img, options)
	return screenshot.CaptureResult{
		Path:      delivered.Path,
		Clipboard: delivered.Clipboard,
		Save:      delivered.Save,
	}
}

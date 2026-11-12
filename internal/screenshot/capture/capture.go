// Package capture provides raw physical-pixel screen capture for the
// Screenshot module. It captures one connected display or an arbitrary
// virtual-desktop rectangle, including rectangles that span monitors, and
// returns opaque RGBA images in physical pixels.
//
// All functions are safe to call from any goroutine. They never touch the
// GUI thread, so callers run them off the GUI thread and deliver results
// through the shell dispatcher, per TECH-005. Concurrent captures are
// serialized internally because they share the screen device context.
//
// The mouse cursor is never part of the output: GDI screen captures read
// the composed desktop without the cursor sprite (SCR-068). App-owned
// surfaces such as the main window, selection overlays, and toolbars are
// excluded by hiding them through Hide before capturing (SCR-044); the
// returned Exclusion restores them.
//
// Failures are reported as *Error values with a Kind, so callers can
// distinguish invalid geometry from a disconnected display and from native
// graphics failures such as protected or exclusive content. A failed
// capture never returns a blank image as success.
package capture

import (
	"errors"
	"fmt"
	"image"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// maxPixels bounds one capture's pixel count. At 4 bytes per pixel this
// caps a single capture's source bitmap near 1 GiB; larger requests are
// rejected as validation errors instead of exhausting memory.
const maxPixels = 1 << 28

// Kind classifies a capture failure so callers can present the right
// recovery path per UX-010.
type Kind int

const (
	// KindValidation means the request itself is invalid: empty or inverted
	// geometry, more than maxPixels pixels, a missing display identifier,
	// or geometry that reaches outside the current virtual desktop.
	KindValidation Kind = iota
	// KindDisplay means the display topology no longer matches the request:
	// the selected display disconnected, or the topology could not be
	// enumerated at all.
	KindDisplay
	// KindNative means the operating system rejected the capture. Protected
	// or exclusive content, remote-session restrictions, and graphics
	// driver failures surface this way; the capture must not be treated as
	// a blank success.
	KindNative
)

func (k Kind) String() string {
	switch k {
	case KindValidation:
		return "validation"
	case KindDisplay:
		return "display"
	case KindNative:
		return "native"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error describes a failed capture operation.
type Error struct {
	Kind Kind
	Op   string
	Err  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func kindError(kind Kind, op string, err error) *Error {
	return &Error{Kind: kind, Op: op, Err: err}
}

// CaptureRect captures an arbitrary rectangle in physical virtual-desktop
// coordinates. The rectangle may span monitors but must fit within the
// current virtual desktop bounds. The returned image has exactly the
// rectangle's physical-pixel dimensions.
func CaptureRect(monitor *display.Monitor, region display.Rect) (*image.RGBA, error) {
	displays, err := monitor.Displays()
	if err != nil {
		return nil, kindError(KindDisplay, "enumerate displays", err)
	}
	if err := validateRegion(display.VirtualDesktopBounds(displays), region); err != nil {
		return nil, err
	}
	return captureRegion(region)
}

// CaptureDisplay captures one connected display, identified by its adapter
// device name, at its current native desktop pixel dimensions and
// orientation (SCR-042). It fails with a KindDisplay error when the display
// is no longer connected, so a stale picker selection never captures the
// wrong surface.
func CaptureDisplay(monitor *display.Monitor, deviceName string) (*image.RGBA, error) {
	displays, err := monitor.Displays()
	if err != nil {
		return nil, kindError(KindDisplay, "enumerate displays", err)
	}
	item, err := findDisplay(displays, deviceName)
	if err != nil {
		return nil, err
	}
	if err := validateRegion(display.VirtualDesktopBounds(displays), item.Bounds); err != nil {
		return nil, err
	}
	return captureRegion(item.Bounds)
}

func validateRegion(desktop, region display.Rect) error {
	switch {
	case region.Empty():
		return kindError(KindValidation, "validate region",
			fmt.Errorf("capture rectangle %v is empty or inverted", region))
	case int64(region.Width())*int64(region.Height()) > maxPixels:
		return kindError(KindValidation, "validate region",
			fmt.Errorf("capture rectangle %d × %d exceeds the %d-megapixel limit",
				region.Width(), region.Height(), maxPixels/1024/1024))
	case region.Left < desktop.Left || region.Top < desktop.Top ||
		region.Right > desktop.Right || region.Bottom > desktop.Bottom:
		return kindError(KindValidation, "validate region",
			fmt.Errorf("capture rectangle %v reaches outside the virtual desktop %v", region, desktop))
	}
	return nil
}

func findDisplay(displays []display.Display, deviceName string) (display.Display, error) {
	if deviceName == "" {
		return display.Display{}, kindError(KindValidation, "find display",
			errors.New("a display device name is required"))
	}
	for _, item := range displays {
		if item.DeviceName == deviceName {
			return item, nil
		}
	}
	return display.Display{}, kindError(KindDisplay, "find display",
		fmt.Errorf("display %s is no longer connected", deviceName))
}

// Exclusion tracks app-owned windows hidden through Hide so captures
// exclude them. Restore shows them again.
type Exclusion struct {
	windows  []uintptr
	restored bool
}

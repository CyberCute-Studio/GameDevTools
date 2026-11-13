//go:build windows

package overlay

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

const (
	vkTab             = 0x09
	gwlExStyle        = ^uintptr(19) // -20 as a uintptr
	wsExLayeredBit    = 0x00080000
	wsExTopmostBit    = 0x00000008
	wsExToolWindowBit = 0x00000080
	wsExNoActivateBit = 0x08000000
)

var (
	procGetWindowLongW             = user32.NewProc("GetWindowLongW")
	procGetWindowRect              = user32.NewProc("GetWindowRect")
	procGetLayeredWindowAttributes = user32.NewProc("GetLayeredWindowAttributes")
	procIsWindow                   = user32.NewProc("IsWindow")
	procIsWindowEnabled            = user32.NewProc("IsWindowEnabled")
	procSetFocus                   = user32.NewProc("SetFocus")
	procGetFocus                   = user32.NewProc("GetFocus")
	procGetDlgItem                 = user32.NewProc("GetDlgItem")
	procSendMessageW               = user32.NewProc("SendMessageW")
)

type win32Rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// newNativeSessionController returns a controller that creates the real
// overlay and toolbar windows but hides and restores through a fake, so the
// test never touches real app windows.
func newNativeSessionController(t *testing.T) (*Controller, *[]string) {
	t.Helper()
	log := &[]string{}
	controller := NewController(display.NewMonitor())
	controller.Hide = func(...uintptr) (func(), error) {
		*log = append(*log, "hide")
		return func() { *log = append(*log, "restore") }, nil
	}
	t.Cleanup(controller.Shutdown)
	return controller, log
}

func nativeOf(t *testing.T, controller *Controller) *windowsSession {
	t.Helper()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session == nil {
		t.Fatal("no active session")
	}
	native, ok := controller.session.native.(*windowsSession)
	if !ok {
		t.Fatalf("session native = %T, want *windowsSession", controller.session.native)
	}
	return native
}

func windowRect(t *testing.T, hwnd windows.Handle) display.Rect {
	t.Helper()
	var bounds win32Rect
	if r, _, err := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&bounds))); r == 0 {
		t.Fatalf("GetWindowRect: %v", err)
	}
	return display.Rect{Left: bounds.Left, Top: bounds.Top, Right: bounds.Right, Bottom: bounds.Bottom}
}

func isWindow(hwnd windows.Handle) bool {
	r, _, _ := procIsWindow.Call(uintptr(hwnd))
	return r != 0
}

func TestNativeSessionCoversVirtualDesktop(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)

	desktop, err := display.NewMonitor().VirtualDesktopBounds()
	if err != nil {
		t.Fatal(err)
	}
	if got := windowRect(t, native.overlay); got != desktop {
		t.Fatalf("overlay rect = %v, want virtual desktop %v", got, desktop)
	}
	exStyle, _, _ := procGetWindowLongW.Call(uintptr(native.overlay), gwlExStyle)
	for _, bit := range []uintptr{wsExLayeredBit, wsExTopmostBit, wsExToolWindowBit, wsExNoActivateBit} {
		if exStyle&bit == 0 {
			t.Fatalf("overlay ex-style %#x lacks %#x", exStyle, bit)
		}
	}
	var alpha byte
	var flags uint32
	if r, _, err := procGetLayeredWindowAttributes.Call(uintptr(native.overlay), 0, uintptr(unsafe.Pointer(&alpha)), uintptr(unsafe.Pointer(&flags))); r == 0 {
		t.Fatalf("GetLayeredWindowAttributes: %v", err)
	}
	if flags&lwaAlpha == 0 || alpha != overlayTintAlpha {
		t.Fatalf("overlay alpha = (%d, flags %#x), want %d with LWA_ALPHA so the input surface stays visible (SCR-052)", alpha, flags, overlayTintAlpha)
	}

	toolbarStyle, _, _ := procGetWindowLongW.Call(uintptr(native.toolbar), gwlExStyle)
	if toolbarStyle&wsExTopmostBit == 0 || toolbarStyle&wsExToolWindowBit == 0 {
		t.Fatalf("toolbar ex-style %#x lacks topmost tool-window bits", toolbarStyle)
	}
	if !isWindow(native.toolbar) {
		t.Fatal("toolbar window was not created")
	}
	placement := windowRect(t, native.toolbar)
	if placement.Left < desktop.Left || placement.Top < desktop.Top || placement.Right > desktop.Right || placement.Bottom > desktop.Bottom {
		t.Fatalf("toolbar rect %v escapes the virtual desktop %v", placement, desktop)
	}
	if r, _, _ := procIsWindowEnabled.Call(uintptr(native.captureButton)); r != 0 {
		t.Fatal("Capture is enabled before a selection exists")
	}

	// Close zeroes the session's handles, so snapshot them before tearing
	// down; checking the zeroed fields would assert nothing.
	overlayHwnd, toolbarHwnd := native.overlay, native.toolbar
	controller.Cancel()
	if isWindow(overlayHwnd) || isWindow(toolbarHwnd) {
		t.Fatal("Cancel left session windows behind")
	}
}

func TestFilterMessageEscapeCancelsSession(t *testing.T) {
	controller, log := newNativeSessionController(t)
	if err := controller.Begin(42, 0, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)

	unrelated := windowMessage{HWND: windows.Handle(0xdead), Message: wmKeyDown, WParam: vkEscape}
	if controller.FilterMessage(unsafe.Pointer(&unrelated)) {
		t.Fatal("filter handled a message for a window the session does not own")
	}
	if !controller.Active() {
		t.Fatal("unrelated message ended the session")
	}

	// Close zeroes the session's handles, so snapshot them before Escape
	// tears the session down; checking the zeroed fields would assert nothing.
	overlayHwnd, toolbarHwnd := native.overlay, native.toolbar
	escape := windowMessage{HWND: overlayHwnd, Message: wmKeyDown, WParam: vkEscape}
	if !controller.FilterMessage(unsafe.Pointer(&escape)) {
		t.Fatal("filter did not handle Escape on the overlay")
	}
	if controller.Active() {
		t.Fatal("session is still active after Escape")
	}
	if isWindow(overlayHwnd) || isWindow(toolbarHwnd) {
		t.Fatal("Escape left session windows behind")
	}
	if got := *log; len(got) != 2 || got[0] != "hide" || got[1] != "restore" {
		t.Fatalf("hide/restore log = %v, want [hide restore]", got)
	}
	if controller.FilterMessage(unsafe.Pointer(&escape)) {
		t.Fatal("filter handled a message after the session ended")
	}
}

func TestToolbarCancelCommandCancelsSession(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	cancelled := 0
	if err := controller.Begin(42, 0, Events{OnCancel: func() { cancelled++ }}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, toolbarCancelCommand, 0)
	if controller.Active() {
		t.Fatal("session is still active after the Cancel command")
	}
	if cancelled != 1 {
		t.Fatalf("OnCancel fired %d times, want 1", cancelled)
	}
}

func TestToolbarCloseCancelsSession(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	cancelled := 0
	if err := controller.Begin(42, 0, Events{OnCancel: func() { cancelled++ }}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	// Alt+F4 delivers WM_CLOSE to the foreground window — the toolbar during
	// a session. Letting DefWindowProc destroy the toolbar alone would leave
	// the input-capturing overlay up with no keyboard recovery (SCR-052).
	// Close zeroes the session's handles, so snapshot them first.
	overlayHwnd, toolbarHwnd := native.overlay, native.toolbar
	procSendMessageW.Call(uintptr(toolbarHwnd), wmClose, 0, 0)
	if controller.Active() {
		t.Fatal("session is still active after the toolbar received WM_CLOSE")
	}
	if cancelled != 1 {
		t.Fatalf("OnCancel fired %d times, want 1", cancelled)
	}
	if isWindow(overlayHwnd) || isWindow(toolbarHwnd) {
		t.Fatal("WM_CLOSE left session windows behind")
	}
}

func TestToolbarCaptureCommandDeliversSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	var captured display.Rect
	if err := controller.Begin(42, 0, Events{OnCapture: func(region display.Rect) { captured = region }}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	region := display.Rect{Left: 4, Top: 8, Right: 804, Bottom: 608}
	controller.SetSelection(region)
	if r, _, _ := procIsWindowEnabled.Call(uintptr(native.captureButton)); r == 0 {
		t.Fatal("Capture stayed disabled after a selection was set")
	}
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, toolbarCaptureCommand, 0)
	if captured != region {
		t.Fatalf("OnCapture region = %v, want %v", captured, region)
	}
	if controller.Active() {
		t.Fatal("session is still active after the Capture command")
	}
}

func TestFilterMessageTabNavigatesToolbar(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	// Capture starts disabled, and disabled controls are skipped by Tab; a
	// selection gives the navigation somewhere to move to.
	controller.SetSelection(display.Rect{Left: 0, Top: 0, Right: 10, Bottom: 10})
	cancelButton := uintptr(0)
	for _, id := range []uintptr{toolbarCancelCommand, toolbarCaptureCommand} {
		hwnd, _, _ := procGetDlgItem.Call(uintptr(native.toolbar), id)
		if hwnd == 0 {
			t.Fatalf("toolbar control %d missing", id)
		}
		if id == toolbarCancelCommand {
			cancelButton = hwnd
		}
	}
	if r, _, _ := procSetFocus.Call(cancelButton); r == 0 {
		t.Skip("SetFocus is unavailable in this session")
	}
	tab := windowMessage{HWND: windows.Handle(cancelButton), Message: wmKeyDown, WParam: vkTab}
	if !controller.FilterMessage(unsafe.Pointer(&tab)) {
		t.Fatal("filter did not give the toolbar dialog navigation")
	}
	focus, _, _ := procGetFocus.Call()
	if focus == 0 || focus == cancelButton {
		t.Fatal("Tab did not move focus within the toolbar")
	}
	controller.Cancel()
}

func TestToolbarPlacement(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: -200, Right: 3840, Bottom: 1080}
	tests := []struct {
		name   string
		anchor display.Rect
		wantX  int32
		wantY  int32
	}{
		{
			name:   "centered above the cursor display work area",
			anchor: display.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040},
			wantX:  (1920 - 480) / 2,
			wantY:  16,
		},
		{
			name:   "negative-coordinate display",
			anchor: display.Rect{Left: -1920, Top: -200, Right: 0, Bottom: 880},
			wantX:  -1920 + (1920-480)/2,
			wantY:  -184,
		},
		{
			name:   "clamped into the desktop",
			anchor: display.Rect{Left: 3800, Top: -200, Right: 3840, Bottom: -100},
			wantX:  3840 - 480,
			wantY:  -184,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y := toolbarPlacement(desktop, test.anchor, 480, 100, 16)
			if x != test.wantX || y != test.wantY {
				t.Fatalf("toolbarPlacement() = (%d, %d), want (%d, %d)", x, y, test.wantX, test.wantY)
			}
		})
	}
}

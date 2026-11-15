//go:build windows

package overlay

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

const (
	vkTab             = 0x09
	wmGetFont         = 0x0031
	gwlExStyle        = ^uintptr(19) // -20 as a uintptr
	wsExLayeredBit    = 0x00080000
	wsExTopmostBit    = 0x00000008
	wsExToolWindowBit = 0x00000080
	wsExNoActivateBit = 0x08000000
)

var (
	procGetWindowLongW  = user32.NewProc("GetWindowLongW")
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procIsWindow        = user32.NewProc("IsWindow")
	procIsWindowEnabled = user32.NewProc("IsWindowEnabled")
	procGetDlgItem      = user32.NewProc("GetDlgItem")
	procSendMessageW    = user32.NewProc("SendMessageW")
	procGetStockObject  = gdi32.NewProc("GetStockObject")
)

type win32Rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// newNativeSessionController returns a controller that creates the real
// overlay and toolbar windows but hides and restores through a fake, so the
// test never touches real app windows. The test goroutine is locked to its
// OS thread: window creation and the tests' SendMessage calls must share
// one thread, or a SendMessage lands cross-thread and waits on a message
// loop the owning thread never pumps.
func newNativeSessionController(t *testing.T) (*Controller, *[]string) {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
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
	// The backing bitmap starts fully dimmed at the tint alpha, so the
	// input-capturing surface is always visible (SCR-052).
	if got := native.pixelSlice()[5*int(native.pixelWidth)+5]; got != dimPixel {
		t.Fatalf("overlay pixel = %#08x, want the dimmed tint %#08x", got, dimPixel)
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{OnCancel: func() { cancelled++ }}); err != nil {
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{OnCancel: func() { cancelled++ }}); err != nil {
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{OnCapture: func(region display.Rect, restore func()) { captured = region; restore() }}); err != nil {
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
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

// TestCreateToolbarAppliesRequestFont pins the creation half of the font
// handoff: Begin receives the shell's current body font, and every toolbar
// control must hold it from the start — the same SetFont path the DPI-change
// update uses (SCR-054).
func TestCreateToolbarAppliesRequestFont(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	// A stock font is a guaranteed-valid HFONT: the controls validate the
	// handle they are handed and keep no record of an invalid one.
	font, _, _ := procGetStockObject.Call(17) // DEFAULT_GUI_FONT
	if err := controller.Begin(42, font, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)

	controls := map[string]windows.Handle{
		"message":      native.message.Handle(),
		"width label":  native.widthLabel.Handle(),
		"height label": native.heightLabel.Handle(),
		"width edit":   native.widthEdit.Handle(),
		"height edit":  native.heightEdit.Handle(),
		"position":     native.position.Handle(),
		"capture":      native.capture.Handle(),
		"cancel":       native.cancel.Handle(),
	}
	for name, hwnd := range controls {
		if got, _, _ := procSendMessageW.Call(uintptr(hwnd), wmGetFont, 0, 0); got != font {
			t.Errorf("%s font at creation = %d, want the request font %d", name, got, font)
		}
	}
	controller.Cancel()
}

// TestNativeSessionAttentionForegroundsToolbar exercises the real SCR-005
// surface: the controller forwards Attention to the live windows session,
// which foregrounds the toolbar without disturbing it.
func TestNativeSessionAttentionForegroundsToolbar(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(0, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	controller.Attention()
	native := nativeOf(t, controller)
	if r, _, err := procIsWindow.Call(uintptr(native.toolbar)); r == 0 {
		t.Fatalf("toolbar was disturbed by Attention: %v", err)
	}
	controller.Cancel()
	// Attention without a session is a no-op and must not panic.
	controller.Attention()
}

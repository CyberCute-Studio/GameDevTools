//go:build windows

package overlay

import (
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

var (
	procWindowFromPoint             = user32.NewProc("WindowFromPoint")
	procWTSQuerySessionInformationW = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")
)

// sessionLockedOrInactive reports whether Windows says this session is locked
// or not the active one. The lock screen is over every topmost window on the
// same desktop, so Windows' own hit test finds it rather than the overlay.
func sessionLockedOrInactive() bool {
	const currentSession, sessionInfoEx = ^uint32(0), 25
	const stateActive, flagsLocked = 0, 0
	// WTSINFOEX level 1: the level, padding, the session's ID, state, and flags.
	var info *[5]int32
	var size uint32
	if ok, _, _ := procWTSQuerySessionInformationW.Call(0, uintptr(currentSession), sessionInfoEx,
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size))); ok == 0 {
		return false
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	return info[3] != stateActive || info[4] == flagsLocked
}

// sendMouse delivers a mouse message to the overlay at client coordinates,
// mirroring a real pointer action on the dimmed surface.
func sendMouse(hwnd windows.Handle, message uint32, x, y int32) {
	lParam := uintptr(uint32(uint16(x)) | uint32(uint16(y))<<16)
	procSendMessageW.Call(uintptr(hwnd), uintptr(message), 0, lParam)
}

// clientRect converts overlay client coordinates to the virtual-desktop
// rectangle the session reports for them.
func clientRect(native *windowsSession, left, top, right, bottom int32) display.Rect {
	return display.Rect{
		Left:   native.bounds.Left + left,
		Top:    native.bounds.Top + top,
		Right:  native.bounds.Left + right,
		Bottom: native.bounds.Top + bottom,
	}
}

// windowUnderOverlayPoint returns the window Windows' own hit test picks at
// overlay client coordinates. go test runs packages side by side and another
// package's topmost overlay can be over this one, so the overlay is raised
// first and a lost race tried again. A pixel that lets clicks through never
// hits the overlay, however often it is raised, so the retries cannot hide
// that failure.
func windowUnderOverlayPoint(native *windowsSession, x, y int32) uintptr {
	const hwndTopmost = ^uintptr(0)
	point := uintptr(uint32(native.bounds.Left+x)) | uintptr(uint32(native.bounds.Top+y))<<32
	var hit uintptr
	for range 40 {
		procSetWindowPos.Call(uintptr(native.overlay), hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		hit, _, _ = procWindowFromPoint.Call(point)
		if windows.Handle(hit) == native.overlay {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return hit
}

func captureEnabled(native *windowsSession) bool {
	r, _, _ := procIsWindowEnabled.Call(uintptr(native.captureButton))
	return r != 0
}

func editsEnabled(native *windowsSession) bool {
	width, _, _ := procIsWindowEnabled.Call(uintptr(native.widthEdit.Handle()))
	height, _, _ := procIsWindowEnabled.Call(uintptr(native.heightEdit.Handle()))
	return width != 0 && height != 0
}

func TestOverlayDragDrawsAndCapturesSelection(t *testing.T) {
	controller, log := newNativeSessionController(t)
	var captured display.Rect
	restored := false
	events := Events{OnCapture: func(region display.Rect, restore func()) {
		captured = region
		// The main window must still be hidden while the handler owns the
		// restore, so its pixels cannot leak into the capture (SCR-044).
		if len(*log) != 1 {
			t.Errorf("hide/restore log at capture = %v, want [hide]", *log)
		}
		restore()
		restored = true
	}}
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// The fields are usable before anything is drawn, and the session
	// starts in the width field, so the size can be typed (SCR-015).
	if !editsEnabled(native) {
		t.Fatal("dimension fields were disabled without a selection, so no size could be typed")
	}
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.widthEdit.Handle()) {
		t.Fatal("a Free Rectangle session did not start in the width field")
	}
	sendMouse(native.overlay, wmLButtonDown, 60, 40)
	sendMouse(native.overlay, wmMouseMove, 460, 340)
	// Live physical-pixel dimensions track the drag in the toolbar fields
	// (SCR-011) and Capture becomes available (SCR-024). The rectangle
	// includes the pixels under both drag corners.
	if got := native.widthEdit.Text(); got != "401" {
		t.Fatalf("width field mid-drag = %q, want 401", got)
	}
	if got := native.heightEdit.Text(); got != "301" {
		t.Fatalf("height field mid-drag = %q, want 301", got)
	}
	sendMouse(native.overlay, wmLButtonUp, 460, 340)

	want := clientRect(native, 60, 40, 461, 341)
	if native.selection != want {
		t.Fatalf("selection = %v, want %v (SCR-010)", native.selection, want)
	}
	if !captureEnabled(native) {
		t.Fatal("Capture stayed disabled with a selection on screen")
	}
	if !editsEnabled(native) {
		t.Fatal("dimension fields stayed disabled with a selection on screen (SCR-013)")
	}
	// The dirty-band compositing must not resize the overlay: it still
	// covers the whole virtual desktop after several selection frames.
	if got := windowRect(t, native.overlay); got != native.bounds {
		t.Fatalf("overlay window rect after selection frames = %v, want the desktop %v", got, native.bounds)
	}
	// The selection interior is visually clear but painted at alpha 1 so it
	// still hit-tests as solid: alpha-zero pixels of a layered window pass
	// mouse messages through to the windows beneath (SCR-012, SCR-052). The
	// desktop around the selection stays dimmed and the frame is opaque.
	pixels := native.pixelSlice()
	at := func(x, y int32) uint32 { return pixels[int(y)*int(native.pixelWidth)+int(x)] }
	if got := at(200, 150); got != 1<<24 {
		t.Fatalf("interior pixel = %#08x, want black at alpha 1, %#08x (visually clear but input-capturing)", got, uint32(1)<<24)
	}
	if got := at(5, 5); got != dimPixel {
		t.Fatalf("dimmed pixel = %#08x, want %#08x", got, dimPixel)
	}
	if got := at(58, 150); got&0xff000000 != 0xff000000 {
		t.Fatalf("border pixel = %#08x, want an opaque frame", got)
	}
	// Windows' own hit test decides where a real click goes, which the mouse
	// messages this test sends directly to the overlay do not exercise.
	if sessionLockedOrInactive() {
		t.Log("the session is locked or inactive, so the real hit test is skipped")
	} else if hit := windowUnderOverlayPoint(native, 200, 150); windows.Handle(hit) != native.overlay {
		t.Fatalf("a real click inside the selection would reach window %#x, not the overlay %#x (SCR-012, SCR-052)", hit, native.overlay)
	}

	// The toolbar moved near the selection without covering it (SCR-025).
	toolbar := windowRect(t, native.toolbar)
	selectionBottom := 341 + native.bounds.Top
	if toolbar.Top != selectionBottom+native.margin {
		t.Fatalf("toolbar top = %d, want %d just below the selection", toolbar.Top, selectionBottom+native.margin)
	}

	// Drawing hands the focus from the width field to the toolbar, so Enter
	// presses Capture rather than applying the field.
	focus, _, _ := procGetFocus.Call()
	if native.isDimensionEdit(focus) {
		t.Fatal("the focus stayed in a dimension field after a pointer draw")
	}
	enter := windowMessage{HWND: native.toolbar, Message: wmKeyDown, WParam: vkReturn}
	if !controller.FilterMessage(unsafe.Pointer(&enter)) {
		t.Fatal("the filter did not take Enter on the toolbar")
	}
	if captured != want {
		t.Fatalf("captured region = %v, want %v (SCR-014)", captured, want)
	}
	if !restored {
		t.Fatal("main window was not restored")
	}
	if controller.Active() {
		t.Fatal("session is still active after capture")
	}
	if got := *log; len(got) != 2 || got[1] != "restore" {
		t.Fatalf("hide/restore log = %v, want [hide restore]", got)
	}
	if isWindow(native.overlay) || isWindow(native.toolbar) {
		t.Fatal("capture left session windows behind")
	}
}

func TestOverlayClickWithoutDragLeavesNoSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 500, 400)
	sendMouse(native.overlay, wmLButtonUp, 500, 400)
	if !native.selection.Empty() {
		t.Fatalf("plain click created a selection: %v", native.selection)
	}
	if captureEnabled(native) {
		t.Fatal("Capture became enabled without a selection")
	}

	// A stationary mouse move still must not create a 1 × 1 selection.
	sendMouse(native.overlay, wmLButtonDown, 500, 400)
	sendMouse(native.overlay, wmMouseMove, 500, 400)
	sendMouse(native.overlay, wmLButtonUp, 500, 400)
	if !native.selection.Empty() {
		t.Fatalf("stationary move created a selection: %v", native.selection)
	}

	// Clearing an existing selection empties the dimension fields but keeps
	// them usable, so a new size can be typed (SCR-015).
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	sendMouse(native.overlay, wmLButtonDown, 500, 400)
	sendMouse(native.overlay, wmLButtonUp, 500, 400)
	if !native.selection.Empty() {
		t.Fatalf("a click outside the selection kept it: %v", native.selection)
	}
	if !editsEnabled(native) || native.widthEdit.Text() != "" || native.heightEdit.Text() != "" {
		t.Fatalf("after the selection was cleared: fields enabled = %v, width %q, height %q; want enabled and empty",
			editsEnabled(native), native.widthEdit.Text(), native.heightEdit.Text())
	}
}

func TestOverlayMoveAndResizeAdjustSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	drawn := clientRect(native, 100, 100, 301, 241)
	if native.selection != drawn {
		t.Fatalf("selection = %v, want %v", native.selection, drawn)
	}

	// Dragging the interior moves the rectangle without changing its
	// dimensions (SCR-012).
	sendMouse(native.overlay, wmLButtonDown, 200, 170)
	sendMouse(native.overlay, wmMouseMove, 240, 210)
	sendMouse(native.overlay, wmLButtonUp, 240, 210)
	moved := clientRect(native, 140, 140, 341, 281)
	if native.selection != moved {
		t.Fatalf("moved selection = %v, want %v", native.selection, moved)
	}

	// Dragging the south-east handle resizes around the anchored top-left
	// corner.
	sendMouse(native.overlay, wmLButtonDown, 340, 280)
	sendMouse(native.overlay, wmMouseMove, 370, 319)
	sendMouse(native.overlay, wmLButtonUp, 370, 319)
	resized := clientRect(native, 140, 140, 371, 320)
	if native.selection != resized {
		t.Fatalf("resized selection = %v, want %v", native.selection, resized)
	}
	if got := native.widthEdit.Text(); got != "231" {
		t.Fatalf("width field after resize = %q, want 231", got)
	}
}

// TestOverlayFocusedFieldTracksHandleResize pins SCR-011 against a stale
// focused field: keyboard focus stays in a dimension field when the pointer
// drags a handle (the overlay never activates), and the live dimensions
// must still reach that field, or a later focus-loss commit would silently
// apply the stale value over the resize.
func TestOverlayFocusedFieldTracksHandleResize(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)

	procSetFocus.Call(uintptr(native.widthEdit.Handle()))
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.widthEdit.Handle()) {
		t.Skip("the test thread could not take keyboard focus")
	}
	t.Cleanup(func() { procSetFocus.Call(0) })

	// Resize by the south-east handle while the width field holds focus.
	sendMouse(native.overlay, wmLButtonDown, 300, 240)
	sendMouse(native.overlay, wmMouseMove, 400, 240)
	sendMouse(native.overlay, wmLButtonUp, 400, 240)
	if got := native.widthEdit.Text(); got != "301" {
		t.Fatalf("focused width field after handle resize = %q, want the live 301 (SCR-011)", got)
	}
}

// TestOverlayDragEndsAtTheReleasePosition pins the drag completion against
// mouse-move coalescing: the final WM_MOUSEMOVE ahead of a release can be
// dropped, so the button-up message's own coordinates must finish the drag.
// A press that never moved is still a click and clears the selection — its
// release position is not applied, so pointer jitter cannot create a tiny
// selection.
func TestOverlayDragEndsAtTheReleasePosition(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// The draw's release corner wins over the last delivered move.
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 310, 245)
	if want := clientRect(native, 100, 100, 311, 246); native.selection != want {
		t.Fatalf("drawn selection = %v, want %v ending at the release position", native.selection, want)
	}

	// A move drag ends where the button was released, not where the last
	// move was delivered.
	sendMouse(native.overlay, wmLButtonDown, 150, 150)
	sendMouse(native.overlay, wmMouseMove, 160, 160)
	sendMouse(native.overlay, wmLButtonUp, 190, 200)
	if want := clientRect(native, 140, 150, 351, 296); native.selection != want {
		t.Fatalf("moved selection = %v, want %v ending at the release position", native.selection, want)
	}

	// A handle resize ends at the release position too.
	sel := native.selection
	sendMouse(native.overlay, wmLButtonDown, sel.Right-1-native.bounds.Left, sel.Bottom-1-native.bounds.Top)
	sendMouse(native.overlay, wmMouseMove, 400, 340)
	sendMouse(native.overlay, wmLButtonUp, 420, 360)
	if want := clientRect(native, 140, 150, 421, 361); native.selection != want {
		t.Fatalf("resized selection = %v, want %v ending at the release position", native.selection, want)
	}

	// A press that never moved clears the selection even when the release
	// lands a pixel or two away from the press: plain-click jitter must not
	// create a tiny selection.
	sendMouse(native.overlay, wmLButtonDown, 500, 400)
	sendMouse(native.overlay, wmLButtonUp, 502, 401)
	if !native.selection.Empty() {
		t.Fatalf("click jitter created a selection: %v", native.selection)
	}
}

// TestOverlayClickOnHandleLeavesSelectionUnchanged pins the never-moved-press
// guard on handle and interior drags: a press on a resize handle released
// without any pointer motion must leave the selection as it was. The press
// point sits inside the handle's grab square, off the selection's edge, so
// applying it as the drag end would silently resize the capture region on a
// plain click — the same jitter class the draw guard rejects.
func TestOverlayClickOnHandleLeavesSelectionUnchanged(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	drawn := native.selection

	// Press and release inside the south-east handle's grab square without
	// moving: the click must not resize the selection to the press point.
	x := drawn.Right - native.bounds.Left - native.grab/2 + 2
	y := drawn.Bottom - native.bounds.Top - native.grab/2 + 2
	sendMouse(native.overlay, wmLButtonDown, x, y)
	sendMouse(native.overlay, wmLButtonUp, x, y)
	if native.selection != drawn {
		t.Fatalf("plain click on a handle resized the selection to %v, want unchanged %v", native.selection, drawn)
	}

	// A stationary mouse move during the press must not resize either.
	sendMouse(native.overlay, wmLButtonDown, x, y)
	sendMouse(native.overlay, wmMouseMove, x, y)
	sendMouse(native.overlay, wmLButtonUp, x, y)
	if native.selection != drawn {
		t.Fatalf("stationary move on a handle resized the selection to %v, want unchanged %v", native.selection, drawn)
	}

	// A press whose pointer motion was coalesced away entirely still ends at
	// the release position, exactly like a drag with delivered moves.
	sendMouse(native.overlay, wmLButtonDown, x, y)
	sendMouse(native.overlay, wmLButtonUp, x+30, y+20)
	want := clientRect(native, 100, 100, x+31, y+21)
	if native.selection != want {
		t.Fatalf("coalesced-move resize = %v, want %v ending at the release position", native.selection, want)
	}
}

// TestOverlayClickOnInteriorLeavesSelectionUnchanged pins the same
// never-moved-press guard on interior (move) drags: a plain click inside the
// selection must not shift the capture region by the release jitter, and a
// stationary spurious move during the press must not shift it either. A press
// whose pointer motion was coalesced away entirely still moves the selection
// to the release position, exactly like a move drag with delivered moves.
func TestOverlayClickOnInteriorLeavesSelectionUnchanged(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	drawn := native.selection

	// Press and release inside the selection without moving: the click must
	// not move the selection.
	sendMouse(native.overlay, wmLButtonDown, 200, 170)
	sendMouse(native.overlay, wmLButtonUp, 200, 170)
	if native.selection != drawn {
		t.Fatalf("plain interior click moved the selection to %v, want unchanged %v", native.selection, drawn)
	}

	// A stationary mouse move during the press must not move it either.
	sendMouse(native.overlay, wmLButtonDown, 200, 170)
	sendMouse(native.overlay, wmMouseMove, 200, 170)
	sendMouse(native.overlay, wmLButtonUp, 200, 170)
	if native.selection != drawn {
		t.Fatalf("stationary move on the interior moved the selection to %v, want unchanged %v", native.selection, drawn)
	}

	// A press whose pointer motion was coalesced away entirely still moves
	// the selection by the press-to-release delta.
	sendMouse(native.overlay, wmLButtonDown, 200, 170)
	sendMouse(native.overlay, wmLButtonUp, 240, 210)
	want := clientRect(native, 140, 140, 341, 281)
	if native.selection != want {
		t.Fatalf("coalesced-move drag = %v, want %v moved by the release delta", native.selection, want)
	}
}

func TestOverlayDimensionEditingAppliesExactSize(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	var captured display.Rect
	events := Events{OnCapture: func(region display.Rect, restore func()) {
		captured = region
		restore()
	}}
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)

	// AC-S05: dimensions typed before capture become the captured region.
	native.widthEdit.SetText("640")
	native.heightEdit.SetText("480")
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, uintptr(enKillFocus<<16|toolbarWidthCommand), 0)
	want := clientRect(native, 100, 100, 740, 580)
	if native.selection != want {
		t.Fatalf("edited selection = %v, want %v", native.selection, want)
	}

	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, toolbarCaptureCommand, 0)
	if captured != want {
		t.Fatalf("captured region = %v, want the edited %v", captured, want)
	}
}

func TestOverlayDimensionEditingRejectsInvalidValues(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	before := native.selection

	native.widthEdit.SetText("many")
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, uintptr(enKillFocus<<16|toolbarWidthCommand), 0)
	if native.selection != before {
		t.Fatalf("invalid width changed the selection to %v", native.selection)
	}
	if got := native.message.Text(); !strings.Contains(got, "Validation error") {
		t.Fatalf("validation message = %q", got)
	}
	if got := native.widthEdit.Text(); got != "201" {
		t.Fatalf("width field after rejection = %q, want the actual 201", got)
	}

	// Dimensions that would reach past the desktop edge are rejected too,
	// rather than silently clamped into a capture of the wrong size.
	native.widthEdit.SetText("999999999")
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, uintptr(enKillFocus<<16|toolbarWidthCommand), 0)
	if native.selection != before {
		t.Fatalf("oversized width changed the selection to %v", native.selection)
	}
	if got := native.message.Text(); !strings.Contains(got, "do not fit") {
		t.Fatalf("fit message = %q", got)
	}

	// A value beyond int32 must be rejected too, not wrapped into a
	// fitting or empty rectangle (SCR-013).
	native.widthEdit.SetText("4294967396")
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, uintptr(enKillFocus<<16|toolbarWidthCommand), 0)
	if native.selection != before {
		t.Fatalf("wrapping width changed the selection to %v", native.selection)
	}
	if got := native.message.Text(); !strings.Contains(got, "do not fit") {
		t.Fatalf("fit message for wrapping width = %q", got)
	}
}

func TestOverlayEnterCommitsDimensionField(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)

	native.widthEdit.SetText("500")
	enter := windowMessage{HWND: native.widthEdit.Handle(), Message: wmKeyDown, WParam: vkReturn}
	if !controller.FilterMessage(unsafe.Pointer(&enter)) {
		t.Fatal("filter did not commit the dimension field on Enter")
	}
	if got := native.selection.Width(); got != 500 {
		t.Fatalf("selection width after Enter = %d, want 500", got)
	}
}

func TestOverlayEscapeCancelsWithoutOutput(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	captured := 0
	cancelled := 0
	events := Events{
		OnCapture: func(display.Rect, func()) { captured++ },
		OnCancel:  func() { cancelled++ },
	}
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// AC-S07: cancelling an active selection produces no file and no
	// clipboard change, so the capture callback must never run.
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	escape := windowMessage{HWND: native.overlay, Message: wmKeyDown, WParam: vkEscape}
	if !controller.FilterMessage(unsafe.Pointer(&escape)) {
		t.Fatal("filter did not handle Escape on the overlay")
	}
	if controller.Active() {
		t.Fatal("session is still active after Escape")
	}
	if captured != 0 || cancelled != 1 {
		t.Fatalf("capture/cancel callbacks = %d/%d, want 0/1", captured, cancelled)
	}
}

// TestOverlayDimensionCommitDroppedMidDrag pins the drag guard on
// commitDimensions: a dimension field keeps keyboard focus across an overlay
// click (the overlay never activates), so Enter can arrive while a handle
// drag is in progress. Committing then would fight the live drag and jump
// the toolbar mid-drag; the commit must be dropped.
func TestOverlayDimensionCommitDroppedMidDrag(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)

	procSetFocus.Call(uintptr(native.widthEdit.Handle()))
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.widthEdit.Handle()) {
		t.Skip("the test thread could not take keyboard focus")
	}
	t.Cleanup(func() { procSetFocus.Call(0) })

	// Start a south-east handle drag; the focused field tracks the live
	// width as the drag proceeds.
	sendMouse(native.overlay, wmLButtonDown, 300, 240)
	sendMouse(native.overlay, wmMouseMove, 350, 240)
	if got := native.widthEdit.Text(); got != "251" {
		t.Fatalf("width field mid-drag = %q, want the live 251", got)
	}
	native.widthEdit.SetText("500")
	enter := windowMessage{HWND: native.widthEdit.Handle(), Message: wmKeyDown, WParam: vkReturn}
	if !controller.FilterMessage(unsafe.Pointer(&enter)) {
		t.Fatal("filter did not swallow Enter in the dimension field")
	}
	if got := native.selection.Width(); got != 251 {
		t.Fatalf("selection width after mid-drag Enter = %d, want the drag's 251, not a committed 500", got)
	}
	sendMouse(native.overlay, wmLButtonUp, 350, 240)
}

// TestOverlayClearingSelectionMovesFocusOffCapture mirrors the dimension
// field focus handling for the Capture button: a plain click on the dimmed
// surface clears the selection and disables Capture, and the overlay never
// takes activation, so the button can hold focus at that moment. Focus must
// move to the toolbar first so Escape keeps cancelling the session.
func TestOverlayClearingSelectionMovesFocusOffCapture(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	if !captureEnabled(native) {
		t.Fatal("Capture stayed disabled with a selection on screen")
	}

	procSetFocus.Call(uintptr(native.captureButton))
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.captureButton) {
		t.Skip("the test thread could not take keyboard focus")
	}
	t.Cleanup(func() { procSetFocus.Call(0) })

	// A plain click on the dimmed area clears the selection and so disables
	// the focused Capture button.
	sendMouse(native.overlay, wmLButtonDown, 600, 450)
	sendMouse(native.overlay, wmLButtonUp, 600, 450)
	if captureEnabled(native) {
		t.Fatal("Capture stayed enabled after the selection was cleared")
	}
	if focus, _, _ := procGetFocus.Call(); focus == uintptr(native.captureButton) {
		t.Fatal("focus stayed on the disabled Capture button")
	}
}

// TestOverlayDrawCompletionPaintsHandles pins SCR-012's visible resize
// affordance: the handles are skipped while the draw is in progress, so the
// button release must repaint them — otherwise the resting selection shows
// no handles until the next interaction starts. Losing the pointer capture
// mid-draw without a button release must finish the draw the same way.
func TestOverlayDrawCompletionPaintsHandles(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)
	at := func(x, y int32) uint32 {
		return native.pixelSlice()[int(y)*int(native.pixelWidth)+int(x)]
	}

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	if got := at(100, 100); got != selectionPixel {
		t.Fatalf("corner pixel mid-draw = %#08x, want the bare interior while the handles are skipped", got)
	}
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	if got := at(100, 100); got != 0xffffffff {
		t.Fatalf("corner pixel after the draw = %#08x, want the painted handle (SCR-012)", got)
	}

	// The same repaint must happen when the capture is lost mid-draw
	// without a button release.
	sendMouse(native.overlay, wmLButtonDown, 500, 400)
	sendMouse(native.overlay, wmMouseMove, 700, 540)
	procSendMessageW.Call(uintptr(native.overlay), wmCaptureChanged, 0, 0)
	if native.drag.op != dragNone {
		t.Fatal("lost capture left the drag running")
	}
	if got := at(500, 400); got != 0xffffffff {
		t.Fatalf("corner pixel after lost capture = %#08x, want the painted handle", got)
	}
}

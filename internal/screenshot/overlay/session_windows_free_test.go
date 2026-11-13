//go:build windows

package overlay

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

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
	if err := controller.Begin(42, 0, ModeFreeRectangle, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	if editsEnabled(native) {
		t.Fatal("dimension fields were editable without a selection")
	}
	sendMouse(native.overlay, wmLButtonDown, 60, 40)
	sendMouse(native.overlay, wmMouseMove, 460, 340)
	// Live physical-pixel dimensions track the drag in the toolbar fields
	// (SCR-011) and Capture becomes available (SCR-024).
	if got := native.widthEdit.Text(); got != "400" {
		t.Fatalf("width field mid-drag = %q, want 400", got)
	}
	if got := native.heightEdit.Text(); got != "300" {
		t.Fatalf("height field mid-drag = %q, want 300", got)
	}
	sendMouse(native.overlay, wmLButtonUp, 460, 340)

	want := clientRect(native, 60, 40, 460, 340)
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
	if got := at(200, 150); got != selectionPixel {
		t.Fatalf("interior pixel = %#08x, want %#08x (visually clear but input-capturing)", got, selectionPixel)
	}
	if got := at(5, 5); got != dimPixel {
		t.Fatalf("dimmed pixel = %#08x, want %#08x", got, dimPixel)
	}
	if got := at(58, 150); got&0xff000000 != 0xff000000 {
		t.Fatalf("border pixel = %#08x, want an opaque frame", got)
	}

	// The toolbar moved near the selection without covering it (SCR-025).
	toolbar := windowRect(t, native.toolbar)
	selectionBottom := 340 + native.bounds.Top
	if toolbar.Top != selectionBottom+native.margin {
		t.Fatalf("toolbar top = %d, want %d just below the selection", toolbar.Top, selectionBottom+native.margin)
	}

	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, toolbarCaptureCommand, 0)
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
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

	// Clearing an existing selection disables the dimension fields again.
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	if !editsEnabled(native) {
		t.Fatal("dimension fields stayed disabled with a selection on screen")
	}
	sendMouse(native.overlay, wmLButtonDown, 500, 400)
	sendMouse(native.overlay, wmLButtonUp, 500, 400)
	if editsEnabled(native) {
		t.Fatal("dimension fields stayed editable after the selection was cleared")
	}
}

func TestOverlayMoveAndResizeAdjustSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	drawn := clientRect(native, 100, 100, 300, 240)
	if native.selection != drawn {
		t.Fatalf("selection = %v, want %v", native.selection, drawn)
	}

	// Dragging the interior moves the rectangle without changing its
	// dimensions (SCR-012).
	sendMouse(native.overlay, wmLButtonDown, 200, 170)
	sendMouse(native.overlay, wmMouseMove, 240, 210)
	sendMouse(native.overlay, wmLButtonUp, 240, 210)
	moved := clientRect(native, 140, 140, 340, 280)
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
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

func TestOverlayDimensionEditingAppliesExactSize(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	var captured display.Rect
	events := Events{OnCapture: func(region display.Rect, restore func()) {
		captured = region
		restore()
	}}
	if err := controller.Begin(42, 0, ModeFreeRectangle, events); err != nil {
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
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
	if got := native.widthEdit.Text(); got != "200" {
		t.Fatalf("width field after rejection = %q, want the actual 200", got)
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
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
	if err := controller.Begin(42, 0, ModeFreeRectangle, events); err != nil {
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

func TestPresetRectangleSessionDoesNotDrawYet(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	if got := native.message.Text(); got != ui.OverlayPresetPending {
		t.Fatalf("preset instructions = %q", got)
	}
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	if !native.selection.Empty() {
		t.Fatalf("preset session drew a selection: %v", native.selection)
	}
	if captureEnabled(native) {
		t.Fatal("Capture became enabled in a preset session")
	}
	if editsEnabled(native) {
		t.Fatal("dimension fields became editable in a preset session")
	}
}

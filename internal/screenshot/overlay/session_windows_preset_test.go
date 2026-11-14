//go:build windows

package overlay

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// presetFrameRect returns the window rectangle the preset frame should
// cover: the selection inflated outward by the border ring.
func presetFrameRect(native *windowsSession) display.Rect {
	return inflateRect(native.selection, native.border)
}

func TestPresetFrameExactSizeAndSurface(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// SCR-020: the frame starts with the preset's exact physical-pixel
	// dimensions, fully inside the virtual desktop.
	if got := native.selection.Width(); got != 320 {
		t.Fatalf("selection width = %d, want 320", got)
	}
	if got := native.selection.Height(); got != 240 {
		t.Fatalf("selection height = %d, want 240", got)
	}
	desktop := native.bounds
	if native.selection.Left < desktop.Left || native.selection.Top < desktop.Top ||
		native.selection.Right > desktop.Right || native.selection.Bottom > desktop.Bottom {
		t.Fatalf("selection %v escapes the virtual desktop %v", native.selection, desktop)
	}

	// The frame window covers exactly the selection plus its border ring,
	// topmost and never activating (SCR-021).
	if got := windowRect(t, native.overlay); got != presetFrameRect(native) {
		t.Fatalf("frame window rect = %v, want the selection inflated by the border %v", got, presetFrameRect(native))
	}
	exStyle, _, _ := procGetWindowLongW.Call(uintptr(native.overlay), gwlExStyle)
	for _, bit := range []uintptr{wsExLayeredBit, wsExTopmostBit, wsExToolWindowBit, wsExNoActivateBit} {
		if exStyle&bit == 0 {
			t.Fatalf("frame ex-style %#x lacks %#x", exStyle, bit)
		}
	}

	// The interior is alpha zero so it is visually unobscured and passes
	// mouse input through to the windows beneath (SCR-022); the border ring
	// is opaque so it receives the move drag (SCR-023).
	pixels := native.pixelSlice()
	at := func(x, y int32) uint32 { return pixels[int(y)*int(native.pixelWidth)+int(x)] }
	if got := at(native.border+10, native.border+10); got != 0 {
		t.Fatalf("interior pixel = %#08x, want fully transparent for click-through", got)
	}
	if got := at(0, native.pixelHeight/2); got != 0xff000000 {
		t.Fatalf("outer border pixel = %#08x, want the opaque dark outline", got)
	}
	if got := at(native.border/2, native.pixelHeight/2); native.border > 2 && got != native.borderPx {
		t.Fatalf("border ring pixel = %#08x, want the opaque highlight %#08x", got, native.borderPx)
	}

	// The toolbar offers Capture and Cancel with live dimensions and the
	// virtual-desktop X/Y readout (SCR-024, SCR-028); Capture starts
	// enabled because the exact frame exists from the start.
	if !captureEnabled(native) {
		t.Fatal("Capture started disabled with the preset frame on screen")
	}
	if !editsEnabled(native) {
		t.Fatal("dimension fields started disabled with the preset frame on screen")
	}
	if got := native.widthEdit.Text(); got != "320" {
		t.Fatalf("width field = %q, want 320", got)
	}
	if got := native.heightEdit.Text(); got != "240" {
		t.Fatalf("height field = %q, want 240", got)
	}
	wantPosition := fmt.Sprintf(ui.SelectionPositionFormat, native.selection.Left, native.selection.Top)
	if got := native.position.Text(); got != wantPosition {
		t.Fatalf("position readout = %q, want %q", got, wantPosition)
	}
	if got := native.message.Text(); got != ui.OverlayPresetInstructions {
		t.Fatalf("preset instructions = %q", got)
	}
	toolbar := windowRect(t, native.toolbar)
	if toolbar.Left < desktop.Left || toolbar.Top < desktop.Top || toolbar.Right > desktop.Right || toolbar.Bottom > desktop.Bottom {
		t.Fatalf("toolbar rect %v escapes the virtual desktop %v", toolbar, desktop)
	}
}

func TestPresetFrameBorderDragMovesWithoutResizing(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	origin := native.selection
	// A press on the border ring — here the top edge's midpoint, in frame
	// client coordinates — drags the frame (SCR-023). AC-S04: the move is
	// exact and the dimensions never change. The window tracks the pointer's
	// virtual-desktop delta, so after the move shifted it by 40 × 30, the
	// release's client coordinates equal the press coordinates again.
	pressX := origin.Left + origin.Width()/2 - native.frameOrigin.X
	pressY := origin.Top - native.frameOrigin.Y
	sendMouse(native.overlay, wmLButtonDown, pressX, pressY)
	sendMouse(native.overlay, wmMouseMove, pressX+40, pressY+30)
	sendMouse(native.overlay, wmLButtonUp, pressX, pressY)

	want := display.Rect{Left: origin.Left + 40, Top: origin.Top + 30, Right: origin.Right + 40, Bottom: origin.Bottom + 30}
	if native.selection != want {
		t.Fatalf("moved selection = %v, want %v moved by exactly 40 × 30", native.selection, want)
	}
	if got := windowRect(t, native.overlay); got != presetFrameRect(native) {
		t.Fatalf("frame window rect after the move = %v, want %v", got, presetFrameRect(native))
	}
	wantPosition := fmt.Sprintf(ui.SelectionPositionFormat, want.Left, want.Top)
	if got := native.position.Text(); got != wantPosition {
		t.Fatalf("position readout after the move = %q, want %q", got, wantPosition)
	}
	// The toolbar followed the moved selection without covering it (SCR-025).
	toolbar := windowRect(t, native.toolbar)
	if toolbar.Top != want.Bottom+native.margin {
		t.Fatalf("toolbar top = %d, want %d just below the moved selection", toolbar.Top, want.Bottom+native.margin)
	}
}

// TestPresetFrameClickOnBorderLeavesSelectionUnchanged pins the
// never-moved-press guard on preset border drags: a plain click on the
// border must not shift the frame, while a press whose pointer motion was
// coalesced away still ends at the release position like any drag.
func TestPresetFrameClickOnBorderLeavesSelectionUnchanged(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	before := native.selection
	x := native.pixelWidth / 2
	y := int32(0)
	sendMouse(native.overlay, wmLButtonDown, x, y)
	sendMouse(native.overlay, wmLButtonUp, x, y)
	if native.selection != before {
		t.Fatalf("plain border click moved the selection to %v, want unchanged %v", native.selection, before)
	}

	sendMouse(native.overlay, wmLButtonDown, x, y)
	sendMouse(native.overlay, wmMouseMove, x, y)
	sendMouse(native.overlay, wmLButtonUp, x, y)
	if native.selection != before {
		t.Fatalf("stationary move on the border moved the selection to %v, want unchanged %v", native.selection, before)
	}

	sendMouse(native.overlay, wmLButtonDown, x, y)
	sendMouse(native.overlay, wmLButtonUp, x+25, y+15)
	want := display.Rect{Left: before.Left + 25, Top: before.Top + 15, Right: before.Right + 25, Bottom: before.Bottom + 15}
	if native.selection != want {
		t.Fatalf("coalesced-move drag = %v, want %v moved by the release delta", native.selection, want)
	}
}

func TestPresetFrameKeyboardNudges(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	origin := native.selection
	// SCR-027: each arrow key moves the frame by exactly one physical pixel.
	right := windowMessage{HWND: native.overlay, Message: wmKeyDown, WParam: vkRight}
	if !controller.FilterMessage(unsafe.Pointer(&right)) {
		t.Fatal("filter did not handle the right arrow on the frame")
	}
	if got := native.selection; got.Left != origin.Left+1 || got.Top != origin.Top {
		t.Fatalf("selection after Right = %v, want one pixel right of %v", got, origin)
	}
	down := windowMessage{HWND: native.overlay, Message: wmKeyDown, WParam: vkDown}
	if !controller.FilterMessage(unsafe.Pointer(&down)) {
		t.Fatal("filter did not handle the down arrow on the frame")
	}
	if got := native.selection; got.Left != origin.Left+1 || got.Top != origin.Top+1 {
		t.Fatalf("selection after Down = %v, want one pixel down", got)
	}
	// The Shift variant's step is exercised directly: ten physical pixels.
	native.nudge(-1, 0, 10)
	if got := native.selection; got.Left != origin.Left-9 || got.Top != origin.Top+1 {
		t.Fatalf("selection after a ten-pixel nudge = %v, want ten pixels left", got)
	}
	if got := native.selection.Width(); got != 320 {
		t.Fatalf("nudges changed the selection width to %d", got)
	}
	if got := windowRect(t, native.overlay); got != presetFrameRect(native) {
		t.Fatalf("frame window rect after nudges = %v, want %v", got, presetFrameRect(native))
	}

	// Nudges clamp at the virtual-desktop edge instead of drifting out.
	for range native.bounds.Width() {
		native.nudge(-1, 0, 1)
	}
	if got := native.selection.Left; got != native.bounds.Left {
		t.Fatalf("selection left after clamping = %d, want the desktop edge %d", got, native.bounds.Left)
	}

	// Arrow keys inside a dimension field keep editing the text: the filter
	// leaves them to the control.
	caret := windowMessage{HWND: native.widthEdit.Handle(), Message: wmKeyDown, WParam: vkLeft}
	before := native.selection
	controller.FilterMessage(unsafe.Pointer(&caret))
	if native.selection != before {
		t.Fatalf("arrow key inside a dimension field moved the selection to %v", native.selection)
	}
}

func TestPresetFrameDimensionEditResizesExactly(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	var captured display.Rect
	events := Events{OnCapture: func(region display.Rect, restore func()) {
		captured = region
		restore()
	}}
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// SCR-029: typed dimensions replace the active rectangle exactly,
	// anchored at its top-left corner.
	topLeft := display.Point{X: native.selection.Left, Y: native.selection.Top}
	native.widthEdit.SetText("640")
	native.heightEdit.SetText("480")
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, uintptr(enKillFocus<<16|toolbarWidthCommand), 0)
	want := display.Rect{Left: topLeft.X, Top: topLeft.Y, Right: topLeft.X + 640, Bottom: topLeft.Y + 480}
	if native.selection != want {
		t.Fatalf("edited selection = %v, want %v", native.selection, want)
	}
	if got := windowRect(t, native.overlay); got != presetFrameRect(native) {
		t.Fatalf("frame window rect after the resize = %v, want %v", got, presetFrameRect(native))
	}
	if got := native.pixelSlice()[int(native.border+5)*int(native.pixelWidth)+int(native.border+5)]; got != 0 {
		t.Fatalf("interior pixel after the resize = %#08x, want transparent", got)
	}

	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, toolbarCaptureCommand, 0)
	if captured != want {
		t.Fatalf("captured region = %v, want the edited %v (SCR-029)", captured, want)
	}
	if controller.Active() {
		t.Fatal("session is still active after capture")
	}
}

func TestPresetFrameRejectsInvalidDimensions(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	before := native.selection
	native.widthEdit.SetText("many")
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, uintptr(enKillFocus<<16|toolbarWidthCommand), 0)
	if native.selection != before {
		t.Fatalf("invalid width changed the selection to %v", native.selection)
	}
	if got := native.message.Text(); !strings.Contains(got, "Validation error") {
		t.Fatalf("validation message = %q", got)
	}
	if got := native.widthEdit.Text(); got != "320" {
		t.Fatalf("width field after rejection = %q, want the actual 320", got)
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
}

func TestPresetFrameCaptureDeliversExactRect(t *testing.T) {
	controller, log := newNativeSessionController(t)
	var captured display.Rect
	restored := false
	events := Events{OnCapture: func(region display.Rect, restore func()) {
		captured = region
		restore()
		restored = true
	}}
	// AC-S01 geometry asks for a 1280 × 720 preset; CI desktops can be
	// smaller, so the frame shrinks to fit rather than testing nothing.
	desktop, err := display.NewMonitor().VirtualDesktopBounds()
	if err != nil {
		t.Fatal(err)
	}
	size := display.Point{X: min(1280, desktop.Width()), Y: min(720, desktop.Height())}
	if err := controller.Begin(42, 0, ModePresetRectangle, size, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// Capture delivers exactly the preset rectangle, and the session windows
	// are destroyed before the pixels are read so no frame or toolbar visual
	// can reach the output (SCR-068).
	selection := native.selection
	procSendMessageW.Call(uintptr(native.toolbar), wmCommand, toolbarCaptureCommand, 0)
	if captured != selection {
		t.Fatalf("captured region = %v, want the exact preset frame %v", captured, selection)
	}
	if captured.Width() != size.X || captured.Height() != size.Y {
		t.Fatalf("captured dimensions = %d × %d, want the preset %d × %d", captured.Width(), captured.Height(), size.X, size.Y)
	}
	if !restored {
		t.Fatal("main window was not restored")
	}
	if isWindow(native.overlay) || isWindow(native.toolbar) {
		t.Fatal("capture left session windows behind")
	}
	if got := *log; len(got) != 2 || got[1] != "restore" {
		t.Fatalf("hide/restore log = %v, want [hide restore]", got)
	}
}

func TestPresetFrameEscapeCancelsWithoutOutput(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	captured := 0
	cancelled := 0
	events := Events{
		OnCapture: func(display.Rect, func()) { captured++ },
		OnCancel:  func() { cancelled++ },
	}
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, events); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	// AC-S07: cancelling produces no file and no clipboard change, so the
	// capture callback must never run.
	escape := windowMessage{HWND: native.overlay, Message: wmKeyDown, WParam: vkEscape}
	if !controller.FilterMessage(unsafe.Pointer(&escape)) {
		t.Fatal("filter did not handle Escape on the frame")
	}
	if controller.Active() {
		t.Fatal("session is still active after Escape")
	}
	if captured != 0 || cancelled != 1 {
		t.Fatalf("capture/cancel callbacks = %d/%d, want 0/1", captured, cancelled)
	}
	if isWindow(native.overlay) || isWindow(native.toolbar) {
		t.Fatal("Escape left session windows behind")
	}
}

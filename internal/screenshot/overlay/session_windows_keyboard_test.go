//go:build windows

package overlay

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

var (
	procCreateFontW = gdi32.NewProc("CreateFontW")
	procGetDC       = user32.NewProc("GetDC")
	procReleaseDC   = user32.NewProc("ReleaseDC")
	procDrawTextW   = user32.NewProc("DrawTextW")

	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
)

const (
	dtWordBreak = 0x0010
	dtCalcRect  = 0x0400
	dtNoPrefix  = 0x0800

	pmRemove = 0x0001
)

// pressEnter hands Enter in the field to the session's message filter, as
// the shell's message loop does.
func pressEnter(t *testing.T, controller *Controller, native *windowsSession, field *ui.Control) {
	t.Helper()
	enter := windowMessage{HWND: field.Handle(), Message: wmKeyDown, WParam: vkReturn}
	if !controller.FilterMessage(unsafe.Pointer(&enter)) {
		t.Fatal("the filter did not take Enter in a dimension field")
	}
}

// TestFreeRectangleTypedSizeCreatesSelection pins the keyboard path through
// Free Rectangle (SCR-015): with nothing drawn, typing a width and height
// and pressing Enter creates a selection of exactly that size, centered on
// the cursor display, and hands the focus to Capture; moving from one field
// to the other is no request,
// with the second field empty or filled; Enter with a field empty, or a
// size larger than the desktop, explains the problem and keeps the typed
// text.
func TestFreeRectangleTypedSizeCreatesSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	selections := 0
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)
	report := native.onSelection
	native.onSelection = func(selection display.Rect) {
		selections++
		report(selection)
	}

	native.widthEdit.SetText("640")
	native.heightEdit.Focus()
	if !native.selection.Empty() || selections != 0 {
		t.Fatalf("moving from the width field to the empty height field created %v", native.selection)
	}
	if got := native.message.Text(); got != ui.OverlayInstructions {
		t.Fatalf("leaving the width field with the height empty showed %q", got)
	}

	pressEnter(t, controller, native, native.heightEdit)
	if got := native.message.Text(); !strings.Contains(got, "Height is required") {
		t.Fatalf("Enter with the height empty showed %q, want the missing height explained", got)
	}
	if got := native.widthEdit.Text(); got != "640" {
		t.Fatalf("a refused size cleared the typed width to %q", got)
	}

	native.heightEdit.SetText("999999999")
	pressEnter(t, controller, native, native.heightEdit)
	if got := native.message.Text(); got != ui.SelectionTooLarge {
		t.Fatalf("a size larger than the desktop showed %q, want %q", got, ui.SelectionTooLarge)
	}
	if !native.selection.Empty() || captureEnabled(native) {
		t.Fatal("a size larger than the desktop created a selection")
	}

	// Going back to the width with both fields filled is still typing the
	// pair, so the size Enter then applies is centered, not resized in place.
	native.heightEdit.SetText("480")
	native.widthEdit.Focus()
	if !native.selection.Empty() || selections != 0 {
		t.Fatalf("moving from the filled height field back to the width created %v", native.selection)
	}
	pressEnter(t, controller, native, native.widthEdit)
	want, ok := typedSelection(native.anchor, native.bounds, 640, 480)
	if !ok {
		t.Fatal("640 × 480 does not fit the test desktop")
	}
	if native.selection != want || want.Width() != 640 || want.Height() != 480 {
		t.Fatalf("typed selection = %v, want %v", native.selection, want)
	}
	if selections != 1 || !captureEnabled(native) {
		t.Fatalf("after the typed selection: %d reports, Capture enabled %v; want 1 and enabled", selections, captureEnabled(native))
	}
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.captureButton) {
		t.Fatalf("Enter that created the selection left the focus on %#x, want Capture (%#x)", focus, uintptr(native.captureButton))
	}
	if got := native.message.Text(); got != ui.OverlayInstructions {
		t.Fatalf("an accepted size left %q in the toolbar, want the instructions", got)
	}
}

// TestFreeRectangleLeavingTypedSizeCreatesSelection pins that leaving the
// pair of fields for another toolbar control commits a typed size as Enter
// does (SCR-013, SCR-015), and does nothing while a field is still empty.
func TestFreeRectangleLeavingTypedSizeCreatesSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	native.widthEdit.SetText("300")
	native.cancel.Focus()
	if !native.selection.Empty() {
		t.Fatalf("leaving the fields with the height empty created %v", native.selection)
	}
	native.heightEdit.Focus()
	native.heightEdit.SetText("200")
	native.cancel.Focus()
	want, ok := typedSelection(native.anchor, native.bounds, 300, 200)
	if !ok {
		t.Fatal("300 × 200 does not fit the test desktop")
	}
	if native.selection != want {
		t.Fatalf("selection after leaving the filled fields = %v, want %v", native.selection, want)
	}
}

// TestFreeRectangleSwitchingAwayCommitsNoTypedSize pins that switching to
// another application while typing the pair is no request (SCR-015): the
// focus leaves the toolbar's thread rather than the pair, and the toolbar
// gives it back to the field on return, so the size finished there is
// centered as typed, not resized in place from a half-typed one created as
// the focus left.
func TestFreeRectangleSwitchingAwayCommitsNoTypedSize(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)
	toolbar := uintptr(native.toolbar)

	native.widthEdit.SetText("640")
	native.heightEdit.Focus()
	native.heightEdit.SetText("4")
	// Another application's activation deactivates the toolbar and takes
	// the focus from its thread.
	procSendMessageW.Call(toolbar, wmActivate, waInactive, 0)
	procSetFocus.Call(0)
	if !native.selection.Empty() {
		t.Fatalf("switching away with the pair half typed created %v", native.selection)
	}
	procSendMessageW.Call(toolbar, wmActivate, waActive, 0)
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.heightEdit.Handle()) {
		t.Fatalf("returning to the toolbar focused %#x, want the height field", focus)
	}
	native.heightEdit.SetText("480")
	pressEnter(t, controller, native, native.heightEdit)
	want, ok := typedSelection(native.anchor, native.bounds, 640, 480)
	if !ok {
		t.Fatal("640 × 480 does not fit the test desktop")
	}
	if native.selection != want {
		t.Fatalf("selection after finishing the pair on return = %v, want %v, centered", native.selection, want)
	}
}

// TestFreeRectangleTabFromFilledPairReachesCapture pins that Tab out of the
// filled pair of dimension fields lands on Capture (SCR-015): the typed
// selection is created before the dialog manager picks the next control, so
// Tab does not pass the still-disabled Capture for Cancel, where Enter would
// cancel the session, and the field's focus loss then reports it no second
// time. A refused size leaves Capture disabled and is announced once.
func TestFreeRectangleTabFromFilledPairReachesCapture(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	selections := 0
	report := native.onSelection
	native.onSelection = func(selection display.Rect) {
		selections++
		report(selection)
	}
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)
	pressTab := func() {
		t.Helper()
		native.heightEdit.Focus()
		tab := windowMessage{HWND: native.heightEdit.Handle(), Message: wmKeyDown, WParam: vkTab}
		if !controller.FilterMessage(unsafe.Pointer(&tab)) {
			t.Fatal("the filter did not give the toolbar Tab")
		}
	}

	native.widthEdit.SetText("640")
	native.heightEdit.SetText("48x")
	pressTab()
	if !native.selection.Empty() || captureEnabled(native) {
		t.Fatalf("Tab out of a refused size created %v", native.selection)
	}
	if got := native.message.Text(); !strings.Contains(got, ui.DimensionHeightName) {
		t.Fatalf("Tab out of a refused height showed %q, want the height explained", got)
	}
	if got := watch.Count(native.message.Handle()); got != 1 {
		t.Fatalf("the refusal was announced %d time(s), want once", got)
	}

	native.heightEdit.SetText("480")
	pressTab()
	want, ok := typedSelection(native.anchor, native.bounds, 640, 480)
	if !ok {
		t.Fatal("640 × 480 does not fit the test desktop")
	}
	if native.selection != want {
		t.Fatalf("selection after Tab out of the filled pair = %v, want %v", native.selection, want)
	}
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.captureButton) {
		t.Fatalf("Tab out of the filled pair focused %#x, want Capture (%#x)", focus, uintptr(native.captureButton))
	}
	if selections != 1 {
		t.Fatalf("Tab out of the filled pair reported the selection %d time(s), want once", selections)
	}
}

// TestToolbarRestoresFocusOnActivation pins that the toolbar gives the
// keyboard focus back to the control that had it when it is activated again
// (UX-006, SCR-015) — on Alt+Tab, or as the global shortcut brings it back —
// rather than keeping it on the toolbar itself, where typed digits go
// nowhere. A click activation leaves the focus to the click, and gives it
// back once the click is handled only if the click left it on the toolbar.
func TestToolbarRestoresFocusOnActivation(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	toolbar := uintptr(native.toolbar)
	focused := func() uintptr {
		focus, _, _ := procGetFocus.Call()
		return focus
	}
	deactivate := func() {
		procSendMessageW.Call(toolbar, wmActivate, waInactive, 0)
		procSetFocus.Call(0)
	}
	// handleClick dispatches what the activation posted, as the shell's
	// message loop does once the click itself has been delivered.
	handleClick := func() {
		var message windowMessage
		for {
			if available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), toolbar, 0, 0, pmRemove); available == 0 {
				return
			}
			if !controller.FilterMessage(unsafe.Pointer(&message)) {
				procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
			}
		}
	}

	native.heightEdit.Focus()
	deactivate()
	procSendMessageW.Call(toolbar, wmActivate, waActive, 0)
	if focused() != uintptr(native.heightEdit.Handle()) {
		t.Fatalf("reactivating the toolbar focused %#x, want the height field it left", focused())
	}

	deactivate()
	procSendMessageW.Call(toolbar, wmActivate, waClickActive, 0)
	if focused() == uintptr(native.heightEdit.Handle()) {
		t.Fatal("a click activation gave the focus back to the height field before the click was handled")
	}
	native.cancel.Focus()
	handleClick()
	if focused() != uintptr(native.cancel.Handle()) {
		t.Fatalf("after a click on Cancel activated the toolbar, %#x has the focus, want Cancel", focused())
	}

	deactivate()
	procSendMessageW.Call(toolbar, wmActivate, waClickActive, 0)
	handleClick()
	if focused() != uintptr(native.cancel.Handle()) {
		t.Fatalf("after a click on the toolbar's background activated it, %#x has the focus, want Cancel, which had it", focused())
	}
}

// TestFreeRectangleArrowKeysNudgeSelection pins that the arrow keys move a
// free rectangle by one physical pixel once one exists (SCR-015), as they
// move the preset frame (SCR-027), and do nothing to the selection before:
// right after the Enter that created it, from the Capture button Enter
// focused, and from the toolbar itself.
func TestFreeRectangleArrowKeysNudgeSelection(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	procSetFocus.Call(uintptr(native.toolbar))
	right := windowMessage{HWND: native.toolbar, Message: wmKeyDown, WParam: vkRight}
	controller.FilterMessage(unsafe.Pointer(&right))
	if !native.selection.Empty() {
		t.Fatalf("an arrow key created a selection: %v", native.selection)
	}

	native.widthEdit.SetText("200")
	native.heightEdit.SetText("100")
	pressEnter(t, controller, native, native.heightEdit)
	start := native.selection
	focus, _, _ := procGetFocus.Call()
	if focus != uintptr(native.captureButton) {
		t.Fatalf("Enter that created the selection left the focus on %#x, want Capture (%#x)", focus, uintptr(native.captureButton))
	}
	down := windowMessage{HWND: native.capture.Handle(), Message: wmKeyDown, WParam: vkDown}
	if !controller.FilterMessage(unsafe.Pointer(&down)) {
		t.Fatal("the filter did not take the Down arrow on the Capture button Enter focused")
	}
	procSetFocus.Call(uintptr(native.toolbar))
	if !controller.FilterMessage(unsafe.Pointer(&right)) {
		t.Fatal("the filter did not take the Right arrow")
	}
	want := display.Rect{Left: start.Left + 1, Top: start.Top + 1, Right: start.Right + 1, Bottom: start.Bottom + 1}
	if native.selection != want {
		t.Fatalf("selection after Right and Down = %v, want %v", native.selection, want)
	}
	// Inside a dimension field the arrows keep moving the caret.
	caret := windowMessage{HWND: native.widthEdit.Handle(), Message: wmKeyDown, WParam: vkLeft}
	controller.FilterMessage(unsafe.Pointer(&caret))
	if native.selection != want {
		t.Fatalf("an arrow key inside the width field moved the selection to %v", native.selection)
	}
}

// TestFreeRectangleDisplayChangeKeepsTypedSize pins that a display change
// (SCR-054) in a Free Rectangle session without a selection leaves the size
// the user is typing toward one in the fields (SCR-015): there is no
// selection whose revalidated size they would show instead.
func TestFreeRectangleDisplayChangeKeepsTypedSize(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)

	native.widthEdit.SetText("1280")
	native.heightEdit.SetText("72")
	grown := native.bounds
	grown.Right += 64
	if !native.DisplayChanged(grown) {
		t.Fatal("the session did not survive a grown desktop")
	}
	if native.bounds != grown {
		t.Fatalf("the display change was answered without revalidating: bounds %v, want %v", native.bounds, grown)
	}
	if width, height := native.widthEdit.Text(), native.heightEdit.Text(); width != "1280" || height != "72" {
		t.Fatalf("the display change left the fields %q × %q, want the typed 1280 × 72", width, height)
	}
}

// TestCancelCommitsNoTypedSize pins that ending a session commits nothing
// typed: the field holding the focus loses it as Close destroys the
// toolbar, and that focus loss is no request to create a selection, nor a
// refusal to announce, after the user cancelled.
func TestCancelCommitsNoTypedSize(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	t.Cleanup(controller.Shutdown)
	selections := 0
	native.onSelection = func(display.Rect) { selections++ }

	native.heightEdit.Focus()
	if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.heightEdit.Handle()) {
		t.Fatal("the height field did not take the focus")
	}
	native.widthEdit.SetText("100")
	native.heightEdit.SetText("50")
	controller.Cancel()
	if selections != 0 {
		t.Fatalf("cancelling with a size typed in the fields created %d selection(s)", selections)
	}
}

// TestToolbarInstructionsFitTheirTwoLines pins that the toolbar's message
// label holds two lines of the shell's 18-pixel Segoe UI at 96 DPI, and that
// each mode's instructions and the selection's size messages fit in two, so
// none is clipped.
func TestToolbarInstructionsFitTheirTwoLines(t *testing.T) {
	face := utf16("Segoe UI")
	font, _, err := procCreateFontW.Call(^uintptr(17), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(face)))
	if font == 0 {
		t.Fatalf("CreateFontW: %v", err)
	}
	defer procDeleteObject.Call(font)
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		t.Fatal("GetDC failed")
	}
	defer procReleaseDC.Call(0, dc)
	previous, _, _ := procSelectObject.Call(dc, font)
	defer procSelectObject.Call(dc, previous)
	measure := func(text string) int32 {
		bounds := rectStruct{Right: toolbarLogicalWidth - 2*toolbarLogicalInset}
		height, _, _ := procDrawTextW.Call(dc, uintptr(unsafe.Pointer(utf16(text))), ^uintptr(0),
			uintptr(unsafe.Pointer(&bounds)), dtWordBreak|dtCalcRect|dtNoPrefix)
		return int32(height)
	}
	line := measure("X")
	if 2*line > toolbarMessageLogicalHeight {
		t.Fatalf("two lines of the toolbar's font are %d pixels tall, but its message label is %d", 2*line, toolbarMessageLogicalHeight)
	}
	for _, text := range []string{ui.OverlayInstructions, ui.OverlayPresetInstructions, ui.SelectionFitInvalid, ui.SelectionTooLarge} {
		if got := measure(text); got > 2*line {
			t.Errorf("%q takes %d lines of the toolbar, which leaves it two", text, (got+line-1)/line)
		}
	}
}

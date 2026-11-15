//go:build windows

package overlay

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// desktopHalf returns the left half of the current virtual desktop, a valid
// shrunken topology for display-change tests.
func desktopHalf(t *testing.T) display.Rect {
	t.Helper()
	desktop, err := display.NewMonitor().VirtualDesktopBounds()
	if err != nil {
		t.Fatal(err)
	}
	return display.Rect{
		Left:   desktop.Left,
		Top:    desktop.Top,
		Right:  desktop.Left + desktop.Width()/2,
		Bottom: desktop.Bottom,
	}
}

// TestDisplayChangeResizesFreeOverlayToNewDesktop is the AC-S12 recovery
// path for Free Rectangle: the desktop shrank under the active session, and
// the session survives with its tint covering exactly the new desktop and
// the selection moved back inside without changing its dimensions
// (SCR-054).
func TestDisplayChangeResizesFreeOverlayToNewDesktop(t *testing.T) {
	controller, log := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	shrunken := desktopHalf(t)
	// A selection reaching into the disconnected half must move back inside
	// the remaining desktop with its exact dimensions kept. The dimensions
	// scale with the test desktop so the selection always fits the shrunken
	// half, whatever resolution the machine runs.
	width, height := shrunken.Width()/2, shrunken.Height()/2
	if width < 8 || height < 8 {
		t.Skipf("test desktop too small: %v", shrunken)
	}
	selection := display.Rect{
		Left:   shrunken.Right - width/2,
		Top:    shrunken.Top + 10,
		Right:  shrunken.Right + width/2,
		Bottom: shrunken.Top + 10 + height,
	}
	native.setSelection(selection)
	controller.DesktopBounds = func() (display.Rect, error) { return shrunken, nil }

	controller.DisplayChange()

	if !controller.Active() {
		t.Fatal("a fitting session was ended by the display change")
	}
	if got := windowRect(t, native.overlay); got != shrunken {
		t.Fatalf("overlay rect after the display change = %v, want the new desktop %v", got, shrunken)
	}
	if native.pixelWidth != shrunken.Width() || native.pixelHeight != shrunken.Height() {
		t.Fatalf("backing bitmap = %d × %d, want the new desktop %d × %d",
			native.pixelWidth, native.pixelHeight, shrunken.Width(), shrunken.Height())
	}
	want := display.Rect{
		Left:   shrunken.Right - selection.Width(),
		Top:    selection.Top,
		Right:  shrunken.Right,
		Bottom: selection.Bottom,
	}
	if native.selection != want {
		t.Fatalf("selection after the display change = %v, want %v (same dimensions, inside the desktop)", native.selection, want)
	}
	if native.bounds != shrunken {
		t.Fatalf("session bounds = %v, want %v", native.bounds, shrunken)
	}
	// The surface was re-dimmed: the input-capturing tint stays visible
	// everywhere outside the selection (SCR-052).
	if got := native.pixelSlice()[5*int(native.pixelWidth)+5]; got != dimPixel {
		t.Fatalf("overlay pixel after the display change = %#08x, want the dimmed tint %#08x", got, dimPixel)
	}
	if len(*log) != 1 {
		t.Fatalf("a kept session touched the main window: %v", *log)
	}
	// The session still works afterwards: cancelling restores the main
	// window exactly once.
	controller.Cancel()
	if len(*log) != 2 || (*log)[1] != "restore" {
		t.Fatalf("call order after cancel = %v", *log)
	}
}

// TestDisplayChangeKeepsPresetFrameInsideNewDesktop covers the Preset
// Rectangle recovery path: the frame keeps its exact dimensions (SCR-020)
// and is moved back inside the shrunken desktop.
func TestDisplayChangeKeepsPresetFrameInsideNewDesktop(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	shrunken := desktopHalf(t)
	size := display.Point{X: 320, Y: 240}
	if shrunken.Width() < size.X || shrunken.Height() < size.Y {
		t.Skipf("test desktop too small: %v", shrunken)
	}
	if err := controller.Begin(42, 0, ModePresetRectangle, size, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	// Move the frame so it reaches into the disconnected half.
	frame := display.Rect{
		Left:   shrunken.Right - 60,
		Top:    shrunken.Top + 30,
		Right:  shrunken.Right - 60 + size.X,
		Bottom: shrunken.Top + 30 + size.Y,
	}
	native.setSelection(frame)
	controller.DesktopBounds = func() (display.Rect, error) { return shrunken, nil }

	controller.DisplayChange()

	if !controller.Active() {
		t.Fatal("a fitting preset frame was ended by the display change")
	}
	if native.selection.Width() != size.X || native.selection.Height() != size.Y {
		t.Fatalf("frame dimensions changed to %d × %d, want %d × %d (SCR-020)",
			native.selection.Width(), native.selection.Height(), size.X, size.Y)
	}
	if native.selection.Right > shrunken.Right || native.selection.Bottom > shrunken.Bottom ||
		native.selection.Left < shrunken.Left || native.selection.Top < shrunken.Top {
		t.Fatalf("frame %v reaches outside the new desktop %v", native.selection, shrunken)
	}
	// The frame window outlines the revalidated selection, border ring
	// included.
	want := inflateRect(native.selection, native.border)
	if got := windowRect(t, native.overlay); got != want {
		t.Fatalf("frame window rect = %v, want %v", got, want)
	}
}

// TestDisplayChangeDestroysSessionWindows is AC-S12's restart path: the
// selection's dimensions exceed the remaining desktop, so the session ends
// with an explanation and every session window is destroyed — no invisible
// input-capturing surface survives (SCR-052).
func TestDisplayChangeDestroysSessionWindows(t *testing.T) {
	controller, log := newNativeSessionController(t)
	var reason string
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{
		OnSessionLost: func(got string) { reason = got },
	}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	overlayWindow, toolbarWindow := native.overlay, native.toolbar
	// A selection drawn on the full desktop cannot fit its left half.
	desktop := native.bounds
	native.setSelection(display.Rect{
		Left:   desktop.Left,
		Top:    desktop.Top,
		Right:  desktop.Left + desktop.Width()*3/4,
		Bottom: desktop.Bottom,
	})
	controller.DesktopBounds = func() (display.Rect, error) { return desktopHalf(t), nil }

	controller.DisplayChange()

	if controller.Active() {
		t.Fatal("session survived a display change it cannot fit")
	}
	if isWindow(overlayWindow) || isWindow(toolbarWindow) {
		t.Fatal("session windows survived the ended session")
	}
	if len(*log) != 2 || (*log)[1] != "restore" {
		t.Fatalf("call order after a lost session = %v, want [hide restore]", *log)
	}
	if !strings.Contains(reason, "display layout changed") {
		t.Fatalf("session-loss reason = %q, want a display-layout explanation", reason)
	}
}

// TestDisplayChangeLeavesUnchangedTopologyUntouched pins the no-op answer:
// a notification whose physical topology, cursor-display scaling, and anchor
// work area all match the session's measurements must not re-render or
// re-report anything (SCR-054).
func TestDisplayChangeLeavesUnchangedTopologyUntouched(t *testing.T) {
	controller, log := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	selection := display.Rect{
		Left:   native.bounds.Left + 100,
		Top:    native.bounds.Top + 100,
		Right:  native.bounds.Left + 740,
		Bottom: native.bounds.Top + 580,
	}
	native.setSelection(selection)
	overlayBefore := windowRect(t, native.overlay)
	toolbarBefore := windowRect(t, native.toolbar)

	controller.DisplayChange()

	if !controller.Active() {
		t.Fatal("an unchanged topology ended the session")
	}
	if native.selection != selection {
		t.Fatalf("selection after an unchanged topology = %v, want %v", native.selection, selection)
	}
	if got := windowRect(t, native.overlay); got != overlayBefore {
		t.Fatalf("overlay rect after an unchanged topology = %v, want %v", got, overlayBefore)
	}
	if got := windowRect(t, native.toolbar); got != toolbarBefore {
		t.Fatalf("toolbar rect after an unchanged topology = %v, want %v", got, toolbarBefore)
	}
	if got := native.pixelSlice()[5*int(native.pixelWidth)+5]; got != dimPixel {
		t.Fatalf("overlay pixel after an unchanged topology = %#08x, want the dimmed tint %#08x", got, dimPixel)
	}
	if len(*log) != 1 {
		t.Fatalf("an unchanged topology touched the main window: %v", *log)
	}
}

// TestDisplayChangeResolvesScalingOnUnchangedTopology covers the SCR-054
// scaling path: a DPI or work-area change keeps the physical virtual-desktop
// rectangle identical, so the controller cannot see it — the notification
// still reaches the session, which re-resolves its DPI-derived measurements
// instead of carrying stale ones.
func TestDisplayChangeResolvesScalingOnUnchangedTopology(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	// Simulate measurements left over from a previous scaling level: the
	// session's own record no longer matches the cursor display, exactly as
	// a mid-session scaling change leaves it.
	native.dpi = 1
	native.anchor = display.Rect{}

	controller.DisplayChange()

	if !controller.Active() {
		t.Fatal("a scaling-only change ended the session")
	}
	cursor, err := display.NewMonitor().CursorDisplay()
	if err != nil {
		t.Fatal(err)
	}
	if native.dpi != cursor.DPIX {
		t.Fatalf("session DPI after the display change = %d, want the cursor display's %d", native.dpi, cursor.DPIX)
	}
	if native.anchor != cursor.WorkArea {
		t.Fatalf("toolbar anchor after the display change = %v, want the cursor display work area %v", native.anchor, cursor.WorkArea)
	}
	wantSize := display.LogicalToPhysical(toolbarLogicalWidth, cursor.DPIX)
	if native.toolbarSize.Width != wantSize {
		t.Fatalf("toolbar width after the display change = %d, want %d at %d DPI", native.toolbarSize.Width, wantSize, cursor.DPIX)
	}
	controller.Cancel()
}

// TestSetFontUpdatesToolbarControls pins the DPI-change font handoff end to
// end: the shell destroys the session toolbar's font while recreating its
// own, so the recreated handle must reach every toolbar control — a control
// left holding the destroyed handle would paint with a fallback font at the
// wrong size (SCR-054).
func TestSetFontUpdatesToolbarControls(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)

	// A stock font is a guaranteed-valid HFONT: the edit controls validate
	// the handle they are handed and keep no record of an invalid one.
	font, _, _ := procGetStockObject.Call(17) // DEFAULT_GUI_FONT
	controller.SetFont(font)

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
			t.Errorf("%s font = %d, want %d", name, got, font)
		}
	}
	controller.Cancel()
}

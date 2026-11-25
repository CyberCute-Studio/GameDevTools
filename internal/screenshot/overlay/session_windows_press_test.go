//go:build windows

package overlay

import (
	"testing"
	"unsafe"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

var (
	procTestSetActiveWindow = user32.NewProc("SetActiveWindow")
	procTestGetActiveWindow = user32.NewProc("GetActiveWindow")
)

const (
	// wmMouseActivateTest is WM_MOUSEACTIVATE, which Windows sends a window
	// before a press on it reaches it, and maActivate, maActivateAndEat,
	// maNoActivateTest, and maNoActivateAndEat are its answers.
	wmMouseActivateTest = 0x0021
	maActivate          = 1
	maActivateAndEat    = 2
	maNoActivateTest    = 3
	maNoActivateAndEat  = 4
)

// pressAsWindowsDoes is a real press on a session window as Windows handles
// it, which sending the button message alone skips: Windows first asks the
// window with WM_MOUSEACTIVATE, activates it unless the answer says not to,
// and then delivers the press unless the answer eats it. It returns the
// answer.
func pressAsWindowsDoes(hwnd uintptr, message uint32, x, y int32) uintptr {
	answer, _, _ := procSendMessageW.Call(hwnd, wmMouseActivateTest, hwnd, uintptr(htClient)|uintptr(message)<<16)
	if answer == maActivate || answer == maActivateAndEat {
		procTestSetActiveWindow.Call(hwnd)
	}
	if answer != maActivateAndEat && answer != maNoActivateAndEat {
		lParam := uintptr(uint32(uint16(x)) | uint32(uint16(y))<<16)
		procSendMessageW.Call(hwnd, uintptr(message), 0, lParam)
	}
	return answer
}

// TestPressOnTheOverlayLeavesTheToolbarTheKeyboard is a real press on the
// Free Rectangle overlay with a size typed in a field but not yet applied. The
// overlay must not take the activation from the toolbar: the field would lose
// the focus before any drag started, applying the typed size (SCR-013), and
// the toolbar would lose the keyboard, so Enter and Tab would do nothing there
// until it was clicked (UX-006, SCR-015).
func TestPressOnTheOverlayLeavesTheToolbarTheKeyboard(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	drawn := native.selection
	native.widthEdit.Focus()
	if active, _, _ := procTestGetActiveWindow.Call(); active != uintptr(native.toolbar) {
		t.Skip("the test thread could not activate the toolbar")
	}
	t.Cleanup(func() { procSetFocus.Call(0) })
	native.widthEdit.SetText("300")

	for _, press := range []struct {
		name    string
		message uint32
		x, y    int32
	}{
		{"left on the selection", wmLButtonDown, 200, 170},
		{"right", wmRButtonDown, 600, 500},
	} {
		if answer := pressAsWindowsDoes(uintptr(native.overlay), press.message, press.x, press.y); answer != maNoActivateTest {
			t.Errorf("%s: the overlay answered WM_MOUSEACTIVATE with %d, want MA_NOACTIVATE", press.name, answer)
		}
		if press.message == wmLButtonDown {
			sendMouse(native.overlay, wmLButtonUp, press.x, press.y)
		}
		if native.selection != drawn {
			t.Fatalf("%s: the press applied the typed width: %v, want %v (SCR-013)", press.name, native.selection, drawn)
		}
		if active, _, _ := procTestGetActiveWindow.Call(); active != uintptr(native.toolbar) {
			t.Fatalf("%s: the press made %#x active, want the toolbar %#x", press.name, active, uintptr(native.toolbar))
		}
		if focus, _, _ := procGetFocus.Call(); focus != uintptr(native.widthEdit.Handle()) {
			t.Fatalf("%s: the press moved the focus to %#x, want the width field", press.name, focus)
		}
	}
}

// TestPressOnThePresetFrameLeavesTheToolbarTheKeyboard is the same press on
// the Preset Rectangle frame's border, which must not take the keyboard from
// the toolbar either.
func TestPressOnThePresetFrameLeavesTheToolbarTheKeyboard(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModePresetRectangle, display.Point{X: 320, Y: 240}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	if answer, _, _ := procSendMessageW.Call(uintptr(native.overlay), wmMouseActivateTest, uintptr(native.overlay), uintptr(htClient)|uintptr(wmLButtonDown)<<16); answer != maNoActivateTest {
		t.Fatalf("the preset frame answered WM_MOUSEACTIVATE with %d, want MA_NOACTIVATE", answer)
	}
}

// TestToolbarStaysAboveTheOverlayWhileThePressIsDown is the first press of a
// session on the overlay, which a real press can raise above the toolbar
// within the topmost band. The toolbar must come back in front as the press
// arrives, not only once it ends, or the live size it shows during the first
// draw is seen only through the overlay's tint (SCR-011).
func TestToolbarStaysAboveTheOverlayWhileThePressIsDown(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	const hwndTopmost = ^uintptr(0)
	procSetWindowPos.Call(uintptr(native.overlay), hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	if !windowAbove(native.overlay, native.toolbar) {
		t.Fatal("the test could not raise the overlay above the toolbar")
	}
	sendMouse(native.overlay, wmLButtonDown, 60, 40)
	if !windowAbove(native.toolbar, native.overlay) {
		t.Fatal("the toolbar stayed behind the overlay while the press was down (SCR-011)")
	}
	sendMouse(native.overlay, wmMouseMove, 460, 340)
	if !windowAbove(native.toolbar, native.overlay) {
		t.Fatal("the toolbar fell behind the overlay during the draw")
	}
	sendMouse(native.overlay, wmLButtonUp, 460, 340)
}

// TestAnotherButtonKeepsADraggedToolbarWhereItIs is a right, middle, or
// X-button press after the user dragged the toolbar aside: the press puts the
// toolbar back in front of the overlay, but must not move it back beside the
// selection (SCR-025).
func TestAnotherButtonKeepsADraggedToolbarWhereItIs(t *testing.T) {
	controller, _ := newNativeSessionController(t)
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
		t.Fatal(err)
	}
	native := nativeOf(t, controller)
	sendMouse(native.overlay, wmLButtonDown, 100, 100)
	sendMouse(native.overlay, wmMouseMove, 300, 240)
	sendMouse(native.overlay, wmLButtonUp, 300, 240)
	aside := display.Point{X: native.bounds.Left + 7, Y: native.bounds.Top + 9}
	procSetWindowPos.Call(uintptr(native.toolbar), 0, uintptr(uint32(aside.X)), uintptr(uint32(aside.Y)), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	const hwndTopmost = ^uintptr(0)
	for _, press := range []struct {
		name    string
		message uint32
	}{{"right", wmRButtonDown}, {"middle", wmMButtonDown}, {"X", wmXButtonDown}} {
		procSetWindowPos.Call(uintptr(native.overlay), hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		sendMouse(native.overlay, press.message, 600, 500)
		var bounds rectStruct
		procGetWindowRect.Call(uintptr(native.toolbar), uintptr(unsafe.Pointer(&bounds)))
		if bounds.Left != aside.X || bounds.Top != aside.Y {
			t.Fatalf("%s: the press moved the toolbar to (%d, %d), want it left at (%d, %d)", press.name, bounds.Left, bounds.Top, aside.X, aside.Y)
		}
		if !windowAbove(native.toolbar, native.overlay) {
			t.Fatalf("%s: the toolbar stayed behind the overlay after the press", press.name)
		}
	}
}

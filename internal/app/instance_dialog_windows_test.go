//go:build windows

package app

import (
	"runtime"
	"testing"
	"time"
	"unsafe"
)

var (
	procInstanceTestSetActiveWindow = user32.NewProc("SetActiveWindow")
	procInstanceTestEnableWindow    = user32.NewProc("EnableWindow")
)

// newInstanceTestDialog creates a visible popup that owner owns, as a modal
// dialog of the running instance is, makes it the window its owner last
// activated, and disables the owner, as the dialog's modal loop does.
func newInstanceTestDialog(t *testing.T, owner uintptr) uintptr {
	t.Helper()
	const wsPopupCaption, swShowNAInstanceDialog = 0x80c00000, 8
	offScreen := int32(-22000)
	dialog, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16("STATIC"))),
		uintptr(unsafe.Pointer(utf16("instance activation test dialog"))),
		wsPopupCaption,
		uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 200, 100,
		owner, 0, 0, 0,
	)
	if dialog == 0 {
		t.Fatalf("CreateWindowExW(dialog): %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(dialog) })
	procShowWindow.Call(dialog, swShowNAInstanceDialog)
	procInstanceTestSetActiveWindow.Call(dialog)
	if popup, _, _ := procGetLastActivePopup.Call(owner); popup != dialog {
		t.Skip("the test could not make the dialog its owner's last active popup")
	}
	procInstanceTestEnableWindow.Call(owner, 0)
	t.Cleanup(func() { procInstanceTestEnableWindow.Call(owner, 1) })
	return dialog
}

// recordForeground replaces bringForward for the test and returns the windows
// it was asked to bring forward.
func recordForeground(t *testing.T) *[]uintptr {
	t.Helper()
	brought := []uintptr{}
	previous := bringForward
	bringForward = func(hwnd uintptr) { brought = append(brought, hwnd) }
	t.Cleanup(func() { bringForward = previous })
	return &brought
}

// TestActivateExistingInstanceBringsTheOpenDialogForward is a second launch
// while a modal dialog of the running instance is open: the dialog has
// disabled the main window, so bringing that window forward would give the
// keyboard to a window that refuses it, leaving the dialog the user must
// answer behind the focus (APP-005). The dialog comes forward instead, also
// when the main window was minimized, which hid the dialog with it.
func TestActivateExistingInstanceBringsTheOpenDialogForward(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, minimized := range []bool{false, true} {
		className, hwnd := newInstanceTestWindow(t)
		const swShowNAInstanceTest = 8
		offScreen := int32(-22000)
		procSetWindowPos.Call(hwnd, 0, uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 320, 240, swpNoZOrder|swpNoActivate)
		procShowWindow.Call(hwnd, swShowNAInstanceTest)
		dialog := newInstanceTestDialog(t, hwnd)
		if minimized {
			procShowWindow.Call(hwnd, swMinimizeInstanceTest)
		}
		brought := recordForeground(t)
		if !activateExistingInstance(className, time.Second) {
			t.Fatalf("minimized %v: activation did not find the existing window", minimized)
		}
		if len(*brought) != 1 || (*brought)[0] != dialog {
			t.Fatalf("minimized %v: activation brought %v forward, want the dialog %#x and not the main window %#x", minimized, *brought, dialog, hwnd)
		}
		if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
			t.Fatalf("minimized %v: activation left the main window minimized", minimized)
		}
	}
}

// TestActivateExistingInstanceBringsAnEnabledWindowForward is the launch with
// no dialog open, and with a main window disabled though none of its popups
// was active: the main window itself comes forward.
func TestActivateExistingInstanceBringsAnEnabledWindowForward(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className, hwnd := newInstanceTestWindow(t)
	const swShowNAInstanceTest = 8
	offScreen := int32(-22000)
	procSetWindowPos.Call(hwnd, 0, uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 320, 240, swpNoZOrder|swpNoActivate)
	procShowWindow.Call(hwnd, swShowNAInstanceTest)
	brought := recordForeground(t)
	if !activateExistingInstance(className, time.Second) || len(*brought) != 1 || (*brought)[0] != hwnd {
		t.Fatalf("an enabled window: brought %v forward, want %#x", *brought, hwnd)
	}
	procInstanceTestEnableWindow.Call(hwnd, 0)
	t.Cleanup(func() { procInstanceTestEnableWindow.Call(hwnd, 1) })
	*brought = (*brought)[:0]
	if !activateExistingInstance(className, time.Second) || len(*brought) != 1 || (*brought)[0] != hwnd {
		t.Fatalf("a disabled window with no popup: brought %v forward, want %#x", *brought, hwnd)
	}
}

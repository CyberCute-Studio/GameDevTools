//go:build windows

package app

import (
	"testing"
	"unsafe"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/capture"
)

const (
	wmSysCommandTest = 0x0112
	scRestoreTest    = 0xf120
	scMaximizeTest   = 0xf030
	scMinimizeTest   = 0xf020
	swShowNATest     = 8
	swpNoSizeTest    = 0x0001
)

// pumpShellMessages dispatches the messages waiting for the window, as the
// message loop does.
func pumpShellMessages(hwnd uintptr) {
	var message msg
	for {
		if available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), hwnd, 0, 0, pmRemove); available == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

// TestAWindowACaptureHidesStaysHiddenUntilItsRestore is the taskbar's restore
// arriving in the instant after a capture hid the main window, as a click on
// its button just before the shortcut is: the window would show before the
// screen is read and be in the image (SCR-044), or show under a selection.
// While the capture keeps it hidden, a restore or maximize, sent or posted as
// the taskbar does, and a direct show, as another program may make, leave it
// hidden, a refused command with a beep. The capture's own restore shows it
// again, minimized if it was.
func TestAWindowACaptureHidesStaysHiddenUntilItsRestore(t *testing.T) {
	shell := newCommandTestShell(t, nil)
	hwnd := uintptr(shell.hwnd)
	beeps := 0
	previous := refusedShowBeep
	refusedShowBeep = func() { beeps++ }
	t.Cleanup(func() { refusedShowBeep = previous })
	offScreen := int32(-22000)
	procSetWindowPos.Call(hwnd, 0, uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 0, 0, swpNoSizeTest|swpNoZOrder|swpNoActivate)

	for _, minimized := range []bool{false, true} {
		procShowWindow.Call(hwnd, swShowNATest)
		if minimized {
			procShowWindow.Call(hwnd, swShowMinNoActive)
		}
		exclusion, err := capture.Hide(hwnd)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(exclusion.Restore)
		if !capture.KeptHidden(hwnd) {
			t.Fatalf("minimized %v: the capture's hide was not recorded", minimized)
		}
		beeps = 0
		for _, attempt := range []struct {
			name string
			show func()
		}{
			{"sent restore", func() { procSendMessageW.Call(hwnd, wmSysCommandTest, scRestoreTest, 0) }},
			{"posted restore", func() {
				procPostMessageW.Call(hwnd, wmSysCommandTest, scRestoreTest, 0)
				pumpShellMessages(hwnd)
			}},
			{"sent maximize", func() { procSendMessageW.Call(hwnd, wmSysCommandTest, scMaximizeTest, 0) }},
			// DefWindowProc reads only the command's bits, as the shell must.
			{"sent restore with higher bits set", func() { procSendMessageW.Call(hwnd, wmSysCommandTest, 0x10000|scRestoreTest|0x2, 0) }},
			{"direct show", func() { procShowWindow.Call(hwnd, swShowNATest) }},
		} {
			attempt.show()
			if visible, _, _ := procIsWindowVisible.Call(hwnd); visible != 0 {
				t.Fatalf("minimized %v: a %s showed the window a capture hides (SCR-044)", minimized, attempt.name)
			}
		}
		if beeps != 4 {
			t.Fatalf("minimized %v: the refused commands beeped %d times, want 4", minimized, beeps)
		}
		exclusion.Restore()
		if capture.KeptHidden(hwnd) {
			t.Fatalf("minimized %v: the window was still recorded as hidden once restored", minimized)
		}
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			t.Fatalf("minimized %v: the capture's restore did not show the window", minimized)
		}
		if iconic, _, _ := procIsIconic.Call(hwnd); (iconic != 0) != minimized {
			t.Fatalf("minimized %v: the window came back minimized %v", minimized, iconic != 0)
		}
	}
}

// TestAMinimizeWhileACaptureHidesTheWindowShowsNothing is a click on the
// taskbar button of the active main window, which minimizes it, arriving in
// the instant after a capture hid it. It shows nothing, so it is let through
// without a beep, and the capture's restore brings the window back minimized.
func TestAMinimizeWhileACaptureHidesTheWindowShowsNothing(t *testing.T) {
	shell := newCommandTestShell(t, nil)
	hwnd := uintptr(shell.hwnd)
	beeps := 0
	previous := refusedShowBeep
	refusedShowBeep = func() { beeps++ }
	t.Cleanup(func() { refusedShowBeep = previous })
	offScreen := int32(-22000)
	procSetWindowPos.Call(hwnd, 0, uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 0, 0, swpNoSizeTest|swpNoZOrder|swpNoActivate)
	procShowWindow.Call(hwnd, swShowNATest)
	exclusion, err := capture.Hide(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exclusion.Restore)
	procSendMessageW.Call(hwnd, wmSysCommandTest, scMinimizeTest, 0)
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible != 0 {
		t.Fatal("a minimize showed the window a capture hides (SCR-044)")
	}
	if beeps != 0 {
		t.Fatalf("a minimize beeped %d times, want none", beeps)
	}
	exclusion.Restore()
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		t.Fatal("the capture's restore did not show the window")
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic == 0 {
		t.Fatal("the window minimized during the capture came back restored")
	}
}

// TestOverlappingCapturesOfTheWindowShowItOnceBothEnd is two exclusions of
// the main window, the second taken while the first keeps it hidden, ended
// first to last. The first's show would be refused while the second keeps the
// window hidden, and the second found it hidden, so the window would stay
// hidden, with no taskbar button, for good; it is shown once both have ended.
func TestOverlappingCapturesOfTheWindowShowItOnceBothEnd(t *testing.T) {
	shell := newCommandTestShell(t, nil)
	hwnd := uintptr(shell.hwnd)
	offScreen := int32(-22000)
	procSetWindowPos.Call(hwnd, 0, uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 0, 0, swpNoSizeTest|swpNoZOrder|swpNoActivate)
	procShowWindow.Call(hwnd, swShowNATest)
	first, err := capture.Hide(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Restore)
	second, err := capture.Hide(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Restore)
	first.Restore()
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible != 0 {
		t.Fatal("the window showed while the second capture kept it hidden (SCR-044)")
	}
	second.Restore()
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		t.Fatal("the window stayed hidden once both captures had ended")
	}
}

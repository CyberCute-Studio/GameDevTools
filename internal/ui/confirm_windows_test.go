//go:build windows

package ui

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	bmClick   = 0x00f5
	wmCommand = 0x0111
	idNo      = 7
)

var (
	procFindWindowW  = user32.NewProc("FindWindowW")
	procGetDlgItem   = user32.NewProc("GetDlgItem")
	procPostMessageW = user32.NewProc("PostMessageW")

	// confirmProbe is what confirmProbeTimer does as it fires inside the
	// question's modal loop: clicks Yes first when yes is set, and then
	// destroys parent, as the end of the Windows session destroys the main
	// window under an open question. The callback is created once, as the
	// runtime never releases one.
	confirmProbe struct {
		parent windows.Handle
		yes    bool
		timer  uintptr
	}
	confirmProbeTimer = windows.NewCallback(func(_, _, _, _ uintptr) uintptr {
		procKillTimer.Call(0, confirmProbe.timer)
		if confirmProbe.yes {
			if yes, _, _ := procGetDlgItem.Call(confirmQuestion(), idYes); yes != 0 {
				procSendMessageW.Call(yes, bmClick, 0, 0)
			}
		}
		procDestroyWindow.Call(uintptr(confirmProbe.parent))
		return 0
	})
)

const confirmProbeTitle = "GameDevTools Confirm test"

// confirmQuestion returns the open question Confirm shows in the test, or 0.
func confirmQuestion() uintptr {
	question, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16("#32770"))), uintptr(unsafe.Pointer(utf16(confirmProbeTitle))))
	return question
}

// TestConfirmAnswersNoOnceItsParentIsDestroyed pins that a question whose
// parent window is destroyed while it is open — as the end of the Windows
// session destroys the main window (WM_ENDSESSION) — answers No, even when
// Yes was clicked just before, so no caller acts on a window that is gone.
// A thread timer, which the question's modal loop dispatches, destroys the
// parent; should it never fire, the question is answered No after a while,
// and the test fails on the time it took rather than hanging.
func TestConfirmAnswersNoOnceItsParentIsDestroyed(t *testing.T) {
	for _, yes := range []bool{false, true} {
		parent := newTestParent(t)
		confirmProbe.parent, confirmProbe.yes = parent, yes
		timer, _, err := procSetTimer.Call(0, 0, 200, confirmProbeTimer)
		if timer == 0 {
			t.Fatalf("SetTimer: %v", err)
		}
		confirmProbe.timer = timer
		stop := make(chan struct{})
		go func() {
			select {
			case <-stop:
			case <-time.After(10 * time.Second):
				procPostMessageW.Call(confirmQuestion(), wmCommand, idNo, 0)
			}
		}()
		started := time.Now()
		answer := Confirm(parent, confirmProbeTitle, "Close anyway?")
		close(stop)
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("the question stayed open for %v after its parent was destroyed", elapsed)
		}
		if answer {
			t.Fatalf("Confirm() = Yes once its parent was destroyed (Yes clicked first: %v), want No", yes)
		}
	}
}

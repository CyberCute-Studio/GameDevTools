//go:build windows

package settings

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var (
	testUser32            = windows.NewLazySystemDLL("user32.dll")
	procTestCreateWindowW = testUser32.NewProc("CreateWindowExW")
	procTestDestroyWindow = testUser32.NewProc("DestroyWindow")
)

// newSettingsTestView creates the Settings view on a plain parent window,
// with a question that answers as answer says and a restore that counts its
// runs.
func newSettingsTestView(t *testing.T, answer bool) (*NativeView, *int, *int) {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := procTestCreateWindowW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x00CF0000, 0, 0, 400, 300, 0, 0, 0, 0)
	if parent == 0 {
		t.Fatalf("CreateWindowExW(test parent): %v", err)
	}
	t.Cleanup(func() { procTestDestroyWindow.Call(parent) })
	view := newNativeView()
	questions, restores := 0, 0
	view.confirm = func(_ windows.Handle, title, message string) bool {
		questions++
		if title != ui.RestoreDefaultsTitle || message != ui.RestoreDefaultsConfirmation {
			t.Errorf("question = %q, %q, want the restore's", title, message)
		}
		return answer
	}
	view.NativeRestoreHandler(func() ui.Notice {
		restores++
		return ui.Notice{Kind: ui.NoticeSuccess, Message: ui.SettingsRestoredNoBackup}
	})
	if err := view.NativeCreate(windows.Handle(parent)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.NativeDestroy)
	return view, &questions, &restores
}

// TestNativeViewRestoresOnlyOnceConfirmed pins Restore Default Settings in
// the Settings view (SET-005, UX-002): the button asks first, a No changes
// nothing, and a Yes runs the shell's restore and shows its outcome.
func TestNativeViewRestoresOnlyOnceConfirmed(t *testing.T) {
	declined, questions, restores := newSettingsTestView(t, false)
	if !declined.NativeCommand(restoreDefaultsCommand, 0) {
		t.Fatal("Restore Default Settings was not handled")
	}
	if *questions != 1 || *restores != 0 || declined.status.Text() != "" {
		t.Fatalf("after No: %d question(s), %d restore(s), status %q; want one question and nothing else", *questions, *restores, declined.status.Text())
	}

	confirmed, questions, restores := newSettingsTestView(t, true)
	confirmed.NativeCommand(restoreDefaultsCommand, 0)
	want := ui.Notice{Kind: ui.NoticeSuccess, Message: ui.SettingsRestoredNoBackup}.Text()
	if *questions != 1 || *restores != 1 || confirmed.status.Text() != want {
		t.Fatalf("after Yes: %d question(s), %d restore(s), status %q; want one each and %q", *questions, *restores, confirmed.status.Text(), want)
	}
	if confirmed.NativeCommand(restoreDefaultsCommand+1, 0) {
		t.Fatal("the view handled another module's command")
	}
}

var (
	procTestSetTimer  = testUser32.NewProc("SetTimer")
	procTestKillTimer = testUser32.NewProc("KillTimer")

	// destroyingTimer destroys destroyingParent as it fires inside the
	// question's modal loop, as the end of the Windows session destroys the
	// main window under an open question (WM_ENDSESSION). The callback is
	// created once, as the runtime never releases one.
	destroyingParent, destroyingTimerID uintptr
	destroyingTimer                     = windows.NewCallback(func(_, _, _, _ uintptr) uintptr {
		procTestKillTimer.Call(0, destroyingTimerID)
		procTestDestroyWindow.Call(destroyingParent)
		return 0
	})
)

// TestNativeViewRestoresNothingOnceDestroyed pins that the end of the
// Windows session, which destroys the window while the restore's question
// is open, leaves the answer nothing to do: the view asks through
// ui.Confirm, which answers No once its parent is gone.
func TestNativeViewRestoresNothingOnceDestroyed(t *testing.T) {
	view, _, restores := newSettingsTestView(t, true)
	view.confirm = ui.Confirm
	destroyingParent = uintptr(view.parent)
	timer, _, err := procTestSetTimer.Call(0, 0, 200, destroyingTimer)
	if timer == 0 {
		t.Fatalf("SetTimer: %v", err)
	}
	destroyingTimerID = timer
	view.NativeCommand(restoreDefaultsCommand, 0)
	if *restores != 0 {
		t.Fatalf("restores after the window was destroyed = %d, want none", *restores)
	}
}

//go:build windows

package ui

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var procEnumThreadWindows = user32.NewProc("EnumThreadWindows")

// closeThreadDialogs answers No and Cancel in every window of thread, which
// ends a question or a picker left open there.
var closeThreadDialogs = windows.NewCallback(func(hwnd, _ uintptr) uintptr {
	const idCancel = 2
	procPostMessageW.Call(hwnd, wmCommand, idNo, 0)
	procPostMessageW.Call(hwnd, wmCommand, idCancel, 0)
	return 1
})

// openWithin runs open on a thread of its own and reports whether it returned
// within limit. One that did not, as a dialog left open does, is closed.
func openWithin(t *testing.T, limit time.Duration, open func()) bool {
	t.Helper()
	thread := make(chan uint32, 1)
	done := make(chan struct{})
	go func() {
		// Never unlocked, so the thread ends with this goroutine.
		runtime.LockOSThread()
		thread <- windows.GetCurrentThreadId()
		open()
		close(done)
	}()
	id := <-thread
	select {
	case <-done:
		return true
	case <-time.After(limit):
	}
	for range 100 {
		procEnumThreadWindows.Call(uintptr(id), closeThreadDialogs, 0)
		select {
		case <-done:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("a dialog left open could not be closed")
	return false
}

// TestHeldDialogsDoNotOpen is a dialog asked for while a capture or a
// selection holds them: the question and both pickers return at once, as
// cancelled, with a beep, and the question answers No; once every hold is
// released, the question opens again.
func TestHeldDialogsDoNotOpen(t *testing.T) {
	if DialogsHeld() {
		t.Fatal("the dialogs were already held as the test started: an earlier test left a hold")
	}
	beeps := 0
	previous := refusalBeep
	refusalBeep = func() { beeps++ }
	t.Cleanup(func() { refusalBeep = previous })

	// Releasing twice is harmless, so a test that fails early still leaves
	// the dialogs free for the tests after it.
	outer := HoldDialogs()
	t.Cleanup(outer)
	inner := HoldDialogs()
	t.Cleanup(inner)
	if !DialogsHeld() {
		t.Fatal("a hold did not hold the dialogs")
	}
	const limit = 2 * time.Second
	answer := true
	if !openWithin(t, limit, func() { answer = Confirm(0, confirmProbeTitle, "Held question") }) || answer {
		t.Fatalf("a held question opened, or answered %v", answer)
	}
	var paths []string
	picked := true
	var err error
	if !openWithin(t, limit, func() { paths, picked, err = PickImageFiles(0, "Held picker") }) || picked || paths != nil || err != nil {
		t.Fatalf("a held image picker opened, or returned %v, %v, %v", paths, picked, err)
	}
	folder, chosen := "x", true
	if !openWithin(t, limit, func() { folder, chosen, err = BrowseForFolder(0, "Held folder picker") }) || chosen || folder != "" || err != nil {
		t.Fatalf("a held folder picker opened, or returned %q, %v, %v", folder, chosen, err)
	}
	if beeps != 3 {
		t.Fatalf("the refusals beeped %d times, want 3", beeps)
	}

	inner()
	inner()
	if !DialogsHeld() {
		t.Fatal("releasing one hold twice released the other")
	}
	outer()
	if DialogsHeld() {
		t.Fatal("the dialogs stayed held once every hold was released")
	}
	var opened atomic.Bool
	go func() {
		for range 100 {
			if question := confirmQuestion(); question != 0 {
				opened.Store(true)
				procPostMessageW.Call(question, wmCommand, idNo, 0)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	openWithin(t, 10*time.Second, func() { Confirm(0, confirmProbeTitle, "Released question") })
	if !opened.Load() {
		t.Fatal("the question did not open once the holds were released")
	}
	if beeps != 3 {
		t.Fatalf("a released question beeped: %d beeps, want 3", beeps)
	}
}

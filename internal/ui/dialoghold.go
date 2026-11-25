package ui

import "sync"

var (
	// holdMu guards dialogHolds and holdsEnded.
	holdMu sync.Mutex
	// dialogHolds counts the holds that keep the application's dialogs
	// closed.
	dialogHolds int
	// holdsEnded is closed once the last hold of a run of them is released,
	// which ends waitWhileDialogsHeld's wait. A hold taken while none is
	// replaces it with an open one.
	holdsEnded = closedChannel()
)

func closedChannel() chan struct{} {
	ended := make(chan struct{})
	close(ended)
	return ended
}

// HoldDialogs keeps every dialog this package opens — Confirm's question and
// the image and folder pickers — from opening until the returned release
// runs, and keeps OpenFolder and RevealInFolder from starting File Explorer
// meanwhile. A capture holds them from the moment it starts until it has
// read the screen, and a selection session for as long as it is on screen:
// the GUI thread goes on dispatching messages meanwhile, so a click already
// queued could otherwise open a dialog that the capture would put in the
// image (SCR-044), or whose modal loop would keep a selection's Escape from
// reaching it (SCR-052). A dialog asked for during a hold is refused with a
// beep and the answer a cancelled one gives, which for Confirm is the safe
// No; File Explorer waits for the hold to end. Holds nest, and release may
// run on any goroutine, more than once.
func HoldDialogs() (release func()) {
	holdMu.Lock()
	if dialogHolds == 0 {
		holdsEnded = make(chan struct{})
	}
	dialogHolds++
	holdMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			holdMu.Lock()
			defer holdMu.Unlock()
			dialogHolds--
			if dialogHolds == 0 {
				close(holdsEnded)
			}
		})
	}
}

// DialogsHeld reports whether a hold keeps the dialogs closed.
func DialogsHeld() bool {
	holdMu.Lock()
	defer holdMu.Unlock()
	return dialogHolds > 0
}

// waitWhileDialogsHeld returns once no hold keeps the dialogs closed. A
// capture's hold is released from its worker or the GUI thread, so the GUI
// thread must never wait here: it would wait for itself.
func waitWhileDialogsHeld() {
	for {
		holdMu.Lock()
		if dialogHolds == 0 {
			holdMu.Unlock()
			return
		}
		ended := holdsEnded
		holdMu.Unlock()
		<-ended
	}
}

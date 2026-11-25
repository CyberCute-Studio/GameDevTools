package ui

import (
	"sync"
	"sync/atomic"
)

// dialogHolds counts the holds that keep the application's dialogs closed.
var dialogHolds atomic.Int64

// HoldDialogs keeps every dialog this package opens — Confirm's question and
// the image and folder pickers — from opening until the returned release
// runs. A capture holds them from the moment it starts until it has read the
// screen, and a selection session for as long as it is on screen: the GUI
// thread goes on dispatching messages meanwhile, so a click already queued
// could otherwise open a dialog that the capture would put in the image
// (SCR-044), or whose modal loop would keep a selection's Escape from
// reaching it (SCR-052). A dialog asked for during a hold is refused with a
// beep and the answer a cancelled one gives, which for Confirm is the safe
// No. Holds nest, and release may run on any goroutine, more than once.
func HoldDialogs() (release func()) {
	dialogHolds.Add(1)
	var once sync.Once
	return func() { once.Do(func() { dialogHolds.Add(-1) }) }
}

// DialogsHeld reports whether a hold keeps the dialogs closed.
func DialogsHeld() bool {
	return dialogHolds.Load() > 0
}

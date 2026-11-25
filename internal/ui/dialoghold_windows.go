//go:build windows

package ui

var procMessageBeep = user32.NewProc("MessageBeep")

// refusalBeep is how a held dialog refuses; tests replace it.
var refusalBeep = func() { procMessageBeep.Call(0) }

// dialogRefused reports whether a hold keeps a dialog from opening, beeping
// when it does: the window the dialog would belong to is hidden for a capture
// or about to be, so a beep is all the user can notice.
func dialogRefused() bool {
	if !DialogsHeld() {
		return false
	}
	refusalBeep()
	return true
}

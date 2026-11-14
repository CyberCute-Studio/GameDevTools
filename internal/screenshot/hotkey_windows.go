//go:build windows

package screenshot

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	errorHotkeyAlreadyRegistered = windows.Errno(0x0581)
)

var (
	hotkeyUser32         = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = hotkeyUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey = hotkeyUser32.NewProc("UnregisterHotKey")
)

// Hotkey owns one RegisterHotKey registration on the shell window. Every
// method runs on the GUI thread. Apply replaces the registered combination
// by unregistering the old one first; a failed attempt — such as a conflict
// with another application — restores the previous registration, so the old
// shortcut keeps working while the user chooses a replacement (SCR-003,
// AC-S11).
type Hotkey struct {
	window     uintptr
	id         int32
	registered bool
	current    Shortcut
}

func newHotkey(window uintptr, id int32) *Hotkey {
	return &Hotkey{window: window, id: id}
}

// Apply registers shortcut, replacing any combination currently registered
// on this hotkey. Re-applying the active combination is a no-op. Windows
// does not free the previous combination when the same identifier is
// registered again, so the old one is unregistered first; if the new
// registration fails — such as a conflict with another application — the
// previous combination is put back so the working shortcut keeps working
// while the user chooses a replacement (SCR-003, AC-S11).
func (h *Hotkey) Apply(shortcut Shortcut) error {
	if h.registered && h.current == shortcut {
		return nil
	}
	previous := h.current
	wasRegistered := h.registered
	if wasRegistered {
		procUnregisterHotKey.Call(h.window, uintptr(h.id))
		h.registered = false
	}
	r, _, err := procRegisterHotKey.Call(h.window, uintptr(h.id), uintptr(modifierFlags(shortcut)|modNoRepeat), uintptr(shortcut.Key))
	if r == 0 {
		if wasRegistered {
			restored, _, _ := procRegisterHotKey.Call(h.window, uintptr(h.id), uintptr(modifierFlags(previous)|modNoRepeat), uintptr(previous.Key))
			if restored != 0 {
				h.registered = true
			}
		}
		if errors.Is(err, errorHotkeyAlreadyRegistered) {
			return shortcutConflictError(shortcut)
		}
		if err == windows.ERROR_SUCCESS {
			return fmt.Errorf(ui.ShortcutRegisterFailed, shortcut, "RegisterHotKey failed")
		}
		return fmt.Errorf(ui.ShortcutRegisterFailed, shortcut, err)
	}
	h.registered = true
	h.current = shortcut
	return nil
}

// Suspend unregisters the active combination, for example while the user
// edits the shortcut field so pressing it cannot start a capture mid-edit.
func (h *Hotkey) Suspend() {
	if !h.registered {
		return
	}
	procUnregisterHotKey.Call(h.window, uintptr(h.id))
	h.registered = false
}

// Release unregisters the shortcut for good when the window goes away
// (SCR-006): no global hotkey may outlive the application (AC-A05).
func (h *Hotkey) Release() {
	h.Suspend()
}

// Registered reports whether a combination is currently registered.
func (h *Hotkey) Registered() bool {
	return h.registered
}

// modifierFlags maps the shortcut's modifiers to the RegisterHotKey MOD_*
// flags.
func modifierFlags(shortcut Shortcut) uint32 {
	flags := uint32(0)
	if shortcut.Alt {
		flags |= modAlt
	}
	if shortcut.Ctrl {
		flags |= modControl
	}
	if shortcut.Shift {
		flags |= modShift
	}
	if shortcut.Win {
		flags |= modWin
	}
	return flags
}

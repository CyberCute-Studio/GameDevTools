//go:build windows

package screenshot

import (
	"errors"
	"testing"
)

// The test combinations use F24 with every modifier so no real application
// or Windows shortcut plausibly owns them; every test unregisters what it
// registers so the machine's global hotkey state is left untouched.
var (
	hotkeyTestComboA = Shortcut{Ctrl: true, Alt: true, Shift: true, Win: true, Key: 0x87, Name: "F24"}
	hotkeyTestComboB = Shortcut{Ctrl: true, Alt: true, Shift: true, Win: true, Key: 0x86, Name: "F23"}
)

func TestHotkeyApplyRegisterReplaceAndRelease(t *testing.T) {
	parent := newViewTestParent(t)
	hotkey := newHotkey(uintptr(parent), 99)

	if hotkey.Registered() {
		t.Fatal("hotkey starts registered")
	}
	if err := hotkey.Apply(hotkeyTestComboA); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if !hotkey.Registered() {
		t.Fatal("hotkey not registered after Apply")
	}
	// Re-applying the active combination is a no-op, not a re-registration.
	if err := hotkey.Apply(hotkeyTestComboA); err != nil {
		t.Fatalf("re-Apply() = %v", err)
	}
	// Applying a new combination replaces the old one, freeing it for
	// another window.
	if err := hotkey.Apply(hotkeyTestComboB); err != nil {
		t.Fatalf("replace Apply() = %v", err)
	}
	other := newViewTestParent(t)
	probe := newHotkey(uintptr(other), 98)
	if err := probe.Apply(hotkeyTestComboA); err != nil {
		t.Fatalf("replaced combination was not freed: %v", err)
	}
	probe.Release()

	hotkey.Suspend()
	if hotkey.Registered() {
		t.Fatal("hotkey stayed registered after Suspend")
	}
	if err := probe.Apply(hotkeyTestComboB); err != nil {
		t.Fatalf("suspended combination was not freed: %v", err)
	}
	probe.Release()

	// Release is the exit path (SCR-006); it must be idempotent so destroy
	// ordering never matters.
	if err := hotkey.Apply(hotkeyTestComboA); err != nil {
		t.Fatalf("re-Apply after Suspend() = %v", err)
	}
	hotkey.Release()
	hotkey.Release()
	if hotkey.Registered() {
		t.Fatal("hotkey stayed registered after Release")
	}
	if err := probe.Apply(hotkeyTestComboA); err != nil {
		t.Fatalf("released combination was not freed: %v", err)
	}
	probe.Release()
}

// TestHotkeyConflictKeepsPreviousRegistration is the AC-S11 mechanism: a
// conflicting registration attempt is reported with ErrShortcutConflict and
// leaves the previously registered combination active, so the working
// shortcut keeps working while the user chooses another.
func TestHotkeyConflictKeepsPreviousRegistration(t *testing.T) {
	holder := newViewTestParent(t)
	keeper := newHotkey(uintptr(holder), 97)
	if err := keeper.Apply(hotkeyTestComboB); err != nil {
		t.Fatalf("holder Apply() = %v", err)
	}
	t.Cleanup(keeper.Release)

	parent := newViewTestParent(t)
	hotkey := newHotkey(uintptr(parent), 96)
	if err := hotkey.Apply(hotkeyTestComboA); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	t.Cleanup(hotkey.Release)

	err := hotkey.Apply(hotkeyTestComboB)
	if !errors.Is(err, ErrShortcutConflict) {
		t.Fatalf("conflicting Apply() error = %v, want ErrShortcutConflict", err)
	}
	if !hotkey.Registered() {
		t.Fatal("conflicting Apply dropped the previous registration")
	}
	// The previous combination is still owned by this hotkey: no other
	// window can take it.
	other := newViewTestParent(t)
	probe := newHotkey(uintptr(other), 95)
	if probeErr := probe.Apply(hotkeyTestComboA); !errors.Is(probeErr, ErrShortcutConflict) {
		t.Fatalf("previous combination error = %v, want it still owned", probeErr)
	}
}

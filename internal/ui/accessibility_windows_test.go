//go:build windows

package ui

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

var (
	procIsDialogMessageW = user32.NewProc("IsDialogMessageW")
	procShowWindowTest   = user32.NewProc("ShowWindow")
)

const (
	wmKeyDown          = 0x0100
	vkTab              = 0x09
	vkDown             = 0x28
	swShowNoActivate   = 4
	liveSettingOff     = 0
	liveSettingPolited = liveSettingPolite
)

func tabStop(hwnd windows.Handle) bool {
	style, _, _ := procGetWindowLongPtrW.Call(uintptr(hwnd), gwlStyle)
	return style&wsTabStop != 0
}

// pressKey hands a key press to the dialog manager as the shell's message
// loop does, from the control that has the focus.
func pressKey(parent windows.Handle, key uintptr) {
	focused, _, _ := procGetFocus.Call()
	message := struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		pt      [2]int32
		private uint32
	}{hwnd: focused, message: wmKeyDown, wParam: key}
	procIsDialogMessageW.Call(uintptr(parent), uintptr(unsafe.Pointer(&message)))
}

// TestRadioButtonsAreTabStopsOnlyWhileChecked pins the radio group's
// keyboard model (UX-006): only the checked button is a tab stop, so Tab
// enters the group at the selection without focusing — and so clicking — an
// unchecked button, and the arrow keys move within the group.
func TestRadioButtonsAreTabStopsOnlyWhileChecked(t *testing.T) {
	parent := newTestParent(t)
	before, err := NewButton(parent, "Before", ButtonOptions{ID: 80})
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewButton(parent, "First", ButtonOptions{ID: 81, Radio: true, Group: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewButton(parent, "Second", ButtonOptions{ID: 82, Radio: true})
	if err != nil {
		t.Fatal(err)
	}
	third, err := NewButton(parent, "Third", ButtonOptions{ID: 83, Radio: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewButton(parent, "After", ButtonOptions{ID: 84, Group: true})
	if err != nil {
		t.Fatal(err)
	}
	procShowWindowTest.Call(uintptr(parent), swShowNoActivate)
	radios := []*Button{first, second, third}
	stops := func() []bool {
		return []bool{tabStop(first.Handle()), tabStop(second.Handle()), tabStop(third.Handle())}
	}
	if got := stops(); !got[0] || got[1] || got[2] {
		t.Fatalf("tab stops before any check = %v, want only the group's first button", got)
	}
	for i, radio := range radios {
		radio.SetChecked(i == 1)
	}
	if got := stops(); got[0] || !got[1] || got[2] {
		t.Fatalf("tab stops with the second checked = %v, want only the checked button", got)
	}
	if !tabStop(before.Handle()) || !tabStop(after.Handle()) {
		t.Fatal("SetChecked changed a push button's tab stop")
	}

	before.Focus()
	pressKey(parent, vkTab)
	if !second.HasFocus() {
		t.Fatal("Tab did not enter the radio group at its checked button")
	}
	pressKey(parent, vkTab)
	if !after.HasFocus() {
		t.Fatal("Tab did not leave the radio group for the next control")
	}
	second.Focus()
	pressKey(parent, vkDown)
	if !third.HasFocus() {
		t.Fatal("the Down arrow did not move within the radio group")
	}
}

// TestLiveLabelsAreLiveRegions pins that the labels reporting results —
// status areas, validation messages, and NewLiveLabel's — are polite UI
// Automation live regions, which a screen reader announces, and that other
// labels are not.
func TestLiveLabelsAreLiveRegions(t *testing.T) {
	parent := newTestParent(t)
	live, err := NewLiveLabel(parent, "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := NewLabel(parent, "Plain")
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewStatusArea(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(status.Destroy)
	text, err := NewTextInput(parent, "&Quality", 90)
	if err != nil {
		t.Fatal(err)
	}
	dimension, err := NewDimensionInput(parent, "&Width", 91)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name string
		hwnd windows.Handle
		want int32
	}{
		{"NewLiveLabel", live.Handle(), liveSettingPolited},
		{"status area", status.Handle(), liveSettingPolited},
		{"text input's validation", text.Error.Handle(), liveSettingPolited},
		{"dimension input's validation", dimension.Error.Handle(), liveSettingPolited},
		{"NewLabel", plain.Handle(), liveSettingOff},
		{"text input's field label", text.Label.Handle(), liveSettingOff},
	} {
		got, err := uiatest.LiveSetting(check.hwnd)
		if err != nil {
			t.Fatalf("%s: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("%s live setting = %d, want %d", check.name, got, check.want)
		}
	}
}

// TestLiveLabelsAnnounceWhatChanged pins when a screen reader is asked to
// read a label: a live label's changed text, and not the same text again or
// an emptied label; a status area's every notice, an identical one
// included, as an identical result is still a new one, but not a quiet
// Update or a Clear; and never a plain label.
func TestLiveLabelsAnnounceWhatChanged(t *testing.T) {
	parent := newTestParent(t)
	live, err := NewLiveLabel(parent, "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := NewLabel(parent, "")
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewStatusArea(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(status.Destroy)
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)

	live.SetText("Validation error: Width is required.")
	live.SetText("Validation error: Width is required.")
	live.SetText("")
	plain.SetText("Not a live region")
	if got := watch.Count(live.Handle()); got != 1 {
		t.Fatalf("live label announcements after one change, a repeat, and clearing = %d, want 1", got)
	}
	live.SetText("Validation error: Width is too large.")
	live.Announce()
	if got := watch.Count(live.Handle()); got != 3 {
		t.Fatalf("live label announcements after another change and Announce = %d, want 3", got)
	}
	if got := watch.Count(plain.Handle()); got != 0 {
		t.Fatalf("a plain label raised %d announcements", got)
	}

	notice := Notice{Kind: NoticeSuccess, Message: "Display captured."}
	if err := status.Show(notice, 0); err != nil {
		t.Fatal(err)
	}
	if err := status.Show(notice, time.Hour); err != nil {
		t.Fatal(err)
	}
	status.Update(Notice{Kind: NoticeInfo, Message: "Resizing: 1 of 9 image(s) done."})
	status.Update(Notice{Kind: NoticeInfo, Message: "Resizing: 2 of 9 image(s) done."})
	status.Clear()
	if err := status.Show(Notice{}, 0); err != nil {
		t.Fatal(err)
	}
	if got := watch.Count(status.Handle()); got != 2 {
		t.Fatalf("status area announcements after two notices, two updates, a clear, and an empty notice = %d, want 2", got)
	}
	if got := status.Text(); got != "" {
		t.Fatalf("status text = %q, want the empty notice", got)
	}
}

//go:build windows

package ui

import (
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const wsOverlappedWindow = 0x00cf0000

var procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")

func TestDimensionInputPresentsValidation(t *testing.T) {
	parent := newTestParent(t)
	input, err := NewDimensionInput(parent, "Width", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := input.Label.Text(); got != "Width (px)" {
		t.Fatalf("label = %q", got)
	}
	style, _, _ := procGetWindowLongPtrW.Call(uintptr(input.Edit.Handle()), ^uintptr(15))
	if style&wsTabStop == 0 {
		t.Fatal("dimension edit is not keyboard focusable")
	}
	input.Edit.SetText("0")
	if !input.HandleCommand(42, enChange) {
		t.Fatal("edit change was not handled")
	}
	if got := input.Error.Text(); !strings.HasPrefix(got, "Validation error: Width must be greater than 0 pixels.") {
		t.Fatalf("validation text = %q", got)
	}
	input.Edit.SetText("640")
	if !input.HandleCommand(42, enKillFocus) {
		t.Fatal("edit focus loss was not handled")
	}
	if got := input.Error.Text(); got != "" {
		t.Fatalf("validation text after valid input = %q", got)
	}
	value, err := input.Validate()
	if err != nil || value != 640 {
		t.Fatalf("Validate() = %d, %v; want 640", value, err)
	}
	if input.HandleCommand(41, enChange) {
		t.Fatal("another control's notification was handled")
	}
}

func TestLabelsControlMnemonicHandling(t *testing.T) {
	parent := newTestParent(t)
	input, err := NewDimensionInput(parent, "&Width", 42)
	if err != nil {
		t.Fatal(err)
	}
	labelStyle, _, _ := procGetWindowLongPtrW.Call(uintptr(input.Label.Handle()), ^uintptr(15))
	if labelStyle&ssNoPrefix != 0 {
		t.Fatal("dimension label disabled its keyboard mnemonic")
	}
	status, err := NewStatusArea(parent)
	if err != nil {
		t.Fatal(err)
	}
	statusStyle, _, _ := procGetWindowLongPtrW.Call(uintptr(status.Handle()), ^uintptr(15))
	if statusStyle&ssNoPrefix == 0 {
		t.Fatal("status label interprets ampersands as mnemonics")
	}
	if err := status.Show(Notice{Message: `Saved C:\Images & Captures`}, 0); err != nil {
		t.Fatal(err)
	}
	if got := status.Text(); got != `Information: Saved C:\Images & Captures` {
		t.Fatalf("status text = %q", got)
	}
}

func TestSharedButtonOptions(t *testing.T) {
	parent := newTestParent(t)
	button, err := NewButton(parent, "Delete", ButtonOptions{Primary: true, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	style, _, _ := procGetWindowLongPtrW.Call(uintptr(button.Handle()), ^uintptr(15))
	if style&wsTabStop == 0 || style&bsDefPushButton == 0 {
		t.Fatalf("button style = %#x", style)
	}
	button.Enable(true)
	button.SetText("Remove")
	if got := button.Text(); got != "Remove" {
		t.Fatalf("button text = %q", got)
	}
}

func TestStatusAreaPresentsAndClearsNotice(t *testing.T) {
	parent := newTestParent(t)
	area, err := NewStatusArea(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := area.Show(Notice{Kind: NoticeWarning, Message: "Clipboard unavailable"}, 0); err != nil {
		t.Fatal(err)
	}
	if got := area.Text(); got != "Warning: Clipboard unavailable" {
		t.Fatalf("status text = %q", got)
	}
	area.Clear()
	if got := area.Text(); got != "" {
		t.Fatalf("cleared status text = %q", got)
	}
	if err := area.Show(Notice{Kind: NoticeSuccess, Message: "Saved"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if area.timer == 0 {
		t.Fatal("timed notice did not create a timer")
	}
	expiredTimer := area.timer
	if err := area.Show(Notice{Kind: NoticeSuccess, Message: "Still visible"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if area.timer == expiredTimer {
		t.Fatal("replacement notice reused the expired timer ID")
	}
	if area.HandleTimer(expiredTimer) {
		t.Fatal("stale timer cleared the replacement notice")
	}
	if got := area.Text(); got != "Success: Still visible" {
		t.Fatalf("replacement status text = %q", got)
	}
	if !area.HandleTimer(area.timer) {
		t.Fatal("notice timer was not handled")
	}
	if got := area.Text(); got != "" || area.timer != 0 {
		t.Fatalf("expired notice = %q, timer = %d", got, area.timer)
	}
	if area.HandleTimer(1234) {
		t.Fatal("unexpected timer was handled")
	}
	if err := area.Show(Notice{}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if area.timer != 0 {
		t.Fatal("empty notice created a timer")
	}
}

// TestStatusAreaRevisionIdentifiesTheNotice pins that every replacement —
// even by identical text, a clear, or an expiring timer — changes the
// revision, so a caller can tell whether its own notice is still shown.
func TestStatusAreaRevisionIdentifiesTheNotice(t *testing.T) {
	area, err := NewStatusArea(newTestParent(t))
	if err != nil {
		t.Fatal(err)
	}
	notice := Notice{Kind: NoticeInfo, Message: "Checking"}
	_ = area.Show(notice, 0)
	shown := area.Revision()
	if area.Revision() != shown {
		t.Fatal("the revision changed without a new notice")
	}
	_ = area.Show(notice, 0)
	if area.Revision() == shown {
		t.Fatal("an identical notice kept the revision")
	}
	shown = area.Revision()
	area.Clear()
	if area.Revision() == shown {
		t.Fatal("clearing kept the revision")
	}
	if err := area.Show(notice, time.Hour); err != nil {
		t.Fatal(err)
	}
	shown = area.Revision()
	area.HandleTimer(area.timer)
	if area.Revision() == shown {
		t.Fatal("an expired notice kept the revision")
	}
}

// TestStatusAreaClearShownClearsOnlyARecordedNotice pins ClearShown: it
// clears the notice only while it is one whose revision was recorded, keeps
// a notice shown since, and takes a revision of 0 for no notice at all.
func TestStatusAreaClearShownClearsOnlyARecordedNotice(t *testing.T) {
	area, err := NewStatusArea(newTestParent(t))
	if err != nil {
		t.Fatal(err)
	}
	recorded := Notice{Kind: NoticeSuccess, Message: "Folder saved."}
	_ = area.Show(recorded, 0)
	revision := area.Revision()
	later := Notice{Kind: NoticeInfo, Message: "Display captured."}
	_ = area.Show(later, 0)
	area.ClearShown(0, revision)
	if got := area.Text(); got != later.Text() {
		t.Fatalf("status after ClearShown of an earlier notice = %q, want the later one kept", got)
	}
	area.ClearShown(0)
	if got := area.Text(); got != later.Text() {
		t.Fatalf("status after ClearShown of no notice = %q, want it kept", got)
	}
	area.ClearShown(revision, area.Revision())
	if got := area.Text(); got != "" {
		t.Fatalf("status after ClearShown of the notice shown = %q, want it cleared", got)
	}
}

func TestStatusAreaReportsTimerFailure(t *testing.T) {
	area := &StatusArea{Label: &Label{Control: Control{hwnd: 1}}, parent: 1}
	if err := area.Show(Notice{Message: "Saved"}, time.Second); err == nil {
		t.Fatal("Show() error = nil for an invalid parent window")
	}
	if area.timer != 0 {
		t.Fatalf("failed timer = %d, want 0", area.timer)
	}
}

func newTestParent(t *testing.T) windows.Handle {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(func() {
		runtime.UnlockOSThread()
	})
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16("STATIC"))),
		uintptr(unsafe.Pointer(utf16("GameDevTools UI test"))),
		wsOverlappedWindow,
		0, 0, 400, 300,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW(test parent): %v", err)
	}
	t.Cleanup(func() {
		procDestroyWindow.Call(hwnd)
	})
	return windows.Handle(hwnd)
}

// TestComboBoxReadsBackItsItems pins that a combo box reports how many items
// its list holds and the text of each, and "" for an index out of range.
// Every item's text is non-empty, so an item that could not be read, which
// reads as "", cannot pass for one.
func TestComboBoxReadsBackItsItems(t *testing.T) {
	parent := newTestParent(t)
	combo, err := NewComboBox(parent, 76)
	if err != nil {
		t.Fatal(err)
	}
	if got := combo.Count(); got != 0 {
		t.Fatalf("empty combo box count = %d", got)
	}
	want := []string{"HD — 1280 × 720 px (built-in)", "Capture 640 — 640 × 480 px", "Écran 📷 — 1 × 1 px"}
	for _, text := range want {
		combo.Add(text)
	}
	if got := combo.Count(); got != len(want) {
		t.Fatalf("count = %d, want %d", got, len(want))
	}
	for index, text := range want {
		if got := combo.ItemText(index); got != text {
			t.Fatalf("item %d = %q, want %q", index, got, text)
		}
	}
	for _, index := range []int{-1, len(want)} {
		if got := combo.ItemText(index); got != "" {
			t.Fatalf("item %d, out of range, = %q", index, got)
		}
	}
	combo.Reset()
	if got := combo.Count(); got != 0 {
		t.Fatalf("count after reset = %d", got)
	}
}

// TestListBoxSetItemTextKeepsSelectionAndScroll pins that rewriting one row
// in place — as background status updates do — preserves the selection of
// that row and of the others, the extended-selection anchor, the caret, and
// the scroll position, including when the rewritten row is the anchor or the
// caret itself.
func TestListBoxSetItemTextKeepsSelectionAndScroll(t *testing.T) {
	parent := newTestParent(t)
	list, err := NewListBox(parent, 77)
	if err != nil {
		t.Fatal(err)
	}
	list.Move(0, 0, 200, 60)
	for index := 0; index < 20; index++ {
		list.Add("row")
	}
	list.SetSelected(3, true)
	list.SetSelected(5, true)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetAnchorIndex, 3, 0)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetCaretIndex, 5, 0)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetTopIndex, 2, 0)

	list.SetItemText(3, "selected row updated")
	list.SetItemText(4, "unselected row updated")
	list.SetItemText(5, "caret row updated")
	list.SetItemText(99, "ignored")

	if got := list.ItemText(3); got != "selected row updated" {
		t.Fatalf("row 3 = %q", got)
	}
	if got := list.ItemText(4); got != "unselected row updated" {
		t.Fatalf("row 4 = %q", got)
	}
	if list.Count() != 20 {
		t.Fatalf("count = %d, want 20", list.Count())
	}
	if got := list.SelectedIndices(); len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Fatalf("selection = %v, want [3 5]", got)
	}
	if anchor, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetAnchorIndex, 0, 0); anchor != 3 {
		t.Fatalf("anchor = %d, want 3", anchor)
	}
	if caret, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetCaretIndex, 0, 0); caret != 5 {
		t.Fatalf("caret = %d, want 5", caret)
	}
	if top, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetTopIndex, 0, 0); top != 2 {
		t.Fatalf("top index = %d, want 2", top)
	}
}

var (
	procSetWinEventHook  = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent   = user32.NewProc("UnhookWinEvent")
	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
)

const (
	eventObjectFocus = 0x8005
	pmRemove         = 1
)

// focusEvents records the accessibility focus events a window raises — what
// a screen reader announces — through an out-of-context hook on the calling
// thread. take pumps the thread's messages, which delivers the queued
// events, and returns the focused item of each event since the last take.
func focusEvents(t *testing.T, window windows.Handle) (take func() []int32) {
	t.Helper()
	var items []int32
	callback := windows.NewCallback(func(_, _, hwnd, _, child, _, _ uintptr) uintptr {
		if windows.Handle(hwnd) == window {
			items = append(items, int32(child))
		}
		return 0
	})
	hook, _, err := procSetWinEventHook.Call(eventObjectFocus, eventObjectFocus, 0, callback, uintptr(windows.GetCurrentProcessId()), uintptr(windows.GetCurrentThreadId()), 0)
	if hook == 0 {
		t.Fatalf("SetWinEventHook: %v", err)
	}
	t.Cleanup(func() { procUnhookWinEvent.Call(hook) })
	return func() []int32 {
		var message [64]byte
		for {
			if available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message[0])), 0, 0, 0, pmRemove); available == 0 {
				break
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message[0])))
		}
		taken := items
		items = nil
		return taken
	}
}

// TestListBoxSetItemTextRaisesNoStrayFocusEvents pins that background row
// updates leave a screen reader alone: rewriting a row other than the
// focused one raises no accessibility focus event, so the focused row is not
// announced again, and rewriting the focused row raises exactly one, for its
// new text. Setting the caret raises the event even when the caret does not
// move, so the caret is restored only when the reinsertion moved it.
func TestListBoxSetItemTextRaisesNoStrayFocusEvents(t *testing.T) {
	parent := newTestParent(t)
	procShowWindow.Call(uintptr(parent), 5)
	list, err := NewListBox(parent, 78)
	if err != nil {
		t.Fatal(err)
	}
	list.Move(0, 0, 200, 120)
	for index := 0; index < 20; index++ {
		list.Add("row")
	}
	list.Focus()
	list.SetSelected(3, true)
	list.SetSelected(5, true)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetAnchorIndex, 3, 0)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetCaretIndex, 3, 0)
	take := focusEvents(t, list.Handle())
	take()

	// Child identifiers are 1-based item indexes.
	procSendMessageW.Call(uintptr(list.Handle()), lbSetCaretIndex, 4, 0)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetCaretIndex, 3, 0)
	if got := take(); len(got) != 2 || got[1] != 4 {
		t.Skipf("this session does not deliver list-box focus events (got %v)", got)
	}
	for _, index := range []int{1, 5, 8} {
		list.SetItemText(index, "updated")
		if got := take(); len(got) != 0 {
			t.Fatalf("rewriting row %d raised focus events for items %v, want none", index, got)
		}
	}
	list.SetItemText(3, "focused row updated")
	if got := take(); len(got) != 1 || got[0] != 4 {
		t.Fatalf("rewriting the focused row raised focus events for items %v, want one for item 4", got)
	}
	if got := list.SelectedIndices(); len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Fatalf("selection = %v, want [3 5]", got)
	}
	if caret, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetCaretIndex, 0, 0); caret != 3 {
		t.Fatalf("caret = %d, want 3", caret)
	}
	if anchor, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetAnchorIndex, 0, 0); anchor != 3 {
		t.Fatalf("anchor = %d, want 3", anchor)
	}
}

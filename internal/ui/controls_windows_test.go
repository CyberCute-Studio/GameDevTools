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

func TestStatusAreaReportsTimerFailure(t *testing.T) {
	area := &StatusArea{Label: &Label{Control{hwnd: 1}}, parent: 1}
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

// TestListBoxSetItemTextKeepsSelectionAndScroll pins that rewriting one row
// in place — as background status updates do — preserves the selection of
// that row and of the others, the caret, and the scroll position.
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
	procSendMessageW.Call(uintptr(list.Handle()), lbSetCaretIndex, 5, 0)
	procSendMessageW.Call(uintptr(list.Handle()), lbSetTopIndex, 2, 0)

	list.SetItemText(3, "selected row updated")
	list.SetItemText(4, "unselected row updated")
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
	if caret, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetCaretIndex, 0, 0); caret != 5 {
		t.Fatalf("caret = %d, want 5", caret)
	}
	if top, _, _ := procSendMessageW.Call(uintptr(list.Handle()), lbGetTopIndex, 0, 0); top != 2 {
		t.Fatalf("top index = %d, want 2", top)
	}
}

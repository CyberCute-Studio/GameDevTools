//go:build windows

package ui

import (
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
	area.Show(Notice{Kind: NoticeWarning, Message: "Clipboard unavailable"}, 0)
	if got := area.Text(); got != "Warning: Clipboard unavailable" {
		t.Fatalf("status text = %q", got)
	}
	area.Clear()
	if got := area.Text(); got != "" {
		t.Fatalf("cleared status text = %q", got)
	}
	area.Show(Notice{Kind: NoticeSuccess, Message: "Saved"}, time.Second)
	if area.timer == 0 {
		t.Fatal("timed notice did not create a timer")
	}
	expiredTimer := area.timer
	area.Show(Notice{Kind: NoticeSuccess, Message: "Still visible"}, time.Hour)
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
}

func newTestParent(t *testing.T) windows.Handle {
	t.Helper()
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

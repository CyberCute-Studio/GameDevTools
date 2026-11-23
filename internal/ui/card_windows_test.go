//go:build windows

package ui

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCardTestSetActiveWindow     = user32.NewProc("SetActiveWindow")
	procCardTestGetActiveWindow     = user32.NewProc("GetActiveWindow")
	procCardTestIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procCardTestGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procCardTestGetWindowLongW      = user32.NewProc("GetWindowLongW")
)

func cardVisible(hwnd windows.Handle) bool {
	visible, _, _ := procCardTestIsWindowVisible.Call(uintptr(hwnd))
	return visible != 0
}

// newOffScreenCard places the card far from every display, so that a capture
// test running beside this one cannot see it.
func newOffScreenCard(t *testing.T, open func(string)) *ResultCard {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	card := NewResultCard(func() CardPlacement {
		return CardPlacement{Left: -22000, Top: -22000, Right: -20000, Bottom: -20000, DPI: 96}
	}, open)
	t.Cleanup(card.Destroy)
	return card
}

func TestResultCardAppearsWithoutTakingTheForeground(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "saving failed"}, "")
	if card.hwnd == 0 || !cardVisible(card.hwnd) {
		t.Fatal("the card did not appear")
	}
	// Only the card is checked: other packages' tests run beside this one
	// and move the foreground between their own windows.
	if after, _, _ := procCardTestGetForegroundWindow.Call(); windows.Handle(after) == card.hwnd {
		t.Fatal("the card took the foreground")
	}
	const gwlExStyle = ^uintptr(19) // -20 as a uintptr
	style, _, _ := procCardTestGetWindowLongW.Call(uintptr(card.hwnd), gwlExStyle)
	for name, want := range map[string]uintptr{"topmost": cardWSExTopmost, "tool window": cardWSExToolWindow, "no-activate": cardWSExNoActivate} {
		if style&want == 0 {
			t.Errorf("the card is not %s (style %#x)", name, style)
		}
	}
	if result := cardWindowProc(card.hwnd, cardWMMouseActivate, 0, 0); result != cardMANoActivate {
		t.Fatalf("WM_MOUSEACTIVATE = %d, want MA_NOACTIVATE", result)
	}
	if got := card.message.Text(); got != "Error: saving failed" {
		t.Fatalf("message = %q", got)
	}
}

func TestResultCardHidesForACaptureAndKeepsItsResults(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "first failed"}, "")
	card.Suspend()
	if cardVisible(card.hwnd) {
		t.Fatal("a suspended card stayed on screen")
	}
	card.Report(Notice{Kind: NoticeSuccess, Message: "second saved"}, `C:\Shots\two.png`)
	if cardVisible(card.hwnd) {
		t.Fatal("a result showed the card during a capture")
	}
	card.Resume()
	if !cardVisible(card.hwnd) || !strings.Contains(card.message.Text(), "second saved") {
		t.Fatalf("after Resume: visible %v, message %q", cardVisible(card.hwnd), card.message.Text())
	}
	if !strings.Contains(card.count.Text(), "1 earlier") {
		t.Fatalf("count = %q, want the failure waiting", card.count.Text())
	}

	// The success leaves by itself and the failure comes back.
	cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
	if !cardVisible(card.hwnd) || !strings.Contains(card.message.Text(), "first failed") {
		t.Fatalf("after the timer: visible %v, message %q", cardVisible(card.hwnd), card.message.Text())
	}
	cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
	if !cardVisible(card.hwnd) {
		t.Fatal("a failure left by itself")
	}
}

func TestResultCardButtons(t *testing.T) {
	var opened []string
	card := newOffScreenCard(t, func(path string) { opened = append(opened, path) })
	card.Report(Notice{Kind: NoticeWarning, Message: "saved, clipboard failed"}, `C:\Shots\one.png`)
	card.Report(Notice{Kind: NoticeError, Message: "no file"}, "")

	const wsVisibleStyle = 0x10000000
	const gwlStyle = ^uintptr(15) // -16 as a uintptr
	style, _, _ := procCardTestGetWindowLongW.Call(uintptr(card.openBtn.Handle()), gwlStyle)
	if style&wsVisibleStyle != 0 {
		t.Fatal("Open Folder is offered for a result with no file")
	}
	cardWindowProc(card.hwnd, cardWMCommand, cardDismissCommand, 0)
	style, _, _ = procCardTestGetWindowLongW.Call(uintptr(card.openBtn.Handle()), gwlStyle)
	if style&wsVisibleStyle == 0 {
		t.Fatal("Open Folder is not offered for a saved file")
	}
	cardWindowProc(card.hwnd, cardWMCommand, cardOpenCommand, 0)
	if len(opened) != 1 || opened[0] != `C:\Shots\one.png` {
		t.Fatalf("opened = %v", opened)
	}
	// A second quick click on Dismiss arrives as a double click.
	cardWindowProc(card.hwnd, cardWMCommand, cardDismissCommand|cardBNDoubleClicked<<16, 0)
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed after its last result was dismissed")
	}
}

// TestResultCardPressLeavesActivationAndFocusAlone presses the real buttons
// with another window of this thread active, as the main window is when the
// card shows over another tool: a standard button would take the focus, and
// with it the activation.
func TestResultCardPressLeavesActivationAndFocusAlone(t *testing.T) {
	var opened []string
	card := newOffScreenCard(t, func(path string) { opened = append(opened, path) })
	card.Report(Notice{Kind: NoticeWarning, Message: "saved, clipboard failed"}, `C:\Shots\one.png`)
	card.Report(Notice{Kind: NoticeError, Message: "second problem"}, `C:\Shots\two.png`)

	other, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16("STATIC"))), 0, cardWSPopup,
		uintptr(uint32(0xffffa880)), uintptr(uint32(0xffffa880)), 50, 50, 0, 0, 0, 0)
	if other == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(other) })
	procCardTestSetActiveWindow.Call(other)
	active, _, _ := procCardTestGetActiveWindow.Call()
	focus, _, _ := procGetFocus.Call()

	press := func(button *Button, up uintptr) {
		procSendMessageW.Call(uintptr(button.hwnd), cardWMLButtonDown, 1, 0x00050005)
		procSendMessageW.Call(uintptr(button.hwnd), cardWMLButtonUp, 0, up)
	}
	checkFocus := func(step string) {
		t.Helper()
		if now, _, _ := procCardTestGetActiveWindow.Call(); now != active {
			t.Fatalf("%s: the active window changed from %#x to %#x", step, active, now)
		}
		if now, _, _ := procGetFocus.Call(); now != focus {
			t.Fatalf("%s: the focus changed from %#x to %#x", step, focus, now)
		}
	}

	// A release off the button is no click.
	press(card.dismiss, 0x7fff7fff)
	checkFocus("release off Dismiss")
	if card.queue.waiting() != 1 {
		t.Fatal("a release off Dismiss dismissed the problem")
	}

	press(card.openBtn, 0x00050005)
	checkFocus("Open Folder")
	if len(opened) != 1 || opened[0] != `C:\Shots\two.png` {
		t.Fatalf("opened = %v", opened)
	}

	press(card.dismiss, 0x00050005)
	checkFocus("Dismiss")
	if card.queue.waiting() != 0 {
		t.Fatal("Dismiss did not dismiss the problem")
	}

	// The second click of a quick pair arrives as a double click.
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDblClk, 1, 0x00050005)
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonUp, 0, 0x00050005)
	checkFocus("double click")
	if cardVisible(card.hwnd) {
		t.Fatal("a quick second Dismiss was lost")
	}
}

func TestResultCardWithdrawsSettingProblems(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.ReportSetting(Notice{Kind: NoticeError, Message: "conflict"})
	if !cardVisible(card.hwnd) {
		t.Fatal("the setting problem did not appear")
	}
	card.WithdrawSettings()
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed after its only problem was withdrawn")
	}
}

func TestResultCardTakeHidesItAndHandsBackProblems(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")
	card.Report(Notice{Kind: NoticeError, Message: "two"}, "")
	entries := card.Take()
	if len(entries) != 2 || entries[0].Notice.Message != "one" {
		t.Fatalf("Take = %+v", entries)
	}
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed on screen after Take")
	}
}

func TestResultCardDoesNothingOnceDestroyed(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")
	card.Destroy()
	card.Report(Notice{Kind: NoticeError, Message: "two"}, "")
	card.Resume()
	card.Reposition()
	if card.hwnd != 0 {
		t.Fatal("a destroyed card made a new window")
	}
	card.Destroy()
}

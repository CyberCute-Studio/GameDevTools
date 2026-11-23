//go:build windows

package ui

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCardTestSetActiveWindow     = user32.NewProc("SetActiveWindow")
	procCardTestGetActiveWindow     = user32.NewProc("GetActiveWindow")
	procCardTestIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procCardTestGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procCardTestGetWindowLongW      = user32.NewProc("GetWindowLongW")
	procCardTestGetCapture          = user32.NewProc("GetCapture")
	procCardTestSetWinEventHook     = user32.NewProc("SetWinEventHook")
	procCardTestUnhookWinEvent      = user32.NewProc("UnhookWinEvent")
	procCardTestPeekMessageW        = user32.NewProc("PeekMessageW")
	procCardTestDispatchMessageW    = user32.NewProc("DispatchMessageW")
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

	press := func(button *Button) {
		procSendMessageW.Call(uintptr(button.hwnd), cardWMLButtonDown, 1, 0x00050005)
		procSendMessageW.Call(uintptr(button.hwnd), cardWMLButtonUp, 0, 0x00050005)
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

	// The pointer leaving the button ends the press, as the release may then
	// go to another application: a later release over it is no click.
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDown, 1, 0x00050005)
	if held, _, _ := procCardTestGetCapture.Call(); held != 0 {
		t.Fatalf("a press took the mouse capture for %#x", held)
	}
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMMouseLeave, 0, 0)
	if cardButtons[card.dismiss.hwnd].pressed {
		t.Fatal("the press outlived the pointer leaving the button")
	}
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonUp, 0, 0x00050005)
	checkFocus("release after leaving Dismiss")
	if card.queue.waiting() != 1 {
		t.Fatal("a release after the pointer left dismissed the problem")
	}

	press(card.openBtn)
	checkFocus("Open Folder")
	if len(opened) != 1 || opened[0] != `C:\Shots\two.png` {
		t.Fatalf("opened = %v", opened)
	}

	press(card.dismiss)
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

// TestResultCardPressEndsWhenTheResultChanges is a press on Dismiss while the
// card moves on: releasing it must not remove a problem the user never saw.
func TestResultCardPressEndsWhenTheResultChanges(t *testing.T) {
	const onButton = 0x00050005
	press := func(card *ResultCard) {
		procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDown, 1, onButton)
	}
	release := func(card *ResultCard) {
		procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonUp, 0, onButton)
	}

	t.Run("the success leaves during the press", func(t *testing.T) {
		card := newOffScreenCard(t, nil)
		card.Report(Notice{Kind: NoticeError, Message: "unread failure"}, "")
		card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
		press(card)
		cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
		release(card)
		if len(card.queue.problems) != 1 {
			t.Fatalf("problems = %+v, want the failure kept", card.queue.problems)
		}
	})
	t.Run("a capture hides the card during the press", func(t *testing.T) {
		card := newOffScreenCard(t, nil)
		card.Report(Notice{Kind: NoticeError, Message: "unread failure"}, "")
		press(card)
		card.Suspend()
		release(card)
		if len(card.queue.problems) != 1 {
			t.Fatalf("problems = %+v, want the failure kept", card.queue.problems)
		}
	})
	t.Run("the same result keeps its press", func(t *testing.T) {
		card := newOffScreenCard(t, nil)
		card.Report(Notice{Kind: NoticeError, Message: "failure"}, "")
		press(card)
		card.Reposition()
		release(card)
		if len(card.queue.problems) != 0 {
			t.Fatalf("problems = %+v, want the press to dismiss it", card.queue.problems)
		}
	})
}

// TestResultCardPressAsksToHearThePointerLeave runs the real leave tracking:
// the card is off every display, so the pointer is already off the button,
// and Windows reports that at once. A malformed request would report nothing
// and leave the press held.
func TestResultCardPressAsksToHearThePointerLeave(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "unread failure"}, "")
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDown, 1, 0x00050005)
	var message [64]byte
	for deadline := time.Now().Add(2 * time.Second); cardButtons[card.dismiss.hwnd].pressed && time.Now().Before(deadline); {
		if got, _, _ := procCardTestPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 1); got != 0 {
			procCardTestDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if cardButtons[card.dismiss.hwnd].pressed {
		t.Fatal("the press was still held with the pointer off the button")
	}
	if len(card.queue.problems) != 1 {
		t.Fatalf("problems = %+v, want the failure kept", card.queue.problems)
	}
}

// TestResultCardDropsASuccessForACapture is a selection started while a
// success shows and then cancelled: the old success must not come back.
func TestResultCardDropsASuccessForACapture(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	card.Suspend()
	card.Resume()
	if cardVisible(card.hwnd) {
		t.Fatalf("the earlier success came back: %q", card.message.Text())
	}
}

// TestResultCardMoveLeavesASuccessTimeRunning is a dock sending display changes
// every few seconds: each repositions the card, and a success must still
// leave. KillTimer reports whether a timer was running.
func TestResultCardMoveLeavesASuccessTimeRunning(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running == 0 {
		t.Fatal("a success started no timer")
	}
	card.Reposition()
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running != 0 {
		t.Fatal("moving the card restarted the success's time")
	}
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running == 0 {
		t.Fatal("a new identical success started no timer")
	}
}

// TestResultCardResumeAnnouncesOnlyAChange is a problem waiting through a
// capture with no result of its own: Resume must not read it out as new, and
// a success identical to an earlier one must still start its time.
func TestResultCardResumeAnnouncesOnlyAChange(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "waiting problem"}, "")
	announcements, settle := watchCardAnnouncements(t, card)

	card.Suspend()
	card.Resume()
	settle()
	if !cardVisible(card.hwnd) {
		t.Fatal("the problem did not come back")
	}
	if *announcements != 0 {
		t.Fatalf("an unchanged problem was announced %d time(s)", *announcements)
	}

	card.Suspend()
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	card.Resume()
	settle()
	if *announcements != 1 {
		t.Fatalf("a new result was announced %d time(s), want 1", *announcements)
	}
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running == 0 {
		t.Fatal("a success shown by Resume started no timer")
	}
}

// TestResultCardResumeAnnouncesARepeatedFailure is a second capture failing
// exactly as the first did: the card lists it once, and a screen reader still
// hears that this capture failed.
func TestResultCardResumeAnnouncesARepeatedFailure(t *testing.T) {
	card := newOffScreenCard(t, nil)
	failure := Notice{Kind: NoticeError, Message: "saving failed"}
	card.Report(failure, "")
	announcements, settle := watchCardAnnouncements(t, card)

	card.Suspend()
	card.Report(failure, "")
	card.Resume()
	settle()
	if *announcements != 1 {
		t.Fatalf("a repeated failure was announced %d time(s), want 1", *announcements)
	}
	if len(card.queue.problems) != 1 {
		t.Fatalf("problems = %+v, want it listed once", card.queue.problems)
	}
}

// TestResultCardExpiryLeavesAWaitingProblemUnannounced is a success leaving
// the card to an earlier failure, which must not be read out as if the
// capture that just succeeded had failed.
func TestResultCardExpiryLeavesAWaitingProblemUnannounced(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "earlier failure"}, "")
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	announcements, settle := watchCardAnnouncements(t, card)

	cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
	settle()
	if !strings.Contains(card.message.Text(), "earlier failure") {
		t.Fatalf("message = %q", card.message.Text())
	}
	if *announcements != 0 {
		t.Fatalf("the earlier failure was announced %d time(s)", *announcements)
	}
}

// TestResultCardExpiryAnnouncesAProblemFromTheCapture is a problem that came
// during a capture that then succeeded. The success is read out over it, so a
// screen reader, which cannot reach the card, hears it as the success leaves,
// and once.
func TestResultCardExpiryAnnouncesAProblemFromTheCapture(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Suspend()
	card.ReportSetting(Notice{Kind: NoticeWarning, Message: "setting not saved"})
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	card.Resume()
	announcements, settle := watchCardAnnouncements(t, card)

	cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
	settle()
	if !strings.Contains(card.message.Text(), "setting not saved") {
		t.Fatalf("message = %q", card.message.Text())
	}
	if *announcements != 1 {
		t.Fatalf("the problem was announced %d time(s), want 1", *announcements)
	}
	card.Reposition()
	settle()
	if *announcements != 1 {
		t.Fatalf("moving the card announced it again: %d", *announcements)
	}
}

// TestResultCardAnnouncesAProblemAfterItsSuccessEndedForACapture is the next
// capture ending that success early and then being cancelled: the problem has
// still not been heard.
func TestResultCardAnnouncesAProblemAfterItsSuccessEndedForACapture(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Suspend()
	card.ReportSetting(Notice{Kind: NoticeWarning, Message: "setting not saved"})
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	card.Resume()
	announcements, settle := watchCardAnnouncements(t, card)

	card.Suspend()
	card.Resume()
	settle()
	if *announcements != 1 {
		t.Fatalf("the problem was announced %d time(s), want 1", *announcements)
	}
}

// watchCardAnnouncements counts the live-region events of the card's message.
// settle lets those already raised arrive.
func watchCardAnnouncements(t *testing.T, card *ResultCard) (announcements *int, settle func()) {
	t.Helper()
	return watchLabelAnnouncements(t, card.message.hwnd)
}

// watchLabelAnnouncements counts the live-region events of one label.
func watchLabelAnnouncements(t *testing.T, label windows.Handle) (announcements *int, settle func()) {
	t.Helper()
	announcements = new(int)
	callback := windows.NewCallback(func(_, _, hwnd, _, _, _, _ uintptr) uintptr {
		if windows.Handle(hwnd) == label {
			*announcements++
		}
		return 0
	})
	hook, _, err := procCardTestSetWinEventHook.Call(eventObjectLiveRegionChanged, eventObjectLiveRegionChanged,
		0, callback, uintptr(windows.GetCurrentProcessId()), uintptr(windows.GetCurrentThreadId()), 0)
	if hook == 0 {
		t.Fatalf("SetWinEventHook: %v", err)
	}
	t.Cleanup(func() { procCardTestUnhookWinEvent.Call(hook) })
	// An out-of-context event arrives as this thread retrieves messages.
	return announcements, func() {
		var message [64]byte
		for deadline := time.Now().Add(150 * time.Millisecond); time.Now().Before(deadline); {
			procCardTestPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 1)
			time.Sleep(5 * time.Millisecond)
		}
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

// TestResultCardWithdrawsOneSettingProblem is a shortcut conflict resolved
// beside another setting's refusal and a capture's failure, which still stand.
func TestResultCardWithdrawsOneSettingProblem(t *testing.T) {
	card := newOffScreenCard(t, nil)
	var told []int
	card.SetProblemsChanged(func(problems int) { told = append(told, problems) })
	conflict := Notice{Kind: NoticeError, Message: "conflict"}
	card.ReportSetting(Notice{Kind: NoticeError, Message: "quality refused"})
	card.Report(conflict, "")
	card.ReportSetting(conflict)
	card.WithdrawSetting(conflict)
	if len(card.queue.problems) != 2 || card.queue.problems[0].Notice.Message != "quality refused" ||
		card.queue.problems[1].Setting {
		t.Fatalf("problems = %+v", card.queue.problems)
	}
	if want := []int{1, 2, 3, 2}; !slices.Equal(told, want) {
		t.Fatalf("told = %v, want %v", told, want)
	}
}

// TestResultCardDismissProblemIsTheKeyboardsWay covers the view's button: a
// success shown over the problems goes first, so that a press never removes a
// problem the card has not shown, and it says how many are left.
func TestResultCardDismissProblemIsTheKeyboardsWay(t *testing.T) {
	card := newOffScreenCard(t, nil)
	var told []int
	card.SetProblemsChanged(func(problems int) { told = append(told, problems) })
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")
	card.Report(Notice{Kind: NoticeWarning, Message: "two"}, "")
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, "")

	card.DismissProblem()
	if got := card.message.Text(); !strings.Contains(got, "two") || len(card.queue.problems) != 2 {
		t.Fatalf("after the press over the success: message %q, problems %+v", got, card.queue.problems)
	}
	card.DismissProblem()
	if got := card.message.Text(); !strings.Contains(got, "one") {
		t.Fatalf("message after the first dismissal = %q", got)
	}
	card.DismissProblem()
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed after its last problem was dismissed")
	}
	if want := []int{1, 2, 2, 2, 1, 0}; !slices.Equal(told, want) {
		t.Fatalf("told = %v, want %v", told, want)
	}
}

// TestResultCardKeepsItsProblemsWhileHiddenForACapture is the main window back
// during a slow save, with the card still hidden: the view stops offering the
// button, and a press that arrives anyway removes nothing nobody can see.
func TestResultCardKeepsItsProblemsWhileHiddenForACapture(t *testing.T) {
	card := newOffScreenCard(t, nil)
	var told []int
	card.SetProblemsChanged(func(problems int) { told = append(told, problems) })
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")

	card.Suspend()
	card.Report(Notice{Kind: NoticeWarning, Message: "refused during the capture"}, "")
	card.DismissProblem()
	cardWindowProc(card.hwnd, cardWMClose, 0, 0)
	if len(card.queue.problems) != 2 {
		t.Fatalf("problems after a press on a hidden card = %+v", card.queue.problems)
	}
	card.Resume()
	if !cardVisible(card.hwnd) || !strings.Contains(card.message.Text(), "refused") {
		t.Fatalf("after Resume: visible %v, message %q", cardVisible(card.hwnd), card.message.Text())
	}
	if want := []int{1, 0, 0, 2}; !slices.Equal(told, want) {
		t.Fatalf("told = %v, want %v", told, want)
	}
}

// TestResultCardAnnouncesTheProblemsWaiting is a screen reader, which cannot
// reach the card: it must hear that more problems stand behind the new one.
func TestResultCardAnnouncesTheProblemsWaiting(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")
	announcements, settle := watchLabelAnnouncements(t, card.count.hwnd)
	card.Report(Notice{Kind: NoticeError, Message: "two"}, "")
	settle()
	if *announcements != 1 {
		t.Fatalf("the waiting count was announced %d time(s), want 1", *announcements)
	}
	cardWindowProc(card.hwnd, cardWMCommand, cardDismissCommand, 0)
	settle()
	if *announcements != 1 {
		t.Fatalf("an empty count was announced: %d", *announcements)
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

//go:build windows

package screenshot

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var (
	procViewTestShowWindow = testUser32.NewProc("ShowWindow")
	procViewTestMoveWindow = testUser32.NewProc("MoveWindow")
)

func newCardTestView(t *testing.T) (*NativeView, *fakeResultCard, *queuedDispatcher) {
	t.Helper()
	parent := newViewTestParent(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, testFolder(t), parent)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 4)}
	view.NativeDispatcher(dispatcher)
	return view, view.card.(*fakeResultCard), dispatcher
}

// showViewOffScreen makes the status area count as in sight without putting a
// window where a capture test running beside this one would see it, and tells
// the view, as the shell does once it has laid out a view it opened.
func showViewOffScreen(t *testing.T, view *NativeView) {
	t.Helper()
	const swShowNoActivate = 4
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(view.parent), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	procViewTestShowWindow.Call(uintptr(view.parent), swShowNoActivate)
	view.windowAtPoint = func(int32, int32) uintptr { return uintptr(view.status.Handle()) }
	view.NativeShow(true)
	if !view.statusVisible() {
		t.Fatal("the test view is not visible")
	}
	view.NativeActivated()
}

// TestShortcutCaptureReportsEveryOutcomeOnTheCard is the failed shortcut
// capture followed by a successful one: each reaches the card, so the later
// result cannot hide the failure, and the card is hidden while each capture
// reads the screen (SCR-060, SCR-068, SCR-069).
func TestShortcutCaptureReportsEveryOutcomeOnTheCard(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)

	suspendedDuringCapture := false
	results := []CaptureResult{
		{Save: errors.New("access denied")},
		{Path: `C:\Shots\two.png`},
	}
	view.capture = func(CaptureRequest) CaptureResult {
		suspendedDuringCapture = card.suspended
		result := results[0]
		results = results[1:]
		return result
	}

	view.NativeHotkey(globalShortcutHotkeyID)
	dispatcher.runNext(t)
	if !suspendedDuringCapture {
		t.Fatal("the card was on screen while the capture ran")
	}
	if card.suspended {
		t.Fatal("the card stayed hidden after the capture ended")
	}
	if len(card.reports) != 1 || card.reports[0].Notice.Kind != ui.NoticeWarning || !strings.Contains(card.reports[0].Notice.Message, "access denied") {
		t.Fatalf("card after the failure = %+v", card.reports)
	}

	view.NativeHotkey(globalShortcutHotkeyID)
	dispatcher.runNext(t)
	if len(card.reports) != 2 || card.reports[1].Notice.Kind != ui.NoticeSuccess || card.reports[1].Path != `C:\Shots\two.png` {
		t.Fatalf("card after the success = %+v", card.reports)
	}
}

func TestStartCaptureButtonStaysOffTheCard(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.capture = func(CaptureRequest) CaptureResult {
		return CaptureResult{Save: errors.New("access denied")}
	}
	view.NativeAction()
	dispatcher.runNext(t)
	if len(card.reports) != 0 {
		t.Fatalf("a capture started in the window reached the card: %+v", card.reports)
	}
}

// TestShortcutCaptureWatchedInTheViewStaysOffTheCard covers the shortcut
// pressed with the Screenshot view in front, whose status area the user sees,
// and the same capture once the window was minimized meanwhile.
func TestShortcutCaptureWatchedInTheViewStaysOffTheCard(t *testing.T) {
	view, card, _ := newCardTestView(t)
	showViewOffScreen(t, view)
	view.fromShortcut, view.watched = true, true
	view.finishCapture(CaptureResult{Save: errors.New("access denied")}, displayCaptureMessages)
	if len(card.reports) != 0 {
		t.Fatalf("a result the user could see reached the card: %+v", card.reports)
	}

	const swMinimize = 6
	procViewTestShowWindow.Call(uintptr(view.parent), swMinimize)
	view.finishCapture(CaptureResult{Save: errors.New("access denied")}, displayCaptureMessages)
	if len(card.reports) != 1 {
		t.Fatalf("a result in a minimized window did not reach the card: %+v", card.reports)
	}
}

// TestShortcutReadsWhetherTheViewIsInFront runs the real calculation of
// watched through the shortcut, with the view in front and then behind.
func TestShortcutReadsWhetherTheViewIsInFront(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	showViewOffScreen(t, view)
	view.capture = func(CaptureRequest) CaptureResult {
		return CaptureResult{Save: errors.New("access denied")}
	}

	view.foregroundWindow = func() windows.Handle { return view.parent }
	view.NativeHotkey(globalShortcutHotkeyID)
	// Only the start counts here: a foreground window at the end would move
	// anything on the card to the status area and hide a wrong report.
	view.foregroundWindow = func() windows.Handle { return 0 }
	dispatcher.runNext(t)
	if len(card.reports) != 0 {
		t.Fatalf("a capture with the view in front reached the card: %+v", card.reports)
	}

	view.NativeHotkey(globalShortcutHotkeyID)
	dispatcher.runNext(t)
	if len(card.reports) != 1 {
		t.Fatalf("a capture with another application in front: card = %+v", card.reports)
	}
}

func TestShortcutRefusalsAndLostSessionsReachTheCard(t *testing.T) {
	t.Run("invalid JPEG quality", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		view.quality.Edit.SetText("500")
		view.NativeHotkey(globalShortcutHotkeyID)
		if len(card.reports) != 1 || card.reports[0].Notice.Kind != ui.NoticeError {
			t.Fatalf("card = %+v", card.reports)
		}
	})
	t.Run("overlay that cannot open", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		_, _, createErr := fakeOverlaySession(view)
		*createErr = errors.New("no overlay")
		view.NativeHotkey(globalShortcutHotkeyID)
		if card.suspended {
			t.Fatal("a failed start left the card hidden")
		}
		if len(card.reports) != 1 || !strings.Contains(card.reports[0].Notice.Message, "no overlay") {
			t.Fatalf("card = %+v", card.reports)
		}
	})
	t.Run("session a display change ended", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		fakeOverlaySession(view)
		view.NativeHotkey(globalShortcutHotkeyID)
		if !card.suspended {
			t.Fatal("the card was on screen during the selection")
		}
		view.sessions.DesktopBounds = func() (display.Rect, error) {
			return display.Rect{}, errors.New("no displays")
		}
		view.sessions.DisplayChange()
		if card.suspended {
			t.Fatal("a lost session left the card hidden")
		}
		if len(card.reports) != 1 || card.reports[0].Notice.Kind != ui.NoticeWarning {
			t.Fatalf("card = %+v", card.reports)
		}
	})
	t.Run("selection the user cancelled", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		fakeOverlaySession(view)
		view.NativeHotkey(globalShortcutHotkeyID)
		view.sessions.Cancel()
		if card.suspended {
			t.Fatal("a cancelled session left the card hidden")
		}
		if len(card.reports) != 0 {
			t.Fatalf("a cancel the user chose reached the card: %+v", card.reports)
		}
	})
	t.Run("second start during a selection keeps the card hidden", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		fakeOverlaySession(view)
		view.NativeHotkey(globalShortcutHotkeyID)
		view.NativeAction()
		if !card.suspended {
			t.Fatal("a refused second start showed the card over the selection")
		}
		if !view.fromShortcut {
			t.Fatal("a refused second start changed how the selection is reported")
		}
		view.sessions.Cancel()
	})
}

func TestStartupShortcutConflictReachesTheCardWhileTheViewIsHidden(t *testing.T) {
	view := newConflictedHotkeyView(t)
	card := view.card.(*fakeResultCard)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.NativeDispatcher(dispatcher)
	dispatcher.runNext(t)
	if len(card.reports) != 1 || !strings.Contains(card.reports[0].Notice.Text(), ui.ErrorShortcutRegistrationLabel) {
		t.Fatalf("card = %+v", card.reports)
	}
}

func TestStartupShortcutConflictStaysOffTheCardWhenTheViewShows(t *testing.T) {
	view := newConflictedHotkeyView(t)
	card := view.card.(*fakeResultCard)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.NativeDispatcher(dispatcher)
	showViewOffScreen(t, view)
	dispatcher.runNext(t)
	if len(card.reports) != 0 {
		t.Fatalf("a conflict shown in the view reached the card: %+v", card.reports)
	}
}

func TestCaptureNoticeReportsASavedImageWithALeftoverNameAsSaved(t *testing.T) {
	leftover := errors.New(`remove C:\Shots\.gamedevtools-1.tmp: access denied`)
	notice := captureNotice(CaptureResult{Path: `C:\Shots\one.png`, Save: leftover}, displayCaptureMessages)
	if notice.Kind != ui.NoticeWarning || strings.Contains(notice.Message, "saving failed") {
		t.Fatalf("notice = %+v", notice)
	}
	for _, want := range []string{`C:\Shots\one.png`, `.gamedevtools-1.tmp`, "can be deleted"} {
		if !strings.Contains(notice.Message, want) {
			t.Fatalf("notice %q does not name %q", notice.Message, want)
		}
	}

	both := captureNotice(CaptureResult{Path: `C:\Shots\one.png`, Save: leftover, Clipboard: errors.New("clipboard busy")}, displayCaptureMessages)
	if strings.Contains(both.Message, "saving failed") || !strings.Contains(both.Message, "clipboard busy") || !strings.Contains(both.Message, "can be deleted") {
		t.Fatalf("notice with a clipboard failure = %+v", both)
	}

	failed := captureNotice(CaptureResult{Save: errors.New("disk full")}, displayCaptureMessages)
	if !strings.Contains(failed.Message, "saving failed") {
		t.Fatalf("a save with no file was not reported as failed: %+v", failed)
	}
}

// TestFailedDisplayEnumerationKeepsTheSelectedDisplay is SCR-040: a failed
// enumeration used to empty the picker, so the next capture took the display
// under the pointer and reported a success.
func TestFailedDisplayEnumerationKeepsTheSelectedDisplay(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	displays := []display.Display{
		{Number: 1, DeviceName: `\\.\DISPLAY1`, Primary: true, Bounds: display.Rect{Right: 1920, Bottom: 1080}, DPIX: 96, DPIY: 96},
		{Number: 2, DeviceName: `\\.\DISPLAY2`, Bounds: display.Rect{Left: 1920, Right: 3840, Bottom: 1080}, DPIX: 96, DPIY: 96},
	}
	var listErr error
	view.listDisplays = func() ([]display.Display, error) { return displays, listErr }
	view.refreshDisplays()
	view.displayCombo.Select(1)

	var captured []string
	view.capture = func(request CaptureRequest) CaptureResult {
		captured = append(captured, request.DeviceName)
		return CaptureResult{Path: `C:\Shots\one.png`}
	}

	listErr = errors.New("enumeration failed")
	view.NativeHotkey(globalShortcutHotkeyID)
	if len(captured) != 0 {
		t.Fatalf("a capture ran on an unreadable topology: %v", captured)
	}
	if len(card.reports) != 1 || !strings.Contains(card.reports[0].Notice.Message, "enumeration failed") {
		t.Fatalf("card = %+v", card.reports)
	}
	if got := view.selectedDeviceName(); got != `\\.\DISPLAY2` {
		t.Fatalf("selection during the failure = %q", got)
	}

	listErr = nil
	view.NativeHotkey(globalShortcutHotkeyID)
	dispatcher.runNext(t)
	if len(captured) != 1 || captured[0] != `\\.\DISPLAY2` {
		t.Fatalf("captured after recovery = %v, want the selected display", captured)
	}
}

// TestReclaimedProblemsKeepTheLatestResult is a failed capture followed by a
// successful one: opening the view shows the failure without replacing the
// result Open Folder belongs to.
func TestReclaimedProblemsKeepTheLatestResult(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.fromShortcut = true
	view.finishCapture(CaptureResult{Save: errors.New("access denied")}, displayCaptureMessages)
	view.finishCapture(CaptureResult{Path: `C:\Shots\two.png`}, displayCaptureMessages)
	showViewOffScreen(t, view)
	if len(card.reports) != 0 {
		t.Fatalf("the card kept its problems: %+v", card.reports)
	}
	text := view.status.Text()
	failed, saved := strings.Index(text, "access denied"), strings.Index(text, `C:\Shots\two.png`)
	if failed < 0 || saved < failed || !strings.Contains(text, "Latest capture") {
		t.Fatalf("status = %q, want the failure and then the latest result", text)
	}
	if view.savedPath != `C:\Shots\two.png` {
		t.Fatalf("Open Folder targets %q", view.savedPath)
	}
}

// TestReclaimedProblemKeepsItsSavedFile is a saved image whose clipboard copy
// failed, followed by a capture with no file: Open Folder still reaches it.
func TestReclaimedProblemKeepsItsSavedFile(t *testing.T) {
	view, _, _ := newCardTestView(t)
	view.fromShortcut = true
	view.finishCapture(CaptureResult{Path: `C:\Shots\one.png`, Clipboard: errors.New("clipboard busy")}, displayCaptureMessages)
	view.finishCapture(CaptureResult{Err: errors.New("no capture")}, displayCaptureMessages)
	if view.savedPath != "" {
		t.Fatalf("savedPath before the view opened = %q", view.savedPath)
	}
	showViewOffScreen(t, view)
	if view.savedPath != `C:\Shots\one.png` {
		t.Fatalf("Open Folder targets %q, want the saved file", view.savedPath)
	}
}

// TestRestoreDefaultsWithdrawsSettingProblems covers a problem with a setting
// on the card and one already moved to the status area: neither survives the
// restore that resolved it, and a capture's failure beside it does.
func TestRestoreDefaultsWithdrawsSettingProblems(t *testing.T) {
	setting := ui.Notice{Kind: ui.NoticeError, Message: "bad shortcut"}
	failure := ui.Notice{Kind: ui.NoticeWarning, Message: "save failed"}

	t.Run("on the card", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		card.ReportSetting(setting)
		card.Report(failure, "")
		view.NativeRestoreDefaults()
		if len(card.reports) != 1 || card.reports[0].Notice != failure {
			t.Fatalf("card = %+v", card.reports)
		}
	})
	t.Run("in the status area", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		card.ReportSetting(setting)
		card.Report(failure, "")
		showViewOffScreen(t, view)
		view.NativeRestoreDefaults()
		if text := view.status.Text(); strings.Contains(text, "bad shortcut") || !strings.Contains(text, "save failed") {
			t.Fatalf("status = %q", text)
		}
	})
	t.Run("alone in the status area", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		card.ReportSetting(setting)
		showViewOffScreen(t, view)
		view.NativeRestoreDefaults()
		if text := view.status.Text(); text != "" {
			t.Fatalf("status = %q, want the resolved problem gone", text)
		}
	})
}

// TestRestoreDefaultsKeepsWhatItDoesNotResolve covers a status notice moved
// from the card: a capture's result survives the restore, a refusal of a
// setting does not, and a default shortcut still taken does not erase a
// capture failure.
func TestRestoreDefaultsKeepsWhatItDoesNotResolve(t *testing.T) {
	conflict := ui.Notice{Kind: ui.NoticeError, Message: "bad shortcut"}
	failure := ui.Notice{Kind: ui.NoticeWarning, Message: "save failed"}

	t.Run("the latest result beside a shortcut failure", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		view.showOutcome(ui.Notice{Kind: ui.NoticeSuccess, Message: `Saved to C:\Shots\two.png`})
		view.shortcut.Error.SetTextQuietly(conflict.Message)
		card.ReportSetting(conflict)
		showViewOffScreen(t, view)
		view.NativeRestoreDefaults()
		if text := view.status.Text(); strings.Contains(text, "bad shortcut") || !strings.Contains(text, "two.png") {
			t.Fatalf("status = %q, want the latest result alone", text)
		}
	})
	t.Run("a refused setting beside a capture failure", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		view.quality.Edit.SetText("500")
		view.NativeAction()
		card.reports = nil
		card.Report(failure, "")
		showViewOffScreen(t, view)
		if text := view.status.Text(); !strings.Contains(text, "JPEG quality") {
			t.Fatalf("status before the restore = %q", text)
		}
		view.NativeRestoreDefaults()
		if text := view.status.Text(); strings.Contains(text, "JPEG quality") || !strings.Contains(text, "save failed") {
			t.Fatalf("status = %q, want the failure alone", text)
		}
	})
	t.Run("a default shortcut still taken", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		card.ReportSetting(conflict)
		card.Report(failure, "")
		showViewOffScreen(t, view)
		view.hotkeys.(*fakeHotkeyRegistrar).applyFunc = func(Shortcut) error { return errors.New("held by another application") }
		view.NativeRestoreDefaults()
		text := view.status.Text()
		if !strings.Contains(text, "save failed") || !strings.Contains(text, "held by another application") || strings.Contains(text, "bad shortcut") {
			t.Fatalf("status = %q, want the failure and the new conflict", text)
		}
	})
}

// TestReclaimedProblemsKeepADisplayFailure is a failed display list and a
// shortcut conflict at startup with another module remembered: the failure
// the status area holds is not replaced by the conflict from the card.
func TestReclaimedProblemsKeepADisplayFailure(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.listDisplays = func() ([]display.Display, error) { return nil, errors.New("enumeration failed") }
	view.refreshDisplays()
	card.ReportSetting(ui.Notice{Kind: ui.NoticeError, Message: "bad shortcut"})
	showViewOffScreen(t, view)
	if text := view.status.Text(); !strings.Contains(text, "enumeration failed") || !strings.Contains(text, "bad shortcut") {
		t.Fatalf("status = %q, want both problems", text)
	}
}

// TestProblemsStayOnTheCardUntilTheStatusAreaIsInSight is the shortcut with
// the view in front and its status area below the fold, where the default
// window size leaves it: a refused start must stay on the card, and move only
// once the user can read it in the status area.
func TestProblemsStayOnTheCardUntilTheStatusAreaIsInSight(t *testing.T) {
	view, card, _ := newCardTestView(t)
	showViewOffScreen(t, view)
	view.foregroundWindow = func() windows.Handle { return view.parent }
	_, _, createErr := fakeOverlaySession(view)
	*createErr = errors.New("no overlay")
	view.status.Move(10, 5000, 200, 60)

	view.NativeHotkey(globalShortcutHotkeyID)
	if card.suspended {
		t.Fatal("a failed start left the card hidden")
	}
	view.NativeActivated()
	if len(card.reports) != 1 {
		t.Fatalf("the problem left the card for a status area out of sight: %+v", card.reports)
	}

	view.status.Move(10, 20, 200, 60)
	view.NativeActivated()
	if len(card.reports) != 0 {
		t.Fatalf("the card kept the problem with the status area in sight: %+v", card.reports)
	}
	if text := view.status.Text(); !strings.Contains(text, "no overlay") {
		t.Fatalf("status = %q", text)
	}
}

// TestReclaimedProblemsKeepOtherProblemsTheStatusHolds is a failure that exists
// only in the status area when the card's problems move there. A problem with
// a setting stays one, so that the restore of the defaults still clears it.
func TestReclaimedProblemsKeepOtherProblemsTheStatusHolds(t *testing.T) {
	failure := ui.Notice{Kind: ui.NoticeWarning, Message: "save failed"}

	t.Run("a failed Open Folder", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		_ = view.status.Show(ui.Notice{Kind: ui.NoticeError, Message: "share offline"}, 0)
		card.Report(failure, "")
		showViewOffScreen(t, view)
		if text := view.status.Text(); !strings.Contains(text, "share offline") || !strings.Contains(text, "save failed") {
			t.Fatalf("status = %q, want both problems", text)
		}
	})
	t.Run("a notice that is no problem", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		_ = view.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: "mode selected"}, 0)
		card.Report(failure, "")
		showViewOffScreen(t, view)
		if text := view.status.Text(); strings.Contains(text, "mode selected") || !strings.Contains(text, "save failed") {
			t.Fatalf("status = %q, want the problem alone", text)
		}
	})
	t.Run("a shortcut failure the restore resolves", func(t *testing.T) {
		view, card, _ := newCardTestView(t)
		view.presentShortcutFailure(errors.New("held by another application"))
		card.Report(failure, "")
		showViewOffScreen(t, view)
		if text := view.status.Text(); !strings.Contains(text, "held by another application") {
			t.Fatalf("status before the restore = %q", text)
		}
		view.NativeRestoreDefaults()
		if text := view.status.Text(); strings.Contains(text, "held by another application") || !strings.Contains(text, "save failed") {
			t.Fatalf("status = %q, want the failure alone", text)
		}
	})
}

// TestRestoreDefaultsKeepsTheLatestResultBesideANewConflict is a default
// shortcut still taken after the restore withdrew everything else the notice
// from the card held: the capture's failure stays beside the new conflict.
func TestRestoreDefaultsKeepsTheLatestResultBesideANewConflict(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.showOutcome(ui.Notice{Kind: ui.NoticeError, Message: "capture failed"})
	card.ReportSetting(ui.Notice{Kind: ui.NoticeError, Message: "bad shortcut"})
	showViewOffScreen(t, view)
	view.hotkeys.(*fakeHotkeyRegistrar).applyFunc = func(Shortcut) error { return errors.New("held by another application") }
	view.NativeRestoreDefaults()
	text := view.status.Text()
	if !strings.Contains(text, "capture failed") || !strings.Contains(text, "held by another application") || strings.Contains(text, "bad shortcut") {
		t.Fatalf("status = %q, want the capture failure and the new conflict", text)
	}
}

// TestStatusScrolledOutOfTheClientAreaIsNotInSight is a status area scrolled
// up under the main window's title bar, where the window at its center is the
// main window itself.
func TestStatusScrolledOutOfTheClientAreaIsNotInSight(t *testing.T) {
	view, _, _ := newCardTestView(t)
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(view.parent), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	view.status.Move(10, -100, 200, 60)
	view.windowAtPoint = func(int32, int32) uintptr { return uintptr(view.parent) }
	if !view.statusCenterCovered() {
		t.Fatal("a status area above the client area counts as in sight")
	}
}

// TestProblemsMoveOnceACaptureEndsWithTheUserBack is the user returning to
// the view during a slow save: the activation came too early to move the
// problems, and no other is coming.
func TestProblemsMoveOnceACaptureEndsWithTheUserBack(t *testing.T) {
	view, card, _ := newCardTestView(t)
	card.Report(ui.Notice{Kind: ui.NoticeError, Message: "earlier failure"}, "")
	view.inProgress = true
	showViewOffScreen(t, view)
	view.NativeActivated()
	if len(card.reports) != 1 {
		t.Fatal("the card gave up its problems during the capture")
	}

	view.foregroundWindow = func() windows.Handle { return view.parent }
	view.finishCapture(CaptureResult{Path: `C:\Shots\one.png`}, displayCaptureMessages)
	if len(card.reports) != 0 {
		t.Fatalf("the card kept its problems: %+v", card.reports)
	}
	if text := view.status.Text(); !strings.Contains(text, "earlier failure") || !strings.Contains(text, `C:\Shots\one.png`) {
		t.Fatalf("status = %q", text)
	}
}

// TestLaterCardProblemJoinsThoseAlreadyMoved is an Open Folder from the card
// that fails after its problems moved to the status area: they have left the
// card, so the new problem must not replace them there.
func TestLaterCardProblemJoinsThoseAlreadyMoved(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.showOutcome(ui.Notice{Kind: ui.NoticeSuccess, Message: `Saved to C:\Shots\three.png`})
	card.Report(ui.Notice{Kind: ui.NoticeWarning, Message: "first failure"}, `C:\Shots\one.png`)
	card.Report(ui.Notice{Kind: ui.NoticeError, Message: "second failure"}, "")
	showViewOffScreen(t, view)

	card.Report(ui.Notice{Kind: ui.NoticeError, Message: "open folder failed"}, "")
	card.Report(ui.Notice{Kind: ui.NoticeError, Message: "second failure"}, "")
	view.NativeActivated()
	text := view.status.Text()
	for _, want := range []string{"first failure", "second failure", "open folder failed", "three.png"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status = %q, want it to keep %q", text, want)
		}
	}
	if strings.Count(text, "second failure") != 1 {
		t.Fatalf("status = %q, a problem is listed twice", text)
	}
}

// TestCardOpenFolderFailureMovesToAViewInFront is the user back in the view
// when the reveal fails: no activation follows, so the failure moves to the
// status area. With another window in front it stays on the card.
func TestCardOpenFolderFailureMovesToAViewInFront(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.reveal = func(string) error { return errors.New("share offline") }
	showViewOffScreen(t, view)

	view.foregroundWindow = func() windows.Handle { return 0 }
	view.revealFromCard(`C:\Shots\one.png`)
	dispatcher.runNext(t)
	if len(card.reports) != 1 {
		t.Fatalf("with another window in front: card = %+v", card.reports)
	}

	view.foregroundWindow = func() windows.Handle { return view.parent }
	view.revealFromCard(`C:\Shots\two.png`)
	dispatcher.runNext(t)
	if len(card.reports) != 0 {
		t.Fatalf("with the view in front: card = %+v", card.reports)
	}
	if text := view.status.Text(); !strings.Contains(text, "share offline") {
		t.Fatalf("status = %q", text)
	}
}

// TestShortcutCaptureGoesOnTheCardOnceTheViewIsCovered is the user leaving
// for a maximized application during the capture.
func TestShortcutCaptureGoesOnTheCardOnceTheViewIsCovered(t *testing.T) {
	view, card, _ := newCardTestView(t)
	showViewOffScreen(t, view)
	view.fromShortcut, view.watched = true, true
	other := newViewTestParent(t)
	view.windowAtPoint = func(int32, int32) uintptr { return uintptr(other) }
	view.finishCapture(CaptureResult{Save: errors.New("access denied")}, displayCaptureMessages)
	if len(card.reports) != 1 {
		t.Fatalf("a result behind another window did not reach the card: %+v", card.reports)
	}
}

// TestStatusCenterCoveredAsksAboutTheStatusAreasCenter pins which point is
// asked about and how the answer is read: the view's own controls do not
// cover it, and another top-level window or no window at all does.
func TestStatusCenterCoveredAsksAboutTheStatusAreasCenter(t *testing.T) {
	view, _, _ := newCardTestView(t)
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(view.parent), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	view.status.Move(10, 20, 200, 60)
	var bounds viewRect
	procViewGetWindowRect.Call(uintptr(view.status.Handle()), uintptr(unsafe.Pointer(&bounds)))

	var askedX, askedY int32
	var answer uintptr
	view.windowAtPoint = func(x, y int32) uintptr {
		askedX, askedY = x, y
		return answer
	}
	answer = uintptr(view.status.Handle())
	if view.statusCenterCovered() {
		t.Fatal("the status area counts as covering itself")
	}
	if askedX != bounds.Left+100 || askedY != bounds.Top+30 {
		t.Fatalf("asked about (%d, %d), want the center of %+v", askedX, askedY, bounds)
	}
	answer = uintptr(view.parent)
	if view.statusCenterCovered() {
		t.Fatal("the view's own window counts as covering it")
	}
	answer = uintptr(newViewTestParent(t))
	if !view.statusCenterCovered() {
		t.Fatal("another window over the status area was not seen")
	}
	answer = 0
	if !view.statusCenterCovered() {
		t.Fatal("a point with no window counts as in sight")
	}
}

func TestShortcutRefusedDuringACaptureReachesTheCardAfterIt(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.fromShortcut, view.watched = false, false
	view.inProgress = true
	view.card.Suspend()
	view.NativeHotkey(globalShortcutHotkeyID)
	if view.fromShortcut {
		t.Fatal("the refused start changed how the running capture is reported")
	}
	if len(card.reports) != 1 || card.reports[0].Notice.Message != ui.CaptureShortcutRefused {
		t.Fatalf("card = %+v", card.reports)
	}
	if !card.suspended {
		t.Fatal("the refused start showed the card during the capture")
	}
}

func TestShortcutQualityThatCannotApplyReachesTheCard(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.options.options.Mode = CaptureMode("not a mode")
	view.quality.Edit.SetText("55")
	view.NativeHotkey(globalShortcutHotkeyID)
	if len(card.reports) != 1 || !card.reports[0].Setting || card.reports[0].Notice.Kind != ui.NoticeError {
		t.Fatalf("card = %+v", card.reports)
	}
}

// TestViewTakesBackTheCardsProblemsOnceVisible is the keyboard's way to them:
// the card never takes the focus, so opening the view shows what it held.
func TestViewTakesBackTheCardsProblemsOnceVisible(t *testing.T) {
	view, card, _ := newCardTestView(t)
	card.Report(ui.Notice{Kind: ui.NoticeWarning, Message: "first problem"}, "")
	card.Report(ui.Notice{Kind: ui.NoticeError, Message: "second problem"}, "")

	view.NativeActivated()
	if len(card.reports) != 2 {
		t.Fatal("the card gave up its problems while the view was hidden")
	}

	view.inProgress = true
	showViewOffScreen(t, view)
	if len(card.reports) != 2 {
		t.Fatal("the card gave up its problems while a capture was running")
	}
	view.inProgress = false

	view.NativeActivated()
	if len(card.reports) != 0 {
		t.Fatalf("the card kept its problems: %+v", card.reports)
	}
	text := view.status.Text()
	if !strings.HasPrefix(text, ui.NoticeErrorLabel) {
		t.Fatalf("status = %q, want the worst problem's severity", text)
	}
	if first, second := strings.Index(text, "first problem"), strings.Index(text, "second problem"); first < 0 || second < first {
		t.Fatalf("status = %q, want both problems, oldest first", text)
	}
}

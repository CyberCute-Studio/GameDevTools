//go:build windows

package screenshot

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

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

// showViewOffScreen makes the status area count as visible without putting a
// window where a capture test running beside this one would see it.
func showViewOffScreen(t *testing.T, view *NativeView) {
	t.Helper()
	const swShowNoActivate = 4
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(view.parent), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	procViewTestShowWindow.Call(uintptr(view.parent), swShowNoActivate)
	view.NativeShow(true)
	if !view.statusVisible() {
		t.Fatal("the test view is not visible")
	}
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

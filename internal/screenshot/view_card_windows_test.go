//go:build windows

package screenshot

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
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
// window where a capture test running beside this one would see it.
func showViewOffScreen(t *testing.T, view *NativeView) {
	t.Helper()
	const swShowNoActivate = 4
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(view.parent), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	procViewTestShowWindow.Call(uintptr(view.parent), swShowNoActivate)
	view.status.Move(10, 20, 200, 60)
	view.shortcut.Error.Move(10, 90, 200, 40)
	view.windowAtPoint = func(int32, int32) uintptr { return uintptr(view.status.Handle()) }
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

// TestOutcomeOnTheCardIsAnnouncedByTheCardAlone is the view visible beside
// another application: a result going on the card, which announces it, is
// written to the status area quietly, and one that stays off the card is still
// announced there.
func TestOutcomeOnTheCardIsAnnouncedByTheCardAlone(t *testing.T) {
	view, card, _ := newCardTestView(t)
	showViewOffScreen(t, view)
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)
	failure := CaptureResult{Save: errors.New("access denied")}

	view.fromShortcut, view.watched = true, false
	view.finishCapture(failure, displayCaptureMessages)
	if len(card.reports) != 1 {
		t.Fatalf("card = %+v", card.reports)
	}
	if !strings.Contains(view.status.Text(), "access denied") {
		t.Fatalf("status = %q", view.status.Text())
	}
	if got := watch.Count(view.status.Handle()); got != 0 {
		t.Fatalf("status announcements beside the card = %d, want 0", got)
	}

	view.fromShortcut, view.watched = true, true
	view.finishCapture(failure, displayCaptureMessages)
	if got := watch.Count(view.status.Handle()); got != 1 {
		t.Fatalf("status announcements with the card not needed = %d, want 1", got)
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
	// Only the start counts.
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
		selectJPEG(view)
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

// TestStartupShortcutConflictReachesTheCardBehindAnotherWindow is the
// application started behind another, with this view the remembered one: the
// message beside the field is there, and nobody can see it.
func TestStartupShortcutConflictReachesTheCardBehindAnotherWindow(t *testing.T) {
	view := newConflictedHotkeyView(t)
	card := view.card.(*fakeResultCard)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.NativeDispatcher(dispatcher)
	showViewOffScreen(t, view)
	other := newViewTestParent(t)
	view.windowAtPoint = func(int32, int32) uintptr { return uintptr(other) }
	dispatcher.runNext(t)
	if len(card.reports) != 1 || !card.reports[0].Setting {
		t.Fatalf("card = %+v", card.reports)
	}
}

// TestRegisteredShortcutWithdrawsTheStartupConflict is the user choosing a free
// combination for the conflict the card reported: the card must stop saying
// the shortcut failed, and keeps what else it holds.
func TestRegisteredShortcutWithdrawsTheStartupConflict(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
	}{
		{"a new combination", "Ctrl+Alt+F9"},
		{"the same combination, now free", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := newConflictedHotkeyView(t)
			card := view.card.(*fakeResultCard)
			dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
			view.NativeDispatcher(dispatcher)
			dispatcher.runNext(t)
			failure := ui.Notice{Kind: ui.NoticeWarning, Message: "save failed"}
			card.Report(failure, "")

			// A combination still taken leaves the conflict on the card.
			view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)
			if len(card.reports) != 2 {
				t.Fatalf("card with the shortcut still taken = %+v", card.reports)
			}

			view.hotkeys.(*fakeHotkeyRegistrar).applyFunc = nil
			if test.text != "" {
				view.shortcut.Edit.SetText(test.text)
			}
			view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)
			if len(card.reports) != 1 || card.reports[0].Notice != failure {
				t.Fatalf("card = %+v", card.reports)
			}
		})
	}
}

// TestStatusRowGivesItsTextTheWholeWidth covers the widths from a narrow window
// to a half-screen and a default one: a saved path has no spaces to wrap at, so
// width the buttons took from the text cut off the file's name.
func TestStatusRowGivesItsTextTheWholeWidth(t *testing.T) {
	view, _, _ := newCardTestView(t)
	scale := func(value int32) int32 { return value }
	measure := func(string, int32) int32 { return 20 }
	bounds := func(control windows.Handle) viewRect {
		var r viewRect
		procViewGetWindowRect.Call(uintptr(control), uintptr(unsafe.Pointer(&r)))
		procViewMapWindowPoints.Call(0, uintptr(view.parent), uintptr(unsafe.Pointer(&r)), 2)
		return r
	}
	const x = 8
	for _, width := range []int32{210, 272, 459, 460, 630, 770} {
		view.NativeLayout(x, 0, width, scale, measure)
		status, dismiss, folder := bounds(view.status.Handle()), bounds(view.dismissProblem.Handle()), bounds(view.openFolder.Handle())
		if status.Right-status.Left != width {
			t.Errorf("width %d: status text is %d wide", width, status.Right-status.Left)
		}
		if dismiss.Top < status.Bottom || folder.Top < status.Bottom {
			t.Errorf("width %d: a button overlaps the status text: %+v %+v %+v", width, status, dismiss, folder)
		}
		if dismiss.Left < x || folder.Right > x+width || dismiss.Right > folder.Left {
			t.Errorf("width %d: buttons leave the content: %+v %+v", width, dismiss, folder)
		}
		height, _, _, _ := view.optionsSectionHeight(width, scale, measure)
		if folder.Bottom > height {
			t.Errorf("width %d: the section is %d tall and its buttons end at %d", width, height, folder.Bottom)
		}
	}
}

// TestOutcomeAsksTheDesktopOnce is a window moving off the status area between
// two looks: a result written quietly for the card must then reach the card.
func TestOutcomeAsksTheDesktopOnce(t *testing.T) {
	view, card, _ := newCardTestView(t)
	showViewOffScreen(t, view)
	view.fromShortcut, view.watched = true, true
	other := newViewTestParent(t)
	asked := 0
	view.windowAtPoint = func(int32, int32) uintptr {
		asked++
		if asked == 1 {
			return uintptr(other)
		}
		return uintptr(view.status.Handle())
	}
	view.finishCapture(CaptureResult{Save: errors.New("access denied")}, displayCaptureMessages)
	if len(card.reports) != 1 {
		t.Fatalf("a result written quietly did not reach the card: %+v", card.reports)
	}
}

// TestFailedDisplayEnumerationIsAnnouncedOnce is a shortcut capture whose
// display list fails with the view visible beside another application: the
// card announces the failure and the status area does not repeat it.
func TestFailedDisplayEnumerationIsAnnouncedOnce(t *testing.T) {
	view, card, _ := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	showViewOffScreen(t, view)
	view.foregroundWindow = func() windows.Handle { return 0 }
	view.listDisplays = func() ([]display.Display, error) { return nil, errors.New("enumeration failed") }
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)
	view.NativeHotkey(globalShortcutHotkeyID)
	if len(card.reports) != 1 || !strings.Contains(view.status.Text(), "enumeration failed") {
		t.Fatalf("card = %+v, status = %q", card.reports, view.status.Text())
	}
	if got := watch.Count(view.status.Handle()); got != 0 {
		t.Fatalf("status announcements beside the card = %d, want 0", got)
	}
}

// TestUnsavedQualityWarningJoinsTheCardAfterItIsHidden is a quality that cannot
// be saved as a shortcut capture starts. Reported first, the warning would be
// announced on a card hidden at once, and a capture ending with no result of
// its own would not announce it again.
func TestUnsavedQualityWarningJoinsTheCardAfterItIsHidden(t *testing.T) {
	parent := newViewTestParent(t)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(blocker, "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, testFolder(t), parent)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 4)}
	view.NativeDispatcher(dispatcher)
	card := view.card.(*fakeResultCard)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.capture = func(CaptureRequest) CaptureResult { return CaptureResult{Path: `C:\Shots\one.png`} }
	selectJPEG(view)
	view.quality.Edit.SetText("55")

	view.NativeHotkey(globalShortcutHotkeyID)
	if len(card.reports) != 1 || !card.reports[0].Setting || card.reports[0].Notice.Kind != ui.NoticeWarning {
		t.Fatalf("card = %+v", card.reports)
	}
	if !card.settingSuspended {
		t.Fatal("the warning reached the card before the capture hid it")
	}
	dispatcher.runNext(t)
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

// TestRestoreDefaultsWithdrawsSettingProblems is a problem with a setting on
// the card: it does not survive the restore that resolved it, and a capture's
// failure beside it does.
func TestRestoreDefaultsWithdrawsSettingProblems(t *testing.T) {
	view, card, _ := newCardTestView(t)
	failure := ui.Notice{Kind: ui.NoticeWarning, Message: "save failed"}
	card.ReportSetting(ui.Notice{Kind: ui.NoticeError, Message: "bad shortcut"})
	card.Report(failure, "")
	view.NativeRestoreDefaults()
	if len(card.reports) != 1 || card.reports[0].Notice != failure {
		t.Fatalf("card = %+v", card.reports)
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
	if !view.controlCovered(view.status.Handle()) {
		t.Fatal("a status area above the client area counts as in sight")
	}
	view.status.Move(10, -30, 200, 60)
	if !view.controlCovered(view.status.Handle()) {
		t.Fatal("a status area partly above the client area counts as in sight")
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

// TestControlCoveredChecksAlongTheControl pins which points are asked about and
// how the answers are read: the view's own controls do not cover the status
// area, and another top-level window or no window over any point asked about
// does, including a narrow window between the corners and the center.
func TestControlCoveredChecksAlongTheControl(t *testing.T) {
	view, _, _ := newCardTestView(t)
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(view.parent), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	view.status.Move(10, 20, 200, 60)
	var bounds viewRect
	procViewGetWindowRect.Call(uintptr(view.status.Handle()), uintptr(unsafe.Pointer(&bounds)))
	corners := [][2]int32{
		{bounds.Left, bounds.Top}, {bounds.Right - 1, bounds.Top},
		{bounds.Left, bounds.Bottom - 1}, {bounds.Right - 1, bounds.Bottom - 1},
	}

	var asked [][2]int32
	own := uintptr(view.status.Handle())
	view.windowAtPoint = func(x, y int32) uintptr {
		asked = append(asked, [2]int32{x, y})
		return own
	}
	if view.controlCovered(view.status.Handle()) {
		t.Fatal("the status area counts as covering itself")
	}
	for _, corner := range corners {
		if !slices.Contains(asked, corner) {
			t.Fatalf("asked about %v, without the corner %v", asked, corner)
		}
	}
	points := slices.Clone(asked)
	own = uintptr(view.parent)
	if view.controlCovered(view.status.Handle()) {
		t.Fatal("the view's own window counts as covering it")
	}

	other := uintptr(newViewTestParent(t))
	for _, answer := range []uintptr{other, 0} {
		for _, covered := range points {
			view.windowAtPoint = func(x, y int32) uintptr {
				if x == covered[0] && y == covered[1] {
					return answer
				}
				return uintptr(view.parent)
			}
			if !view.controlCovered(view.status.Handle()) {
				t.Fatalf("answer %#x at %v was not seen", answer, covered)
			}
		}
	}

	// A window as wide as the step, anywhere along the control and over only
	// its middle, is seen.
	for left := bounds.Left; left+controlCoverStep <= bounds.Right; left++ {
		view.windowAtPoint = func(x, y int32) uintptr {
			if x >= left && x < left+controlCoverStep && y > bounds.Top && y < bounds.Bottom-1 {
				return other
			}
			return uintptr(view.parent)
		}
		if !view.controlCovered(view.status.Handle()) {
			t.Fatalf("a window over x %d to %d was not seen", left, left+controlCoverStep)
		}
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
	selectJPEG(view)
	view.options.options.Mode = CaptureMode("not a mode")
	view.quality.Edit.SetText("55")
	view.NativeHotkey(globalShortcutHotkeyID)
	if len(card.reports) != 1 || !card.reports[0].Setting || card.reports[0].Notice.Kind != ui.NoticeError {
		t.Fatalf("card = %+v", card.reports)
	}
}

// TestDismissProblemButtonFollowsTheCard is the keyboard's way to the card's
// problems: the card never takes the focus, so the view offers the button
// exactly while there is a problem, and a button that disappears hands the
// focus on.
func TestDismissProblemButtonFollowsTheCard(t *testing.T) {
	view, card, _ := newCardTestView(t)
	showViewOffScreen(t, view)
	offered := func() bool {
		style, _, _ := procGetWindowLongW.Call(uintptr(view.dismissProblem.Handle()), gwlStyle)
		enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.dismissProblem.Handle()))
		return style&wsVisible != 0 && enabled != 0
	}
	if offered() {
		t.Fatal("the button is offered with no problem on the card")
	}
	card.Report(ui.Notice{Kind: ui.NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	if offered() {
		t.Fatal("the button is offered for a success, which leaves by itself")
	}
	card.reports = nil
	card.Report(ui.Notice{Kind: ui.NoticeError, Message: "first problem"}, "")
	card.Report(ui.Notice{Kind: ui.NoticeWarning, Message: "second problem"}, "")
	if !offered() {
		t.Fatal("the button is not offered for a problem")
	}

	view.NativeShow(false)
	style, _, _ := procGetWindowLongW.Call(uintptr(view.dismissProblem.Handle()), gwlStyle)
	if style&wsVisible != 0 {
		t.Fatal("the button shows while another module does")
	}
	view.NativeShow(true)

	view.NativeCommand(optionsCommandBase+dismissProblemCommand, 0)
	if len(card.reports) != 1 || card.reports[0].Notice.Message != "first problem" || !offered() {
		t.Fatalf("after one dismissal: card = %+v, offered = %v", card.reports, offered())
	}
	view.dismissProblem.Focus()
	view.NativeCommand(optionsCommandBase+dismissProblemCommand, viewBNDoubleClicked)
	if len(card.reports) != 0 || offered() {
		t.Fatalf("after the last dismissal: card = %+v, offered = %v", card.reports, offered())
	}
	if view.dismissProblem.HasFocus() {
		t.Fatal("the focus was left on a button that has gone")
	}
	// A Space pressed once too often must not reach a control that acts on it.
	if !view.folder.HasFocus() {
		t.Fatal("the focus did not move to the read-only output folder field")
	}
}

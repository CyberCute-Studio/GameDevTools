//go:build windows

package screenshot

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// selectJPEG chooses the JPEG format, which enables the quality field.
func selectJPEG(view *NativeView) {
	view.format.Select(1)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)
}

// TestShortcutFailureLeavesTheStatusAreaOnceItRegisters pins that the status
// area stops reporting a shortcut failure when the shortcut registers, and
// that a later notice which replaced the failure stays (SCR-003, AC-S11).
func TestShortcutFailureLeavesTheStatusAreaOnceItRegisters(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	fake.applyFunc = func(Shortcut) error { return errors.New("held by another application") }
	view.commitShortcut()
	if got := view.status.Text(); !strings.Contains(got, "held by another application") {
		t.Fatalf("status after the failure = %q", got)
	}

	fake.applyFunc = nil
	view.commitShortcut()
	if got := view.status.Text(); got != "" {
		t.Fatalf("status after the shortcut registered = %q, want the failure gone", got)
	}

	fake.applyFunc = func(Shortcut) error { return errors.New("held again") }
	view.commitShortcut()
	const later = "A later result."
	_ = view.status.Show(ui.Notice{Kind: ui.NoticeInfo, Message: later}, 0)
	fake.applyFunc = nil
	view.commitShortcut()
	if got := view.status.Text(); !strings.Contains(got, later) {
		t.Fatalf("status = %q, want the later notice kept", got)
	}
}

// TestPNGCaptureIgnoresInvalidTextInTheDisabledQualityField pins that text in
// a field the PNG format disables cannot refuse a capture that does not use
// it, and that the stored quality keeps its value.
func TestPNGCaptureIgnoresInvalidTextInTheDisabledQualityField(t *testing.T) {
	view, _, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.quality.Edit.SetText("500")
	captured := make(chan CaptureRequest, 1)
	view.capture = func(request CaptureRequest) CaptureResult {
		captured <- request
		return CaptureResult{Path: `C:\Shots\one.png`}
	}

	view.NativeAction()

	select {
	case request := <-captured:
		if request.Options.Format != FormatPNG || request.Options.JPEGQuality != 90 {
			t.Fatalf("capture options = %+v", request.Options)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no capture ran; status = %q", view.status.Text())
	}
	dispatcher.runNext(t)
	if got := view.options.Options().JPEGQuality; got != 90 {
		t.Fatalf("stored JPEG quality = %d, want 90", got)
	}
}

// TestPNGCaptureLeavesValidQualityTextUncommitted pins that a PNG capture does
// not save text from the disabled field, which it does not use.
func TestPNGCaptureLeavesValidQualityTextUncommitted(t *testing.T) {
	view, _, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.quality.Edit.SetText("50")
	view.capture = func(CaptureRequest) CaptureResult {
		return CaptureResult{Path: `C:\Shots\one.png`}
	}

	view.NativeAction()
	dispatcher.runNext(t)

	if got := view.options.Options().JPEGQuality; got != 90 {
		t.Fatalf("stored JPEG quality = %d, want 90", got)
	}
}

// TestChoosingPNGClearsTheQualityFieldsMessage pins that a refusal does not
// stay beside a field PNG disables.
func TestChoosingPNGClearsTheQualityFieldsMessage(t *testing.T) {
	view, _, _ := newCardTestView(t)
	selectJPEG(view)
	view.quality.Edit.SetText("500")
	view.NativeCommand(optionsCommandBase+qualityCommand, ui.NotificationEditKillFocus)
	if view.quality.Error.Text() == "" {
		t.Fatal("invalid quality text showed no message")
	}

	view.format.Select(0)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)

	if got := view.quality.Error.Text(); got != "" {
		t.Fatalf("message after choosing PNG = %q", got)
	}
}

// flakyDispatcher fails its first posts, as a full message queue does.
type flakyDispatcher struct {
	failures atomic.Int32
	queue    *queuedDispatcher
}

func (d *flakyDispatcher) Post(task func()) error {
	if d.failures.Add(-1) >= 0 {
		return errors.New("the message queue is full")
	}
	return d.queue.Post(task)
}

// TestFailedCompletionPostIsRetriedWhileTheWindowLives pins that a capture
// whose result could not be posted at first still ends: it would otherwise
// stay in progress for the session, with the result card hidden.
func TestFailedCompletionPostIsRetriedWhileTheWindowLives(t *testing.T) {
	view, card, queue := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	flaky := &flakyDispatcher{queue: queue}
	flaky.failures.Store(2)
	view.dispatcher = flaky
	view.capture = func(CaptureRequest) CaptureResult {
		return CaptureResult{Path: `C:\Shots\one.png`}
	}

	view.NativeHotkey(globalShortcutHotkeyID)
	queue.runNext(t)

	if view.inProgress {
		t.Fatal("the capture stayed in progress")
	}
	if card.suspended {
		t.Fatal("the result card stayed hidden")
	}
}

// TestFailedCompletionPostStopsOnceTheWindowIsGone pins that the retry cannot
// hold up shutdown, which waits for the capture's goroutine.
func TestFailedCompletionPostStopsOnceTheWindowIsGone(t *testing.T) {
	view, _, queue := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	flaky := &flakyDispatcher{queue: queue}
	flaky.failures.Store(1 << 30)
	view.dispatcher = flaky
	var checks atomic.Int32
	view.windowAlive = func() bool { return checks.Add(1) < 3 }
	view.capture = func(CaptureRequest) CaptureResult { return CaptureResult{} }

	view.NativeHotkey(globalShortcutHotkeyID)

	finished := make(chan struct{})
	go func() {
		view.pending.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the capture goroutine kept retrying after the window was gone")
	}
}

//go:build windows

package screenshot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

func newHotkeyTestView(t *testing.T, configPath string) (*NativeView, *fakeHotkeyRegistrar) {
	t.Helper()
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: configPath})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	fake, ok := view.hotkeys.(*fakeHotkeyRegistrar)
	if !ok {
		t.Fatal("test view did not use the fake hotkey registrar")
	}
	return view, fake
}

// newConflictedHotkeyView builds a view whose registrar rejects every
// combination, simulating another application owning the shortcut.
func newConflictedHotkeyView(t *testing.T) *NativeView {
	t.Helper()
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	options, err := NewOptionsManager(optionsStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := preset.NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := preset.NewManager(repository, &value)
	if err != nil {
		t.Fatal(err)
	}
	conflicted := &fakeHotkeyRegistrar{applyFunc: func(shortcut Shortcut) error {
		return shortcutConflictError(shortcut)
	}}
	view := NewNativeView(manager, options, display.NewMonitor(), nil, nil, nil)
	view.hotkeyFactory = func(uintptr, int32) hotkeyRegistrar { return conflicted }
	if err := view.NativeCreate(parent); err != nil {
		t.Fatalf("a startup shortcut conflict must not fail view creation (AC-S11): %v", err)
	}
	t.Cleanup(view.NativeDestroy)
	return view
}

// TestNativeHotkeyInvokesSelectedCaptureMode is SCR-002: the global shortcut
// starts the currently selected capture mode exactly like Start Capture.
func TestNativeHotkeyInvokesSelectedCaptureMode(t *testing.T) {
	view, _ := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	var request CaptureRequest
	view.capture = func(got CaptureRequest) CaptureResult {
		request = got
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	if view.NativeHotkey(9876) {
		t.Fatal("an unknown hotkey identifier was consumed")
	}
	if request.DeviceName != "" {
		t.Fatal("an unknown hotkey identifier started a capture")
	}
	if !view.NativeHotkey(globalShortcutHotkeyID) {
		t.Fatal("the capture hotkey was not consumed")
	}
	dispatcher.runNext(t)
	if request.DeviceName == "" {
		t.Fatal("the hotkey did not start a Full Display capture")
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") {
		t.Fatalf("status after hotkey capture = %q", got)
	}
}

// TestNativeHotkeyDuringSessionBringsAttention is SCR-005: invoking the
// global shortcut while a selection is on screen draws attention to the
// existing session instead of stacking a second capture overlay.
func TestNativeHotkeyDuringSessionBringsAttention(t *testing.T) {
	view, _ := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	log, native, _ := fakeOverlaySession(view)

	captureCalled := false
	view.captureRectangle = func(display.Rect, func(), CaptureOptions) CaptureResult {
		captureCalled = true
		return CaptureResult{}
	}

	view.NativeAction()
	if !view.sessions.Active() {
		t.Fatal("session did not start")
	}
	if !view.NativeHotkey(globalShortcutHotkeyID) {
		t.Fatal("the hotkey was not consumed during the session")
	}
	if native.attention != 1 {
		t.Fatalf("session Attention calls = %d, want 1", native.attention)
	}
	if captureCalled {
		t.Fatal("the hotkey started a capture while the session was active")
	}
	if len(*log) != 2 {
		t.Fatalf("the hotkey changed session state: %v, want [hide create]", *log)
	}
	view.sessions.Cancel()
}

func TestNativeViewShortcutCommitAppliesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	view, fake := newHotkeyTestView(t, path)

	view.shortcut.Edit.SetText("ctrl+shift+f5")
	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)

	want, err := ParseShortcut("Ctrl+Shift+F5")
	if err != nil {
		t.Fatal(err)
	}
	if got := view.options.Options().Shortcut; got != want {
		t.Fatalf("committed shortcut = %+v, want %+v", got, want)
	}
	if got := view.shortcut.Edit.Text(); got != "Ctrl+Shift+F5" {
		t.Fatalf("field text = %q, want the canonical form", got)
	}
	if got := view.shortcut.Error.Text(); got != "" {
		t.Fatalf("field error after successful commit = %q", got)
	}
	if len(fake.applied) == 0 || fake.applied[len(fake.applied)-1] != want {
		t.Fatalf("registrar applies = %+v, want the new combination last", fake.applied)
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") || !strings.Contains(got, "Ctrl+Shift+F5") {
		t.Fatalf("status after shortcut commit = %q", got)
	}

	// The change persists and takes effect without reinstalling (SCR-004):
	// a restart loads the new combination.
	restarted := settings.NewStore(settings.Paths{ConfigFile: path})
	restartedOptions, err := NewOptionsStore(restarted, testFolder(t))
	if err != nil {
		t.Fatal(err)
	}
	loaded := restarted.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	options, err := restartedOptions.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if options.Shortcut != want {
		t.Fatalf("persisted shortcut = %+v, want %+v", options.Shortcut, want)
	}
}

func TestNativeViewShortcutCommitRejectsInvalidText(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	before := len(fake.applied)

	view.shortcut.Edit.SetText("banana")
	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)

	if got := view.shortcut.Error.Text(); !strings.HasPrefix(got, "Validation error:") {
		t.Fatalf("field error for invalid shortcut = %q", got)
	}
	if got := view.options.Options().Shortcut; got != DefaultShortcut() {
		t.Fatalf("rejected edit changed the stored shortcut to %+v", got)
	}
	// The focus suspension is undone: the active combination is restored.
	if len(fake.applied) != before+1 || fake.applied[len(fake.applied)-1] != DefaultShortcut() {
		t.Fatalf("registrar applies = %+v, want the active combination restored", fake.applied)
	}
}

// TestNativeViewShortcutConflictKeepsOldShortcut is the AC-S11 flow through
// the view: a combination another application owns is reported as a
// registration conflict, the stored shortcut does not change, and the
// working combination is restored.
func TestNativeViewShortcutConflictKeepsOldShortcut(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	fake.applyFunc = func(shortcut Shortcut) error {
		if shortcut == DefaultShortcut() {
			return nil
		}
		return shortcutConflictError(shortcut)
	}

	view.shortcut.Edit.SetText("Ctrl+Shift+F9")
	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)

	if got := view.shortcut.Error.Text(); !strings.Contains(got, "Shortcut registration error:") ||
		!strings.Contains(got, "already registered") || !strings.Contains(got, "different shortcut") {
		t.Fatalf("field error for conflict = %q", got)
	}
	if got := view.status.Text(); !strings.Contains(got, "Shortcut registration error:") {
		t.Fatalf("status for conflict = %q", got)
	}
	if got := view.options.Options().Shortcut; got != DefaultShortcut() {
		t.Fatalf("conflicted edit changed the stored shortcut to %+v", got)
	}
	if len(fake.applied) == 0 || fake.applied[len(fake.applied)-1] != DefaultShortcut() {
		t.Fatalf("registrar applies = %+v, want the working combination restored", fake.applied)
	}
}

func TestNativeViewShortcutFieldSuspendsHotkeyWhileEditing(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	before := len(fake.applied)

	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditSetFocus)
	if fake.suspended != 1 {
		t.Fatalf("Suspend calls = %d, want 1 while the field is edited", fake.suspended)
	}
	// Committing the unchanged combination re-registers it.
	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)
	if len(fake.applied) != before+1 || fake.applied[len(fake.applied)-1] != DefaultShortcut() {
		t.Fatalf("registrar applies = %+v, want the combination restored after the edit", fake.applied)
	}
	if got := view.shortcut.Error.Text(); got != "" {
		t.Fatalf("unchanged commit left a field error: %q", got)
	}
}

// TestNativeViewShortcutCommitReportsRestoreFailure covers the path where
// the previous combination was claimed by another application in the
// meantime: the user must hear that the working shortcut was lost, not only
// that the new one failed to register.
func TestNativeViewShortcutCommitReportsRestoreFailure(t *testing.T) {
	view := newConflictedHotkeyView(t)

	view.shortcut.Edit.SetText("Ctrl+Shift+F9")
	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)

	if got := view.shortcut.Error.Text(); !strings.Contains(got, "Shortcut registration error:") ||
		!strings.Contains(got, "Ctrl+Alt+S") {
		t.Fatalf("field error when the working shortcut is lost = %q, want it to name Ctrl+Alt+S", got)
	}
	if got := view.options.Options().Shortcut; got != DefaultShortcut() {
		t.Fatalf("failed commit changed the stored shortcut to %+v", got)
	}
}

// TestNativeViewAnnouncesItsStartupNotice pins that the view announces its
// notice when the shell asks once the main window appears, as a startup
// shortcut conflict is shown while the window is hidden; that a conflict a
// later notice replaced in the status area, as a display failure at startup
// does in Full Display mode, is announced from beside the field instead; and
// that it has nothing to announce with nothing shown.
func TestNativeViewAnnouncesItsStartupNotice(t *testing.T) {
	view := newConflictedHotkeyView(t)
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)

	view.NativeAnnounceNotice()
	if got := watch.Count(view.status.Handle()); got != 1 {
		t.Fatalf("announcements of the startup conflict = %d, want 1", got)
	}
	if got := watch.Count(view.shortcut.Error.Handle()); got != 0 {
		t.Fatalf("the conflict the status area announced was announced %d time(s) beside the field too", got)
	}

	// A display failure takes the status area, as refreshDisplays reports it.
	_ = view.status.Show(ui.Notice{Kind: ui.NoticeError, Message: "The displays could not be listed."}, 0)
	view.NativeAnnounceNotice()
	if got := watch.Count(view.shortcut.Error.Handle()); got != 1 {
		t.Fatalf("announcements of the conflict the display failure replaced = %d, want 1 from beside the field", got)
	}

	view.status.Clear()
	view.shortcut.Error.SetText("")
	view.NativeAnnounceNotice()
	if got := watch.Count(view.status.Handle()); got != 3 {
		t.Fatalf("status announcements after the status area was cleared = %d, want still 3", got)
	}
	if got := watch.Count(view.shortcut.Error.Handle()); got != 1 {
		t.Fatalf("field announcements after the field was cleared = %d, want still 1", got)
	}
}

// TestNativeViewAnnouncesAShortcutFailureOnce pins that a registration
// conflict, shown both beside the shortcut field and in the status area, is
// announced once, by the status area, rather than read twice in a row.
func TestNativeViewAnnouncesAShortcutFailureOnce(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	fake.applyFunc = func(shortcut Shortcut) error {
		if shortcut == DefaultShortcut() {
			return nil
		}
		return shortcutConflictError(shortcut)
	}
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)

	view.shortcut.Edit.SetText("Ctrl+Shift+F9")
	view.NativeCommand(optionsCommandBase+shortcutCommand, ui.NotificationEditKillFocus)
	if got := view.shortcut.Error.Text(); !strings.Contains(got, "Shortcut registration error:") {
		t.Fatalf("field error for conflict = %q", got)
	}
	if got := watch.Count(view.status.Handle()); got != 1 {
		t.Fatalf("status announcements of the conflict = %d, want 1", got)
	}
	if got := watch.Count(view.shortcut.Error.Handle()); got != 0 {
		t.Fatalf("field announcements of the conflict = %d, want 0, as the status area announces it", got)
	}
}

// TestNativeViewDestroyReleasesHotkey is SCR-006 and the shortcut half of
// AC-A05: exiting releases the global shortcut so no registration outlives
// the application.
func TestNativeViewDestroyReleasesHotkey(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	options, err := NewOptionsManager(optionsStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := preset.NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := preset.NewManager(repository, &value)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeHotkeyRegistrar{}
	view := NewNativeView(manager, options, nil, nil, nil, nil)
	view.hotkeyFactory = func(uintptr, int32) hotkeyRegistrar { return fake }
	if err := view.NativeCreate(parent); err != nil {
		t.Fatal(err)
	}

	view.NativeDestroy()

	if fake.released != 1 {
		t.Fatalf("Release calls = %d, want 1 on destroy", fake.released)
	}
	if len(fake.applied) != 1 || fake.applied[0] != DefaultShortcut() {
		t.Fatalf("startup applies = %+v, want the default combination", fake.applied)
	}
}

// TestNativeViewStartupConflictIsReported is AC-S11 at startup: when another
// application already owns the configured shortcut, the failure is reported
// beside the field and in the status area, and the application keeps
// running so the user can choose a replacement.
func TestNativeViewStartupConflictIsReported(t *testing.T) {
	view := newConflictedHotkeyView(t)

	if got := view.shortcut.Error.Text(); !strings.Contains(got, "Shortcut registration error:") {
		t.Fatalf("field error after startup conflict = %q", got)
	}
	if got := view.status.Text(); !strings.Contains(got, "Shortcut registration error:") || !strings.Contains(got, "Ctrl+Alt+S") {
		t.Fatalf("status after startup conflict = %q", got)
	}
	// The stored shortcut is unchanged; committing it again retries the
	// registration once the other application has released it.
	if got := view.options.Options().Shortcut; got != DefaultShortcut() {
		t.Fatalf("startup conflict changed the stored shortcut to %+v", got)
	}
}

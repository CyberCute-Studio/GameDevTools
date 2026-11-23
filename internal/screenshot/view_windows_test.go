//go:build windows

package screenshot

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const wsOverlappedWindow = 0x00cf0000

var (
	testUser32          = windows.NewLazySystemDLL("user32.dll")
	procCreateWindowExW = testUser32.NewProc("CreateWindowExW")
	procDestroyWindow   = testUser32.NewProc("DestroyWindow")
	procIsWindowEnabled = testUser32.NewProc("IsWindowEnabled")
)

func newViewTestParent(t *testing.T) windows.Handle {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	className, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	windowName, err := windows.UTF16PtrFromString("GameDevTools screenshot view test")
	if err != nil {
		t.Fatal(err)
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
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

func newNativeViewForTest(t *testing.T, store *settings.Store, value *settings.Settings, folder string, parent windows.Handle) *NativeView {
	t.Helper()
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	options, err := NewOptionsManager(optionsStore, value)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := preset.NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := preset.NewManager(repository, value)
	if err != nil {
		t.Fatal(err)
	}
	capture := func(CaptureRequest) CaptureResult {
		return CaptureResult{}
	}
	captureRectangle := func(display.Rect, func(), CaptureOptions) CaptureResult {
		return CaptureResult{}
	}
	view := NewNativeView(manager, options, display.NewMonitor(), capture, captureRectangle, overlay.NewController(display.NewMonitor()))
	// View tests never touch the real global hotkey: RegisterHotKey
	// combinations are machine-global, so a test view would fail or succeed
	// depending on whatever the test machine happens to own. Registration
	// itself is covered by the Hotkey tests with unique combinations.
	view.hotkeyFactory = func(uintptr, int32) hotkeyRegistrar {
		return &fakeHotkeyRegistrar{}
	}
	view.card = &fakeResultCard{}
	if err := view.NativeCreate(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.NativeDestroy)
	return view
}

// fakeResultCard records what the view reports on the result card without
// putting a topmost window on the desktop.
type fakeResultCard struct {
	reports   []ui.CardEntry
	suspended bool
	suspends  int
	resumes   int
	moves     int
	destroyed bool
}

func (f *fakeResultCard) Report(notice ui.Notice, path string) {
	f.reports = append(f.reports, ui.CardEntry{Notice: notice, Path: path})
}
func (f *fakeResultCard) Suspend() { f.suspended = true; f.suspends++ }
func (f *fakeResultCard) Resume()  { f.suspended = false; f.resumes++ }
func (f *fakeResultCard) Reposition() {
	f.moves++
}
func (f *fakeResultCard) Take() []ui.CardEntry {
	var problems []ui.CardEntry
	for _, entry := range f.reports {
		if entry.Notice.Kind >= ui.NoticeWarning {
			problems = append(problems, entry)
		}
	}
	f.reports = nil
	return problems
}
func (f *fakeResultCard) Destroy() { f.destroyed = true }

// fakeHotkeyRegistrar records the view's hotkey calls without touching the
// machine-global RegisterHotKey state. applyFunc can fail specific
// combinations, such as a simulated conflict with another application.
type fakeHotkeyRegistrar struct {
	applied   []Shortcut
	suspended int
	released  int
	applyFunc func(Shortcut) error
}

func (f *fakeHotkeyRegistrar) Apply(shortcut Shortcut) error {
	if f.applyFunc != nil {
		if err := f.applyFunc(shortcut); err != nil {
			return err
		}
	}
	f.applied = append(f.applied, shortcut)
	return nil
}

func (f *fakeHotkeyRegistrar) Suspend() { f.suspended++ }
func (f *fakeHotkeyRegistrar) Release() { f.released++ }

func TestNativeViewDefaultsAndModeSelection(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	options := view.options.Options()
	if options.Mode != ModeFreeRectangle || options.Format != FormatPNG || options.JPEGQuality != 90 || options.OutputFolder != folder {
		t.Fatalf("default options = %+v", options)
	}
	if got := view.folder.Text(); got != folder {
		t.Fatalf("folder display = %q, want %q", got, folder)
	}
	if got := view.pattern.Text(); got != "Screenshot_yyyyMMdd_HHmmssfff.png" {
		t.Fatalf("pattern display = %q", got)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled != 0 {
		t.Fatal("JPEG quality is enabled while PNG is selected")
	}
	if got := view.quality.Edit.Text(); got != "90" {
		t.Fatalf("quality text = %q", got)
	}

	view.NativeCommand(optionsCommandBase+modePresetCommand, 0)
	if view.options.Options().Mode != ModePresetRectangle {
		t.Fatalf("mode after preset radio = %q", view.options.Options().Mode)
	}
	if got := view.status.Text(); !strings.Contains(got, "Preset Rectangle") {
		t.Fatalf("status after mode change = %q", got)
	}
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	if view.options.Options().Mode != ModeFullDisplay {
		t.Fatalf("mode after full display radio = %q", view.options.Options().Mode)
	}

	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(restartedStore)
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restarted := newNativeViewForTest(t, restartedStore, &loaded.Settings, folder, parent)
	if restarted.options.Options().Mode != ModeFullDisplay {
		t.Fatalf("restored mode = %q", restarted.options.Options().Mode)
	}
}

func TestNativeViewFormatQualityAndPattern(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	view.format.Select(1)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)
	if view.options.Options().Format != FormatJPEG {
		t.Fatalf("format after JPEG selection = %q", view.options.Options().Format)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled == 0 {
		t.Fatal("JPEG quality stayed disabled after JPEG selection")
	}
	if got := view.pattern.Text(); got != "Screenshot_yyyyMMdd_HHmmssfff.jpg" {
		t.Fatalf("pattern after JPEG selection = %q", got)
	}

	view.quality.Edit.SetText("45")
	view.NativeCommand(optionsCommandBase+qualityCommand, ui.NotificationEditKillFocus)
	if view.options.Options().JPEGQuality != 45 {
		t.Fatalf("quality after commit = %d", view.options.Options().JPEGQuality)
	}
	if got := view.quality.Error.Text(); got != "" {
		t.Fatalf("quality error after valid commit = %q", got)
	}

	view.quality.Edit.SetText("0")
	view.NativeCommand(optionsCommandBase+qualityCommand, ui.NotificationEditKillFocus)
	if got := view.quality.Error.Text(); !strings.Contains(got, "between 1 and 100") {
		t.Fatalf("quality error for zero = %q", got)
	}
	if view.options.Options().JPEGQuality != 45 {
		t.Fatalf("rejected quality changed the option to %d", view.options.Options().JPEGQuality)
	}
	view.quality.Edit.SetText("101")
	view.NativeCommand(optionsCommandBase+qualityCommand, ui.NotificationEditKillFocus)
	if got := view.quality.Error.Text(); !strings.Contains(got, "between 1 and 100") {
		t.Fatalf("quality error for 101 = %q", got)
	}

	view.format.Select(0)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)
	if view.options.Options().Format != FormatPNG {
		t.Fatalf("format after PNG selection = %q", view.options.Options().Format)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled != 0 {
		t.Fatal("JPEG quality stayed enabled after PNG selection")
	}
	if got := view.pattern.Text(); got != "Screenshot_yyyyMMdd_HHmmssfff.png" {
		t.Fatalf("pattern after PNG selection = %q", got)
	}

	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(restartedStore)
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restarted := newNativeViewForTest(t, restartedStore, &loaded.Settings, folder, parent)
	restored := restarted.options.Options()
	if restored.Format != FormatPNG || restored.JPEGQuality != 45 {
		t.Fatalf("restored options = %+v", restored)
	}
	if got := restarted.quality.Edit.Text(); got != "45" {
		t.Fatalf("restored quality text = %q", got)
	}
}

func TestNativeViewBrowseForOutputFolder(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	chosen := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	cancelled := false
	view.browse = func(windows.Handle, string) (string, bool, error) {
		cancelled = true
		return "", false, nil
	}
	view.NativeCommand(optionsCommandBase+browseCommand, 0)
	if !cancelled {
		t.Fatal("browse action did not open the folder picker")
	}
	if view.options.Options().OutputFolder != folder {
		t.Fatalf("cancelled browse changed the folder to %q", view.options.Options().OutputFolder)
	}

	view.browse = func(windows.Handle, string) (string, bool, error) {
		return chosen, true, nil
	}
	view.NativeCommand(optionsCommandBase+browseCommand, 0)
	if view.options.Options().OutputFolder != chosen {
		t.Fatalf("folder after browse = %q, want %q", view.options.Options().OutputFolder, chosen)
	}
	if got := view.folder.Text(); got != chosen {
		t.Fatalf("folder display after browse = %q", got)
	}
	if got := view.status.Text(); !strings.Contains(got, chosen) {
		t.Fatalf("status after browse = %q", got)
	}

	view.browse = func(windows.Handle, string) (string, bool, error) {
		return "relative\\Screenshots", true, nil
	}
	view.NativeCommand(optionsCommandBase+browseCommand, 0)
	if view.options.Options().OutputFolder != chosen {
		t.Fatalf("relative picker result changed the folder to %q", view.options.Options().OutputFolder)
	}
	if got := view.status.Text(); !strings.Contains(got, "absolute") {
		t.Fatalf("status after invalid picker result = %q", got)
	}

	view.browse = func(windows.Handle, string) (string, bool, error) {
		return "", false, errors.New("dialog unavailable")
	}
	view.NativeCommand(optionsCommandBase+browseCommand, 0)
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("status after picker failure = %q", got)
	}
}

func TestNativeViewPersistenceFailureStaysInMemory(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	directory := t.TempDir()
	blocker := filepath.Join(directory, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(blocker, "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	view.NativeCommand(optionsCommandBase+modePresetCommand, 0)
	if view.options.Options().Mode != ModePresetRectangle {
		t.Fatal("failed save discarded the in-memory mode")
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Warning:") || !strings.Contains(got, "remains in memory") {
		t.Fatalf("status after failed save = %q", got)
	}
}

func TestNativeViewLayoutStacksRadiosAndExpandsForMessages(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	scale := func(value int32) int32 { return value }
	measure := func(text string, width int32) int32 {
		if text == "" {
			return 0
		}
		return 120
	}

	narrow := view.NativeHeight(400, scale, measure)
	wide := view.NativeHeight(600, scale, measure)
	if narrow != wide+52 {
		t.Fatalf("stacked radio rows should add two rows: narrow = %d, wide = %d", narrow, wide)
	}

	baseline := view.NativeHeight(600, scale, measure)
	view.quality.Error.SetText("A wrapped quality validation message")
	view.status.SetText("A wrapped persistence warning")
	expanded := view.NativeHeight(600, scale, measure)
	if expanded-baseline != 152 {
		t.Fatalf("height increase = %d, want 152", expanded-baseline)
	}
	view.NativeLayout(0, 0, 600, scale, measure)
	if view.quality.Error.Text() == "" || view.status.Text() == "" {
		t.Fatal("layout discarded validation or persistence text")
	}
}

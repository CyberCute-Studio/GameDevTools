//go:build windows

package imageresizer

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
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

// presetNameCommandID is the Image Resizer preset manager's name field
// (command base 300 + presetNameCommand). Any preset-namespace command
// refreshes the view's fitting-mode description, and an edit notification on
// the name field changes nothing else.
const presetNameCommandID = 300 + 2

func newViewTestParent(t *testing.T) windows.Handle {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	className, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	windowName, err := windows.UTF16PtrFromString("GameDevTools image resizer view test")
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

func newNativeViewForTest(t *testing.T, store *settings.Store, value *settings.Settings, folder string, parent windows.Handle) (*NativeView, *preset.Manager) {
	t.Helper()
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	options, err := NewOptionsManager(optionsStore, value)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := preset.NewRepository(shell.ModuleImageResizer, store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := preset.NewManager(repository, value)
	if err != nil {
		t.Fatal(err)
	}
	view := NewNativeView(manager, options)
	if err := view.NativeCreate(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.NativeDestroy)
	return view, manager
}

func TestNativeViewDefaultsAndCollisionSelection(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, _ := newNativeViewForTest(t, store, &value, folder, parent)

	options := view.options.Options()
	if options.Format != FormatKeepSource || options.Collision != CollisionRename || options.JPEGQuality != 90 || options.Background != "#FFFFFF" || options.OutputFolder != folder {
		t.Fatalf("default options = %+v", options)
	}
	if got := view.folder.Text(); got != folder {
		t.Fatalf("folder display = %q, want %q", got, folder)
	}
	if got := view.format.Selected(); got != 0 {
		t.Fatalf("format selection = %d, want Keep Source Format at index 0", got)
	}
	if got := view.collisionNote.Text(); !strings.HasPrefix(got, "Rename (default):") {
		t.Fatalf("collision description = %q", got)
	}
	if got := view.modeDescription.Text(); !strings.HasPrefix(got, "Fit (16 × 16):") {
		t.Fatalf("fitting-mode description = %q", got)
	}
	if got := view.naming.Text(); !strings.Contains(got, "keeps its source file's name") {
		t.Fatalf("naming behavior = %q", got)
	}

	view.NativeCommand(optionsCommandBase+collisionSkipCommand, 0)
	if view.options.Options().Collision != CollisionSkip {
		t.Fatalf("policy after skip radio = %q", view.options.Options().Collision)
	}
	if got := view.status.Text(); !strings.Contains(got, "Skip") {
		t.Fatalf("status after policy change = %q", got)
	}
	if got := view.collisionNote.Text(); !strings.Contains(got, "marked Skipped") {
		t.Fatalf("collision description after skip = %q", got)
	}
	view.NativeCommand(optionsCommandBase+collisionOverwriteCommand, 0)
	if view.options.Options().Collision != CollisionOverwrite {
		t.Fatalf("policy after overwrite radio = %q", view.options.Options().Collision)
	}
	if got := view.collisionNote.Text(); !strings.Contains(got, "confirm") || !strings.Contains(got, "never overwritten") {
		t.Fatalf("collision description after overwrite = %q", got)
	}

	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(restartedStore)
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restarted, _ := newNativeViewForTest(t, restartedStore, &loaded.Settings, folder, parent)
	if restarted.options.Options().Collision != CollisionOverwrite {
		t.Fatalf("restored policy = %q", restarted.options.Options().Collision)
	}
	if got := restarted.collisionNote.Text(); !strings.Contains(got, "confirm") {
		t.Fatalf("restored collision description = %q", got)
	}
}

func TestNativeViewFormatQualityAndBackground(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, _ := newNativeViewForTest(t, store, &value, folder, parent)

	// Keep Source Format can still produce JPEG output from JPEG sources, so
	// quality and background stay enabled; only a forced PNG output disables
	// them.
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled == 0 {
		t.Fatal("JPEG quality is disabled while Keep Source Format is selected")
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.background.Edit.Handle())); enabled == 0 {
		t.Fatal("background is disabled while Keep Source Format is selected")
	}

	view.format.Select(1)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)
	if view.options.Options().Format != FormatPNG {
		t.Fatalf("format after PNG selection = %q", view.options.Options().Format)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled != 0 {
		t.Fatal("JPEG quality stayed enabled after PNG selection")
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.background.Edit.Handle())); enabled != 0 {
		t.Fatal("background stayed enabled after PNG selection")
	}

	view.format.Select(2)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)
	if view.options.Options().Format != FormatJPEG {
		t.Fatalf("format after JPEG selection = %q", view.options.Options().Format)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled == 0 {
		t.Fatal("JPEG quality stayed disabled after JPEG selection")
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

	view.background.Edit.SetText("#ff8000")
	view.NativeCommand(optionsCommandBase+backgroundCommand, ui.NotificationEditKillFocus)
	if view.options.Options().Background != "#FF8000" {
		t.Fatalf("background after commit = %q", view.options.Options().Background)
	}
	if got := view.background.Edit.Text(); got != "#FF8000" {
		t.Fatalf("background field was not normalized: %q", got)
	}
	view.background.Edit.SetText("white")
	view.NativeCommand(optionsCommandBase+backgroundCommand, ui.NotificationEditKillFocus)
	if got := view.background.Error.Text(); !strings.Contains(got, "six hexadecimal digits") {
		t.Fatalf("background error for a color name = %q", got)
	}
	if view.options.Options().Background != "#FF8000" {
		t.Fatalf("rejected background changed the option to %q", view.options.Options().Background)
	}

	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(restartedStore)
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restarted, _ := newNativeViewForTest(t, restartedStore, &loaded.Settings, folder, parent)
	restored := restarted.options.Options()
	if restored.Format != FormatJPEG || restored.JPEGQuality != 45 || restored.Background != "#FF8000" {
		t.Fatalf("restored options = %+v", restored)
	}
	if got := restarted.quality.Edit.Text(); got != "45" {
		t.Fatalf("restored quality text = %q", got)
	}
	if got := restarted.background.Edit.Text(); got != "#FF8000" {
		t.Fatalf("restored background text = %q", got)
	}
}

func TestNativeViewRejectedFormatReselectsStoredFormat(t *testing.T) {
	parent := newViewTestParent(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	// Without a default folder every setter fails output-folder validation, so
	// the format change is rejected and the combo must fall back to the stored
	// format instead of showing a selection that was never applied.
	view, _ := newNativeViewForTest(t, store, &value, "", parent)

	view.format.Select(2)
	view.NativeCommand(optionsCommandBase+formatCommand, 1)
	if view.options.Options().Format != FormatKeepSource {
		t.Fatalf("format after rejected selection = %q, want %q", view.options.Options().Format, FormatKeepSource)
	}
	if got := view.format.Selected(); got != 0 {
		t.Fatalf("combo after rejected selection = %d, want the stored format at index 0", got)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.quality.Edit.Handle())); enabled == 0 {
		t.Fatal("JPEG quality enablement no longer matches the stored Keep Source Format")
	}
	if got := view.status.Text(); !strings.Contains(got, "output folder is required") {
		t.Fatalf("status after rejected selection = %q", got)
	}
}

func TestNativeViewModeDescriptionFollowsPresetSelection(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, manager := newNativeViewForTest(t, store, &value, folder, parent)

	stretch, err := manager.Create("Stretch goal", 640, 480, preset.ResizeStretch)
	if err != nil {
		t.Fatal(err)
	}
	view.refreshModeDescription()
	if got := view.modeDescription.Text(); !strings.HasPrefix(got, "Stretch (640 × 480):") || !strings.Contains(got, "distorted") {
		t.Fatalf("stretch description = %q", got)
	}

	if err := manager.Update(stretch.ID, "Crop goal", 640, 480, preset.ResizeCropToFill); err != nil {
		t.Fatal(err)
	}
	// The manager-level update changes the selected preset outside the view's
	// command flow; a preset-namespace command refreshes the description.
	view.NativeCommand(presetNameCommandID, ui.NotificationEditChange)
	if got := view.modeDescription.Text(); !strings.HasPrefix(got, "Crop to Fill (640 × 480):") || !strings.Contains(got, "trimmed edges") {
		t.Fatalf("crop description = %q", got)
	}
}

func TestNativeViewBrowseForOutputFolder(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	chosen := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, _ := newNativeViewForTest(t, store, &value, folder, parent)

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
		return "relative\\Resized", true, nil
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
	view, _ := newNativeViewForTest(t, store, &value, folder, parent)

	view.NativeCommand(optionsCommandBase+collisionSkipCommand, 0)
	if view.options.Options().Collision != CollisionSkip {
		t.Fatal("failed save discarded the in-memory collision policy")
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
	view, _ := newNativeViewForTest(t, store, &value, folder, parent)
	scale := func(value int32) int32 { return value }
	measure := func(text string, width int32) int32 {
		if text == "" {
			return 0
		}
		return 120
	}

	narrow := view.NativeHeight(400, scale, measure)
	wide := view.NativeHeight(600, scale, measure)
	// Two extra radio rows (2 × 26 = 52) in the options section and three
	// stacked source buttons instead of one row (3×36 + 2×8 − 36 = 88) at
	// the narrow width.
	if narrow != wide+140 {
		t.Fatalf("stacked rows should add 140: narrow = %d, wide = %d", narrow, wide)
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

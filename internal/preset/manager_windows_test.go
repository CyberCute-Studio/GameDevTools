//go:build windows

package preset

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

func TestNativeManagerCRUDIndependenceAndRestart(t *testing.T) {
	parent := newPresetTestParent(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(store)
	value := settings.Default()
	screenshot := newNativeManagerForTest(t, shell.ModuleScreenshot, store, &value, parent)
	resizer := newNativeManagerForTest(t, shell.ModuleImageResizer, store, &value, parent)

	setNativeForm(screenshot, "Shared name", "640", "480", -1)
	screenshot.NativeCommand(200+presetCreateCommand, 0)
	if len(screenshot.manager.library.Custom()) != 1 {
		t.Fatalf("Screenshot custom presets = %+v", screenshot.manager.library.Custom())
	}
	created := screenshot.manager.library.Custom()[0]
	if created.Width != 640 || created.Height != 480 || screenshot.manager.library.SelectedID() != created.ID {
		t.Fatalf("created Screenshot preset = %+v, selected = %q", created, screenshot.manager.library.SelectedID())
	}

	setNativeForm(resizer, "Shared name", "800", "600", 2)
	resizer.NativeCommand(300+presetCreateCommand, 0)
	if len(resizer.manager.library.Custom()) != 1 {
		t.Fatalf("Image Resizer custom presets = %+v", resizer.manager.library.Custom())
	}
	resizerCreated := resizer.manager.library.Custom()[0]
	if resizerCreated.ResizeMode != ResizeCropToFill {
		t.Fatalf("Image Resizer mode = %q", resizerCreated.ResizeMode)
	}

	setNativeForm(screenshot, "Renamed", "1280", "720", -1)
	screenshot.NativeCommand(200+presetUpdateCommand, 0)
	renamed, exists := screenshot.manager.library.Get(created.ID)
	if !exists || renamed.Name != "Renamed" || renamed.Width != 1280 || renamed.Height != 720 {
		t.Fatalf("renamed preset = %+v, exists = %v", renamed, exists)
	}

	selectNativePreset(t, screenshot, screenshot.manager.library.Presets()[0].ID)
	screenshot.NativeCommand(200+presetDuplicateCommand, 0)
	duplicated := screenshot.manager.library.Custom()[1]
	if duplicated.BuiltIn || duplicated.Name != "HD copy" || duplicated.Width != 1280 || duplicated.Height != 720 {
		t.Fatalf("built-in duplicate = %+v", duplicated)
	}

	selectNativePreset(t, screenshot, created.ID)
	screenshot.confirm = func(windows.Handle, string, string) bool { return false }
	screenshot.NativeCommand(200+presetDeleteCommand, 0)
	if _, exists := screenshot.manager.library.Get(created.ID); !exists {
		t.Fatal("preset was deleted without confirmation")
	}
	screenshot.confirm = func(windows.Handle, string, string) bool { return true }
	screenshot.remove.Focus()
	screenshot.NativeCommand(200+presetDeleteCommand, 0)
	if _, exists := screenshot.manager.library.Get(created.ID); exists {
		t.Fatal("confirmed preset deletion did not remove the preset")
	}
	getFocus := windows.NewLazySystemDLL("user32.dll").NewProc("GetFocus")
	if focus, _, _ := getFocus.Call(); windows.Handle(focus) != screenshot.list.Handle() {
		t.Fatalf("focus after deletion = %#x, want preset list %#x", focus, screenshot.list.Handle())
	}
	if _, exists := resizer.manager.library.Get(resizerCreated.ID); !exists || len(resizer.manager.library.Custom()) != 1 {
		t.Fatal("Screenshot deletion changed the Image Resizer library")
	}

	selectNativePreset(t, screenshot, duplicated.ID)
	selectNativePreset(t, resizer, resizerCreated.ID)
	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(restartedStore)
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restartedScreenshot := newNativeManagerForTest(t, shell.ModuleScreenshot, restartedStore, &loaded.Settings, parent)
	restartedResizer := newNativeManagerForTest(t, shell.ModuleImageResizer, restartedStore, &loaded.Settings, parent)
	if got := restartedScreenshot.manager.library.SelectedID(); got != duplicated.ID {
		t.Fatalf("restored Screenshot selection = %q, want %q", got, duplicated.ID)
	}
	if got := restartedResizer.manager.library.SelectedID(); got != resizerCreated.ID {
		t.Fatalf("restored Image Resizer selection = %q, want %q", got, resizerCreated.ID)
	}
	if selected, exists := restartedScreenshot.selected(); !exists || selected.ID != duplicated.ID || restartedScreenshot.name.Edit.Text() != "HD copy" {
		t.Fatalf("restored Screenshot UI selection = %+v, exists = %v, name = %q", selected, exists, restartedScreenshot.name.Edit.Text())
	}
	if selected, exists := restartedResizer.selected(); !exists || selected.ID != resizerCreated.ID || restartedResizer.name.Edit.Text() != "Shared name" || restartedResizer.mode.Selected() != 2 {
		t.Fatalf("restored Image Resizer UI selection = %+v, exists = %v, name = %q, mode = %d", selected, exists, restartedResizer.name.Edit.Text(), restartedResizer.mode.Selected())
	}
	if restartedScreenshot.manager.library.Custom()[0].Name != "HD copy" || restartedResizer.manager.library.Custom()[0].Name != "Shared name" {
		t.Fatal("restart did not preserve independent custom libraries")
	}
}

// listedTexts reads back the rows a preset manager's list shows.
func listedTexts(manager *NativeManager) []string {
	texts := make([]string, 0, manager.list.Count())
	for index := range manager.list.Count() {
		texts = append(texts, manager.list.ItemText(index))
	}
	return texts
}

// TestNativeManagerScreenshotPresetAbsentFromImageResizer pins AC-R15: a
// custom 640 × 480 Screenshot preset is not among the presets the Image
// Resizer lists — before or after a restart — until one is created there
// separately, which then stands on its own: deleting the Screenshot preset
// leaves it, in the open window and in the stored settings a restart reads.
// A manager's list changes only when a command in it rebuilds the list from
// its library, so the open Image Resizer list is read after the user selects
// a preset in it, which saves its library and rebuilds the list. That save
// rewrites the Image Resizer's stored section from its library, which would
// hide a Screenshot save that had dropped or changed that section, so each
// Screenshot change is read back by a restart before the Image Resizer saves
// again, and each Image Resizer save by a restart after it, which shows it
// kept the Screenshot's presets as they were.
func TestNativeManagerScreenshotPresetAbsentFromImageResizer(t *testing.T) {
	parent := newPresetTestParent(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(store)
	value := settings.Default()
	screenshot := newNativeManagerForTest(t, shell.ModuleScreenshot, store, &value, parent)
	resizer := newNativeManagerForTest(t, shell.ModuleImageResizer, store, &value, parent)
	resizerRows := listedTexts(resizer)
	row := fmt.Sprintf(ui.PresetListFormat, "Capture 640", ui.FormatDimensions(640, 480), "")
	// restart builds both managers again from the settings file, as the next
	// launch does, so what they list is what each module's presets saved, and
	// checks that the Screenshot list shows the 640 × 480 preset or not, as
	// shown says, and that the Image Resizer list is resizerWant.
	restart := func(when string, shown bool, resizerWant []string) (screenshot, resizer *NativeManager) {
		t.Helper()
		restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
		RegisterValidators(restartedStore)
		loaded := restartedStore.Load()
		if len(loaded.Warnings) != 0 {
			t.Fatalf("restart %s: warnings = %v", when, loaded.Warnings)
		}
		screenshot = newNativeManagerForTest(t, shell.ModuleScreenshot, restartedStore, &loaded.Settings, parent)
		resizer = newNativeManagerForTest(t, shell.ModuleImageResizer, restartedStore, &loaded.Settings, parent)
		if got := listedTexts(screenshot); slices.Contains(got, row) != shown {
			t.Fatalf("restarted %s, the Screenshot list is %q; showing %q = %v, want %v", when, got, row, !shown, shown)
		}
		if got := listedTexts(resizer); !slices.Equal(got, resizerWant) {
			t.Fatalf("restarted %s, the Image Resizer list is %q, want %q", when, got, resizerWant)
		}
		return screenshot, resizer
	}

	setNativeForm(screenshot, "Capture 640", "640", "480", -1)
	screenshot.NativeCommand(200+presetCreateCommand, 0)
	if !slices.Contains(listedTexts(screenshot), row) {
		t.Fatalf("the Screenshot list %q does not show %q", listedTexts(screenshot), row)
	}
	restart("after the Screenshot preset was created", true, resizerRows)
	selectNativePreset(t, resizer, resizer.manager.library.SelectedID())
	if got := listedTexts(resizer); !slices.Equal(got, resizerRows) {
		t.Fatalf("the open Image Resizer list became %q, was %q", got, resizerRows)
	}
	restartedScreenshot, restartedResizer := restart("after the Image Resizer saved its presets", true, resizerRows)

	setNativeForm(restartedResizer, "Capture 640", "640", "480", 0)
	restartedResizer.NativeCommand(300+presetCreateCommand, 0)
	ownRows := listedTexts(restartedResizer)
	if !slices.Contains(ownRows, row) || len(ownRows) != len(resizerRows)+1 {
		t.Fatalf("the Image Resizer list after creating its own preset = %q", ownRows)
	}
	created := restartedScreenshot.manager.library.Custom()[0]
	selectNativePreset(t, restartedScreenshot, created.ID)
	restartedScreenshot.confirm = func(windows.Handle, string, string) bool { return true }
	restartedScreenshot.NativeCommand(200+presetDeleteCommand, 0)
	if slices.Contains(listedTexts(restartedScreenshot), row) {
		t.Fatal("the Screenshot preset was not deleted")
	}
	restart("after the Screenshot preset was deleted", false, ownRows)
	resizerCreated := restartedResizer.manager.library.Custom()[0]
	selectNativePreset(t, restartedResizer, resizerCreated.ID)
	if got := listedTexts(restartedResizer); !slices.Equal(got, ownRows) {
		t.Fatalf("deleting the Screenshot preset changed the open Image Resizer list to %q, was %q", got, ownRows)
	}
	restart("after the Image Resizer saved its presets again", false, ownRows)
}

func TestNativeManagerValidationAndBuiltInProtection(t *testing.T) {
	parent := newPresetTestParent(t)
	value := settings.Default()
	manager := newNativeManagerForTest(t, shell.ModuleScreenshot, settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")}), &value, parent)
	setNativeForm(manager, "HD", "640", "480", -1)
	manager.NativeCommand(200+presetCreateCommand, 0)
	if len(manager.manager.library.Custom()) != 0 || !strings.Contains(manager.name.Error.Text(), "already exists") {
		t.Fatalf("duplicate name error = %q, custom = %+v", manager.name.Error.Text(), manager.manager.library.Custom())
	}
	setNativeForm(manager, "Invalid", "0", "480", -1)
	manager.NativeCommand(200+presetCreateCommand, 0)
	if !strings.Contains(manager.width.Error.Text(), "greater than 0") {
		t.Fatalf("zero-dimension error = %q", manager.width.Error.Text())
	}
	setNativeForm(manager, "Invalid", "16385", "480", -1)
	manager.NativeCommand(200+presetCreateCommand, 0)
	if !strings.Contains(manager.width.Error.Text(), "16,384") {
		t.Fatalf("range error = %q", manager.width.Error.Text())
	}

	manager.NativeCommand(200+presetUpdateCommand, 0)
	manager.NativeCommand(200+presetDeleteCommand, 0)
	if len(manager.manager.library.Presets()) != 4 || !strings.Contains(manager.status.Text(), "built-in") {
		t.Fatalf("built-in mutation result = %q, presets = %+v", manager.status.Text(), manager.manager.library.Presets())
	}
}

// TestNativeManagerAnnouncesEachRefusal pins that a refused Create has a
// screen reader read the field's message each time (UX-003), as the focus
// moves to the field: the message a field shows as it is typed is not
// announced again when Create refuses the same text, and a message Create
// itself sets is announced once.
func TestNativeManagerAnnouncesEachRefusal(t *testing.T) {
	parent := newPresetTestParent(t)
	value := settings.Default()
	manager := newNativeManagerForTest(t, shell.ModuleScreenshot, settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")}), &value, parent)
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)

	// Typing validates the field as it changes, which announces the message.
	setNativeForm(manager, "Typed", "12a", "480", -1)
	manager.NativeCommand(200+presetWidthCommand, ui.NotificationEditChange)
	if got := watch.Count(manager.width.Error.Handle()); got != 1 {
		t.Fatalf("announcements of the typed width's message = %d, want 1", got)
	}
	manager.NativeCommand(200+presetCreateCommand, 0)
	manager.NativeCommand(200+presetCreateCommand, 0)
	if got := watch.Count(manager.width.Error.Handle()); got != 3 {
		t.Fatalf("announcements after Create refused the width twice = %d, want 3", got)
	}

	// A message Create's own validation sets, as for a height emptied
	// without its field validating, is announced once, not twice.
	manager.width.Edit.SetText("640")
	manager.height.Edit.SetText("")
	manager.NativeCommand(200+presetCreateCommand, 0)
	if got := watch.Count(manager.height.Error.Handle()); got != 1 {
		t.Fatalf("announcements of the empty height Create refused = %d, want 1", got)
	}
	if len(manager.manager.library.Custom()) != 0 {
		t.Fatalf("a refused Create added %+v", manager.manager.library.Custom())
	}
}

func TestNativeManagerFailedSaveKeepsInMemoryWorkflow(t *testing.T) {
	parent := newPresetTestParent(t)
	destination := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	manager := newNativeManagerForTest(t, shell.ModuleScreenshot, settings.NewStore(settings.Paths{ConfigFile: destination}), &value, parent)
	setNativeForm(manager, "In memory", "640", "480", -1)
	manager.NativeCommand(200+presetCreateCommand, 0)
	if len(manager.manager.library.Custom()) != 1 {
		t.Fatal("failed save discarded the in-memory preset")
	}
	if !strings.Contains(manager.status.Text(), "remains in memory") {
		t.Fatalf("write-failure notice = %q", manager.status.Text())
	}
}

func TestNativeManagerLayoutExpandsForWrappedMessages(t *testing.T) {
	parent := newPresetTestParent(t)
	value := settings.Default()
	manager := newNativeManagerForTest(t, shell.ModuleScreenshot, settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")}), &value, parent)
	scale := func(value int32) int32 { return value }
	measure := func(text string, width int32) int32 {
		if text == "" {
			return 0
		}
		return 120
	}
	baseline := manager.NativeHeight(480, scale, measure)
	manager.width.Error.SetText("A wrapped dimension validation message")
	manager.status.SetText("A wrapped persistence warning")
	expanded := manager.NativeHeight(480, scale, measure)
	if expanded-baseline != 152 {
		t.Fatalf("height increase = %d, want 152", expanded-baseline)
	}
	manager.NativeLayout(0, 0, 480, scale, measure)
	if got := manager.width.Error.Text(); got == "" || manager.status.Text() == "" {
		t.Fatal("layout discarded validation or persistence text")
	}

	manager.width.Error.SetText("")
	manager.status.SetText("")
	manager.name.Error.SetText("A wrapped name validation message")
	if got := manager.NativeHeight(480, scale, measure) - baseline; got != 80 {
		t.Fatalf("name-only height increase = %d, want 80", got)
	}
}

func TestNativeManagerLayoutStacksNarrowControls(t *testing.T) {
	parent := newPresetTestParent(t)
	value := settings.Default()
	manager := newNativeManagerForTest(t, shell.ModuleScreenshot, settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")}), &value, parent)
	scale := func(value int32) int32 { return (value*129 + 48) / 96 }
	measure := func(string, int32) int32 { return 0 }

	narrowHeight := manager.NativeHeight(240, scale, measure)
	wideHeight := manager.NativeHeight(480, scale, measure)
	if narrowHeight <= wideHeight {
		t.Fatalf("narrow manager height = %d, wide manager height = %d; narrow controls were not stacked", narrowHeight, wideHeight)
	}
	manager.NativeLayout(0, 0, 240, scale, measure)
	getWindowRect := windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowRect")
	type windowBounds struct{ Left, Top, Right, Bottom int32 }
	controlBounds := func(handle windows.Handle) windowBounds {
		t.Helper()
		bounds := windowBounds{}
		result, _, err := getWindowRect.Call(uintptr(handle), uintptr(unsafe.Pointer(&bounds)))
		if result == 0 {
			t.Fatalf("GetWindowRect(%v): %v", handle, err)
		}
		return bounds
	}
	for name, handle := range map[string]windows.Handle{
		"width edit":       manager.width.Edit.Handle(),
		"height edit":      manager.height.Edit.Handle(),
		"create button":    manager.create.Handle(),
		"update button":    manager.update.Handle(),
		"duplicate button": manager.duplicate.Handle(),
		"delete button":    manager.remove.Handle(),
	} {
		bounds := controlBounds(handle)
		if got := bounds.Right - bounds.Left; got != 240 {
			t.Errorf("%s width = %d, want full narrow-layout width 240", name, got)
		}
	}
	top := controlBounds(manager.listLabel.Handle()).Top
	bottom := controlBounds(manager.status.Handle()).Bottom
	if got := bottom - top; got != narrowHeight {
		t.Fatalf("laid-out manager height = %d, calculated height = %d", got, narrowHeight)
	}
}

func newNativeManagerForTest(t *testing.T, module shell.ModuleID, store *settings.Store, value *settings.Settings, parent windows.Handle) *NativeManager {
	t.Helper()
	repository, err := NewRepository(module, store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(repository, value)
	if err != nil {
		t.Fatal(err)
	}
	native := NewNativeManager(manager)
	if err := native.NativeCreate(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.NativeDestroy)
	return native
}

func setNativeForm(manager *NativeManager, name, width, height string, mode int) {
	manager.name.Edit.SetText(name)
	manager.width.Edit.SetText(width)
	manager.height.Edit.SetText(height)
	if manager.mode != nil && mode >= 0 {
		manager.mode.Select(mode)
	}
}

func selectNativePreset(t *testing.T, manager *NativeManager, id string) {
	t.Helper()
	for index, candidate := range manager.listed {
		if candidate.ID == id {
			manager.list.Select(index)
			base := int32(200)
			if manager.manager.library.Module() == shell.ModuleImageResizer {
				base = 300
			}
			manager.NativeCommand(base+presetListCommand, 1)
			return
		}
	}
	t.Fatalf("preset %q is not listed", id)
}

func newPresetTestParent(t *testing.T) windows.Handle {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(func() {
		runtime.UnlockOSThread()
	})
	user32 := windows.NewLazySystemDLL("user32.dll")
	createWindow := user32.NewProc("CreateWindowExW")
	destroyWindow := user32.NewProc("DestroyWindow")
	className := windows.StringToUTF16Ptr("STATIC")
	title := windows.StringToUTF16Ptr("GameDevTools preset UI test")
	hwnd, _, err := createWindow.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0x00cf0000,
		0, 0, 800, 700,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW(test parent): %v", err)
	}
	t.Cleanup(func() {
		destroyWindow.Call(hwnd)
	})
	return windows.Handle(hwnd)
}

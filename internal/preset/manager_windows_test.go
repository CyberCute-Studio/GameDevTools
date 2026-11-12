//go:build windows

package preset

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
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

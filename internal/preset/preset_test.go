package preset

import (
	"errors"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

func TestBuiltInDefinitions(t *testing.T) {
	screenshot, err := BuiltIns(shell.ModuleScreenshot)
	if err != nil {
		t.Fatal(err)
	}
	wantScreenshot := []struct {
		name          string
		width, height int
	}{{"HD", 1280, 720}, {"Full HD", 1920, 1080}, {"QHD", 2560, 1440}, {"4K UHD", 3840, 2160}}
	if len(screenshot) != len(wantScreenshot) {
		t.Fatalf("Screenshot built-in count = %d, want %d", len(screenshot), len(wantScreenshot))
	}
	for index, want := range wantScreenshot {
		got := screenshot[index]
		if got.Name != want.name || got.Width != want.width || got.Height != want.height || !got.BuiltIn || got.ResizeMode != "" {
			t.Errorf("Screenshot built-in %d = %+v", index, got)
		}
	}

	resizer, err := BuiltIns(shell.ModuleImageResizer)
	if err != nil {
		t.Fatal(err)
	}
	wantDimensions := [][2]int{{16, 16}, {32, 32}, {64, 64}, {128, 128}, {256, 256}, {512, 512}, {1024, 1024}, {1920, 1080}}
	if len(resizer) != len(wantDimensions) {
		t.Fatalf("Image Resizer built-in count = %d, want %d", len(resizer), len(wantDimensions))
	}
	for index, want := range wantDimensions {
		got := resizer[index]
		if got.Width != want[0] || got.Height != want[1] || got.ResizeMode != ResizeFit || !got.BuiltIn {
			t.Errorf("Image Resizer built-in %d = %+v", index, got)
		}
	}
}

func TestValidationBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		module        shell.ModuleID
		presetName    string
		width, height int
		mode          ResizeMode
		wantError     string
	}{
		{name: "Screenshot maximum area", module: shell.ModuleScreenshot, presetName: "Large", width: 16384, height: 16384},
		{name: "resizer exact pixel limit", module: shell.ModuleImageResizer, presetName: "Limit", width: 10000, height: 10000, mode: ResizeFit},
		{name: "resizer above pixel limit", module: shell.ModuleImageResizer, presetName: "Too large", width: 10000, height: 10001, mode: ResizeFit, wantError: "100 megapixels"},
		{name: "resizer maximum axes above pixel limit", module: shell.ModuleImageResizer, presetName: "Too large", width: 16384, height: 16384, mode: ResizeFit, wantError: "100 megapixels"},
		{name: "zero width", module: shell.ModuleScreenshot, presetName: "Invalid", width: 0, height: 1, wantError: "between 1 and 16,384"},
		{name: "negative width", module: shell.ModuleScreenshot, presetName: "Invalid", width: -1, height: 1, wantError: "between 1 and 16,384"},
		{name: "excess width", module: shell.ModuleScreenshot, presetName: "Invalid", width: 16385, height: 1, wantError: "between 1 and 16,384"},
		{name: "zero height", module: shell.ModuleScreenshot, presetName: "Invalid", width: 1, height: 0, wantError: "between 1 and 16,384"},
		{name: "negative height", module: shell.ModuleScreenshot, presetName: "Invalid", width: 1, height: -1, wantError: "between 1 and 16,384"},
		{name: "excess height", module: shell.ModuleScreenshot, presetName: "Invalid", width: 1, height: 16385, wantError: "between 1 and 16,384"},
		{name: "empty name", module: shell.ModuleScreenshot, presetName: "", width: 1, height: 1, wantError: "name is required"},
		{name: "whitespace name", module: shell.ModuleScreenshot, presetName: " \t\n ", width: 1, height: 1, wantError: "name is required"},
		{name: "null in name", module: shell.ModuleScreenshot, presetName: "bad\x00name", width: 1, height: 1, wantError: "null character"},
		{name: "resizer missing mode", module: shell.ModuleImageResizer, presetName: "Invalid", width: 1, height: 1, wantError: "Choose Fit"},
		{name: "resizer invalid mode", module: shell.ModuleImageResizer, presetName: "Invalid", width: 1, height: 1, mode: "unknown", wantError: "Choose Fit"},
		{name: "resizer stretch", module: shell.ModuleImageResizer, presetName: "Stretch", width: 1, height: 1, mode: ResizeStretch},
		{name: "resizer crop", module: shell.ModuleImageResizer, presetName: "Crop", width: 1, height: 1, mode: ResizeCropToFill},
		{name: "Screenshot mode", module: shell.ModuleScreenshot, presetName: "Invalid", width: 1, height: 1, mode: ResizeFit, wantError: "do not have a resize fitting mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			library, err := NewLibrary(test.module, nil)
			if err != nil {
				t.Fatal(err)
			}
			created, err := library.Create(test.presetName, test.width, test.height, test.mode)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Create() error = %v", err)
				}
				if created.Name != strings.TrimSpace(test.presetName) || created.ID == "" || created.BuiltIn {
					t.Fatalf("Create() = %+v", created)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Create() error = %v, want text %q", err, test.wantError)
			}
		})
	}
}

func TestUniqueNamesAreTrimmedAndCaseInsensitive(t *testing.T) {
	library, err := NewLibrary(shell.ModuleScreenshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Create("  Custom Name  ", 640, 480, ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"custom name", " CUSTOM NAME ", " hd ", "Full HD"} {
		if _, err := library.Create(name, 1, 1, ""); !errors.Is(err, ErrDuplicateName) {
			t.Errorf("Create(%q) error = %v, want ErrDuplicateName", name, err)
		}
	}
	created, exists := library.Get(library.Custom()[0].ID)
	if !exists || created.Name != "Custom Name" {
		t.Fatalf("stored custom preset = %+v, exists = %v", created, exists)
	}
}

func TestBuiltInImmutabilityAndDuplication(t *testing.T) {
	library, err := NewLibrary(shell.ModuleImageResizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	builtIn := library.Presets()[0]
	if err := library.Rename(builtIn.ID, "Changed"); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Rename() error = %v, want ErrBuiltIn", err)
	}
	if err := library.Update(builtIn.ID, "Changed", 1, 1, ResizeStretch); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Update() error = %v, want ErrBuiltIn", err)
	}
	if err := library.Delete(builtIn.ID); !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("Delete() error = %v, want ErrBuiltIn", err)
	}

	duplicate, err := library.Duplicate(builtIn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID == builtIn.ID || duplicate.BuiltIn || duplicate.Name != builtIn.Name+" copy" || duplicate.Width != builtIn.Width || duplicate.Height != builtIn.Height || duplicate.ResizeMode != ResizeFit {
		t.Fatalf("Duplicate() = %+v, source = %+v", duplicate, builtIn)
	}
	second, err := library.Duplicate(builtIn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Name != builtIn.Name+" copy 2" {
		t.Fatalf("second duplicate name = %q", second.Name)
	}
	if len(library.Custom()) != 2 || len(library.Presets()) != 10 {
		t.Fatalf("custom = %d, all = %d", len(library.Custom()), len(library.Presets()))
	}
}

func TestRenameAndUpdateKeepStableIdentifier(t *testing.T) {
	library, err := NewLibrary(shell.ModuleScreenshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := library.Create("Before", 640, 480, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := library.Rename(created.ID, "After"); err != nil {
		t.Fatal(err)
	}
	if err := library.Update(created.ID, "After", 800, 600, ""); err != nil {
		t.Fatal(err)
	}
	updated, exists := library.Get(created.ID)
	if !exists || updated.ID != created.ID || updated.Name != "After" || updated.Width != 800 || updated.Height != 600 {
		t.Fatalf("updated preset = %+v, exists = %v", updated, exists)
	}
	if err := library.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, exists := library.Get(created.ID); exists {
		t.Fatal("deleted preset remains in library")
	}
	if err := library.Delete(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
}

func TestLibrariesHaveIndependentNamespaces(t *testing.T) {
	screenshot, err := NewLibrary(shell.ModuleScreenshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	resizer, err := NewLibrary(shell.ModuleImageResizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := screenshot.Create("Shared", 640, 480, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resizer.Create("Shared", 640, 480, ResizeFit); err != nil {
		t.Fatalf("same name in independent namespace: %v", err)
	}
	if err := screenshot.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if len(resizer.Custom()) != 1 || resizer.Custom()[0].Name != "Shared" {
		t.Fatalf("resizer customs changed through Screenshot: %+v", resizer.Custom())
	}
	if _, exists := resizer.Get(created.ID); exists {
		t.Fatal("Screenshot preset identifier leaked into Image Resizer")
	}
}

func TestReturnedPresetsAreCopies(t *testing.T) {
	library, err := NewLibrary(shell.ModuleScreenshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	all := library.Presets()
	all[0].Name = "Changed"
	builtIn, _ := library.Get(all[0].ID)
	if builtIn.Name != "HD" {
		t.Fatalf("built-in mutated through returned slice: %+v", builtIn)
	}
}

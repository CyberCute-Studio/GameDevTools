package preset

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

func TestRepositoryPersistsIndependentLibrariesAndStableIdentifiers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(store)
	screenshotRepository, err := NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	resizerRepository, err := NewRepository(shell.ModuleImageResizer, store)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	value.Modules["screenshot"] = json.RawMessage(`{"format":"png"}`)
	screenshotLibrary, err := screenshotRepository.Load(value)
	if err != nil {
		t.Fatal(err)
	}
	resizerLibrary, err := resizerRepository.Load(value)
	if err != nil {
		t.Fatal(err)
	}
	screenshotPreset, err := screenshotLibrary.Create("Shared name", 640, 480, "")
	if err != nil {
		t.Fatal(err)
	}
	resizerPreset, err := resizerLibrary.Create("Shared name", 800, 600, ResizeCropToFill)
	if err != nil {
		t.Fatal(err)
	}
	if err := screenshotRepository.Save(&value, screenshotLibrary); err != nil {
		t.Fatal(err)
	}
	if err := resizerRepository.Save(&value, resizerLibrary); err != nil {
		t.Fatal(err)
	}

	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(restartedStore)
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restartedScreenshot, err := NewRepository(shell.ModuleScreenshot, restartedStore)
	if err != nil {
		t.Fatal(err)
	}
	restartedResizer, err := NewRepository(shell.ModuleImageResizer, restartedStore)
	if err != nil {
		t.Fatal(err)
	}
	screenshotLibrary, err = restartedScreenshot.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	resizerLibrary, err = restartedResizer.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(screenshotLibrary.Custom()) != 1 || len(resizerLibrary.Custom()) != 1 {
		t.Fatalf("restored custom counts = %d and %d", len(screenshotLibrary.Custom()), len(resizerLibrary.Custom()))
	}
	restoredScreenshot := screenshotLibrary.Custom()[0]
	restoredResizer := resizerLibrary.Custom()[0]
	if restoredScreenshot.ID != screenshotPreset.ID || restoredResizer.ID != resizerPreset.ID {
		t.Fatalf("restored identifiers = %q and %q", restoredScreenshot.ID, restoredResizer.ID)
	}
	if restoredScreenshot.ResizeMode != "" || restoredResizer.ResizeMode != ResizeCropToFill {
		t.Fatalf("restored options = %q and %q", restoredScreenshot.ResizeMode, restoredResizer.ResizeMode)
	}
	var screenshotSection map[string]json.RawMessage
	if err := json.Unmarshal(loaded.Settings.Modules["screenshot"], &screenshotSection); err != nil {
		t.Fatal(err)
	}
	if string(screenshotSection["format"]) != `"png"` {
		t.Fatalf("unrelated module option was not preserved: %s", screenshotSection["format"])
	}
	var stored storedPresets
	if err := json.Unmarshal(screenshotSection[sectionKey], &stored); err != nil {
		t.Fatal(err)
	}
	for _, custom := range stored.Custom {
		if strings.HasPrefix(custom.ID, "builtin.") {
			t.Fatal("built-in definitions were persisted as custom data")
		}
	}

	if err := screenshotLibrary.Rename(screenshotPreset.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := restartedScreenshot.Save(&loaded.Settings, screenshotLibrary); err != nil {
		t.Fatal(err)
	}
	afterRename := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(afterRename)
	result := afterRename.Load()
	if len(result.Warnings) != 0 {
		t.Fatalf("rename restart warnings = %v", result.Warnings)
	}
	repository, _ := NewRepository(shell.ModuleScreenshot, afterRename)
	library, err := repository.Load(result.Settings)
	if err != nil {
		t.Fatal(err)
	}
	renamed, exists := library.Get(screenshotPreset.ID)
	if !exists || renamed.Name != "Renamed" {
		t.Fatalf("preset after rename = %+v, exists = %v", renamed, exists)
	}
}

func TestRepositoryPersistsEmptyCustomArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	repository, err := NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	library, err := repository.Load(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(&value, library); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"custom": []`) {
		t.Fatalf("empty custom library was not stored as an array: %s", data)
	}
	restarted := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(restarted)
	if loaded := restarted.Load(); len(loaded.Warnings) != 0 {
		t.Fatalf("empty library restart warnings = %v", loaded.Warnings)
	}
}

func TestMalformedStoredPresetsAreBackedUpAndBuiltInsSurvive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := settings.Default()
	original.Modules["screenshot"] = json.RawMessage(`{"presets":{"version":1,"custom":[{"id":"custom.bad","name":"Bad","width":0,"height":720}]}}`)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(store)
	loaded := store.Load()
	if len(loaded.Warnings) != 1 || !strings.Contains(loaded.Warnings[0], "backed up") {
		t.Fatalf("recovery warnings = %v", loaded.Warnings)
	}
	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	backedUp, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUp) != string(data) {
		t.Fatalf("backup differs from original\n got: %s\nwant: %s", backedUp, data)
	}
	repository, err := NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	library, err := repository.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(library.Custom()) != 0 || len(library.Presets()) != 4 {
		t.Fatalf("recovered library has %d custom and %d total presets", len(library.Custom()), len(library.Presets()))
	}
}

func TestRepositoryRejectsMalformedStoredData(t *testing.T) {
	tests := []struct {
		name    string
		module  shell.ModuleID
		section string
	}{
		{name: "malformed section", module: shell.ModuleScreenshot, section: `{"presets":`},
		{name: "null section", module: shell.ModuleScreenshot, section: `null`},
		{name: "null presets", module: shell.ModuleScreenshot, section: `{"presets":null}`},
		{name: "missing custom array", module: shell.ModuleScreenshot, section: `{"presets":{"version":1}}`},
		{name: "null custom array", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":null}}`},
		{name: "unsupported version", module: shell.ModuleScreenshot, section: `{"presets":{"version":2,"custom":[]}}`},
		{name: "unknown library field", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[],"extra":true}}`},
		{name: "unknown selected preset", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[],"selectedPresetId":"custom.missing"}}`},
		{name: "empty selected preset", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[],"selectedPresetId":""}}`},
		{name: "null selected preset", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[],"selectedPresetId":null}}`},
		{name: "unknown preset field", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1,"extra":true}]}}`},
		{name: "fractional dimension", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1.5,"height":1}]}}`},
		{name: "non-numeric dimension", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":"one","height":1}]}}`},
		{name: "zero dimension", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":0,"height":1}]}}`},
		{name: "empty name", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":" ","width":1,"height":1}]}}`},
		{name: "null in name", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"bad\u0000name","width":1,"height":1}]}}`},
		{name: "empty identifier", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"","name":"One","width":1,"height":1}]}}`},
		{name: "duplicate identifiers", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1},{"id":"custom.one","name":"Two","width":2,"height":2}]}}`},
		{name: "duplicate names", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1},{"id":"custom.two","name":" one ","width":2,"height":2}]}}`},
		{name: "built-in identifier collision", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"builtin.screenshot.hd","name":"Custom","width":1,"height":1}]}}`},
		{name: "built-in name collision", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":" hd ","width":1,"height":1}]}}`},
		{name: "Screenshot resize mode", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1,"resizeMode":"fit"}]}}`},
		{name: "Screenshot null resize mode", module: shell.ModuleScreenshot, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1,"resizeMode":null}]}}`},
		{name: "resizer missing mode", module: shell.ModuleImageResizer, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1}]}}`},
		{name: "resizer invalid mode", module: shell.ModuleImageResizer, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":1,"height":1,"resizeMode":"unknown"}]}}`},
		{name: "resizer pixel limit", module: shell.ModuleImageResizer, section: `{"presets":{"version":1,"custom":[{"id":"custom.one","name":"One","width":10000,"height":10001,"resizeMode":"fit"}]}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, err := NewRepository(test.module, settings.NewStore(settings.Paths{}))
			if err != nil {
				t.Fatal(err)
			}
			value := settings.Default()
			value.Modules[string(test.module)] = json.RawMessage(test.section)
			if _, err := repository.Load(value); err == nil {
				t.Fatal("Load() accepted malformed stored presets")
			}
		})
	}
}

func TestStoreSaveRunsPresetValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	RegisterValidators(store)
	value := settings.Default()
	value.Modules["image-resizer"] = json.RawMessage(`{"presets":{"version":1,"custom":[{"id":"custom.bad","name":"Bad","width":10000,"height":10001,"resizeMode":"fit"}]}}`)
	if err := store.Save(value); err == nil || !strings.Contains(err.Error(), "100 megapixels") {
		t.Fatalf("Save() error = %v, want preset pixel-limit validation", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid settings were written: %v", err)
	}
}

func TestFailedSaveKeepsCurrentInMemoryPresetData(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(settings.Paths{ConfigFile: destination})
	repository, err := NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	library, err := repository.Load(value)
	if err != nil {
		t.Fatal(err)
	}
	created, err := library.Create("In memory", 640, 480, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(&value, library); err == nil {
		t.Fatal("Save() succeeded over a directory")
	}
	reloaded, err := repository.Load(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := reloaded.Get(created.ID); !exists {
		t.Fatal("failed save discarded current in-memory preset data")
	}
}

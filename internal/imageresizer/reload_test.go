package imageresizer

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

// TestOptionsManagerReloadTakesTheSharedValue pins the options half of
// Restore Default Settings (SET-005): once the restore leaves the shared
// settings value without resize options, Reload returns the manager to the
// defaults and forgets a stored folder that only earlier builds accepted,
// whose warning no longer applies, saving nothing.
func TestOptionsManagerReloadTakesTheSharedValue(t *testing.T) {
	folder := testFolder(t)
	legacy := filepath.Join(t.TempDir(), "Resized.")
	section, err := json.Marshal(map[string]any{
		"resize": map[string]any{
			"version":      OptionsVersion,
			"format":       FormatJPEG,
			"collision":    CollisionSkip,
			"jpegQuality":  70,
			"background":   "#000000",
			"outputFolder": legacy,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	value.Modules[string(moduleID)] = section
	manager, err := NewOptionsManager(optionsStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	if manager.FolderWarning(nil) == "" {
		t.Fatal("a stored folder only earlier builds accepted gave no startup warning")
	}

	delete(value.Modules, string(moduleID))
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if got, want := manager.Options(), DefaultOptions(folder); got != want {
		t.Fatalf("options after reloading a value without resize options = %+v, want the defaults %+v", got, want)
	}
	if warning := manager.FolderWarning(nil); warning != "" {
		t.Fatalf("folder warning after the reload = %q, want none", warning)
	}
}

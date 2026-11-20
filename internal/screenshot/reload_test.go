package screenshot

import (
	"path/filepath"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

// TestOptionsManagerReloadTakesTheSharedValue pins the options half of
// Restore Default Settings (SET-005): once the restore leaves the shared
// settings value without capture options, Reload returns the manager to the
// defaults, and once the value stores options again it takes those, saving
// nothing either way.
func TestOptionsManagerReloadTakesTheSharedValue(t *testing.T) {
	folder := testFolder(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	manager, err := NewOptionsManager(optionsStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	shortcut, err := ParseShortcut("Ctrl+Shift+F9")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetMode(ModeFullDisplay); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetShortcut(shortcut); err != nil {
		t.Fatal(err)
	}
	stored := value.Modules[string(moduleID)]

	delete(value.Modules, string(moduleID))
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if got, want := manager.Options(), DefaultOptions(folder); got != want {
		t.Fatalf("options after reloading a value without capture options = %+v, want the defaults %+v", got, want)
	}

	value.Modules[string(moduleID)] = stored
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := manager.Options(); got.Mode != ModeFullDisplay || got.Shortcut != shortcut {
		t.Fatalf("options after reloading the stored section = %+v", got)
	}
}

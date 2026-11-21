package preset

import (
	"encoding/json"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

// TestManagerReloadReplacesOnlyAnotherLibrary pins how a module takes the
// preset library Restore Default Settings kept (SET-005): a shared settings
// value that stores the library the manager holds leaves it as it is, and
// one that stores another — the settings file's, kept when the session
// started from the defaults because the file could not be read — replaces
// it, its selected preset included.
func TestManagerReloadReplacesOnlyAnotherLibrary(t *testing.T) {
	store := settings.NewStore(settings.Paths{})
	repository, err := NewRepository(shell.ModuleImageResizer, store)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	manager, err := NewManager(repository, &value)
	if err != nil {
		t.Fatal(err)
	}
	held := manager.Library()

	if changed, err := manager.Reload(); err != nil || changed || manager.Library() != held {
		t.Fatalf("Reload() of the same library = %v, %v, want it left as it is", changed, err)
	}

	const id = "custom.0123456789abcdef0123456789abcdef"
	value.Modules[string(shell.ModuleImageResizer)] = json.RawMessage(`{"presets":{"version":1,"custom":[{"id":"` + id + `","name":"Icon","width":512,"height":512,"resizeMode":"crop-to-fill"}],"selectedPresetId":"` + id + `"}}`)
	changed, err := manager.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload() of another library = %v, %v, want it replaced", changed, err)
	}
	if custom := manager.Library().Custom(); len(custom) != 1 || custom[0].Name != "Icon" || manager.Library().SelectedID() != id {
		t.Fatalf("library after Reload() = %+v selecting %q, want the stored one", custom, manager.Library().SelectedID())
	}
	if changed, err := manager.Reload(); err != nil || changed {
		t.Fatalf("Reload() again = %v, %v, want nothing changed", changed, err)
	}
}

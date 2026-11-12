package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

func TestStoreFreshStartAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	store := NewStore(Paths{ConfigFile: path})
	fresh := store.Load()
	if len(fresh.Warnings) != 0 {
		t.Fatalf("fresh load warnings = %v", fresh.Warnings)
	}
	if fresh.Settings.Shell.SelectedModule != string(shell.ModuleScreenshot) {
		t.Fatalf("default module = %q", fresh.Settings.Shell.SelectedModule)
	}

	value := fresh.Settings
	value.Shell.SelectedModule = string(shell.ModuleImageResizer)
	value.Shell.Window = WindowSettings{X: -120, Y: 40, Width: 1440, Height: 900, Maximized: true, HasPosition: true}
	value.Modules["screenshot"] = json.RawMessage(`{"format":"png"}`)
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}

	restarted := NewStore(Paths{ConfigFile: path}).Load()
	if len(restarted.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", restarted.Warnings)
	}
	if restarted.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("restored module = %q", restarted.Settings.Shell.SelectedModule)
	}
	if restarted.Settings.Shell.Window != value.Shell.Window {
		t.Fatalf("restored window = %+v, want %+v", restarted.Settings.Shell.Window, value.Shell.Window)
	}
	var section struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(restarted.Settings.Modules["screenshot"], &section); err != nil || section.Format != "png" {
		t.Fatalf("module section = %s, err = %v", restarted.Settings.Modules["screenshot"], err)
	}
}

func TestStoreMalformedSettingsAreBackedUpAndRecovered(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte("{\"version\":1")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(directory, "source.png")
	imageData := []byte("user image bytes")
	if err := os.WriteFile(image, imageData, 0o600); err != nil {
		t.Fatal(err)
	}

	result := NewStore(Paths{ConfigFile: path}).Load()
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "backed up") {
		t.Fatalf("recovery warnings = %v", result.Warnings)
	}
	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	backedUp, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUp) != string(original) {
		t.Fatalf("backup = %q, want original %q", backedUp, original)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) == string(original) || !json.Valid(current) {
		t.Fatalf("replacement settings were not regenerated: %q", current)
	}
	untouched, err := os.ReadFile(image)
	if err != nil || string(untouched) != string(imageData) {
		t.Fatalf("saved image changed: %q, err = %v", untouched, err)
	}
}

func TestStoreFailedBackupProtectsOriginalAndBlocksSaving(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte("not json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(Paths{ConfigFile: path})
	store.now = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }
	blockedBackup := path + ".corrupt-20261111T120000.000000000Z"
	if err := os.Mkdir(blockedBackup, 0o700); err != nil {
		t.Fatal(err)
	}

	result := store.Load()
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "saving is disabled") {
		t.Fatalf("backup failure warnings = %v", result.Warnings)
	}
	if err := store.Save(Default()); err == nil || !strings.Contains(err.Error(), "recovery is incomplete") {
		t.Fatalf("Save() error = %v, want blocked recovery", err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(original) {
		t.Fatalf("original changed: %q, err = %v", current, err)
	}
}

func TestStoreUnavailablePathAndFailedWrite(t *testing.T) {
	t.Run("unavailable path", func(t *testing.T) {
		store := NewStore(Paths{})
		result := store.Load()
		if len(result.Warnings) != 1 {
			t.Fatalf("warnings = %v", result.Warnings)
		}
		if err := store.Save(Default()); err == nil {
			t.Fatal("Save() succeeded without a settings path")
		}
	})

	t.Run("unreadable existing path blocks saving", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "settings.json")
		if err := os.Mkdir(destination, 0o700); err != nil {
			t.Fatal(err)
		}
		store := NewStore(Paths{ConfigFile: destination})
		result := store.Load()
		if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "saving is disabled") {
			t.Fatalf("warnings = %v", result.Warnings)
		}
		if err := store.Save(Default()); err == nil || !strings.Contains(err.Error(), "recovery is incomplete") {
			t.Fatalf("Save() error = %v, want blocked recovery", err)
		}
	})

	t.Run("failed atomic replacement leaves no temporary file", func(t *testing.T) {
		directory := t.TempDir()
		destination := filepath.Join(directory, "settings.json")
		if err := os.Mkdir(destination, 0o700); err != nil {
			t.Fatal(err)
		}
		store := NewStore(Paths{ConfigFile: destination})
		if err := store.Save(Default()); err == nil {
			t.Fatal("Save() succeeded over a directory")
		}
		matches, err := filepath.Glob(filepath.Join(directory, ".settings-*.tmp"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("temporary files = %v, err = %v", matches, err)
		}
	})
}

func TestStoreMigrationFixture(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "settings-v0.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	result := NewStore(Paths{ConfigFile: path}, legacyMigration{}).Load()
	if len(result.Warnings) != 0 {
		t.Fatalf("migration warnings = %v", result.Warnings)
	}
	if result.Settings.Version != CurrentVersion || result.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("migrated settings = %+v", result.Settings)
	}
	if result.Settings.Shell.Window.X != -100 || result.Settings.Shell.Window.Width != 1280 {
		t.Fatalf("migrated window = %+v", result.Settings.Shell.Window)
	}
	backups, err := filepath.Glob(path + ".migration-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backups = %v, err = %v", backups, err)
	}
	backedUp, err := os.ReadFile(backups[0])
	if err != nil || string(backedUp) != string(fixture) {
		t.Fatalf("migration backup differs: %q, err = %v", backedUp, err)
	}
	persisted, err := os.ReadFile(path)
	if err != nil || !json.Valid(persisted) || string(persisted) == string(fixture) {
		t.Fatalf("migrated settings were not persisted: %q, err = %v", persisted, err)
	}
}

func TestStoreRejectsInvalidAndFutureDataWithoutLosingIt(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "unknown field", data: `{"version":1,"shell":{"selectedModule":"screenshot","window":{"width":1100,"height":700}},"unexpected":true}`},
		{name: "unknown module", data: `{"version":1,"shell":{"selectedModule":"other","window":{"width":1100,"height":700}}}`},
		{name: "empty module", data: `{"version":1,"shell":{"selectedModule":"","window":{"width":1100,"height":700}}}`},
		{name: "zero window width", data: `{"version":1,"shell":{"selectedModule":"screenshot","window":{"width":0,"height":700}}}`},
		{name: "zero window height", data: `{"version":1,"shell":{"selectedModule":"screenshot","window":{"width":1100,"height":0}}}`},
		{name: "non-object module payload", data: `{"version":1,"shell":{"selectedModule":"screenshot","window":{"width":1100,"height":700}},"modules":{"screenshot":"png"}}`},
		{name: "future version", data: `{"version":2,"shell":{"selectedModule":"screenshot","window":{"width":1100,"height":700}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			result := NewStore(Paths{ConfigFile: path}).Load()
			if len(result.Warnings) == 0 {
				t.Fatal("invalid settings loaded without a recovery warning")
			}
			backups, err := filepath.Glob(path + ".corrupt-*")
			if err != nil || len(backups) != 1 {
				t.Fatalf("backups = %v, err = %v", backups, err)
			}
			backedUp, err := os.ReadFile(backups[0])
			if err != nil || string(backedUp) != test.data {
				t.Fatalf("backup = %q, err = %v", backedUp, err)
			}
		})
	}
}

func TestStoreComposesModuleValidators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(Paths{ConfigFile: path})
	checked := make([]string, 0, 2)
	validator := func(name string, reject bool) ModuleValidator {
		return func(section json.RawMessage) error {
			checked = append(checked, name)
			if reject {
				return fmt.Errorf("%s rejected the section", name)
			}
			return nil
		}
	}
	store.RegisterModuleValidator("screenshot", validator("first", false))
	store.RegisterModuleValidator("screenshot", validator("second", false))

	value := Default()
	value.Modules["screenshot"] = json.RawMessage(`{"presets":{"version":1,"custom":[]}}`)
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 || checked[0] != "first" || checked[1] != "second" {
		t.Fatalf("validators that ran = %v, want first and second", checked)
	}

	checked = checked[:0]
	store.RegisterModuleValidator("screenshot", validator("third", true))
	if err := store.Save(value); err == nil || !strings.Contains(err.Error(), "third rejected the section") {
		t.Fatalf("Save() error = %v, want the failing validator", err)
	}
	if len(checked) != 3 {
		t.Fatalf("validators that ran = %v, want all three", checked)
	}

	checked = checked[:0]
	store.RegisterModuleValidator("screenshot", nil)
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	if len(checked) != 0 {
		t.Fatalf("validators that ran after clearing = %v", checked)
	}
}

type legacyMigration struct{}

func (legacyMigration) FromVersion() int { return 0 }

func (legacyMigration) Migrate(data json.RawMessage) (json.RawMessage, error) {
	var legacy struct {
		Module string `json:"module"`
		Window struct {
			X      int32 `json:"x"`
			Y      int32 `json:"y"`
			Width  int32 `json:"width"`
			Height int32 `json:"height"`
		} `json:"window"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	return json.Marshal(Settings{
		Version: CurrentVersion,
		Shell: ShellSettings{
			SelectedModule: legacy.Module,
			Window: WindowSettings{
				X: legacy.Window.X, Y: legacy.Window.Y, Width: legacy.Window.Width, Height: legacy.Window.Height, HasPosition: true,
			},
		},
	})
}

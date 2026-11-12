package screenshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

func TestOptionsStoreDefaultsWithoutSection(t *testing.T) {
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}

	options, err := optionsStore.Load(settings.Default())
	if err != nil {
		t.Fatal(err)
	}
	if options != DefaultOptions(folder) {
		t.Fatalf("options = %+v, want defaults with folder %q", options, folder)
	}

	value := settings.Default()
	value.Modules[string(shell.ModuleScreenshot)] = json.RawMessage(`{"presets":{"version":1,"custom":[]}}`)
	options, err = optionsStore.Load(value)
	if err != nil {
		t.Fatal(err)
	}
	if options != DefaultOptions(folder) {
		t.Fatalf("options with unrelated section keys = %+v", options)
	}
}

func TestOptionsStoreRoundTripAndPresetCoexistence(t *testing.T) {
	folder := testFolder(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(store)
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := preset.NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()

	presetManager, err := preset.NewManager(repository, &value)
	if err != nil {
		t.Fatal(err)
	}
	created, err := presetManager.Create("Store thumbnail", 640, 480, "")
	if err != nil {
		t.Fatal(err)
	}

	options := CaptureOptions{Mode: ModeFullDisplay, Format: FormatJPEG, JPEGQuality: 55, OutputFolder: folder}
	if err := optionsStore.Save(&value, options); err != nil {
		t.Fatal(err)
	}

	restartedStore := settings.NewStore(settings.Paths{ConfigFile: path})
	preset.RegisterValidators(restartedStore)
	restartedOptions, err := NewOptionsStore(restartedStore, folder)
	if err != nil {
		t.Fatal(err)
	}
	loaded := restartedStore.Load()
	if len(loaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", loaded.Warnings)
	}
	restored, err := restartedOptions.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if restored != options {
		t.Fatalf("restored options = %+v, want %+v", restored, options)
	}

	restartedRepository, err := preset.NewRepository(shell.ModuleScreenshot, restartedStore)
	if err != nil {
		t.Fatal(err)
	}
	library, err := restartedRepository.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if library.SelectedID() != created.ID || len(library.Custom()) != 1 || library.Custom()[0].Name != "Store thumbnail" {
		t.Fatalf("options save damaged the preset library: %+v, selected %q", library.Custom(), library.SelectedID())
	}

	if err := restartedRepository.Save(&loaded.Settings, library); err != nil {
		t.Fatal(err)
	}
	reloaded, err := restartedOptions.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded != options {
		t.Fatalf("preset save discarded capture options: %+v, want %+v", reloaded, options)
	}
}

func TestOptionsStoreRejectsMalformedSections(t *testing.T) {
	folder := testFolder(t)
	quoted, err := json.Marshal(folder)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		section string
		wantErr string
	}{
		{name: "section is not an object", section: `[1]`, wantErr: "JSON object"},
		{name: "duplicate section field", section: `{"capture":{},"capture":{}}`, wantErr: "duplicate"},
		{name: "duplicate capture field", section: `{"capture":{"version":1,"version":1,"mode":"free-rectangle","format":"png","jpegQuality":90,"outputFolder":` + string(quoted) + `}}`, wantErr: "duplicate"},
		{name: "trailing values", section: `{"capture":{}} null`, wantErr: "trailing"},
		{name: "unsupported version", section: `{"capture":{"version":2,"mode":"free-rectangle","format":"png","jpegQuality":90,"outputFolder":` + string(quoted) + `}}`, wantErr: "version"},
		{name: "unknown field", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"png","jpegQuality":90,"outputFolder":` + string(quoted) + `,"display":1}}`, wantErr: "unknown field"},
		{name: "trailing capture values", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"png","jpegQuality":90,"outputFolder":` + string(quoted) + `} {}}`, wantErr: "invalid character"},
		{name: "unknown mode", section: `{"capture":{"version":1,"mode":"window","format":"png","jpegQuality":90,"outputFolder":` + string(quoted) + `}}`, wantErr: "Capture mode"},
		{name: "unknown format", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"bmp","jpegQuality":90,"outputFolder":` + string(quoted) + `}}`, wantErr: "PNG or JPEG"},
		{name: "quality above range", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"png","jpegQuality":101,"outputFolder":` + string(quoted) + `}}`, wantErr: "between 1 and 100"},
		{name: "relative folder", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"png","jpegQuality":90,"outputFolder":"Screenshots"}}`, wantErr: "absolute"},
		{name: "missing folder", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"png","jpegQuality":90}}`, wantErr: "required"},
		{name: "surrogate escape in folder", section: `{"capture":{"version":1,"mode":"free-rectangle","format":"png","jpegQuality":90,"outputFolder":"C:\\Screenshots\ud800"}}`, wantErr: "not valid text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := decodeOptions(json.RawMessage(test.section))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("decodeOptions() error = %v, want text containing %q", err, test.wantErr)
			}
		})
	}
}

func TestOptionsStoreValidSection(t *testing.T) {
	folder := testFolder(t)
	quoted, err := json.Marshal(folder)
	if err != nil {
		t.Fatal(err)
	}
	section := json.RawMessage(`{"capture":{"version":1,"mode":"preset-rectangle","format":"jpeg","jpegQuality":40,"outputFolder":` + string(quoted) + `}}`)
	stored, exists, err := decodeOptions(section)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("stored capture options were not detected")
	}
	if stored.Mode != ModePresetRectangle || stored.Format != FormatJPEG || stored.JPEGQuality != 40 || stored.OutputFolder != folder {
		t.Fatalf("stored options = %+v", stored)
	}
}

func TestMalformedCaptureOptionsRecoverThroughStore(t *testing.T) {
	folder := testFolder(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte(`{"version":1,"shell":{"selectedModule":"screenshot","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}},"modules":{"screenshot":{"capture":{"version":1,"mode":"window","format":"png","jpegQuality":90,"outputFolder":"C:\\Screenshots"}}}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(settings.Paths{ConfigFile: path})
	if _, err := NewOptionsStore(store, folder); err != nil {
		t.Fatal(err)
	}

	loaded := store.Load()
	if len(loaded.Warnings) == 0 || !strings.Contains(loaded.Warnings[0], "backed up") {
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
	if string(backedUp) != string(original) {
		t.Fatalf("backup = %q, want original %q", backedUp, original)
	}
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	options, err := optionsStore.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if options != DefaultOptions(folder) {
		t.Fatalf("recovered options = %+v", options)
	}
}

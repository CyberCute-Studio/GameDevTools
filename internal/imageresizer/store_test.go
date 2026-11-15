package imageresizer

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
	value.Modules[string(shell.ModuleImageResizer)] = json.RawMessage(`{"presets":{"version":1,"custom":[]}}`)
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
	repository, err := preset.NewRepository(shell.ModuleImageResizer, store)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()

	presetManager, err := preset.NewManager(repository, &value)
	if err != nil {
		t.Fatal(err)
	}
	created, err := presetManager.Create("Icon", 512, 512, preset.ResizeCropToFill)
	if err != nil {
		t.Fatal(err)
	}

	options := ResizeOptions{Format: FormatJPEG, Collision: CollisionSkip, JPEGQuality: 55, Background: "#102030", OutputFolder: folder}
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

	restartedRepository, err := preset.NewRepository(shell.ModuleImageResizer, restartedStore)
	if err != nil {
		t.Fatal(err)
	}
	library, err := restartedRepository.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if library.SelectedID() != created.ID || len(library.Custom()) != 1 || library.Custom()[0].Name != "Icon" {
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
		t.Fatalf("preset save discarded resize options: %+v, want %+v", reloaded, options)
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
		{name: "duplicate section field", section: `{"resize":{},"resize":{}}`, wantErr: "duplicate"},
		{name: "duplicate resize field", section: `{"resize":{"version":1,"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `}}`, wantErr: "duplicate"},
		{name: "trailing values", section: `{"resize":{}} null`, wantErr: "trailing"},
		{name: "unsupported version", section: `{"resize":{"version":2,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `}}`, wantErr: "version"},
		{name: "unknown field", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `,"workers":4}}`, wantErr: "unknown field"},
		{name: "trailing resize values", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `} {}}`, wantErr: "invalid character"},
		{name: "unknown format", section: `{"resize":{"version":1,"format":"bmp","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `}}`, wantErr: "Keep Source Format, PNG, or JPEG"},
		{name: "unknown collision policy", section: `{"resize":{"version":1,"format":"png","collision":"delete","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `}}`, wantErr: "Rename, Skip, or Overwrite"},
		{name: "quality above range", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":101,"background":"#FFFFFF","outputFolder":` + string(quoted) + `}}`, wantErr: "between 1 and 100"},
		{name: "invalid background", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"white","outputFolder":` + string(quoted) + `}}`, wantErr: "six hexadecimal digits"},
		{name: "relative folder", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":"Resized"}}`, wantErr: "absolute"},
		{name: "missing folder", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF"}}`, wantErr: "required"},
		{name: "surrogate escape in folder", section: `{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":"C:\\Resized\ud800"}}`, wantErr: "not valid text"},
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
	section := json.RawMessage(`{"resize":{"version":1,"format":"jpeg","collision":"overwrite","jpegQuality":40,"background":"#102030","outputFolder":` + string(quoted) + `}}`)
	stored, exists, err := decodeOptions(section)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("stored resize options were not detected")
	}
	if stored.Format != FormatJPEG || stored.Collision != CollisionOverwrite || stored.JPEGQuality != 40 || stored.Background != "#102030" || stored.OutputFolder != folder {
		t.Fatalf("stored options = %+v", stored)
	}
}

func TestMalformedResizeOptionsRecoverThroughStore(t *testing.T) {
	folder := testFolder(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte(`{"version":1,"shell":{"selectedModule":"image-resizer","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}},"modules":{"image-resizer":{"resize":{"version":1,"format":"tiff","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":"C:\\Resized"}}}}`)
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

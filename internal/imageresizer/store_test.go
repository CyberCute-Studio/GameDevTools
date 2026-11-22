package imageresizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

func TestOptionsStoreDefaultsWithoutSection(t *testing.T) {
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}

	options, _, err := optionsStore.Load(settings.Default())
	if err != nil {
		t.Fatal(err)
	}
	if options != DefaultOptions(folder) {
		t.Fatalf("options = %+v, want defaults with folder %q", options, folder)
	}

	value := settings.Default()
	value.Modules[string(shell.ModuleImageResizer)] = json.RawMessage(`{"presets":{"version":1,"custom":[]}}`)
	options, _, err = optionsStore.Load(value)
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
	restored, replaced, err := restartedOptions.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if restored != options || replaced != "" {
		t.Fatalf("restored options = %+v, replaced folder %q, want %+v and none", restored, replaced, options)
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
	reloaded, _, err := restartedOptions.Load(loaded.Settings)
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
			_, err := decodeOptions(json.RawMessage(test.section))
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
	found, err := decodeOptions(section)
	if err != nil {
		t.Fatal(err)
	}
	if !found.exists || found.legacyFolder {
		t.Fatalf("stored resize options detected = %v, legacy folder = %v", found.exists, found.legacyFolder)
	}
	if stored := found.stored; stored.Format != FormatJPEG || stored.Collision != CollisionOverwrite || stored.JPEGQuality != 40 || stored.Background != "#102030" || stored.OutputFolder != folder {
		t.Fatalf("stored options = %+v", stored)
	}
}

// TestStoredFolderEarlierBuildsAcceptedFallsBackToTheDefault: earlier builds
// saved an output folder Windows resolves to another path, such as one whose
// own name ends in a space or a period, or with a folder name in its path
// that ends in a period, or that names only a volume or a server, or a device
// that holds no folders, such as a named pipe, or a server's pipe, mailslot,
// or IPC$ share, or a network path with no plain server and share name, such
// as \\server\..\x, which output planning now refuses.
// Loading such a file keeps every other setting, the preset library
// included, and only the folder falls back to the default, rather than the
// whole file being reset as invalid; the options manager reports the folder
// it replaced, so the application can say why the folder changed instead of
// forgetting the user's folder in silence.
func TestStoredFolderEarlierBuildsAcceptedFallsBackToTheDefault(t *testing.T) {
	folder := testFolder(t)
	// Outside Windows paths have no volume name, and every name is kept.
	var legacyFolders []string
	if volume := filepath.VolumeName(folder); volume != "" {
		legacyFolders = append(legacyFolders, folder+".", folder+" ", filepath.Join(folder+".", "Resized"),
			filepath.Join(folder, "NUL"), folder+"::$INDEX_ALLOCATION", filepath.Join(folder, "x:y"),
			`\\server\share.`, `\\?\`+volume, `\\?\UNC\server`, `\\.\pipe\Resized`,
			`\\localhost\pipe\Resized`, `\\server\mailslot\Resized`, `\\?\UNC\server\IPC$\Resized`,
			`\\server\..\x`, `\\\nas\photos`, `\\;x\nas\photos`, `\\nas\;x\photos`,
			`\??\`+volume, `\??\pipe`, `\??\UNC`, `\??\Volume{00000000-0000-0000-0000-000000000000}`)
	}
	for _, saved := range legacyFolders {
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
		if _, err := presetManager.Create("Icon", 512, 512, preset.ResizeCropToFill); err != nil {
			t.Fatal(err)
		}
		options := ResizeOptions{Format: FormatJPEG, Collision: CollisionSkip, JPEGQuality: 55, Background: "#102030", OutputFolder: folder}
		if err := optionsStore.Save(&value, options); err != nil {
			t.Fatal(err)
		}
		kept, err := NewOptionsManager(optionsStore, &value)
		if err != nil {
			t.Fatal(err)
		}
		if kept.Options() != options || kept.FolderWarning(nil) != "" {
			t.Fatalf("a valid stored folder loads as %+v, warning %q", kept.Options(), kept.FolderWarning(nil))
		}
		// Write the folder an earlier build would have saved.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		quoted, err := json.Marshal(folder)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(data), string(quoted), string(legacy), 1)
		if edited == string(data) {
			t.Fatalf("the saved settings do not hold the folder %s: %s", quoted, data)
		}
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}

		defaultFolder := testFolder(t)
		restarted := settings.NewStore(settings.Paths{ConfigFile: path})
		preset.RegisterValidators(restarted)
		restartedOptions, err := NewOptionsStore(restarted, defaultFolder)
		if err != nil {
			t.Fatal(err)
		}
		loaded := restarted.Load()
		if len(loaded.Warnings) != 0 {
			t.Fatalf("%q: load warnings = %v", saved, loaded.Warnings)
		}
		restored, replaced, err := restartedOptions.Load(loaded.Settings)
		if err != nil {
			t.Fatalf("%q: %v", saved, err)
		}
		want := options
		want.OutputFolder = defaultFolder
		if restored != want || replaced != saved {
			t.Fatalf("%q: restored options = %+v, replaced folder %q, want %+v", saved, restored, replaced, want)
		}
		manager, err := NewOptionsManager(restartedOptions, &loaded.Settings)
		if err != nil {
			t.Fatalf("%q: %v", saved, err)
		}
		if manager.Options() != want {
			t.Fatalf("%q: manager options = %+v", saved, manager.Options())
		}
		if warning := manager.FolderWarning(nil); warning != fmt.Sprintf(ui.ResizeFolderReplaced, saved) {
			t.Fatalf("%q: replaced folder warning = %q", saved, warning)
		}
		restartedRepository, err := preset.NewRepository(shell.ModuleImageResizer, restarted)
		if err != nil {
			t.Fatal(err)
		}
		library, err := restartedRepository.Load(loaded.Settings)
		if err != nil {
			t.Fatal(err)
		}
		if len(library.Custom()) != 1 || library.Custom()[0].Name != "Icon" {
			t.Fatalf("%q: the preset library was lost: %+v", saved, library.Custom())
		}
		// Saving another module's settings keeps validating the file.
		if err := restartedRepository.Save(&loaded.Settings, library); err != nil {
			t.Fatalf("%q: save presets: %v", saved, err)
		}
		if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) != 0 {
			t.Fatalf("%q: the settings file was backed up as invalid: %v", saved, backups)
		}
	}
}

// TestFolderWarning: the application shows one startup warning about the
// output folder, which asks the user to choose a folder once. When no
// default folder could be resolved, nothing takes the place of a stored
// folder only earlier builds accepted, so the warning says the default
// folder is unavailable too rather than that it is selected, and no second
// warning about the default folder follows. Once the user chooses a folder,
// there is no warning, as the shell's note shows the warning again with
// every save: in particular, a replaced folder no longer says the default
// folder is selected in its place.
func TestFolderWarning(t *testing.T) {
	folder := testFolder(t)
	if filepath.VolumeName(folder) == "" {
		t.Skip("outside Windows no folder only earlier builds accepted is refused by its name")
	}
	unavailable := errors.New("locate Pictures folder: not found")
	tests := []struct {
		name          string
		stored        string
		defaultFolder string
		defaultErr    error
		want          string
	}{
		{name: "valid stored folder", stored: folder, defaultFolder: folder, want: ""},
		{name: "valid stored folder without a default", stored: folder, defaultErr: unavailable, want: ""},
		{name: "replaced by the default", stored: folder + ".", defaultFolder: folder, want: fmt.Sprintf(ui.ResizeFolderReplaced, folder+".")},
		// The warning says why the default folder is unavailable, as it does
		// when no folder was stored.
		{name: "refused without a default", stored: folder + ".", defaultErr: unavailable, want: fmt.Sprintf(ui.ResizeFolderRefused, folder+".", unavailable)},
		{name: "no stored folder", defaultFolder: folder, want: ""},
		{name: "no stored folder and no default", defaultErr: unavailable, want: fmt.Sprintf(ui.ResizeFolderUnavailable, unavailable)},
		// No folder is selected though nothing says why, as when the store
		// was given no default folder and no error: the warnings still say
		// the default folder is unavailable, rather than formatting a nil
		// error or saying nothing.
		{name: "refused without a default or a reason", stored: folder + ".", want: fmt.Sprintf(ui.ResizeFolderRefused, folder+".", ui.ResizeDefaultFolderUnknown)},
		{name: "no stored folder, default, or reason", want: fmt.Sprintf(ui.ResizeFolderUnavailable, ui.ResizeDefaultFolderUnknown)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
			optionsStore, err := NewOptionsStore(store, test.defaultFolder)
			if err != nil {
				t.Fatal(err)
			}
			value := settings.Default()
			if test.stored != "" {
				quoted, err := json.Marshal(test.stored)
				if err != nil {
					t.Fatal(err)
				}
				value.Modules = map[string]json.RawMessage{
					string(moduleID): json.RawMessage(`{"resize":{"version":1,"format":"png","collision":"rename","jpegQuality":90,"background":"#FFFFFF","outputFolder":` + string(quoted) + `}}`),
				}
			}
			manager, err := NewOptionsManager(optionsStore, &value)
			if err != nil {
				t.Fatal(err)
			}
			warning := manager.FolderWarning(test.defaultErr)
			if warning != test.want {
				t.Fatalf("FolderWarning = %q, want %q", warning, test.want)
			}
			if strings.Contains(warning, "%!") || (test.defaultErr != nil && warning != "" && !strings.Contains(warning, test.defaultErr.Error())) {
				t.Fatalf("FolderWarning = %q, want one that says why the default folder is unavailable", warning)
			}
			if err := manager.SetOutputFolder(filepath.Join(folder, "Chosen")); err != nil {
				t.Fatal(err)
			}
			if warning := manager.FolderWarning(test.defaultErr); warning != "" {
				t.Fatalf("FolderWarning once a folder was chosen = %q, want none", warning)
			}
		})
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
	options, _, err := optionsStore.Load(loaded.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if options != DefaultOptions(folder) {
		t.Fatalf("recovered options = %+v", options)
	}
}

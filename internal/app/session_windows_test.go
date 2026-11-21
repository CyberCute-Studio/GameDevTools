//go:build windows

package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

// sessionFixture is a settings file a build that changed the settings
// format wrote, kept in testdata/settings, with what the current build must
// load from it.
type sessionFixture struct {
	name string
	// presets is whether the build stored custom presets, and selection
	// whether it stored each module's selected preset.
	presets, selection bool
	// capture and shortcut are whether it stored the capture options, and
	// their shortcut, and resize whether it stored the resize options.
	capture, shortcut, resize bool
	// legacyFolder is the stored resize output folder that only earlier
	// builds accepted, which loads as the default folder with a warning.
	legacyFolder string
}

// sessionFixtures holds a settings file from each build that changed what
// it writes; every build in between wrote the format of the one before it.
var sessionFixtures = []sessionFixture{
	{name: "fnd-04"},
	{name: "fnd-06", presets: true},
	{name: "fnd-07", presets: true, selection: true},
	{name: "scr-01", presets: true, selection: true, capture: true},
	{name: "scr-09", presets: true, selection: true, capture: true, shortcut: true},
	{name: "rsz-01", presets: true, selection: true, capture: true, shortcut: true, resize: true},
	{name: "rsz-01-legacy-folder", presets: true, selection: true, capture: true, shortcut: true, resize: true, legacyFolder: `D:\Art\Resized.`},
}

// openFixtureSession writes data as the settings file in a new folder and
// opens the session on it as the application starts, with default output
// folders in that folder too.
func openFixtureSession(t *testing.T, data []byte) (*session, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	if data != nil {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return reopenSession(t, path), path
}

// reopenSession opens the session on the settings file at path again, as a
// restart does.
func reopenSession(t *testing.T, path string) *session {
	t.Helper()
	directory := filepath.Dir(path)
	state, err := openSession(settings.Paths{ConfigFile: path}, nil, defaultFolders{
		screenshot: filepath.Join(directory, "Screenshots"),
		resizer:    filepath.Join(directory, "Resized"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// warnings returns the warnings the session gives as the options now hold
// them, as the shell's note lists them at startup (startupWarnings): the
// settings file's, and then the output folders'.
func (state *session) warnings() []string {
	return append(slices.Clone(state.settingsWarnings), state.folderWarnings()...)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "settings", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// libraries returns the session's two preset libraries as the modules load
// them.
func libraries(t *testing.T, state *session) (*preset.Library, *preset.Library) {
	t.Helper()
	capture, err := preset.NewManager(state.screenshotPresets, state.value)
	if err != nil {
		t.Fatal(err)
	}
	resize, err := preset.NewManager(state.resizerPresets, state.value)
	if err != nil {
		t.Fatal(err)
	}
	return capture.Library(), resize.Library()
}

// checkFixtureSession checks that the session holds what the fixture's
// build stored, or the defaults for what it did not.
func checkFixtureSession(t *testing.T, fixture sessionFixture, state *session, directory string) {
	t.Helper()
	window := settings.WindowSettings{X: -1600, Y: 80, Width: 1280, Height: 800, HasPosition: true}
	if got := state.value.Shell; got.SelectedModule != string(core.ModuleImageResizer) || got.Window != window {
		t.Errorf("shell settings = %+v, want Image Resizer and %+v", got, window)
	}

	captureLibrary, resizeLibrary := libraries(t, state)
	captureBuiltIns, err := preset.BuiltIns(core.ModuleScreenshot)
	if err != nil {
		t.Fatal(err)
	}
	resizeBuiltIns, err := preset.BuiltIns(core.ModuleImageResizer)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(captureLibrary.Presets()); got != len(captureBuiltIns)+boolCount(fixture.presets) {
		t.Errorf("Screenshot presets = %d, want every built-in and %d custom", got, boolCount(fixture.presets))
	}
	wantCaptureSelection, wantResizeSelection := captureBuiltIns[0].ID, resizeBuiltIns[0].ID
	if fixture.presets {
		custom := captureLibrary.Custom()
		if len(custom) != 1 || custom[0].Name != "Store thumbnail" || custom[0].Width != 640 || custom[0].Height != 480 {
			t.Errorf("Screenshot custom presets = %+v", custom)
		}
		resized := resizeLibrary.Custom()
		if len(resized) != 1 || resized[0].Name != "Icon" || resized[0].Width != 512 || resized[0].ResizeMode != preset.ResizeCropToFill {
			t.Errorf("Image Resizer custom presets = %+v", resized)
		}
		if fixture.selection && len(resized) == 1 {
			wantCaptureSelection, wantResizeSelection = captureBuiltIns[1].ID, resized[0].ID
		}
	}
	if got := captureLibrary.SelectedID(); got != wantCaptureSelection {
		t.Errorf("Screenshot selected preset = %q, want %q", got, wantCaptureSelection)
	}
	if got := resizeLibrary.SelectedID(); got != wantResizeSelection {
		t.Errorf("Image Resizer selected preset = %q, want %q", got, wantResizeSelection)
	}

	wantCapture := screenshot.DefaultOptions(filepath.Join(directory, "Screenshots"))
	if fixture.capture {
		wantCapture.Mode, wantCapture.Format, wantCapture.JPEGQuality, wantCapture.OutputFolder = screenshot.ModePresetRectangle, screenshot.FormatJPEG, 85, `D:\Captures`
	}
	if fixture.shortcut {
		shortcut, err := screenshot.ParseShortcut("Ctrl+Shift+F9")
		if err != nil {
			t.Fatal(err)
		}
		wantCapture.Shortcut = shortcut
	}
	if got := state.capture.Options(); got != wantCapture {
		t.Errorf("capture options = %+v, want %+v", got, wantCapture)
	}

	wantResize := imageresizer.DefaultOptions(filepath.Join(directory, "Resized"))
	if fixture.resize {
		wantResize.Format, wantResize.Collision, wantResize.JPEGQuality, wantResize.Background = imageresizer.FormatJPEG, imageresizer.CollisionSkip, 80, "#202020"
		if fixture.legacyFolder == "" {
			wantResize.OutputFolder = `D:\Art\Resized`
		}
	}
	if got := state.resize.Options(); got != wantResize {
		t.Errorf("resize options = %+v, want %+v", got, wantResize)
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

// TestOpenSessionUpgradesEverySettingsFormat pins the upgrade path (QLT-02):
// a settings file each earlier build wrote loads in the current build with
// every validator registered, as the application starts — with nothing
// reported, rewritten, or backed up, and every stored setting, preset, and
// selection kept — and survives a normal restart (AC-A01). A resize folder
// only earlier builds accepted is replaced by the default folder with a
// warning naming it, which the file keeps until the next save of the resize
// options, and no longer gives after that save.
func TestOpenSessionUpgradesEverySettingsFormat(t *testing.T) {
	for _, fixture := range sessionFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			data := readFixture(t, fixture.name)
			state, path := openFixtureSession(t, data)
			directory := filepath.Dir(path)
			if fixture.legacyFolder == "" {
				if len(state.warnings()) != 0 {
					t.Fatalf("load warnings = %v, want none", state.warnings())
				}
			} else if len(state.warnings()) != 1 || !strings.Contains(state.warnings()[0], fixture.legacyFolder) {
				t.Fatalf("load warnings = %v, want one naming %s", state.warnings(), fixture.legacyFolder)
			}
			if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, data) {
				t.Fatalf("loading rewrote the settings file: err = %v", err)
			}
			for _, kind := range []string{"corrupt", "migration", "reset"} {
				if backups, _ := filepath.Glob(path + "." + kind + "-*"); len(backups) != 0 {
					t.Fatalf("loading made %s backups %v", kind, backups)
				}
			}
			checkFixtureSession(t, fixture, state, directory)

			// A normal restart: the session saves as the window closes and
			// the next launch loads that file.
			if err := state.store.Save(*state.value); err != nil {
				t.Fatal(err)
			}
			restarted := reopenSession(t, path)
			if len(restarted.warnings()) != len(state.warnings()) {
				t.Fatalf("restart warnings = %v, want %v", restarted.warnings(), state.warnings())
			}
			checkFixtureSession(t, fixture, restarted, directory)

			if fixture.legacyFolder != "" {
				if err := restarted.resize.SetJPEGQuality(80); err != nil {
					t.Fatal(err)
				}
				if again := reopenSession(t, path); len(again.warnings()) != 0 {
					t.Fatalf("warnings once the resize options were saved = %v, want none", again.warnings())
				}
			}
		})
	}
}

// TestRefusedScreenshotFolderAloneRecordsNoRecovery pins that a sound settings
// file storing a Screenshot folder captures refuse warns the user without
// logging a settings recovery at every start, while any other startup warning
// beside it still records the event.
func TestRefusedScreenshotFolderAloneRecordsNoRecovery(t *testing.T) {
	stored := bytes.Replace(readFixture(t, "rsz-01"), []byte(`D:\\Captures`), []byte(`D:\\Captures.`), 1)
	state, _ := openFixtureSession(t, stored)
	startup := state.startupWarnings()
	if startup.text() != state.capture.FolderWarning() || startup.text() == "" {
		t.Fatalf("startup warnings = %q, want only the stored folder's", startup.text())
	}
	if state.recordsRecovery(startup) {
		t.Fatal("a refused Screenshot folder alone recorded a settings recovery")
	}
	startup.settingsFile = []string{"the settings file could not be read"}
	if !state.recordsRecovery(startup) {
		t.Fatal("a settings file warning beside the refused folder recorded no recovery")
	}
	clean, _ := openFixtureSession(t, readFixture(t, "rsz-01"))
	if clean.recordsRecovery(clean.startupWarnings()) {
		t.Fatal("a session without warnings recorded a settings recovery")
	}
}

// TestOpenSessionRecoversFromMalformedSettings pins recovery with every
// section owner's validator registered, as the application starts (SET-006,
// PRESET-008): settings malformed anywhere — the file, the shell, the
// capture options, either preset library, or the resize options — are
// backed up byte for byte, reported once with the backup's path, and
// replaced by defaults that keep both modules' built-in presets, which a
// restart then loads without a warning. Saved images beside the settings
// and in the output folders are not touched.
func TestOpenSessionRecoversFromMalformedSettings(t *testing.T) {
	valid := string(readFixture(t, "rsz-01"))
	replace := func(old, new string) string {
		t.Helper()
		if !strings.Contains(valid, old) {
			t.Fatalf("the fixture has no %q to replace", old)
		}
		return strings.Replace(valid, old, new, 1)
	}
	cases := []struct {
		name string
		data string
	}{
		{"truncated file", valid[:len(valid)/2]},
		{"trailing data", valid + "{}"},
		{"future version", replace(`"version": 1,
  "shell"`, `"version": 2,
  "shell"`)},
		{"window too small", replace(`"width": 1280`, `"width": 0`)},
		{"unknown module", replace(`"selectedModule": "image-resizer"`, `"selectedModule": "editor"`)},
		{"module section not an object", replace(`"screenshot": {`, `"screenshot": [], "unused": {`)},
		{"unknown capture mode", replace(`"mode": "preset-rectangle"`, `"mode": "panorama"`)},
		{"shortcut without a modifier", replace(`"shortcut": "Ctrl+Shift+F9"`, `"shortcut": "F9"`)},
		{"unknown capture field", replace(`"mode": "preset-rectangle"`, `"mode": "preset-rectangle", "delay": 3`)},
		{"duplicate preset names", replace(`"name": "Store thumbnail",`, `"name": "Full HD",`)},
		{"unknown selected preset", replace(`"selectedPresetId": "builtin.screenshot.full-hd"`, `"selectedPresetId": "builtin.screenshot.8k"`)},
		{"resize preset without a fitting mode", replace(`"resizeMode": "crop-to-fill"`, `"resizeMode": ""`)},
		{"unknown collision policy", replace(`"collision": "skip"`, `"collision": "merge"`)},
		{"malformed background", replace(`"background": "#202020"`, `"background": "#20202"`)},
		{"relative output folder", replace(`"outputFolder": "D:\\Art\\Resized"`, `"outputFolder": "Resized"`)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "settings.json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			images := map[string][]byte{
				filepath.Join(directory, "source.png"):                 []byte("source image"),
				filepath.Join(directory, "Screenshots", "capture.png"): []byte("saved capture"),
				filepath.Join(directory, "Resized", "icon.png"):        []byte("saved result"),
			}
			for image, data := range images {
				if err := os.MkdirAll(filepath.Dir(image), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(image, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			state := reopenSession(t, path)
			backups, err := filepath.Glob(path + ".corrupt-*")
			if err != nil || len(backups) != 1 {
				t.Fatalf("backups = %v, err = %v, want one", backups, err)
			}
			if len(state.warnings()) != 1 || !strings.Contains(state.warnings()[0], backups[0]) {
				t.Fatalf("load warnings = %v, want one naming the backup %s", state.warnings(), backups[0])
			}
			if copied, err := os.ReadFile(backups[0]); err != nil || string(copied) != test.data {
				t.Fatalf("backup differs from the malformed file: err = %v", err)
			}
			captureLibrary, resizeLibrary := libraries(t, state)
			if len(captureLibrary.Custom()) != 0 || len(resizeLibrary.Custom()) != 0 || len(captureLibrary.Presets()) == 0 || len(resizeLibrary.Presets()) == 0 {
				t.Fatalf("libraries after recovery: %d Screenshot and %d Image Resizer presets, %d and %d custom; want the built-ins only",
					len(captureLibrary.Presets()), len(resizeLibrary.Presets()), len(captureLibrary.Custom()), len(resizeLibrary.Custom()))
			}
			if got, want := state.capture.Options(), screenshot.DefaultOptions(filepath.Join(directory, "Screenshots")); got != want {
				t.Fatalf("capture options after recovery = %+v, want the defaults", got)
			}
			if got, want := state.resize.Options(), imageresizer.DefaultOptions(filepath.Join(directory, "Resized")); got != want {
				t.Fatalf("resize options after recovery = %+v, want the defaults", got)
			}
			if restarted := reopenSession(t, path); len(restarted.warnings()) != 0 {
				t.Fatalf("restart warnings = %v, want none", restarted.warnings())
			}
			for image, data := range images {
				if current, err := os.ReadFile(image); err != nil || !bytes.Equal(current, data) {
					t.Fatalf("saved image %s changed: err = %v", filepath.Base(image), err)
				}
			}
		})
	}
}

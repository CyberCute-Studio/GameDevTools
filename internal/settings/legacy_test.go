package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const legacyResizerSettings = `{"version":1,"shell":{"selectedModule":"image-resizer","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}},"modules":{"screenshot":{"format":"png"}}}`

// legacyPaths are settings paths in a temporary folder, with the file of a
// build from before the rename holding content.
func legacyPaths(t *testing.T, content string) Paths {
	t.Helper()
	root := t.TempDir()
	paths := Paths{
		ConfigFile:       filepath.Join(root, ui.AppName, "settings.json"),
		LegacyConfigFile: filepath.Join(root, legacyFolderName, "settings.json"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.LegacyConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyConfigFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return paths
}

func requireFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func TestLocalPathsNameTheProductAndTheLegacyFolder(t *testing.T) {
	paths, err := LocalPaths()
	if err != nil {
		t.Skipf("no per-user folders: %v", err)
	}
	if !strings.HasSuffix(paths.ConfigFile, filepath.Join(ui.AppName, "settings.json")) {
		t.Errorf("ConfigFile = %q", paths.ConfigFile)
	}
	if !strings.HasSuffix(paths.LogFile, filepath.Join(ui.AppName, "Logs", ui.AppFileName+".log")) {
		t.Errorf("LogFile = %q", paths.LogFile)
	}
	if want := filepath.Join(filepath.Dir(filepath.Dir(paths.ConfigFile)), "GameDevTools", "settings.json"); paths.LegacyConfigFile != want {
		t.Errorf("LegacyConfigFile = %q, want %q", paths.LegacyConfigFile, want)
	}
}

func TestStoreAdoptsLegacySettingsOnceAndLeavesThem(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	first := NewStore(paths).Load()
	if len(first.Warnings) != 0 {
		t.Fatalf("warnings = %v", first.Warnings)
	}
	if first.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("selected module = %q", first.Settings.Shell.SelectedModule)
	}
	if string(first.Settings.Modules["screenshot"]) != `{"format":"png"}` {
		t.Fatalf("module section = %s", first.Settings.Modules["screenshot"])
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatalf("the adopted settings were not written: %v", err)
	}
	requireFileContent(t, paths.LegacyConfigFile, legacyResizerSettings)

	// Once the settings file exists the old one is never read again, even
	// when an older build run since has changed it.
	store := NewStore(paths)
	value := store.Load().Settings
	value.Shell.SelectedModule = string(shell.ModuleSettings)
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyConfigFile, []byte(strings.ReplaceAll(legacyResizerSettings, "image-resizer", "screenshot")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewStore(paths).Load().Settings.Shell.SelectedModule; got != string(shell.ModuleSettings) {
		t.Fatalf("selected module after restart = %q", got)
	}
}

func TestStoreWithoutLegacySettingsStartsFresh(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	result := NewStore(paths).Load()
	if len(result.Warnings) != 0 || result.Settings.Shell.SelectedModule != string(shell.ModuleScreenshot) {
		t.Fatalf("load = %+v", result)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("a fresh start wrote a settings file: %v", err)
	}
}

// A legacy file that cannot be read must not be abandoned by a save creating
// the new file, which would stop it ever being adopted.
func TestStoreUnreadableLegacySettingsBlockSaving(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.LegacyConfigFile, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	result := store.Load()
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], paths.LegacyConfigFile) {
		t.Fatalf("warnings = %v, want one naming the legacy file", result.Warnings)
	}
	if err := store.Save(result.Settings); err == nil {
		t.Fatal("a save succeeded while the legacy settings were unread")
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("the settings file was created: %v", err)
	}
}

func TestStoreMalformedLegacySettingsAreRecoveredAndLeft(t *testing.T) {
	const malformed = `{"version":1,`
	paths := legacyPaths(t, malformed)
	result := NewStore(paths).Load()
	if result.Recovered == "" {
		t.Fatalf("no recovery warning: %v", result.Warnings)
	}
	if result.Settings.Shell.SelectedModule != string(shell.ModuleScreenshot) {
		t.Fatalf("selected module = %q", result.Settings.Shell.SelectedModule)
	}
	requireFileContent(t, paths.LegacyConfigFile, malformed)
	backups, err := filepath.Glob(paths.ConfigFile + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	requireFileContent(t, backups[0], malformed)
}

// When the adopted settings cannot be written the session still has them, and
// the next launch adopts them again.
func TestStoreAdoptionThatCannotBeWrittenIsRetried(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	blocker := filepath.Dir(paths.ConfigFile)
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result := NewStore(paths).Load()
	if result.Unwritten == "" || !strings.Contains(result.Unwritten, paths.LegacyConfigFile) {
		t.Fatalf("Unwritten = %q", result.Unwritten)
	}
	if result.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("selected module = %q", result.Settings.Shell.SelectedModule)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	retried := NewStore(paths).Load()
	if len(retried.Warnings) != 0 || retried.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("retry = %+v", retried)
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
}

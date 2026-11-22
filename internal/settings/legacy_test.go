package settings

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const legacyResizerSettings = `{"version":1,"shell":{"selectedModule":"image-resizer","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}},"modules":{"screenshot":{"format":"png"}}}`

// currentSettingsFile is a settings file at the new location, told from the
// legacy settings by its selected module.
const currentSettingsFile = `{"version":1,"shell":{"selectedModule":"settings","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}}}`

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

// unreadableLegacyStore is a store whose Load found no settings file and could
// not read the legacy one, which a folder in its place stands for, so saving
// is blocked.
func unreadableLegacyStore(t *testing.T) (*Store, Paths) {
	t.Helper()
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.LegacyConfigFile, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	store.Load()
	if err := store.Save(Default()); err == nil {
		t.Fatal("saving was not blocked")
	}
	return store, paths
}

// makeLegacyReadable puts the legacy settings back in place of the folder that
// stood for an unreadable file.
func makeLegacyReadable(t *testing.T, paths Paths) {
	t.Helper()
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyConfigFile, []byte(legacyResizerSettings), 0o600); err != nil {
		t.Fatal(err)
	}
}

// While the legacy file exists but cannot be read, a restore would create the
// new file without the presets it promises to keep and stop the old file ever
// being taken, so it changes nothing and names the file.
func TestStoreRestoreRefusesWhileTheLegacyFileIsUnreadable(t *testing.T) {
	store, paths := unreadableLegacyStore(t)
	_, backup, err := store.Restore(Default(), restoreTo(Default()))
	if !errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v, want ErrNotRestored", backup, err)
	}
	if !strings.Contains(err.Error(), paths.LegacyConfigFile) {
		t.Fatalf("the error does not name the legacy file: %v", err)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("the settings file was created: %v", err)
	}
	if err := store.Save(Default()); err == nil {
		t.Fatal("a refused restore unblocked saving")
	}
}

// Moving the unreadable legacy file away is the way out of its block: the
// restore then creates the new file.
func TestStoreRestoreClearsTheBlockOnceTheLegacyFileIsMovedAway(t *testing.T) {
	store, paths := unreadableLegacyStore(t)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	restored, backup, err := store.Restore(Default(), restoreTo(Default()))
	if err != nil || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v", backup, err)
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatalf("the restored settings were not written: %v", err)
	}
	if err := store.Save(restored); err != nil {
		t.Fatalf("saving is still blocked: %v", err)
	}
}

// A legacy file readable by the time of the restore is backed up, and its
// settings are the ones the restore derives from.
func TestStoreRestoreBacksUpALegacyFileThatBecameReadable(t *testing.T) {
	store, paths := unreadableLegacyStore(t)
	makeLegacyReadable(t, paths)
	_, backup, err := store.Restore(Default(), func(replaced Settings) (Settings, error) {
		if replaced.Shell.SelectedModule != string(shell.ModuleImageResizer) {
			t.Errorf("replaced module = %q, want the legacy file's", replaced.Shell.SelectedModule)
		}
		return Default(), nil
	})
	if err != nil || backup == "" {
		t.Fatalf("Restore() backup = %q, error = %v", backup, err)
	}
	requireFileContent(t, backup, legacyResizerSettings)
	requireFileContent(t, paths.LegacyConfigFile, legacyResizerSettings)
}

// A session that loaded the legacy file but could not back it up for its
// migration holds that file's settings with its own edits, so the restore
// derives from those and does not bring back what the session changed.
func TestStoreRestoreAfterABlockedMigrationKeepsTheSessionsEdits(t *testing.T) {
	legacy := strings.Replace(legacyResizerSettings, `"version":1`, `"version":0`, 1)
	paths := legacyPaths(t, legacy)
	store := NewStore(paths, appearingMigration{func() {}})
	store.readBack = func(string) ([]byte, error) { return nil, errors.New("backup unreadable") }
	result := store.Load()
	if err := store.Save(result.Settings); err == nil {
		t.Fatal("saving was not blocked")
	}
	store.readBack = os.ReadFile

	session := result.Settings
	session.Modules = map[string]json.RawMessage{"screenshot": json.RawMessage(`{"format":"jpeg"}`)}
	_, backup, err := store.Restore(session, func(replaced Settings) (Settings, error) {
		if got := string(replaced.Modules["screenshot"]); got != `{"format":"jpeg"}` {
			t.Errorf("replaced section = %s, want the session's", got)
		}
		return Default(), nil
	})
	if err != nil || backup == "" {
		t.Fatalf("Restore() backup = %q, error = %v", backup, err)
	}
	requireFileContent(t, backup, legacy)
	requireFileContent(t, paths.LegacyConfigFile, legacy)
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatalf("the restored settings were not written: %v", err)
	}
}

// A settings file the restore did not see, as one on a share that was
// reported missing, is neither replaced nor unprotected.
func TestStoreRestoreFromALegacyBlockLeavesASettingsFileThatAppeared(t *testing.T) {
	store, paths := unreadableLegacyStore(t)
	makeLegacyReadable(t, paths)
	_, backup, err := store.Restore(Default(), func(Settings) (Settings, error) {
		if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
			t.Fatal(err)
		}
		return Default(), nil
	})
	if !errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v, want ErrNotRestored", backup, err)
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
	if copies, _ := filepath.Glob(paths.ConfigFile + ".reset-*"); len(copies) != 0 {
		t.Fatalf("backups left behind: %v", copies)
	}
	if err := store.Save(Default()); err == nil {
		t.Fatal("a failed restore unblocked saving")
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
}

// appearingMigration migrates version 0 to 1, calling appear on the way.
type appearingMigration struct{ appear func() }

func (appearingMigration) FromVersion() int { return 0 }

func (m appearingMigration) Migrate(data json.RawMessage) (json.RawMessage, error) {
	m.appear()
	return json.RawMessage(strings.Replace(string(data), `"version":0`, `"version":1`, 1)), nil
}

// A read can report the settings file missing when it is not, as on a
// redirected folder whose share is reconnecting. Adopting the legacy settings
// then must not replace it, whichever way they are written.
func TestStoreAdoptionNeverReplacesASettingsFile(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy string
		arm    func(*Store, func())
	}{
		{"adopted", legacyResizerSettings, func(store *Store, appear func()) {
			store.RegisterModuleValidator("screenshot", func(json.RawMessage) error {
				appear()
				return nil
			})
		}},
		{"malformed", legacyResizerSettings, func(store *Store, appear func()) {
			store.RegisterModuleValidator("screenshot", func(json.RawMessage) error {
				appear()
				return errors.New("invalid section")
			})
		}},
		{"migrated", strings.Replace(legacyResizerSettings, `"version":1`, `"version":0`, 1), func(store *Store, appear func()) {
			store.migrations[0] = appearingMigration{appear}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := legacyPaths(t, test.legacy)
			store := NewStore(paths)
			test.arm(store, func() {
				if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
					t.Fatal(err)
				}
			})
			result := store.Load()
			requireFileContent(t, paths.ConfigFile, currentSettingsFile)
			requireFileContent(t, paths.LegacyConfigFile, test.legacy)
			if len(result.Warnings) != 0 || result.Settings.Shell.SelectedModule != string(shell.ModuleSettings) {
				t.Fatalf("load = %+v, want the settings file's settings", result)
			}
			if err := store.Save(result.Settings); err != nil {
				t.Fatalf("saving is blocked: %v", err)
			}
			// The abandoned adoption leaves no backup nothing names.
			if left, _ := filepath.Glob(paths.ConfigFile + ".*"); len(left) != 0 {
				t.Fatalf("files left beside the settings file: %v", left)
			}
		})
	}
}

// unwrittenAdoption is a store whose Load took the legacy settings but could
// not write them, with what stopped it removed again and the settings folder
// in place.
func unwrittenAdoption(t *testing.T) (*Store, Paths, Settings) {
	t.Helper()
	paths := legacyPaths(t, legacyResizerSettings)
	blocker := filepath.Dir(paths.ConfigFile)
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	result := store.Load()
	if result.Unwritten == "" {
		t.Fatalf("the adoption was written: %+v", result)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	return store, paths, result.Settings
}

// A settings file that was only reported missing when the adoption failed is
// kept by every later save, and Restore can still back it up and replace it.
func TestStoreSaveAfterAnUnwrittenAdoptionKeepsASettingsFile(t *testing.T) {
	store, paths, session := unwrittenAdoption(t)
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := store.Save(session); err == nil {
			t.Fatal("a save replaced a settings file the session never loaded")
		}
		requireFileContent(t, paths.ConfigFile, currentSettingsFile)
	}
	_, backup, err := store.Restore(session, func(replaced Settings) (Settings, error) {
		if replaced.Shell.SelectedModule != string(shell.ModuleSettings) {
			t.Errorf("replaced module = %q, want the settings file's", replaced.Shell.SelectedModule)
		}
		return Default(), nil
	})
	if err != nil || backup == "" {
		t.Fatalf("Restore() backup = %q, error = %v", backup, err)
	}
	requireFileContent(t, backup, currentSettingsFile)
	if err := store.Save(Default()); err != nil {
		t.Fatalf("saving is still blocked: %v", err)
	}
}

// A restore in the same state only creates the settings file, so one that
// appears while it runs is kept.
func TestStoreRestoreAfterAnUnwrittenAdoptionOnlyCreatesTheFile(t *testing.T) {
	store, paths, session := unwrittenAdoption(t)
	_, backup, err := store.Restore(session, func(Settings) (Settings, error) {
		if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
			t.Fatal(err)
		}
		return Default(), nil
	})
	if !errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v, want ErrNotRestored", backup, err)
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
}

// With saving not blocked, a restore that cannot create the file is a failed
// write like any other: the session takes the restored settings, and a later
// save still only creates the file.
func TestStoreRestoreAfterAnUnwrittenAdoptionReportsAFailedWrite(t *testing.T) {
	store, paths, session := unwrittenAdoption(t)
	folder := filepath.Dir(paths.ConfigFile)
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(folder, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, backup, err := store.Restore(session, restoreTo(Default()))
	if err == nil || errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v, want a failed write", backup, err)
	}
	if restored.Shell.SelectedModule != Default().Shell.SelectedModule {
		t.Fatalf("restored module = %q", restored.Shell.SelectedModule)
	}
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(restored); err == nil {
		t.Fatal("a save replaced a settings file the session never loaded")
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
}

// The first save that reaches the settings file ends the adoption, so later
// saves replace the file as usual.
func TestStoreSaveCompletesAnUnwrittenAdoption(t *testing.T) {
	store, paths, session := unwrittenAdoption(t)
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	session.Shell.SelectedModule = string(shell.ModuleSettings)
	if err := store.Save(session); err != nil {
		t.Fatalf("a second save failed: %v", err)
	}
	if got := NewStore(paths).Load().Settings.Shell.SelectedModule; got != string(shell.ModuleSettings) {
		t.Fatalf("selected module after restart = %q", got)
	}
}

// A share that is down reports both files missing. The session must not treat
// that as a first run, or a save once the share returns would replace the
// settings file or stop the legacy file ever being taken.
func TestStoreBlocksSavingWhenNeitherFileCanBeReached(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	store.reachable = func(string) bool { return false }
	result := store.Load()
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], paths.LegacyConfigFile) {
		t.Fatalf("warnings = %v, want one naming the legacy file", result.Warnings)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(result.Settings); err == nil {
		t.Fatal("a save succeeded with neither file reachable at launch")
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
	if got := NewStore(paths).Load().Settings.Shell.SelectedModule; got != string(shell.ModuleSettings) {
		t.Fatalf("selected module after the share returned = %q", got)
	}
}

// A legacy file reported missing from a folder that cannot be reached has not
// been moved away, so the restore keeps its block until the folder answers.
func TestStoreRestoreKeepsTheBlockWhileTheLegacyFolderIsUnreachable(t *testing.T) {
	store, paths := unreadableLegacyStore(t)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store.reachable = func(string) bool { return false }
	if _, _, err := store.Restore(Default(), restoreTo(Default())); !errors.Is(err, ErrNotRestored) || !strings.Contains(err.Error(), paths.LegacyConfigFile) {
		t.Fatalf("Restore() error = %v, want ErrNotRestored naming the legacy file", err)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("the settings file was created: %v", err)
	}
	if err := store.Save(Default()); err == nil {
		t.Fatal("a refused restore unblocked saving")
	}
	store.reachable = folderReachable
	if _, _, err := store.Restore(Default(), restoreTo(Default())); err != nil {
		t.Fatalf("Restore() once the folder answers: %v", err)
	}
	if err := store.Save(Default()); err != nil {
		t.Fatalf("saving is still blocked: %v", err)
	}
}

// A share can return between the reads that found nothing and the check that
// its folder answers. What it brought back is then what the session loads.
func TestStoreLoadRereadsOnceTheFolderAnswers(t *testing.T) {
	t.Run("settings file", func(t *testing.T) {
		paths := legacyPaths(t, legacyResizerSettings)
		if err := os.Remove(paths.LegacyConfigFile); err != nil {
			t.Fatal(err)
		}
		store := NewStore(paths)
		store.reachable = func(string) bool {
			if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
				t.Fatal(err)
			}
			return true
		}
		result := store.Load()
		if len(result.Warnings) != 0 || result.Settings.Shell.SelectedModule != string(shell.ModuleSettings) {
			t.Fatalf("load = %+v, want the settings file's settings", result)
		}
		requireFileContent(t, paths.ConfigFile, currentSettingsFile)
	})
	t.Run("legacy file", func(t *testing.T) {
		paths := legacyPaths(t, legacyResizerSettings)
		if err := os.Remove(paths.LegacyConfigFile); err != nil {
			t.Fatal(err)
		}
		store := NewStore(paths)
		store.reachable = func(string) bool {
			if err := os.WriteFile(paths.LegacyConfigFile, []byte(legacyResizerSettings), 0o600); err != nil {
				t.Fatal(err)
			}
			return true
		}
		result := store.Load()
		if len(result.Warnings) != 0 || result.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
			t.Fatalf("load = %+v, want the legacy file's settings", result)
		}
		if _, err := os.Stat(paths.ConfigFile); err != nil {
			t.Fatalf("the adopted settings were not written: %v", err)
		}
	})
}

// A first run's first save only creates the settings file, so one a share
// brings back after Load is kept for the next launch rather than replaced.
func TestStoreFirstRunSaveKeepsASettingsFileThatAppeared(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	result := store.Load()
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(result.Settings); err == nil {
		t.Fatal("a first run's save replaced a settings file it never loaded")
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
}

// A first run's first save reaches the file, and later saves replace it.
func TestStoreFirstRunSavesAsUsual(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	value := store.Load().Settings
	for _, module := range []shell.ModuleID{shell.ModuleImageResizer, shell.ModuleSettings} {
		value.Shell.SelectedModule = string(module)
		if err := store.Save(value); err != nil {
			t.Fatalf("save selecting %s: %v", module, err)
		}
	}
	if got := NewStore(paths).Load().Settings.Shell.SelectedModule; got != string(shell.ModuleSettings) {
		t.Fatalf("selected module after restart = %q", got)
	}
}

// Inherited names the legacy settings only until they reach the settings
// file, so Restore can tell presets the session merely took from them.
func TestStoreInheritedLastsUntilTheSettingsAreWritten(t *testing.T) {
	store, paths, session := unwrittenAdoption(t)
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(paths.ConfigFile, later, later); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != string(shell.ModuleImageResizer) {
		t.Fatalf("inherited module = %q, want the legacy file's", got)
	}
	if err := os.Remove(paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != "" {
		t.Fatalf("inherited module after the write = %q", got)
	}
}

// Inherited counts only for a settings file known to be written after the
// legacy file. One older than it, or written at the same time, may predate
// presets an earlier build added there, and with either file gone the order
// cannot be told, so nothing then counts as inherited.
func TestStoreInheritedYieldsToALegacyFileNewerThanTheSettingsFile(t *testing.T) {
	store, paths, _ := unwrittenAdoption(t)
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(paths.LegacyConfigFile, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(paths.ConfigFile, now, now); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != string(shell.ModuleImageResizer) {
		t.Fatalf("inherited module with the settings file newer = %q, want the legacy file's", got)
	}
	if err := os.Chtimes(paths.LegacyConfigFile, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != "" {
		t.Fatalf("inherited module with the legacy file newer = %q, want none", got)
	}
	if err := os.Chtimes(paths.LegacyConfigFile, now, now); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != "" {
		t.Fatalf("inherited module with both files written together = %q, want none", got)
	}
	if err := os.Chtimes(paths.LegacyConfigFile, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != "" {
		t.Fatalf("inherited module with no settings file = %q, want none", got)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	if got := store.Inherited().Shell.SelectedModule; got != "" {
		t.Fatalf("inherited module with the legacy file gone = %q, want none", got)
	}
}

// A junction at the settings folder whose target is gone makes the folder's
// creation fail with an error matching os.ErrExist. That is no settings file
// being there: the legacy settings are still taken, the failure is reported as
// a failed write, and a real file behind the junction once it returns is kept.
func TestStoreDanglingSettingsFolderIsNotAFoundSettingsFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses a directory junction")
	}
	dangling := func(t *testing.T, paths Paths) (target string) {
		t.Helper()
		link := filepath.Dir(paths.ConfigFile)
		target = link + "-target"
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
		t.Cleanup(func() { os.Remove(link) })
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		return target
	}

	t.Run("legacy settings", func(t *testing.T) {
		paths := legacyPaths(t, legacyResizerSettings)
		target := dangling(t, paths)
		store := NewStore(paths)
		result := store.Load()
		if got := result.Settings.Shell.SelectedModule; got != string(shell.ModuleImageResizer) {
			t.Fatalf("selected module = %q, want the legacy file's; warnings %v", got, result.Warnings)
		}
		if result.Unwritten == "" {
			t.Fatalf("the adoption was not reported unwritten: %+v", result)
		}
		err := store.Save(result.Settings)
		if err == nil || strings.Contains(err.Error(), "did not load") || strings.Contains(err.Error(), "saving is disabled") {
			t.Fatalf("Save() = %v, want a plain failed write", err)
		}
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := store.Save(result.Settings); err == nil {
			t.Fatal("a save replaced the settings file behind the returned junction")
		}
		requireFileContent(t, paths.ConfigFile, currentSettingsFile)
		if err := os.Remove(paths.ConfigFile); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no legacy settings", func(t *testing.T) {
		paths := legacyPaths(t, legacyResizerSettings)
		if err := os.Remove(paths.LegacyConfigFile); err != nil {
			t.Fatal(err)
		}
		dangling(t, paths)
		store := NewStore(paths)
		result := store.Load()
		err := store.Save(result.Settings)
		if err == nil || strings.Contains(err.Error(), "did not load") {
			t.Fatalf("Save() = %v, want a plain failed write", err)
		}
	})
}

// The warning for a folder that cannot be reached asks for the share back
// rather than for a file that may never have existed to be closed or moved.
func TestStoreUnreachableFolderWarningsSayToReconnect(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	store.reachable = func(string) bool { return false }
	result := store.Load()
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "Reconnect") || strings.Contains(result.Warnings[0], "original file") {
		t.Fatalf("warnings = %v, want one saying to reconnect", result.Warnings)
	}
	_, _, err := store.Restore(Default(), restoreTo(Default()))
	if !errors.Is(err, ErrNotRestored) || !strings.Contains(err.Error(), "reconnect") || strings.Contains(err.Error(), "close whatever") {
		t.Fatalf("Restore() error = %v, want one saying to reconnect", err)
	}
}

func TestFolderReachable(t *testing.T) {
	root := t.TempDir()
	if !folderReachable(filepath.Join(root, "settings.json")) {
		t.Error("an empty existing folder was not reachable")
	}
	if !folderReachable(filepath.Join(root, "missing", "deeper", "settings.json")) {
		t.Error("a missing folder under a readable one was not reachable")
	}
	stray := filepath.Join(root, "stray")
	if err := os.WriteFile(stray, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !folderReachable(filepath.Join(stray, "settings.json")) {
		t.Error("a file in the folder's place hid the readable folder above it")
	}
	if runtime.GOOS != "windows" {
		return
	}
	// A junction whose target is gone is a volume that is down, however
	// readable the folder holding the junction is.
	target, link := filepath.Join(root, "target"), filepath.Join(root, "Roaming")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	t.Cleanup(func() { os.Remove(link) })
	if !folderReachable(filepath.Join(link, "GameDevTools", "settings.json")) {
		t.Error("a folder through a working junction was not reachable")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if folderReachable(filepath.Join(link, "GameDevTools", "settings.json")) {
		t.Error("a folder through a junction whose target is gone was reachable")
	}
	if folderReachable(`\\unreachable.invalid\users\me\AppData\Roaming\GameDevTools\settings.json`) {
		t.Error("a path on a server that does not answer was reachable")
	}
	for letter := 'Z'; letter >= 'D'; letter-- {
		drive := string(letter) + `:\`
		if _, err := os.Stat(drive); errors.Is(err, os.ErrNotExist) {
			if folderReachable(drive + `Roaming\GameDevTools\settings.json`) {
				t.Errorf("a path on the absent drive %s was reachable", drive)
			}
			return
		}
	}
	t.Log("no absent drive letter to test with")
}

// A folder that refuses to be listed has answered, so a first run under a
// locked-down profile is a first run, and its settings save.
func TestStoreFirstRunWhereListingIsDeniedSaves(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("denies listing with a Windows access control entry")
	}
	root := filepath.Join(t.TempDir(), "Roaming")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	entry := "*" + account.Uid
	if out, err := exec.Command("icacls", root, "/deny", entry+":(RD)").CombinedOutput(); err != nil {
		t.Fatalf("deny listing: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("icacls", root, "/remove:d", entry).CombinedOutput(); err != nil {
			t.Errorf("allow listing again: %v: %s", err, out)
		}
	})
	if _, err := os.ReadDir(root); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("listing the folder gave %v, want it denied", err)
	}

	paths := Paths{
		ConfigFile:       filepath.Join(root, ui.AppName, "settings.json"),
		LegacyConfigFile: filepath.Join(root, legacyFolderName, "settings.json"),
	}
	store := NewStore(paths)
	result := store.Load()
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want a plain first run", result.Warnings)
	}
	if err := store.Save(result.Settings); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatalf("the first save wrote no settings file: %v", err)
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

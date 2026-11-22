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

// legacyPresetSettings is a legacy file holding a custom preset.
const legacyPresetSettings = `{"version":1,"shell":{"selectedModule":"image-resizer","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}},"modules":{"image-resizer":{"presets":{"version":1,"custom":[{"id":"custom.00000000000000000000000000000001","name":"Kept","width":64,"height":64,"resizeMode":"fit"}]}}}}`

// adoptionVariants are the kinds of legacy file an adoption writes, with the
// module the session then shows.
var adoptionVariants = []struct {
	name, legacy string
	module       shell.ModuleID
}{
	{"valid", legacyResizerSettings, shell.ModuleImageResizer},
	{"migrated", strings.Replace(legacyResizerSettings, `"version":1`, `"version":0`, 1), shell.ModuleImageResizer},
	{"malformed", `{"version":1,`, shell.ModuleScreenshot},
}

// failingCreate is a renameNew that fails as a disk or share does, finding no
// file there, so an adoption's backup succeeds and its write does not.
func failingCreate(string, string) error { return errors.New("the disk is full") }

// failedAdoption is a store whose Load read the legacy file and could not
// write the new one, with what stopped it gone again.
func failedAdoption(t *testing.T, legacy string) (*Store, Paths, LoadResult) {
	t.Helper()
	paths := legacyPaths(t, legacy)
	store := NewStore(paths, appearingMigration{func() {}})
	store.renameNew = failingCreate
	result := store.Load()
	store.renameNew = renameNoReplace
	if store.blocked == nil {
		t.Fatalf("saving is not blocked: %+v", result)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("the settings file exists: %v", err)
	}
	return store, paths, result
}

// A failed adoption write shows the legacy settings, blocks saving, and says
// so, naming the file that still holds them. It is not reported as Unwritten,
// which promises that the next save settles it, and it leaves no backup behind
// for each retry to add to.
func TestStoreFailedAdoptionWarnsAndBlocksSaving(t *testing.T) {
	for _, test := range adoptionVariants {
		t.Run(test.name, func(t *testing.T) {
			store, paths, result := failedAdoption(t, test.legacy)
			if result.Unwritten != "" || result.Recovered != "" {
				t.Fatalf("Unwritten = %q, Recovered = %q, want neither", result.Unwritten, result.Recovered)
			}
			if got := result.Settings.Shell.SelectedModule; got != string(test.module) {
				t.Fatalf("selected module = %q, want %q", got, test.module)
			}
			if len(result.Warnings) != 1 {
				t.Fatalf("warnings = %v, want one", result.Warnings)
			}
			for _, want := range []string{paths.LegacyConfigFile, "will not be saved", "next launch"} {
				if !strings.Contains(result.Warnings[0], want) {
					t.Errorf("warning %q does not say %q", result.Warnings[0], want)
				}
			}
			entries, err := os.ReadDir(filepath.Dir(paths.ConfigFile))
			if err != nil || len(entries) != 0 {
				t.Fatalf("settings folder holds %v, err = %v, want nothing", entries, err)
			}
			if err := store.Save(result.Settings); err == nil {
				t.Fatal("a save succeeded after a failed adoption")
			}
		})
	}
}

// A backup the adoption cannot make fails it as its write would: the warning
// says the same, as the same folder is usually at fault.
func TestStoreAdoptionBackupFailureWarnsAsAFailedWrite(t *testing.T) {
	for _, test := range adoptionVariants[1:] {
		t.Run(test.name, func(t *testing.T) {
			paths := legacyPaths(t, test.legacy)
			store := NewStore(paths, appearingMigration{func() {}})
			store.readBack = func(string) ([]byte, error) { return nil, errors.New("backup unreadable") }
			result := store.Load()
			if len(result.Warnings) != 1 {
				t.Fatalf("warnings = %v, want one", result.Warnings)
			}
			for _, want := range []string{paths.LegacyConfigFile, "will not be saved", "next launch"} {
				if !strings.Contains(result.Warnings[0], want) {
					t.Errorf("warning %q does not say %q", result.Warnings[0], want)
				}
			}
			if err := store.Save(result.Settings); err == nil {
				t.Fatal("a save succeeded after a failed adoption")
			}
		})
	}
}

// Invariant: an existing settings file is never replaced without a backup. A
// file that turns up after the session found none is refused by Save, and
// Restore copies it first.
func TestInvariantExistingFileIsNeverReplacedWithoutABackup(t *testing.T) {
	for _, test := range adoptionVariants {
		t.Run("failed adoption "+test.name, func(t *testing.T) {
			store, paths, result := failedAdoption(t, test.legacy)
			if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := store.Save(result.Settings); err == nil {
				t.Fatal("a save replaced a settings file the session never loaded")
			}
			requireFileContent(t, paths.ConfigFile, currentSettingsFile)
			_, backup, err := store.Restore(result.Settings, func(replaced Settings) (Settings, error) {
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
		})
	}
	t.Run("restore after a fresh start", func(t *testing.T) {
		paths := legacyPaths(t, legacyResizerSettings)
		if err := os.Remove(paths.LegacyConfigFile); err != nil {
			t.Fatal(err)
		}
		store := NewStore(paths)
		store.Load()
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
	})
}

// With saving not blocked, a restore that cannot create the file is a failed
// write like any other: the session takes the restored settings, and a later
// save still only creates the file.
func TestStoreRestoreAfterAFreshStartReportsAFailedWrite(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	store.Load()
	store.renameNew = failingCreate
	restored, backup, err := store.Restore(Default(), restoreTo(Default()))
	store.renameNew = renameNoReplace
	if err == nil || errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v, want a failed write", backup, err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(restored); err == nil {
		t.Fatal("a save replaced a settings file the session never loaded")
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
}

// A restore that finds the loaded file missing may be looking at a share that
// is down. When it cannot create the file either, no copy of the file exists,
// so a later save only creates: a file that returns is kept, for a restore to
// back up, and a file that is really gone is written as usual.
func TestStoreFailedRestoreOfAMissingFileNeverReplacesIt(t *testing.T) {
	failedRestore := func(t *testing.T) (*Store, Paths, Settings, string) {
		t.Helper()
		paths := legacyPaths(t, legacyResizerSettings)
		if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
			t.Fatal(err)
		}
		store := NewStore(paths)
		store.Load()
		away := paths.ConfigFile + ".away"
		if err := os.Rename(paths.ConfigFile, away); err != nil {
			t.Fatal(err)
		}
		store.renameNew = failingCreate
		restored, backup, err := store.Restore(Default(), restoreTo(Default()))
		store.renameNew = renameNoReplace
		if err == nil || errors.Is(err, ErrNotRestored) || backup != "" {
			t.Fatalf("Restore() backup = %q, error = %v, want a failed write", backup, err)
		}
		return store, paths, restored, away
	}

	t.Run("file returns", func(t *testing.T) {
		store, paths, restored, away := failedRestore(t)
		if err := os.Rename(away, paths.ConfigFile); err != nil {
			t.Fatal(err)
		}
		if err := store.Save(restored); err == nil {
			t.Fatal("a save replaced a returned settings file with no copy made")
		}
		requireFileContent(t, paths.ConfigFile, currentSettingsFile)
		_, backup, err := store.Restore(restored, restoreTo(Default()))
		if err != nil || backup == "" {
			t.Fatalf("Restore() backup = %q, error = %v, want the file backed up", backup, err)
		}
		requireFileContent(t, backup, currentSettingsFile)
	})

	t.Run("file really gone", func(t *testing.T) {
		store, paths, restored, _ := failedRestore(t)
		if err := store.Save(restored); err != nil {
			t.Fatalf("a save with no file to keep failed: %v", err)
		}
		if _, err := os.Stat(paths.ConfigFile); err != nil {
			t.Fatalf("the save wrote no settings file: %v", err)
		}
	})
}

// Invariant: the legacy file is never modified, whether the adoption is
// written or fails, and whatever the session does after it.
func TestInvariantLegacyFileIsNeverModified(t *testing.T) {
	for _, test := range adoptionVariants {
		for _, failing := range []bool{false, true} {
			name := test.name + " written"
			if failing {
				name = test.name + " failed"
			}
			t.Run(name, func(t *testing.T) {
				paths := legacyPaths(t, test.legacy)
				before, err := os.Stat(paths.LegacyConfigFile)
				if err != nil {
					t.Fatal(err)
				}
				store := NewStore(paths, appearingMigration{func() {}})
				if failing {
					store.renameNew = failingCreate
				}
				result := store.Load()
				store.renameNew = renameNoReplace
				_ = store.Save(result.Settings)
				_, _, _ = store.Restore(result.Settings, restoreTo(Default()))
				_ = store.Save(Default())
				requireFileContent(t, paths.LegacyConfigFile, test.legacy)
				after, err := os.Stat(paths.LegacyConfigFile)
				if err != nil {
					t.Fatal(err)
				}
				if !after.ModTime().Equal(before.ModTime()) {
					t.Fatalf("the legacy file was written: %v, was %v", after.ModTime(), before.ModTime())
				}
			})
		}
	}
}

// Invariant: a custom preset is never lost. Whenever the new file was not
// written, the legacy file still holds the preset, and the next launch that
// can write takes it.
func TestInvariantCustomPresetIsNeverLost(t *testing.T) {
	store, paths, result := failedAdoption(t, legacyPresetSettings)
	session := result.Settings
	session.Modules = map[string]json.RawMessage{"image-resizer": json.RawMessage(`{"presets":{"version":1}}`)}
	if err := store.Save(session); err == nil {
		t.Fatal("a save succeeded after a failed adoption")
	}
	if _, _, err := store.Restore(session, restoreTo(Default())); !errors.Is(err, ErrNotRestored) {
		t.Fatalf("Restore() error = %v, want ErrNotRestored", err)
	}
	requireFileContent(t, paths.LegacyConfigFile, legacyPresetSettings)

	next := NewStore(paths).Load()
	if len(next.Warnings) != 0 {
		t.Fatalf("warnings on the next launch = %v", next.Warnings)
	}
	if got := string(next.Settings.Modules["image-resizer"]); !strings.Contains(got, `"name":"Kept"`) {
		t.Fatalf("the next launch lost the preset: %s", got)
	}
	written, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `"Kept"`) {
		t.Fatalf("the new settings file lacks the preset: %s", written)
	}
}

// Invariant: a blocked session with no settings file writes none, through
// Save, the write Hold returns, or Restore, which refuses before it derives
// anything and says what to do. That holds for every way Load blocks saving,
// and after what caused the block has gone away.
func TestInvariantBlockedSessionWritesNoSettingsFile(t *testing.T) {
	failedBackup := func(legacy string) func(*testing.T) (*Store, Paths) {
		return func(t *testing.T) (*Store, Paths) {
			paths := legacyPaths(t, legacy)
			store := NewStore(paths, appearingMigration{func() {}})
			store.readBack = func(string) ([]byte, error) { return nil, errors.New("backup unreadable") }
			store.Load()
			store.readBack = os.ReadFile
			return store, paths
		}
	}
	for name, blocked := range map[string]func(*testing.T) (*Store, Paths){
		"folder unreachable": func(t *testing.T) (*Store, Paths) {
			paths := legacyPaths(t, legacyResizerSettings)
			if err := os.Remove(paths.LegacyConfigFile); err != nil {
				t.Fatal(err)
			}
			store := NewStore(paths)
			store.reachable = func(string) bool { return false }
			store.Load()
			return store, paths
		},
		"legacy file unreadable": unreadableLegacyStore,
		"adoption write failed": func(t *testing.T) (*Store, Paths) {
			store, paths, _ := failedAdoption(t, legacyResizerSettings)
			return store, paths
		},
		"migration backup failed": failedBackup(strings.Replace(legacyResizerSettings, `"version":1`, `"version":0`, 1)),
		"malformed backup failed": failedBackup(`{"version":1,`),
	} {
		t.Run(name, func(t *testing.T) {
			store, paths := blocked(t)
			store.reachable = folderReachable
			requireNoSettingsFile := func() {
				t.Helper()
				left, err := filepath.Glob(paths.ConfigFile + "*")
				if err != nil || len(left) != 0 {
					t.Fatalf("settings files = %v, err = %v, want none", left, err)
				}
			}
			if err := store.Save(Default()); err == nil {
				t.Fatal("Save() succeeded while blocked")
			}
			requireNoSettingsFile()
			write := store.Hold()
			if err := write(Default()); err == nil {
				t.Fatal("Hold's write succeeded while blocked")
			}
			requireNoSettingsFile()
			_, backup, err := store.Restore(Default(), func(Settings) (Settings, error) {
				t.Error("a refused restore derived settings")
				return Default(), nil
			})
			if !errors.Is(err, ErrNotRestored) || backup != "" {
				t.Fatalf("Restore() backup = %q, error = %v, want ErrNotRestored", backup, err)
			}
			if !strings.Contains(err.Error(), "saving is disabled") || !strings.Contains(err.Error(), "start the application again") {
				t.Fatalf("Restore() error = %v, want why and what to do", err)
			}
			requireNoSettingsFile()
		})
	}
}

// Invariant: a failed adoption write is retried on the next launch, and then
// succeeds and saves as usual.
func TestInvariantFailedAdoptionIsRetriedOnTheNextLaunch(t *testing.T) {
	for _, test := range adoptionVariants {
		t.Run(test.name, func(t *testing.T) {
			_, paths, _ := failedAdoption(t, test.legacy)
			store := NewStore(paths, appearingMigration{func() {}})
			retried := store.Load()
			if retried.Unwritten != "" || store.blocked != nil {
				t.Fatalf("retry = %+v, blocked = %v", retried, store.blocked)
			}
			if got := retried.Settings.Shell.SelectedModule; got != string(test.module) {
				t.Fatalf("selected module = %q, want %q", got, test.module)
			}
			if _, err := os.Stat(paths.ConfigFile); err != nil {
				t.Fatalf("the retry wrote no settings file: %v", err)
			}
			requireFileContent(t, paths.LegacyConfigFile, test.legacy)
			value := retried.Settings
			value.Shell.SelectedModule = string(shell.ModuleSettings)
			if err := store.Save(value); err != nil {
				t.Fatalf("a save after the retry failed: %v", err)
			}
			if got := NewStore(paths).Load().Settings.Shell.SelectedModule; got != string(shell.ModuleSettings) {
				t.Fatalf("selected module after restart = %q", got)
			}
		})
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

// A junction at the settings folder whose target is gone makes the folder's
// creation fail with an error matching os.ErrExist. That is no settings file
// being there: the legacy settings are still taken, the failed adoption blocks
// saving, and a real file behind the junction once it returns is kept.
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
		if result.Unwritten != "" || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], paths.LegacyConfigFile) {
			t.Fatalf("load = %+v, want one warning naming the legacy file", result)
		}
		err := store.Save(result.Settings)
		if err == nil || strings.Contains(err.Error(), "has not backed up") || !strings.Contains(err.Error(), "saving is disabled") {
			t.Fatalf("Save() = %v, want saving blocked by the failed adoption", err)
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
		target := dangling(t, paths)
		store := NewStore(paths)
		result := store.Load()
		if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], paths.ConfigFile) || !strings.Contains(result.Warnings[0], "Reconnect") {
			t.Fatalf("warnings = %v, want one naming the settings file and saying to reconnect", result.Warnings)
		}
		err := store.Save(result.Settings)
		if err == nil || strings.Contains(err.Error(), "has not backed up") || !strings.Contains(err.Error(), "saving is disabled") {
			t.Fatalf("Save() = %v, want saving blocked by the unreachable folder", err)
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
}

// With no legacy file, the legacy folder answering says nothing about the
// settings folder, which may be on another volume that is down.
func TestStoreBlocksSavingWhenOnlyTheSettingsFolderCannotBeReached(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	if err := os.Remove(paths.LegacyConfigFile); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	store.reachable = func(path string) bool { return path != paths.ConfigFile }
	result := store.Load()
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], paths.ConfigFile) || !strings.Contains(result.Warnings[0], "Reconnect") {
		t.Fatalf("warnings = %v, want one naming the settings file and saying to reconnect", result.Warnings)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(currentSettingsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(result.Settings); err == nil {
		t.Fatal("a save succeeded with the settings folder unreachable at launch")
	}
	requireFileContent(t, paths.ConfigFile, currentSettingsFile)
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
	if !errors.Is(err, ErrNotRestored) || !strings.Contains(err.Error(), "start the application again") {
		t.Fatalf("Restore() error = %v, want a refusal saying what to do", err)
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

// A real failure to write, a file where the settings folder belongs: the
// session has the legacy settings and saves nothing, and once the obstacle is
// gone the next launch adopts them.
func TestStoreAdoptionThatCannotBeWrittenIsRetried(t *testing.T) {
	paths := legacyPaths(t, legacyResizerSettings)
	blocker := filepath.Dir(paths.ConfigFile)
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(paths)
	result := store.Load()
	if result.Unwritten != "" || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], paths.LegacyConfigFile) {
		t.Fatalf("Unwritten = %q, warnings = %v", result.Unwritten, result.Warnings)
	}
	if result.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("selected module = %q", result.Settings.Shell.SelectedModule)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(result.Settings); err == nil {
		t.Fatal("a save succeeded in a session whose adoption failed")
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("the blocked session wrote a settings file: %v", err)
	}
	retried := NewStore(paths).Load()
	if len(retried.Warnings) != 0 || retried.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("retry = %+v", retried)
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
}

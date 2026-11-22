package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

// restoreTo is a reset for Restore that restores value whatever the
// settings it replaces.
func restoreTo(value Settings) func(Settings) (Settings, error) {
	return func(Settings) (Settings, error) { return value, nil }
}

// compactJSON returns a module section without the file's indentation, for
// comparing it with the section that was saved, or "" for none.
func compactJSON(t *testing.T, section json.RawMessage) string {
	t.Helper()
	if len(section) == 0 {
		return ""
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, section); err != nil {
		t.Fatal(err)
	}
	return compacted.String()
}

// TestStoreRestoreBacksUpTheFileItReplaces pins the store half of Restore
// Default Settings (SET-005): the restored settings are derived from the
// session's, the file's bytes are copied beside it first, byte for byte,
// and only then replaced by the restored settings, which a restart loads.
func TestStoreRestoreBacksUpTheFileItReplaces(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	store := NewStore(Paths{ConfigFile: path})
	value := Default()
	value.Shell.SelectedModule = string(shell.ModuleImageResizer)
	value.Modules["screenshot"] = json.RawMessage(`{"capture":{"mode":"full-display"}}`)
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	restored, backup, err := store.Restore(value, func(replaced Settings) (Settings, error) {
		if string(replaced.Modules["screenshot"]) != string(value.Modules["screenshot"]) {
			t.Errorf("replaced settings = %+v, want the session's", replaced)
		}
		restored := Default()
		restored.Shell = replaced.Shell
		return restored, nil
	})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.Shell != value.Shell || len(restored.Modules) != 0 {
		t.Fatalf("Restore() settings = %+v, want the restored settings", restored)
	}
	if !strings.HasPrefix(filepath.Base(backup), "settings.json.reset-") || filepath.Dir(backup) != directory {
		t.Fatalf("backup = %q, want settings.json.reset-<timestamp> beside the file", backup)
	}
	copied, err := os.ReadFile(backup)
	if err != nil || string(copied) != string(original) {
		t.Fatalf("backup = %q, err = %v, want the replaced bytes %q", copied, err, original)
	}
	reloaded := NewStore(Paths{ConfigFile: path}).Load()
	if len(reloaded.Warnings) != 0 {
		t.Fatalf("restart warnings = %v", reloaded.Warnings)
	}
	if _, exists := reloaded.Settings.Modules["screenshot"]; exists || reloaded.Settings.Shell.SelectedModule != string(shell.ModuleImageResizer) {
		t.Fatalf("restored settings after restart = %+v", reloaded.Settings)
	}
}

// TestStoreRestoreWithoutAFileWritesOne pins that Restore copies nothing
// when there is no settings file yet and reports no backup.
func TestStoreRestoreWithoutAFileWritesOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	_, backup, err := NewStore(Paths{ConfigFile: path}).Restore(Default(), restoreTo(Default()))
	if err != nil || backup != "" {
		t.Fatalf("Restore() = %q, %v, want no backup and no error", backup, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Restore() wrote no settings file: %v", err)
	}
}

// TestStoreRestoreKeepsAFileReportedMissingThatReturns pins that a session
// that loaded its settings file does not replace it uncopied when a share
// reports it missing as the restore reads it and brings it back before the
// write: nothing changes, and the next restore backs the file up.
func TestStoreRestoreKeepsAFileReportedMissingThatReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(Paths{ConfigFile: path})
	value := Default()
	value.Shell.SelectedModule = string(shell.ModuleImageResizer)
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, backup, err := store.Restore(value, func(Settings) (Settings, error) {
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		return Default(), nil
	})
	if !errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() backup = %q, error = %v, want ErrNotRestored", backup, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("settings file = %q, err = %v, want it kept as %q", got, err, original)
	}
	_, backup, err = store.Restore(value, restoreTo(Default()))
	if err != nil || backup == "" {
		t.Fatalf("second Restore() backup = %q, error = %v, want the file backed up", backup, err)
	}
	if copied, err := os.ReadFile(backup); err != nil || !bytes.Equal(copied, original) {
		t.Fatalf("backup = %q, err = %v, want the file's bytes", copied, err)
	}
}

// TestStoreRestoreReenablesSavingOnceTheOriginalIsBackedUp pins that the
// restore is the way out of a session whose saving Load disabled because it
// could not back up a malformed file: once Restore has copied the file,
// saving works again, and the copy holds the original bytes. The backup at
// load fails its verification, which fails the same way on every platform.
func TestStoreRestoreReenablesSavingOnceTheOriginalIsBackedUp(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte("not json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(Paths{ConfigFile: path})
	store.readBack = func(string) ([]byte, error) { return nil, errors.New("device not ready") }
	if warnings := store.Load().Warnings; len(warnings) != 1 || !strings.Contains(warnings[0], "saving is disabled") {
		t.Fatalf("load warnings = %v, want saving disabled", warnings)
	}
	store.readBack = os.ReadFile

	_, backup, err := store.Restore(Default(), restoreTo(Default()))
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	copied, err := os.ReadFile(backup)
	if err != nil || string(copied) != string(original) {
		t.Fatalf("backup = %q, err = %v, want the original bytes", copied, err)
	}
	if err := store.Save(Default()); err != nil {
		t.Fatalf("Save() after the restore = %v, want saving enabled again", err)
	}
}

// TestStoreRestoreKeepsSavingDisabledForAnOriginalItCannotFind pins that a
// store whose saving Load disabled to protect a file it could not read saves
// again only once the restore has copied that file: an original the restore
// finds missing cannot be told from one a network share that is down
// reports missing, so the restore changes nothing, and saving stays
// disabled rather than letting a later save replace a file that has no
// copy.
func TestStoreRestoreKeepsSavingDisabledForAnOriginalItCannotFind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(Paths{ConfigFile: path})
	if warnings := store.Load().Warnings; len(warnings) != 1 || !strings.Contains(warnings[0], "saving is disabled") {
		t.Fatalf("load warnings = %v, want the file unread and saving disabled", warnings)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if _, backup, err := store.Restore(Default(), restoreTo(Default())); !errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() = %q, %v, want ErrNotRestored without a backup", backup, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the restore wrote a settings file: %v", err)
	}
	if err := store.Save(Default()); err == nil {
		t.Fatal("Save() succeeded after a restore that copied nothing")
	}
}

// TestStoreRestoreDerivesFromAFileTheSessionNeverLoaded pins that a session
// Load started from the defaults, as it could not read the settings file,
// restores from the file's settings once the file can be read, not from the
// session's defaults, so what the restore keeps is what the file stores;
// once a save has replaced the file, the session's own settings are the
// ones the restore replaces again.
func TestStoreRestoreDerivesFromAFileTheSessionNeverLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(Paths{ConfigFile: path})
	if warnings := store.Load().Warnings; len(warnings) != 1 || !strings.Contains(warnings[0], "saving is disabled") {
		t.Fatalf("load warnings = %v, want the file unread and saving disabled", warnings)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	stored := Default()
	stored.Modules["screenshot"] = json.RawMessage(`{"presets":{"version":1}}`)
	if err := NewStore(Paths{ConfigFile: path}).Save(stored); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	keepScreenshot := func(replaced Settings) (Settings, error) {
		restored := Default()
		if section, exists := replaced.Modules["screenshot"]; exists {
			restored.Modules["screenshot"] = section
		}
		return restored, nil
	}
	restored, backup, err := store.Restore(Default(), keepScreenshot)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := compactJSON(t, restored.Modules["screenshot"]); got != string(stored.Modules["screenshot"]) {
		t.Fatalf("restored screenshot section = %q, want the file's %q", got, stored.Modules["screenshot"])
	}
	if copied, err := os.ReadFile(backup); err != nil || string(copied) != string(original) {
		t.Fatalf("backup = %q, err = %v, want the file's bytes", copied, err)
	}
	if reloaded := NewStore(Paths{ConfigFile: path}).Load(); compactJSON(t, reloaded.Settings.Modules["screenshot"]) != string(stored.Modules["screenshot"]) {
		t.Fatalf("settings after a restart = %+v, want the file's section kept", reloaded.Settings)
	}

	restored, _, err = store.Restore(Default(), keepScreenshot)
	if _, exists := restored.Modules["screenshot"]; err != nil || exists {
		t.Fatalf("second Restore() = %+v, %v, want the session's settings replaced", restored, err)
	}
}

// TestStoreRestoreChangesNothingWhenItCannotBackUp pins that a file Restore
// cannot read, or cannot copy, is left alone and the error says the
// settings were not restored, so the caller keeps the session unchanged.
func TestStoreRestoreChangesNothingWhenItCannotBackUp(t *testing.T) {
	t.Run("unreadable file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		store := NewStore(Paths{ConfigFile: path})
		store.Load()
		if _, _, err := store.Restore(Default(), restoreTo(Default())); !errors.Is(err, ErrNotRestored) {
			t.Fatalf("Restore() error = %v, want ErrNotRestored", err)
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("the unreadable path changed: %v, err = %v", info, err)
		}
		if err := store.Save(Default()); err == nil {
			t.Fatal("Save() succeeded after a restore that backed nothing up")
		}
	})
	t.Run("backup that fails verification", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		store := NewStore(Paths{ConfigFile: path})
		if err := store.Save(Default()); err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		store.readBack = func(string) ([]byte, error) { return []byte("{}"), nil }
		if _, _, err := store.Restore(Default(), restoreTo(Default())); !errors.Is(err, ErrNotRestored) || !strings.Contains(err.Error(), "differs") {
			t.Fatalf("Restore() error = %v, want ErrNotRestored for a copy that differs", err)
		}
		current, err := os.ReadFile(path)
		if err != nil || string(current) != string(original) {
			t.Fatalf("settings file changed: %q, err = %v", current, err)
		}
		if backups, _ := filepath.Glob(path + ".reset-*"); len(backups) != 0 {
			t.Fatalf("a backup that failed verification was kept: %v", backups)
		}
	})
	t.Run("invalid restored settings", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		store := NewStore(Paths{ConfigFile: path})
		if err := store.Save(Default()); err != nil {
			t.Fatal(err)
		}
		invalid := Default()
		invalid.Shell.SelectedModule = "other"
		if _, _, err := store.Restore(Default(), restoreTo(invalid)); !errors.Is(err, ErrNotRestored) {
			t.Fatalf("Restore() error = %v, want ErrNotRestored", err)
		}
		if backups, _ := filepath.Glob(path + ".reset-*"); len(backups) != 0 {
			t.Fatalf("an invalid restore made backups %v", backups)
		}
	})
	t.Run("restored settings that cannot be made", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		store := NewStore(Paths{ConfigFile: path})
		if err := store.Save(Default()); err != nil {
			t.Fatal(err)
		}
		failed := func(Settings) (Settings, error) { return Settings{}, errors.New("malformed preset section") }
		if _, _, err := store.Restore(Default(), failed); !errors.Is(err, ErrNotRestored) || !strings.Contains(err.Error(), "malformed preset section") {
			t.Fatalf("Restore() error = %v, want ErrNotRestored with the reason", err)
		}
		if backups, _ := filepath.Glob(path + ".reset-*"); len(backups) != 0 {
			t.Fatalf("a restore that could not be made made backups %v", backups)
		}
	})
}

// TestStoreRestoreWithoutAPathFailsLikeASave pins that a session without a
// settings path, kept in memory only, takes a restore as it takes any
// change: Restore returns the restored settings and the write failure,
// ErrPathUnavailable, not ErrNotRestored, so the session still takes them
// (SET-004).
func TestStoreRestoreWithoutAPathFailsLikeASave(t *testing.T) {
	restored, backup, err := NewStore(Paths{}).Restore(Default(), restoreTo(Default()))
	if !errors.Is(err, ErrPathUnavailable) || errors.Is(err, ErrNotRestored) || backup != "" || restored.Version != CurrentVersion {
		t.Fatalf("Restore() = %+v, %q, %v, want the restored settings and ErrPathUnavailable", restored, backup, err)
	}
}

// TestStoreBackupThatFailsVerificationBlocksSaving pins the corrupt-data
// path's verification (SET-006): a backup that does not read back as the
// original is removed and treated as no backup, so the original is not
// replaced by defaults and saving stays disabled.
func TestStoreBackupThatFailsVerificationBlocksSaving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version":1`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(Paths{ConfigFile: path})
	store.readBack = func(string) ([]byte, error) { return nil, errors.New("device not ready") }
	warnings := store.Load().Warnings
	if len(warnings) != 1 || !strings.Contains(warnings[0], "saving is disabled") || !strings.Contains(warnings[0], "device not ready") {
		t.Fatalf("load warnings = %v, want the verification failure and saving disabled", warnings)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(original) {
		t.Fatalf("original changed: %q, err = %v", current, err)
	}
	if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) != 0 {
		t.Fatalf("an unverified backup was kept: %v", backups)
	}
	if err := store.Save(Default()); err == nil {
		t.Fatal("Save() succeeded without a verified backup of the original")
	}
}

// TestStoreLoadRemovesStaleTemporaryFiles pins the cleanup after an
// interrupted save: Load removes the temporary files a save left behind
// once they are older than any save in flight, and nothing else. A file
// minutes old is kept, as the time of a file on a redirected settings
// directory comes from its server's clock, which can be minutes off this
// computer's, so it can belong to a save another session is making now.
func TestStoreLoadRemovesStaleTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	stale := filepath.Join(directory, ".settings-123.tmp")
	fresh := filepath.Join(directory, ".settings-456.tmp")
	skewed := filepath.Join(directory, ".settings-654.tmp")
	unrelated := filepath.Join(directory, ".settings-789.txt")
	folder := filepath.Join(directory, ".settings-folder.tmp")
	for _, file := range []string{stale, fresh, skewed, unrelated} {
		if err := os.WriteFile(file, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleTemporaryAge)
	for _, entry := range []string{stale, unrelated, folder} {
		if err := os.Chtimes(entry, old, old); err != nil {
			t.Fatal(err)
		}
	}
	minutesAgo := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(skewed, minutesAgo, minutesAgo); err != nil {
		t.Fatal(err)
	}

	if warnings := NewStore(Paths{ConfigFile: path}).Load().Warnings; len(warnings) != 0 {
		t.Fatalf("load warnings = %v", warnings)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temporary file remains: %v", err)
	}
	for _, kept := range []string{fresh, skewed, unrelated, folder} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was removed: %v", filepath.Base(kept), err)
		}
	}
}

// TestStoreHoldWritesOnlyThroughItsWrite pins Hold, as the end of the
// Windows session holds the saves the main window's destruction makes: a
// held store's Save reports success and writes nothing, and the function
// Hold returns writes the settings as Save did, checking them first.
func TestStoreHoldWritesOnlyThroughItsWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(Paths{ConfigFile: path})
	write := store.Hold()
	if err := store.Save(Default()); err != nil {
		t.Fatalf("held Save() error = %v, want success", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a held Save wrote the settings file: %v", err)
	}
	invalid := Default()
	invalid.Shell.SelectedModule = "other"
	if err := write(invalid); err == nil {
		t.Fatal("Hold's write accepted invalid settings")
	}
	saved := Default()
	saved.Shell.SelectedModule = string(shell.ModuleImageResizer)
	if err := write(saved); err != nil {
		t.Fatalf("Hold's write error = %v", err)
	}
	if reloaded := NewStore(Paths{ConfigFile: path}).Load(); len(reloaded.Warnings) != 0 || reloaded.Settings.Shell.SelectedModule != saved.Shell.SelectedModule {
		t.Fatalf("settings after a restart = %+v, warnings %v, want the ones Hold's write wrote", reloaded.Settings, reloaded.Warnings)
	}
}

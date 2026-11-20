package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

// TestStoreRestoreBacksUpTheFileItReplaces pins the store half of Restore
// Default Settings (SET-005): the file's bytes are copied beside it first,
// byte for byte, and only then replaced by the restored settings, which a
// restart loads.
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

	restored := Default()
	restored.Shell = value.Shell
	backup, err := store.Restore(restored)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
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
	backup, err := NewStore(Paths{ConfigFile: path}).Restore(Default())
	if err != nil || backup != "" {
		t.Fatalf("Restore() = %q, %v, want no backup and no error", backup, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Restore() wrote no settings file: %v", err)
	}
}

// TestStoreRestoreReenablesSavingOnceTheOriginalIsBackedUp pins that the
// restore is the way out of a session whose saving Load disabled because it
// could not back up a malformed file: once Restore has copied the file,
// saving works again, and the copy holds the original bytes.
func TestStoreRestoreReenablesSavingOnceTheOriginalIsBackedUp(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	original := []byte("not json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(Paths{ConfigFile: path})
	store.now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	if err := os.Mkdir(path+".corrupt-20261120T120000.000000000Z", 0o700); err != nil {
		t.Fatal(err)
	}
	if warnings := store.Load().Warnings; len(warnings) != 1 || !strings.Contains(warnings[0], "saving is disabled") {
		t.Fatalf("load warnings = %v, want saving disabled", warnings)
	}

	backup, err := store.Restore(Default())
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
		if _, err := store.Restore(Default()); !errors.Is(err, ErrNotRestored) {
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
		if _, err := store.Restore(Default()); !errors.Is(err, ErrNotRestored) || !strings.Contains(err.Error(), "differs") {
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
		if _, err := store.Restore(invalid); !errors.Is(err, ErrNotRestored) {
			t.Fatalf("Restore() error = %v, want ErrNotRestored", err)
		}
		if backups, _ := filepath.Glob(path + ".reset-*"); len(backups) != 0 {
			t.Fatalf("an invalid restore made backups %v", backups)
		}
	})
}

// TestStoreRestoreWithoutAPathFailsLikeASave pins that a session without a
// settings path, kept in memory only, takes a restore as it takes any
// change: Restore reports the write failure, not ErrNotRestored, so the
// session still takes the restored settings (SET-004).
func TestStoreRestoreWithoutAPathFailsLikeASave(t *testing.T) {
	backup, err := NewStore(Paths{}).Restore(Default())
	if err == nil || errors.Is(err, ErrNotRestored) || backup != "" {
		t.Fatalf("Restore() = %q, %v, want a write failure", backup, err)
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
// once they are older than any save in flight, and nothing else.
func TestStoreLoadRemovesStaleTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	stale := filepath.Join(directory, ".settings-123.tmp")
	fresh := filepath.Join(directory, ".settings-456.tmp")
	unrelated := filepath.Join(directory, ".settings-789.txt")
	folder := filepath.Join(directory, ".settings-folder.tmp")
	for _, file := range []string{stale, fresh, unrelated} {
		if err := os.WriteFile(file, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	for _, entry := range []string{stale, unrelated, folder} {
		if err := os.Chtimes(entry, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if warnings := NewStore(Paths{ConfigFile: path}).Load().Warnings; len(warnings) != 0 {
		t.Fatalf("load warnings = %v", warnings)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temporary file remains: %v", err)
	}
	for _, kept := range []string{fresh, unrelated, folder} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was removed: %v", filepath.Base(kept), err)
		}
	}
}

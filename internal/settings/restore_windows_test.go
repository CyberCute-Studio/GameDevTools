//go:build windows

package settings

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestStoreRestoreAfterAFailedWriteReplacesTheSessionsSettings pins that
// once a restore has backed up the file a session never loaded — one Load
// could not read — the session's settings are the ones a later restore
// replaces, though that restore's write failed: the session took the
// restored settings, derived from the file's, so a later restore keeps the
// presets the session holds then, renamed, deleted, or created since, and
// not the file's again. The file is held open without sharing its
// deletion, as a sync client or a virus scanner can, so reading it and
// backing it up work but replacing it fails.
func TestStoreRestoreAfterAFailedWriteReplacesTheSessionsSettings(t *testing.T) {
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
	const fileSection = `{"presets":{"version":1,"from":"file"}}`
	stored := Default()
	stored.Modules["screenshot"] = json.RawMessage(fileSection)
	if err := NewStore(Paths{ConfigFile: path}).Save(stored); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("hold the settings file open: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })

	keepScreenshot := func(replaced Settings) (Settings, error) {
		restored := Default()
		if section, exists := replaced.Modules["screenshot"]; exists {
			restored.Modules["screenshot"] = section
		}
		return restored, nil
	}
	restored, backup, err := store.Restore(Default(), keepScreenshot)
	if err == nil || errors.Is(err, ErrNotRestored) || backup == "" {
		t.Fatalf("Restore() = backup %q, error %v; want the file backed up and the write failed", backup, err)
	}
	if got := compactJSON(t, restored.Modules["screenshot"]); got != fileSection {
		t.Fatalf("restored screenshot section = %q, want the file's %q", got, fileSection)
	}

	// The session takes the restored settings and changes its presets.
	const sessionSection = `{"presets":{"version":1,"from":"session"}}`
	session := restored
	session.Modules = maps.Clone(restored.Modules)
	session.Modules["screenshot"] = json.RawMessage(sessionSection)
	restored, _, err = store.Restore(session, keepScreenshot)
	if errors.Is(err, ErrNotRestored) {
		t.Fatalf("second Restore() error = %v", err)
	}
	if got := compactJSON(t, restored.Modules["screenshot"]); got != sessionSection {
		t.Fatalf("second restore kept the screenshot section %q, want the session's %q, not the file's again", got, sessionSection)
	}
}

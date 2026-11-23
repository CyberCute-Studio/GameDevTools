//go:build windows

package output

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
)

// TestHardLinkSaveWaitsToRemoveABrieflyHeldTemporaryName pins that a program
// holding the temporary name for a moment after the hard link, as a virus
// scanner does, leaves neither a warning nor the temporary file behind.
func TestHardLinkSaveWaitsToRemoveABrieflyHeldTemporaryName(t *testing.T) {
	t.Cleanup(func() { linkForTest = nil })
	linkForTest = func(oldname, newname string) error {
		if err := os.Link(oldname, newname); err != nil {
			return err
		}
		handle := holdOpen(t, oldname)
		time.AfterFunc(60*time.Millisecond, func() { windows.CloseHandle(handle) })
		return nil
	}
	folder := t.TempDir()
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decodeFile(t, path)
	if names := folderNames(t, folder); len(names) != 1 || names[0] != filepath.Base(path) {
		t.Fatalf("the folder holds %v, want only %s", names, filepath.Base(path))
	}
}

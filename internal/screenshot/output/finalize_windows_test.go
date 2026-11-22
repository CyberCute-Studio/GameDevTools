//go:build windows

package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
)

// withoutHardLinks makes the save's hard link fail as it does on a volume
// that has none, such as FAT32 or exFAT, running before first, if any, with
// the temporary file's path. No FAT32 or exFAT volume is needed, so the rename
// these tests exercise runs on the test machine's own file system.
func withoutHardLinks(t *testing.T, before func(temporary string)) {
	t.Helper()
	previous := createHardLink
	t.Cleanup(func() { createHardLink = previous })
	createHardLink = func(oldname, newname string) error {
		if before != nil {
			before(oldname)
		}
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: windows.ERROR_INVALID_FUNCTION}
	}
}

// folderNames lists a folder's entries.
func folderNames(t *testing.T, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

// TestSaveWithoutHardLinksMovesTheFinishedFile pins that a capture is saved
// on a volume with no hard links, under its final name, with no temporary
// file left.
func TestSaveWithoutHardLinksMovesTheFinishedFile(t *testing.T) {
	withoutHardLinks(t, nil)
	folder := t.TempDir()
	path, err := save(sampleImage(4, 3), optionsFor(folder, screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if bounds := decodeFile(t, path).Bounds(); bounds.Dx() != 4 || bounds.Dy() != 3 {
		t.Fatalf("saved image is %v", bounds)
	}
	if names := folderNames(t, folder); len(names) != 1 || names[0] != filepath.Base(path) {
		t.Fatalf("the folder holds %v, want only %s", names, filepath.Base(path))
	}
}

// TestSaveWithoutHardLinksNeverReplacesAnEntry pins SCR-065 on such a volume:
// a file or folder that has the final name is left as it is, and the capture
// takes the next numbered name.
func TestSaveWithoutHardLinksNeverReplacesAnEntry(t *testing.T) {
	withoutHardLinks(t, nil)
	now := time.Date(2026, 11, 22, 10, 15, 0, 123*int(time.Millisecond), time.Local)
	base := "Screenshot_" + now.Format(timestampLayout) + "123"
	folder := t.TempDir()
	existing := filepath.Join(folder, base+".png")
	if err := os.WriteFile(existing, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, base+"_2.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), now)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if want := filepath.Join(folder, base+"_3.png"); path != want {
		t.Fatalf("saved to %s, want %s", path, want)
	}
	decodeFile(t, path)
	if data, err := os.ReadFile(existing); err != nil || !bytes.Equal(data, []byte("keep me")) {
		t.Fatalf("the existing file now holds %q (read error %v)", data, err)
	}
	if info, err := os.Stat(filepath.Join(folder, base+"_2.png")); err != nil || !info.IsDir() {
		t.Fatalf("the existing folder was changed: %v", err)
	}
	if names := folderNames(t, folder); len(names) != 3 {
		t.Fatalf("the folder holds %v, want the two entries and the capture", names)
	}
}

// TestSaveWithoutHardLinksWaitsForABriefOpen pins that a program holding the
// finished file open for a moment, as a virus scanner does on a USB drive,
// does not fail the save.
func TestSaveWithoutHardLinksWaitsForABriefOpen(t *testing.T) {
	withoutHardLinks(t, func(temporary string) {
		name, err := windows.UTF16PtrFromString(temporary)
		if err != nil {
			t.Error(err)
			return
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
			windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Error(err)
			return
		}
		time.AfterFunc(60*time.Millisecond, func() { windows.CloseHandle(handle) })
	})
	folder := t.TempDir()
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decodeFile(t, path)
	if names := folderNames(t, folder); len(names) != 1 {
		t.Fatalf("the folder holds %v, want only the capture", names)
	}
}

// TestSaveWithoutHardLinksReportsAFailedMove pins that a rename that fails is
// a write failure that leaves no file behind (SCR-066).
func TestSaveWithoutHardLinksReportsAFailedMove(t *testing.T) {
	withoutHardLinks(t, func(temporary string) {
		if err := os.Remove(temporary); err != nil {
			t.Error(err)
		}
	})
	folder := t.TempDir()
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
	assertKind(t, err, KindWrite)
	if path != "" {
		t.Fatalf("path = %q after a failed save", path)
	}
	if names := folderNames(t, folder); len(names) != 0 {
		t.Fatalf("the failed save left %v", names)
	}
}

// TestSaveWithoutHardLinksInALongPath pins that the rename works past the
// 260-character limit, as the rest of a save does through package os.
func TestSaveWithoutHardLinksInALongPath(t *testing.T) {
	withoutHardLinks(t, nil)
	folder := t.TempDir()
	for len(folder) < 300 {
		folder = filepath.Join(folder, strings.Repeat("d", 40))
	}
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decodeFile(t, path)
	if names := folderNames(t, folder); len(names) != 1 {
		t.Fatalf("the folder holds %v, want only the capture", names)
	}
}

func TestExtendedLengthPath(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{`C:\Shots\a.png`, `\\?\C:\Shots\a.png`},
		{`\\server\share\a.png`, `\\?\UNC\server\share\a.png`},
		{`\\.\C:\Shots\a.png`, `\\?\C:\Shots\a.png`},
		{`\??\C:\Shots\a.png`, `\\?\C:\Shots\a.png`},
		{`\\?\C:\Shots\a.png`, `\\?\C:\Shots\a.png`},
		{`\\?\UNC\server\share\a.png`, `\\?\UNC\server\share\a.png`},
	} {
		if got := extendedLengthPath(test.path); got != test.want {
			t.Errorf("extendedLengthPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

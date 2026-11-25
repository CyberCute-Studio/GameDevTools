//go:build windows

package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// recordShellStarts replaces startShell for the test and returns a channel
// that receives each request the shell would have been handed.
func recordShellStarts(t *testing.T) <-chan string {
	t.Helper()
	started := make(chan string, 4)
	previous := startShell
	startShell = func(what string, _, _, _ *uint16) error {
		started <- what
		return nil
	}
	t.Cleanup(func() { startShell = previous })
	return started
}

// TestExplorerDoesNotStartWhileDialogsAreHeld is Open Folder asked for just
// before a capture, whose call reaches the shell once the capture has begun:
// the reveal of a saved screenshot, and the Image Resizer's Open Output
// Folder, each look at the file on their own goroutine first, which a slow
// drive can hold up. File Explorer started then would open during the
// capture's screen read and appear in the image (SCR-044). Each waits until
// the hold ends, and then starts it.
func TestExplorerDoesNotStartWhileDialogsAreHeld(t *testing.T) {
	if DialogsHeld() {
		t.Fatal("the dialogs were already held as the test began")
	}
	folder := t.TempDir()
	file := filepath.Join(folder, "Screenshot_1.png")
	if err := os.WriteFile(file, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, open := range []struct {
		name string
		call func() error
	}{
		{"reveal", func() error { return RevealInFolder(file) }},
		{"folder", func() error { return OpenFolder(folder) }},
	} {
		started := recordShellStarts(t)
		release := HoldDialogs()
		t.Cleanup(release)
		done := make(chan error, 1)
		go func() { done <- open.call() }()
		select {
		case <-started:
			t.Fatalf("%s: File Explorer started while a capture held the dialogs (SCR-044)", open.name)
		case err := <-done:
			t.Fatalf("%s: returned %v while the dialogs were held, without starting File Explorer", open.name, err)
		case <-time.After(300 * time.Millisecond):
		}
		release()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: File Explorer did not start once the hold ended", open.name)
		}
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", open.name, err)
		}
	}
}

// TestExplorerIsNotStartedForAPathGoneDuringAHold is Open Folder asked for
// just before a selection, during which the file or folder it looked at is
// deleted, as a sync client or a script may: Explorer handed a missing file
// would open some other folder and report success. Each looks again once the
// hold ends and refuses the path instead.
func TestExplorerIsNotStartedForAPathGoneDuringAHold(t *testing.T) {
	if DialogsHeld() {
		t.Fatal("the dialogs were already held as the test began")
	}
	for _, open := range []struct {
		name string
		call func(folder, file string) error
		want error
	}{
		{"reveal", func(_, file string) error { return RevealInFolder(file) }, errSavedFileNotFound},
		{"folder", func(folder, _ string) error { return OpenFolder(folder) }, fs.ErrNotExist},
	} {
		folder := filepath.Join(t.TempDir(), "Screenshots")
		if err := os.Mkdir(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(folder, "Screenshot_1.png")
		if err := os.WriteFile(file, []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
		started := recordShellStarts(t)
		release := HoldDialogs()
		t.Cleanup(release)
		done := make(chan error, 1)
		go func() { done <- open.call(folder, file) }()
		select {
		case err := <-done:
			t.Fatalf("%s: returned %v while the dialogs were held", open.name, err)
		case <-time.After(300 * time.Millisecond):
		}
		if err := os.RemoveAll(folder); err != nil {
			t.Fatal(err)
		}
		release()
		select {
		case err := <-done:
			if !errors.Is(err, open.want) {
				t.Fatalf("%s: returned %v for a path gone during the hold, want %v", open.name, err, open.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: did not return once the hold ended", open.name)
		}
		select {
		case what := <-started:
			t.Fatalf("%s: File Explorer was handed %s, gone during the hold", open.name, what)
		default:
		}
	}
}

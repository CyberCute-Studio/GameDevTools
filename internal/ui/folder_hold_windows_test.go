//go:build windows

package ui

import (
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

//go:build !windows

package output

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteGivesResultsTheUsualPermissions: outside Windows a result keeps
// the permissions of its temporary file, which are those os.Create gives a
// file, not the owner-only ones of os.CreateTemp.
func TestWriteGivesResultsTheUsualPermissions(t *testing.T) {
	out := t.TempDir()
	item := Item{Action: ActionCreate, Destination: filepath.Join(out, "photo.png")}
	if _, err := Write(context.Background(), item, func(w io.Writer) error {
		_, err := io.WriteString(w, "result")
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	usual, err := os.Create(filepath.Join(out, "usual.png"))
	if err != nil {
		t.Fatal(err)
	}
	usual.Close()
	got, err := os.Stat(item.Destination)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(usual.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode().Perm() != want.Mode().Perm() {
		t.Fatalf("the result's permissions are %v, want %v", got.Mode().Perm(), want.Mode().Perm())
	}
}

// TestLinkUnsupported: a file system without hard links is told apart from
// a name that is taken.
func TestLinkUnsupported(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EPERM, syscall.ENOTSUP, syscall.EOPNOTSUPP} {
		if !linkUnsupported(&os.LinkError{Op: "link", Err: errno}) {
			t.Fatalf("%v is not read as a file system without hard links", errno)
		}
	}
	if linkUnsupported(&os.LinkError{Op: "link", Err: syscall.EEXIST}) || !errors.Is(&os.LinkError{Op: "link", Err: syscall.EEXIST}, os.ErrExist) {
		t.Fatal("a taken name is read as a file system without hard links")
	}
}

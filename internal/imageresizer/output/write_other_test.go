//go:build !windows

package output

import (
	"context"
	"errors"
	"io"
	"io/fs"
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

// TestLinkUnsupported: a file system without hard links, or one that refused
// the link, is told apart from a name that is taken, and keeps the system's
// error, so errors.Is reports EPERM as the permission failure it may be.
func TestLinkUnsupported(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EPERM, syscall.ENOTSUP, syscall.EOPNOTSUPP} {
		err := linkFailure(&os.LinkError{Op: "link", Err: errno})
		if !linkUnsupported(&os.LinkError{Op: "link", Err: errno}) || !errors.Is(err, errLinkUnsupported) || !errors.Is(err, errno) {
			t.Fatalf("%v: claim's error is %v, want errLinkUnsupported and the system's error", errno, err)
		}
	}
	if !errors.Is(linkFailure(&os.LinkError{Op: "link", Err: syscall.EPERM}), fs.ErrPermission) {
		t.Fatal("EPERM is no longer reported as a permission failure")
	}
	taken := &os.LinkError{Op: "link", Err: syscall.EEXIST}
	if linkUnsupported(taken) || linkFailure(taken) != error(taken) || !errors.Is(taken, os.ErrExist) {
		t.Fatal("a taken name is read as a file system without hard links")
	}
}

// TestDiskFull: a full drive and a used-up disk quota are both reported as
// ErrDiskFull (RSZ-030).
func TestDiskFull(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.EDQUOT} {
		if err := writeFailure("write result", &fs.PathError{Op: "write", Path: "result", Err: errno}); !errors.Is(err, ErrDiskFull) {
			t.Fatalf("%v: error = %v, want ErrDiskFull", errno, err)
		}
	}
}

// TestCheckWriteRefusesReplacing: outside Windows, where planning plans no
// replacement, an ActionReplace item is refused before anything is encoded.
func TestCheckWriteRefusesReplacing(t *testing.T) {
	out := t.TempDir()
	item := Item{Action: ActionReplace, Destination: filepath.Join(out, "photo.png")}
	if err := item.CheckWrite(); !errors.Is(err, errReplaceUnsupported) {
		t.Fatalf("CheckWrite = %v, want errReplaceUnsupported", err)
	}
	encoded := false
	_, err := Write(context.Background(), item, func(w io.Writer) error {
		encoded = true
		return nil
	})
	var writeErr *Error
	if !errors.As(err, &writeErr) || writeErr.Kind != KindWrite || encoded || !errors.Is(err, errReplaceUnsupported) {
		t.Fatalf("Write = %v, encoded %v, want errReplaceUnsupported before encoding", err, encoded)
	}
}

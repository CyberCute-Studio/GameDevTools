//go:build !windows

package output

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

var (
	// errReplaceUnsupported is the cause of the KindWrite error Write and
	// Item.CheckWrite return for an ActionReplace item outside Windows, where
	// planning never proves a file replaceable and so plans none.
	errReplaceUnsupported = errors.New("replacing an existing file is supported on Windows only")
	// errLinkUnsupported is the cause of the KindWrite error Write returns
	// outside Windows when the output folder's file system does not give the
	// result's temporary file a second name, which saving a result without
	// replacing anything needs there. link(2) reports EPERM both for a file
	// system without hard links and for a link the system refuses, so the
	// error wraps the system's error too, and errors.Is reports EPERM as
	// fs.ErrPermission.
	errLinkUnsupported = errors.New("saving a result outside Windows needs a hard link, which the output folder's file system does not support or refused")
)

// tempFile is the temporary file a result is written to.
type tempFile struct {
	file *os.File
	path string
}

// createTemp creates a new temporary file in the folder, never opening an
// existing file. Its permissions are those os.Create gives a file, so the
// result, which keeps them, is as readable as any other file the user saves.
func createTemp(folder string) (*tempFile, error) {
	var file *os.File
	path, err := unusedName(folder, tempName, func(path string) error {
		var err error
		file, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &tempFile{file: file, path: path}, nil
}

func (t *tempFile) Write(p []byte) (int, error) { return t.file.Write(p) }

func (t *tempFile) sync() error { return t.file.Sync() }

// claim gives the file the destination's name without replacing anything
// there: a hard link, which the system refuses to create over an existing
// entry.
func (t *tempFile) claim(destination string) error {
	return linkFailure(os.Link(t.path, destination))
}

// linkFailure is the error claim returns for link(2)'s: a link the file
// system does not support, or refused, is marked errLinkUnsupported and keeps
// the system's error.
func linkFailure(err error) error {
	if err != nil && !nameTaken(err) && linkUnsupported(err) {
		return fmt.Errorf("%w: %w", errLinkUnsupported, err)
	}
	return err
}

// finish closes the file and removes its temporary name.
func (t *tempFile) finish() error {
	t.file.Close()
	return os.Remove(t.path)
}

// discard closes the file and removes it: the file has no name but its
// temporary one, which finish removes.
func (t *tempFile) discard() error { return t.finish() }

func replace(_ *tempFile, item Item, _ writeSteps) (bool, error) {
	return false, checkReplace(item)
}

func checkReplace(Item) error {
	return kindError(KindWrite, "replace existing file", errReplaceUnsupported)
}

func nameTaken(err error) bool { return errors.Is(err, fs.ErrExist) }

// diskFull reports whether an operation failed because the drive is full, or
// the user's disk quota on it is used up.
func diskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// linkUnsupported reports whether creating a hard link failed because the
// file system has none, or refused it: link(2) reports EPERM for either, and
// some file systems report ENOTSUP.
func linkUnsupported(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}

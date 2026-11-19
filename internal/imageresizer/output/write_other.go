//go:build !windows

package output

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// errReplaceUnsupported is the cause of the KindWrite error Write returns for
// an ActionReplace item outside Windows, where planning never proves a file
// replaceable and so plans none.
var errReplaceUnsupported = errors.New("replacing an existing file is supported on Windows only")

// tempFile is the temporary file a result is written to.
type tempFile struct {
	file *os.File
	path string
}

func createTemp(folder string) (*tempFile, error) {
	file, err := os.CreateTemp(folder, tempPrefix+"*"+tempSuffix)
	if err != nil {
		return nil, err
	}
	return &tempFile{file: file, path: file.Name()}, nil
}

func (t *tempFile) Write(p []byte) (int, error) { return t.file.Write(p) }

func (t *tempFile) sync() error { return t.file.Sync() }

// claim gives the file the destination's name without replacing anything
// there: a hard link, which the system refuses to create over an existing
// entry.
func (t *tempFile) claim(destination string) error {
	return os.Link(t.path, destination)
}

// finish closes the file and removes its temporary name.
func (t *tempFile) finish() error {
	t.file.Close()
	return os.Remove(t.path)
}

// discard closes the file and removes it.
func (t *tempFile) discard() error {
	t.file.Close()
	return os.Remove(t.path)
}

func replace(*tempFile, Item, writeSteps) (bool, error) {
	return false, kindError(KindWrite, "replace existing file", errReplaceUnsupported)
}

func nameTaken(err error) bool { return errors.Is(err, fs.ErrExist) }

func diskFull(err error) bool { return errors.Is(err, syscall.ENOSPC) }

//go:build !windows

package output

import (
	"errors"
	"io/fs"
	"path/filepath"
)

// fileID is empty outside Windows, where the production application does
// not run.
type fileID struct{}

var errIdentityUnsupported = errors.New("file identity is available on Windows only")

// identify always fails outside Windows, so planning there never proves an
// existing file is not a source and an Overwrite job renames instead of
// replacing.
func identify(string) (fileID, uint32, error) {
	return fileID{}, 0, errIdentityUnsupported
}

// fileMissing reports whether a file could not be opened because it does
// not exist.
func fileMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// storedFile reports no irregular entry as a file outside Windows, so such
// an entry is never replaced.
func storedFile(string) (bool, error) {
	return false, nil
}

// entryName returns the path's last element outside Windows, which has no
// second names such as 8.3 short names.
func entryName(path string) (string, error) {
	return filepath.Base(path), nil
}

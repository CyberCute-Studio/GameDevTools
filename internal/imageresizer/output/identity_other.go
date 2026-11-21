//go:build !windows

package output

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// fileID is empty outside Windows, where the production application does
// not run.
type fileID struct{}

// anyVolume is the identity itself outside Windows, where there is none.
func (id fileID) anyVolume() fileID { return id }

var errIdentityUnsupported = errors.New("file identity is available on Windows only")

// identify always fails outside Windows, so planning there never proves an
// existing file is not a source and an Overwrite job renames instead of
// replacing.
func identify(string) (identity, error) {
	return identity{}, errIdentityUnsupported
}

// resolvedPath is the form of a path PathKey compares: outside Windows, the
// cleaned path.
func resolvedPath(path string) string {
	return filepath.Clean(path)
}

// volumeName, isAbsolute, cleanPath, parentPath, and baseName are package
// filepath's own outside Windows, where no path has an extended-length
// prefix.
func volumeName(path string) string { return filepath.VolumeName(path) }
func isAbsolute(path string) bool   { return filepath.IsAbs(path) }
func cleanPath(path string) string  { return filepath.Clean(path) }
func parentPath(path string) string { return filepath.Dir(path) }
func baseName(path string) string   { return filepath.Base(path) }

// resolvesAsWritten reports whether the system resolves a cleaned absolute
// path to exactly that path. Outside Windows every name is kept as written.
func resolvesAsWritten(string) bool { return true }

// namesStream reports whether a path names a file stream. Outside Windows a
// colon is an ordinary character of a name.
func namesStream(string) bool { return false }

// mkdirAll creates a folder and every missing folder that holds it.
func mkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// nameNotFound reports whether an error says the path was not found. A path
// whose folder is gone fails with the same ENOENT, so fileMissing checks the
// folder: a name in an output folder removed while a plan is made is not
// free.
func nameNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// findEntry describes the entry at path outside Windows, which has no second
// names such as 8.3 short names, so its name is the path's last element, and
// where only a regular file is a file, so an irregular entry is never
// replaced.
func findEntry(path string) (entry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return entry{}, err
	}
	return entry{name: filepath.Base(path), file: info.Mode().IsRegular()}, nil
}

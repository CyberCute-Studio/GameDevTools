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

// resolvesAsWritten reports whether the system resolves a cleaned absolute
// path to exactly that path. Outside Windows every name is kept as written.
func resolvesAsWritten(string) bool { return true }

// namesStream reports whether a path names a file stream. Outside Windows a
// colon is an ordinary character of a name.
func namesStream(string) bool { return false }

// mkdirAll creates a folder and every missing folder that holds it.
func mkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// fileMissing reports whether a file could not be opened because no entry
// has its name in its folder, as on Windows. The error alone cannot tell, as
// a path whose folder is gone fails with the same ENOENT, so the path's
// folder must still be one, as the planner's filesystem reports it: a name
// in an output folder removed while a plan is made is not free.
func fileMissing(files fileSystem, err error) bool {
	var pathErr *fs.PathError
	if !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &pathErr) {
		return false
	}
	info, statErr := files.Stat(filepath.Dir(pathErr.Path))
	return statErr == nil && info.IsDir()
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

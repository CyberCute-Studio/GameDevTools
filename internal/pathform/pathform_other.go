//go:build !windows

// Package pathform reads an output folder's path the same way on every Go
// release. Only Windows paths ever read differently, so outside Windows,
// where the production application does not run, these are package
// filepath's own.
package pathform

import "path/filepath"

func VolumeName(path string) string { return filepath.VolumeName(path) }
func IsAbs(path string) bool        { return filepath.IsAbs(path) }
func Clean(path string) string      { return filepath.Clean(path) }
func Dir(path string) string        { return filepath.Dir(path) }
func Base(path string) string       { return filepath.Base(path) }

// ReadsAsOneFolder is always true: only Windows reads a name differently by
// where it falls in a path.
func ReadsAsOneFolder(string) bool { return true }

// SaveFolder is the folder as written: only Windows reads a name differently
// by where it falls in a path.
func SaveFolder(folder string) (string, bool) { return folder, true }

// CreationPath is the folder as written: only Windows shortens a name by
// where it falls in a path.
func CreationPath(folder string) string { return folder }

// Join is filepath.Join for a folder and the names under it.
func Join(folder string, names ...string) string {
	return filepath.Join(append([]string{folder}, names...)...)
}

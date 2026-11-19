//go:build windows

package imageresizer

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultOutputFolder resolves the per-user Pictures folder and appends the
// resized-image output directory used when the user has not chosen a folder
// (RSZ-021). When the known Pictures folder cannot be located, or gives a
// folder the output folder rules refuse, the Pictures folder in the user's
// profile is used, and when that gives none either, the error says why.
func DefaultOutputFolder() (string, error) {
	pictures, err := windows.KnownFolderPath(windows.FOLDERID_Pictures, 0)
	return defaultFolder(pictures, err, os.UserHomeDir)
}

// resolveFolder resolves an absolute path as Windows resolves it before it
// opens or creates anything, dropping trailing spaces and periods it would
// drop. A path it cannot resolve is returned as it is, for the folder rules
// to judge.
func resolveFolder(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	full, err := windows.FullPath(path)
	if err != nil {
		return path
	}
	return full
}

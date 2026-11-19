//go:build !windows

package imageresizer

import "errors"

// DefaultOutputFolder is a Windows-only capability; the production
// application only runs on Windows.
func DefaultOutputFolder() (string, error) {
	return "", errors.New("the default resize output folder is available on Windows only")
}

// resolveFolder returns the path as it is: outside Windows every name is
// kept as written.
func resolveFolder(path string) string { return path }

//go:build !windows

package imageresizer

import "errors"

// DefaultOutputFolder is a Windows-only capability; the production
// application only runs on Windows.
func DefaultOutputFolder() (string, error) {
	return "", errors.New("the default resize output folder is available on Windows only")
}

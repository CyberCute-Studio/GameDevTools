//go:build !windows

package screenshot

import "errors"

// DefaultOutputFolder is a Windows-only capability; the production
// application only runs on Windows.
func DefaultOutputFolder() (string, error) {
	return "", errors.New("the default screenshot output folder is available on Windows only")
}

//go:build windows

package imageresizer

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultOutputFolder resolves the per-user Pictures folder and appends the
// resized-image output directory used when the user has not chosen a folder
// (RSZ-021). A folder the output folder rules refuse is returned as an error,
// as a Pictures folder that cannot be located is.
func DefaultOutputFolder() (string, error) {
	pictures, err := windows.KnownFolderPath(windows.FOLDERID_Pictures, 0)
	if err != nil || pictures == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			if err != nil {
				return "", fmt.Errorf("locate Pictures folder: %w", err)
			}
			return "", fmt.Errorf("locate Pictures folder: %w", homeErr)
		}
		pictures = filepath.Join(home, "Pictures")
	}
	return defaultFolderIn(pictures)
}

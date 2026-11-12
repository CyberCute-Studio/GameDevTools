//go:build windows

package screenshot

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultOutputFolder resolves the per-user Pictures folder and appends the
// screenshot output directory used when the user has not chosen a folder.
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
	return filepath.Join(pictures, "GameDevTools", "Screenshots"), nil
}

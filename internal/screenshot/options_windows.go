//go:build windows

package screenshot

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/pathform"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// DefaultOutputFolder resolves the per-user Pictures folder and appends the
// screenshot output directory used when the user has not chosen a folder.
// When the known Pictures folder cannot be located, or gives a folder every
// capture would refuse, the Pictures folder in the user's profile is used,
// and when that gives none either, the error says why.
func DefaultOutputFolder() (string, error) {
	pictures, err := windows.KnownFolderPath(windows.FOLDERID_Pictures, 0)
	return defaultFolder(pictures, err, os.UserHomeDir)
}

// defaultFolder is the default output folder under the Pictures folder the
// system knows for the user, known, or, when that cannot be located (knownErr)
// or gives a folder a capture would refuse, under the Pictures folder in the
// user's profile, which home locates. When neither gives a folder, the error
// is the known folder's refusal, else the profile's, else why the folders
// could not be located.
func defaultFolder(known string, knownErr error, home func() (string, error)) (string, error) {
	var refused error
	if knownErr == nil && known != "" {
		folder, err := defaultFolderIn(known)
		if err == nil {
			return folder, nil
		}
		refused = err
	}
	profile, homeErr := home()
	if homeErr == nil {
		folder, err := defaultFolderIn(pathform.Join(profile, "Pictures"))
		if err == nil {
			return folder, nil
		}
		if refused == nil {
			refused = err
		}
	}
	switch {
	case refused != nil:
		return "", refused
	case knownErr != nil:
		return "", fmt.Errorf("locate Pictures folder: %w", knownErr)
	}
	return "", fmt.Errorf("locate Pictures folder: %w", homeErr)
}

// defaultFolderIn is the default output folder under a Pictures folder, or
// why a capture could not use it. The Pictures folder's path is first resolved
// as Windows resolves it, so one that ends in a period or a space, which
// Windows drops, gives a folder in the Pictures folder Windows opens for it.
// A path that is not absolute is refused rather than resolved, as it would
// lead under whatever folder the application was started from, and so is one
// Windows resolves to a device, as it does a last name such as NUL.
func defaultFolderIn(pictures string) (string, error) {
	named := pathform.Join(pictures, "GameDevTools", "Screenshots")
	if !pathform.IsAbs(pictures) {
		return "", fmt.Errorf("%s: %s", named, ui.OutputFolderNotAbsoluteReason)
	}
	if full, err := windows.FullPath(pictures); err == nil {
		if strings.HasPrefix(pathform.VolumeName(full), `\\.\`) && !strings.HasPrefix(pathform.VolumeName(pictures), `\\.\`) {
			return "", fmt.Errorf("%s: %s", named, ui.OutputFolderNameReason)
		}
		pictures = full
	}
	folder := pathform.Join(pictures, "GameDevTools", "Screenshots")
	if !pathform.NamesOneFolder(folder) {
		return "", fmt.Errorf("%s: %s", folder, ui.OutputFolderNameReason)
	}
	return folder, nil
}

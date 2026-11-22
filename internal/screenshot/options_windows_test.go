//go:build windows

package screenshot

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// TestSetOutputFolderRefusesAFolderEveryCaptureWouldRefuse pins that choosing
// a folder Windows would read as another place fails where the user chose it,
// keeping the previous folder, while the same folder already stored still
// validates, so it cannot fail the whole settings file.
func TestSetOutputFolderRefusesAFolderEveryCaptureWouldRefuse(t *testing.T) {
	root := t.TempDir()
	previous := filepath.Join(root, "Shots")
	manager, _ := newTestOptionsManager(t, filepath.Join(root, "settings.json"), previous)
	for _, folder := range []string{root + `\Shots `, root + `\Shots.`, root + `\x .\Shots`, root + `\NUL`, root + `\COM1\Shots`, root + `\Shots::$INDEX_ALLOCATION`} {
		if err := manager.SetOutputFolder(folder); err == nil || err.Error() != ui.OutputFolderNameInvalid {
			t.Errorf("SetOutputFolder(%q) = %v, want the folder-name refusal", folder, err)
		}
		if got := manager.Options().OutputFolder; got != previous {
			t.Fatalf("after refusing %q the folder is %q, want %q", folder, got, previous)
		}
		options := DefaultOptions(folder)
		if err := options.Validate(); err != nil {
			t.Errorf("Validate with stored folder %q: %v", folder, err)
		}
	}
}

// TestStoredFolderSpellingsStayAbsolute pins that a folder an earlier build
// accepted still validates under Go 1.27, whose package filepath calls these
// spellings relative. A stored folder that failed here would fail the whole
// settings file's validation, and every module would lose its settings.
func TestStoredFolderSpellingsStayAbsolute(t *testing.T) {
	for _, folder := range []string{
		`\??\UNC\nas\shots`,
		`\\?\UNC\nas\shots`,
		`\\server\..\x`,
		`//server/../x`,
		`\\.\..\x`,
		`\??\..\x`,
	} {
		options := DefaultOptions(folder)
		if err := options.Validate(); err != nil {
			t.Errorf("Validate with output folder %q: %v", folder, err)
		}
	}
}

// TestDefaultFolderIsOneACaptureCanUse pins that a Pictures folder whose path
// ends in a period or a space gives the folder Windows opens for it, and that
// one no capture could use falls back to the profile's, or is reported as
// unavailable rather than selected.
func TestDefaultFolderIsOneACaptureCanUse(t *testing.T) {
	const suffix = `\GameDevTools\Screenshots`
	profile := func() (string, error) { return `C:\Users\me`, nil }
	refusedProfile := func() (string, error) { return `C:\COM1\me`, nil }
	noProfile := func() (string, error) { return "", errors.New("no profile") }
	for _, test := range []struct {
		known string
		err   error
		home  func() (string, error)
		want  string
	}{
		{`D:\Pictures`, nil, noProfile, `D:\Pictures` + suffix},
		{`D:\Pictures.`, nil, noProfile, `D:\Pictures` + suffix},
		{`D:\Pictures `, nil, noProfile, `D:\Pictures` + suffix},
		{`D:\COM1\Pictures`, nil, profile, `C:\Users\me\Pictures` + suffix},
		{"", errors.New("not known"), profile, `C:\Users\me\Pictures` + suffix},
		// Windows resolves a last name of NUL to the device, whatever follows
		// it once the default's own names are added.
		{`D:\Pics\NUL`, nil, profile, `C:\Users\me\Pictures` + suffix},
		{`D:\Pics\NUL.`, nil, profile, `C:\Users\me\Pictures` + suffix},
		{`Pictures`, nil, profile, `C:\Users\me\Pictures` + suffix},
		// Validate refuses this text, so every capture would.
		{"D:\\Pict\uFFFDres", nil, profile, `C:\Users\me\Pictures` + suffix},
	} {
		if got, err := defaultFolder(test.known, test.err, test.home); err != nil || got != test.want {
			t.Errorf("defaultFolder(%q) = %q, %v, want %q", test.known, got, err, test.want)
		}
	}
	got, err := defaultFolder(`D:\COM1\Pictures`, nil, refusedProfile)
	if err == nil || got != "" || !strings.Contains(err.Error(), `D:\COM1\Pictures`+suffix) {
		t.Errorf("defaultFolder with both refused = %q, %v, want the known folder's refusal", got, err)
	}
	if got, err := defaultFolder("D:\\Pict\uFFFDres", nil, noProfile); err == nil || got != "" || !strings.Contains(err.Error(), ui.OutputFolderTextReason) {
		t.Errorf("defaultFolder with invalid text and no profile = %q, %v, want the text refusal", got, err)
	}
	// Go 1.27's filepath.Join would move this path to the current drive, where
	// it would be accepted. Its share, "..", ends in a period, so it is refused
	// under its own name.
	serverDots := func() (string, error) { return `\\server\..\me`, nil }
	if got, err := defaultFolder("", errors.New("not known"), serverDots); err == nil || got != "" || !strings.Contains(err.Error(), `\\server\..\me\Pictures`+suffix) {
		t.Errorf("defaultFolder under a share named .. = %q, %v, want a refusal naming that path", got, err)
	}
	// A profile path that is not absolute must not resolve under the folder
	// the application was started from.
	for _, relative := range []string{`Profiles\me`, `.`, `C:`, `C:rel`, `\rooted\me`} {
		home := func() (string, error) { return relative, nil }
		if got, err := defaultFolder("", errors.New("not known"), home); err == nil || got != "" {
			t.Errorf("defaultFolder with profile %q = %q, %v, want a refusal", relative, got, err)
		}
	}
	if got, err := defaultFolder(`D:\Pics\NUL`, nil, noProfile); err == nil || got != "" || !strings.Contains(err.Error(), `D:\Pics\NUL`+suffix) {
		t.Errorf("defaultFolder under a device with no profile = %q, %v, want a refusal naming the folder", got, err)
	}
	if _, err := defaultFolder("", errors.New("not known"), noProfile); err == nil || !strings.Contains(err.Error(), "not known") {
		t.Errorf("defaultFolder with neither located = %v, want the known folder's failure", err)
	}
}

// TestAPathThatNamesNoFolderIsRefusedAndReported pins that a device, volume,
// or server path is refused when chosen and reported at startup when stored,
// by the reason that fits it, and still passes Validate, so a hand-edited
// settings file holding one cannot reset every other setting.
func TestAPathThatNamesNoFolderIsRefusedAndReported(t *testing.T) {
	root := t.TempDir()
	usable := filepath.Join(root, "Shots")
	manager, _ := newTestOptionsManager(t, filepath.Join(root, "settings.json"), usable)
	for _, folder := range []string{
		`\\.\pipe\x`, `\\?\C:`, `\\.\C:`, `\\server`, `\\?\UNC\server`, `\\\localhost\pipe\x`,
		`\??\C:`, `\??\pipe`, `\??\NUL`, `\??\UNC`, `\??\Volume{00000000-0000-0000-0000-000000000000}`,
		`\\server\..\x`, `\\\nas\photos`, `\\;x\nas\photos`, `\\nas\;x\photos`,
	} {
		if err := manager.SetOutputFolder(folder); err == nil || err.Error() != ui.OutputFolderRootInvalid {
			t.Errorf("SetOutputFolder(%q) = %v, want the root refusal", folder, err)
		}
		if got := manager.Options().OutputFolder; got != usable {
			t.Fatalf("after refusing %q the folder is %q, want %q", folder, got, usable)
		}
		options := DefaultOptions(folder)
		if err := options.Validate(); err != nil {
			t.Errorf("Validate with stored folder %q: %v", folder, err)
		}
		manager.options.OutputFolder = folder
		if got, want := manager.FolderWarning(), fmt.Sprintf(ui.OutputFolderStoredRootRefused, folder); got != want {
			t.Errorf("FolderWarning = %q, want %q", got, want)
		}
		manager.options.OutputFolder = usable
	}
}

// TestDefaultFolderReportsAPathThatNamesNoFolder pins that a Pictures folder
// leading through a device or IPC share is refused for that reason rather than
// for a name Windows would change. Both calls only read the path's text.
func TestDefaultFolderReportsAPathThatNamesNoFolder(t *testing.T) {
	for _, pictures := range []string{`\\.\pipe\Pictures`, `\\nas\pipe\Pictures`, `\\nas\IPC$\Pictures`} {
		_, err := defaultFolderIn(pictures)
		if err == nil || !strings.HasSuffix(err.Error(), ": "+ui.OutputFolderRootReason) {
			t.Errorf("defaultFolderIn(%q) = %v, want the root reason", pictures, err)
		}
	}
}

// TestValidateStillRefusesARelativeFolder pins that accepting a stored \??\
// path that names only a volume or device did not widen Validate to a path
// that is relative, a drive's current folder among them.
func TestValidateStillRefusesARelativeFolder(t *testing.T) {
	for _, folder := range []string{`C:`, `C:Shots`, `Shots`, `.`, `\Shots`, `/??/C:`} {
		options := DefaultOptions(folder)
		if err := options.Validate(); err == nil || err.Error() != ui.OutputFolderNotAbsolute {
			t.Errorf("Validate with output folder %q = %v, want the absolute-path refusal", folder, err)
		}
	}
}

// TestFolderWarningNamesAStoredFolderEveryCaptureRefuses pins that a stored
// folder the save rule refuses is reported at startup and stays selected, and
// that choosing a usable one ends the warning.
func TestFolderWarningNamesAStoredFolderEveryCaptureRefuses(t *testing.T) {
	root := t.TempDir()
	usable := filepath.Join(root, "Shots")
	manager, _ := newTestOptionsManager(t, filepath.Join(root, "settings.json"), usable)
	if warning := manager.FolderWarning(); warning != "" {
		t.Fatalf("FolderWarning for a usable folder = %q", warning)
	}
	stored := root + `\Shots.`
	manager.options.OutputFolder = stored
	if got, want := manager.FolderWarning(), fmt.Sprintf(ui.OutputFolderStoredRefused, stored); got != want {
		t.Fatalf("FolderWarning = %q, want %q", got, want)
	}
	if err := manager.SetOutputFolder(usable); err != nil {
		t.Fatal(err)
	}
	if warning := manager.FolderWarning(); warning != "" {
		t.Fatalf("FolderWarning after choosing a usable folder = %q", warning)
	}
}

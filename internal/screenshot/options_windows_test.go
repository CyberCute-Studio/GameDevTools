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
	} {
		if got, err := defaultFolder(test.known, test.err, test.home); err != nil || got != test.want {
			t.Errorf("defaultFolder(%q) = %q, %v, want %q", test.known, got, err, test.want)
		}
	}
	got, err := defaultFolder(`D:\COM1\Pictures`, nil, refusedProfile)
	if err == nil || got != "" || !strings.Contains(err.Error(), `D:\COM1\Pictures`+suffix) {
		t.Errorf("defaultFolder with both refused = %q, %v, want the known folder's refusal", got, err)
	}
	if _, err := defaultFolder("", errors.New("not known"), noProfile); err == nil || !strings.Contains(err.Error(), "not known") {
		t.Errorf("defaultFolder with neither located = %v, want the known folder's failure", err)
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

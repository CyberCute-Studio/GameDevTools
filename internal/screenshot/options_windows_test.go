//go:build windows

package screenshot

import (
	"path/filepath"
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
	for _, folder := range []string{root + `\Shots `, root + `\Shots.`, root + `\x .\Shots`, root + `\NUL`} {
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

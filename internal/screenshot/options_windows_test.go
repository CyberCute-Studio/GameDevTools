//go:build windows

package screenshot

import "testing"

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

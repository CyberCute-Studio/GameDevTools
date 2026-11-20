package preset

import (
	"encoding/json"
	"testing"
)

// TestLibrarySectionKeepsOnlyThePresetLibrary pins what Restore Default
// Settings keeps of a module section (SET-005): the preset library exactly
// as stored, its selected preset included, and nothing of the module's
// other settings; a section without a library keeps nothing.
func TestLibrarySectionKeepsOnlyThePresetLibrary(t *testing.T) {
	library := `{"version":1,"custom":[{"id":"custom.0123456789abcdef0123456789abcdef","name":"Icon","width":512,"height":512}],"selectedPresetId":"custom.0123456789abcdef0123456789abcdef"}`
	section := json.RawMessage(`{"capture":{"version":1,"mode":"full-display"},"presets":` + library + `}`)

	kept, err := LibrarySection(section)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(kept, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || string(fields[sectionKey]) != library {
		t.Fatalf("kept section = %s, want only the stored library %s", kept, library)
	}

	for _, empty := range []json.RawMessage{nil, json.RawMessage(`{"capture":{"version":1}}`)} {
		kept, err := LibrarySection(empty)
		if err != nil || kept != nil {
			t.Fatalf("LibrarySection(%s) = %s, %v, want nothing kept", empty, kept, err)
		}
	}
	if _, err := LibrarySection(json.RawMessage(`{"presets":{},"presets":{}}`)); err == nil {
		t.Fatal("a section with a duplicated library was accepted")
	}
}

package preset

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
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

// TestKeptSectionAddsTheSessionsOwnPresets pins what Restore Default
// Settings keeps when the settings it replaces are not the session's — the
// settings file's, which a session that could not read it never loaded
// (SET-005): the file's library with its selected preset, joined by the
// custom presets the session created, which could not be saved, one whose
// name the file's library already uses under the name Duplicate gives a
// copy. A session that holds only the replaced library's presets adds
// nothing, and the library is kept exactly as stored.
func TestKeptSectionAddsTheSessionsOwnPresets(t *testing.T) {
	const (
		fileID    = "custom.00000000000000000000000000000001"
		sessionID = "custom.00000000000000000000000000000002"
		clashID   = "custom.00000000000000000000000000000003"
	)
	library := `{"version":1,"custom":[{"id":"` + fileID + `","name":"Icon","width":512,"height":512,"resizeMode":"fit"}],"selectedPresetId":"` + fileID + `"}`
	replaced := json.RawMessage(`{"resize":{"version":1},"presets":` + library + `}`)
	session := json.RawMessage(`{"presets":{"version":1,"custom":[{"id":"` + sessionID + `","name":"Banner","width":300,"height":200,"resizeMode":"stretch"},{"id":"` + clashID + `","name":"icon","width":64,"height":64,"resizeMode":"crop-to-fill"}],"selectedPresetId":"` + sessionID + `"}}`)

	kept, err := KeptSection(shell.ModuleImageResizer, replaced, session, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeSection(shell.ModuleImageResizer, kept)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(kept, &fields); err != nil || len(fields) != 1 {
		t.Fatalf("kept section = %s, want only its presets", kept)
	}
	want := []Preset{
		{ID: fileID, Name: "Icon", Width: 512, Height: 512, ResizeMode: ResizeFit},
		{ID: sessionID, Name: "Banner", Width: 300, Height: 200, ResizeMode: ResizeStretch},
		{ID: clashID, Name: "icon copy", Width: 64, Height: 64, ResizeMode: ResizeCropToFill},
	}
	if !slices.Equal(stored.Custom, want) || stored.SelectedID != fileID {
		t.Fatalf("kept presets = %+v selecting %q, want %+v selecting the file's %q", stored.Custom, stored.SelectedID, want, fileID)
	}

	for _, same := range []json.RawMessage{replaced, json.RawMessage(`{"presets":` + library + `}`), nil, json.RawMessage(`{"resize":{"version":1}}`)} {
		kept, err := KeptSection(shell.ModuleImageResizer, replaced, same, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want, _ := LibrarySection(replaced); string(kept) != string(want) {
			t.Fatalf("KeptSection with session %s = %s, want the library as stored %s", same, kept, want)
		}
	}
	if kept, err := KeptSection(shell.ModuleImageResizer, nil, nil, nil); err != nil || kept != nil {
		t.Fatalf("KeptSection without a library = %s, %v, want nothing kept", kept, err)
	}
}

// TestKeptSectionLeavesAnInheritedPresetDeleted pins that a preset the session
// holds only because it took an older file's settings does not return to a
// settings file that lacks it, while one the session created or changed is
// kept.
func TestKeptSectionLeavesAnInheritedPresetDeleted(t *testing.T) {
	const (
		deletedID = "custom.00000000000000000000000000000001"
		editedID  = "custom.00000000000000000000000000000002"
		newID     = "custom.00000000000000000000000000000003"
	)
	entry := func(id, name string) string {
		return `{"id":"` + id + `","name":"` + name + `","width":64,"height":64,"resizeMode":"fit"}`
	}
	section := func(entries ...string) json.RawMessage {
		return json.RawMessage(`{"presets":{"version":1,"custom":[` + strings.Join(entries, ",") + `]}}`)
	}
	inherited := section(entry(deletedID, "Deleted"), entry(editedID, "Before"))
	session := section(entry(deletedID, "Deleted"), entry(editedID, "After"), entry(newID, "New"))

	kept, err := KeptSection(shell.ModuleImageResizer, section(), session, inherited)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeSection(shell.ModuleImageResizer, kept)
	if err != nil {
		t.Fatal(err)
	}
	want := []Preset{
		{ID: editedID, Name: "After", Width: 64, Height: 64, ResizeMode: ResizeFit},
		{ID: newID, Name: "New", Width: 64, Height: 64, ResizeMode: ResizeFit},
	}
	if !slices.Equal(stored.Custom, want) {
		t.Fatalf("kept presets = %+v, want %+v", stored.Custom, want)
	}
}

// TestKeptSectionKeepsAnotherBuildsModuleAsStored pins that the section of a
// module this build has no presets for — one a newer build added, which
// loading the settings accepts — keeps its library exactly as stored, in a
// format only that build can decode, rather than failing the restore of the
// default settings for every module (SET-005). A field of such a section
// given twice, which no validator of this build checks as the file loads,
// does not fail the restore either, as only the library is kept; a library
// given twice does, as which one to keep is not known.
func TestKeptSectionKeepsAnotherBuildsModuleAsStored(t *testing.T) {
	library := `{"version":2,"custom":[{"id":"custom.0123456789abcdef0123456789abcdef","name":"Sprite","width":64,"height":64,"layer":"top"}]}`
	for _, section := range []json.RawMessage{
		json.RawMessage(`{"canvas":{"zoom":2},"presets":` + library + `}`),
		json.RawMessage(`{"zoom":1,"presets":` + library + `,"zoom":2}`),
	} {
		for _, session := range []json.RawMessage{section, nil} {
			kept, err := KeptSection("editor", section, session, nil)
			if err != nil {
				t.Fatalf("KeptSection(%s) with session %s: %v", section, session, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(kept, &fields); err != nil || len(fields) != 1 || string(fields[sectionKey]) != library {
				t.Fatalf("kept section of %s with session %s = %s, want only the stored library %s", section, session, kept, library)
			}
		}
	}
	if _, err := KeptSection("editor", json.RawMessage(`{"presets":`+library+`,"presets":{}}`), nil, nil); err == nil {
		t.Fatal("a section with its library given twice was kept")
	}
}

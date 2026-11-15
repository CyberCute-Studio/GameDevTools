//go:build windows

package imageresizer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

func newSourcesTestView(t *testing.T) *NativeView {
	t.Helper()
	parent := newViewTestParent(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, _ := newNativeViewForTest(t, store, &value, testFolder(t), parent)
	return view
}

func writeViewSource(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func listContents(list *ui.ListBox) []string {
	rows := make([]string, 0, list.Count())
	for index := 0; index < list.Count(); index++ {
		rows = append(rows, list.ItemText(index))
	}
	return rows
}

func TestNativeViewAddImagesThroughPicker(t *testing.T) {
	view := newSourcesTestView(t)
	directory := t.TempDir()
	ready := writeViewSource(t, directory, "ready.png")
	unsupported := writeViewSource(t, directory, "notes.bmp")
	missing := filepath.Join(directory, "missing.jpg")

	// A cancelled picker changes nothing.
	view.pickFiles = func(windows.Handle, string) ([]string, bool, error) {
		return nil, false, nil
	}
	view.NativeCommand(sourcesCommandBase+addImagesCommand, 0)
	if view.list.Count() != 0 {
		t.Fatalf("cancelled picker added %d items", view.list.Count())
	}

	// A picker failure is reported, not swallowed.
	view.pickFiles = func(windows.Handle, string) ([]string, bool, error) {
		return nil, false, errors.New("dialog unavailable")
	}
	view.NativeCommand(sourcesCommandBase+addImagesCommand, 0)
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("status after picker failure = %q", got)
	}

	view.pickFiles = func(windows.Handle, string) ([]string, bool, error) {
		return []string{ready, unsupported, missing, ready}, true, nil
	}
	view.NativeCommand(sourcesCommandBase+addImagesCommand, 0)
	rows := listContents(view.list)
	if len(rows) != 3 {
		t.Fatalf("list rows = %v, want 3 (the duplicate path is not listed)", rows)
	}
	if rows[0] != "ready.png — Ready" || rows[1] != "notes.bmp — Unsupported format" || rows[2] != "missing.jpg — Cannot be read" {
		t.Fatalf("list rows = %v", rows)
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Warning:") || !strings.Contains(got, "Added 1 image(s).") ||
		!strings.Contains(got, "1 already in the list") || !strings.Contains(got, "1 listed with an unsupported format") ||
		!strings.Contains(got, "1 could not be read") {
		t.Fatalf("status after mixed add = %q", got)
	}

	// The originals are untouched: the inventory opens sources read-only
	// (RSZ-009).
	content, err := os.ReadFile(ready)
	if err != nil || string(content) != "source" {
		t.Fatalf("source after add = %q, %v", content, err)
	}
}

func TestNativeViewDropFilesAndFolder(t *testing.T) {
	view := newSourcesTestView(t)
	directory := t.TempDir()
	first := writeViewSource(t, directory, "one.png")
	writeViewSource(t, directory, "two.JPG")
	writeViewSource(t, directory, "skipped.bmp")
	nested := filepath.Join(directory, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	writeViewSource(t, nested, "nested.png")

	view.NativeDropFiles([]string{directory, first})
	rows := listContents(view.list)
	if len(rows) != 2 || rows[0] != "one.png — Ready" || rows[1] != "two.JPG — Ready" {
		t.Fatalf("rows after folder drop = %v, want the two supported files", rows)
	}
	if got := view.status.Text(); !strings.Contains(got, "Added 2 image(s).") || !strings.Contains(got, "1 already in the list") {
		t.Fatalf("status after folder drop = %q", got)
	}
}

func TestNativeViewRemoveSelectedAndClearSources(t *testing.T) {
	view := newSourcesTestView(t)
	directory := t.TempDir()
	first := writeViewSource(t, directory, "one.png")
	second := writeViewSource(t, directory, "two.png")
	third := writeViewSource(t, directory, "three.png")
	view.NativeDropFiles([]string{first, second, third})

	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.removeButton.Handle())); enabled != 0 {
		t.Fatal("Remove is enabled with no selection")
	}

	// Multi-selection removal keeps unselected entries (RSZ-006).
	view.list.SetSelected(0, true)
	view.list.SetSelected(2, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.removeButton.Handle())); enabled == 0 {
		t.Fatal("Remove stayed disabled with a selection")
	}
	// The full path of the first selected item is shown on demand (RSZ-005).
	if got := view.path.Text(); got != first {
		t.Fatalf("full path display = %q, want %q", got, first)
	}
	if got := view.pathDetail.Text(); got != "" {
		t.Fatalf("detail for a ready item = %q", got)
	}
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)
	rows := listContents(view.list)
	if len(rows) != 1 || rows[0] != "two.png — Ready" {
		t.Fatalf("rows after removal = %v", rows)
	}
	if got := view.status.Text(); !strings.Contains(got, "Removed 2 image(s).") {
		t.Fatalf("status after removal = %q", got)
	}

	// Clearing disables the Clear button itself, so focus must move off it
	// rather than being stranded on a disabled control.
	view.clearButton.Focus()
	view.NativeCommand(sourcesCommandBase+clearListCommand, 0)
	if view.list.Count() != 0 {
		t.Fatalf("rows after clear = %d", view.list.Count())
	}
	getFocus := windows.NewLazySystemDLL("user32.dll").NewProc("GetFocus")
	if focus, _, _ := getFocus.Call(); windows.Handle(focus) != view.list.Handle() {
		t.Fatalf("focus after clear = %#x, want the source list %#x", focus, view.list.Handle())
	}
	if got := view.path.Text(); got != "" {
		t.Fatalf("path display after clear = %q", got)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.clearButton.Handle())); enabled != 0 {
		t.Fatal("Clear stayed enabled for an empty list")
	}
	// Removed and cleared sources can be added again.
	view.NativeDropFiles([]string{first})
	if view.list.Count() != 1 {
		t.Fatalf("rows after re-adding = %d", view.list.Count())
	}
}

func TestNativeViewSelectionShowsStatusDetail(t *testing.T) {
	view := newSourcesTestView(t)
	directory := t.TempDir()
	unsupported := writeViewSource(t, directory, "notes.bmp")
	view.NativeDropFiles([]string{unsupported})

	view.list.SetSelected(0, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	if got := view.path.Text(); got != unsupported {
		t.Fatalf("full path display = %q, want %q", got, unsupported)
	}
	if got := view.pathDetail.Text(); !strings.Contains(got, "Unsupported format") || !strings.Contains(got, ".png, .jpg, or .jpeg") {
		t.Fatalf("status detail = %q", got)
	}
}

func TestNativeViewEmptyDropReportsNothingFound(t *testing.T) {
	view := newSourcesTestView(t)
	directory := t.TempDir()
	writeViewSource(t, directory, "only.bmp")
	view.NativeDropFiles([]string{directory})
	if view.list.Count() != 0 {
		t.Fatalf("unsupported folder contents listed %d items", view.list.Count())
	}
	if got := view.status.Text(); !strings.Contains(got, "No supported PNG or JPEG files") {
		t.Fatalf("status after empty folder drop = %q", got)
	}
}

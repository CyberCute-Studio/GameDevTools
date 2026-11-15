package imageresizer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSource(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("not-yet-validated image content"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSupportedExtensionIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{"a.png", "b.PNG", "c.jpg", "d.JPG", "e.jpeg", "f.JpEg"} {
		if !SupportedExtension(name) {
			t.Fatalf("SupportedExtension(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"a.bmp", "b.gif", "c.png.txt", "d", "e."} {
		if SupportedExtension(name) {
			t.Fatalf("SupportedExtension(%q) = true, want false", name)
		}
	}
}

func TestInventoryAddClassifiesMixedBatch(t *testing.T) {
	directory := t.TempDir()
	ready := writeSource(t, directory, "ready.png")
	upper := writeSource(t, directory, "upper.JPEG")
	unsupported := writeSource(t, directory, "notes.bmp")
	missing := filepath.Join(directory, "gone.png")

	inventory := NewInventory()
	summary := inventory.Add([]string{ready, upper, unsupported, missing})
	if summary.Added != 2 || summary.Unsupported != 1 || summary.Inaccessible != 1 || summary.Duplicates != 0 {
		t.Fatalf("summary = %+v, want 2 added, 1 unsupported, 1 inaccessible", summary)
	}
	items := inventory.Items()
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	if items[0].Status != StatusReady || items[1].Status != StatusReady {
		t.Fatalf("valid files must be Ready: %+v, %+v", items[0], items[1])
	}
	if items[0].Name != "ready.png" || items[0].Path != ready {
		t.Fatalf("first item = %+v", items[0])
	}
	if items[2].Status != StatusUnsupported || items[2].Detail == "" {
		t.Fatalf("unsupported item = %+v, want Unsupported with detail", items[2])
	}
	if items[3].Status != StatusInaccessible || items[3].Detail == "" {
		t.Fatalf("missing item = %+v, want Inaccessible with detail", items[3])
	}
}

func TestInventoryAddDeduplicatesFullPathsCaseInsensitively(t *testing.T) {
	directory := t.TempDir()
	ready := writeSource(t, directory, "photo.png")

	inventory := NewInventory()
	inventory.Add([]string{ready})
	summary := inventory.Add([]string{ready, strings.ToUpper(ready), filepath.Join(directory, ".", "photo.png")})
	if summary.Duplicates != 3 {
		t.Fatalf("duplicates = %d, want 3", summary.Duplicates)
	}
	if len(inventory.Items()) != 1 {
		t.Fatalf("items = %d, want the single original entry", len(inventory.Items()))
	}

	inventory.Remove([]int{0})
	summary = inventory.Add([]string{ready})
	if summary.Added != 1 || summary.Duplicates != 0 {
		t.Fatalf("re-add after removal = %+v, want 1 added", summary)
	}
}

func TestInventoryAddExpandsFoldersNonrecursively(t *testing.T) {
	directory := t.TempDir()
	first := writeSource(t, directory, "one.png")
	second := writeSource(t, directory, "two.jpg")
	writeSource(t, directory, "ignored.bmp")
	nested := filepath.Join(directory, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSource(t, nested, "nested.png")

	inventory := NewInventory()
	summary := inventory.Add([]string{directory})
	if summary.Added != 2 {
		t.Fatalf("folder summary = %+v, want 2 added", summary)
	}
	items := inventory.Items()
	if len(items) != 2 || items[0].Path != first || items[1].Path != second {
		t.Fatalf("folder items = %+v, want %q and %q", items, first, second)
	}
	for _, item := range items {
		if strings.Contains(item.Path, "nested") || item.Status != StatusReady {
			t.Fatalf("unexpected folder item: %+v", item)
		}
	}
}

func TestInventoryAddReportsUnreadableFolder(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	inventory := NewInventory()
	inventory.readDir = func(string) ([]os.DirEntry, error) {
		return nil, os.ErrPermission
	}
	summary := inventory.Add([]string{filepath.Join(directory, "broken")})
	items := inventory.Items()
	if summary.Inaccessible != 1 || len(items) != 1 {
		t.Fatalf("summary = %+v, items = %+v, want 1 inaccessible", summary, items)
	}
	if items[0].Status != StatusInaccessible || !strings.Contains(items[0].Detail, "permission") {
		t.Fatalf("unreadable folder item = %+v", items[0])
	}
}

func TestInventoryRemoveAndClear(t *testing.T) {
	directory := t.TempDir()
	first := writeSource(t, directory, "one.png")
	second := writeSource(t, directory, "two.png")
	third := writeSource(t, directory, "three.png")

	inventory := NewInventory()
	inventory.Add([]string{first, second, third})
	inventory.Remove([]int{0, 2})
	items := inventory.Items()
	if len(items) != 1 || items[0].Path != second {
		t.Fatalf("items after removal = %+v, want only %q", items, second)
	}

	inventory.Clear()
	if len(inventory.Items()) != 0 {
		t.Fatalf("items after clear = %d, want 0", len(inventory.Items()))
	}
	summary := inventory.Add([]string{first})
	if summary.Added != 1 {
		t.Fatalf("add after clear = %+v, want 1 added", summary)
	}
}

// TestInventoryAddNeverModifiesSources pins RSZ-009: inventory validation
// opens originals read-only, so content and modification time survive.
func TestInventoryAddNeverModifiesSources(t *testing.T) {
	directory := t.TempDir()
	path := writeSource(t, directory, "untouched.png")
	old := time.Now().Add(-time.Hour).Round(0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	inventory := NewInventory()
	if summary := inventory.Add([]string{path}); summary.Added != 1 {
		t.Fatalf("summary = %+v, want 1 added", summary)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "not-yet-validated image content" {
		t.Fatalf("source content changed: %q", content)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(old) {
		t.Fatalf("source modification time changed: %v -> %v", old, after.ModTime())
	}
}

func TestInventoryItemsReturnsACopy(t *testing.T) {
	directory := t.TempDir()
	inventory := NewInventory()
	inventory.Add([]string{writeSource(t, directory, "one.png")})
	items := inventory.Items()
	items[0].Status = StatusInaccessible
	if inventory.Items()[0].Status != StatusReady {
		t.Fatal("mutating the returned slice changed the inventory")
	}
}

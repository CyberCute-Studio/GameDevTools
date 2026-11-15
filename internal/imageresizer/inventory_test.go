package imageresizer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
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
	// Supported files wait for their off-GUI-thread content check, which
	// the summary queues in list order.
	if items[0].Status != StatusChecking || items[1].Status != StatusChecking {
		t.Fatalf("supported files must be Checking: %+v, %+v", items[0], items[1])
	}
	if len(summary.Inspect) != 2 || summary.Inspect[0] != (Inspection{ID: items[0].ID, Path: ready}) || summary.Inspect[1] != (Inspection{ID: items[1].ID, Path: upper}) {
		t.Fatalf("queued checks = %+v", summary.Inspect)
	}
	if inventory.Checking() != 2 {
		t.Fatalf("Checking() = %d, want 2", inventory.Checking())
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
		if strings.Contains(item.Path, "nested") || item.Status != StatusChecking {
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
	added := inventory.Add([]string{first, second, third})
	removed := inventory.Remove([]int{0, 2})
	if len(removed) != 2 || removed[0] != added.Inspect[0].ID || removed[1] != added.Inspect[2].ID {
		t.Fatalf("removed IDs = %v, want the first and third items' IDs", removed)
	}
	items := inventory.Items()
	if len(items) != 1 || items[0].Path != second {
		t.Fatalf("items after removal = %+v, want only %q", items, second)
	}
	if inventory.Checking() != 1 {
		t.Fatalf("Checking() after removal = %d, want 1", inventory.Checking())
	}

	inventory.Clear()
	if len(inventory.Items()) != 0 || inventory.Checking() != 0 {
		t.Fatalf("items after clear = %d (checking %d), want 0", len(inventory.Items()), inventory.Checking())
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
	if inventory.Items()[0].Status != StatusChecking {
		t.Fatal("mutating the returned slice changed the inventory")
	}
}

func TestInventoryResolveAppliesCheckResults(t *testing.T) {
	directory := t.TempDir()
	inventory := NewInventory()
	summary := inventory.Add([]string{writeSource(t, directory, "one.png"), writeSource(t, directory, "two.jpg")})
	first, second := summary.Inspect[0], summary.Inspect[1]

	info := decode.Info{Format: decode.FormatPNG, Width: 640, Height: 480, Orientation: decode.OrientationNormal, HasAlpha: true}
	index, ok := inventory.Resolve(InspectionResult{ID: first.ID, Info: info})
	if !ok || index != 0 {
		t.Fatalf("Resolve(first) = %d, %v", index, ok)
	}
	if item := inventory.Item(0); item.Status != StatusReady || item.Info != info || item.Detail != "" {
		t.Fatalf("resolved item = %+v", item)
	}
	if inventory.Checking() != 1 {
		t.Fatalf("Checking() = %d, want 1", inventory.Checking())
	}
	// A result is applied once; a repeat changes nothing.
	if _, ok := inventory.Resolve(InspectionResult{ID: first.ID, Err: errors.New("late")}); ok {
		t.Fatal("a second result for a resolved item was applied")
	}
	if inventory.Item(0).Status != StatusReady {
		t.Fatal("a repeated result changed the resolved item")
	}

	failure := &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatJPEG, Err: errors.New("unexpected end of data")}
	if index, ok := inventory.Resolve(InspectionResult{ID: second.ID, Err: failure}); !ok || index != 1 {
		t.Fatalf("Resolve(second) = %d, %v", index, ok)
	}
	if item := inventory.Item(1); item.Status != StatusCorrupt || !strings.Contains(item.Detail, "JPEG data is damaged") || !strings.Contains(item.Detail, "unexpected end of data") {
		t.Fatalf("failed item = %+v", item)
	}
	if inventory.Checking() != 0 {
		t.Fatalf("Checking() = %d, want 0", inventory.Checking())
	}
}

// TestInventoryResolveFindsItemsAfterRemoval pins the ID lookup across a
// list with gaps: removal keeps the items in ID order, so every remaining
// item is still found at its new index and removed IDs are not.
func TestInventoryResolveFindsItemsAfterRemoval(t *testing.T) {
	directory := t.TempDir()
	paths := make([]string, 0, 9)
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png", "e.png", "f.png", "g.png", "h.png", "i.png"} {
		paths = append(paths, writeSource(t, directory, name))
	}
	inventory := NewInventory()
	checks := inventory.Add(paths).Inspect
	inventory.Remove([]int{0, 3, 4, 8})
	// The kept items b, c, f, g, h now sit at indexes 0 through 4.
	for want, original := range []int{1, 2, 5, 6, 7} {
		if index, ok := inventory.Resolve(InspectionResult{ID: checks[original].ID, Info: decode.Info{Format: decode.FormatPNG, Width: 1, Height: 1}}); !ok || index != want {
			t.Fatalf("Resolve(%s) = %d, %v; want index %d", filepath.Base(paths[original]), index, ok, want)
		}
	}
	for _, original := range []int{0, 3, 4, 8} {
		if index, ok := inventory.Resolve(InspectionResult{ID: checks[original].ID}); ok || index != -1 {
			t.Fatalf("Resolve(removed %s) = %d, %v", filepath.Base(paths[original]), index, ok)
		}
	}
	if inventory.Checking() != 0 {
		t.Fatalf("Checking() = %d, want 0", inventory.Checking())
	}
}

// TestInventoryIgnoresStaleResults pins that a check finishing after its
// item was removed cannot update anything — including a re-added item for
// the same path, which gets a new identifier.
func TestInventoryIgnoresStaleResults(t *testing.T) {
	directory := t.TempDir()
	path := writeSource(t, directory, "photo.png")
	inventory := NewInventory()
	stale := inventory.Add([]string{path}).Inspect[0]
	inventory.Remove([]int{0})
	fresh := inventory.Add([]string{path}).Inspect[0]
	if fresh.ID == stale.ID {
		t.Fatalf("re-added item reused identifier %d", fresh.ID)
	}
	if _, ok := inventory.Resolve(InspectionResult{ID: stale.ID, Info: decode.Info{Format: decode.FormatPNG, Width: 1, Height: 1}}); ok {
		t.Fatal("a stale result was applied")
	}
	if item := inventory.Item(0); item.Status != StatusChecking || inventory.Checking() != 1 {
		t.Fatalf("re-added item after a stale result = %+v (checking %d)", item, inventory.Checking())
	}
	inventory.Clear()
	if _, ok := inventory.Resolve(InspectionResult{ID: fresh.ID}); ok {
		t.Fatal("a result after Clear was applied")
	}
}

// TestFailureStatusIdentifiesEachKind pins RSZ-004: damaged, unsupported,
// unreadable, and oversized content each get a distinct status and an
// actionable explanation.
func TestFailureStatusIdentifiesEachKind(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status SourceStatus
		detail string
	}{
		{"read", &decode.Error{Kind: decode.KindRead, Err: os.ErrPermission}, StatusInaccessible, "permission denied"},
		{"unrecognized", &decode.Error{Kind: decode.KindUnsupported, Err: decode.ErrUnrecognized}, StatusUnsupported, "not PNG or JPEG image data"},
		{"feature", &decode.Error{Kind: decode.KindUnsupported, Format: decode.FormatJPEG, Err: errors.New("unsupported JPEG feature: precision")}, StatusUnsupported, "This JPEG file uses a feature the image decoder does not support (unsupported JPEG feature: precision)"},
		{"corrupt", &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}, StatusCorrupt, "The PNG data is damaged or incomplete and cannot be decoded (invalid checksum)"},
		{"empty", &decode.Error{Kind: decode.KindCorrupt, Err: decode.ErrEmpty}, StatusCorrupt, "The file is empty."},
		{"too large", &decode.Error{Kind: decode.KindTooLarge, Format: decode.FormatPNG, Width: 70000, Height: 70000, Err: errors.New("limit")}, StatusTooLarge, "At 70000 × 70000 px this image needs more than the 1024 MiB"},
		{"canceled", &decode.Error{Kind: decode.KindCanceled, Err: context.Canceled}, StatusInaccessible, "context canceled"},
		{"untyped", errors.New("surprise"), StatusCorrupt, "surprise"},
		// The zero Kind is a read failure; without a cause it is still
		// explained rather than dereferenced on the GUI thread.
		{"read without a cause", &decode.Error{Format: decode.FormatPNG}, StatusInaccessible, "The file could not be read."},
	}
	for _, tc := range cases {
		status, detail := failureStatus(tc.err)
		if status != tc.status || !strings.Contains(detail, tc.detail) {
			t.Fatalf("%s: failureStatus = %v, %q; want %v containing %q", tc.name, status, detail, tc.status, tc.detail)
		}
	}
}

func TestSourceItemRowAndDetailText(t *testing.T) {
	directory := t.TempDir()
	cases := []struct {
		name   string
		item   SourceItem
		row    string
		detail string
	}{
		{
			name:   "checking",
			item:   SourceItem{Name: "a.png", Path: filepath.Join(directory, "a.png"), Status: StatusChecking},
			row:    "a.png — Checking…",
			detail: "The image content is being checked.",
		},
		{
			name:   "ready PNG with alpha",
			item:   SourceItem{Name: "a.png", Path: filepath.Join(directory, "a.png"), Status: StatusReady, Info: decode.Info{Format: decode.FormatPNG, Width: 512, Height: 256, Orientation: decode.OrientationNormal, HasAlpha: true}},
			row:    "a.png — 512 × 256 px — Ready",
			detail: "PNG image, 512 × 256 px, with an alpha channel.",
		},
		{
			name:   "oriented JPEG",
			item:   SourceItem{Name: "b.JPG", Path: filepath.Join(directory, "b.JPG"), Status: StatusReady, Info: decode.Info{Format: decode.FormatJPEG, Width: 3000, Height: 4000, Orientation: decode.OrientationRotate90}},
			row:    "b.JPG — 3000 × 4000 px — Ready",
			detail: "JPEG image, 3000 × 4000 px, upright after applying its EXIF orientation (stored as 4000 × 3000 px).",
		},
		{
			name:   "content mismatch",
			item:   SourceItem{Name: "c.png", Path: filepath.Join(directory, "c.png"), Status: StatusReady, Info: decode.Info{Format: decode.FormatJPEG, Width: 10, Height: 20, Orientation: decode.OrientationNormal}},
			row:    "c.png — 10 × 20 px — Ready",
			detail: "JPEG image, 10 × 20 px. The content is JPEG data despite the .png extension, so it is processed as JPEG.",
		},
		{
			// Only a standard orientation other than Normal was applied; the
			// zero value means none was recorded.
			name:   "no recorded orientation",
			item:   SourceItem{Name: "e.jpg", Path: filepath.Join(directory, "e.jpg"), Status: StatusReady, Info: decode.Info{Format: decode.FormatJPEG, Width: 8, Height: 6}},
			row:    "e.jpg — 8 × 6 px — Ready",
			detail: "JPEG image, 8 × 6 px.",
		},
		{
			name:   "failed",
			item:   SourceItem{Name: "d.png", Path: filepath.Join(directory, "d.png"), Status: StatusCorrupt, Detail: "The file is empty."},
			row:    "d.png — Damaged or incomplete",
			detail: "Damaged or incomplete: The file is empty.",
		},
	}
	for _, tc := range cases {
		if got := tc.item.RowText(); got != tc.row {
			t.Fatalf("%s: RowText = %q, want %q", tc.name, got, tc.row)
		}
		if got := tc.item.DetailText(); got != tc.detail {
			t.Fatalf("%s: DetailText = %q, want %q", tc.name, got, tc.detail)
		}
	}
}

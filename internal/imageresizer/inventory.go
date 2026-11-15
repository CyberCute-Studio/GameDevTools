package imageresizer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// SourceStatus is the per-item validation state shown in the source list
// (RSZ-004, RSZ-005). Full content decoding arrives with RSZ-03; this initial
// validation covers the extension and read-only accessibility.
type SourceStatus uint8

const (
	StatusReady SourceStatus = iota
	StatusUnsupported
	StatusInaccessible
)

func (s SourceStatus) Label() string {
	switch s {
	case StatusUnsupported:
		return ui.SourceStatusUnsupported
	case StatusInaccessible:
		return ui.SourceStatusInaccessible
	default:
		return ui.SourceStatusReady
	}
}

// SourceItem is one inventory entry. Name is the display name; Path is the
// absolute source path; Detail carries the status explanation (for example
// the read error) and stays empty for Ready items.
type SourceItem struct {
	Name   string
	Path   string
	Status SourceStatus
	Detail string
}

// AddSummary reports the outcome of one Add call so the view can present the
// batch result without re-reading the inventory.
type AddSummary struct {
	Added        int
	Duplicates   int
	Unsupported  int
	Inaccessible int
}

// Inventory is the ordered, deduplicated list of resize sources. It is not
// safe for concurrent use; the view drives it from the GUI thread.
type Inventory struct {
	items   []SourceItem
	byKey   map[string]struct{}
	readDir func(string) ([]os.DirEntry, error)
}

func NewInventory() *Inventory {
	return &Inventory{byKey: make(map[string]struct{}), readDir: os.ReadDir}
}

func (v *Inventory) Items() []SourceItem {
	return append([]SourceItem(nil), v.items...)
}

// SupportedExtension reports whether the path carries one of the supported
// input extensions, case-insensitively (RSZ-003).
func SupportedExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// Add validates and appends the given paths. A directory adds the supported
// files directly inside it without recursing (RSZ-008). Explicitly chosen
// files with an unsupported extension or a failed read still enter the list
// with their status, so invalid items are identified without blocking the
// valid ones (RSZ-004); the same full path is never added twice (RSZ-007).
func (v *Inventory) Add(paths []string) AddSummary {
	var summary AddSummary
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			// A path that cannot be queried — typically it vanished between
			// selection and arrival — is listed as inaccessible.
			v.addInaccessible(path, err, &summary)
			continue
		}
		if info.IsDir() {
			summary.merge(v.addFolder(path))
			continue
		}
		v.addFile(path, &summary)
	}
	return summary
}

// Remove deletes the items at the given ascending indexes.
func (v *Inventory) Remove(indices []int) {
	removing := make(map[int]struct{}, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(v.items) {
			removing[index] = struct{}{}
		}
	}
	if len(removing) == 0 {
		return
	}
	kept := v.items[:0]
	for index, item := range v.items {
		if _, drop := removing[index]; drop {
			delete(v.byKey, pathKey(item.Path))
			continue
		}
		kept = append(kept, item)
	}
	// The retained tail aliases the dropped prefix, so clear it to keep
	// removed paths from lingering in the backing array.
	for index := len(kept); index < len(v.items); index++ {
		v.items[index] = SourceItem{}
	}
	v.items = kept
}

func (v *Inventory) Clear() {
	v.items = nil
	v.byKey = make(map[string]struct{})
}

// addFolder adds the supported files directly inside the dropped folder
// (RSZ-008). Folder contents are filtered by extension rather than listed as
// Unsupported, so dropping a mixed folder does not flood the list with files
// the user never picked; subfolders are not recursed. An unreadable folder
// contributes one Inaccessible item for the folder path itself.
func (v *Inventory) addFolder(folder string) AddSummary {
	var summary AddSummary
	entries, err := v.readDir(folder)
	if err != nil {
		v.addInaccessible(folder, err, &summary)
		return summary
	}
	for _, entry := range entries {
		if entry.IsDir() || !SupportedExtension(entry.Name()) {
			continue
		}
		v.addFile(filepath.Join(folder, entry.Name()), &summary)
	}
	return summary
}

// addInaccessible lists a path that cannot be enumerated or queried with its
// error, so the failure is identified without blocking valid entries.
func (v *Inventory) addInaccessible(path string, cause error, summary *AddSummary) {
	key := pathKey(path)
	if _, exists := v.byKey[key]; exists {
		summary.Duplicates++
		return
	}
	v.byKey[key] = struct{}{}
	v.items = append(v.items, SourceItem{
		Name:   filepath.Base(path),
		Path:   path,
		Status: StatusInaccessible,
		Detail: cause.Error(),
	})
	summary.Inaccessible++
}

func (v *Inventory) addFile(path string, summary *AddSummary) {
	key := pathKey(path)
	if _, exists := v.byKey[key]; exists {
		summary.Duplicates++
		return
	}
	item := SourceItem{Name: filepath.Base(path), Path: path, Status: StatusReady}
	if !SupportedExtension(path) {
		item.Status = StatusUnsupported
		item.Detail = ui.SourceUnsupportedDetail
		summary.Unsupported++
	} else if err := checkReadable(path); err != nil {
		item.Status = StatusInaccessible
		item.Detail = err.Error()
		summary.Inaccessible++
	} else {
		summary.Added++
	}
	v.byKey[key] = struct{}{}
	v.items = append(v.items, item)
}

func (s *AddSummary) merge(other AddSummary) {
	s.Added += other.Added
	s.Duplicates += other.Duplicates
	s.Unsupported += other.Unsupported
	s.Inaccessible += other.Inaccessible
}

// checkReadable proves the file can be opened read-only (RSZ-009): sources
// are never opened for writing, here or during the job.
func checkReadable(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

// pathKey normalizes a full path for deduplication. Windows paths compare
// case-insensitively, so "photo.PNG" and "PHOTO.png" name the same source.
func pathKey(path string) string {
	return strings.ToUpper(filepath.Clean(path))
}

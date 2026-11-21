package imageresizer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/pathform"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// SourceStatus is the per-item validation state shown in the source list
// (RSZ-004, RSZ-005). Adding a file checks its extension and that it exists;
// a supported file then stays Checking until its content has been decoded
// off the GUI thread (RSZ-003) and its result is resolved into the item.
// The zero value is Checking, so an item is never Ready unless a decode
// resolved it so.
type SourceStatus uint8

const (
	StatusChecking SourceStatus = iota
	StatusReady
	StatusUnsupported
	StatusInaccessible
	StatusCorrupt
	StatusTooLarge
)

// Label is the status shown in the source list. A value outside the defined
// statuses reads as Checking rather than claiming Ready.
func (s SourceStatus) Label() string {
	switch s {
	case StatusReady:
		return ui.SourceStatusReady
	case StatusUnsupported:
		return ui.SourceStatusUnsupported
	case StatusInaccessible:
		return ui.SourceStatusInaccessible
	case StatusCorrupt:
		return ui.SourceStatusCorrupt
	case StatusTooLarge:
		return ui.SourceStatusTooLarge
	default:
		return ui.SourceStatusChecking
	}
}

// SourceItem is one inventory entry. ID is unique within the inventory and
// never reused, so a content check that finishes after its item was removed
// cannot update a later item for the same path. Name is the display name;
// Path is the absolute source path; Detail carries the status explanation
// (for example the read error) and stays empty for Ready and Checking items.
// Info describes the decoded content once the item is Ready. Inspected
// reports that the item was queued for a content check when it was added, as
// every supported file that exists is, so its Status is that check's outcome
// once it resolves rather than a rejection made up front.
type SourceItem struct {
	ID        uint64
	Name      string
	Path      string
	Status    SourceStatus
	Detail    string
	Info      decode.Info
	Inspected bool
	// Result is what the last resize job reported for the item while InJob
	// is set: the item was one of that job's sources. Its row and detail then
	// show the job's state and result (RSZ-043, RSZ-046) instead of the
	// content check, which Status keeps for the next job.
	Result job.Status
	InJob  bool
	// key is the path key the item claimed in the inventory, which Remove
	// releases.
	key string
}

// RowText is the source-list row: the file name, the upright source
// dimensions once they are known, and the status (RSZ-005) — the job's state
// for the item once a job included it (RSZ-043), in words, never by color
// alone (UX-005).
func (s SourceItem) RowText() string {
	status := s.Status.Label()
	if s.InJob {
		status = stateLabel(s.Result.State)
	}
	if s.Status == StatusReady {
		return fmt.Sprintf(ui.SourceListReadyFormat, s.Name, ui.FormatDimensions(s.Info.Width, s.Info.Height), status)
	}
	return fmt.Sprintf(ui.SourceListFormat, s.Name, status)
}

// DetailText explains the item's status below the full path: the decoded
// format, dimensions, alpha, and applied orientation for a Ready item, and
// the reason for every other status; once a job included the item, its
// result instead, as resultDetail describes (RSZ-046).
func (s SourceItem) DetailText() string {
	if s.InJob {
		return resultDetail(s.Result)
	}
	switch s.Status {
	case StatusReady:
		return readyDetail(s)
	case StatusChecking:
		return ui.SourceCheckingDetail
	}
	return s.Status.Label() + ui.LabelSeparator + s.Detail
}

func readyDetail(item SourceItem) string {
	info := item.Info
	var detail strings.Builder
	fmt.Fprintf(&detail, ui.SourceReadyDetail, formatLabel(info.Format), ui.FormatDimensions(info.Width, info.Height))
	if info.HasAlpha {
		detail.WriteString(ui.SourceReadyAlpha)
	}
	if info.Orientation.Reorients() {
		width, height := info.StoredSize()
		fmt.Fprintf(&detail, ui.SourceReadyOriented, ui.FormatDimensions(width, height))
	}
	detail.WriteString(".")
	if extension := strings.ToLower(filepath.Ext(item.Path)); extensionFormat(extension) != info.Format {
		fmt.Fprintf(&detail, ui.SourceContentMismatch, formatLabel(info.Format), extension, formatLabel(info.Format))
	}
	return detail.String()
}

// extensionFormat is the format a supported extension names.
func extensionFormat(extension string) decode.Format {
	switch extension {
	case ".png":
		return decode.FormatPNG
	case ".jpg", ".jpeg":
		return decode.FormatJPEG
	}
	return decode.FormatUnknown
}

func formatLabel(format decode.Format) string {
	switch format {
	case decode.FormatPNG:
		return ui.ImageFormatPNG
	case decode.FormatJPEG:
		return ui.ImageFormatJPEG
	}
	return ui.SourceFormatUnknown
}

// AddSummary reports the outcome of one Add call so the view can present the
// batch result without re-reading the inventory. Added counts the supported
// files now being checked; Inspect lists their content checks, in order.
// DuplicateOf holds, for each of the Duplicates, the ID of the item that
// already lists its path.
type AddSummary struct {
	Added        int
	Duplicates   int
	Unsupported  int
	Inaccessible int
	Inspect      []Inspection
	DuplicateOf  []uint64
}

// Inventory is the ordered, deduplicated list of resize sources. It is not
// safe for concurrent use; the view drives it from the GUI thread and
// resolves content checks there after the shell dispatcher delivers them.
type Inventory struct {
	items []SourceItem
	// byKey maps each listed path's key to its item's ID.
	byKey    map[string]uint64
	nextID   uint64
	checking int
	readDir  func(string) ([]os.DirEntry, error)
}

func NewInventory() *Inventory {
	return &Inventory{byKey: make(map[string]uint64), readDir: os.ReadDir}
}

func (v *Inventory) Items() []SourceItem {
	return append([]SourceItem(nil), v.items...)
}

// Item returns the item at index, which must be in range.
func (v *Inventory) Item(index int) SourceItem {
	return v.items[index]
}

// Len reports how many items are listed.
func (v *Inventory) Len() int {
	return len(v.items)
}

// Checking reports how many items still await their content check.
func (v *Inventory) Checking() int {
	return v.checking
}

// SupportedExtension reports whether the path carries one of the supported
// input extensions, case-insensitively (RSZ-003).
func SupportedExtension(path string) bool {
	return extensionFormat(strings.ToLower(filepath.Ext(path))) != decode.FormatUnknown
}

// Add validates and appends the given paths. A directory adds the supported
// files directly inside it without recursing (RSZ-008). Explicitly chosen
// files with an unsupported extension or that cannot be found still enter
// the list with their status, so invalid items are identified without
// blocking the valid ones (RSZ-004); the same full path is never added twice
// (RSZ-007). Supported files enter as Checking, and the returned summary
// lists the content checks the caller runs off the GUI thread.
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
			v.addFolder(path, &summary)
			continue
		}
		v.addFile(path, &summary)
	}
	return summary
}

// IndexOf returns the index of the item with the given ID, or ok == false
// when no listed item has it.
func (v *Inventory) IndexOf(id uint64) (index int, ok bool) {
	// Identifiers are allocated in increasing order as items are appended,
	// and removal keeps the order, so the items stay sorted by ID.
	index = sort.Search(len(v.items), func(candidate int) bool {
		return v.items[candidate].ID >= id
	})
	if index == len(v.items) || v.items[index].ID != id {
		return -1, false
	}
	return index, true
}

// Resolve applies a finished content check to the item it belongs to and
// returns that item's index. A result whose item was removed, or that was
// already resolved, changes nothing and reports ok == false.
func (v *Inventory) Resolve(result InspectionResult) (index int, ok bool) {
	index, ok = v.IndexOf(result.ID)
	if !ok {
		return -1, false
	}
	item := &v.items[index]
	if item.Status != StatusChecking {
		return index, false
	}
	v.checking--
	item.resolve(result)
	return index, true
}

// resolve gives the item its content check's outcome: Ready with the
// decoded description, or the failure's status and explanation.
func (s *SourceItem) resolve(result InspectionResult) {
	if result.Err != nil {
		s.Status, s.Detail = failureStatus(result.Err)
		s.Info = decode.Info{}
		return
	}
	s.Status, s.Detail, s.Info = StatusReady, "", result.Info
}

// failureStatus maps a typed decode failure to the list status and its
// explanation, so damaged, unsupported, unreadable, and oversized content
// are each identified distinctly (RSZ-004). The Inspector never delivers a
// cancelled check, so KindCanceled only shares the read-failure mapping to
// keep every kind accounted for. A read failure is explained by the file
// system's own message; an error without a cause — which only a decode
// function other than decode.Inspect could build — still gets an
// explanation rather than failing on the GUI thread.
func failureStatus(err error) (SourceStatus, string) {
	var decodeErr *decode.Error
	if !errors.As(err, &decodeErr) {
		return StatusCorrupt, fmt.Sprintf(ui.SourceCorruptDetail, ui.SourceFormatUnknown, err)
	}
	switch decodeErr.Kind {
	case decode.KindRead, decode.KindCanceled:
		if decodeErr.Err == nil {
			return StatusInaccessible, ui.SourceReadFailedDetail
		}
		return StatusInaccessible, decodeErr.Err.Error()
	case decode.KindUnsupported:
		if decodeErr.Format == decode.FormatUnknown {
			return StatusUnsupported, ui.SourceContentUnrecognizedDetail
		}
		return StatusUnsupported, fmt.Sprintf(ui.SourceFeatureUnsupportedDetail, formatLabel(decodeErr.Format), decodeErr.Err)
	case decode.KindTooLarge:
		return StatusTooLarge, fmt.Sprintf(ui.SourceTooLargeDetail, ui.FormatDimensions(decodeErr.Width, decodeErr.Height), decode.MaxDecodedBytes>>20)
	}
	if errors.Is(decodeErr, decode.ErrEmpty) {
		return StatusCorrupt, ui.SourceEmptyDetail
	}
	return StatusCorrupt, fmt.Sprintf(ui.SourceCorruptDetail, formatLabel(decodeErr.Format), decodeErr.Err)
}

// Remove deletes the items at the given ascending indexes and returns their
// identifiers so the caller can abandon their pending content checks.
func (v *Inventory) Remove(indices []int) []uint64 {
	removing := make(map[int]struct{}, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(v.items) {
			removing[index] = struct{}{}
		}
	}
	if len(removing) == 0 {
		return nil
	}
	removed := make([]uint64, 0, len(removing))
	kept := v.items[:0]
	for index, item := range v.items {
		if _, drop := removing[index]; drop {
			delete(v.byKey, item.key)
			removed = append(removed, item.ID)
			if item.Status == StatusChecking {
				v.checking--
			}
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
	return removed
}

func (v *Inventory) Clear() {
	v.items = nil
	v.byKey = make(map[string]uint64)
	v.checking = 0
}

// addFolder adds the supported files directly inside the dropped folder
// (RSZ-008). Folder contents are filtered by extension rather than listed as
// Unsupported, so dropping a mixed folder does not flood the list with files
// the user never picked; subfolders are not recursed. An unreadable folder
// contributes one Inaccessible item for the folder path itself.
func (v *Inventory) addFolder(folder string, summary *AddSummary) {
	entries, err := v.readDir(folder)
	if err != nil {
		v.addInaccessible(folder, err, summary)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !SupportedExtension(entry.Name()) {
			continue
		}
		v.addFile(pathform.Join(folder, entry.Name()), summary)
	}
}

// addInaccessible lists a path that cannot be enumerated or queried with its
// error, so the failure is identified without blocking valid entries.
func (v *Inventory) addInaccessible(path string, cause error, summary *AddSummary) {
	id, key, claimed := v.claim(path, summary)
	if !claimed {
		return
	}
	v.items = append(v.items, SourceItem{
		ID:     id,
		Name:   filepath.Base(path),
		Path:   path,
		Status: StatusInaccessible,
		Detail: cause.Error(),
		key:    key,
	})
	summary.Inaccessible++
}

// addFile lists one file. A supported extension enters as Checking and
// queues its content check; the read-only open happens there, off the GUI
// thread (RSZ-009, TECH-005).
func (v *Inventory) addFile(path string, summary *AddSummary) {
	id, key, claimed := v.claim(path, summary)
	if !claimed {
		return
	}
	item := SourceItem{ID: id, Name: filepath.Base(path), Path: path, Status: StatusChecking, key: key}
	if !SupportedExtension(path) {
		item.Status = StatusUnsupported
		item.Detail = ui.SourceUnsupportedDetail
		summary.Unsupported++
	} else {
		item.Inspected = true
		v.checking++
		summary.Added++
		summary.Inspect = append(summary.Inspect, Inspection{ID: item.ID, Path: path})
	}
	v.items = append(v.items, item)
}

// claim allocates the ID of the item that will list the path and returns
// the path key it claimed, or counts the path as a duplicate of the item
// already listing the same full path.
func (v *Inventory) claim(path string, summary *AddSummary) (uint64, string, bool) {
	key := output.PathKey(path)
	if existing, exists := v.byKey[key]; exists {
		summary.Duplicates++
		summary.DuplicateOf = append(summary.DuplicateOf, existing)
		return 0, "", false
	}
	id := v.allocateID()
	v.byKey[key] = id
	return id, key, true
}

// allocateID returns the next item identifier. Identifiers start at 1 so the
// zero value never names an item.
func (v *Inventory) allocateID() uint64 {
	v.nextID++
	return v.nextID
}

// Package output plans where an Image Resizer job writes its results.
//
// Prepare maps every source of a job to one destination in the job's
// output folder before anything is written. A destination keeps the
// source's base file name with the extension of the encoded format
// (RSZ-023): Keep Source Format encodes each source as its detected content
// format, so a JPEG saved with a .png extension becomes a .jpg output
// (RSZ-022).
//
// Collisions are resolved by the job's policy (RSZ-024, RSZ-025): Rename
// appends _2, _3, and so on until it finds an unused name, Skip leaves the
// existing destination untouched and marks the item skipped, and Overwrite
// replaces an existing destination file only when the request carries the
// user's explicit confirmation for the job. Two sources of the same job that
// would produce the same destination never replace one another: the first in
// request order keeps the name, and a later one is renamed, or skipped under
// Skip.
//
// No plan ever writes over a source (RSZ-026, NFR-020). Destinations are
// compared with every source of the job by path, case-insensitively, and an
// existing destination file is also compared by file identity, so a hard
// link, a junction, or another alias of a source is recognized as that
// source. A destination that is a source is never replaced, even under
// Overwrite: Rename and Overwrite rename the result instead, and Skip skips
// it. Overwrite replaces an existing file only when it can prove the file is
// none of the job's sources; when the identity of the file or of a source
// cannot be read, the result is renamed rather than risk a source.
//
// Prepare validates the request, creates the output folder when it does not
// exist (RSZ-030), and inspects the destinations as the filesystem stands
// when it runs, so re-running a job plans afresh rather than assuming
// earlier results (RSZ-049). A file can still appear at a planned
// destination after Prepare returns; the writer must finalize ActionCreate
// items without replacing anything.
//
// Failures are reported as *Error values with a Kind. A failure that
// concerns the whole job — an invalid request, a missing confirmation, or an
// unusable output folder — is returned by Prepare; a failure that concerns
// one destination is attached to that item, and the other items are planned
// regardless (RSZ-044).
//
// Prepare touches the filesystem and never the GUI, so callers run it off
// the GUI thread and deliver results through the shell dispatcher, per
// TECH-005. It is safe to call from any goroutine.
package output

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

// maxSuffix bounds the Rename search, so a folder crowded with earlier
// results, or a filesystem that keeps reporting names as taken, cannot keep
// the search running forever. The Screenshot output uses the same bound.
const maxSuffix = 9999

// maxNameLength is the longest file name NTFS, ReFS, exFAT, and FAT32 long
// names accept, in UTF-16 code units.
const maxNameLength = 255

// Kind classifies a planning failure so callers can present the right
// recovery path per UX-010.
type Kind uint8

const (
	// KindValidation means the request itself is invalid: resize options
	// that fail validation, no sources, or a source that is not an absolute
	// file path with a decoded PNG or JPEG format.
	KindValidation Kind = iota
	// KindConfirmation means the Overwrite policy was selected without the
	// job's explicit confirmation.
	KindConfirmation
	// KindFolder means the output folder could not be created or used.
	KindFolder
	// KindDestination means one item's destination could not be checked or
	// named. It is attached to that item rather than returned by Prepare.
	KindDestination
	// KindCanceled means the context ended before planning finished.
	KindCanceled
)

func (k Kind) String() string {
	switch k {
	case KindValidation:
		return "validation"
	case KindConfirmation:
		return "confirmation"
	case KindFolder:
		return "folder"
	case KindDestination:
		return "destination"
	case KindCanceled:
		return "canceled"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error describes a failed planning step.
type Error struct {
	Kind Kind
	Op   string
	Err  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func kindError(kind Kind, op string, err error) *Error {
	return &Error{Kind: kind, Op: op, Err: err}
}

var (
	// ErrOverwriteUnconfirmed is the cause of the KindConfirmation error
	// returned for an Overwrite job the user has not confirmed.
	ErrOverwriteUnconfirmed = errors.New("the Overwrite policy replaces existing files only after the job is confirmed")
	// ErrNoSources is the cause of the KindValidation error returned for a
	// job without sources.
	ErrNoSources = errors.New("the job has no sources to resize")
	// ErrNotFolder is the cause of the KindFolder error returned when the
	// output folder path names something other than a folder.
	ErrNotFolder = errors.New("the output path is not a folder")
	// ErrNameTooLong is the cause of the KindDestination error attached to
	// an item whose output file name would exceed what Windows file systems
	// accept.
	ErrNameTooLong = fmt.Errorf("the output file name would be longer than %d characters", maxNameLength)
	// ErrNoUnusedName is the cause of the KindDestination error attached to
	// an item for which the Rename search found no unused name.
	ErrNoUnusedName = fmt.Errorf("no unused output file name was found after %d attempts", maxSuffix-1)
)

// Source is one processable source of a job: a file whose content was
// decoded successfully.
type Source struct {
	// Path is the absolute source path, as listed in the source inventory.
	Path string
	// Format is the detected content format, from the source's decode.Info.
	// Keep Source Format encodes the result in this format.
	Format decode.Format
}

// Request describes one job to plan.
type Request struct {
	// Sources lists the job's processable sources in the order the job
	// processes them. That order decides which of two sources with the
	// same destination keeps the name.
	Sources []Source
	// Options is the job's output configuration: folder, format, and
	// collision policy.
	Options imageresizer.ResizeOptions
	// ConfirmOverwrite records the user's explicit confirmation, given for
	// this job, that existing destination files may be replaced. Prepare
	// refuses an Overwrite job without it; other policies ignore it.
	ConfirmOverwrite bool
}

// Action is what the job does with one item's destination.
type Action uint8

const (
	// ActionCreate writes a new file at Destination, where nothing existed
	// when the plan was made. The writer must not replace a file that
	// appears there afterwards.
	ActionCreate Action = iota
	// ActionReplace replaces the existing file at Destination, which the
	// plan proved is not a source. Only a confirmed Overwrite job replaces.
	ActionReplace
	// ActionSkip leaves Destination untouched and marks the item Skipped.
	// Only a Skip job skips.
	ActionSkip
	// ActionFail marks the item Failed with Err; Destination is the name
	// that could not be used.
	ActionFail
)

func (a Action) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionReplace:
		return "replace"
	case ActionSkip:
		return "skip"
	case ActionFail:
		return "fail"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// Reason explains why an item did not get its natural destination: why it
// was renamed (ActionCreate) or skipped (ActionSkip).
type Reason uint8

const (
	// ReasonNone means the item writes its natural destination: the source
	// base name with the output format's extension.
	ReasonNone Reason = iota
	// ReasonExists means a file or folder already exists at the natural
	// destination.
	ReasonExists
	// ReasonDuplicate means an earlier source of the same job writes the
	// natural destination, or replaces the same existing file.
	ReasonDuplicate
	// ReasonSource means the natural destination is one of the job's
	// sources, by path or by file identity, which is never overwritten
	// (RSZ-026).
	ReasonSource
	// ReasonUnverified means an Overwrite job could not prove that the file
	// at the natural destination is not one of its sources, so the result
	// was renamed instead of replacing it.
	ReasonUnverified
)

func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonExists:
		return "exists"
	case ReasonDuplicate:
		return "duplicate"
	case ReasonSource:
		return "source"
	case ReasonUnverified:
		return "unverified"
	}
	return fmt.Sprintf("Reason(%d)", int(r))
}

// Item is the planned outcome of one source.
type Item struct {
	// Source is the source path, as given in the request.
	Source string
	// Format is the format the result is encoded in: decode.FormatPNG or
	// decode.FormatJPEG.
	Format decode.Format
	Action Action
	// Destination is the file the item creates or replaces, or, for a
	// skipped or failed item, the natural destination it left alone.
	Destination string
	Reason      Reason
	// Err is the *Error of an ActionFail item, with KindDestination.
	Err error
}

// Renamed reports whether the item creates a file under a numbered name
// because its natural destination was unavailable.
func (i Item) Renamed() bool {
	return i.Action == ActionCreate && i.Reason != ReasonNone
}

// Plan is a job's planned destinations. Items holds one entry per request
// source, at the same index.
type Plan struct {
	// Folder is the output folder, cleaned, which Prepare created when it
	// did not exist.
	Folder string
	Items  []Item
}

// OutputFormat is the format a source is encoded in under the job's output
// format option: the source's own content format for Keep Source Format,
// otherwise the chosen format (RSZ-022).
func OutputFormat(option imageresizer.OutputFormat, content decode.Format) decode.Format {
	switch option {
	case imageresizer.FormatPNG:
		return decode.FormatPNG
	case imageresizer.FormatJPEG:
		return decode.FormatJPEG
	}
	return content
}

// Extension is the file extension written for an encoded format: .png for
// PNG and .jpg for JPEG (RSZ-023). Other formats have none.
func Extension(format decode.Format) string {
	switch format {
	case decode.FormatPNG:
		return ".png"
	case decode.FormatJPEG:
		return ".jpg"
	}
	return ""
}

// Prepare validates the request, creates the output folder when needed, and
// plans every source's destination. See the package documentation for the
// rules. A job-level failure returns an *Error and no plan; a failure that
// concerns one destination is attached to its item.
func Prepare(ctx context.Context, request Request) (Plan, error) {
	return prepare(ctx, request, osFiles{})
}

func prepare(ctx context.Context, request Request, files fileSystem) (Plan, error) {
	if err := validateRequest(request); err != nil {
		return Plan{}, err
	}
	if request.Options.Collision == imageresizer.CollisionOverwrite && !request.ConfirmOverwrite {
		return Plan{}, kindError(KindConfirmation, "confirm overwrite", ErrOverwriteUnconfirmed)
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, kindError(KindCanceled, "plan outputs", err)
	}
	folder := filepath.Clean(request.Options.OutputFolder)
	if err := prepareFolder(files, folder); err != nil {
		return Plan{}, err
	}
	planner := newPlanner(ctx, request, folder, files)
	items, err := planner.plan()
	if err != nil {
		return Plan{}, err
	}
	return Plan{Folder: folder, Items: items}, nil
}

func validateRequest(request Request) error {
	if err := request.Options.Validate(); err != nil {
		return kindError(KindValidation, "validate resize options", err)
	}
	if len(request.Sources) == 0 {
		return kindError(KindValidation, "validate sources", ErrNoSources)
	}
	for index, source := range request.Sources {
		if err := validateSource(source); err != nil {
			return kindError(KindValidation, "validate sources",
				fmt.Errorf("source %d (%q): %w", index+1, source.Path, err))
		}
	}
	return nil
}

func validateSource(source Source) error {
	if strings.ContainsRune(source.Path, 0) {
		return errors.New("the path contains a NUL character")
	}
	if !filepath.IsAbs(source.Path) {
		return errors.New("the path is not absolute")
	}
	switch name := filepath.Base(source.Path); name {
	case ".", "..", string(filepath.Separator):
		return errors.New("the path does not name a file")
	}
	if source.Format != decode.FormatPNG && source.Format != decode.FormatJPEG {
		return fmt.Errorf("the content format is %v, not PNG or JPEG", source.Format)
	}
	return nil
}

// prepareFolder creates the output folder when it does not exist and checks
// that the path names a folder (RSZ-030).
func prepareFolder(files fileSystem, folder string) error {
	if err := files.MkdirAll(folder); err != nil {
		return kindError(KindFolder, "create output folder", err)
	}
	info, err := files.Stat(folder)
	if err != nil {
		return kindError(KindFolder, "open output folder", err)
	}
	if !info.IsDir() {
		return kindError(KindFolder, "open output folder", fmt.Errorf("%s: %w", folder, ErrNotFolder))
	}
	return nil
}

// fileSystem is the filesystem access planning needs, so tests can inject
// failures the real filesystem cannot produce on demand.
type fileSystem interface {
	MkdirAll(path string) error
	Stat(path string) (fs.FileInfo, error)
	Lstat(path string) (fs.FileInfo, error)
	// Identity returns the identity of the file the path resolves to,
	// following links, so two paths to one file report the same identity.
	Identity(path string) (fileID, error)
}

type osFiles struct{}

func (osFiles) MkdirAll(path string) error             { return os.MkdirAll(path, 0o755) }
func (osFiles) Stat(path string) (fs.FileInfo, error)  { return os.Stat(path) }
func (osFiles) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (osFiles) Identity(path string) (fileID, error)   { return identify(path) }

// entry is what exists at a candidate destination.
type entry uint8

const (
	// entryAbsent means nothing exists at the path.
	entryAbsent entry = iota
	// entryFile means a regular file exists at the path.
	entryFile
	// entryOther means a folder, link, or other entry exists at the path,
	// which a result never replaces.
	entryOther
)

// probe is the cached result of inspecting one destination path.
type probe struct {
	entry entry
	err   error
}

type planner struct {
	ctx       context.Context
	files     fileSystem
	folder    string
	format    imageresizer.OutputFormat
	collision imageresizer.CollisionPolicy
	sources   []Source
	// sourceKeys holds the path key of every source, so no destination is
	// ever planned onto a source path, whether or not the source still
	// exists.
	sourceKeys map[string]struct{}
	// claimed holds the path keys of destinations already planned for
	// creation or replacement.
	claimed map[string]struct{}
	// probes caches destination inspections by path key.
	probes map[string]probe
	// sourceIDs holds the file identities of the sources, loaded the first
	// time an existing destination file must be compared with them.
	sourceIDs map[fileID]struct{}
	loaded    bool
	// unverified reports that some source exists but its identity could
	// not be read, so no existing file can be proven not to be it.
	unverified bool
	// replaced holds the identities of files planned for replacement, so
	// two items never replace one file through two names.
	replaced map[fileID]struct{}
}

func newPlanner(ctx context.Context, request Request, folder string, files fileSystem) *planner {
	sourceKeys := make(map[string]struct{}, len(request.Sources))
	for _, source := range request.Sources {
		sourceKeys[imageresizer.PathKey(source.Path)] = struct{}{}
	}
	return &planner{
		ctx:        ctx,
		files:      files,
		folder:     folder,
		format:     request.Options.Format,
		collision:  request.Options.Collision,
		sources:    request.Sources,
		sourceKeys: sourceKeys,
		claimed:    make(map[string]struct{}, len(request.Sources)),
		probes:     make(map[string]probe, len(request.Sources)),
		sourceIDs:  make(map[fileID]struct{}),
		replaced:   make(map[fileID]struct{}),
	}
}

// plan assigns destinations in two passes. The first gives every item whose
// natural destination is available that destination, in request order, so
// a source never loses its own name to another item's numbered name. The
// second resolves the remaining items by the collision policy, in request
// order, so numbered names are deterministic (AC-R08).
func (p *planner) plan() ([]Item, error) {
	items := make([]Item, len(p.sources))
	var conflicts []int
	for index, source := range p.sources {
		if err := p.ctx.Err(); err != nil {
			return nil, kindError(KindCanceled, "plan outputs", err)
		}
		format := OutputFormat(p.format, source.Format)
		item := Item{
			Source:      source.Path,
			Format:      format,
			Destination: filepath.Join(p.folder, stem(source.Path)+Extension(format)),
		}
		resolved, err := p.natural(&item)
		if err != nil {
			return nil, err
		}
		items[index] = item
		if !resolved {
			conflicts = append(conflicts, index)
		}
	}
	for _, index := range conflicts {
		if err := p.resolve(&items[index]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// natural tries to give the item its natural destination. It returns false
// with the item's Reason set when the destination is unavailable and the
// collision policy must resolve the item. A context error is returned; a
// destination failure is attached to the item.
func (p *planner) natural(item *Item) (bool, error) {
	name := filepath.Base(item.Destination)
	if tooLong(name) {
		p.fail(item, "name output file", fmt.Errorf("%s: %w", name, ErrNameTooLong))
		return true, nil
	}
	key := imageresizer.PathKey(item.Destination)
	if _, source := p.sourceKeys[key]; source {
		item.Reason = ReasonSource
		return false, nil
	}
	if _, taken := p.claimed[key]; taken {
		item.Reason = ReasonDuplicate
		return false, nil
	}
	found := p.probe(key, item.Destination)
	switch {
	case found.err != nil:
		p.fail(item, "check destination", found.err)
		return true, nil
	case found.entry == entryAbsent:
		p.claimed[key] = struct{}{}
		item.Action = ActionCreate
		return true, nil
	case found.entry == entryOther:
		item.Reason = ReasonExists
		return false, nil
	}
	// A regular file exists. Whatever the policy, a file that is one of the
	// sources under another name is reported as the source.
	id, idErr := p.files.Identity(item.Destination)
	if idErr == nil {
		source, err := p.isSource(id)
		if err != nil {
			return false, err
		}
		if source {
			item.Reason = ReasonSource
			return false, nil
		}
	}
	if p.collision != imageresizer.CollisionOverwrite {
		item.Reason = ReasonExists
		return false, nil
	}
	if idErr != nil || p.unverified {
		item.Reason = ReasonUnverified
		return false, nil
	}
	if _, twice := p.replaced[id]; twice {
		item.Reason = ReasonDuplicate
		return false, nil
	}
	p.replaced[id] = struct{}{}
	p.claimed[key] = struct{}{}
	item.Action = ActionReplace
	return true, nil
}

// resolve applies the collision policy to an item whose natural destination
// is unavailable: Skip skips it, and Rename and Overwrite search for an
// unused numbered name.
func (p *planner) resolve(item *Item) error {
	if p.collision == imageresizer.CollisionSkip {
		item.Action = ActionSkip
		return nil
	}
	base := filepath.Base(item.Destination)
	extension := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, extension)
	for suffix := 2; suffix <= maxSuffix; suffix++ {
		if err := p.ctx.Err(); err != nil {
			return kindError(KindCanceled, "plan outputs", err)
		}
		name := fmt.Sprintf("%s_%d%s", prefix, suffix, extension)
		if tooLong(name) {
			p.fail(item, "name output file", fmt.Errorf("%s: %w", name, ErrNameTooLong))
			return nil
		}
		candidate := filepath.Join(p.folder, name)
		key := imageresizer.PathKey(candidate)
		if _, source := p.sourceKeys[key]; source {
			continue
		}
		if _, taken := p.claimed[key]; taken {
			continue
		}
		found := p.probe(key, candidate)
		if found.err != nil {
			p.fail(item, "check destination", found.err)
			return nil
		}
		if found.entry != entryAbsent {
			// A numbered name never replaces anything, whatever the
			// policy: Overwrite replaces only the destination the user
			// would expect, the source's own name.
			continue
		}
		p.claimed[key] = struct{}{}
		item.Destination = candidate
		item.Action = ActionCreate
		return nil
	}
	p.fail(item, "name output file", fmt.Errorf("%s: %w", base, ErrNoUnusedName))
	return nil
}

func (p *planner) fail(item *Item, op string, err error) {
	item.Action = ActionFail
	item.Err = kindError(KindDestination, op, err)
}

// probe inspects a destination path without following a final link, so a
// link is reported as an entry of its own rather than as its target.
func (p *planner) probe(key, path string) probe {
	if cached, ok := p.probes[key]; ok {
		return cached
	}
	var found probe
	info, err := p.files.Lstat(path)
	switch {
	case err == nil && info.Mode().IsRegular():
		found.entry = entryFile
	case err == nil:
		found.entry = entryOther
	case errors.Is(err, fs.ErrNotExist):
		found.entry = entryAbsent
	default:
		found.err = err
	}
	p.probes[key] = found
	return found
}

// isSource reports whether the identity belongs to one of the sources,
// loading the source identities on first use.
func (p *planner) isSource(id fileID) (bool, error) {
	if !p.loaded {
		for _, source := range p.sources {
			if err := p.ctx.Err(); err != nil {
				return false, kindError(KindCanceled, "plan outputs", err)
			}
			sourceID, err := p.files.Identity(source.Path)
			switch {
			case err == nil:
				p.sourceIDs[sourceID] = struct{}{}
			case errors.Is(err, fs.ErrNotExist):
				// A source that no longer exists cannot be overwritten; its
				// path stays protected by its key.
			default:
				p.unverified = true
			}
		}
		p.loaded = true
	}
	_, source := p.sourceIDs[id]
	return source, nil
}

// stem is the source's base file name without its extension.
func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// tooLong reports whether a file name exceeds maxNameLength UTF-16 code
// units.
func tooLong(name string) bool {
	if len(name) <= maxNameLength {
		// UTF-8 never takes fewer bytes than UTF-16 code units.
		return false
	}
	units := 0
	for _, character := range name {
		units += utf16.RuneLen(character)
		if units > maxNameLength {
			return true
		}
	}
	return false
}

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
// Skip. Under Overwrite, a renamed result replaces an existing file with its
// numbered name, such as an earlier run's result, as readily as a result
// with its natural name, so re-running a job leaves the same files behind.
//
// No plan ever writes over a source (RSZ-026, NFR-020). Destinations are
// compared by path, case-insensitively, with every source of the job and
// with every other file listed as a source that the job does not process.
// Before Overwrite replaces an existing file it also compares the file's
// identity with theirs, so a hard link, a junction, or another alias of a
// source is recognized as that source. A destination that is a source is
// never replaced, even under Overwrite: Rename and Overwrite rename the
// result instead, and Skip skips it. Overwrite replaces an existing file
// only when it can prove the file is none of the sources; when the identity
// of the file or of a source cannot be read, the result is renamed rather
// than risk a source. Rename and Skip never replace anything, so they do not
// read identities.
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
//
// The package imports no other Image Resizer package but decode, so the
// Image Resizer view and the job that runs it can both import it.
package output

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

// maxSuffix bounds the Rename search, so a folder crowded with earlier
// results, or a filesystem that keeps reporting names as taken, cannot keep
// the search running forever. The Screenshot output uses the same bound.
const maxSuffix = 9999

// maxNameLength is the longest file name NTFS, ReFS, exFAT, and FAT32 long
// names accept, in UTF-16 code units.
const maxNameLength = 255

// Format is a job's output format option (RSZ-022). The Image Resizer's
// persisted options use these values.
type Format string

const (
	// FormatKeepSource encodes each source in its detected content format.
	FormatKeepSource Format = "keep-source"
	// FormatPNG encodes every source as PNG.
	FormatPNG Format = "png"
	// FormatJPEG encodes every source as JPEG.
	FormatJPEG Format = "jpeg"
)

// Collision is a job's collision policy (RSZ-025). The Image Resizer's
// persisted options use these values.
type Collision string

const (
	// CollisionRename saves a colliding result under a numbered name.
	CollisionRename Collision = "rename"
	// CollisionSkip leaves the existing destination untouched and skips the
	// item.
	CollisionSkip Collision = "skip"
	// CollisionOverwrite replaces existing destination files, only after
	// the user's explicit confirmation for the job.
	CollisionOverwrite Collision = "overwrite"
)

// Kind classifies a planning failure so callers can present the right
// recovery path per UX-010.
type Kind uint8

const (
	// KindValidation means the request itself is invalid: an output folder
	// that is not an absolute path, an unknown format or policy, no sources,
	// a source that is not an absolute file path with a decoded PNG or JPEG
	// format, or a protected path that is not an absolute file path.
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
	// output folder path, or a path that would have to hold it, names a
	// file rather than a folder.
	ErrNotFolder = errors.New("the output path is not a folder")
	// ErrNameTooLong is the cause of the KindDestination error attached to
	// an item whose output file name would exceed what Windows file systems
	// accept.
	ErrNameTooLong = fmt.Errorf("the output file name would be longer than %d characters", maxNameLength)
	// ErrNoUnusedName is the cause of the KindDestination error attached to
	// an item for which the Rename search found no unused name.
	ErrNoUnusedName = fmt.Errorf("no unused output file name was found after %d attempts", maxSuffix-1)
)

// PathKey normalizes a full path for comparison. Windows paths compare
// case-insensitively, so "photo.PNG" and "PHOTO.png" name the same file.
// The source inventory deduplicates sources by it, and output planning
// compares destinations with sources and with each other by it.
func PathKey(path string) string {
	return strings.ToUpper(filepath.Clean(path))
}

// Source is one processable source of a job: a file whose content was
// decoded successfully.
type Source struct {
	// Path is the absolute source path, as listed in the source inventory.
	Path string
	// Format is the detected content format, from the source's decode.Info.
	// Keep Source Format encodes the result in this format.
	Format decode.Format
}

// Request describes one job to plan. Prepare validates only what planning
// uses; the job validates the rest of its options, such as JPEG quality.
type Request struct {
	// Sources lists the job's processable sources in the order the job
	// processes them. That order decides which of two sources with the
	// same destination keeps the name.
	Sources []Source
	// Protected lists the absolute paths of the other files listed as the
	// job's sources: those the job does not process, such as files that are
	// unsupported, corrupt, too large, or inaccessible. They are protected
	// exactly as Sources are, so no result is ever planned onto a listed
	// original, whether or not the job resizes it (RSZ-026, NFR-020).
	Protected []string
	// Folder is the absolute path of the output folder.
	Folder string
	// Format is the output format option.
	Format Format
	// Collision is the collision policy.
	Collision Collision
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
	// plan proved is none of the sources. Only a confirmed Overwrite job
	// replaces: the natural destination, or for a renamed item a file with
	// its numbered name.
	ActionReplace
	// ActionSkip leaves Destination untouched and marks the item Skipped.
	// Only a Skip job skips.
	ActionSkip
	// ActionFail marks the item Failed with Err. Destination is the item's
	// natural destination; Err names the file name or check that failed,
	// which for a renamed item may be a numbered name.
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
// was renamed (ActionCreate or ActionReplace under a numbered name) or
// skipped (ActionSkip).
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
	// sources or protected paths, by path or, under Overwrite, by file
	// identity, which is never overwritten (RSZ-026).
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
	// DuplicateOf, when Reason is ReasonDuplicate, is the index in
	// Plan.Items of the earlier item that writes this item's natural
	// destination or replaces the same file. It means nothing for other
	// reasons. A Skip job skips such an item only because that item writes
	// the name; see Plan.Successor for when it does not.
	DuplicateOf int
	// Err is the *Error of an ActionFail item, with KindDestination.
	Err error
}

// Renamed reports whether the item writes a file under a numbered name
// because its natural destination was unavailable.
func (i Item) Renamed() bool {
	return (i.Action == ActionCreate || i.Action == ActionReplace) && i.Reason != ReasonNone
}

// Plan is a job's planned destinations. Items holds one entry per request
// source, at the same index.
type Plan struct {
	// Folder is the output folder, cleaned, which Prepare created when it
	// did not exist.
	Folder string
	Items  []Item
}

// Successor returns the index of the item that may write the destination
// the item at index was to create but did not: the next item a Skip job
// skipped only because the same name was planned for an earlier item. The
// job asks for it when an ActionCreate item ends without writing its file,
// writes the successor's result at that destination as ActionCreate,
// without replacing anything, and asks again should the successor fail
// too, so a Skip job still saves one result per name whenever one can be
// saved (RSZ-024). It reports false when no item waits on the name.
func (p Plan) Successor(index int) (int, bool) {
	if index < 0 || index >= len(p.Items) {
		return 0, false
	}
	owner := index
	if p.Items[index].Reason == ReasonDuplicate {
		owner = p.Items[index].DuplicateOf
	}
	for next := index + 1; next < len(p.Items); next++ {
		item := p.Items[next]
		if item.Action == ActionSkip && item.Reason == ReasonDuplicate && item.DuplicateOf == owner {
			return next, true
		}
	}
	return 0, false
}

// OutputFormat is the format a source is encoded in under the job's output
// format option: the source's own content format for Keep Source Format,
// otherwise the chosen format (RSZ-022).
func OutputFormat(option Format, content decode.Format) decode.Format {
	switch option {
	case FormatPNG:
		return decode.FormatPNG
	case FormatJPEG:
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
	if request.Collision == CollisionOverwrite && !request.ConfirmOverwrite {
		return Plan{}, kindError(KindConfirmation, "confirm overwrite", ErrOverwriteUnconfirmed)
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, kindError(KindCanceled, "plan outputs", err)
	}
	folder := filepath.Clean(request.Folder)
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
	if err := validateFolder(request.Folder); err != nil {
		return kindError(KindValidation, "validate output folder", fmt.Errorf("%q: %w", request.Folder, err))
	}
	switch request.Format {
	case FormatKeepSource, FormatPNG, FormatJPEG:
	default:
		return kindError(KindValidation, "validate output format", fmt.Errorf("unknown output format %q", request.Format))
	}
	switch request.Collision {
	case CollisionRename, CollisionSkip, CollisionOverwrite:
	default:
		return kindError(KindValidation, "validate collision policy", fmt.Errorf("unknown collision policy %q", request.Collision))
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
	for index, path := range request.Protected {
		if err := validateFile(path); err != nil {
			return kindError(KindValidation, "validate protected paths",
				fmt.Errorf("protected path %d (%q): %w", index+1, path, err))
		}
	}
	return nil
}

func validateFolder(folder string) error {
	switch {
	case folder == "":
		return errors.New("the output folder is empty")
	case strings.ContainsRune(folder, 0):
		return errors.New("the path contains a NUL character")
	case !filepath.IsAbs(folder):
		return errors.New("the path is not absolute")
	}
	return nil
}

func validateSource(source Source) error {
	if err := validateFile(source.Path); err != nil {
		return err
	}
	if source.Format != decode.FormatPNG && source.Format != decode.FormatJPEG {
		return fmt.Errorf("the content format is %v, not PNG or JPEG", source.Format)
	}
	return nil
}

func validateFile(path string) error {
	if strings.ContainsRune(path, 0) {
		return errors.New("the path contains a NUL character")
	}
	if !filepath.IsAbs(path) {
		return errors.New("the path is not absolute")
	}
	switch name := filepath.Base(path); name {
	case ".", "..", string(filepath.Separator):
		return errors.New("the path does not name a file")
	}
	return nil
}

// prepareFolder creates the output folder when it does not exist and checks
// that the path names a folder (RSZ-030).
func prepareFolder(files fileSystem, folder string) error {
	if err := files.MkdirAll(folder); err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			// os.MkdirAll reports a file standing at the folder's path, or
			// at the path of a folder that would hold it, as ENOTDIR.
			err = fmt.Errorf("%w: %w", ErrNotFolder, err)
		}
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
	// StoredFile reports whether an entry Lstat reports as irregular is a
	// file whose reparse point only says how its data is stored, rather
	// than a link or a folder.
	StoredFile(path string) (bool, error)
}

type osFiles struct{}

func (osFiles) MkdirAll(path string) error             { return os.MkdirAll(path, 0o755) }
func (osFiles) Stat(path string) (fs.FileInfo, error)  { return os.Stat(path) }
func (osFiles) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (osFiles) Identity(path string) (fileID, error)   { return identify(path) }
func (osFiles) StoredFile(path string) (bool, error)   { return storedFile(path) }

// entry is what exists at a candidate destination.
type entry uint8

const (
	// entryAbsent means nothing exists at the path.
	entryAbsent entry = iota
	// entryFile means a file exists at the path: a regular file, or one
	// whose reparse point only says how its data is stored.
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

// verdict is whether an Overwrite job may replace one existing file.
type verdict struct {
	// reason is ReasonNone when the file may be replaced, otherwise why it
	// may not: ReasonSource, ReasonUnverified, or ReasonDuplicate.
	reason Reason
	// id is the file's identity, when it could be read.
	id fileID
	// owner is the index of the item that already replaces the file, for
	// ReasonDuplicate.
	owner int
}

type planner struct {
	ctx       context.Context
	files     fileSystem
	folder    string
	format    Format
	collision Collision
	sources   []Source
	// protected holds the request's protected paths.
	protected []string
	// sourceKeys holds the path key of every source and protected path, so
	// no destination is ever planned onto one, whether or not it still
	// exists.
	sourceKeys map[string]struct{}
	// claimed maps the path key of every destination already planned for
	// creation or replacement to the index of its item.
	claimed map[string]int
	// probes caches destination inspections by path key.
	probes map[string]probe
	// sourceIDs holds the file identities of the sources and protected
	// paths, loaded the first time an Overwrite job must compare an existing
	// file with them.
	sourceIDs map[fileID]struct{}
	loaded    bool
	// unverified reports that some source or protected path exists but its
	// identity could not be read, so no existing file can be proven not to
	// be it.
	unverified bool
	// replaced maps the identity of every file planned for replacement to
	// the index of its item, so two items never replace one file through
	// two names.
	replaced map[fileID]int
	// next maps a numbered-name pattern — the upper-case name prefix and
	// extension — to the first suffix the Rename search has not passed over.
	// Every lower suffix is a source, another item's destination, or an
	// entry that cannot be used, and none of that changes while a plan is
	// made, so the next item with the same pattern starts there.
	next map[string]int
}

func newPlanner(ctx context.Context, request Request, folder string, files fileSystem) *planner {
	sourceKeys := make(map[string]struct{}, len(request.Sources)+len(request.Protected))
	for _, source := range request.Sources {
		sourceKeys[PathKey(source.Path)] = struct{}{}
	}
	for _, path := range request.Protected {
		sourceKeys[PathKey(path)] = struct{}{}
	}
	return &planner{
		ctx:        ctx,
		files:      files,
		folder:     folder,
		format:     request.Format,
		collision:  request.Collision,
		sources:    request.Sources,
		protected:  request.Protected,
		sourceKeys: sourceKeys,
		claimed:    make(map[string]int, len(request.Sources)),
		probes:     make(map[string]probe, len(request.Sources)),
		sourceIDs:  make(map[fileID]struct{}),
		replaced:   make(map[fileID]int),
		next:       make(map[string]int),
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
		resolved, err := p.natural(index, &item)
		if err != nil {
			return nil, err
		}
		items[index] = item
		if !resolved {
			conflicts = append(conflicts, index)
		}
	}
	for _, index := range conflicts {
		if err := p.resolve(index, &items[index]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// natural tries to give the item its natural destination. It returns false
// with the item's Reason set when the destination is unavailable and the
// collision policy must resolve the item. A context error is returned; a
// destination failure is attached to the item.
func (p *planner) natural(index int, item *Item) (bool, error) {
	name := filepath.Base(item.Destination)
	if tooLong(name) {
		p.fail(item, "name output file", fmt.Errorf("%s: %w", name, ErrNameTooLong))
		return true, nil
	}
	key := PathKey(item.Destination)
	if _, source := p.sourceKeys[key]; source {
		item.Reason = ReasonSource
		return false, nil
	}
	if owner, taken := p.claimed[key]; taken {
		item.Reason = ReasonDuplicate
		item.DuplicateOf = owner
		return false, nil
	}
	found := p.probe(key, item.Destination)
	switch {
	case found.err != nil:
		p.fail(item, "check destination", found.err)
		return true, nil
	case found.entry == entryAbsent:
		p.claimed[key] = index
		item.Action = ActionCreate
		return true, nil
	case found.entry == entryOther, p.collision != CollisionOverwrite:
		// Rename and Skip never replace a file, so whether an existing file
		// is a source under another name changes nothing they do.
		item.Reason = ReasonExists
		return false, nil
	}
	decision, err := p.replaceable(item.Destination)
	if err != nil {
		return false, err
	}
	if decision.reason != ReasonNone {
		item.Reason = decision.reason
		item.DuplicateOf = decision.owner
		return false, nil
	}
	p.replace(key, decision.id, index)
	item.Action = ActionReplace
	return true, nil
}

// resolve applies the collision policy to an item whose natural destination
// is unavailable: Skip skips it, and Rename and Overwrite search for a
// numbered name that is unused or, under Overwrite, an existing file that
// may be replaced.
func (p *planner) resolve(index int, item *Item) error {
	if p.collision == CollisionSkip {
		item.Action = ActionSkip
		return nil
	}
	base := filepath.Base(item.Destination)
	extension := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, extension)
	pattern := strings.ToUpper(prefix) + "\x00" + strings.ToUpper(extension)
	var uncheckable error
	for suffix := max(p.next[pattern], 2); suffix <= maxSuffix; suffix++ {
		if err := p.ctx.Err(); err != nil {
			return kindError(KindCanceled, "plan outputs", err)
		}
		name := fmt.Sprintf("%s_%d%s", prefix, suffix, extension)
		if tooLong(name) {
			// Every later suffix is at least as long.
			p.next[pattern] = suffix
			p.fail(item, "name output file", fmt.Errorf("%s: %w", name, ErrNameTooLong))
			return nil
		}
		candidate := filepath.Join(p.folder, name)
		key := PathKey(candidate)
		if _, source := p.sourceKeys[key]; source {
			continue
		}
		if _, taken := p.claimed[key]; taken {
			continue
		}
		found := p.probe(key, candidate)
		switch {
		case found.err != nil:
			// A numbered name only has to be usable, so a name that cannot
			// be checked is passed over, unless the output folder itself
			// can no longer be reached, which every later name would meet.
			if _, err := p.files.Stat(p.folder); err != nil {
				p.fail(item, "check destination", found.err)
				return nil
			}
			if uncheckable == nil {
				uncheckable = found.err
			}
		case found.entry == entryAbsent:
			p.next[pattern] = suffix + 1
			p.claimed[key] = index
			item.Destination = candidate
			item.Action = ActionCreate
			return nil
		case found.entry == entryFile && p.collision == CollisionOverwrite:
			decision, err := p.replaceable(candidate)
			if err != nil {
				return err
			}
			if decision.reason == ReasonNone {
				p.next[pattern] = suffix + 1
				p.replace(key, decision.id, index)
				item.Destination = candidate
				item.Action = ActionReplace
				return nil
			}
		}
	}
	p.next[pattern] = maxSuffix + 1
	err := fmt.Errorf("%s: %w", base, ErrNoUnusedName)
	if uncheckable != nil {
		err = fmt.Errorf("%w; a name that could not be checked was passed over: %w", err, uncheckable)
	}
	p.fail(item, "name output file", err)
	return nil
}

func (p *planner) fail(item *Item, op string, err error) {
	item.Action = ActionFail
	item.Err = kindError(KindDestination, op, err)
}

// replace records that the item at index replaces the file at key.
func (p *planner) replace(key string, id fileID, index int) {
	p.replaced[id] = index
	p.claimed[key] = index
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
	case err == nil && info.Mode().Type() == fs.ModeIrregular:
		// Since Go 1.23, os.Lstat reports a Windows file with a reparse
		// point other than a symbolic link as irregular, and that includes
		// every file OneDrive keeps in sync. A reparse point that only says
		// how the data is stored leaves a file like any other; a link, a
		// junction, or one that cannot be read is never replaced.
		found.entry = entryOther
		if stored, err := p.files.StoredFile(path); err == nil && stored {
			found.entry = entryFile
		}
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

// replaceable decides whether an Overwrite job may replace the existing file
// at path: only when its identity proves it is none of the sources and no
// earlier item replaces it. Only a context error is returned.
func (p *planner) replaceable(path string) (verdict, error) {
	id, err := p.files.Identity(path)
	if err != nil {
		return verdict{reason: ReasonUnverified}, nil
	}
	source, err := p.isSource(id)
	if err != nil {
		return verdict{}, err
	}
	switch owner, twice := p.replaced[id]; {
	case source:
		return verdict{reason: ReasonSource, id: id}, nil
	case p.unverified:
		return verdict{reason: ReasonUnverified, id: id}, nil
	case twice:
		return verdict{reason: ReasonDuplicate, id: id, owner: owner}, nil
	}
	return verdict{id: id}, nil
}

// isSource reports whether the identity belongs to one of the sources or
// protected paths, loading their identities on first use.
func (p *planner) isSource(id fileID) (bool, error) {
	if !p.loaded {
		paths := make([]string, 0, len(p.sources)+len(p.protected))
		for _, source := range p.sources {
			paths = append(paths, source.Path)
		}
		paths = append(paths, p.protected...)
		for _, path := range paths {
			if err := p.ctx.Err(); err != nil {
				return false, kindError(KindCanceled, "plan outputs", err)
			}
			sourceID, err := p.files.Identity(path)
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

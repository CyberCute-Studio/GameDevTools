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
// request order keeps the name, and a later one is renamed, or under Skip
// deferred, written only when the first ends without writing it. Overwrite
// replaces only a result's natural destination. A result it renames takes
// the first unused numbered name, as under Rename, because a file with a
// numbered name may be an original the job was never asked to replace
// rather than an earlier result (RSZ-049).
//
// No plan ever writes over a source (RSZ-026, NFR-020). Destinations are
// compared by path, case-insensitively, with every source of the job and
// with every other file listed as a source that the job does not process.
// Before Overwrite replaces an existing file it also compares the file's
// identity with theirs, so a hard link, a junction, or another alias of a
// source is recognized as that source. A destination that is a source is
// never replaced, even under Overwrite: Rename and Overwrite rename the
// result instead, and Skip skips it. Overwrite replaces an existing file
// only when it can prove the file is none of the sources; when the file's
// identity cannot be read, or a listed file's cannot and the file may be
// that one, the result is renamed rather than risk a source. It replaces
// only a file its folder lists under the destination's own name, never one
// the name reaches as an 8.3 short name. Rename and Skip never replace
// anything, so they do not read identities.
//
// Prepare validates the request, creates the output folder when it does not
// exist (RSZ-030), and inspects the destinations as the filesystem stands
// when it runs, so re-running a job plans afresh rather than assuming
// earlier results (RSZ-049). A file can still appear at a planned
// destination after Prepare returns; the writer must finalize ActionCreate
// items without replacing anything. A source can likewise be moved or linked
// to the name of a file planned for replacement, so the writer replaces an
// ActionReplace item's file only while Item.StillReplaceable reports that it
// is still the file the plan proved is none of the sources.
//
// Failures are reported as *Error values with a Kind. A failure that
// concerns the whole job — an invalid request, a missing confirmation, or an
// output folder that is unusable, or becomes so while the plan is made — is
// returned by Prepare; a failure that concerns one destination is attached
// to that item, and the other items are planned regardless (RSZ-044).
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
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

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

// Valid reports whether the format is one of the output format options.
func (f Format) Valid() bool {
	switch f {
	case FormatKeepSource, FormatPNG, FormatJPEG:
		return true
	}
	return false
}

// Valid reports whether the policy is one of the collision policies.
func (c Collision) Valid() bool {
	switch c {
	case CollisionRename, CollisionSkip, CollisionOverwrite:
		return true
	}
	return false
}

// Kind classifies a planning failure so callers can present the right
// recovery path per UX-010.
type Kind uint8

const (
	// KindValidation means the request itself is invalid: an output folder
	// ValidateFolder refuses, an unknown format or policy, no sources,
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
	// an item for which the Rename search found no unused name: every
	// numbered name it may take was already used, by this item's search or
	// an earlier one with the same name.
	ErrNoUnusedName = fmt.Errorf("no unused output file name was found among the numbered names _2 to _%d", maxSuffix)

	// ErrFolderEmpty, ErrFolderText, ErrFolderNotAbsolute, ErrFolderRoot,
	// and ErrFolderName are the causes ValidateFolder reports.
	ErrFolderEmpty       = errors.New("the output folder is empty")
	ErrFolderText        = errors.New("the path contains a NUL or replacement character")
	ErrFolderNotAbsolute = errors.New("the path is not absolute")
	ErrFolderRoot        = errors.New("the path names a drive, volume, device, or server rather than a folder on it")
	ErrFolderName        = errors.New("a folder name in the path ends in a space or a period, which Windows removes")
)

// ValidateFolder checks an output folder path the way the persisted options
// and Prepare both require: it is not empty, is valid text, is absolute,
// names a folder, and no folder name in it ends in a space or a period.
//
// An absolute path that is only a volume name, such as \\?\C: or \\.\C:
// without the root folder's separator, or \\server without a share, names a
// volume, device, or server rather than a folder: no folder can be created
// or opened there, and filepath.Join puts a file name straight after it,
// giving \\?\C:x.png, so its destinations would not lie in it. A share,
// \\server\share, is its own root folder.
//
// A path that is not valid UTF-8, as Go holds a Windows name with an
// unpaired surrogate, cannot be kept in the settings file, whose JSON
// replaces such bytes with U+FFFD, and a U+FFFD replacement character marks
// a path such a lossy conversion changed; either may name another folder
// than the one chosen. Windows removes trailing spaces and periods from the
// last name of a path it creates or opens, but not all of them from a name
// a longer path leads through: for an output folder with such a name, the
// folder Prepare creates and checks would not be the folder its destinations
// lie in, and creating a missing folder with such a name on the way to it
// would leave a stray folder without them that the rest of the path never
// reaches. An extended-length path, marked \\?\ or \??\, is one Windows takes
// verbatim, so every name in it keeps its spaces and periods and the rule
// does not apply.
func ValidateFolder(folder string) error {
	switch {
	case folder == "":
		return ErrFolderEmpty
	case strings.ContainsRune(folder, 0), strings.ContainsRune(folder, utf8.RuneError):
		return ErrFolderText
	case !filepath.IsAbs(folder):
		return ErrFolderNotAbsolute
	}
	clean := filepath.Clean(folder)
	if volume := filepath.VolumeName(clean); len(volume) == len(clean) && !shareRoot(volume) {
		return ErrFolderRoot
	}
	if strings.HasPrefix(clean, `\\?\`) || strings.HasPrefix(clean, `\??\`) {
		return nil
	}
	names := strings.FieldsFunc(clean[len(filepath.VolumeName(clean)):], func(character rune) bool {
		return character == '\\' || character == '/'
	})
	for _, name := range names {
		if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
			return ErrFolderName
		}
	}
	return nil
}

// shareRoot reports whether a volume name, as filepath.VolumeName gives it
// for a cleaned path, names a share, \\server\share, which is a folder,
// rather than a drive (C:), a server without a share (\\server), or the
// first element of a device path (\\?\C:, \\.\NUL), which are not.
func shareRoot(volume string) bool {
	if len(volume) < 2 || !os.IsPathSeparator(volume[0]) || !os.IsPathSeparator(volume[1]) {
		return false
	}
	server, share, found := strings.Cut(volume[2:], string(filepath.Separator))
	return found && share != "" && server != "." && server != "?"
}

// PathKey normalizes a full path for comparison. Windows paths compare
// case-insensitively, so "photo.PNG" and "PHOTO.png" name the same file.
// The source inventory deduplicates sources by it, and output planning
// compares destinations with sources and with each other by it. Bytes that
// are not valid UTF-8, such as the WTF-8 Go holds an unpaired surrogate in,
// are kept as they are, so two names that differ only in them keep distinct
// keys rather than both becoming U+FFFD.
func PathKey(path string) string {
	return foldCase(filepath.Clean(path))
}

// foldCase upper-cases text for case-insensitive comparison, as PathKey
// does, keeping every byte that is not valid UTF-8, which strings.ToUpper
// would turn into U+FFFD.
func foldCase(text string) string {
	if utf8.ValidString(text) {
		return strings.ToUpper(text)
	}
	var key strings.Builder
	key.Grow(len(text))
	for rest := text; rest != ""; {
		character, size := utf8.DecodeRuneInString(rest)
		if character == utf8.RuneError && size == 1 {
			key.WriteByte(rest[0])
		} else {
			key.WriteRune(unicode.ToUpper(character))
		}
		rest = rest[size:]
	}
	return key.String()
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
	// ActionReplace replaces the existing file at Destination, the item's
	// natural destination, which the plan proved is none of the sources.
	// Only a confirmed Overwrite job replaces, and never a file with a
	// numbered name. The writer replaces the file only while
	// Item.StillReplaceable reports it is still that file.
	ActionReplace
	// ActionSkip leaves Destination untouched and marks the item Skipped.
	// Only a Skip job skips.
	ActionSkip
	// ActionFail marks the item Failed with Err. Destination is the item's
	// natural destination; Err names the file name or check that failed,
	// which for a renamed item may be a numbered name.
	ActionFail
	// ActionDefer leaves Destination, the item's natural destination, to the
	// earlier item at DuplicateOf, which plans to create it. Only a Skip job
	// defers. The item ends Skipped once the file is written; when the item
	// it defers to ends without writing it, Plan.Successor names the deferred
	// item that creates it instead.
	ActionDefer
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
	case ActionDefer:
		return "defer"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// Reason explains why an item did not get its natural destination: why it
// was renamed (ActionCreate under a numbered name), skipped (ActionSkip), or
// deferred (ActionDefer).
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
	// ReasonUnverified means an Overwrite job could not prove that the entry
	// at the natural destination is a file it may replace: its folder could
	// not list it as a file under the destination's own name, or the file's
	// identity could not be read, or the identity of a listed file it may
	// be, so the result was renamed instead of replacing it.
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
	// skipped, deferred, or failed item, the natural destination it left
	// alone.
	Destination string
	Reason      Reason
	// DuplicateOf is -1 unless Reason is ReasonDuplicate, when it is the
	// index in Plan.Items of the earlier item that writes this item's
	// natural destination or replaces the same file. A Skip job defers such
	// an item to that one.
	DuplicateOf int
	// Err is the *Error of an ActionFail item, with KindDestination.
	Err error
	// replaces is the identity of the file an ActionReplace item replaces,
	// the one the plan proved is none of the sources.
	replaces fileID
}

// Renamed reports whether the item creates a file under a numbered name
// because its natural destination was unavailable.
func (i Item) Renamed() bool {
	return i.Action == ActionCreate && i.Reason != ReasonNone
}

// StillReplaceable reports whether the file at an ActionReplace item's
// Destination is still the file the plan proved is none of the sources: the
// same file, by its identity, whatever it now holds. A source moved or
// linked to the destination's name after planning is another file, so the
// writer checks this immediately before it replaces the file, and replaces
// nothing when it reports false. It reports false for every other action,
// and when the file's identity cannot be read.
func (i Item) StillReplaceable() bool {
	return i.stillReplaceable(osFiles{})
}

func (i Item) stillReplaceable(files fileSystem) bool {
	if i.Action != ActionReplace {
		return false
	}
	id, _, err := files.Identity(i.Destination)
	return err == nil && id == i.replaces
}

// Plan is a job's planned destinations. Items holds one entry per request
// source, at the same index.
type Plan struct {
	// Folder is the output folder, cleaned, which Prepare created when it
	// did not exist.
	Folder string
	Items  []Item
	// successors maps each item's index to the index of the next item
	// deferred to the same name, or -1.
	successors []int
}

// Successor returns the index of the item that creates the destination the
// item at index was to create but did not: the next item deferred to the
// same earlier item (ActionDefer). The job asks for it when an ActionCreate
// item ends without writing its file, creates the successor's result at
// that destination as ActionCreate, without replacing anything, and asks
// again should the successor end without writing it too. Once the file is
// written, every item still deferred to the name ends Skipped, so a Skip job
// saves one result per name whenever one can be saved (RSZ-024). It reports
// false when no item waits on the name.
func (p Plan) Successor(index int) (int, bool) {
	if index < 0 || index >= len(p.successors) || p.successors[index] < 0 {
		return 0, false
	}
	return p.successors[index], true
}

// successors links every item to the next item deferred to the same name:
// the item a deferred item defers to links to the first, and each deferred
// item to the one after it.
func successors(items []Item) []int {
	links := make([]int, len(items))
	last := make(map[int]int)
	for index, item := range items {
		links[index] = -1
		if item.Action != ActionDefer {
			continue
		}
		previous, chained := last[item.DuplicateOf]
		if !chained {
			previous = item.DuplicateOf
		}
		links[previous] = index
		last[item.DuplicateOf] = index
	}
	return links
}

// EncodedFormat is the format a source is encoded in under the job's output
// format option: the source's own content format for Keep Source Format,
// otherwise the chosen format (RSZ-022).
func EncodedFormat(option Format, content decode.Format) decode.Format {
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
	return Plan{Folder: folder, Items: items, successors: successors(items)}, nil
}

func validateRequest(request Request) error {
	if err := ValidateFolder(request.Folder); err != nil {
		return kindError(KindValidation, "validate output folder", fmt.Errorf("%q: %w", request.Folder, err))
	}
	if !request.Format.Valid() {
		return kindError(KindValidation, "validate output format", fmt.Errorf("unknown output format %q", request.Format))
	}
	if !request.Collision.Valid() {
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
		if fileInTheWay(files, folder) {
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

// fileInTheWay reports whether a file, rather than a folder, stands at the
// folder's path or at the path of a folder that would hold it: whether the
// nearest of those paths that can be inspected is not a folder. The error
// os.MkdirAll returns cannot tell, as the ENOTDIR it reports for a file in
// the way is, on Windows, ERROR_PATH_NOT_FOUND, which a missing drive,
// share, or folder reports too.
func fileInTheWay(files fileSystem, folder string) bool {
	for path := folder; ; {
		if info, err := files.Stat(path); err == nil {
			return !info.IsDir()
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}

// fileSystem is the filesystem access planning needs, so tests can inject
// failures the real filesystem cannot produce on demand.
type fileSystem interface {
	MkdirAll(path string) error
	Stat(path string) (fs.FileInfo, error)
	Lstat(path string) (fs.FileInfo, error)
	// Identity returns the identity of the file the path resolves to,
	// following links, so two paths to one file report the same identity,
	// and the file's number of names.
	Identity(path string) (fileID, uint32, error)
	// Entry returns how the folder lists the entry the path's last element
	// opens, the entry itself rather than what a link leads to.
	Entry(path string) (entry, error)
}

// entry is how a folder lists one of its entries.
type entry struct {
	// name is the entry's name in the folder. It differs from the last
	// element of a path that opens the entry under another name, such as its
	// 8.3 short name.
	name string
	// file reports that the entry is a file rather than a folder or a link:
	// it has no reparse point, or one that only says how its data is stored,
	// as a file OneDrive keeps in sync has.
	file bool
}

type osFiles struct{}

func (osFiles) MkdirAll(path string) error                   { return os.MkdirAll(path, 0o755) }
func (osFiles) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }
func (osFiles) Lstat(path string) (fs.FileInfo, error)       { return os.Lstat(path) }
func (osFiles) Identity(path string) (fileID, uint32, error) { return identify(path) }
func (osFiles) Entry(path string) (entry, error)             { return findEntry(path) }

// probe is the cached result of inspecting one destination path: whether an
// entry has its name, and that entry's type.
type probe struct {
	exists bool
	mode   fs.FileMode
	err    error
}

// verdict is whether an Overwrite job may replace one existing file.
type verdict struct {
	// reason is ReasonNone when the file may be replaced, otherwise why it
	// may not: ReasonSource, ReasonUnverified, or ReasonDuplicate.
	reason Reason
	// id is the identity of a file that may be replaced, for ReasonNone.
	id fileID
	// owner is the index of the item that already replaces the file, for
	// ReasonDuplicate, and otherwise -1.
	owner int
}

// identified is the cached identity of one path and the number of names of
// the file it resolves to.
type identified struct {
	id    fileID
	links uint32
	err   error
}

type planner struct {
	ctx       context.Context
	files     fileSystem
	folder    string
	format    Format
	collision Collision
	sources   []Source
	// listed holds the path of every source and protected path.
	listed []string
	// sourceKeys holds the path key of every listed path, so no destination
	// is ever planned onto one, whether or not it still exists.
	sourceKeys map[string]struct{}
	// claimed maps the path key of every destination already planned for
	// creation or replacement to the index of its item.
	claimed map[string]int
	// probes caches destination inspections by path key.
	probes map[string]probe
	// identities caches identities by path key: those of existing
	// destinations, so items with one natural destination read it once, and
	// those of the output folder and the folders holding unreadable listed
	// files, so each folder is read once.
	identities map[string]identified
	// sourceIDs holds the file identities of the listed paths, loaded the
	// first time an Overwrite job must compare an existing file with them.
	sourceIDs map[fileID]struct{}
	loaded    bool
	// unverified reports that some listed path may exist but its identity,
	// and where it lies, could not be read, so no existing file can be
	// proven not to be it.
	unverified bool
	// unverifiedFolders holds the identities of the folders holding a listed
	// file that is no link but whose own identity could not be read. Such a
	// file has a name in that folder, so only an existing file that may have
	// a name there too can be it.
	unverifiedFolders map[fileID]struct{}
	// replaced maps the identity of every file planned for replacement to
	// the index of its item, so two items never replace one file through
	// two names.
	replaced map[fileID]int
	// next maps a numbered-name pattern — the upper-case name prefix and
	// extension — to the first suffix the Rename search has not passed over.
	// Every lower suffix is a source, another item's destination, an
	// existing entry, or a name that cannot be checked, and none of that
	// changes while a plan is made, so the next item with the same pattern
	// starts there.
	next map[string]int
	// passed maps a numbered-name pattern to the first error of a name the
	// search passed over because it could not be checked, so every item
	// whose search for the pattern finds no name can say why.
	passed map[string]error
	// unchecked counts the destination names that could not be checked, so
	// folderLost checks the output folder at the first, second, fourth, and
	// so on.
	unchecked int
}

func newPlanner(ctx context.Context, request Request, folder string, files fileSystem) *planner {
	listed := make([]string, 0, len(request.Sources)+len(request.Protected))
	for _, source := range request.Sources {
		listed = append(listed, source.Path)
	}
	listed = append(listed, request.Protected...)
	sourceKeys := make(map[string]struct{}, len(listed))
	for _, path := range listed {
		sourceKeys[PathKey(path)] = struct{}{}
	}
	return &planner{
		ctx:               ctx,
		files:             files,
		folder:            folder,
		format:            request.Format,
		collision:         request.Collision,
		sources:           request.Sources,
		listed:            listed,
		sourceKeys:        sourceKeys,
		claimed:           make(map[string]int, len(request.Sources)),
		probes:            make(map[string]probe, len(request.Sources)),
		identities:        make(map[string]identified),
		sourceIDs:         make(map[fileID]struct{}),
		unverifiedFolders: make(map[fileID]struct{}),
		replaced:          make(map[fileID]int),
		next:              make(map[string]int),
		passed:            make(map[string]error),
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
		format := EncodedFormat(p.format, source.Format)
		item := Item{
			Source:      source.Path,
			Format:      format,
			Destination: filepath.Join(p.folder, stem(source.Path)+Extension(format)),
			DuplicateOf: -1,
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

// reserved reports why no result may be planned at the path key, whatever
// exists there: ReasonSource for a source or protected path, or
// ReasonDuplicate with the index of the item already planned there. It
// reports ReasonNone and -1 for a key that is neither.
func (p *planner) reserved(key string) (Reason, int) {
	if _, source := p.sourceKeys[key]; source {
		return ReasonSource, -1
	}
	if owner, taken := p.claimed[key]; taken {
		return ReasonDuplicate, owner
	}
	return ReasonNone, -1
}

// natural tries to give the item its natural destination. It returns false
// with the item's Reason set when the destination is unavailable and the
// collision policy must resolve the item. A context error, or an output
// folder that can no longer be reached, is returned; a destination failure
// is attached to the item.
func (p *planner) natural(index int, item *Item) (bool, error) {
	name := filepath.Base(item.Destination)
	if tooLong(name) {
		p.fail(item, "name output file", fmt.Errorf("%s: %w", name, ErrNameTooLong))
		return true, nil
	}
	key := PathKey(item.Destination)
	if reason, owner := p.reserved(key); reason != ReasonNone {
		item.Reason, item.DuplicateOf = reason, owner
		return false, nil
	}
	found := p.probe(key, item.Destination)
	switch {
	case found.err != nil:
		if err := p.folderLost(); err != nil {
			return false, err
		}
		p.fail(item, "check destination", found.err)
		return true, nil
	case !found.exists:
		p.claimed[key] = index
		item.Action = ActionCreate
		return true, nil
	case p.collision != CollisionOverwrite:
		// Rename and Skip never replace a file, so whether an existing file
		// is a source under another name changes nothing they do.
		item.Reason = ReasonExists
		return false, nil
	}
	if reason := p.ownFile(item.Destination, found.mode); reason != ReasonNone {
		item.Reason = reason
		return false, nil
	}
	decision, err := p.replaceable(key, item.Destination)
	if err != nil {
		return false, err
	}
	if decision.reason != ReasonNone {
		item.Reason, item.DuplicateOf = decision.reason, decision.owner
		return false, nil
	}
	p.replaced[decision.id] = index
	p.claimed[key] = index
	item.Action = ActionReplace
	item.replaces = decision.id
	return true, nil
}

// resolve applies the collision policy to an item whose natural destination
// is unavailable: Skip skips it, or defers it to the earlier item that
// plans to create the same name, and Rename and Overwrite search for the
// first numbered name that is unused. Overwrite never replaces a file under
// a numbered name: such a file may be an original the job was never asked
// to replace, which the planner cannot tell from an earlier result. A
// context error, or an output folder that can no longer be reached, is
// returned; a destination failure is attached to the item.
func (p *planner) resolve(index int, item *Item) error {
	if p.collision == CollisionSkip {
		item.Action = ActionSkip
		if item.Reason == ReasonDuplicate {
			item.Action = ActionDefer
		}
		return nil
	}
	base := filepath.Base(item.Destination)
	extension := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, extension)
	// The pattern folds case as PathKey does, so two names that differ only
	// in bytes that are not valid UTF-8, whose numbered names never collide,
	// keep their own searches.
	pattern := foldCase(prefix) + "\x00" + foldCase(extension)
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
		if reason, _ := p.reserved(key); reason != ReasonNone {
			continue
		}
		found := p.probe(key, candidate)
		if found.err != nil {
			// A numbered name only has to be usable, so a name that cannot
			// be checked is passed over, unless the output folder itself
			// can no longer be reached, which fails the job.
			if err := p.folderLost(); err != nil {
				return err
			}
			if _, noted := p.passed[pattern]; !noted {
				p.passed[pattern] = found.err
			}
			continue
		}
		if !found.exists {
			p.next[pattern] = suffix + 1
			p.claimed[key] = index
			item.Destination = candidate
			item.Action = ActionCreate
			return nil
		}
	}
	p.next[pattern] = maxSuffix + 1
	err := fmt.Errorf("%s: %w", base, ErrNoUnusedName)
	if passed := p.passed[pattern]; passed != nil {
		err = fmt.Errorf("%w; a name that could not be checked was passed over: %w", err, passed)
	}
	p.fail(item, "name output file", err)
	return nil
}

func (p *planner) fail(item *Item, op string, err error) {
	item.Action = ActionFail
	item.Err = kindError(KindDestination, op, err)
}

// folderLost is called for each destination name that cannot be checked,
// which may mean the output folder itself can no longer be reached, as when
// its drive or share has gone since Prepare opened it. Every later name
// would then fail too, so it returns the folder's failure as a KindFolder
// error that fails the job once, as an unusable folder does, rather than
// every remaining item failing on its own (UX-010). The folder is checked at
// the first, second, fourth, eighth, and so on name of the job that cannot
// be checked, so names that cannot be checked in a reachable folder cost a
// few checks rather than one each, and a folder that becomes unreachable is
// noticed before the job has met twice the names that could not be checked
// when the folder was last reached.
func (p *planner) folderLost() error {
	p.unchecked++
	if p.unchecked&(p.unchecked-1) != 0 {
		return nil
	}
	if _, err := p.files.Stat(p.folder); err != nil {
		return kindError(KindFolder, "open output folder", err)
	}
	return nil
}

// probe inspects a destination path without following a final link, so a
// link is reported as an entry of its own rather than as its target. Only a
// name missing from the output folder is free: a path whose folder cannot be
// found, such as on a drive or share that has gone, cannot be checked.
func (p *planner) probe(key, path string) probe {
	if cached, ok := p.probes[key]; ok {
		return cached
	}
	var found probe
	info, err := p.files.Lstat(path)
	switch {
	case err == nil:
		found = probe{exists: true, mode: info.Mode()}
	case !fileMissing(err):
		found.err = err
	}
	p.probes[key] = found
	return found
}

// listFile returns how its folder lists the existing entry at path, of the
// given type, when that type may be a file's: a regular file, or an entry
// Lstat reports as irregular. Since Go 1.23, os.Lstat reports a Windows file
// with a reparse point other than a symbolic link as irregular, and that
// includes every file OneDrive keeps in sync; such a file has its data and
// its name where it stands, like any other, and only the listing tells it
// from a junction or another link. An entry of any other type, such as a
// folder or a symbolic link, is returned as no file without being listed.
// This is the one place that decides which entries are files.
func (p *planner) listFile(path string, mode fs.FileMode) (entry, error) {
	if kind := mode.Type(); kind != 0 && kind != fs.ModeIrregular {
		return entry{}, nil
	}
	return p.files.Entry(path)
}

// ownFile decides whether the existing entry at a natural destination, of
// the given type, is the only kind of entry Overwrite replaces: a file its
// folder lists under the destination's own name, compared
// case-insensitively. One listing answers both. It reports ReasonExists for
// a folder, a link, or a junction, which no policy replaces, and for a file
// the name only reaches, as Windows opens a file through its 8.3 short name,
// so PHOTOG~1.png can reach photograph.png. It reports ReasonUnverified when
// the folder cannot list the entry, which then cannot be proven to be
// either, and ReasonNone for the destination's own file.
func (p *planner) ownFile(path string, mode fs.FileMode) Reason {
	listed, err := p.listFile(path, mode)
	switch {
	case err != nil:
		return ReasonUnverified
	case !listed.file || PathKey(listed.name) != PathKey(filepath.Base(path)):
		return ReasonExists
	}
	return ReasonNone
}

// replaceable decides whether an Overwrite job may replace the existing file
// at a natural destination: only when its identity proves it is none of the
// sources and no earlier item replaces it. Only a context error is returned.
func (p *planner) replaceable(key, path string) (verdict, error) {
	found := p.identity(key, path)
	if found.err != nil {
		return verdict{reason: ReasonUnverified, owner: -1}, nil
	}
	source, err := p.isSource(found.id)
	if err != nil {
		return verdict{}, err
	}
	if source {
		return verdict{reason: ReasonSource, owner: -1}, nil
	}
	if p.mayBeUnverified(found.links) {
		return verdict{reason: ReasonUnverified, owner: -1}, nil
	}
	if owner, twice := p.replaced[found.id]; twice {
		return verdict{reason: ReasonDuplicate, owner: owner}, nil
	}
	return verdict{id: found.id, owner: -1}, nil
}

// identity reads the identity of the file or folder at a path, and its
// number of names, once per path key.
func (p *planner) identity(key, path string) identified {
	if cached, ok := p.identities[key]; ok {
		return cached
	}
	var found identified
	found.id, found.links, found.err = p.files.Identity(path)
	p.identities[key] = found
	return found
}

// isSource reports whether the identity belongs to one of the listed paths,
// loading their identities on first use. Once a listed path that may be any
// existing file is found, no existing file can be replaced, so the rest are
// not opened: a file whose identity matches none loaded is renamed as
// ReasonUnverified rather than ReasonSource, which renames it all the same.
func (p *planner) isSource(id fileID) (bool, error) {
	if !p.loaded {
		for _, path := range p.listed {
			if err := p.ctx.Err(); err != nil {
				return false, kindError(KindCanceled, "plan outputs", err)
			}
			sourceID, _, err := p.files.Identity(path)
			switch {
			case err == nil:
				p.sourceIDs[sourceID] = struct{}{}
			case fileMissing(err):
				// No file has the name in its folder any more, so none can
				// be overwritten through it; the path stays protected by its
				// key.
			default:
				p.unreadable(path)
			}
			if p.unverified {
				break
			}
		}
		p.loaded = true
	}
	_, source := p.sourceIDs[id]
	return source, nil
}

// unreadable records a listed path that may exist but whose identity could
// not be read, such as a file the user may not open. When the path names a
// file, one that is no link, in a folder whose identity can be read, the
// file has its name in that folder; otherwise, such as for a drive, share,
// or link that can no longer be reached, the file may be any existing file.
func (p *planner) unreadable(path string) {
	info, err := p.files.Lstat(path)
	if err != nil {
		p.unverified = true
		return
	}
	if info.Mode().Type() != 0 {
		// A regular file is a file without listing it; any other entry is
		// one only when its folder lists it as one.
		listed, err := p.listFile(path, info.Mode())
		if err != nil || !listed.file {
			p.unverified = true
			return
		}
	}
	folder := filepath.Dir(path)
	found := p.identity(PathKey(folder), folder)
	if found.err != nil {
		p.unverified = true
		return
	}
	p.unverifiedFolders[found.id] = struct{}{}
}

// mayBeUnverified reports whether an existing file with the given number of
// names, whose identity matches no listed path, may still be a listed file
// whose own identity could not be read. A file with a single name has it in
// the output folder, so it can be such a file only when one lies in the
// output folder too; a file with more names may be any of them.
func (p *planner) mayBeUnverified(links uint32) bool {
	if p.unverified {
		return true
	}
	if len(p.unverifiedFolders) == 0 {
		return false
	}
	output := p.identity(PathKey(p.folder), p.folder)
	if output.err != nil {
		return true
	}
	if _, held := p.unverifiedFolders[output.id]; held {
		return true
	}
	return links != 1
}

// stem is the source's base file name without its extension.
func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// tooLong reports whether a file name exceeds maxNameLength UTF-16 code
// units. Go holds a Windows name with an unpaired surrogate, which NTFS
// accepts, in WTF-8: the surrogate takes three bytes that are not valid
// UTF-8 but is one code unit, as is any other byte that is not valid UTF-8,
// which Windows receives as U+FFFD.
func tooLong(name string) bool {
	if len(name) <= maxNameLength {
		// Neither UTF-8 nor WTF-8 takes fewer bytes than UTF-16 code units.
		return false
	}
	units := 0
	for rest := name; rest != ""; {
		character, size := utf8.DecodeRuneInString(rest)
		if character == utf8.RuneError && size == 1 && len(rest) >= 3 &&
			rest[0] == 0xED && rest[1]&0xE0 == 0xA0 && rest[2]&0xC0 == 0x80 {
			size = 3
		}
		units += utf16.RuneLen(character)
		if units > maxNameLength {
			return true
		}
		rest = rest[size:]
	}
	return false
}

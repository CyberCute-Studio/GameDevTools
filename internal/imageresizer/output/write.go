package output

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
)

// tempPrefix and tempSuffix frame the names of the temporary files Write
// writes results to in the output folder. Such a file that a process killed
// mid-write left behind holds at most a result, which re-running the job
// writes again, so it is safe to delete. The Screenshot output names its
// temporary files alike.
const (
	tempPrefix = ".gamedevtools-"
	tempSuffix = ".tmp"
)

// asidePrefix starts the name an ActionReplace item's existing file has
// while the result takes its place, followed by random digits and the
// destination's own extension. It is no temporary file's name: a process
// killed between the two renames leaves the user's file under it, and so
// does a failure that cannot give the file its name back, so the file can be
// told from the results' temporary files, opened as the picture it is, and
// renamed back.
const asidePrefix = ".gamedevtools-replaced-"

// tempAttempts bounds each search for an unused name, for a temporary file
// or for the file an ActionReplace item moves aside, so a folder that keeps
// reporting every name as taken cannot keep Write searching forever. Each
// name holds 64 random bits, so a real collision is vanishingly rare.
const tempAttempts = 100

// writeBuffer is how much encoded output Write gathers before it writes to
// the temporary file.
const writeBuffer = 64 << 10

var (
	// ErrNotWritten is the cause of the KindValidation error Write returns
	// for an item that saves no file: one whose action is neither
	// ActionCreate nor ActionReplace, or a nil encode function.
	ErrNotWritten = errors.New("the item does not create or replace a file")
	// ErrDestinationTaken is the cause of the KindChanged error Write returns
	// for an ActionCreate item whose destination, free when the plan was
	// made, has an entry now, such as a file another program wrote there. It
	// is never replaced. A Skip job ends the item Skipped, as Plan.Successor
	// describes; under another policy re-running the job plans around it.
	ErrDestinationTaken = errors.New("a file now has the destination's name, which was free when the job was planned")
	// ErrDestinationChanged is the cause of the KindChanged error Write
	// returns for an ActionReplace item whose destination no longer holds
	// the file the plan proved is none of the sources, unchanged: it was
	// rewritten, moved, deleted and then the name taken, or replaced by
	// another file, a folder, or a link. Nothing is replaced. It is the cause
	// too when another program takes the name in the instant Write has moved
	// that file aside; the file then keeps the .gamedevtools-replaced- name
	// the error gives.
	ErrDestinationChanged = errors.New("the file at the destination changed after the job was planned, so it was not replaced")
	// ErrReadOnly is the cause of the KindWrite error Write returns for an
	// ActionReplace item whose existing file is read-only, which it never
	// replaces. errors.Is reports it as fs.ErrPermission too.
	ErrReadOnly = fmt.Errorf("the existing file at the destination is read-only: %w", fs.ErrPermission)
	// ErrDiskFull is wrapped, together with the operating system's error, by
	// the KindWrite error Write returns when the output drive is full, or the
	// user's storage quota on it is used up.
	ErrDiskFull = errors.New("the output drive is full")
	// ErrLeftBehind is wrapped, together with the operating system's error,
	// by the KindWrite error Write adds when it cannot remove its temporary
	// file, which is then left in the output folder.
	ErrLeftBehind = errors.New("the temporary file could not be removed")
)

// Write saves one item's result at its destination (RSZ-027, NFR-023).
// encode writes the encoded result to the writer it is given, once; Write
// gives it a writer that buffers its output into a new temporary file in the
// output folder, flushes the file to the drive, and renames it to the item's
// Destination only once encode has returned successfully, so no partial file
// ever has the destination's name. It returns the destination once the
// result has its name.
//
// An ActionCreate item's result takes the destination's name only while
// nothing has it: a file another program wrote there after the plan was made
// is never replaced, and the item fails with ErrDestinationTaken, before the
// result is encoded when Item.CheckWrite already finds the file there.
//
// An ActionReplace item replaces only the file the plan proved is none of the
// sources, unchanged (RSZ-026, NFR-020). Item.CheckWrite looks at that file
// before the result is encoded, so an item whose file changed or is
// read-only fails before that work is done. Write then opens the file at the
// destination so that no other program can write to, move, rename, replace,
// or delete it while Write holds it, makes Item.StillReplaceable's check with
// the identity read through that handle, moves the file aside under a name
// that starts with .gamedevtools-replaced- by that same handle, renames the
// result to the destination without replacing anything, and deletes the file
// it moved aside, again by that handle, so that its name is gone even while
// another program that allowed deletion still has the file open. Whatever
// happens at the destination's name meanwhile, the only file Write removes is
// the one it checked, with the content it checked. An atomic rename over the
// destination could not offer that: it replaces whatever has the name at
// that instant, and the check could only precede it. So, for the instant
// between the two renames, the destination's name is free; it never names a
// partial file. When the result cannot take the name, the moved file is
// given its name back, and when another program took the name meanwhile, the
// moved file keeps the name the error gives. A file the plan cleared but that
// is gone by the time Write runs is not needed: the result is created in its
// place, again without replacing anything. A read-only file is never
// replaced (ErrReadOnly), and a file another program holds open for writing,
// or without allowing deletion, cannot be: Write waits a fraction of a second
// for such a program to close it, as one reading it briefly does, and then
// fails the item with the sharing violation. Holding a file does not stop
// another program from changing its attributes: a file marked read-only
// after Write checked it cannot be deleted once the result has its name, so
// it keeps its .gamedevtools-replaced- name, which the KindWrite error Write
// then returns with the destination gives.
//
// A failed or cancelled write removes its temporary file and leaves the
// destination as it was. A process killed while writing can leave a
// temporary file named .gamedevtools-*.tmp in the output folder, never a
// partial file at a destination. One killed between an ActionReplace item's
// renames leaves the file it was replacing under its .gamedevtools-replaced-
// name, which ends in the destination's extension, beside the complete
// result's temporary file.
//
// The context cancels the write before anything is done for it, even before
// the destination is looked at, so a cancelled item is reported as cancelled
// rather than by what that look would find; at encode's next write, which
// fails with the context's error; and once more before the result is renamed
// (RSZ-045). From then on Write finishes.
//
// Failures are *Error values: Item.CheckWrite's error for an item it refuses,
// and KindValidation for a nil encode function (ErrNotWritten); KindCanceled;
// KindChanged for a destination that changed after planning; and KindWrite
// for a failure to create, write, flush, rename, or remove a file, or to
// check the existing one, wrapping the operating system's error, with
// ErrDiskFull when the drive is full or the user's quota on it is used up. A
// failure to create, write, or flush the temporary file names the item's
// destination as well as the temporary file, which the failure removes. An
// error encode returns for a reason other than a failed write to the file,
// such as an encoder's own failure, is returned as it is. A temporary file
// that cannot be removed after a failure adds its own KindWrite error,
// wrapping ErrLeftBehind, joined to the first by errors.Join, so errors.Is
// and errors.As find each. When the result has its name but a file could
// not be removed afterwards, Write returns the destination with a KindWrite
// error that names the file left behind, wrapping ErrLeftBehind when it is
// the temporary file.
func Write(ctx context.Context, item Item, encode func(io.Writer) error) (string, error) {
	return write(ctx, item, encode, writeSteps{})
}

// writeSteps lets tests act between Write's steps, as another program could,
// and fail the ones the real filesystem cannot be made to fail on demand.
// Every field may be nil.
type writeSteps struct {
	// wrap wraps the temporary file's writer.
	wrap func(io.Writer) io.Writer
	// written runs once the result is complete in its temporary file.
	written func()
	// pinned runs once the file an ActionReplace item replaces is held and
	// checked, before it is moved aside.
	pinned func()
	// movedAside runs once that file is moved aside, before the result takes
	// its name.
	movedAside func()
	// claim runs before the result is renamed to its destination; an error
	// it returns fails the rename.
	claim func() error
}

func write(ctx context.Context, item Item, encode func(io.Writer) error, steps writeSteps) (path string, err error) {
	if err := item.checkSaves(); err != nil {
		return "", err
	}
	if encode == nil {
		return "", kindError(KindValidation, "write result", fmt.Errorf("%w: no encoder was given", ErrNotWritten))
	}
	if err := ctx.Err(); err != nil {
		return "", kindError(KindCanceled, "write result", err)
	}
	if err := item.checkDestination(); err != nil {
		return "", err
	}
	temp, err := createTemp(filepath.Dir(item.Destination))
	if err != nil {
		return "", resultFailure("create temporary file", item.Destination, err)
	}
	// Until the result has its name, every way out of Write — a failure, a
	// cancellation, or a panic in encode — removes the temporary file.
	claimed := false
	defer func() {
		if claimed {
			return
		}
		if discardErr := temp.discard(); discardErr != nil {
			err = withFailure(err, notRemoved(discardErr))
		}
	}()
	var file io.Writer = temp
	if steps.wrap != nil {
		file = steps.wrap(temp)
	}
	sink := newResultWriter(ctx, file)
	encodeErr := encode(sink)
	if encodeErr == nil {
		encodeErr = sink.flush()
	}
	switch {
	case sink.err != nil:
		return "", sink.failure(item.Destination)
	case encodeErr != nil:
		return "", encodeErr
	}
	if err := temp.sync(); err != nil {
		return "", resultFailure("flush result", item.Destination, err)
	}
	if steps.written != nil {
		steps.written()
	}
	if err := ctx.Err(); err != nil {
		return "", kindError(KindCanceled, "write result", err)
	}
	if item.Action == ActionReplace {
		claimed, err = replace(temp, item, steps)
	} else {
		claimed, err = create(temp, item.Destination, ErrDestinationTaken, steps)
	}
	if !claimed {
		return "", err
	}
	if finishErr := temp.finish(); finishErr != nil {
		err = withFailure(err, notRemoved(finishErr))
	}
	return item.Destination, err
}

// CheckWrite returns the error Write returns for an item it can tell it
// cannot save before any work is done for it, or nil. A job checks an item
// this way before doing the work for it, and Write checks it before encoding.
// An item whose action is neither ActionCreate nor ActionReplace, as Writes
// reports, saves no file (ErrNotWritten), and no plan gives an item a
// Destination that is not an absolute path, as the zero Item has; both are
// KindValidation errors. An ActionCreate item whose destination has an entry
// now, such as a file another program wrote there after planning, fails as
// Write would fail it when renaming the result (KindChanged,
// ErrDestinationTaken); a failure to look passes, as Write's rename meets
// whatever stands there. For an ActionReplace item, CheckWrite makes
// Item.StillReplaceable's check of the file at the destination, without
// holding it, and fails the item as Write would when that file is no longer
// the planned one, unchanged (KindChanged, ErrDestinationChanged), or is
// read-only (KindWrite, ErrReadOnly), or when it cannot be inspected
// (KindWrite). A planned file deleted since passes, as Write creates the
// result in its place, and so does a file another program has open, which
// it may have closed by the time Write replaces the file. Write makes its
// own check again, holding the file, immediately before it replaces it.
// Outside Windows, where planning never plans a replacement, an
// ActionReplace item fails as Write would fail it.
func (i Item) CheckWrite() error {
	if err := i.checkSaves(); err != nil {
		return err
	}
	return i.checkDestination()
}

// checkDestination is the part of CheckWrite that looks at the destination
// of an item that saves a file.
func (i Item) checkDestination() error {
	if i.Action == ActionReplace {
		return checkReplace(i)
	}
	// Lstat reports any entry, a link or a folder included, as the rename
	// that would give the result its name meets it.
	if _, err := os.Lstat(i.Destination); err == nil {
		return kindError(KindChanged, "check destination", fmt.Errorf("%s: %w", i.Destination, ErrDestinationTaken))
	}
	return nil
}

// checkSaves is the part of CheckWrite that looks at the item alone: that it
// saves a file, at an absolute destination.
func (i Item) checkSaves() error {
	if !i.Writes() {
		return kindError(KindValidation, "write result", fmt.Errorf("%w: its action is %v", ErrNotWritten, i.Action))
	}
	if !filepath.IsAbs(i.Destination) {
		return kindError(KindValidation, "write result", fmt.Errorf("the destination %q is not an absolute path", i.Destination))
	}
	return nil
}

// notRemoved is the KindWrite error for a temporary file that could not be
// removed, marked ErrLeftBehind.
func notRemoved(err error) *Error {
	return kindError(KindWrite, "remove temporary file", fmt.Errorf("%w: %w", ErrLeftBehind, err))
}

// withFailure adds a failure that followed err: the failure alone when there
// was none, so that Write's own error is an *Error, and the two joined
// otherwise.
func withFailure(err error, failure *Error) error {
	if err == nil {
		return failure
	}
	return errors.Join(err, failure)
}

// create gives the complete result the destination's name without replacing
// anything there. changed is the cause reported when the name is taken. It
// reports whether the result has the name.
func create(temp *tempFile, destination string, changed error, steps writeSteps) (bool, error) {
	if err := claim(temp, destination, steps); err != nil {
		if nameTaken(err) {
			return false, kindError(KindChanged, "save result", fmt.Errorf("%s: %w", destination, changed))
		}
		return false, writeFailure("save result", err)
	}
	return true, nil
}

// claim renames the temporary file to the destination without replacing
// anything there.
func claim(temp *tempFile, destination string, steps writeSteps) error {
	if steps.claim != nil {
		if err := steps.claim(); err != nil {
			return err
		}
	}
	return temp.claim(destination)
}

// writeFailure is the KindWrite error for a failed file operation, marked
// ErrDiskFull when the drive is full.
func writeFailure(op string, err error) *Error {
	if diskFull(err) {
		err = fmt.Errorf("%w: %w", ErrDiskFull, err)
	}
	return kindError(KindWrite, op, err)
}

// resultFailure is writeFailure for a failure to create, write, or flush the
// temporary file of the result for destination. The operating system's error
// names only the temporary file, which the user never sees and the failed
// write removes, so the error names the destination too.
func resultFailure(op, destination string, err error) *Error {
	failure := writeFailure(op, err)
	failure.Err = fmt.Errorf("%s: %w", destination, failure.Err)
	return failure
}

// tempName returns a random temporary file name.
func tempName() string {
	return fmt.Sprintf("%s%016x%s", tempPrefix, rand.Uint64(), tempSuffix)
}

// asideName returns a random name for the existing file at an ActionReplace
// item's destination while the result takes its place.
func asideName(destination string) string {
	return fmt.Sprintf("%s%016x%s", asidePrefix, rand.Uint64(), filepath.Ext(destination))
}

// unusedName calls try with a path in the folder for each name that name
// returns, for as long as try fails because an entry already has the name,
// and returns the path try succeeded with; any other failure is returned as
// it is.
func unusedName(folder string, name func() string, try func(path string) error) (string, error) {
	for range tempAttempts {
		path := filepath.Join(folder, name())
		err := try(path)
		if err == nil {
			return path, nil
		}
		if !nameTaken(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("no unused name was found in %s after %d attempts", folder, tempAttempts)
}

// resultWriter is the writer encode is given. It fails encode's first write
// after the context ends, and gathers the rest in a buffer of writeBuffer
// bytes that it writes to the temporary file, remembering the first failure
// of either.
type resultWriter struct {
	ctx      context.Context
	buffer   *bufio.Writer
	err      error
	canceled bool
}

func newResultWriter(ctx context.Context, file io.Writer) *resultWriter {
	return &resultWriter{ctx: ctx, buffer: bufio.NewWriterSize(file, writeBuffer)}
}

func (r *resultWriter) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if err := r.ctx.Err(); err != nil {
		r.err, r.canceled = err, true
		return 0, err
	}
	// The buffer fails only when writing to the file does.
	n, err := r.buffer.Write(p)
	if err != nil {
		r.err = err
	}
	return n, err
}

// flush writes the output still in the buffer to the file.
func (r *resultWriter) flush() error {
	if r.err == nil {
		r.err = r.buffer.Flush()
	}
	return r.err
}

// failure is the error Write reports for the writer's failure to write the
// result for destination.
func (r *resultWriter) failure(destination string) *Error {
	if r.canceled {
		return kindError(KindCanceled, "write result", r.err)
	}
	return resultFailure("write result", destination, r.err)
}

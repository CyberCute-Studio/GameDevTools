//go:build windows

package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// text is an encode function that writes the content.
func text(content string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, content)
		return err
	}
}

// names lists the entries of a folder, sorted.
func names(t *testing.T, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("list %s: %v", folder, err)
	}
	var listed []string
	for _, entry := range entries {
		listed = append(listed, entry.Name())
	}
	return listed
}

func assertNames(t *testing.T, folder string, want ...string) {
	t.Helper()
	if got := names(t, folder); !slices.Equal(got, want) {
		t.Fatalf("%s holds %q, want %q", folder, got, want)
	}
}

// temporaryFiles lists the temporary files Write left in a folder.
func temporaryFiles(t *testing.T, folder string) []string {
	t.Helper()
	var found []string
	for _, name := range names(t, folder) {
		if strings.HasPrefix(name, tempPrefix) && strings.HasSuffix(name, tempSuffix) {
			found = append(found, filepath.Join(folder, name))
		}
	}
	return found
}

// asideFiles lists the files an ActionReplace item moved aside and left in a
// folder.
func asideFiles(t *testing.T, folder string) []string {
	t.Helper()
	var found []string
	for _, name := range names(t, folder) {
		if strings.HasPrefix(name, asidePrefix) {
			found = append(found, filepath.Join(folder, name))
		}
	}
	return found
}

// planOne plans one source into the output folder under the policy and
// returns its item, which must have the wanted action.
func planOne(t *testing.T, out string, policy Collision, source string, want Action) Item {
	t.Helper()
	plan := mustPrepare(t, jobRequest(out, policy, png(source)))
	item := plan.Items[0]
	if item.Action != want {
		t.Fatalf("planned %v (%v, %v), want %v", item.Action, item.Reason, item.Err, want)
	}
	return item
}

// replaceSetup is an output folder holding photo.png, which is no source, and
// a source photo.png in another folder, planned under a confirmed Overwrite
// job to replace it.
type replaceSetup struct {
	out, source, destination string
	item                     Item
}

func newReplaceSetup(t *testing.T) replaceSetup {
	t.Helper()
	setup := replaceSetup{out: t.TempDir(), source: filepath.Join(t.TempDir(), "photo.png")}
	setup.destination = filepath.Join(setup.out, "photo.png")
	writeFile(t, setup.source, "source")
	writeFile(t, setup.destination, "old")
	setup.item = planOne(t, setup.out, CollisionOverwrite, setup.source, ActionReplace)
	return setup
}

// TestWriteCreatesTheResult pins RSZ-027: the result is written through a
// temporary file that is gone once the result has its name, at every size,
// including results larger than Write's buffer.
func TestWriteCreatesTheResult(t *testing.T) {
	for _, size := range []int{0, 1, writeBuffer - 1, writeBuffer, 3*writeBuffer + 17} {
		out := filepath.Join(t.TempDir(), "out")
		source := filepath.Join(t.TempDir(), "photo.png")
		writeFile(t, source, "source")
		item := planOne(t, out, CollisionRename, source, ActionCreate)
		content := strings.Repeat("r", size)
		path, err := Write(context.Background(), item, text(content))
		if err != nil || path != item.Destination {
			t.Fatalf("size %d: Write = %q, %v, want %q", size, path, err, item.Destination)
		}
		if got := readFile(t, path); got != content {
			t.Fatalf("size %d: result holds %d bytes", size, len(got))
		}
		assertNames(t, out, "photo.png")
		if readFile(t, source) != "source" {
			t.Fatal("the source changed")
		}
	}
}

// TestWriteSavesNumberedNames: a renamed item is saved under its numbered
// name beside the existing file it avoided, which is untouched.
func TestWriteSavesNumberedNames(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "photo.png"), "earlier")
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	if _, err := Write(context.Background(), item, text("result")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertNames(t, out, "photo.png", "photo_2.png")
	if readFile(t, filepath.Join(out, "photo.png")) != "earlier" || readFile(t, filepath.Join(out, "photo_2.png")) != "result" {
		t.Fatal("the results are not where they were planned")
	}
}

// TestWriteNeverReplacesAFileThatAppeared: a file another program writes at
// an ActionCreate item's destination after planning — before Write starts,
// while it encodes, or after the result is complete — is never replaced, and
// the temporary file is removed.
func TestWriteNeverReplacesAFileThatAppeared(t *testing.T) {
	for _, when := range []string{"before", "while encoding", "after encoding"} {
		out := t.TempDir()
		item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
		appear := func() { writeFile(t, item.Destination, "another program's") }
		encode := text("result")
		steps := writeSteps{}
		switch when {
		case "before":
			appear()
		case "while encoding":
			encode = func(w io.Writer) error {
				appear()
				return text("result")(w)
			}
		case "after encoding":
			steps.written = appear
		}
		path, err := write(context.Background(), item, encode, steps)
		assertKind(t, err, KindChanged)
		if path != "" || !errors.Is(err, ErrDestinationTaken) {
			t.Fatalf("%s: Write = %q, %v, want ErrDestinationTaken", when, path, err)
		}
		if readFile(t, item.Destination) != "another program's" {
			t.Fatalf("%s: the file that appeared was replaced", when)
		}
		assertNames(t, out, "photo.png")
	}
}

// TestWriteChecksATakenNameBeforeEncoding: an ActionCreate item whose name a
// file or a folder took after planning fails with ErrDestinationTaken before
// anything is encoded, as Item.CheckWrite reports too, and what took the name
// is kept.
func TestWriteChecksATakenNameBeforeEncoding(t *testing.T) {
	takers := map[string]func(t *testing.T, path string){
		"file": func(t *testing.T, path string) { writeFile(t, path, "another program's") },
		"folder": func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, take := range takers {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
			if err := item.CheckWrite(); err != nil {
				t.Fatalf("CheckWrite of a free name: %v", err)
			}
			take(t, item.Destination)
			checked := item.CheckWrite()
			assertKind(t, checked, KindChanged)
			if !errors.Is(checked, ErrDestinationTaken) {
				t.Fatalf("CheckWrite = %v, want ErrDestinationTaken", checked)
			}
			encoded := false
			path, err := Write(context.Background(), item, func(w io.Writer) error {
				encoded = true
				return text("result")(w)
			})
			assertKind(t, err, KindChanged)
			if path != "" || encoded || !errors.Is(err, ErrDestinationTaken) {
				t.Fatalf("Write = %q, %v, encoded %v, want ErrDestinationTaken before encoding", path, err, encoded)
			}
			assertNames(t, out, "photo.png")
		})
	}
}

// TestWriteRemovesTheTemporaryFileWhenEncodingFails pins RSZ-027 and NFR-023
// for the encoder's own failures: its error is returned as it is, and
// neither a partial result nor its temporary file is left, even when the
// encoder panics.
func TestWriteRemovesTheTemporaryFileWhenEncodingFails(t *testing.T) {
	broken := errors.New("encoder broke")
	out := t.TempDir()
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	path, err := Write(context.Background(), item, func(w io.Writer) error {
		w.Write(bytes.Repeat([]byte("p"), 3*writeBuffer))
		return broken
	})
	if path != "" || err != broken {
		t.Fatalf("Write = %q, %v, want the encoder's error as it is", path, err)
	}
	assertNames(t, out)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the encoder's panic was swallowed")
			}
		}()
		Write(context.Background(), item, func(w io.Writer) error {
			w.Write(bytes.Repeat([]byte("p"), 3*writeBuffer))
			panic("encoder panicked")
		})
	}()
	assertNames(t, out)
}

// failAfter fails every write past a byte budget with an operating system
// error, as a drive does when it fills.
type failAfter struct {
	w      io.Writer
	budget int
	err    error
}

func (f *failAfter) Write(p []byte) (int, error) {
	if len(p) > f.budget {
		n, _ := f.w.Write(p[:f.budget])
		f.budget = 0
		return n, &fs.PathError{Op: "write", Path: "result", Err: f.err}
	}
	f.budget -= len(p)
	return f.w.Write(p)
}

// TestWriteReportsWriteFailures pins RSZ-030 and AC-R14: a failed write
// leaves no partial result and no temporary file, and the error says why —
// a full drive or a used-up quota as ErrDiskFull, a denied write as
// fs.ErrPermission — and names the destination the result was for, not only
// the temporary file it removed, even when the encoder reports a failure of
// its own after meeting it.
func TestWriteReportsWriteFailures(t *testing.T) {
	cases := []struct {
		errno windows.Errno
		want  error
	}{
		{windows.ERROR_DISK_FULL, ErrDiskFull},
		{windows.ERROR_HANDLE_DISK_FULL, ErrDiskFull},
		{windows.ERROR_DISK_QUOTA_EXCEEDED, ErrDiskFull},
		{windows.ERROR_ACCESS_DENIED, fs.ErrPermission},
	}
	for _, tc := range cases {
		for _, ownError := range []bool{false, true} {
			out := t.TempDir()
			item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
			steps := writeSteps{wrap: func(w io.Writer) io.Writer { return &failAfter{w: w, budget: writeBuffer + 5, err: tc.errno} }}
			path, err := write(context.Background(), item, func(w io.Writer) error {
				_, err := w.Write(bytes.Repeat([]byte("p"), 4*writeBuffer))
				if ownError {
					return errors.New("the encoder's own account of it")
				}
				return err
			}, steps)
			assertKind(t, err, KindWrite)
			if path != "" || !errors.Is(err, tc.want) || !errors.Is(err, tc.errno) || !strings.Contains(err.Error(), item.Destination) {
				t.Fatalf("%v: Write = %q, %v, want %v naming %s", tc.errno, path, err, tc.want, item.Destination)
			}
			assertNames(t, out)
		}
	}
	if diskFull(windows.ERROR_NOT_ENOUGH_QUOTA) {
		t.Fatal("a process's own resources running out is reported as a full drive")
	}
}

// TestResultFailureNamesTheDestination: a failure of the temporary file, such
// as a flush that cannot be made to fail on demand, names the destination the
// result was for and keeps the disk-full mark.
func TestResultFailureNamesTheDestination(t *testing.T) {
	failure := resultFailure("flush result", `C:\Out\photo.png`, &fs.PathError{Op: "flush", Path: `C:\Out\.gamedevtools-0.tmp`, Err: windows.ERROR_DISK_FULL})
	if failure.Kind != KindWrite || !errors.Is(failure, ErrDiskFull) || !strings.Contains(failure.Error(), `C:\Out\photo.png`) {
		t.Fatalf("resultFailure = %v", failure)
	}
}

// TestWriteObservesCancellation pins RSZ-045 at the item level: a context
// cancelled before the write, while it encodes, or once the result is
// complete but before it is renamed saves nothing and removes the temporary
// file. While it encodes, the encoder's next write fails, even one the
// buffer would take without writing to the file, so the encoder stops there
// rather than at the next full buffer.
func TestWriteObservesCancellation(t *testing.T) {
	for _, when := range []string{"before", "while encoding", "while encoding little", "before renaming"} {
		out := t.TempDir()
		item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
		ctx, cancel := context.WithCancel(context.Background())
		encode := text("result")
		steps := writeSteps{}
		switch when {
		case "before":
			cancel()
		case "while encoding":
			encode = func(w io.Writer) error {
				w.Write(bytes.Repeat([]byte("p"), 2*writeBuffer))
				cancel()
				_, err := w.Write(bytes.Repeat([]byte("p"), 2*writeBuffer))
				return err
			}
		case "while encoding little":
			encode = func(w io.Writer) error {
				w.Write([]byte("p"))
				cancel()
				_, err := w.Write([]byte("p"))
				if !errors.Is(err, context.Canceled) {
					t.Errorf("the write after the cancellation returned %v, want the context's error", err)
				}
				return err
			}
		case "before renaming":
			steps.written = cancel
		}
		path, err := write(ctx, item, encode, steps)
		cancel()
		assertKind(t, err, KindCanceled)
		if path != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: Write = %q, %v", when, path, err)
		}
		assertNames(t, out)
	}

	// A cancelled ActionReplace item is reported as cancelled, not by what
	// the look at its file would find, here that it was marked read-only.
	setup := newReplaceSetup(t)
	if err := os.Chmod(setup.destination, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(setup.destination, 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Write(ctx, setup.item, text("new"))
	assertKind(t, err, KindCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replacement: Write = %v, want the cancellation", err)
	}
}

// TestCheckWritePassesAFileDeletedWhileLooked: nothing holds the file
// Item.CheckWrite looks at, so another program can delete it between the open
// and the listing of its entry. That is a planned file gone since, which
// passes as Write creates the result in its place, not a destination that
// changed.
func TestCheckWritePassesAFileDeletedWhileLooked(t *testing.T) {
	setup := newReplaceSetup(t)
	deleting := faultyFiles{entry: func(path string) (entry, error) {
		if err := os.Remove(path); err != nil {
			t.Fatalf("delete the looked-at file: %v", err)
		}
		listed, err := findEntry(path)
		if err == nil {
			t.Skip("deleting the open file kept its name until it was closed")
		}
		return listed, err
	}}
	if err := inspectReplaced(setup.item, deleting); err != nil {
		t.Fatalf("a file deleted while looked at fails the item: %v", err)
	}
	if _, err := Write(context.Background(), setup.item, text("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if readFile(t, setup.destination) != "new" {
		t.Fatal("the result was not created in the deleted file's place")
	}
}

func TestWriteRejectsItemsThatSaveNothing(t *testing.T) {
	out := t.TempDir()
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	for _, action := range []Action{ActionSkip, ActionFail, ActionDefer, Action(9)} {
		other := item
		other.Action = action
		if other.Writes() {
			t.Fatalf("%v.Writes() = true", action)
		}
		_, err := Write(context.Background(), other, text("result"))
		assertKind(t, err, KindValidation)
		if !errors.Is(err, ErrNotWritten) {
			t.Fatalf("%v: error = %v, want ErrNotWritten", action, err)
		}
	}
	_, err := Write(context.Background(), item, nil)
	assertKind(t, err, KindValidation)
	assertNames(t, out)
}

// TestWriteRejectsItemsWithoutADestination: an item no plan gives, such as
// the zero Item, whose action is ActionCreate but whose destination is no
// absolute path, is refused before any file is created, rather than written
// to a temporary file in the working folder.
func TestWriteRejectsItemsWithoutADestination(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)
	for _, item := range []Item{{}, {Action: ActionCreate, Destination: "photo.png"}, {Action: ActionReplace, Destination: `out\photo.png`}} {
		encoded := false
		_, err := Write(context.Background(), item, func(w io.Writer) error {
			encoded = true
			return text("result")(w)
		})
		assertKind(t, err, KindValidation)
		if encoded || item.CheckWrite() == nil {
			t.Fatalf("%q: the item was encoded, or CheckWrite accepts it", item.Destination)
		}
		assertNames(t, working)
	}
}

// TestWriteReportsAFolderThatIsGone: an output folder removed after planning
// fails the item as a write failure that errors.Is reports as not existing,
// naming the destination rather than only the temporary file it could not
// create.
func TestWriteReportsAFolderThatIsGone(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	_, err := Write(context.Background(), item, text("result"))
	assertKind(t, err, KindWrite)
	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), item.Destination) {
		t.Fatalf("error = %v, want a not-exist error naming %s", err, item.Destination)
	}
}

// TestWriteKeepsNamesWindowsWouldShorten: a folder named with a trailing
// space, which only an extended-length path keeps, receives its results under
// that name rather than in the folder its shortened name names.
func TestWriteKeepsNamesWindowsWouldShorten(t *testing.T) {
	parent := t.TempDir()
	out := `\\?\` + filepath.Join(parent, "Photos ")
	// The test's own cleanup cannot remove a name Windows shortens, so this
	// one, registered after it, runs first, whether or not the test passes.
	t.Cleanup(func() {
		os.Remove(`\\?\` + filepath.Join(parent, "Photos ", "photo.png"))
		os.Remove(out)
	})
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	if _, err := Write(context.Background(), item, text("result")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertNames(t, parent, "Photos ")
	if readFile(t, `\\?\`+filepath.Join(parent, "Photos ", "photo.png")) != "result" {
		t.Fatal("the result is not in the folder named with the space")
	}
}

// TestWriteHandlesLongPaths: a destination longer than the 260 characters of
// a classic Windows path is created and replaced through its extended-length
// path.
func TestWriteHandlesLongPaths(t *testing.T) {
	out := t.TempDir()
	for len(out) < 300 {
		out = filepath.Join(out, strings.Repeat("deep", 10))
	}
	source := filepath.Join(t.TempDir(), "photo.png")
	writeFile(t, source, "source")
	item := planOne(t, out, CollisionRename, source, ActionCreate)
	if _, err := Write(context.Background(), item, text("first")); err != nil {
		t.Fatalf("create: %v", err)
	}
	item = planOne(t, out, CollisionOverwrite, source, ActionReplace)
	if _, err := Write(context.Background(), item, text("second")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if readFile(t, item.Destination) != "second" {
		t.Fatal("the long-path result was not replaced")
	}
	assertNames(t, out, "photo.png")
}

// TestWriteReplacesTheConfirmedFile pins AC-R10: a confirmed Overwrite job
// replaces the existing file that is no source, leaving neither the old file
// nor any temporary file behind.
func TestWriteReplacesTheConfirmedFile(t *testing.T) {
	setup := newReplaceSetup(t)
	path, err := Write(context.Background(), setup.item, text("new"))
	if err != nil || path != setup.destination {
		t.Fatalf("Write = %q, %v", path, err)
	}
	if readFile(t, setup.destination) != "new" || readFile(t, setup.source) != "source" {
		t.Fatal("the destination was not replaced, or the source changed")
	}
	assertNames(t, setup.out, "photo.png")
}

// TestWriteReplacesOnlyTheDestinationsName: replacing a file with another
// name elsewhere replaces only the destination's name; the other name keeps
// the old content, as a rename over the destination would leave it.
func TestWriteReplacesOnlyTheDestinationsName(t *testing.T) {
	out := t.TempDir()
	destination := filepath.Join(out, "photo.png")
	writeFile(t, destination, "old")
	elsewhere := filepath.Join(t.TempDir(), "kept.png")
	if err := os.Link(destination, elsewhere); err != nil {
		t.Skipf("hard-link: %v", err)
	}
	item := planOne(t, out, CollisionOverwrite, filepath.Join(t.TempDir(), "photo.png"), ActionReplace)
	if _, err := Write(context.Background(), item, text("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if readFile(t, destination) != "new" || readFile(t, elsewhere) != "old" {
		t.Fatal("the file's other name changed")
	}
	assertNames(t, out, "photo.png")
}

// TestWriteCreatesWhereTheReplacedFileIsGone: when the file the plan cleared
// for replacement was deleted meanwhile, the result is created at its name.
func TestWriteCreatesWhereTheReplacedFileIsGone(t *testing.T) {
	setup := newReplaceSetup(t)
	if err := os.Remove(setup.destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(context.Background(), setup.item, text("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if readFile(t, setup.destination) != "new" {
		t.Fatal("the result was not created")
	}
	assertNames(t, setup.out, "photo.png")
}

// TestWriteNeverReplacesAChangedDestination pins RSZ-026 and NFR-020 against
// changes made after planning: a source moved or linked to the destination's
// name, a rewritten file, and a folder or junction put there are never
// replaced, and nothing of the result is left.
func TestWriteNeverReplacesAChangedDestination(t *testing.T) {
	changes := map[string]func(t *testing.T, setup replaceSetup) string{
		"source moved there": func(t *testing.T, setup replaceSetup) string {
			os.Remove(setup.destination)
			if err := os.Rename(setup.source, setup.destination); err != nil {
				t.Fatal(err)
			}
			return "source"
		},
		"source linked there": func(t *testing.T, setup replaceSetup) string {
			os.Remove(setup.destination)
			if err := os.Link(setup.source, setup.destination); err != nil {
				t.Skipf("hard-link: %v", err)
			}
			return "source"
		},
		"rewritten": func(t *testing.T, setup replaceSetup) string {
			writeFile(t, setup.destination, "rewritten by another program")
			return "rewritten by another program"
		},
		"rewritten at the same size": func(t *testing.T, setup replaceSetup) string {
			writeFile(t, setup.destination, "olx")
			// A program that rewrites the file after the plan was made stamps
			// a later write time, but here the rewrite can fall within the
			// tick of the system clock that stamped the plan's, so the
			// later time is given as the later rewrite would give it.
			later := time.Now().Add(time.Second)
			if err := os.Chtimes(setup.destination, later, later); err != nil {
				t.Fatal(err)
			}
			return "olx"
		},
		"name gained": func(t *testing.T, setup replaceSetup) string {
			if err := os.Link(setup.destination, filepath.Join(t.TempDir(), "gained.png")); err != nil {
				t.Skipf("hard-link: %v", err)
			}
			return "old"
		},
		"folder": func(t *testing.T, setup replaceSetup) string {
			os.Remove(setup.destination)
			if err := os.Mkdir(setup.destination, 0o755); err != nil {
				t.Fatal(err)
			}
			return ""
		},
		"junction": func(t *testing.T, setup replaceSetup) string {
			os.Remove(setup.destination)
			mustJunction(t, setup.destination, filepath.Dir(setup.source))
			return ""
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			setup := newReplaceSetup(t)
			want := change(t, setup)
			path, err := Write(context.Background(), setup.item, text("new"))
			assertKind(t, err, KindChanged)
			if path != "" || !errors.Is(err, ErrDestinationChanged) {
				t.Fatalf("Write = %q, %v, want ErrDestinationChanged", path, err)
			}
			if info, statErr := os.Lstat(setup.destination); statErr != nil {
				t.Fatalf("the destination is gone: %v", statErr)
			} else if info.Mode().IsRegular() && readFile(t, setup.destination) != want {
				t.Fatal("the changed destination was replaced")
			}
			if temps := append(temporaryFiles(t, setup.out), asideFiles(t, setup.out)...); len(temps) != 0 {
				t.Fatalf("temporary files were left: %q", temps)
			}
		})
	}
}

// TestWriteNeverReplacesAReadOnlyFile: a read-only file is never replaced,
// which errors.Is reports as a permission failure.
func TestWriteNeverReplacesAReadOnlyFile(t *testing.T) {
	setup := newReplaceSetup(t)
	if err := os.Chmod(setup.destination, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(setup.destination, 0o644)
	// Marking the file read-only does not change its identity or stamp.
	if !setup.item.StillReplaceable() {
		t.Fatal("the read-only file no longer counts as the planned file")
	}
	_, err := Write(context.Background(), setup.item, text("new"))
	assertKind(t, err, KindWrite)
	if !errors.Is(err, ErrReadOnly) || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error = %v, want ErrReadOnly", err)
	}
	if readFile(t, setup.destination) != "old" {
		t.Fatal("the read-only file was replaced")
	}
	assertNames(t, setup.out, "photo.png")
}

// TestWriteChecksTheReplacedFileBeforeEncoding: an ActionReplace item whose
// file is read-only or changed since planning fails before anything is
// encoded, with the error Write would meet when replacing it, which
// Item.CheckWrite returns too; a file another program has open passes the
// early look, as it may be closed by the time Write holds it.
func TestWriteChecksTheReplacedFileBeforeEncoding(t *testing.T) {
	changes := map[string]struct {
		change func(t *testing.T, setup replaceSetup)
		kind   Kind
		want   error
	}{
		"read-only": {func(t *testing.T, setup replaceSetup) {
			if err := os.Chmod(setup.destination, 0o444); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(setup.destination, 0o644) })
		}, KindWrite, ErrReadOnly},
		"rewritten": {func(t *testing.T, setup replaceSetup) {
			writeFile(t, setup.destination, "rewritten by another program")
		}, KindChanged, ErrDestinationChanged},
		"source moved there": {func(t *testing.T, setup replaceSetup) {
			os.Remove(setup.destination)
			if err := os.Rename(setup.source, setup.destination); err != nil {
				t.Fatal(err)
			}
		}, KindChanged, ErrDestinationChanged},
	}
	for name, tc := range changes {
		t.Run(name, func(t *testing.T) {
			setup := newReplaceSetup(t)
			tc.change(t, setup)
			checked := setup.item.CheckWrite()
			assertKind(t, checked, tc.kind)
			if !errors.Is(checked, tc.want) {
				t.Fatalf("CheckWrite = %v, want %v", checked, tc.want)
			}
			encoded := false
			path, err := Write(context.Background(), setup.item, func(w io.Writer) error {
				encoded = true
				return text("new")(w)
			})
			assertKind(t, err, tc.kind)
			if path != "" || encoded || !errors.Is(err, tc.want) {
				t.Fatalf("Write = %q, %v, encoded %v, want %v before encoding", path, err, encoded, tc.want)
			}
		})
	}
	setup := newReplaceSetup(t)
	handle := openWithoutSharingDelete(t, setup.destination)
	defer windows.CloseHandle(handle)
	if err := setup.item.CheckWrite(); err != nil {
		t.Fatalf("CheckWrite refuses a file another program has open: %v", err)
	}
}

// TestWriteKeepsAFileMarkedReadOnlyWhileHeld: holding a file does not stop
// another program from marking it read-only after Write checked it. Such a
// file cannot be deleted once the result has its name, so it is kept under
// its .gamedevtools-replaced- name with its content, and Write returns the
// destination with a KindWrite error that names it.
func TestWriteKeepsAFileMarkedReadOnlyWhileHeld(t *testing.T) {
	setup := newReplaceSetup(t)
	steps := writeSteps{pinned: func() {
		if err := os.Chmod(setup.destination, 0o444); err != nil {
			t.Fatalf("mark the held file read-only: %v", err)
		}
	}}
	path, err := write(context.Background(), setup.item, text("new"), steps)
	aside := asideFiles(t, setup.out)
	for _, file := range aside {
		t.Cleanup(func() { os.Chmod(file, 0o644) })
	}
	assertKind(t, err, KindWrite)
	if path != setup.destination || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Write = %q, %v, want the destination and a permission failure", path, err)
	}
	if readFile(t, setup.destination) != "new" {
		t.Fatal("the result does not have the destination's name")
	}
	if len(aside) != 1 || readFile(t, aside[0]) != "old" || !strings.Contains(err.Error(), aside[0]) {
		t.Fatalf("files moved aside = %q after %v, want the replaced file, named in the error", aside, err)
	}
}

// openToRead opens a file for reading while allowing every other program to
// read, write, and delete it, as a picture viewer, the search indexer, or a
// virus scanner does.
func openToRead(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, shareAll, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return handle
}

// TestWriteRemovesTheNamesOfFilesOthersHaveOpen: a file Write deletes loses
// its name at once even while another program that allowed deletion still
// has it open, rather than staying listed, under a name no program can open,
// rename, or delete, until that program closes it; the program still reads
// the content it opened. This holds for the file a replacement moved aside
// and for the temporary file of a failed write.
func TestWriteRemovesTheNamesOfFilesOthersHaveOpen(t *testing.T) {
	t.Run("moved aside", func(t *testing.T) {
		setup := newReplaceSetup(t)
		viewer := openToRead(t, setup.destination)
		defer windows.CloseHandle(viewer)
		if _, err := Write(context.Background(), setup.item, text("new")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		assertNames(t, setup.out, "photo.png")
		content := make([]byte, 8)
		var n uint32
		if err := windows.ReadFile(viewer, content, &n, nil); err != nil || string(content[:n]) != "old" {
			t.Fatalf("the viewer read %q, %v, want the content it opened", content[:n], err)
		}
	})
	t.Run("temporary", func(t *testing.T) {
		out := t.TempDir()
		item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
		broken := errors.New("encoder broke")
		var indexer windows.Handle
		_, err := Write(context.Background(), item, func(w io.Writer) error {
			temps := temporaryFiles(t, out)
			if len(temps) != 1 {
				t.Fatalf("temporary files while encoding: %q", temps)
			}
			indexer = openToRead(t, temps[0])
			return broken
		})
		if indexer != 0 {
			defer windows.CloseHandle(indexer)
		}
		if err != broken {
			t.Fatalf("Write = %v, want the encoder's error", err)
		}
		assertNames(t, out)
	})
}

// TestCheckHeldFindsARetargetedPath: the folders that hold a held file can be
// neither renamed nor removed, so a folder that no longer lists the held file
// means a link in the destination's path now leads elsewhere, and the
// destination changed.
func TestCheckHeldFindsARetargetedPath(t *testing.T) {
	real, elsewhere := t.TempDir(), t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	mustJunction(t, out, real)
	writeFile(t, filepath.Join(real, "photo.png"), "old")
	item := planOne(t, out, CollisionOverwrite, filepath.Join(t.TempDir(), "photo.png"), ActionReplace)
	held, err := hold(item.Destination)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(held)
	if err := os.Rename(real, real+"-moved"); err == nil {
		t.Fatal("the held file's folder was renamed")
	}
	if err := os.Remove(out); err != nil {
		t.Fatalf("remove the junction: %v", err)
	}
	mustJunction(t, out, elsewhere)
	err = checkHeld(&heldFile{fileSystem: osFiles{}, handle: held}, item)
	assertKind(t, err, KindChanged)
	if !errors.Is(err, ErrDestinationChanged) {
		t.Fatalf("error = %v, want ErrDestinationChanged", err)
	}
}

// TestUnusedNameGivesUp: a folder that reports every name as taken ends the
// search after tempAttempts names, with an error that names the folder and
// suits either search.
func TestUnusedNameGivesUp(t *testing.T) {
	tries := 0
	_, err := unusedName(`C:\Out`, tempName, func(string) error {
		tries++
		return windows.ERROR_FILE_EXISTS
	})
	if err == nil || tries != tempAttempts || !strings.Contains(err.Error(), `C:\Out`) || strings.Contains(err.Error(), "temporary") {
		t.Fatalf("after %d tries: %v", tries, err)
	}
}

// openWithoutSharingDelete opens a file the way many programs do, allowing
// others to read and write it but not to delete or rename it.
func openWithoutSharingDelete(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return handle
}

// openForWriting opens a file for writing while allowing every other program
// to read, write, and delete it, as a program that keeps a file open and
// saves into it may.
func openForWriting(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, windows.GENERIC_WRITE, shareAll, nil, windows.OPEN_EXISTING, 0, 0)
}

// TestWriteFailsForAFileInUse: a file another program holds open without
// allowing deletion, or holds open for writing even while it allows
// deletion, cannot be replaced; the item fails with the sharing violation
// and the file keeps its content.
func TestWriteFailsForAFileInUse(t *testing.T) {
	opens := map[string]func(t *testing.T, path string) windows.Handle{
		"without sharing delete": openWithoutSharingDelete,
		"for writing": func(t *testing.T, path string) windows.Handle {
			handle, err := openForWriting(path)
			if err != nil {
				t.Fatalf("open %s: %v", path, err)
			}
			return handle
		},
	}
	for name, open := range opens {
		t.Run(name, func(t *testing.T) {
			setup := newReplaceSetup(t)
			handle := open(t, setup.destination)
			defer windows.CloseHandle(handle)
			_, err := Write(context.Background(), setup.item, text("new"))
			assertKind(t, err, KindWrite)
			if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
				t.Fatalf("error = %v, want a sharing violation", err)
			}
			if readFile(t, setup.destination) != "old" {
				t.Fatal("the file in use was replaced")
			}
			assertNames(t, setup.out, "photo.png")
		})
	}
}

// TestWriteWaitsForABriefOpen: a file another program opens without allowing
// deletion only briefly, as a thumbnail generator or a sync client reading it
// may, is replaced once that program closes it, rather than failing the item
// with the sharing violation.
func TestWriteWaitsForABriefOpen(t *testing.T) {
	setup := newReplaceSetup(t)
	handle := openWithoutSharingDelete(t, setup.destination)
	closed := make(chan struct{})
	steps := writeSteps{written: func() {
		// The result is complete; the program closes the file well within
		// the time Write waits for it.
		time.AfterFunc(40*time.Millisecond, func() {
			windows.CloseHandle(handle)
			close(closed)
		})
	}}
	path, err := write(context.Background(), setup.item, text("new"), steps)
	<-closed
	if err != nil || path != setup.destination {
		t.Fatalf("Write = %q, %v, want the file replaced once it was closed", path, err)
	}
	if readFile(t, setup.destination) != "new" {
		t.Fatal("the file was not replaced")
	}
	assertNames(t, setup.out, "photo.png")
}

// TestWriteHoldsTheContentItChecked: once Write has checked the file it
// replaces, no other program can open it for writing until the file is
// gone, so the content Write deletes is the content it checked, and a
// program's newer content is never deleted along with it.
func TestWriteHoldsTheContentItChecked(t *testing.T) {
	setup := newReplaceSetup(t)
	steps := writeSteps{pinned: func() {
		handle, err := openForWriting(setup.destination)
		if err == nil {
			windows.WriteFile(handle, []byte("newer"), nil, nil)
			windows.CloseHandle(handle)
			t.Fatal("another program opened the held file for writing")
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("opening the held file for writing failed with %v, want a sharing violation", err)
		}
	}}
	if _, err := write(context.Background(), setup.item, text("new"), steps); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if readFile(t, setup.destination) != "new" {
		t.Fatal("the result does not have the destination's name")
	}
	assertNames(t, setup.out, "photo.png")
}

// TestCheckHeldTellsFailuresFromChanges: a held file whose folder entry
// cannot be listed, as when listing is denied or a network share fails, is a
// write failure carrying the system's error, not a destination that changed;
// a path that no longer leads to the held file changed, as
// TestCheckHeldFindsARetargetedPath shows.
func TestCheckHeldTellsFailuresFromChanges(t *testing.T) {
	setup := newReplaceSetup(t)
	held, err := hold(setup.destination)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(held)
	check := func(entry func(string) (entry, error)) error {
		return checkHeld(&heldFile{fileSystem: faultyFiles{entry: entry}, handle: held}, setup.item)
	}
	if err := check(nil); err != nil {
		t.Fatalf("the unchanged file fails its check: %v", err)
	}
	for _, errno := range []windows.Errno{windows.ERROR_ACCESS_DENIED, windows.ERROR_NETNAME_DELETED} {
		err := check(func(path string) (entry, error) {
			return entry{}, &fs.PathError{Op: "find", Path: path, Err: errno}
		})
		assertKind(t, err, KindWrite)
		if !errors.Is(err, errno) || errors.Is(err, ErrDestinationChanged) {
			t.Fatalf("%v: error = %v, want the write failure alone", errno, err)
		}
	}
	err = check(func(path string) (entry, error) {
		return entry{}, &fs.PathError{Op: "find", Path: path, Err: windows.ERROR_PATH_NOT_FOUND}
	})
	assertKind(t, err, KindChanged)
	if !errors.Is(err, ErrDestinationChanged) {
		t.Fatalf("error = %v, want ErrDestinationChanged", err)
	}
}

// TestWriteHoldsTheFileItReplaces proves the replacement removes only the
// file it checked: while Write holds that file, no other program can move a
// source over it, delete it, rename it, or touch either temporary file. Once
// the file is moved aside its name is free, and a program that takes it then
// keeps it, as TestWriteKeepsTheReplacedFileWhenTheNameIsTaken shows.
func TestWriteHoldsTheFileItReplaces(t *testing.T) {
	setup := newReplaceSetup(t)
	attempt := func(stage string) {
		for _, temp := range append(temporaryFiles(t, setup.out), asideFiles(t, setup.out)...) {
			if err := os.Remove(temp); err == nil {
				t.Fatalf("%s: the temporary file %s was deleted", stage, temp)
			}
			if err := os.Rename(setup.source, temp); err == nil {
				t.Fatalf("%s: a source was moved over the temporary file %s", stage, temp)
			}
		}
	}
	steps := writeSteps{
		pinned: func() {
			attempt("held")
			if err := os.Rename(setup.source, setup.destination); err == nil {
				t.Fatal("a source was moved over the held file")
			}
			if err := os.Remove(setup.destination); err == nil {
				t.Fatal("the held file was deleted")
			}
			if err := os.Rename(setup.destination, filepath.Join(setup.out, "moved.png")); err == nil {
				t.Fatal("the held file was renamed")
			}
			if temps := temporaryFiles(t, setup.out); len(temps) != 1 {
				t.Fatalf("temporary files while held: %q, want the result's alone", temps)
			}
		},
		movedAside: func() {
			temps, aside := temporaryFiles(t, setup.out), asideFiles(t, setup.out)
			if len(temps) != 1 || len(aside) != 1 || filepath.Ext(aside[0]) != ".png" {
				t.Fatalf("files once moved aside: %q and %q, want the result's and the held file, as a .png", temps, aside)
			}
			attempt("moved aside")
		},
	}
	if _, err := write(context.Background(), setup.item, text("new"), steps); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if readFile(t, setup.destination) != "new" || readFile(t, setup.source) != "source" {
		t.Fatal("the replacement went wrong")
	}
	assertNames(t, setup.out, "photo.png")
}

// TestWriteGivesTheNameBackWhenTheResultCannotTakeIt: when renaming the
// result to the destination fails after the replaced file was moved aside,
// the file gets its name back and keeps its content.
func TestWriteGivesTheNameBackWhenTheResultCannotTakeIt(t *testing.T) {
	setup := newReplaceSetup(t)
	steps := writeSteps{claim: func() error { return windows.ERROR_ACCESS_DENIED }}
	path, err := write(context.Background(), setup.item, text("new"), steps)
	assertKind(t, err, KindWrite)
	if path != "" || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Write = %q, %v", path, err)
	}
	if readFile(t, setup.destination) != "old" {
		t.Fatal("the existing file did not get its name back")
	}
	assertNames(t, setup.out, "photo.png")
	// The file is the planned one still, so a later attempt replaces it.
	if !setup.item.StillReplaceable() {
		t.Fatal("restoring the name changed the file")
	}
}

// TestWriteKeepsTheReplacedFileWhenTheNameIsTaken: when another program
// takes the destination's name in the instant it is free — writing a file
// there, or moving a source there — the destination changed: the result
// never replaces that file, its temporary file is removed, and the file the
// result was to replace is kept under its .gamedevtools-replaced- name, which
// no temporary file has and the error names.
func TestWriteKeepsTheReplacedFileWhenTheNameIsTaken(t *testing.T) {
	for _, taker := range []string{"new file", "source"} {
		setup := newReplaceSetup(t)
		want := "another program's"
		steps := writeSteps{movedAside: func() {
			if taker == "source" {
				want = "source"
				if err := os.Rename(setup.source, setup.destination); err != nil {
					t.Fatal(err)
				}
				return
			}
			writeFile(t, setup.destination, want)
		}}
		path, err := write(context.Background(), setup.item, text("new"), steps)
		assertKind(t, err, KindChanged)
		if path != "" || !errors.Is(err, ErrDestinationChanged) {
			t.Fatalf("%s: Write = %q, %v, want ErrDestinationChanged", taker, path, err)
		}
		if readFile(t, setup.destination) != want {
			t.Fatalf("%s: the file that took the name was replaced", taker)
		}
		aside := asideFiles(t, setup.out)
		if len(aside) != 1 || readFile(t, aside[0]) != "old" || !strings.Contains(err.Error(), aside[0]) {
			t.Fatalf("%s: files moved aside = %q after %v, want the replaced file alone, named in the error", taker, aside, err)
		}
		if temps := temporaryFiles(t, setup.out); len(temps) != 0 {
			t.Fatalf("%s: temporary files were left: %q", taker, temps)
		}
	}
}

// TestWriteKeepsTheReplacedFileWhenTheNameIsTakenAfterAFailure: when the
// result cannot take the free name for a reason of its own, and another
// program takes the name before the moved file gets it back, the destination
// changed all the same: the item fails with ErrDestinationChanged, keeping
// the result's own failure, and the moved file keeps its
// .gamedevtools-replaced- name, which the error gives.
func TestWriteKeepsTheReplacedFileWhenTheNameIsTakenAfterAFailure(t *testing.T) {
	setup := newReplaceSetup(t)
	steps := writeSteps{claim: func() error {
		writeFile(t, setup.destination, "another program's")
		return windows.ERROR_ACCESS_DENIED
	}}
	path, err := write(context.Background(), setup.item, text("new"), steps)
	assertKind(t, err, KindChanged)
	if path != "" || !errors.Is(err, ErrDestinationChanged) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("Write = %q, %v, want ErrDestinationChanged with the result's own failure", path, err)
	}
	if readFile(t, setup.destination) != "another program's" {
		t.Fatal("the file that took the name was replaced")
	}
	aside := asideFiles(t, setup.out)
	if len(aside) != 1 || readFile(t, aside[0]) != "old" || !strings.Contains(err.Error(), aside[0]) {
		t.Fatalf("files moved aside = %q after %v, want the replaced file alone, named in the error", aside, err)
	}
	if temps := temporaryFiles(t, setup.out); len(temps) != 0 {
		t.Fatalf("temporary files were left: %q", temps)
	}
}

// TestWithFailure: a failure that follows none is Write's *Error as it is,
// and one that follows another is joined to it, so both are found.
func TestWithFailure(t *testing.T) {
	removal := kindError(KindWrite, "remove temporary file", windows.ERROR_ACCESS_DENIED)
	var writeErr *Error
	if err := withFailure(nil, removal); !errors.As(err, &writeErr) || err != error(removal) {
		t.Fatalf("withFailure(nil, removal) = %#v, want the *Error itself", err)
	}
	broken := errors.New("encoder broke")
	if err := withFailure(broken, removal); !errors.Is(err, broken) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("withFailure(broken, removal) = %v, want both", err)
	}
}

func TestKindPresentation(t *testing.T) {
	for kind, want := range map[Kind]string{KindWrite: "write", KindChanged: "changed"} {
		if got := kind.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", kind, got, want)
		}
	}
}

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
// a full drive as ErrDiskFull, a denied write as fs.ErrPermission — even
// when the encoder reports a failure of its own after meeting it.
func TestWriteReportsWriteFailures(t *testing.T) {
	cases := []struct {
		errno windows.Errno
		want  error
	}{
		{windows.ERROR_DISK_FULL, ErrDiskFull},
		{windows.ERROR_HANDLE_DISK_FULL, ErrDiskFull},
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
			if path != "" || !errors.Is(err, tc.want) || !errors.Is(err, tc.errno) {
				t.Fatalf("%v: Write = %q, %v, want %v", tc.errno, path, err, tc.want)
			}
			assertNames(t, out)
		}
	}
}

// TestWriteObservesCancellation pins RSZ-045 at the item level: a context
// cancelled before the write, while it encodes, or once the result is
// complete but before it is renamed saves nothing and removes the temporary
// file.
func TestWriteObservesCancellation(t *testing.T) {
	for _, when := range []string{"before", "while encoding", "before renaming"} {
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

// TestWriteReportsAFolderThatIsGone: an output folder removed after planning
// fails the item as a write failure that errors.Is reports as not existing.
func TestWriteReportsAFolderThatIsGone(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	_, err := Write(context.Background(), item, text("result"))
	assertKind(t, err, KindWrite)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want a not-exist error", err)
	}
}

// TestWriteKeepsNamesWindowsWouldShorten: a folder named with a trailing
// space, which only an extended-length path keeps, receives its results under
// that name rather than in the folder its shortened name names.
func TestWriteKeepsNamesWindowsWouldShorten(t *testing.T) {
	parent := t.TempDir()
	out := `\\?\` + filepath.Join(parent, "Photos ")
	item := planOne(t, out, CollisionRename, filepath.Join(t.TempDir(), "photo.png"), ActionCreate)
	if _, err := Write(context.Background(), item, text("result")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertNames(t, parent, "Photos ")
	if readFile(t, `\\?\`+filepath.Join(parent, "Photos ", "photo.png")) != "result" {
		t.Fatal("the result is not in the folder named with the space")
	}
	// The test's cleanup cannot remove a name Windows shortens.
	os.Remove(`\\?\` + filepath.Join(parent, "Photos ", "photo.png"))
	os.Remove(out)
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
			if temps := temporaryFiles(t, setup.out); len(temps) != 0 {
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

// TestWriteFailsForAFileInUse: a file another program holds open without
// allowing deletion cannot be replaced; the item fails with the sharing
// violation and the file keeps its content.
func TestWriteFailsForAFileInUse(t *testing.T) {
	setup := newReplaceSetup(t)
	handle := openWithoutSharingDelete(t, setup.destination)
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
}

// TestWriteHoldsTheFileItReplaces proves the replacement removes only the
// file it checked: while Write holds that file, no other program can move a
// source over it, delete it, rename it, or touch either temporary file. Once
// the file is moved aside its name is free, and a program that takes it then
// keeps it, as TestWriteKeepsTheReplacedFileWhenTheNameIsTaken shows.
func TestWriteHoldsTheFileItReplaces(t *testing.T) {
	setup := newReplaceSetup(t)
	attempt := func(stage string) {
		for _, temp := range temporaryFiles(t, setup.out) {
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
			if temps := temporaryFiles(t, setup.out); len(temps) != 2 {
				t.Fatalf("temporary files once moved aside: %q, want the result's and the held file", temps)
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
// there, or moving a source there — the result never replaces that file, and
// the file the result was to replace is kept under its temporary name, which
// the error names.
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
		writeErr := assertKind(t, err, KindWrite)
		if path != "" || writeErr.Op != "restore existing file" {
			t.Fatalf("%s: Write = %q, %v", taker, path, err)
		}
		if readFile(t, setup.destination) != want {
			t.Fatalf("%s: the file that took the name was replaced", taker)
		}
		temps := temporaryFiles(t, setup.out)
		if len(temps) != 1 || readFile(t, temps[0]) != "old" || !strings.Contains(err.Error(), temps[0]) {
			t.Fatalf("%s: temporary files = %q after %v, want the replaced file alone, named in the error", taker, temps, err)
		}
	}
}

func TestKindPresentation(t *testing.T) {
	for kind, want := range map[Kind]string{KindWrite: "write", KindChanged: "changed"} {
		if got := kind.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", kind, got, want)
		}
	}
}

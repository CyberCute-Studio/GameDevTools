//go:build windows

// The planner's rules are Windows rules: paths compare case-insensitively
// and Overwrite replaces only files whose identity it can read, which it can
// only on Windows. prepare_other_test.go pins what the package does
// elsewhere.

package output

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

var policies = []Collision{CollisionRename, CollisionSkip, CollisionOverwrite}

// jobRequest builds a request for the folder and policy with Keep Source
// Format, confirming Overwrite jobs.
func jobRequest(folder string, policy Collision, sources ...Source) Request {
	return Request{
		Sources:          sources,
		Folder:           folder,
		Format:           FormatKeepSource,
		Collision:        policy,
		ConfirmOverwrite: policy == CollisionOverwrite,
	}
}

func png(path string) Source  { return Source{Path: path, Format: decode.FormatPNG} }
func jpeg(path string) Source { return Source{Path: path, Format: decode.FormatJPEG} }

// writeFile creates a file, and its folder, with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create folder for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustPrepare(t *testing.T, request Request) Plan {
	t.Helper()
	plan, err := Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	checkPlanShape(t, plan, request)
	return plan
}

func checkPlanShape(t *testing.T, plan Plan, request Request) {
	t.Helper()
	if len(plan.Items) != len(request.Sources) {
		t.Fatalf("plan has %d items for %d sources", len(plan.Items), len(request.Sources))
	}
	for index, item := range plan.Items {
		if item.Source != request.Sources[index].Path {
			t.Fatalf("item %d source = %q, want %q", index, item.Source, request.Sources[index].Path)
		}
	}
}

func assertKind(t *testing.T, err error, want Kind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want kind %v", want)
	}
	var planErr *Error
	if !errors.As(err, &planErr) {
		t.Fatalf("error = %T (%v), want *Error with kind %v", err, err, want)
	}
	if planErr.Kind != want {
		t.Fatalf("error kind = %v, want %v (error: %v)", planErr.Kind, want, err)
	}
	return planErr
}

// expect is the planned outcome of one item: the destination's base name,
// the action, and the reason.
type expect struct {
	name   string
	action Action
	reason Reason
}

func assertItems(t *testing.T, plan Plan, want []expect) {
	t.Helper()
	if len(plan.Items) != len(want) {
		t.Fatalf("plan has %d items, want %d", len(plan.Items), len(want))
	}
	for index, item := range plan.Items {
		got := expect{name: filepath.Base(item.Destination), action: item.Action, reason: item.Reason}
		if got != want[index] {
			t.Errorf("item %d (%s) = %+v, want %+v (err: %v)", index, item.Source, got, want[index], item.Err)
		}
		if filepath.Dir(item.Destination) != plan.Folder {
			t.Errorf("item %d destination %q is outside the output folder %q", index, item.Destination, plan.Folder)
		}
		if (item.Action == ActionFail) != (item.Err != nil) {
			t.Errorf("item %d action %v carries error %v", index, item.Action, item.Err)
		}
	}
}

// materialize writes every file the plan creates or replaces, as the job
// would, so a later plan sees the earlier results. A created file must not
// exist yet.
func materialize(t *testing.T, plan Plan, run string) {
	t.Helper()
	for index, item := range plan.Items {
		content := []byte(fmt.Sprintf("%s result %d", run, index))
		switch item.Action {
		case ActionCreate:
			file, err := os.OpenFile(item.Destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				t.Fatalf("create planned %s: %v", item.Destination, err)
			}
			if _, err := file.Write(content); err != nil {
				t.Fatalf("write planned %s: %v", item.Destination, err)
			}
			if err := file.Close(); err != nil {
				t.Fatalf("close planned %s: %v", item.Destination, err)
			}
		case ActionReplace:
			if _, err := os.Lstat(item.Destination); err != nil {
				t.Fatalf("replaced destination %s does not exist: %v", item.Destination, err)
			}
			if err := os.WriteFile(item.Destination, content, 0o644); err != nil {
				t.Fatalf("replace planned %s: %v", item.Destination, err)
			}
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestOutputFormatAndExtension(t *testing.T) {
	tests := []struct {
		option  Format
		content decode.Format
		want    decode.Format
		ext     string
	}{
		{FormatKeepSource, decode.FormatPNG, decode.FormatPNG, ".png"},
		{FormatKeepSource, decode.FormatJPEG, decode.FormatJPEG, ".jpg"},
		{FormatPNG, decode.FormatJPEG, decode.FormatPNG, ".png"},
		{FormatPNG, decode.FormatPNG, decode.FormatPNG, ".png"},
		{FormatJPEG, decode.FormatPNG, decode.FormatJPEG, ".jpg"},
		{FormatJPEG, decode.FormatJPEG, decode.FormatJPEG, ".jpg"},
	}
	for _, test := range tests {
		got := OutputFormat(test.option, test.content)
		if got != test.want {
			t.Errorf("OutputFormat(%q, %v) = %v, want %v", test.option, test.content, got, test.want)
		}
		if ext := Extension(got); ext != test.ext {
			t.Errorf("Extension(%v) = %q, want %q", got, ext, test.ext)
		}
	}
	if ext := Extension(decode.FormatUnknown); ext != "" {
		t.Errorf("Extension(unknown) = %q, want none", ext)
	}
}

func TestPathKey(t *testing.T) {
	if PathKey(`C:\In\photo.PNG`) != PathKey(`c:\in\.\PHOTO.png`) {
		t.Fatal("paths differing in letter case and a . element have different keys")
	}
	if PathKey(`C:\In\a.png`) == PathKey(`C:\In\b.png`) {
		t.Fatal("two files share a key")
	}
}

// TestPrepareNamesDestinationsAfterSources pins RSZ-022 and RSZ-023: each
// result keeps its source's base name, with the extension of the format it
// is encoded in, and Keep Source Format follows the detected content rather
// than the source's extension.
func TestPrepareNamesDestinationsAfterSources(t *testing.T) {
	in := t.TempDir()
	sources := []Source{
		png(filepath.Join(in, "hero.png")),
		jpeg(filepath.Join(in, "Sky.JPEG")),
		jpeg(filepath.Join(in, "map.tiles.jpg")),
		jpeg(filepath.Join(in, "mislabeled.png")),
	}
	tests := []struct {
		format  Format
		names   []string
		formats []decode.Format
	}{
		{
			format:  FormatKeepSource,
			names:   []string{"hero.png", "Sky.jpg", "map.tiles.jpg", "mislabeled.jpg"},
			formats: []decode.Format{decode.FormatPNG, decode.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG},
		},
		{
			format:  FormatPNG,
			names:   []string{"hero.png", "Sky.png", "map.tiles.png", "mislabeled.png"},
			formats: []decode.Format{decode.FormatPNG, decode.FormatPNG, decode.FormatPNG, decode.FormatPNG},
		},
		{
			format:  FormatJPEG,
			names:   []string{"hero.jpg", "Sky.jpg", "map.tiles.jpg", "mislabeled.jpg"},
			formats: []decode.Format{decode.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG},
		},
	}
	for _, test := range tests {
		t.Run(string(test.format), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			request := jobRequest(out, CollisionRename, sources...)
			request.Format = test.format
			plan := mustPrepare(t, request)
			if plan.Folder != out {
				t.Fatalf("Folder = %q, want %q", plan.Folder, out)
			}
			want := make([]expect, len(test.names))
			for index, name := range test.names {
				want[index] = expect{name: name, action: ActionCreate, reason: ReasonNone}
			}
			assertItems(t, plan, want)
			for index, item := range plan.Items {
				if item.Format != test.formats[index] {
					t.Errorf("item %d format = %v, want %v", index, item.Format, test.formats[index])
				}
				if item.Renamed() {
					t.Errorf("item %d reports a rename for its natural name", index)
				}
			}
		})
	}
}

// TestPrepareCreatesTheOutputFolder pins RSZ-030: a missing output folder,
// including missing parents, is created before planning.
func TestPrepareCreatesTheOutputFolder(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a", "b", "Resized")
	plan := mustPrepare(t, jobRequest(out+string(filepath.Separator), CollisionRename,
		png(filepath.Join(t.TempDir(), "x.png"))))
	if plan.Folder != out {
		t.Fatalf("Folder = %q, want the cleaned %q", plan.Folder, out)
	}
	info, err := os.Stat(out)
	if err != nil || !info.IsDir() {
		t.Fatalf("output folder after Prepare: %v, %v", info, err)
	}
	assertItems(t, plan, []expect{{"x.png", ActionCreate, ReasonNone}})
}

func TestPrepareRejectsInvalidRequests(t *testing.T) {
	in := t.TempDir()
	root := filepath.VolumeName(in) + string(filepath.Separator)
	valid := png(filepath.Join(in, "hero.png"))
	tests := []struct {
		name   string
		mutate func(*Request)
		want   error
	}{
		{name: "relative folder", mutate: func(r *Request) { r.Folder = "Resized" }, want: ErrFolderNotAbsolute},
		{name: "empty folder", mutate: func(r *Request) { r.Folder = "" }, want: ErrFolderEmpty},
		{name: "NUL in folder", mutate: func(r *Request) { r.Folder += "\x00" }, want: ErrFolderText},
		// The persisted options refuse the replacement character a lossy
		// conversion leaves, and so does the planner.
		{name: "replacement character in folder", mutate: func(r *Request) { r.Folder += string(utf8.RuneError) }, want: ErrFolderText},
		{name: "unknown policy", mutate: func(r *Request) { r.Collision = "delete" }},
		{name: "empty policy", mutate: func(r *Request) { r.Collision = "" }},
		{name: "unknown format", mutate: func(r *Request) { r.Format = "gif" }},
		{name: "empty format", mutate: func(r *Request) { r.Format = "" }},
		{name: "no sources", mutate: func(r *Request) { r.Sources = nil }, want: ErrNoSources},
		{name: "relative source", mutate: func(r *Request) { r.Sources = append(r.Sources, png("hero.png")) }},
		{name: "empty source", mutate: func(r *Request) { r.Sources = append(r.Sources, png("")) }},
		{name: "NUL in source", mutate: func(r *Request) { r.Sources = append(r.Sources, png(valid.Path+"\x00.png")) }},
		{name: "root source", mutate: func(r *Request) { r.Sources = append(r.Sources, png(root)) }},
		{name: "unknown source format", mutate: func(r *Request) {
			r.Sources = append(r.Sources, Source{Path: filepath.Join(in, "a.png"), Format: decode.FormatUnknown})
		}},
		{name: "relative protected path", mutate: func(r *Request) { r.Protected = []string{"hero.gif"} }},
		{name: "NUL in protected path", mutate: func(r *Request) { r.Protected = []string{valid.Path + "\x00"} }},
		{name: "root protected path", mutate: func(r *Request) { r.Protected = []string{root} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			request := jobRequest(out, CollisionRename, valid)
			test.mutate(&request)
			plan, err := Prepare(context.Background(), request)
			assertKind(t, err, KindValidation)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if plan.Items != nil {
				t.Fatalf("a rejected request returned %d items", len(plan.Items))
			}
			if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("a rejected request touched the output folder: %v", statErr)
			}
		})
	}
}

// TestPrepareRequiresOverwriteConfirmation pins the RSZ-025 Overwrite rule:
// an Overwrite job is refused without its explicit confirmation, before the
// filesystem is touched.
func TestPrepareRequiresOverwriteConfirmation(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	source := png(filepath.Join(t.TempDir(), "hero.png"))
	request := jobRequest(out, CollisionOverwrite, source)
	request.ConfirmOverwrite = false
	_, err := Prepare(context.Background(), request)
	assertKind(t, err, KindConfirmation)
	if !errors.Is(err, ErrOverwriteUnconfirmed) {
		t.Fatalf("error = %v, want ErrOverwriteUnconfirmed", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("an unconfirmed job touched the output folder: %v", statErr)
	}

	request.ConfirmOverwrite = true
	assertItems(t, mustPrepare(t, request), []expect{{"hero.png", ActionCreate, ReasonNone}})

	// The confirmation means nothing to the other policies.
	for _, policy := range []Collision{CollisionRename, CollisionSkip} {
		for _, confirmed := range []bool{false, true} {
			request := jobRequest(out, policy, source)
			request.ConfirmOverwrite = confirmed
			assertItems(t, mustPrepare(t, request), []expect{{"hero.png", ActionCreate, ReasonNone}})
		}
	}
}

// faultyFiles wraps the real filesystem with injectable failures.
type faultyFiles struct {
	osFiles
	mkdirAll   func(path string) error
	stat       func(path string) (fs.FileInfo, error)
	lstat      func(path string) (fs.FileInfo, error)
	identity   func(path string) (fileID, uint32, error)
	storedFile func(path string) (bool, error)
}

func (f faultyFiles) MkdirAll(path string) error {
	if f.mkdirAll != nil {
		return f.mkdirAll(path)
	}
	return f.osFiles.MkdirAll(path)
}

func (f faultyFiles) Stat(path string) (fs.FileInfo, error) {
	if f.stat != nil {
		return f.stat(path)
	}
	return f.osFiles.Stat(path)
}

func (f faultyFiles) Lstat(path string) (fs.FileInfo, error) {
	if f.lstat != nil {
		return f.lstat(path)
	}
	return f.osFiles.Lstat(path)
}

func (f faultyFiles) Identity(path string) (fileID, uint32, error) {
	if f.identity != nil {
		return f.identity(path)
	}
	return f.osFiles.Identity(path)
}

func (f faultyFiles) StoredFile(path string) (bool, error) {
	if f.storedFile != nil {
		return f.storedFile(path)
	}
	return f.osFiles.StoredFile(path)
}

// fakeInfo describes a file that exists only in a test.
type fakeInfo struct {
	name string
	mode fs.FileMode
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

func TestPrepareReportsUnusableFolders(t *testing.T) {
	source := png(filepath.Join(t.TempDir(), "hero.png"))

	// A file standing where the folder, or a folder holding it, should be
	// is reported as ErrNotFolder, as the sentinel documents.
	t.Run("blocked by a file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-folder")
		writeFile(t, blocker, "file")
		_, err := Prepare(context.Background(), jobRequest(blocker, CollisionRename, source))
		assertKind(t, err, KindFolder)
		if !errors.Is(err, ErrNotFolder) {
			t.Fatalf("error = %v, want ErrNotFolder", err)
		}
		if readFile(t, blocker) != "file" {
			t.Fatal("the blocking file was changed")
		}
	})

	t.Run("blocked parent", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-folder")
		writeFile(t, blocker, "file")
		_, err := Prepare(context.Background(), jobRequest(filepath.Join(blocker, "out"), CollisionRename, source))
		assertKind(t, err, KindFolder)
		if !errors.Is(err, ErrNotFolder) {
			t.Fatalf("error = %v, want ErrNotFolder", err)
		}
	})

	t.Run("permission", func(t *testing.T) {
		files := faultyFiles{mkdirAll: func(path string) error {
			return &fs.PathError{Op: "mkdir", Path: path, Err: fs.ErrPermission}
		}}
		_, err := prepare(context.Background(), jobRequest(filepath.Join(t.TempDir(), "out"), CollisionRename, source), files)
		assertKind(t, err, KindFolder)
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("error = %v, want a permission failure", err)
		}
		if errors.Is(err, ErrNotFolder) {
			t.Fatalf("a permission failure is reported as ErrNotFolder: %v", err)
		}
	})

	t.Run("not a folder", func(t *testing.T) {
		files := faultyFiles{
			mkdirAll: func(string) error { return nil },
			stat:     func(path string) (fs.FileInfo, error) { return fakeInfo{name: filepath.Base(path)}, nil },
		}
		_, err := prepare(context.Background(), jobRequest(filepath.Join(t.TempDir(), "out"), CollisionRename, source), files)
		assertKind(t, err, KindFolder)
		if !errors.Is(err, ErrNotFolder) {
			t.Fatalf("error = %v, want ErrNotFolder", err)
		}
	})

	t.Run("unreadable folder", func(t *testing.T) {
		files := faultyFiles{stat: func(path string) (fs.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
		}}
		_, err := prepare(context.Background(), jobRequest(t.TempDir(), CollisionRename, source), files)
		assertKind(t, err, KindFolder)
	})
}

// TestPrepareSameNameFromDifferentFolders pins RSZ-024 and AC-R08: sources
// that would produce the same destination never share it. The first keeps
// the name; Rename and Overwrite number the others deterministically, and
// Skip defers them to it. Names compare case-insensitively, as Windows paths
// do.
func TestPrepareSameNameFromDifferentFolders(t *testing.T) {
	sources := []Source{
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x.png")),
		jpeg(filepath.Join(t.TempDir(), "X.PNG")),
	}
	want := map[Collision][]expect{
		CollisionRename: {
			{"x.png", ActionCreate, ReasonNone},
			{"x_2.png", ActionCreate, ReasonDuplicate},
			{"X.jpg", ActionCreate, ReasonNone},
		},
		CollisionSkip: {
			{"x.png", ActionCreate, ReasonNone},
			{"x.png", ActionDefer, ReasonDuplicate},
			{"X.jpg", ActionCreate, ReasonNone},
		},
		CollisionOverwrite: {
			{"x.png", ActionCreate, ReasonNone},
			{"x_2.png", ActionCreate, ReasonDuplicate},
			{"X.jpg", ActionCreate, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			out := t.TempDir()
			request := jobRequest(out, policy, sources...)
			plan := mustPrepare(t, request)
			assertItems(t, plan, want[policy])
			for index, owner := range []int{-1, 0, -1} {
				if got := plan.Items[index].DuplicateOf; got != owner {
					t.Fatalf("item %d DuplicateOf = %d, want %d", index, got, owner)
				}
			}

			// With PNG output, the third source's name differs from the
			// others only in letter case.
			request.Format = FormatPNG
			plan = mustPrepare(t, request)
			if policy == CollisionSkip {
				assertItems(t, plan, []expect{
					{"x.png", ActionCreate, ReasonNone},
					{"x.png", ActionDefer, ReasonDuplicate},
					{"X.png", ActionDefer, ReasonDuplicate},
				})
				return
			}
			assertItems(t, plan, []expect{
				{"x.png", ActionCreate, ReasonNone},
				{"x_2.png", ActionCreate, ReasonDuplicate},
				{"X_3.png", ActionCreate, ReasonDuplicate},
			})
		})
	}
}

// TestPlanSuccessor: a Skip job defers a duplicate to the earlier item that
// creates the name, rather than skipping it outright, so when that item
// does not write it, the plan names the duplicate that creates it instead,
// in job order (RSZ-024). An item skipped because the name exists waits on
// nothing.
func TestPlanSuccessor(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "z.png"), "earlier")
	sources := []Source{
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "y.png")),
		png(filepath.Join(t.TempDir(), "X.png")),
		png(filepath.Join(t.TempDir(), "Y.png")),
		png(filepath.Join(t.TempDir(), "z.png")),
	}
	plan := mustPrepare(t, jobRequest(out, CollisionSkip, sources...))
	assertItems(t, plan, []expect{
		{"x.png", ActionCreate, ReasonNone},
		{"x.png", ActionDefer, ReasonDuplicate},
		{"y.png", ActionCreate, ReasonNone},
		{"X.png", ActionDefer, ReasonDuplicate},
		{"Y.png", ActionDefer, ReasonDuplicate},
		{"z.png", ActionSkip, ReasonExists},
	})
	for index, owner := range []int{-1, 0, -1, 0, 2, -1} {
		if got := plan.Items[index].DuplicateOf; got != owner {
			t.Errorf("item %d DuplicateOf = %d, want %d", index, got, owner)
		}
	}
	successors := map[int]int{0: 1, 1: 3, 2: 4}
	// The links are made with the plan, so Successor answers in constant
	// time without walking the items, however many fail.
	linked := plan
	linked.Items = nil
	for index := -1; index <= len(plan.Items); index++ {
		want, wantOK := successors[index]
		for _, plan := range []Plan{plan, linked} {
			if got, ok := plan.Successor(index); got != want || ok != wantOK {
				t.Errorf("Successor(%d) = %d, %v, want %d, %v", index, got, ok, want, wantOK)
			}
		}
	}

	// Rename and Overwrite give every duplicate a name of its own, so no
	// item waits on another.
	for _, policy := range []Collision{CollisionRename, CollisionOverwrite} {
		plan := mustPrepare(t, jobRequest(t.TempDir(), policy, sources...))
		for index := range plan.Items {
			if next, ok := plan.Successor(index); ok {
				t.Errorf("%s: Successor(%d) = %d, want none", policy, index, next)
			}
		}
	}
}

// TestPrepareExistingDestinations pins RSZ-025 and AC-R09/AC-R10: an
// existing destination is renamed around, skipped, or — only under a
// confirmed Overwrite and only when it is a file — replaced.
func TestPrepareExistingDestinations(t *testing.T) {
	in := t.TempDir()
	sources := []Source{
		png(filepath.Join(in, "x.png")),
		png(filepath.Join(in, "folder.png")),
		png(filepath.Join(in, "new.png")),
	}
	want := map[Collision][]expect{
		CollisionRename: {
			{"x_2.png", ActionCreate, ReasonExists},
			{"folder_2.png", ActionCreate, ReasonExists},
			{"new.png", ActionCreate, ReasonNone},
		},
		CollisionSkip: {
			{"x.png", ActionSkip, ReasonExists},
			{"folder.png", ActionSkip, ReasonExists},
			{"new.png", ActionCreate, ReasonNone},
		},
		CollisionOverwrite: {
			{"x.png", ActionReplace, ReasonNone},
			{"folder_2.png", ActionCreate, ReasonExists},
			{"new.png", ActionCreate, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			out := t.TempDir()
			writeFile(t, filepath.Join(out, "x.png"), "earlier")
			if err := os.Mkdir(filepath.Join(out, "folder.png"), 0o755); err != nil {
				t.Fatalf("create folder destination: %v", err)
			}
			plan := mustPrepare(t, jobRequest(out, policy, sources...))
			assertItems(t, plan, want[policy])
			if got := plan.Items[0].Renamed(); got != (policy == CollisionRename) {
				t.Fatalf("Renamed() = %v under %s", got, policy)
			}
			if readFile(t, filepath.Join(out, "x.png")) != "earlier" {
				t.Fatal("planning changed an existing destination")
			}
		})
	}
}

// TestPrepareTreatsStoredFilesAsFiles: since Go 1.23, os.Lstat reports a
// file with a reparse point other than a symbolic link, such as every file
// OneDrive keeps in sync, as irregular. Such a file is still a file that a
// confirmed Overwrite replaces; an irregular entry that is a link, a
// folder, or cannot be inspected is not.
func TestPrepareTreatsStoredFilesAsFiles(t *testing.T) {
	irregular := func(mode fs.FileMode) func(string) (fs.FileInfo, error) {
		return func(path string) (fs.FileInfo, error) {
			info, err := os.Lstat(path)
			if err != nil {
				return nil, err
			}
			return fakeInfo{name: info.Name(), mode: mode}, nil
		}
	}
	tests := []struct {
		name   string
		mode   fs.FileMode
		stored func(string) (bool, error)
		want   map[Collision]expect
	}{
		{
			name:   "placeholder",
			mode:   fs.ModeIrregular,
			stored: func(string) (bool, error) { return true, nil },
			want: map[Collision]expect{
				CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
				CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
				CollisionOverwrite: {"x.png", ActionReplace, ReasonNone},
			},
		},
		{
			name:   "link",
			mode:   fs.ModeIrregular,
			stored: func(string) (bool, error) { return false, nil },
			want: map[Collision]expect{
				CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
				CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
				CollisionOverwrite: {"x_2.png", ActionCreate, ReasonExists},
			},
		},
		{
			name: "uninspectable",
			mode: fs.ModeIrregular,
			stored: func(path string) (bool, error) {
				return false, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
			},
			want: map[Collision]expect{
				CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
				CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
				CollisionOverwrite: {"x_2.png", ActionCreate, ReasonExists},
			},
		},
		{
			name: "irregular folder",
			mode: fs.ModeIrregular | fs.ModeDir,
			stored: func(string) (bool, error) {
				return true, errors.New("a folder must not be inspected as a stored file")
			},
			want: map[Collision]expect{
				CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
				CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
				CollisionOverwrite: {"x_2.png", ActionCreate, ReasonExists},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := t.TempDir()
			destination := filepath.Join(out, "x.png")
			writeFile(t, destination, "earlier")
			for _, policy := range policies {
				inspections := 0
				files := faultyFiles{
					lstat: func(path string) (fs.FileInfo, error) {
						if strings.EqualFold(path, destination) {
							return irregular(test.mode)(path)
						}
						return os.Lstat(path)
					},
					storedFile: func(path string) (bool, error) {
						inspections++
						return test.stored(path)
					},
				}
				plan, err := prepare(context.Background(), jobRequest(out, policy, png(filepath.Join(t.TempDir(), "x.png"))), files)
				if err != nil {
					t.Fatalf("%s: prepare: %v", policy, err)
				}
				assertItems(t, plan, []expect{test.want[policy]})
				// Only Overwrite replaces, so only it asks how an irregular
				// file is stored, and never for a folder.
				want := 0
				if policy == CollisionOverwrite && !test.mode.IsDir() {
					want = 1
				}
				if inspections != want {
					t.Fatalf("%s inspected the reparse point %d times, want %d", policy, inspections, want)
				}
			}
		})
	}
}

// TestPrepareNeverTargetsSources pins RSZ-026 and AC-R07 with the output
// folder holding the sources themselves: no policy plans onto a source.
// Rename and Overwrite rename the result, and Skip skips it.
func TestPrepareNeverTargetsSources(t *testing.T) {
	want := map[Collision][]expect{
		CollisionRename: {
			{"a_2.png", ActionCreate, ReasonSource},
			{"b_2.jpg", ActionCreate, ReasonSource},
			{"c.jpg", ActionCreate, ReasonNone},
		},
		CollisionSkip: {
			{"a.png", ActionSkip, ReasonSource},
			{"b.jpg", ActionSkip, ReasonSource},
			{"c.jpg", ActionCreate, ReasonNone},
		},
		CollisionOverwrite: {
			{"a_2.png", ActionCreate, ReasonSource},
			{"b_2.jpg", ActionCreate, ReasonSource},
			{"c.jpg", ActionCreate, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			folder := t.TempDir()
			sources := []Source{
				png(filepath.Join(folder, "a.png")),
				jpeg(filepath.Join(folder, "b.jpg")),
				jpeg(filepath.Join(folder, "c.jpeg")),
			}
			for _, source := range sources {
				writeFile(t, source.Path, "original "+filepath.Base(source.Path))
			}
			plan := mustPrepare(t, jobRequest(folder, policy, sources...))
			assertItems(t, plan, want[policy])
			materialize(t, plan, "run")
			for _, source := range sources {
				if got := readFile(t, source.Path); got != "original "+filepath.Base(source.Path) {
					t.Fatalf("source %s was overwritten: %q", source.Path, got)
				}
			}

			// A re-run of an Overwrite job replaces the result with its
			// natural name, and saves a renamed result under the next unused
			// name: the planner cannot tell an earlier result from an
			// original that has a numbered name.
			if policy == CollisionOverwrite {
				again := mustPrepare(t, jobRequest(folder, policy, sources...))
				assertItems(t, again, []expect{
					{"a_3.png", ActionCreate, ReasonSource},
					{"b_3.jpg", ActionCreate, ReasonSource},
					{"c.jpg", ActionReplace, ReasonNone},
				})
			}
		})
	}
}

// TestPrepareProtectsListedSourcesItDoesNotProcess pins RSZ-026 and NFR-020
// for files listed as sources that the job does not resize, such as one too
// large to process: no policy plans a result onto one, by its natural or a
// numbered name, whether or not it still exists.
func TestPrepareProtectsListedSourcesItDoesNotProcess(t *testing.T) {
	want := map[Collision][]expect{
		CollisionRename: {
			{"hero_3.png", ActionCreate, ReasonSource},
			{"gone_2.png", ActionCreate, ReasonSource},
			{"new.png", ActionCreate, ReasonNone},
		},
		CollisionSkip: {
			{"hero.png", ActionSkip, ReasonSource},
			{"gone.png", ActionSkip, ReasonSource},
			{"new.png", ActionCreate, ReasonNone},
		},
		CollisionOverwrite: {
			{"hero_3.png", ActionCreate, ReasonSource},
			{"gone_2.png", ActionCreate, ReasonSource},
			{"new.png", ActionCreate, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			out := t.TempDir()
			protected := []string{
				filepath.Join(out, "HERO.png"),
				filepath.Join(out, "hero_2.png"),
				filepath.Join(out, "gone.png"),
			}
			writeFile(t, protected[0], "too large to resize")
			writeFile(t, protected[1], "corrupt")
			in := t.TempDir()
			request := jobRequest(out, policy,
				png(filepath.Join(in, "hero.png")),
				png(filepath.Join(in, "gone.png")),
				png(filepath.Join(in, "new.png")),
			)
			request.Protected = protected
			plan := mustPrepare(t, request)
			assertItems(t, plan, want[policy])
			materialize(t, plan, "run")
			if readFile(t, protected[0]) != "too large to resize" || readFile(t, protected[1]) != "corrupt" {
				t.Fatal("a listed source the job does not process was overwritten")
			}
		})
	}
}

// TestPrepareProtectsSourcesThatNoLongerExist: a listed source that has
// disappeared still keeps its path; nothing is planned onto it.
func TestPrepareProtectsSourcesThatNoLongerExist(t *testing.T) {
	folder := t.TempDir()
	for _, policy := range policies {
		plan := mustPrepare(t, jobRequest(folder, policy, png(filepath.Join(folder, "gone.png"))))
		if policy == CollisionSkip {
			assertItems(t, plan, []expect{{"gone.png", ActionSkip, ReasonSource}})
			continue
		}
		assertItems(t, plan, []expect{{"gone_2.png", ActionCreate, ReasonSource}})
	}
}

// TestPrepareKeepsNaturalNamesAheadOfNumberedOnes: a source whose own name
// is free keeps it, even when an earlier source's numbered name would have
// taken it.
func TestPrepareKeepsNaturalNamesAheadOfNumberedOnes(t *testing.T) {
	plan := mustPrepare(t, jobRequest(t.TempDir(), CollisionRename,
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x_2.png")),
	))
	assertItems(t, plan, []expect{
		{"x.png", ActionCreate, ReasonNone},
		{"x_3.png", ActionCreate, ReasonDuplicate},
		{"x_2.png", ActionCreate, ReasonNone},
	})
}

// TestPrepareNumberedNamesAvoidSourcesAndClaims: a numbered name is never
// a source or another item's destination, and Rename and Overwrite alike
// pass over an existing file with a numbered name.
func TestPrepareNumberedNamesAvoidSourcesAndClaims(t *testing.T) {
	want := []expect{
		{"x.png", ActionCreate, ReasonNone},
		{"x_4.png", ActionCreate, ReasonDuplicate},
		{"x_2_2.png", ActionCreate, ReasonSource},
	}
	for _, policy := range []Collision{CollisionRename, CollisionOverwrite} {
		out := t.TempDir()
		resident := filepath.Join(out, "x_2.png")
		writeFile(t, resident, "a source inside the output folder")
		writeFile(t, filepath.Join(out, "x_3.png"), "an earlier result")
		plan := mustPrepare(t, jobRequest(out, policy,
			png(filepath.Join(t.TempDir(), "x.png")),
			png(filepath.Join(t.TempDir(), "x.png")),
			png(resident),
		))
		assertItems(t, plan, want)
		if !plan.Items[1].Renamed() {
			t.Fatalf("%s: a duplicate under a numbered name does not report Renamed", policy)
		}
	}
}

// TestPrepareOverwriteNeverReplacesNumberedNames pins RSZ-020, RSZ-026, and
// AC-R10 for renamed results: a confirmed Overwrite replaces only a
// result's natural destination. A file with a numbered name, such as a
// photo_2.png the job does not list beside the photo.png it resizes into
// its own folder, may be the user's original, so a renamed result takes the
// first unused name instead.
func TestPrepareOverwriteNeverReplacesNumberedNames(t *testing.T) {
	t.Run("source folder", func(t *testing.T) {
		folder := t.TempDir()
		source := filepath.Join(folder, "photo.png")
		original := filepath.Join(folder, "photo_2.png")
		writeFile(t, source, "listed original")
		writeFile(t, original, "unlisted original")
		plan := mustPrepare(t, jobRequest(folder, CollisionOverwrite, png(source)))
		assertItems(t, plan, []expect{{"photo_3.png", ActionCreate, ReasonSource}})
		materialize(t, plan, "result")
		if readFile(t, source) != "listed original" || readFile(t, original) != "unlisted original" {
			t.Fatal("an original was overwritten")
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		out := t.TempDir()
		original := filepath.Join(out, "x_2.png")
		writeFile(t, filepath.Join(out, "x.png"), "earlier")
		writeFile(t, original, "unlisted original")
		plan := mustPrepare(t, jobRequest(out, CollisionOverwrite,
			png(filepath.Join(t.TempDir(), "x.png")),
			png(filepath.Join(t.TempDir(), "x.png")),
		))
		assertItems(t, plan, []expect{
			{"x.png", ActionReplace, ReasonNone},
			{"x_3.png", ActionCreate, ReasonDuplicate},
		})
		materialize(t, plan, "result")
		if readFile(t, original) != "unlisted original" {
			t.Fatal("a file with a numbered name was overwritten")
		}
	})
}

// TestPrepareRepeatedRuns pins RSZ-049: a re-run plans against the results
// the earlier run left, never assuming them. Rename adds new numbered
// results, Skip skips, and Overwrite replaces the results with natural names
// and adds a new numbered result for a duplicate, since a file with a
// numbered name may as well be an original as an earlier result.
func TestPrepareRepeatedRuns(t *testing.T) {
	sources := []Source{
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "y.png")),
	}
	want := map[Collision][]expect{
		CollisionRename: {
			{"x_3.png", ActionCreate, ReasonExists},
			{"x_4.png", ActionCreate, ReasonExists},
			{"y_2.png", ActionCreate, ReasonExists},
		},
		CollisionSkip: {
			{"x.png", ActionSkip, ReasonExists},
			{"x.png", ActionSkip, ReasonExists},
			{"y.png", ActionSkip, ReasonExists},
		},
		CollisionOverwrite: {
			{"x.png", ActionReplace, ReasonNone},
			{"x_3.png", ActionCreate, ReasonDuplicate},
			{"y.png", ActionReplace, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			out := t.TempDir()
			first := mustPrepare(t, jobRequest(out, CollisionRename, sources...))
			assertItems(t, first, []expect{
				{"x.png", ActionCreate, ReasonNone},
				{"x_2.png", ActionCreate, ReasonDuplicate},
				{"y.png", ActionCreate, ReasonNone},
			})
			materialize(t, first, "first")

			second := mustPrepare(t, jobRequest(out, policy, sources...))
			assertItems(t, second, want[policy])
			materialize(t, second, "second")
			for index, item := range first.Items {
				replaced := false
				for _, later := range second.Items {
					if later.Action == ActionReplace && later.Destination == item.Destination {
						replaced = true
					}
				}
				wantContent := fmt.Sprintf("first result %d", index)
				if got := readFile(t, item.Destination); !replaced && got != wantContent {
					t.Fatalf("earlier result %s = %q, want it untouched", item.Destination, got)
				}
			}
			if policy != CollisionOverwrite {
				return
			}
			third := mustPrepare(t, jobRequest(out, policy, sources...))
			assertItems(t, third, []expect{
				{"x.png", ActionReplace, ReasonNone},
				{"x_4.png", ActionCreate, ReasonDuplicate},
				{"y.png", ActionReplace, ReasonNone},
			})
			if got := readFile(t, first.Items[1].Destination); got != "first result 1" {
				t.Fatalf("the first run's numbered result = %q, want it untouched", got)
			}
		})
	}
}

// TestPrepareSkipsIdentityUnlessOverwriting: Rename and Skip never replace
// a file, so they plan without reading any file identity.
func TestPrepareSkipsIdentityUnlessOverwriting(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "x.png"), "earlier")
	writeFile(t, filepath.Join(out, "x_2.png"), "earlier")
	in := t.TempDir()
	sources := []Source{png(filepath.Join(in, "x.png")), png(filepath.Join(in, "y.png"))}
	for _, source := range sources {
		writeFile(t, source.Path, "source")
	}
	for _, policy := range policies {
		calls := 0
		files := faultyFiles{identity: func(path string) (fileID, uint32, error) {
			calls++
			return identify(path)
		}}
		if _, err := prepare(context.Background(), jobRequest(out, policy, sources...), files); err != nil {
			t.Fatalf("%s: prepare: %v", policy, err)
		}
		if overwrite := policy == CollisionOverwrite; overwrite != (calls > 0) {
			t.Fatalf("%s read %d identities", policy, calls)
		}
	}
}

// TestPrepareFailsOneDestinationWithoutStoppingOthers pins RSZ-044: a
// destination that cannot be checked fails only its own item. A numbered
// name that cannot be checked is passed over, since another one serves.
func TestPrepareFailsOneDestinationWithoutStoppingOthers(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "taken.png"), "earlier")
	denied := map[string]bool{
		PathKey(filepath.Join(out, "bad.png")):     true,
		PathKey(filepath.Join(out, "taken_2.png")): true,
	}
	files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
		if denied[PathKey(path)] {
			return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
		}
		return os.Lstat(path)
	}}
	sources := []Source{
		png(filepath.Join(t.TempDir(), "good.png")),
		png(filepath.Join(t.TempDir(), "bad.png")),
		png(filepath.Join(t.TempDir(), "taken.png")),
		png(filepath.Join(t.TempDir(), "after.png")),
	}
	want := map[Collision][]expect{
		CollisionRename: {
			{"good.png", ActionCreate, ReasonNone},
			{"bad.png", ActionFail, ReasonNone},
			{"taken_3.png", ActionCreate, ReasonExists},
			{"after.png", ActionCreate, ReasonNone},
		},
		CollisionSkip: {
			{"good.png", ActionCreate, ReasonNone},
			{"bad.png", ActionFail, ReasonNone},
			{"taken.png", ActionSkip, ReasonExists},
			{"after.png", ActionCreate, ReasonNone},
		},
		CollisionOverwrite: {
			{"good.png", ActionCreate, ReasonNone},
			{"bad.png", ActionFail, ReasonNone},
			{"taken.png", ActionReplace, ReasonNone},
			{"after.png", ActionCreate, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			plan, err := prepare(context.Background(), jobRequest(out, policy, sources...), files)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			assertItems(t, plan, want[policy])
			for _, item := range plan.Items {
				if item.Action != ActionFail {
					continue
				}
				assertKind(t, item.Err, KindDestination)
				if !errors.Is(item.Err, fs.ErrPermission) {
					t.Fatalf("item error = %v, want the permission failure", item.Err)
				}
			}
		})
	}

	// Once the output folder itself cannot be reached, the search stops at
	// the first name it cannot check rather than trying every other.
	t.Run("folder gone", func(t *testing.T) {
		gone := false
		calls := 0
		files := faultyFiles{
			lstat: func(path string) (fs.FileInfo, error) {
				calls++
				if denied[PathKey(path)] {
					gone = true
					return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
				}
				return os.Lstat(path)
			},
			stat: func(path string) (fs.FileInfo, error) {
				if gone {
					return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
				}
				return os.Stat(path)
			},
		}
		plan, err := prepare(context.Background(), jobRequest(out, CollisionRename, sources[2]), files)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		assertItems(t, plan, []expect{{"taken.png", ActionFail, ReasonExists}})
		if !errors.Is(plan.Items[0].Err, fs.ErrPermission) || calls != 2 {
			t.Fatalf("item error = %v after %d checks, want the permission failure after 2", plan.Items[0].Err, calls)
		}
	})
}

// TestPrepareFailsDestinationsWhenTheFolderGoes: a name is free only when it
// is missing from a folder that exists. When the output folder goes after
// Prepare has opened it, as when its drive is removed, no destination is
// planned as free; each item that needs a name fails with the reason.
func TestPrepareFailsDestinationsWhenTheFolderGoes(t *testing.T) {
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
				if err := os.Remove(out); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("remove the output folder: %v", err)
				}
				return os.Lstat(path)
			}}
			// The first source lies in the output folder, so its result
			// needs a numbered name.
			plan, err := prepare(context.Background(), jobRequest(out, policy,
				png(filepath.Join(out, "x.png")), png(filepath.Join(t.TempDir(), "y.png"))), files)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			want := []expect{{"x.png", ActionFail, ReasonSource}, {"y.png", ActionFail, ReasonNone}}
			if policy == CollisionSkip {
				want[0] = expect{"x.png", ActionSkip, ReasonSource}
			}
			assertItems(t, plan, want)
			for index, item := range plan.Items {
				if item.Action == ActionFail && !errors.Is(item.Err, fs.ErrNotExist) {
					t.Fatalf("item %d error = %v, want the missing folder", index, item.Err)
				}
			}
		})
	}
}

// TestPrepareSearchesPastUncheckableNames: a search passing over names it
// cannot check, in a folder that stays reachable, checks the folder at the
// first, second, fourth, and so on such name rather than at every one, and
// each item whose search for the name finds none says why (UX-010), not
// only the first.
func TestPrepareSearchesPastUncheckableNames(t *testing.T) {
	out := t.TempDir()
	natural := PathKey(filepath.Join(out, "x.png"))
	lstats, stats := 0, 0
	files := faultyFiles{
		lstat: func(path string) (fs.FileInfo, error) {
			lstats++
			if PathKey(path) == natural {
				return fakeInfo{name: filepath.Base(path)}, nil
			}
			return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
		},
		stat: func(path string) (fs.FileInfo, error) {
			stats++
			return os.Stat(path)
		},
	}
	plan, err := prepare(context.Background(), jobRequest(out, CollisionRename,
		png(filepath.Join(t.TempDir(), "x.png")), png(filepath.Join(t.TempDir(), "X.png"))), files)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertItems(t, plan, []expect{{"x.png", ActionFail, ReasonExists}, {"X.png", ActionFail, ReasonExists}})
	for index, item := range plan.Items {
		if !errors.Is(item.Err, ErrNoUnusedName) || !errors.Is(item.Err, fs.ErrPermission) {
			t.Fatalf("item %d error = %v, want ErrNoUnusedName noting the name passed over", index, item.Err)
		}
	}
	// One check of the folder when Prepare opens it, then one at each power
	// of two up to the 9,998 names passed over: 1, 2, 4, ..., 8192.
	if lstats != maxSuffix || stats != 1+14 {
		t.Fatalf("checked %d names and the folder %d times, want %d and %d", lstats, stats, maxSuffix, 1+14)
	}
}

func TestTooLong(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{strings.Repeat("a", 255), false},
		{strings.Repeat("a", 256), true},
		{strings.Repeat("é", 255), false}, // 510 bytes, 255 code units
		{strings.Repeat("é", 256), true},
		{strings.Repeat("😀", 127) + "a", false}, // two code units each
		{strings.Repeat("😀", 128), true},
		// Go holds an unpaired surrogate, which an NTFS name may contain, in
		// three bytes of WTF-8; Windows stores it as one code unit.
		{strings.Repeat("a", 252) + strings.Repeat("\xed\xa0\x80", 3), false},
		{strings.Repeat("a", 253) + strings.Repeat("\xed\xa0\x80", 3), true},
		{strings.Repeat("é", 254) + "\xff", false}, // a stray byte arrives as U+FFFD
		{strings.Repeat("é", 255) + "\xff", true},
		{strings.Repeat("�", 255), false},
	}
	for _, test := range tests {
		if got := tooLong(test.name); got != test.want {
			t.Errorf("tooLong(%d bytes) = %v, want %v", len(test.name), got, test.want)
		}
		// Each case agrees with the name Windows would receive.
		encoded, err := windows.UTF16FromString(test.name)
		if err != nil {
			t.Fatalf("encode %q: %v", test.name, err)
		}
		if units := len(encoded) - 1; (units > maxNameLength) != test.want {
			t.Errorf("a %d-byte name is %d code units, so want %v is wrong", len(test.name), units, test.want)
		}
	}
}

// TestPrepareRejectsNamesTooLongForWindows: a name Windows would refuse,
// natural or numbered, fails its item with an explanation that names it,
// while the item's Destination stays its natural destination.
func TestPrepareRejectsNamesTooLongForWindows(t *testing.T) {
	out := t.TempDir()
	fits := strings.Repeat("a", 251)        // 255 with .png
	tooLongName := strings.Repeat("b", 252) // 256 with .png
	writeFile(t, filepath.Join(out, fits+".png"), "earlier")
	plan := mustPrepare(t, jobRequest(out, CollisionRename,
		png(filepath.Join(t.TempDir(), fits+".png")),
		png(filepath.Join(t.TempDir(), tooLongName+".png")),
		jpeg(filepath.Join(t.TempDir(), tooLongName[:251]+".jpeg")),
	))
	assertItems(t, plan, []expect{
		{fits + ".png", ActionFail, ReasonExists},
		{tooLongName + ".png", ActionFail, ReasonNone},
		{tooLongName[:251] + ".jpg", ActionCreate, ReasonNone},
	})
	for index, name := range []string{fits + "_2.png", tooLongName + ".png"} {
		item := plan.Items[index]
		assertKind(t, item.Err, KindDestination)
		if !errors.Is(item.Err, ErrNameTooLong) || !strings.Contains(item.Err.Error(), name) {
			t.Fatalf("item %d error = %v, want ErrNameTooLong naming %s", index, item.Err, name)
		}
	}
}

// TestPrepareBoundsTheRenameSearch: a folder where every name is taken
// fails the item instead of searching forever, and a later item with the
// same name fails without searching again.
func TestPrepareBoundsTheRenameSearch(t *testing.T) {
	out := t.TempDir()
	calls := 0
	files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
		calls++
		return fakeInfo{name: filepath.Base(path)}, nil
	}}
	plan, err := prepare(context.Background(), jobRequest(out, CollisionRename,
		png(filepath.Join(t.TempDir(), "x.png")), png(filepath.Join(t.TempDir(), "X.png"))), files)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertItems(t, plan, []expect{{"x.png", ActionFail, ReasonExists}, {"X.png", ActionFail, ReasonExists}})
	for _, item := range plan.Items {
		if !errors.Is(item.Err, ErrNoUnusedName) {
			t.Fatalf("item error = %v, want ErrNoUnusedName", item.Err)
		}
	}
	if calls != maxSuffix {
		t.Fatalf("checked %d names, want %d", calls, maxSuffix)
	}
}

// TestPrepareRenameSearchIsLinear: many sources with one name each take the
// next numbered name without walking past every name already given out, so
// planning a large job of same-named files stays fast.
func TestPrepareRenameSearchIsLinear(t *testing.T) {
	const count = 2000
	in := t.TempDir()
	sources := make([]Source, count)
	for index := range sources {
		sources[index] = png(filepath.Join(in, fmt.Sprint(index), "icon.png"))
	}
	files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: windows.ERROR_FILE_NOT_FOUND}
	}}
	request := jobRequest(t.TempDir(), CollisionRename, sources...)
	var plan Plan
	allocations := testing.AllocsPerRun(1, func() {
		var err error
		if plan, err = prepare(context.Background(), request, files); err != nil {
			t.Fatalf("prepare: %v", err)
		}
	})
	for index, item := range plan.Items {
		want := "icon.png"
		if index > 0 {
			want = fmt.Sprintf("icon_%d.png", index+1)
		}
		if filepath.Base(item.Destination) != want || item.Action != ActionCreate {
			t.Fatalf("item %d = %v %s, want create %s", index, item.Action, item.Destination, want)
		}
	}
	if allocations > 100*count {
		t.Fatalf("planning %d same-named sources made %.0f allocations, want at most %d", count, allocations, 100*count)
	}
}

func TestPrepareObservesCancellation(t *testing.T) {
	sources := []Source{png(filepath.Join(t.TempDir(), "x.png")), png(filepath.Join(t.TempDir(), "y.png"))}

	t.Run("before", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Prepare(ctx, jobRequest(out, CollisionRename, sources...))
		assertKind(t, err, KindCanceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("a cancelled job created the output folder: %v", statErr)
		}
	})

	t.Run("between items", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
			cancel()
			return os.Lstat(path)
		}}
		_, err := prepare(ctx, jobRequest(t.TempDir(), CollisionRename, sources...), files)
		assertKind(t, err, KindCanceled)
	})

	t.Run("during the rename search", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
			calls++
			if calls == 5 {
				cancel()
			}
			return fakeInfo{name: filepath.Base(path)}, nil
		}}
		_, err := prepare(ctx, jobRequest(t.TempDir(), CollisionRename, sources[0]), files)
		assertKind(t, err, KindCanceled)
		if calls != 5 {
			t.Fatalf("checked %d names after cancellation at 5", calls)
		}
	})
}

// TestPrepareNeverOverwritesASource generates many random jobs — sources
// spread over two input folders and the output folder itself, listed files
// the job does not process, earlier results, folders, and hard links to
// sources occupying destinations — and checks the plan of each against the
// collision rules, then writes every planned result and checks that no
// listed file changed (RSZ-024 to RSZ-026, NFR-020).
func TestPrepareNeverOverwritesASource(t *testing.T) {
	stems := []string{"a", "A", "b", "a_2", "B_2", "a_3"}
	extensions := []string{".png", ".PNG", ".jpg", ".jpeg"}
	formats := []Format{FormatKeepSource, FormatPNG, FormatJPEG}
	numbered := regexp.MustCompile(`^(.*)_([0-9]+)(\.png|\.jpg)$`)
	random := rand.New(rand.NewSource(5))
	for scenario := 0; scenario < 120; scenario++ {
		base := t.TempDir()
		folders := []string{filepath.Join(base, "in1"), filepath.Join(base, "in2"), filepath.Join(base, "out")}
		out := folders[2]
		var sources []Source
		var protected []string
		var listed []string
		seen := map[string]bool{}
		sourceCount, protectedCount := 1+random.Intn(6), random.Intn(3)
		for len(sources) < sourceCount || len(protected) < protectedCount {
			name := stems[random.Intn(len(stems))] + extensions[random.Intn(len(extensions))]
			path := filepath.Join(folders[random.Intn(len(folders))], name)
			if seen[PathKey(path)] {
				continue
			}
			seen[PathKey(path)] = true
			writeFile(t, path, "listed "+path)
			listed = append(listed, path)
			if len(sources) == sourceCount {
				protected = append(protected, path)
				continue
			}
			format := decode.FormatPNG
			if random.Intn(2) == 0 {
				format = decode.FormatJPEG
			}
			sources = append(sources, Source{Path: path, Format: format})
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatalf("create output folder: %v", err)
		}
		for extra := random.Intn(5); extra > 0; extra-- {
			name := stems[random.Intn(len(stems))] + []string{".png", ".jpg"}[random.Intn(2)]
			path := filepath.Join(out, name)
			if _, err := os.Lstat(path); err == nil {
				continue
			}
			switch random.Intn(3) {
			case 0:
				writeFile(t, path, "earlier result")
			case 1:
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("create folder %s: %v", path, err)
				}
			case 2:
				target := listed[random.Intn(len(listed))]
				if err := os.Link(target, path); err != nil {
					t.Fatalf("hard-link %s to %s: %v", path, target, err)
				}
			}
		}
		policy := policies[random.Intn(len(policies))]
		request := jobRequest(out, policy, sources...)
		request.Protected = protected
		request.Format = formats[random.Intn(len(formats))]
		label := fmt.Sprintf("scenario %d (%s, %s)", scenario, policy, request.Format)

		existed := map[string]fs.FileInfo{}
		entries, err := os.ReadDir(out)
		if err != nil {
			t.Fatalf("%s: list output folder: %v", label, err)
		}
		for _, entry := range entries {
			info, err := os.Lstat(filepath.Join(out, entry.Name()))
			if err != nil {
				t.Fatalf("%s: inspect %s: %v", label, entry.Name(), err)
			}
			existed[strings.ToUpper(entry.Name())] = info
		}
		listedInfos := make([]fs.FileInfo, len(listed))
		listedKeys := map[string]bool{}
		for index, path := range listed {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("%s: inspect listed file: %v", label, err)
			}
			listedInfos[index] = info
			listedKeys[PathKey(path)] = true
		}

		plan := mustPrepare(t, request)
		written := map[string]bool{}
		for index, item := range plan.Items {
			source := sources[index]
			natural := stem(source.Path) + Extension(OutputFormat(request.Format, source.Format))
			name := filepath.Base(item.Destination)
			if filepath.Dir(item.Destination) != out {
				t.Fatalf("%s: item %d destination %s is outside the output folder", label, index, item.Destination)
			}
			if item.Reason == ReasonDuplicate && (item.DuplicateOf < 0 || item.DuplicateOf >= index) ||
				item.Reason != ReasonDuplicate && item.DuplicateOf != -1 {
				t.Fatalf("%s: item %d for %v is a duplicate of item %d", label, index, item.Reason, item.DuplicateOf)
			}
			switch item.Action {
			case ActionFail:
				t.Fatalf("%s: item %d failed: %v", label, index, item.Err)
			case ActionSkip:
				if policy != CollisionSkip || item.Reason == ReasonNone || item.Reason == ReasonDuplicate || name != natural {
					t.Fatalf("%s: item %d skipped %s for %v", label, index, name, item.Reason)
				}
				continue
			case ActionDefer:
				owner := plan.Items[item.DuplicateOf]
				if policy != CollisionSkip || item.Reason != ReasonDuplicate || name != natural ||
					owner.Action != ActionCreate || PathKey(owner.Destination) != PathKey(item.Destination) {
					t.Fatalf("%s: item %d defers %s to item %d", label, index, name, item.DuplicateOf)
				}
				continue
			case ActionReplace:
				// Only a natural destination is ever replaced: a file with
				// a numbered name may be an original nobody listed.
				info, ok := existed[strings.ToUpper(name)]
				if policy != CollisionOverwrite || item.Reason != ReasonNone || !ok || !info.Mode().IsRegular() {
					t.Fatalf("%s: item %d replaces %s, which it must not", label, index, name)
				}
			case ActionCreate:
				if _, ok := existed[strings.ToUpper(name)]; ok {
					t.Fatalf("%s: item %d creates %s, which already exists", label, index, name)
				}
			}
			if item.Reason == ReasonNone {
				if name != natural {
					t.Fatalf("%s: item %d writes %s, not its natural %s", label, index, name, natural)
				}
			} else if match := numbered.FindStringSubmatch(name); match == nil ||
				match[1]+match[3] != natural || match[2] == "0" || match[2] == "1" || strings.HasPrefix(match[2], "0") {
				t.Fatalf("%s: renamed item %d writes %s, not a numbered %s", label, index, name, natural)
			}
			key := PathKey(item.Destination)
			if listedKeys[key] {
				t.Fatalf("%s: item %d writes the listed path %s", label, index, item.Destination)
			}
			if written[key] {
				t.Fatalf("%s: two items write %s", label, item.Destination)
			}
			written[key] = true
			if info, ok := existed[strings.ToUpper(name)]; ok {
				for _, listedInfo := range listedInfos {
					if os.SameFile(info, listedInfo) {
						t.Fatalf("%s: item %d replaces %s, an alias of a listed file", label, index, name)
					}
				}
			}
		}
		materialize(t, plan, "result")
		for _, path := range listed {
			if got := readFile(t, path); got != "listed "+path {
				t.Fatalf("%s: listed file %s was overwritten with %q", label, path, got)
			}
		}
	}
}

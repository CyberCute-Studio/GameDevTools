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

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

var policies = []imageresizer.CollisionPolicy{
	imageresizer.CollisionRename,
	imageresizer.CollisionSkip,
	imageresizer.CollisionOverwrite,
}

// jobRequest builds a request for the folder and policy with the default
// Keep Source Format, confirming Overwrite jobs.
func jobRequest(folder string, policy imageresizer.CollisionPolicy, sources ...Source) Request {
	options := imageresizer.DefaultOptions(folder)
	options.Collision = policy
	return Request{
		Sources:          sources,
		Options:          options,
		ConfirmOverwrite: policy == imageresizer.CollisionOverwrite,
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
	if len(plan.Items) != len(request.Sources) {
		t.Fatalf("plan has %d items for %d sources", len(plan.Items), len(request.Sources))
	}
	for index, item := range plan.Items {
		if item.Source != request.Sources[index].Path {
			t.Fatalf("item %d source = %q, want %q", index, item.Source, request.Sources[index].Path)
		}
	}
	return plan
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
		option  imageresizer.OutputFormat
		content decode.Format
		want    decode.Format
		ext     string
	}{
		{imageresizer.FormatKeepSource, decode.FormatPNG, decode.FormatPNG, ".png"},
		{imageresizer.FormatKeepSource, decode.FormatJPEG, decode.FormatJPEG, ".jpg"},
		{imageresizer.FormatPNG, decode.FormatJPEG, decode.FormatPNG, ".png"},
		{imageresizer.FormatPNG, decode.FormatPNG, decode.FormatPNG, ".png"},
		{imageresizer.FormatJPEG, decode.FormatPNG, decode.FormatJPEG, ".jpg"},
		{imageresizer.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG, ".jpg"},
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
		format  imageresizer.OutputFormat
		names   []string
		formats []decode.Format
	}{
		{
			format:  imageresizer.FormatKeepSource,
			names:   []string{"hero.png", "Sky.jpg", "map.tiles.jpg", "mislabeled.jpg"},
			formats: []decode.Format{decode.FormatPNG, decode.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG},
		},
		{
			format:  imageresizer.FormatPNG,
			names:   []string{"hero.png", "Sky.png", "map.tiles.png", "mislabeled.png"},
			formats: []decode.Format{decode.FormatPNG, decode.FormatPNG, decode.FormatPNG, decode.FormatPNG},
		},
		{
			format:  imageresizer.FormatJPEG,
			names:   []string{"hero.jpg", "Sky.jpg", "map.tiles.jpg", "mislabeled.jpg"},
			formats: []decode.Format{decode.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG, decode.FormatJPEG},
		},
	}
	for _, test := range tests {
		t.Run(string(test.format), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			request := jobRequest(out, imageresizer.CollisionRename, sources...)
			request.Options.Format = test.format
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
	plan := mustPrepare(t, jobRequest(out+string(filepath.Separator), imageresizer.CollisionRename,
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
		{name: "relative folder", mutate: func(r *Request) { r.Options.OutputFolder = "Resized" }},
		{name: "empty folder", mutate: func(r *Request) { r.Options.OutputFolder = "" }},
		{name: "unknown policy", mutate: func(r *Request) { r.Options.Collision = "delete" }},
		{name: "unknown format", mutate: func(r *Request) { r.Options.Format = "gif" }},
		{name: "quality", mutate: func(r *Request) { r.Options.JPEGQuality = 0 }},
		{name: "no sources", mutate: func(r *Request) { r.Sources = nil }, want: ErrNoSources},
		{name: "relative source", mutate: func(r *Request) { r.Sources = append(r.Sources, png("hero.png")) }},
		{name: "empty source", mutate: func(r *Request) { r.Sources = append(r.Sources, png("")) }},
		{name: "NUL in source", mutate: func(r *Request) { r.Sources = append(r.Sources, png(valid.Path+"\x00.png")) }},
		{name: "root source", mutate: func(r *Request) { r.Sources = append(r.Sources, png(root)) }},
		{name: "unknown source format", mutate: func(r *Request) {
			r.Sources = append(r.Sources, Source{Path: filepath.Join(in, "a.png"), Format: decode.FormatUnknown})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			request := jobRequest(out, imageresizer.CollisionRename, valid)
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
	request := jobRequest(out, imageresizer.CollisionOverwrite, source)
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
	for _, policy := range []imageresizer.CollisionPolicy{imageresizer.CollisionRename, imageresizer.CollisionSkip} {
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
	mkdirAll func(path string) error
	stat     func(path string) (fs.FileInfo, error)
	lstat    func(path string) (fs.FileInfo, error)
	identity func(path string) (fileID, error)
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

func (f faultyFiles) Identity(path string) (fileID, error) {
	if f.identity != nil {
		return f.identity(path)
	}
	return f.osFiles.Identity(path)
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

	t.Run("blocked by a file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-folder")
		writeFile(t, blocker, "file")
		_, err := Prepare(context.Background(), jobRequest(blocker, imageresizer.CollisionRename, source))
		assertKind(t, err, KindFolder)
		if readFile(t, blocker) != "file" {
			t.Fatal("the blocking file was changed")
		}
	})

	t.Run("blocked parent", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-folder")
		writeFile(t, blocker, "file")
		_, err := Prepare(context.Background(), jobRequest(filepath.Join(blocker, "out"), imageresizer.CollisionRename, source))
		assertKind(t, err, KindFolder)
	})

	t.Run("permission", func(t *testing.T) {
		files := faultyFiles{mkdirAll: func(path string) error {
			return &fs.PathError{Op: "mkdir", Path: path, Err: fs.ErrPermission}
		}}
		_, err := prepare(context.Background(), jobRequest(filepath.Join(t.TempDir(), "out"), imageresizer.CollisionRename, source), files)
		assertKind(t, err, KindFolder)
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("error = %v, want a permission failure", err)
		}
	})

	t.Run("not a folder", func(t *testing.T) {
		files := faultyFiles{
			mkdirAll: func(string) error { return nil },
			stat:     func(path string) (fs.FileInfo, error) { return fakeInfo{name: filepath.Base(path)}, nil },
		}
		_, err := prepare(context.Background(), jobRequest(filepath.Join(t.TempDir(), "out"), imageresizer.CollisionRename, source), files)
		assertKind(t, err, KindFolder)
		if !errors.Is(err, ErrNotFolder) {
			t.Fatalf("error = %v, want ErrNotFolder", err)
		}
	})

	t.Run("unreadable folder", func(t *testing.T) {
		files := faultyFiles{stat: func(path string) (fs.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
		}}
		_, err := prepare(context.Background(), jobRequest(t.TempDir(), imageresizer.CollisionRename, source), files)
		assertKind(t, err, KindFolder)
	})
}

// TestPrepareSameNameFromDifferentFolders pins RSZ-024 and AC-R08: sources
// that would produce the same destination never share it. The first keeps
// the name; Rename and Overwrite number the others deterministically, and
// Skip skips them. Names compare case-insensitively, as Windows paths do.
func TestPrepareSameNameFromDifferentFolders(t *testing.T) {
	sources := []Source{
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x.png")),
		jpeg(filepath.Join(t.TempDir(), "X.PNG")),
	}
	want := map[imageresizer.CollisionPolicy][]expect{
		imageresizer.CollisionRename: {
			{"x.png", ActionCreate, ReasonNone},
			{"x_2.png", ActionCreate, ReasonDuplicate},
			{"X.jpg", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionSkip: {
			{"x.png", ActionCreate, ReasonNone},
			{"x.png", ActionSkip, ReasonDuplicate},
			{"X.jpg", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionOverwrite: {
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

			// With PNG output, the third source's name differs from the
			// others only in letter case.
			request.Options.Format = imageresizer.FormatPNG
			plan = mustPrepare(t, request)
			if policy == imageresizer.CollisionSkip {
				assertItems(t, plan, []expect{
					{"x.png", ActionCreate, ReasonNone},
					{"x.png", ActionSkip, ReasonDuplicate},
					{"X.png", ActionSkip, ReasonDuplicate},
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
	want := map[imageresizer.CollisionPolicy][]expect{
		imageresizer.CollisionRename: {
			{"x_2.png", ActionCreate, ReasonExists},
			{"folder_2.png", ActionCreate, ReasonExists},
			{"new.png", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionSkip: {
			{"x.png", ActionSkip, ReasonExists},
			{"folder.png", ActionSkip, ReasonExists},
			{"new.png", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionOverwrite: {
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
			if got := plan.Items[0].Renamed(); got != (policy == imageresizer.CollisionRename) {
				t.Fatalf("Renamed() = %v under %s", got, policy)
			}
			if readFile(t, filepath.Join(out, "x.png")) != "earlier" {
				t.Fatal("planning changed an existing destination")
			}
		})
	}
}

// TestPrepareNeverTargetsSources pins RSZ-026 and AC-R07 with the output
// folder holding the sources themselves: no policy plans onto a source.
// Rename and Overwrite rename the result, and Skip skips it.
func TestPrepareNeverTargetsSources(t *testing.T) {
	want := map[imageresizer.CollisionPolicy][]expect{
		imageresizer.CollisionRename: {
			{"a_2.png", ActionCreate, ReasonSource},
			{"b_2.jpg", ActionCreate, ReasonSource},
			{"c.jpg", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionSkip: {
			{"a.png", ActionSkip, ReasonSource},
			{"b.jpg", ActionSkip, ReasonSource},
			{"c.jpg", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionOverwrite: {
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
		})
	}
}

// TestPrepareProtectsSourcesThatNoLongerExist: a listed source that has
// disappeared still keeps its path; nothing is planned onto it.
func TestPrepareProtectsSourcesThatNoLongerExist(t *testing.T) {
	folder := t.TempDir()
	for _, policy := range policies {
		plan := mustPrepare(t, jobRequest(folder, policy, png(filepath.Join(folder, "gone.png"))))
		if policy == imageresizer.CollisionSkip {
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
	plan := mustPrepare(t, jobRequest(t.TempDir(), imageresizer.CollisionRename,
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
// a source, another item's destination, or an existing file.
func TestPrepareNumberedNamesAvoidSourcesAndClaims(t *testing.T) {
	out := t.TempDir()
	resident := filepath.Join(out, "x_2.png")
	writeFile(t, resident, "a source inside the output folder")
	writeFile(t, filepath.Join(out, "x_3.png"), "an earlier result")
	for _, policy := range []imageresizer.CollisionPolicy{imageresizer.CollisionRename, imageresizer.CollisionOverwrite} {
		plan := mustPrepare(t, jobRequest(out, policy,
			png(filepath.Join(t.TempDir(), "x.png")),
			png(filepath.Join(t.TempDir(), "x.png")),
			png(resident),
		))
		assertItems(t, plan, []expect{
			{"x.png", ActionCreate, ReasonNone},
			{"x_4.png", ActionCreate, ReasonDuplicate},
			{"x_2_2.png", ActionCreate, ReasonSource},
		})
	}
}

// TestPrepareRepeatedRuns pins RSZ-049: a re-run plans against the results
// the earlier run left, never assuming them.
func TestPrepareRepeatedRuns(t *testing.T) {
	sources := []Source{
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "x.png")),
		png(filepath.Join(t.TempDir(), "y.png")),
	}
	want := map[imageresizer.CollisionPolicy][]expect{
		imageresizer.CollisionRename: {
			{"x_3.png", ActionCreate, ReasonExists},
			{"x_4.png", ActionCreate, ReasonExists},
			{"y_2.png", ActionCreate, ReasonExists},
		},
		imageresizer.CollisionSkip: {
			{"x.png", ActionSkip, ReasonExists},
			{"x.png", ActionSkip, ReasonExists},
			{"y.png", ActionSkip, ReasonExists},
		},
		// The second source cannot replace the first source's result, so it
		// takes a new numbered name rather than replacing the numbered file
		// the earlier run wrote.
		imageresizer.CollisionOverwrite: {
			{"x.png", ActionReplace, ReasonNone},
			{"x_3.png", ActionCreate, ReasonDuplicate},
			{"y.png", ActionReplace, ReasonNone},
		},
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			out := t.TempDir()
			first := mustPrepare(t, jobRequest(out, imageresizer.CollisionRename, sources...))
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
		})
	}
}

// TestPrepareFailsOneDestinationWithoutStoppingOthers pins RSZ-044: a
// destination that cannot be checked fails only its own item.
func TestPrepareFailsOneDestinationWithoutStoppingOthers(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "taken.png"), "earlier")
	denied := map[string]bool{
		imageresizer.PathKey(filepath.Join(out, "bad.png")):     true,
		imageresizer.PathKey(filepath.Join(out, "taken_2.png")): true,
	}
	files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
		if denied[imageresizer.PathKey(path)] {
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
	want := map[imageresizer.CollisionPolicy][]expect{
		imageresizer.CollisionRename: {
			{"good.png", ActionCreate, ReasonNone},
			{"bad.png", ActionFail, ReasonNone},
			{"taken.png", ActionFail, ReasonExists},
			{"after.png", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionSkip: {
			{"good.png", ActionCreate, ReasonNone},
			{"bad.png", ActionFail, ReasonNone},
			{"taken.png", ActionSkip, ReasonExists},
			{"after.png", ActionCreate, ReasonNone},
		},
		imageresizer.CollisionOverwrite: {
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
	}
	for _, test := range tests {
		if got := tooLong(test.name); got != test.want {
			t.Errorf("tooLong(%d bytes) = %v, want %v", len(test.name), got, test.want)
		}
	}
}

// TestPrepareRejectsNamesTooLongForWindows: a name Windows would refuse,
// natural or numbered, fails its item with an explanation.
func TestPrepareRejectsNamesTooLongForWindows(t *testing.T) {
	out := t.TempDir()
	fits := strings.Repeat("a", 251)        // 255 with .png
	tooLongName := strings.Repeat("b", 252) // 256 with .png
	writeFile(t, filepath.Join(out, fits+".png"), "earlier")
	plan := mustPrepare(t, jobRequest(out, imageresizer.CollisionRename,
		png(filepath.Join(t.TempDir(), fits+".png")),
		png(filepath.Join(t.TempDir(), tooLongName+".png")),
		jpeg(filepath.Join(t.TempDir(), tooLongName[:251]+".jpeg")),
	))
	assertItems(t, plan, []expect{
		{fits + ".png", ActionFail, ReasonExists},
		{tooLongName + ".png", ActionFail, ReasonNone},
		{tooLongName[:251] + ".jpg", ActionCreate, ReasonNone},
	})
	for _, item := range plan.Items[:2] {
		assertKind(t, item.Err, KindDestination)
		if !errors.Is(item.Err, ErrNameTooLong) {
			t.Fatalf("item error = %v, want ErrNameTooLong", item.Err)
		}
	}
}

// TestPrepareBoundsTheRenameSearch: a folder where every name is taken
// fails the item instead of searching forever.
func TestPrepareBoundsTheRenameSearch(t *testing.T) {
	out := t.TempDir()
	calls := 0
	files := faultyFiles{lstat: func(path string) (fs.FileInfo, error) {
		calls++
		return fakeInfo{name: filepath.Base(path)}, nil
	}}
	plan, err := prepare(context.Background(),
		jobRequest(out, imageresizer.CollisionRename, png(filepath.Join(t.TempDir(), "x.png"))), files)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertItems(t, plan, []expect{{"x.png", ActionFail, ReasonExists}})
	if !errors.Is(plan.Items[0].Err, ErrNoUnusedName) {
		t.Fatalf("item error = %v, want ErrNoUnusedName", plan.Items[0].Err)
	}
	if calls != maxSuffix {
		t.Fatalf("checked %d names, want %d", calls, maxSuffix)
	}
}

func TestPrepareObservesCancellation(t *testing.T) {
	sources := []Source{png(filepath.Join(t.TempDir(), "x.png")), png(filepath.Join(t.TempDir(), "y.png"))}

	t.Run("before", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Prepare(ctx, jobRequest(out, imageresizer.CollisionRename, sources...))
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
		_, err := prepare(ctx, jobRequest(t.TempDir(), imageresizer.CollisionRename, sources...), files)
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
		_, err := prepare(ctx, jobRequest(t.TempDir(), imageresizer.CollisionRename, sources[0]), files)
		assertKind(t, err, KindCanceled)
		if calls != 5 {
			t.Fatalf("checked %d names after cancellation at 5", calls)
		}
	})
}

// TestPrepareNeverOverwritesASource generates many random jobs — sources
// spread over two input folders and the output folder itself, earlier
// results, folders, and hard links to sources occupying destinations — and
// checks the plan of each against the collision rules, then writes every
// planned result and checks that no source changed (RSZ-024 to RSZ-026,
// NFR-020).
func TestPrepareNeverOverwritesASource(t *testing.T) {
	stems := []string{"a", "A", "b", "a_2", "B_2", "a_3"}
	extensions := []string{".png", ".PNG", ".jpg", ".jpeg"}
	formats := []imageresizer.OutputFormat{imageresizer.FormatKeepSource, imageresizer.FormatPNG, imageresizer.FormatJPEG}
	numbered := regexp.MustCompile(`^(.*)_([0-9]+)(\.png|\.jpg)$`)
	random := rand.New(rand.NewSource(5))
	for scenario := 0; scenario < 120; scenario++ {
		base := t.TempDir()
		folders := []string{filepath.Join(base, "in1"), filepath.Join(base, "in2"), filepath.Join(base, "out")}
		out := folders[2]
		var sources []Source
		seen := map[string]bool{}
		for count := 1 + random.Intn(6); len(sources) < count; {
			name := stems[random.Intn(len(stems))] + extensions[random.Intn(len(extensions))]
			path := filepath.Join(folders[random.Intn(len(folders))], name)
			if seen[imageresizer.PathKey(path)] {
				continue
			}
			seen[imageresizer.PathKey(path)] = true
			format := decode.FormatPNG
			if random.Intn(2) == 0 {
				format = decode.FormatJPEG
			}
			sources = append(sources, Source{Path: path, Format: format})
			writeFile(t, path, "source "+path)
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
				target := sources[random.Intn(len(sources))].Path
				if err := os.Link(target, path); err != nil {
					t.Fatalf("hard-link %s to %s: %v", path, target, err)
				}
			}
		}
		policy := policies[random.Intn(len(policies))]
		request := jobRequest(out, policy, sources...)
		request.Options.Format = formats[random.Intn(len(formats))]
		label := fmt.Sprintf("scenario %d (%s, %s)", scenario, policy, request.Options.Format)

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
		sourceInfos := make([]fs.FileInfo, len(sources))
		sourceKeys := map[string]bool{}
		for index, source := range sources {
			info, err := os.Stat(source.Path)
			if err != nil {
				t.Fatalf("%s: inspect source: %v", label, err)
			}
			sourceInfos[index] = info
			sourceKeys[imageresizer.PathKey(source.Path)] = true
		}

		plan := mustPrepare(t, request)
		written := map[string]bool{}
		for index, item := range plan.Items {
			source := sources[index]
			natural := stem(source.Path) + Extension(OutputFormat(request.Options.Format, source.Format))
			name := filepath.Base(item.Destination)
			if filepath.Dir(item.Destination) != out {
				t.Fatalf("%s: item %d destination %s is outside the output folder", label, index, item.Destination)
			}
			switch item.Action {
			case ActionFail:
				t.Fatalf("%s: item %d failed: %v", label, index, item.Err)
			case ActionSkip:
				if policy != imageresizer.CollisionSkip || item.Reason == ReasonNone || name != natural {
					t.Fatalf("%s: item %d skipped %s for %v", label, index, name, item.Reason)
				}
				continue
			case ActionReplace:
				info, ok := existed[strings.ToUpper(name)]
				if policy != imageresizer.CollisionOverwrite || !ok || !info.Mode().IsRegular() || name != natural {
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
			key := imageresizer.PathKey(item.Destination)
			if sourceKeys[key] {
				t.Fatalf("%s: item %d writes the source path %s", label, index, item.Destination)
			}
			if written[key] {
				t.Fatalf("%s: two items write %s", label, item.Destination)
			}
			written[key] = true
			if info, ok := existed[strings.ToUpper(name)]; ok {
				for _, sourceInfo := range sourceInfos {
					if os.SameFile(info, sourceInfo) {
						t.Fatalf("%s: item %d replaces %s, an alias of a source", label, index, name)
					}
				}
			}
		}
		materialize(t, plan, "result")
		for _, source := range sources {
			if got := readFile(t, source.Path); got != "source "+source.Path {
				t.Fatalf("%s: source %s was overwritten with %q", label, source.Path, got)
			}
		}
	}
}

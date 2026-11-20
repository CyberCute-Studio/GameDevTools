package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
)

// inspected lists a source as the source list does once its content check
// succeeds.
func inspected(t *testing.T, path string) Input {
	t.Helper()
	info, err := decode.Inspect(context.Background(), path)
	if err != nil {
		t.Fatalf("inspect %s: %v", path, err)
	}
	return Input{Path: path, Info: info}
}

// listed lists a file the fake processes never read, as a 1 × 1 PNG source
// whose decoding the content check estimated at decodedBytes.
func listed(t *testing.T, name string, decodedBytes uint64) Input {
	t.Helper()
	path := writeSource(t, name, []byte("never read"))
	return Input{Path: path, Info: decode.Info{Format: decode.FormatPNG, Width: 1, Height: 1, DecodedBytes: decodedBytes}}
}

// damagedSinceChecked lists a source that was checked as a valid PNG and was
// then overwritten with damaged content.
func damagedSinceChecked(t *testing.T, name string) Input {
	t.Helper()
	input := inspected(t, writeSource(t, name, encodePNG(t, halfTransparent())))
	if err := os.WriteFile(input.Path, []byte("\x89PNG\r\n\x1a\nnot really"), 0o644); err != nil {
		t.Fatal(err)
	}
	return input
}

func newRequest(inputs []Input, folder string, policy output.Collision) Request {
	return Request{
		Inputs:           inputs,
		Folder:           folder,
		Format:           output.FormatKeepSource,
		Collision:        policy,
		ConfirmOverwrite: policy == output.CollisionOverwrite,
		Settings:         settings(16, 16, preset.ResizeFit, white),
	}
}

// recorder collects a job's events and notes whether two were ever reported
// at once.
type recorder struct {
	mu      sync.Mutex
	events  []Event
	inside  atomic.Int32
	overlap atomic.Bool
	// then, when set, runs after each event is recorded.
	then func(Event)
}

func (r *recorder) observe(event Event) {
	if r.inside.Add(1) > 1 {
		r.overlap.Store(true)
	}
	defer r.inside.Add(-1)
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	if r.then != nil {
		r.then(event)
	}
}

// states lists the states the item at index was reported in.
func (r *recorder) states(index int) []State {
	var states []State
	for _, event := range r.events {
		if event.Index == index {
			states = append(states, event.Status.State)
		}
	}
	return states
}

// check verifies the event stream against the summary: events never
// overlap, every item ends exactly once — straight from Pending or after
// being Processing, which it is reported again only under a new destination
// or after it was Pending again — in the status the summary gives it, and
// every event's counts are those of the states reported so far.
func (r *recorder) check(t *testing.T, summary Summary) {
	t.Helper()
	if r.overlap.Load() {
		t.Error("two events were reported at once")
	}
	total := len(summary.Items)
	replay := make([]State, total)
	reported := make([]Status, total)
	counts := Progress{Total: total, Pending: total}
	done := 0
	for _, event := range r.events {
		if event.Index < 0 || event.Index >= total {
			t.Fatalf("event for item %d of %d", event.Index, total)
		}
		previous := replay[event.Index]
		next := event.Status.State
		switch {
		case previous.Final():
			t.Fatalf("item %d: %v after it ended %v", event.Index, next, previous)
		case next == StateProcessing && previous == StateProcessing && event.Status.Destination == reported[event.Index].Destination:
			t.Fatalf("item %d: processing again at %q", event.Index, event.Status.Destination)
		case next == StatePending && previous != StateProcessing:
			t.Fatalf("item %d: reported pending after %v", event.Index, previous)
		}
		replay[event.Index] = next
		reported[event.Index] = event.Status
		counts.count(previous, -1)
		counts.count(next, 1)
		if event.Progress != counts {
			t.Fatalf("item %d %v: progress %+v, want %+v", event.Index, next, event.Progress, counts)
		}
		if event.Progress.Done() < done {
			t.Fatalf("progress went back from %d to %d done", done, event.Progress.Done())
		}
		done = event.Progress.Done()
	}
	for index, status := range summary.Items {
		if !status.State.Final() || reported[index] != status {
			t.Fatalf("item %d: last reported %+v, summary %+v", index, reported[index], status)
		}
	}
	if summary.Progress != counts || summary.Progress.Done() != total {
		t.Fatalf("summary progress %+v, want %+v with every item done", summary.Progress, counts)
	}
}

// requireStatus checks an item's state, destination, and reason.
func requireStatus(t *testing.T, summary Summary, index int, state State, destination string, reason output.Reason) Status {
	t.Helper()
	status := summary.Items[index]
	if status.State != state || status.Destination != destination || status.Reason != reason {
		t.Fatalf("item %d = %v at %q (%v, %v), want %v at %q (%v)", index, status.State, status.Destination, status.Reason, status.Err, state, destination, reason)
	}
	return status
}

func requireFiles(t *testing.T, folder string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("%s holds %q, want %q", folder, names, want)
	}
}

func requireContent(t *testing.T, path string, want []byte) {
	t.Helper()
	if content, err := os.ReadFile(path); err != nil || !bytes.Equal(content, want) {
		t.Fatalf("%s holds %q, %v, want %q", path, content, err, want)
	}
}

// testConfig runs at most workers items at once with an unlimited budget.
func testConfig(workers int) config {
	cfg := defaultConfig()
	cfg.workers = workers
	cfg.budget = ^uint64(0)
	return cfg
}

// TestRunMixedBatch pins AC-R06: every valid source is resized, a source the
// list could not use and one damaged since it was checked each fail with
// their own error, and no source changes (NFR-020).
func TestRunMixedBatch(t *testing.T) {
	spriteContent := encodePNG(t, halfTransparent())
	sprite := inspected(t, writeSource(t, "sprite.png", spriteContent))
	photoContent := encodeJPEG(t, quadrants(64, 48))
	photo := inspected(t, writeSource(t, "photo.jpg", photoContent))
	unsupportedErr := &decode.Error{Kind: decode.KindUnsupported, Err: decode.ErrUnrecognized}
	unsupported := Input{Path: writeSource(t, "notes.png", []byte("GIF89a")), Err: unsupportedErr}
	damaged := damagedSinceChecked(t, "damaged.png")
	out := filepath.Join(t.TempDir(), "out")

	var events recorder
	summary, err := run(context.Background(), newRequest([]Input{sprite, unsupported, damaged, photo}, out, output.CollisionRename), events.observe, testConfig(4))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite.png"), output.ReasonNone)
	if failed := requireStatus(t, summary, 1, StateFailed, "", output.ReasonNone); failed.Err != unsupportedErr {
		t.Fatalf("unsupported source error = %v, want the list's", failed.Err)
	}
	failed := requireStatus(t, summary, 2, StateFailed, filepath.Join(out, "damaged.png"), output.ReasonNone)
	var decodeErr *decode.Error
	if !errors.As(failed.Err, &decodeErr) || decodeErr.Kind != decode.KindCorrupt {
		t.Fatalf("damaged source error = %v, want a corrupt-source decode error", failed.Err)
	}
	requireStatus(t, summary, 3, StateCompleted, filepath.Join(out, "photo.jpg"), output.ReasonNone)
	if got := summary.Progress; got.Completed != 2 || got.Failed != 2 || got.Skipped != 0 || got.Cancelled != 0 {
		t.Fatalf("summary counts = %+v", got)
	}
	if summary.Folder != out {
		t.Fatalf("summary folder = %q, want %q", summary.Folder, out)
	}
	if got := events.states(0); !slices.Equal(got, []State{StateProcessing, StateCompleted}) {
		t.Fatalf("resized item went through %v", got)
	}
	if got := events.states(1); !slices.Equal(got, []State{StateFailed}) {
		t.Fatalf("unusable item went through %v", got)
	}
	if result := decodeResult(t, filepath.Join(out, "sprite.png")); result.Width != 16 || result.Height != 8 || !result.HasAlpha {
		t.Fatalf("sprite result = %+v", result.Info)
	}
	if result := decodeResult(t, filepath.Join(out, "photo.jpg")); result.Width != 16 || result.Height != 12 {
		t.Fatalf("photo result = %+v", result.Info)
	}
	requireFiles(t, out, "sprite.png", "photo.jpg")
	requireContent(t, sprite.Path, spriteContent)
	requireContent(t, photo.Path, photoContent)
}

// TestRunRenamesDuplicatesAndReplansOnRerun pins AC-R08 and RSZ-049: two
// sources with one name get deterministic numbered names, and running the
// job again plans around the first run's results instead of assuming them,
// leaving them as they were.
func TestRunRenamesDuplicatesAndReplansOnRerun(t *testing.T) {
	first := inspected(t, writeSource(t, "sprite.png", encodePNG(t, halfTransparent())))
	second := inspected(t, writeSource(t, "sprite.png", encodePNG(t, quadrants(40, 40))))
	out := t.TempDir()
	request := newRequest([]Input{first, second}, out, output.CollisionRename)

	summary, err := Run(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite.png"), output.ReasonNone)
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "sprite_2.png"), output.ReasonDuplicate)
	earlier, _ := os.ReadFile(filepath.Join(out, "sprite.png"))
	earlierRenamed, _ := os.ReadFile(filepath.Join(out, "sprite_2.png"))

	var events recorder
	summary, err = Run(context.Background(), request, events.observe)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite_3.png"), output.ReasonExists)
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "sprite_4.png"), output.ReasonExists)
	requireContent(t, filepath.Join(out, "sprite.png"), earlier)
	requireContent(t, filepath.Join(out, "sprite_2.png"), earlierRenamed)
	requireFiles(t, out, "sprite.png", "sprite_2.png", "sprite_3.png", "sprite_4.png")
}

// TestRunSkipLeavesExistingDestinations pins AC-R09: an existing destination
// is left untouched and its item Skipped rather than Completed; of two
// sources with one name only the first is written; and a rerun skips every
// item.
func TestRunSkipLeavesExistingDestinations(t *testing.T) {
	out := t.TempDir()
	existing := filepath.Join(out, "sprite.png")
	if err := os.WriteFile(existing, []byte("earlier result"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := []Input{
		inspected(t, writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))),
		inspected(t, writeSource(t, "pair.png", encodePNG(t, halfTransparent()))),
		inspected(t, writeSource(t, "pair.png", encodePNG(t, quadrants(32, 32)))),
	}
	request := newRequest(inputs, out, output.CollisionSkip)
	var events recorder
	summary, err := run(context.Background(), request, events.observe, testConfig(4))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateSkipped, existing, output.ReasonExists)
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "pair.png"), output.ReasonNone)
	requireStatus(t, summary, 2, StateSkipped, filepath.Join(out, "pair.png"), output.ReasonDuplicate)
	if got := events.states(2); !slices.Equal(got, []State{StateSkipped}) {
		t.Fatalf("deferred duplicate went through %v", got)
	}
	requireContent(t, existing, []byte("earlier result"))
	if result := decodeResult(t, filepath.Join(out, "pair.png")); result.Width != 16 || result.Height != 8 {
		t.Fatalf("the first pair source did not keep the name: %+v", result.Info)
	}
	written, _ := os.ReadFile(filepath.Join(out, "pair.png"))

	summary, err = Run(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	for index, destination := range []string{existing, filepath.Join(out, "pair.png"), filepath.Join(out, "pair.png")} {
		requireStatus(t, summary, index, StateSkipped, destination, output.ReasonExists)
	}
	requireContent(t, filepath.Join(out, "pair.png"), written)
	requireFiles(t, out, "sprite.png", "pair.png")
}

// TestRunSkipHandsTheNameToTheNextDuplicate: under Skip, when the source
// that was to write a shared name fails, the next source with that name is
// processed in its place, and so on, and the rest are Skipped once the name
// is written (RSZ-024).
func TestRunSkipHandsTheNameToTheNextDuplicate(t *testing.T) {
	inputs := []Input{
		damagedSinceChecked(t, "shared.png"),
		damagedSinceChecked(t, "shared.png"),
		inspected(t, writeSource(t, "shared.png", encodePNG(t, halfTransparent()))),
		inspected(t, writeSource(t, "shared.png", encodePNG(t, quadrants(32, 32)))),
	}
	out := t.TempDir()
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionSkip), events.observe, testConfig(4))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	shared := filepath.Join(out, "shared.png")
	requireStatus(t, summary, 0, StateFailed, shared, output.ReasonNone)
	requireStatus(t, summary, 1, StateFailed, shared, output.ReasonNone)
	requireStatus(t, summary, 2, StateCompleted, shared, output.ReasonNone)
	requireStatus(t, summary, 3, StateSkipped, shared, output.ReasonDuplicate)
	for _, index := range []int{1, 2} {
		if got := events.states(index); len(got) != 2 || got[0] != StateProcessing {
			t.Fatalf("item %d went through %v, want processing in its turn", index, got)
		}
	}
	if result := decodeResult(t, shared); result.Width != 16 || result.Height != 8 {
		t.Fatalf("the shared name holds %+v, want the third source's result", result.Info)
	}
	requireFiles(t, out, "shared.png")
}

// TestRunHandlesANameTakenAfterPlanning: a file another program writes at a
// planned name before the item saves it is never replaced. It is an existing
// destination, which Skip leaves untouched, so the item and the duplicate
// deferred to it end Skipped; under Rename, and under Overwrite, which was
// never confirmed for that file, the item is planned again and saved under
// the first numbered name no file or other item has.
func TestRunHandlesANameTakenAfterPlanning(t *testing.T) {
	for _, policy := range []output.Collision{output.CollisionSkip, output.CollisionRename, output.CollisionOverwrite} {
		inputs := []Input{
			inspected(t, writeSource(t, "shared.png", encodePNG(t, halfTransparent()))),
			inspected(t, writeSource(t, "shared.png", encodePNG(t, halfTransparent()))),
		}
		out := t.TempDir()
		cfg := testConfig(4)
		planned := false
		cfg.prepare = func(ctx context.Context, request output.Request) (output.Plan, error) {
			plan, err := output.Prepare(ctx, request)
			if err == nil && !planned {
				planned = true
				err = os.WriteFile(plan.Items[0].Destination, []byte("another program's"), 0o644)
			}
			return plan, err
		}
		var events recorder
		summary, err := run(context.Background(), newRequest(inputs, out, policy), events.observe, cfg)
		if err != nil {
			t.Fatalf("%v: Run: %v", policy, err)
		}
		events.check(t, summary)
		shared := filepath.Join(out, "shared.png")
		if policy == output.CollisionSkip {
			requireStatus(t, summary, 0, StateSkipped, shared, output.ReasonExists)
			requireStatus(t, summary, 1, StateSkipped, shared, output.ReasonExists)
			requireFiles(t, out, "shared.png")
		} else {
			requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "shared_3.png"), output.ReasonExists)
			requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "shared_2.png"), output.ReasonDuplicate)
			if got := events.states(0); !slices.Equal(got, []State{StateProcessing, StateProcessing, StateCompleted}) {
				t.Fatalf("%v: the planned-again item went through %v", policy, got)
			}
			requireFiles(t, out, "shared.png", "shared_2.png", "shared_3.png")
		}
		requireContent(t, shared, []byte("another program's"))
	}
}

// TestRunGivesUpOnANameTakenAgainAndAgain: an item whose every new name is
// taken before it saves is planned again a bounded number of times and then
// fails with ErrDestinationTaken.
func TestRunGivesUpOnANameTakenAgainAndAgain(t *testing.T) {
	inputs := []Input{listed(t, "sprite.png", 1)}
	cfg := testConfig(1)
	var destinations []string
	cfg.process = func(_ context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
		destinations = append(destinations, filepath.Base(item.Destination))
		if err := os.WriteFile(item.Destination, []byte("another program's"), 0o644); err != nil {
			t.Fatal(err)
		}
		return "", &output.Error{Kind: output.KindChanged, Op: "rename result", Err: output.ErrDestinationTaken}
	}
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, t.TempDir(), output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if status := summary.Items[0]; status.State != StateFailed || !errors.Is(status.Err, output.ErrDestinationTaken) {
		t.Fatalf("item = %+v, want ErrDestinationTaken", status)
	}
	if want := []string{"sprite.png", "sprite_2.png", "sprite_3.png", "sprite_4.png"}; !slices.Equal(destinations, want) {
		t.Fatalf("tried %q, want %q", destinations, want)
	}
}

// TestRunOverwriteReplacesOnlyConfirmedDestinations pins AC-R10 and AC-R07: a
// confirmed Overwrite job replaces an existing result, while a source in the
// output folder is never overwritten and its result is renamed instead.
func TestRunOverwriteReplacesOnlyConfirmedDestinations(t *testing.T) {
	out := t.TempDir()
	earlier := filepath.Join(out, "sprite.png")
	if err := os.WriteFile(earlier, []byte("earlier result"), 0o644); err != nil {
		t.Fatal(err)
	}
	inFolderContent := encodePNG(t, quadrants(32, 32))
	inFolder := filepath.Join(out, "original.png")
	if err := os.WriteFile(inFolder, inFolderContent, 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := []Input{
		inspected(t, writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))),
		inspected(t, inFolder),
	}
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionOverwrite), events.observe, testConfig(4))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "original_2.png"), output.ReasonSource)
	requireContent(t, inFolder, inFolderContent)
	if summary.Items[0].Reason == output.ReasonUnverified {
		t.Skip("replacing needs file identities, read on Windows only")
	}
	requireStatus(t, summary, 0, StateCompleted, earlier, output.ReasonNone)
	if result := decodeResult(t, earlier); result.Width != 16 || result.Height != 8 {
		t.Fatalf("the replaced file holds %+v", result.Info)
	}
	requireFiles(t, out, "sprite.png", "original.png", "original_2.png")
}

// TestRunFailsAnInvalidJobBeforeAnything pins RSZ-040: an invalid preset,
// JPEG option, folder, policy confirmation, or a job with nothing it can
// resize fails as a whole with the step's own error, before the output
// folder is created or any event is reported; JPEG options that no result
// uses do not fail it.
func TestRunFailsAnInvalidJobBeforeAnything(t *testing.T) {
	valid := inspected(t, writeSource(t, "sprite.png", encodePNG(t, halfTransparent())))
	unusable := Input{Path: writeSource(t, "notes.png", []byte("text")), Err: errors.New("unsupported")}
	cases := []struct {
		name   string
		change func(*Request)
		check  func(error) bool
	}{
		{"invalid preset", func(r *Request) { r.Settings.Target.Width = 0 }, func(err error) bool {
			var resizeErr *resize.Error
			return errors.As(err, &resizeErr) && resizeErr.Kind == resize.KindInvalid
		}},
		{"invalid JPEG quality", func(r *Request) { r.Format = output.FormatJPEG; r.Settings.JPEGQuality = 0 }, func(err error) bool {
			return errors.Is(err, encode.ErrQuality)
		}},
		{"translucent JPEG background", func(r *Request) { r.Format = output.FormatJPEG; r.Settings.Background.A = 0 }, func(err error) bool {
			return errors.Is(err, encode.ErrBackground)
		}},
		{"relative folder", func(r *Request) { r.Folder = "out" }, func(err error) bool {
			var outputErr *output.Error
			return errors.As(err, &outputErr) && outputErr.Kind == output.KindValidation && errors.Is(err, output.ErrFolderNotAbsolute)
		}},
		{"unconfirmed overwrite", func(r *Request) { r.Collision = output.CollisionOverwrite; r.ConfirmOverwrite = false }, func(err error) bool {
			return errors.Is(err, output.ErrOverwriteUnconfirmed)
		}},
		{"nothing to resize", func(r *Request) { r.Inputs = []Input{unusable} }, func(err error) bool {
			return errors.Is(err, output.ErrNoSources)
		}},
	}
	for _, tc := range cases {
		out := filepath.Join(t.TempDir(), "out")
		request := newRequest([]Input{valid, unusable}, out, output.CollisionRename)
		tc.change(&request)
		var events recorder
		cfg := testConfig(4)
		cfg.process = func(context.Context, output.Item, Settings, reservation) (string, error) {
			t.Errorf("%s: an item was processed", tc.name)
			return "", nil
		}
		summary, err := run(context.Background(), request, events.observe, cfg)
		if !tc.check(err) {
			t.Fatalf("%s: Run error = %v", tc.name, err)
		}
		if summary.Items != nil || len(events.events) != 0 {
			t.Fatalf("%s: summary %+v and %d events, want none", tc.name, summary, len(events.events))
		}
		if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("%s: the output folder was created: %v", tc.name, statErr)
		}
	}

	// PNG results ignore the JPEG options.
	out := t.TempDir()
	request := newRequest([]Input{valid}, out, output.CollisionRename)
	request.Settings.JPEGQuality = 0
	if summary, err := Run(context.Background(), request, nil); err != nil || summary.Progress.Completed != 1 {
		t.Fatalf("PNG job with an unused JPEG quality: %+v, %v", summary.Progress, err)
	}
}

// TestRunCancelledBeforeItStarts: a job cancelled before it starts cancels
// every item it could resize without planning, creating, or processing
// anything, while an item it could never resize still ends Failed.
func TestRunCancelledBeforeItStarts(t *testing.T) {
	inputs := []Input{
		inspected(t, writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))),
		{Path: writeSource(t, "notes.png", []byte("text")), Err: errors.New("unsupported")},
	}
	out := filepath.Join(t.TempDir(), "out")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := testConfig(4)
	cfg.prepare = func(context.Context, output.Request) (output.Plan, error) {
		t.Error("the job was planned")
		return output.Plan{}, nil
	}
	var events recorder
	summary, err := run(ctx, newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateCancelled, "", output.ReasonNone)
	requireStatus(t, summary, 1, StateFailed, "", output.ReasonNone)
	if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("the output folder was created: %v", statErr)
	}
}

// TestRunCancelledWhilePlanning: a cancellation output.Prepare reports cancels
// every item, and nothing is processed.
func TestRunCancelledWhilePlanning(t *testing.T) {
	inputs := []Input{
		inspected(t, writeSource(t, "a.png", encodePNG(t, halfTransparent()))),
		inspected(t, writeSource(t, "b.png", encodePNG(t, halfTransparent()))),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testConfig(4)
	cfg.prepare = func(planCtx context.Context, request output.Request) (output.Plan, error) {
		cancel()
		return output.Prepare(planCtx, request)
	}
	cfg.process = func(context.Context, output.Item, Settings, reservation) (string, error) {
		t.Error("an item was processed")
		return "", nil
	}
	var events recorder
	summary, err := run(ctx, newRequest(inputs, t.TempDir(), output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if summary.Progress.Cancelled != 2 {
		t.Fatalf("summary counts = %+v, want both cancelled", summary.Progress)
	}
}

// TestRunCancellationKeepsCompletedResults pins AC-R13 and RSZ-045: once the
// job is cancelled no further item starts, the item in flight stops without
// leaving a file, the waiting items end Cancelled, and the completed result
// stays valid.
func TestRunCancellationKeepsCompletedResults(t *testing.T) {
	var inputs []Input
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		inputs = append(inputs, inspected(t, writeSource(t, name, encodePNG(t, halfTransparent()))))
	}
	out := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var processed atomic.Int32
	cfg := testConfig(1)
	cfg.process = func(ctx context.Context, item output.Item, settings Settings, _ reservation) (string, error) {
		processed.Add(1)
		return Process(ctx, item, settings)
	}
	events := recorder{then: func(event Event) {
		if event.Index == 1 && event.Status.State == StateProcessing {
			cancel()
		}
	}}
	summary, err := run(ctx, newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "a.png"), output.ReasonNone)
	requireStatus(t, summary, 1, StateCancelled, filepath.Join(out, "b.png"), output.ReasonNone)
	requireStatus(t, summary, 2, StateCancelled, filepath.Join(out, "c.png"), output.ReasonNone)
	requireStatus(t, summary, 3, StateCancelled, filepath.Join(out, "d.png"), output.ReasonNone)
	if got := processed.Load(); got != 2 {
		t.Fatalf("%d items were processed, want 2", got)
	}
	if got := events.states(2); !slices.Equal(got, []State{StateCancelled}) {
		t.Fatalf("a waiting item went through %v", got)
	}
	requireFiles(t, out, "a.png")
	if result := decodeResult(t, filepath.Join(out, "a.png")); result.Width != 16 || result.Height != 8 {
		t.Fatalf("the completed result is %+v", result.Info)
	}
}

// TestRunWaitsForItemsInFlight: Run returns only once every item in flight
// has stopped; an item that had already passed its last cancellation check
// ends Completed, and one that observed the cancellation ends Cancelled, as
// do the items deferred to it.
func TestRunWaitsForItemsInFlight(t *testing.T) {
	inputs := []Input{
		listed(t, "finishing.png", 1),
		listed(t, "stopping.png", 1),
		listed(t, "stopping.png", 1),
		listed(t, "waiting.png", 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var running sync.WaitGroup
	running.Add(2)
	var returned atomic.Int32
	cfg := testConfig(2)
	cfg.process = func(ctx context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
		defer returned.Add(1)
		running.Done()
		<-ctx.Done()
		// Let Run observe the cancellation before the items return.
		time.Sleep(20 * time.Millisecond)
		if filepath.Base(item.Source) == "finishing.png" {
			return item.Destination, nil
		}
		return "", &output.Error{Kind: output.KindCanceled, Op: "write result", Err: ctx.Err()}
	}
	go func() {
		running.Wait()
		cancel()
	}()
	var events recorder
	summary, err := run(ctx, newRequest(inputs, t.TempDir(), output.CollisionSkip), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := returned.Load(); got != 2 {
		t.Fatalf("Run returned with %d of 2 items finished", got)
	}
	events.check(t, summary)
	want := []State{StateCompleted, StateCancelled, StateCancelled, StateCancelled}
	for index, state := range want {
		if summary.Items[index].State != state {
			t.Fatalf("item %d = %v, want %v", index, summary.Items[index].State, state)
		}
	}
}

// TestRunCancelsTheSuccessorOfAFailureAfterCancellation: when the item a
// duplicate defers to fails after the job was cancelled, the duplicate is
// not started but Cancelled.
func TestRunCancelsTheSuccessorOfAFailureAfterCancellation(t *testing.T) {
	inputs := []Input{listed(t, "shared.png", 1), listed(t, "shared.png", 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testConfig(2)
	calls := 0
	cfg.process = func(context.Context, output.Item, Settings, reservation) (string, error) {
		calls++
		cancel()
		return "", &output.Error{Kind: output.KindWrite, Op: "write result", Err: fs.ErrPermission}
	}
	var events recorder
	summary, err := run(ctx, newRequest(inputs, t.TempDir(), output.CollisionSkip), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if calls != 1 || summary.Items[0].State != StateFailed || summary.Items[1].State != StateCancelled {
		t.Fatalf("%d calls, items %v and %v, want the first failed and its successor cancelled", calls, summary.Items[0].State, summary.Items[1].State)
	}
}

// TestRunIsolatesItemFailures pins RSZ-044, NFR-021, and AC-R14: a write
// failure and panics fail their own items, and the remaining items are still
// resized and saved.
func TestRunIsolatesItemFailures(t *testing.T) {
	cause := errors.New("a defect")
	var inputs []Input
	for _, name := range []string{"panics.png", "first.png", "unwritable.png", "error.png", "last.png"} {
		inputs = append(inputs, inspected(t, writeSource(t, name, encodePNG(t, halfTransparent()))))
	}
	out := t.TempDir()
	cfg := testConfig(2)
	cfg.process = func(ctx context.Context, item output.Item, settings Settings, _ reservation) (string, error) {
		switch filepath.Base(item.Source) {
		case "panics.png":
			panic("boom")
		case "error.png":
			panic(cause)
		case "unwritable.png":
			return "", &output.Error{Kind: output.KindWrite, Op: "create temporary file", Err: fs.ErrPermission}
		}
		return Process(ctx, item, settings)
	}
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	panicked := requireStatus(t, summary, 0, StateFailed, filepath.Join(out, "panics.png"), output.ReasonNone)
	if !errors.Is(panicked.Err, ErrUnexpected) {
		t.Fatalf("panic error = %v, want ErrUnexpected", panicked.Err)
	}
	unwritable := requireStatus(t, summary, 2, StateFailed, filepath.Join(out, "unwritable.png"), output.ReasonNone)
	var outputErr *output.Error
	if !errors.As(unwritable.Err, &outputErr) || outputErr.Kind != output.KindWrite || !errors.Is(unwritable.Err, fs.ErrPermission) {
		t.Fatalf("write failure = %v", unwritable.Err)
	}
	if failed := requireStatus(t, summary, 3, StateFailed, filepath.Join(out, "error.png"), output.ReasonNone); !errors.Is(failed.Err, ErrUnexpected) || !errors.Is(failed.Err, cause) {
		t.Fatalf("error panic = %v, want ErrUnexpected wrapping the panic's error", failed.Err)
	}
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "first.png"), output.ReasonNone)
	requireStatus(t, summary, 4, StateCompleted, filepath.Join(out, "last.png"), output.ReasonNone)
	requireFiles(t, out, "first.png", "last.png")
}

// TestRunReportsASavedResultsLeftoverFile: a result saved with a file left
// behind is Completed, carrying the error that names the file.
func TestRunReportsASavedResultsLeftoverFile(t *testing.T) {
	inputs := []Input{listed(t, "sprite.png", 1)}
	leftover := &output.Error{Kind: output.KindWrite, Op: "remove temporary file", Err: fmt.Errorf("%w: %w", output.ErrLeftBehind, fs.ErrPermission)}
	cfg := testConfig(1)
	cfg.process = func(_ context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
		return item.Destination, leftover
	}
	out := t.TempDir()
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionRename), nil, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if status := requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite.png"), output.ReasonNone); status.Err != leftover {
		t.Fatalf("error = %v, want the leftover file's", status.Err)
	}
}

// TestRunReportsATemporaryFileLeftBehindWhateverTheItemEndsIn: a write that
// could not remove its temporary file joins that failure to the error that
// ended it, which stays with the item when it ends Cancelled or Skipped, and
// when it is planned again and saved under a new name, while an item that
// stopped cleanly carries no error.
func TestRunReportsATemporaryFileLeftBehindWhateverTheItemEndsIn(t *testing.T) {
	removal := &output.Error{Kind: output.KindWrite, Op: "remove temporary file", Err: fmt.Errorf("%w: %w", output.ErrLeftBehind, fs.ErrPermission)}
	taken := &output.Error{Kind: output.KindChanged, Op: "save result", Err: output.ErrDestinationTaken}
	requireLeftover := func(t *testing.T, status Status) {
		t.Helper()
		var outputErr *output.Error
		if !errors.Is(status.Err, removal) || !errors.As(status.Err, &outputErr) {
			t.Fatalf("%v item error = %v, want it to name the temporary file left behind", status.State, status.Err)
		}
	}

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg := testConfig(2)
		cfg.process = func(ctx context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
			cancel()
			stopped := &output.Error{Kind: output.KindCanceled, Op: "write result", Err: ctx.Err()}
			if filepath.Base(item.Source) == "clean.png" {
				return "", stopped
			}
			return "", errors.Join(stopped, removal)
		}
		inputs := []Input{listed(t, "leaves.png", 1), listed(t, "clean.png", 1)}
		summary, err := run(ctx, newRequest(inputs, t.TempDir(), output.CollisionRename), nil, cfg)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if summary.Items[0].State != StateCancelled || summary.Items[1].State != StateCancelled {
			t.Fatalf("items = %+v, want both cancelled", summary.Items)
		}
		requireLeftover(t, summary.Items[0])
		if summary.Items[1].Err != nil {
			t.Fatalf("a cleanly cancelled item carries %v", summary.Items[1].Err)
		}
	})

	t.Run("cancelled by a panic", func(t *testing.T) {
		// The contained panic's error wraps two errors, as a joined one does,
		// but names no file left behind.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg := testConfig(1)
		cfg.process = func(ctx context.Context, _ output.Item, _ Settings, _ reservation) (string, error) {
			cancel()
			panic(fmt.Errorf("stopped: %w", ctx.Err()))
		}
		summary, err := run(ctx, newRequest([]Input{listed(t, "panics.png", 1)}, t.TempDir(), output.CollisionRename), nil, cfg)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if status := summary.Items[0]; status.State != StateCancelled || status.Err != nil {
			t.Fatalf("item = %+v, want cancelled with no file left behind", status)
		}
	})

	t.Run("skipped", func(t *testing.T) {
		cfg := testConfig(1)
		cfg.process = func(context.Context, output.Item, Settings, reservation) (string, error) {
			return "", errors.Join(taken, removal)
		}
		out := t.TempDir()
		summary, err := run(context.Background(), newRequest([]Input{listed(t, "sprite.png", 1)}, out, output.CollisionSkip), nil, cfg)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		requireLeftover(t, requireStatus(t, summary, 0, StateSkipped, filepath.Join(out, "sprite.png"), output.ReasonExists))
	})

	t.Run("planned again", func(t *testing.T) {
		cfg := testConfig(1)
		cfg.process = func(_ context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
			if filepath.Base(item.Destination) == "sprite.png" {
				if err := os.WriteFile(item.Destination, []byte("another program's"), 0o644); err != nil {
					t.Fatal(err)
				}
				return "", errors.Join(taken, removal)
			}
			return item.Destination, nil
		}
		out := t.TempDir()
		var events recorder
		summary, err := run(context.Background(), newRequest([]Input{listed(t, "sprite.png", 1)}, out, output.CollisionRename), events.observe, cfg)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		events.check(t, summary)
		requireLeftover(t, requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite_2.png"), output.ReasonExists))
	})

	t.Run("planned again and failed", func(t *testing.T) {
		// The error that ended the last attempt comes first, as Status.Err
		// documents, so the view classifies the failure by it rather than by
		// the earlier attempt's file left behind.
		full := &output.Error{Kind: output.KindWrite, Op: "write result", Err: output.ErrDiskFull}
		cfg := testConfig(1)
		cfg.process = func(_ context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
			if filepath.Base(item.Destination) == "sprite.png" {
				if err := os.WriteFile(item.Destination, []byte("another program's"), 0o644); err != nil {
					t.Fatal(err)
				}
				return "", errors.Join(taken, removal)
			}
			return "", full
		}
		out := t.TempDir()
		summary, err := run(context.Background(), newRequest([]Input{listed(t, "sprite.png", 1)}, out, output.CollisionRename), nil, cfg)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		status := requireStatus(t, summary, 0, StateFailed, filepath.Join(out, "sprite_2.png"), output.ReasonNone)
		requireLeftover(t, status)
		joined, ok := status.Err.(interface{ Unwrap() []error })
		if !ok || len(joined.Unwrap()) < 2 || joined.Unwrap()[0] != full {
			t.Fatalf("error = %v, want the last attempt's failure first, then the file left behind", status.Err)
		}
	})
}

// TestRunKeepsTheReasonOfAnItemPlannedAgain: a duplicate renamed at planning
// and planned again after another program took its numbered name is still
// reported as a duplicate, not as a source, though its natural name is now
// protected as another item's destination.
func TestRunKeepsTheReasonOfAnItemPlannedAgain(t *testing.T) {
	inputs := []Input{listed(t, "sprite.png", 1), listed(t, "sprite.png", 1)}
	out := t.TempDir()
	cfg := testConfig(1)
	cfg.process = func(_ context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
		if filepath.Base(item.Destination) == "sprite_2.png" {
			if err := os.WriteFile(item.Destination, []byte("another program's"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "", &output.Error{Kind: output.KindChanged, Op: "save result", Err: output.ErrDestinationTaken}
		}
		return item.Destination, nil
	}
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite.png"), output.ReasonNone)
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "sprite_3.png"), output.ReasonDuplicate)
}

// TestRunStartsOverASourceThatGrewSinceItsCheck pins NFR-014 for a source
// rewritten after its content check: one that now needs more to decode is
// not decoded within the estimate it no longer fits, and one rewritten to
// another format that decodes within it but needs more for its resized image
// is not resized within the memory its check sized; either is reported
// Pending again and processed again alone, with the most any source needs
// set aside, and its result reflects the source as it now is (RSZ-049).
func TestRunStartsOverASourceThatGrewSinceItsCheck(t *testing.T) {
	cases := []struct {
		name    string
		rewrite func(t *testing.T) Input
		result  string
	}{
		{"needs more to decode", func(t *testing.T) Input {
			grown := inspected(t, writeSource(t, "grown.png", encodePNG(t, quadrants(4, 4))))
			if err := os.WriteFile(grown.Path, encodePNG(t, quadrants(64, 32)), 0o644); err != nil {
				t.Fatal(err)
			}
			return grown
		}, "grown.png"},
		{"needs more to resize", func(t *testing.T) Input {
			// Checked as a JPEG of the same shape and decoded size, whose
			// result is estimated at 4 bytes a pixel, and now a PNG, whose
			// result may take 8.
			changed := inspected(t, writeSource(t, "grown.png", encodePNG(t, quadrants(64, 32))))
			changed.Info.Format = decode.FormatJPEG
			return changed
		}, "grown.jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grown := tc.rewrite(t)
			inputs := []Input{
				grown,
				inspected(t, writeSource(t, "beside.png", encodePNG(t, halfTransparent()))),
				inspected(t, writeSource(t, "after.png", encodePNG(t, halfTransparent()))),
			}
			out := t.TempDir()
			cfg := testConfig(2)
			cfg.budget = memoryBudget
			var mu sync.Mutex
			running := 0
			var allowances []uint64
			cfg.process = func(ctx context.Context, item output.Item, settings Settings, share reservation) (string, error) {
				mu.Lock()
				running++
				if item.Source == grown.Path {
					allowances = append(allowances, share.allowance)
					if len(allowances) == 2 && running != 1 {
						t.Errorf("the grown source ran again beside %d other items", running-1)
					}
				}
				mu.Unlock()
				defer func() {
					mu.Lock()
					running--
					mu.Unlock()
				}()
				return processWithin(ctx, item, settings, share)
			}
			var events recorder
			summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			events.check(t, summary)
			for index, name := range []string{tc.result, "beside.png", "after.png"} {
				requireStatus(t, summary, index, StateCompleted, filepath.Join(out, name), output.ReasonNone)
			}
			if want := []uint64{grown.Info.DecodedBytes, decode.MaxDecodedBytes}; !slices.Equal(allowances, want) {
				t.Fatalf("the grown source was decoded within %v, want %v", allowances, want)
			}
			if got := events.states(0); !slices.Equal(got, []State{StateProcessing, StatePending, StateProcessing, StateCompleted}) {
				t.Fatalf("the grown source went through %v", got)
			}
			if result := decodeResult(t, filepath.Join(out, tc.result)); result.Width != 16 || result.Height != 8 {
				t.Fatalf("the grown source's result is %+v, want its new shape", result.Info)
			}
		})
	}
}

// TestProcessWithinFailsASourceThatOutgrewItsReservation: a source that
// decodes within its allowance but would need more in all than the item
// holds fails with errOutgrown before it is resized, and one that fits is
// processed.
func TestProcessWithinFailsASourceThatOutgrewItsReservation(t *testing.T) {
	input := inspected(t, writeSource(t, "sprite.png", encodePNG(t, quadrants(64, 32))))
	target := resize.Target{Width: 16, Height: 16, Mode: preset.ResizeFit}
	need := itemBytes(input.Info, input.Info.DecodedBytes, target)
	out := t.TempDir()
	item := output.Item{Source: input.Path, Format: decode.FormatPNG, Action: output.ActionCreate, Destination: filepath.Join(out, "sprite.png"), DuplicateOf: -1}
	process := func(held uint64) error {
		_, err := processWithin(context.Background(), item, settings(16, 16, preset.ResizeFit, white), reservation{allowance: input.Info.DecodedBytes, held: held})
		return err
	}
	if err := process(need - 1); !errors.Is(err, errOutgrown) || !outgrown(err) {
		t.Fatalf("within %d of the %d bytes it needs: %v, want errOutgrown", need-1, need, err)
	}
	requireFiles(t, out)
	if err := process(need); err != nil {
		t.Fatalf("within the %d bytes it needs: %v", need, err)
	}
	requireFiles(t, out, "sprite.png")
}

// TestRunCancelsANameTakenOnceCancelled: an item whose name another program
// took, which would have been planned again, ends Cancelled once the job is
// cancelled rather than Failed (RSZ-045).
func TestRunCancelsANameTakenOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testConfig(1)
	calls := 0
	cfg.process = func(context.Context, output.Item, Settings, reservation) (string, error) {
		calls++
		cancel()
		return "", &output.Error{Kind: output.KindChanged, Op: "save result", Err: output.ErrDestinationTaken}
	}
	out := t.TempDir()
	var events recorder
	summary, err := run(ctx, newRequest([]Input{listed(t, "sprite.png", 1)}, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if status := requireStatus(t, summary, 0, StateCancelled, filepath.Join(out, "sprite.png"), output.ReasonNone); status.Err != nil || calls != 1 {
		t.Fatalf("item = %+v after %d calls, want cancelled without an error after one", status, calls)
	}
}

// TestRunFailsTheRestOnceTheOutputFolderIsLost pins UX-010: once an item
// fails to write because the output folder has gone, the items still
// waiting, including a Skip duplicate promoted in the failed item's place,
// end Failed with the folder's error without being decoded, while a write
// failure in a folder that is still there fails only its own item.
func TestRunFailsTheRestOnceTheOutputFolderIsLost(t *testing.T) {
	inputs := []Input{listed(t, "shared.png", 1), listed(t, "shared.png", 1), listed(t, "other.png", 1)}
	out := filepath.Join(t.TempDir(), "out")
	cfg := testConfig(1)
	calls := 0
	cfg.process = func(_ context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
		calls++
		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
		return "", &output.Error{Kind: output.KindWrite, Op: "create temporary file", Err: &fs.PathError{Op: "open", Path: item.Destination, Err: fs.ErrNotExist}}
	}
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionSkip), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if calls != 1 {
		t.Fatalf("%d items were processed, want only the one that found the folder gone", calls)
	}
	first := requireStatus(t, summary, 0, StateFailed, filepath.Join(out, "shared.png"), output.ReasonNone)
	var outputErr *output.Error
	if !errors.As(first.Err, &outputErr) || outputErr.Kind != output.KindWrite {
		t.Fatalf("the item that failed to write carries %v, want its own write failure", first.Err)
	}
	for index, name := range map[int]string{1: "shared.png", 2: "other.png"} {
		status := requireStatus(t, summary, index, StateFailed, filepath.Join(out, name), output.ReasonNone)
		if !errors.As(status.Err, &outputErr) || outputErr.Kind != output.KindFolder || !errors.Is(status.Err, fs.ErrNotExist) {
			t.Fatalf("item %d carries %v, want the lost folder's error", index, status.Err)
		}
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("the lost output folder was created again: %v", statErr)
	}
}

// TestRunDoesNotPlanAgainIntoALostFolder pins UX-010 for an item whose name
// another program took, found once its output folder has gone: it is not
// planned again, which would create the folder anew, but ends Failed with the
// folder's error, and so do the items still waiting, without being decoded.
func TestRunDoesNotPlanAgainIntoALostFolder(t *testing.T) {
	inputs := []Input{listed(t, "taken.png", 1), listed(t, "other.png", 1)}
	out := filepath.Join(t.TempDir(), "out")
	cfg := testConfig(1)
	calls := 0
	cfg.process = func(context.Context, output.Item, Settings, reservation) (string, error) {
		calls++
		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
		return "", &output.Error{Kind: output.KindChanged, Op: "check destination", Err: output.ErrDestinationTaken}
	}
	var events recorder
	summary, err := run(context.Background(), newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if calls != 1 {
		t.Fatalf("%d items were processed, want only the one whose name was taken", calls)
	}
	for index, name := range map[int]string{0: "taken.png", 1: "other.png"} {
		status := requireStatus(t, summary, index, StateFailed, filepath.Join(out, name), output.ReasonNone)
		if !hasKind(status.Err, output.KindFolder) || !errors.Is(status.Err, fs.ErrNotExist) {
			t.Fatalf("item %d carries %v, want the lost folder's error", index, status.Err)
		}
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("the lost output folder was created again: %v", statErr)
	}
}

// TestRunReportsReasonsOnlyForSavedAndSkippedItems: Status.Reason says why a
// Completed item has a numbered name, or why an item was Skipped, and is
// output.ReasonNone in every other state, for an item planned under a
// numbered name too.
func TestRunReportsReasonsOnlyForSavedAndSkippedItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var inputs []Input
	for range 5 {
		inputs = append(inputs, listed(t, "sprite.png", 1))
	}
	cfg := testConfig(1)
	cfg.process = func(ctx context.Context, item output.Item, _ Settings, _ reservation) (string, error) {
		switch filepath.Base(item.Destination) {
		case "sprite_3.png":
			return "", errors.New("the source broke")
		case "sprite_4.png":
			cancel()
			return "", &output.Error{Kind: output.KindCanceled, Op: "write result", Err: ctx.Err()}
		}
		return item.Destination, nil
	}
	out := t.TempDir()
	var events recorder
	summary, err := run(ctx, newRequest(inputs, out, output.CollisionRename), events.observe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	requireStatus(t, summary, 0, StateCompleted, filepath.Join(out, "sprite.png"), output.ReasonNone)
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "sprite_2.png"), output.ReasonDuplicate)
	requireStatus(t, summary, 2, StateFailed, filepath.Join(out, "sprite_3.png"), output.ReasonNone)
	requireStatus(t, summary, 3, StateCancelled, filepath.Join(out, "sprite_4.png"), output.ReasonNone)
	requireStatus(t, summary, 4, StateCancelled, filepath.Join(out, "sprite_5.png"), output.ReasonNone)
	for _, event := range events.events {
		if state := event.Status.State; state != StateCompleted && state != StateSkipped && event.Status.Reason != output.ReasonNone {
			t.Fatalf("item %d reported %v with the reason %v", event.Index, state, event.Status.Reason)
		}
	}
}

// TestRunCollectsBeforeHandingOnMemory pins NFR-014 for the memory finished
// items drop: once an item during which the heap allocated 64 MiB or more
// ends, or the small items that ended since the last collection allocated
// that much together, garbage is collected before the next item can start in
// that memory, while an item that allocated nothing, though it held that
// much of the budget, as one whose name was taken, does not collect.
func TestRunCollectsBeforeHandingOnMemory(t *testing.T) {
	target := resize.Target{Width: 16, Height: 16, Mode: preset.ResizeFit}
	allocates := map[string]uint64{
		"large1.png": 100 << 20,
		"large2.png": 100 << 20,
		"taken.png":  0,
		"small1.png": 30 << 20,
		"small2.png": 30 << 20,
		"small3.png": 30 << 20,
	}
	var inputs []Input
	for _, name := range []string{"large1.png", "large2.png", "taken.png", "small1.png", "small2.png", "small3.png"} {
		// Each item holds 100 MiB of the budget by its estimate.
		inputs = append(inputs, listed(t, name, 100<<20))
	}
	// The items run one at a time, so each next one starts in the memory the
	// last one held, and the heap's count is each item's own.
	cfg := testConfig(1)
	var mu sync.Mutex
	var steps []string
	note := func(step string) {
		mu.Lock()
		steps = append(steps, step)
		mu.Unlock()
	}
	var heap atomic.Uint64
	cfg.allocated = heap.Load
	cfg.collect = func() { note("collect") }
	cfg.process = func(_ context.Context, item output.Item, _ Settings, share reservation) (string, error) {
		name := filepath.Base(item.Source)
		if share.held < 100<<20 {
			t.Errorf("%s holds %d bytes of the budget, want its estimate", name, share.held)
		}
		note("start " + name)
		heap.Add(allocates[name])
		note("end " + name)
		if name == "taken.png" {
			return "", &output.Error{Kind: output.KindChanged, Op: "check destination", Err: output.ErrDestinationTaken}
		}
		return item.Destination, nil
	}
	request := newRequest(inputs, t.TempDir(), output.CollisionSkip)
	request.Settings.Target = target
	summary, err := run(context.Background(), request, nil, cfg)
	if err != nil || summary.Progress.Completed != len(inputs)-1 || summary.Progress.Skipped != 1 {
		t.Fatalf("Run: %+v, %v", summary.Progress, err)
	}
	want := []string{
		"start large1.png", "end large1.png", "collect",
		"start large2.png", "end large2.png", "collect",
		"start taken.png", "end taken.png",
		"start small1.png", "end small1.png",
		"start small2.png", "end small2.png",
		"start small3.png", "end small3.png", "collect",
	}
	if !slices.Equal(steps, want) {
		t.Fatalf("steps = %q, want %q", steps, want)
	}
}

// TestRunFailsAnUncheckedSourceAlone pins RSZ-044 for an input listed
// without an error or a checked format, as one whose check has not finished:
// it ends Failed with ErrUnchecked, its path stays protected, and the other
// items are resized as usual.
func TestRunFailsAnUncheckedSourceAlone(t *testing.T) {
	out := t.TempDir()
	unchecked := Input{Path: filepath.Join(out, "sprite.png")}
	if err := os.WriteFile(unchecked.Path, []byte("still being checked"), 0o644); err != nil {
		t.Fatal(err)
	}
	valid := inspected(t, writeSource(t, "sprite.png", encodePNG(t, halfTransparent())))
	var events recorder
	summary, err := run(context.Background(), newRequest([]Input{unchecked, valid}, out, output.CollisionRename), events.observe, testConfig(2))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events.check(t, summary)
	if status := requireStatus(t, summary, 0, StateFailed, "", output.ReasonNone); !errors.Is(status.Err, ErrUnchecked) {
		t.Fatalf("unchecked source error = %v, want ErrUnchecked", status.Err)
	}
	requireStatus(t, summary, 1, StateCompleted, filepath.Join(out, "sprite_2.png"), output.ReasonSource)
	requireContent(t, unchecked.Path, []byte("still being checked"))
}

// load tracks the fake items in flight: how many, and the memory their
// estimates add up to.
type load struct {
	mu          sync.Mutex
	running     int
	reserved    uint64
	maxRunning  int
	maxReserved uint64
	alone       bool
	err         error
}

// enter notes an item starting and returns how many are running with it.
func (l *load) enter(cost uint64, exclusive bool) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.alone || (exclusive && l.running > 0) {
		l.err = fmt.Errorf("an item that needs more than the budget ran beside another")
	}
	l.running++
	l.reserved += cost
	l.alone = exclusive
	l.maxRunning = max(l.maxRunning, l.running)
	if !exclusive {
		l.maxReserved = max(l.maxReserved, l.reserved)
	}
	return l.running
}

func (l *load) leave(cost uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running--
	l.reserved -= cost
	l.alone = false
}

// TestRunBoundsWorkersAndMemory pins RSZ-050, TECH-006, and NFR-014: no more
// items run at once than there are workers, or than the memory budget holds
// by the items' estimates, and an item whose estimate exceeds the budget runs
// alone.
func TestRunBoundsWorkersAndMemory(t *testing.T) {
	target := resize.Target{Width: 8, Height: 8, Mode: preset.ResizeFit}
	// The estimate counts the decoded source, the resized image at 8 bytes
	// a pixel for a PNG source, the filter's working memory for the
	// source's 1 × 1 pixels, and the decoder's and encoder's buffers.
	cost := func(decoded uint64) uint64 { return decoded + 8*8*8 + resize.WorkingBytes(1, 1, target) + bufferBytes }
	const small = 1 << 20
	cases := []struct {
		name        string
		workers     int
		budget      uint64
		wantRunning int
	}{
		{"workers bind", 2, ^uint64(0), 2},
		{"memory binds", 4, 3 * cost(small), 3},
	}
	for _, tc := range cases {
		var inputs []Input
		decodes := make(map[string]uint64)
		for index := range 10 {
			decoded := uint64(small)
			if index == 5 {
				decoded = decode.MaxDecodedBytes
			}
			input := listed(t, fmt.Sprintf("item%d.png", index), decoded)
			inputs = append(inputs, input)
			decodes[input.Path] = decoded
		}
		var tracked load
		// The first items, as many as the bound lets run at once, wait until
		// they all run, so they overlap however the goroutines are scheduled;
		// the job starts them all without waiting for any to end.
		full := make(chan struct{})
		var fullOnce sync.Once
		cfg := testConfig(tc.workers)
		cfg.budget = tc.budget
		cfg.process = func(_ context.Context, item output.Item, _ Settings, share reservation) (string, error) {
			decoded := decodes[item.Source]
			cost := cost(decoded)
			if share.allowance != decoded || share.held != cost {
				t.Errorf("%s: %s decoded within %d of %d bytes, want its estimate, %d of %d", tc.name, filepath.Base(item.Source), share.allowance, share.held, decoded, cost)
			}
			if tracked.enter(cost, cost > tc.budget) >= tc.wantRunning {
				fullOnce.Do(func() { close(full) })
			}
			if slices.ContainsFunc(inputs[:tc.wantRunning], func(input Input) bool { return input.Path == item.Source }) {
				select {
				case <-full:
				case <-time.After(10 * time.Second):
					t.Errorf("%s: %d items never ran at once", tc.name, tc.wantRunning)
				}
			}
			tracked.leave(cost)
			return item.Destination, nil
		}
		request := newRequest(inputs, t.TempDir(), output.CollisionRename)
		request.Settings.Target = target
		var events recorder
		summary, err := run(context.Background(), request, events.observe, cfg)
		if err != nil {
			t.Fatalf("%s: Run: %v", tc.name, err)
		}
		events.check(t, summary)
		if tracked.err != nil {
			t.Fatalf("%s: %v", tc.name, tracked.err)
		}
		if tracked.maxRunning != tc.wantRunning || tracked.maxReserved > tc.budget {
			t.Fatalf("%s: %d items and %d bytes at once, want %d and at most %d", tc.name, tracked.maxRunning, tracked.maxReserved, tc.wantRunning, tc.budget)
		}
		if summary.Progress.Completed != len(inputs) {
			t.Fatalf("%s: summary counts = %+v", tc.name, summary.Progress)
		}
	}
}

// TestRunBudgetsAJPEGsResultAtFourBytesAPixel: a JPEG source's resized image
// is estimated at 4 bytes a pixel, a PNG source's at 8, and every fitting
// mode at the target's pixels.
func TestRunBudgetsAJPEGsResultAtFourBytesAPixel(t *testing.T) {
	target := resize.Target{Width: 100, Height: 50, Mode: preset.ResizeFit}
	if got := resultBytes(decode.FormatJPEG, target); got != 4*100*50 {
		t.Fatalf("JPEG result estimate = %d", got)
	}
	if got := resultBytes(decode.FormatPNG, target); got != 8*100*50 {
		t.Fatalf("PNG result estimate = %d", got)
	}
	// An input without an estimate is costed at the decoder's limit and the
	// most working memory any source needs, one with its dimensions at the
	// working memory those need, and one whose source turned out to need more
	// than its estimate at the most any source needs; each with the
	// decoder's and encoder's buffers.
	inputs := []Input{
		{Info: decode.Info{Format: decode.FormatPNG}},
		{Info: decode.Info{Format: decode.FormatJPEG, Width: 4000, Height: 3000, DecodedBytes: 18 << 20}},
	}
	b := newBatch(Request{Inputs: inputs, Settings: Settings{Target: target}}, nil, testConfig(1))
	if got, want := b.cost(0), uint64(decode.MaxDecodedBytes)+8*100*50+resize.MaxWorkingBytes+bufferBytes; got != want {
		t.Fatalf("cost without an estimate = %d, want %d", got, want)
	}
	working := resize.WorkingBytes(4000, 3000, target)
	if got, want := b.cost(1), uint64(18<<20)+4*100*50+working+bufferBytes; got != want || working >= resize.MaxWorkingBytes {
		t.Fatalf("cost with an estimate = %d, want %d with %d bytes of working memory", got, want, working)
	}
	b.records[1].worstCase = true
	if got, want := b.cost(1), uint64(decode.MaxDecodedBytes)+8*100*50+resize.MaxWorkingBytes+bufferBytes; got != want || b.allowance(1) != decode.MaxDecodedBytes {
		t.Fatalf("cost of a source that grew = %d within %d, want %d within the decoder's limit", got, b.allowance(1), want)
	}
	// However the source then decodes, the item fits the most any source
	// needs.
	for _, info := range []decode.Info{
		{Format: decode.FormatPNG, Width: 1 << 20, Height: 1, DecodedBytes: decode.MaxDecodedBytes},
		{Format: decode.FormatPNG, Width: 16384, Height: 16384, DecodedBytes: decode.MaxDecodedBytes},
		{Format: decode.FormatJPEG, Width: 1, Height: 1 << 20, DecodedBytes: decode.MaxDecodedBytes},
	} {
		if need := itemBytes(info, info.DecodedBytes, target); need > b.cost(1) {
			t.Fatalf("a %d × %d %v source needs %d bytes, beyond the worst case, %d", info.Width, info.Height, info.Format, need, b.cost(1))
		}
	}
}

// TestDefaultWorkersLeaveAProcessorFree: the default runs at least one item
// and at most four, leaving a processor to the GUI thread when it can.
func TestDefaultWorkersLeaveAProcessorFree(t *testing.T) {
	workers := defaultWorkers()
	if workers < 1 || workers > maxWorkers {
		t.Fatalf("defaultWorkers = %d", workers)
	}
}

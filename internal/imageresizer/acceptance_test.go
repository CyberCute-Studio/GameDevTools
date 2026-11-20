package imageresizer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// The Image Resizer acceptance suite (RSZ-09) runs every scenario of PRD
// section 15.2 that a job decides — AC-R01 through AC-R13 here, and AC-R14,
// which needs Windows' access control lists, in acceptance_windows_test.go —
// end to end, as the view runs a job once Start Resize is pressed, but
// without the window:
// the fixtures are written to disk, added to a source list, and checked by
// the content check the view's inspector runs (decode.Inspect behind its
// panic guard); the list, the preset, and the options are checked by Start
// Resize's own checks (jobRefusal, jobTarget, jobBackground), the request is
// built by its own newJobStart, and the sources whose check failed are
// checked again first by its own recheckInputs; and job.Run
// resizes every listed source with the real decoder, resizer, encoder, and
// writer. The results are decoded back from disk, and every item's final
// status is applied to the list, so the rows and details the view would show
// are checked too. Each test is named after its scenario, so
// `go test -run TestAcceptance ./internal/imageresizer` runs the whole suite;
// docs/image-resizer-verification.md maps each scenario to these tests and to
// the unit tests behind them.

// acceptanceResult is a finished acceptance job: the source list with every
// item's final status applied, as the view's rows show them, the job's
// summary, and the indexes of the sources it checked again before it
// started.
type acceptanceResult struct {
	inventory *Inventory
	summary   job.Summary
	rechecked []int
}

// item returns the listed source at index, with its job result.
func (r acceptanceResult) item(index int) SourceItem {
	return r.inventory.Item(index)
}

// acceptanceCheck is the content check the view's inspector runs for every
// added file and its jobs run again for the sources whose check failed:
// decode.Inspect, behind the inspector's panic guard. The suite queues
// nothing on it, so it starts no goroutine.
var acceptanceCheck = NewInspector(decode.Inspect, nil).check

// listSources adds the paths to a new source list and checks each supported
// file's content, as the view does when files are added.
func listSources(t *testing.T, paths []string) *Inventory {
	t.Helper()
	inventory := NewInventory()
	inventory.Add(paths)
	for _, item := range inventory.Items() {
		if item.Status != StatusChecking {
			continue
		}
		info, err := acceptanceCheck(context.Background(), item.Path)
		if _, ok := inventory.Resolve(InspectionResult{ID: item.ID, Info: info, Err: err}); !ok {
			t.Fatalf("the content check of %s was not applied", item.Path)
		}
	}
	return inventory
}

// startAcceptance runs a job over the listed sources under the selected
// preset with the options, as the view's Start Resize does once the user
// confirmed an Overwrite job, or declined to when confirmed is false: Start
// Resize's own checks of the list, the preset, and the options (jobRefusal,
// jobTarget, jobBackground) must let the job start, the request is built by
// its newJobStart, and the sources whose check failed are checked again by
// its recheckInputs, their checks applied to the list, before job.Run. Every
// item's final status is then applied to the list.
func startAcceptance(t *testing.T, ctx context.Context, inventory *Inventory, options ResizeOptions, confirmed bool, selected preset.Preset, observe func(job.Event)) (acceptanceResult, error) {
	t.Helper()
	if refusal := inventory.jobRefusal(); refusal != "" {
		t.Fatalf("Start Resize refuses the list: %s", refusal)
	}
	target, err := jobTarget(selected)
	if err != nil {
		t.Fatalf("Start Resize refuses the preset %+v: %v", selected, err)
	}
	background, err := jobBackground(options)
	if err != nil {
		t.Fatalf("Start Resize refuses the options %+v: %v", options, err)
	}
	start := inventory.newJobStart(options, target, background, confirmed)
	applied := func(result InspectionResult) {
		if _, ok := inventory.Recheck(result); !ok {
			t.Errorf("the check of item %d made again was not applied", result.ID)
		}
	}
	if !recheckInputs(ctx, start.rechecks, start.ids, start.request.Inputs, acceptanceCheck, applied) {
		t.Fatal("the job was cancelled while it checked its sources again")
	}
	summary, err := job.Run(ctx, start.request, observe)
	for index, status := range summary.Items {
		inventory.SetResult(start.ids[index], status)
	}
	return acceptanceResult{inventory: inventory, summary: summary, rechecked: start.rechecks}, err
}

// runAcceptance lists the sources and runs one job over them, which must
// not fail as a whole.
func runAcceptance(t *testing.T, ctx context.Context, sources []string, options ResizeOptions, selected preset.Preset, observe func(job.Event)) acceptanceResult {
	t.Helper()
	result, err := startAcceptance(t, ctx, listSources(t, sources), options, options.Collision == CollisionOverwrite, selected, observe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func acceptanceOptions(folder string, format OutputFormat, collision CollisionPolicy) ResizeOptions {
	options := DefaultOptions(folder)
	options.Format, options.Collision = format, collision
	return options
}

// acceptancePreset is a custom Image Resizer preset of the size and fitting
// mode, as the user selects one before Start Resize.
func acceptancePreset(width, height int, mode preset.ResizeMode) preset.Preset {
	return preset.Preset{ID: "acceptance", Name: "Acceptance", Width: width, Height: height, ResizeMode: mode}
}

// requireItem checks an item's final state and destination, and returns its
// status.
func requireItem(t *testing.T, result acceptanceResult, index int, state job.State, destination string) job.Status {
	t.Helper()
	status := result.summary.Items[index]
	if status.State != state || status.Destination != destination {
		t.Fatalf("item %d (%s) = %v at %q (%v, %v), want %v at %q", index, result.item(index).Name, status.State, status.Destination, status.Reason, status.Err, state, destination)
	}
	return status
}

// decodeOutput decodes a saved result back from disk.
func decodeOutput(t *testing.T, path string) *decode.Image {
	t.Helper()
	decoded, err := decode.Decode(context.Background(), path)
	if err != nil {
		t.Fatalf("decode the result %s: %v", path, err)
	}
	return decoded
}

func requireSize(t *testing.T, path string, img *decode.Image, width, height int) {
	t.Helper()
	if img.Width != width || img.Height != height {
		t.Fatalf("%s is %d × %d, want %d × %d", filepath.Base(path), img.Width, img.Height, width, height)
	}
}

// requirePixel checks that the pixel at (x, y) is within tolerance of want
// in every channel, alpha included, comparing colors without premultiplied
// alpha.
func requirePixel(t *testing.T, name string, img image.Image, x, y int, want color.NRGBA, tolerance int) {
	t.Helper()
	got := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	near := func(a, b uint8) bool { return int(a)-int(b) <= tolerance && int(b)-int(a) <= tolerance }
	if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) || !near(got.A, want.A) {
		t.Fatalf("%s pixel (%d, %d) = %v, want %v within %d", name, x, y, got, want, tolerance)
	}
}

// fileState is a file's content and modification time.
type fileState struct {
	content  []byte
	modified time.Time
}

func snapshot(t *testing.T, paths ...string) map[string]fileState {
	t.Helper()
	states := make(map[string]fileState, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		states[path] = fileState{content: content, modified: info.ModTime()}
	}
	return states
}

// requireUnchanged checks that every file still has the content and
// modification time it had.
func requireUnchanged(t *testing.T, before map[string]fileState) {
	t.Helper()
	for path, state := range before {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, state.content) || !info.ModTime().Equal(state.modified) {
			t.Fatalf("%s changed: modified %v, was %v", path, info.ModTime(), state.modified)
		}
	}
}

// folderNames lists the names in a folder, sorted.
func folderNames(t *testing.T, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

// requireNames checks a folder holds exactly the named entries, which also
// proves it holds no temporary or moved-aside file of a write. It sorts a
// copy of want, so a caller's slice keeps its order.
func requireNames(t *testing.T, folder string, want ...string) {
	t.Helper()
	want = slices.Clone(want)
	slices.Sort(want)
	if got := folderNames(t, folder); !slices.Equal(got, want) {
		t.Fatalf("%s holds %q, want %q", folder, got, want)
	}
}

// TestAcceptanceR01Stretch pins AC-R01: a 1000 × 500 source under a
// 512 × 512 Stretch preset is exactly 512 × 512, its width and height scaled
// independently — by 0.512 and 1.024, so the red quarter is the left 128
// columns and the ten-pixel stripes are five pixels wide across and ten
// down. The view discloses the distortion for every Stretch preset before a
// job can start, which TestNativeViewModeDescriptionFollowsPresetSelection
// pins in the window.
func TestAcceptanceR01Stretch(t *testing.T) {
	source := writeFixture(t, t.TempDir(), "marks.png", fixturePNG(t, fixtureCropMarks()))
	out := filepath.Join(t.TempDir(), "Resized")
	result := runAcceptance(t, context.Background(), []string{source}, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(512, 512, preset.ResizeStretch), nil)

	path := filepath.Join(out, "marks.png")
	requireItem(t, result, 0, job.StateCompleted, path)
	img := decodeOutput(t, path)
	requireSize(t, path, img, 512, 512)
	requirePixel(t, "stretched", img.Pixels, 64, 150, fixtureRed, 4)
	requirePixel(t, "stretched", img.Pixels, 200, 150, fixtureGreen, 4)
	requirePixel(t, "stretched", img.Pixels, 448, 150, fixtureBlue, 4)
	requirePixel(t, "stretched", img.Pixels, 256, 150, fixtureBlack, 24)
	requirePixel(t, "stretched", img.Pixels, 200, 256, fixtureBlack, 4)
}

// TestAcceptanceR02FitLandscape pins AC-R02 for a PNG and a JPEG source: a
// 1000 × 500 source under a 512 × 512 Fit preset is 512 × 256, its aspect
// ratio kept, with no padding: every corner and edge holds the source's own
// content, opaque, and the PNG result carries no alpha channel a pad would
// need.
func TestAcceptanceR02FitLandscape(t *testing.T) {
	sources := t.TempDir()
	marks := fixtureCropMarks()
	paths := []string{
		writeFixture(t, sources, "marks.png", fixturePNG(t, marks)),
		writeFixture(t, sources, "marks.jpg", fixtureJPEG(t, marks)),
	}
	out := t.TempDir()
	result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(512, 512, preset.ResizeFit), nil)

	for index, name := range []string{"marks.png", "marks.jpg"} {
		path := filepath.Join(out, name)
		requireItem(t, result, index, job.StateCompleted, path)
		img := decodeOutput(t, path)
		requireSize(t, path, img, 512, 256)
		if img.HasAlpha {
			t.Fatalf("%s carries an alpha channel", name)
		}
		tolerance := 4
		if strings.HasSuffix(name, ".jpg") {
			tolerance = 16
		}
		for _, corner := range []struct {
			x, y int
			want color.NRGBA
		}{{0, 0, fixtureRed}, {511, 0, fixtureBlue}, {0, 255, fixtureRed}, {511, 255, fixtureBlue}, {200, 0, fixtureGreen}, {200, 255, fixtureGreen}} {
			requirePixel(t, name, img.Pixels, corner.x, corner.y, corner.want, tolerance)
		}
	}
}

// TestAcceptanceR03FitPortraitAndRounding pins AC-R03: under Fit, mixed
// portrait, landscape, square, and extreme sources, PNG and JPEG, each fit
// within the target, fill it along their limiting side, keep their aspect
// ratio within one pixel of the exact scaled size, and are never smaller
// than 1 × 1 — under a square target and under a landscape one whose
// limiting side differs between sources.
func TestAcceptanceR03FitPortraitAndRounding(t *testing.T) {
	sources := t.TempDir()
	sizes := []struct{ width, height int }{
		{1000, 500}, {500, 1000}, {1000, 333}, {333, 1000}, {999, 1000},
		{640, 480}, {480, 640}, {7, 3}, {1, 1000}, {3000, 1},
	}
	var paths []string
	for index, size := range sizes {
		img := fixtureQuadrants(size.width, size.height)
		name := fmt.Sprintf("source-%d-%dx%d.png", index, size.width, size.height)
		content := fixturePNG(t, img)
		if index%2 == 1 {
			name, content = strings.TrimSuffix(name, ".png")+".jpg", fixtureJPEG(t, img)
		}
		paths = append(paths, writeFixture(t, sources, name, content))
	}
	for _, fit := range []preset.Preset{acceptancePreset(512, 512, preset.ResizeFit), acceptancePreset(1024, 768, preset.ResizeFit)} {
		out := t.TempDir()
		result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), fit, nil)
		for index, size := range sizes {
			path := filepath.Join(out, filepath.Base(paths[index]))
			requireItem(t, result, index, job.StateCompleted, path)
			img := decodeOutput(t, path)
			scale := math.Min(float64(fit.Width)/float64(size.width), float64(fit.Height)/float64(size.height))
			exactWidth, exactHeight := float64(size.width)*scale, float64(size.height)*scale
			switch {
			case img.Width < 1 || img.Height < 1 || img.Width > fit.Width || img.Height > fit.Height:
				t.Fatalf("Fit %d × %d: %d × %d result %d × %d does not fit within the target or is empty", fit.Width, fit.Height, size.width, size.height, img.Width, img.Height)
			case img.Width != fit.Width && img.Height != fit.Height:
				t.Fatalf("Fit %d × %d: %d × %d result %d × %d fills neither side of the target", fit.Width, fit.Height, size.width, size.height, img.Width, img.Height)
			case math.Abs(float64(img.Width)-exactWidth) > 1 || math.Abs(float64(img.Height)-exactHeight) > 1:
				t.Fatalf("Fit %d × %d: %d × %d result %d × %d is more than a pixel from the exact %.2f × %.2f", fit.Width, fit.Height, size.width, size.height, img.Width, img.Height, exactWidth, exactHeight)
			}
		}
	}
}

// TestAcceptanceR04CropToFill pins AC-R04: a 1000 × 500 source under a
// 512 × 512 Crop to Fill preset is exactly 512 × 512, scaled by 1.024 along
// both axes before cropping — the stripes at x 300 and y 50 both land on
// output column and row 56, and those at x 495 and y 245 on column and row
// 256 — and cropped at the center: the output holds exactly the source's
// green middle half, with no red or blue even in its first and last
// columns, which a crop even half a source pixel off center would show.
func TestAcceptanceR04CropToFill(t *testing.T) {
	source := writeFixture(t, t.TempDir(), "marks.png", fixturePNG(t, fixtureCropMarks()))
	out := t.TempDir()
	result := runAcceptance(t, context.Background(), []string{source}, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(512, 512, preset.ResizeCropToFill), nil)

	path := filepath.Join(out, "marks.png")
	requireItem(t, result, 0, job.StateCompleted, path)
	img := decodeOutput(t, path)
	requireSize(t, path, img, 512, 512)
	for _, column := range []int{0, 1, 44, 68, 150, 244, 268, 510, 511} {
		requirePixel(t, "cropped", img.Pixels, column, 150, fixtureGreen, 6)
	}
	for _, row := range []int{0, 44, 68, 150, 244, 268, 511} {
		requirePixel(t, "cropped", img.Pixels, 150, row, fixtureGreen, 6)
	}
	for _, mark := range []int{56, 256} {
		requirePixel(t, "cropped", img.Pixels, mark, 150, fixtureBlack, 8)
		requirePixel(t, "cropped", img.Pixels, 150, mark, fixtureBlack, 8)
	}
}

// TestAcceptanceR05Upscaling pins AC-R05: a 100 × 50 source smaller than a
// 512 × 512 preset is enlarged under every fitting mode — to 512 × 512 under
// Stretch and Crop to Fill and to 512 × 256 under Fit — never kept at its
// own size, with its content enlarged rather than padded.
func TestAcceptanceR05Upscaling(t *testing.T) {
	source := writeFixture(t, t.TempDir(), "small.png", fixturePNG(t, fixtureQuadrants(100, 50)))
	for _, tc := range []struct {
		mode          preset.ResizeMode
		width, height int
	}{
		{preset.ResizeStretch, 512, 512},
		{preset.ResizeFit, 512, 256},
		{preset.ResizeCropToFill, 512, 512},
	} {
		out := t.TempDir()
		result := runAcceptance(t, context.Background(), []string{source}, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(512, 512, tc.mode), nil)
		path := filepath.Join(out, "small.png")
		requireItem(t, result, 0, job.StateCompleted, path)
		img := decodeOutput(t, path)
		requireSize(t, path, img, tc.width, tc.height)
		quarterX, quarterY := tc.width/4, tc.height/4
		requirePixel(t, string(tc.mode), img.Pixels, quarterX, quarterY, fixtureRed, 4)
		requirePixel(t, string(tc.mode), img.Pixels, 3*quarterX, quarterY, fixtureGreen, 4)
		requirePixel(t, string(tc.mode), img.Pixels, quarterX, 3*quarterY, fixtureBlue, 4)
		requirePixel(t, string(tc.mode), img.Pixels, 3*quarterX, 3*quarterY, fixtureWhite, 4)
	}
}

// TestAcceptanceR06MixedBatch pins AC-R06: of valid PNG and JPEG sources
// listed with one corrupt file and one unsupported file, every valid source
// is resized and saved, and each invalid one ends Failed with a status that
// names its category and what is wrong, as its row and detail show it, while
// no source changes.
func TestAcceptanceR06MixedBatch(t *testing.T) {
	sources := t.TempDir()
	paths := []string{
		writeFixture(t, sources, "sprite.png", fixturePNG(t, fixtureTranslucent())),
		writeFixture(t, sources, "photo.jpg", fixtureJPEG(t, fixtureQuadrants(64, 48))),
		writeFixture(t, sources, "damaged.png", fixtureDamagedPNG),
		writeFixture(t, sources, "sheet.gif", fixtureGIF),
	}
	before := snapshot(t, paths...)
	out := t.TempDir()
	result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(32, 32, preset.ResizeFit), nil)

	requireItem(t, result, 0, job.StateCompleted, filepath.Join(out, "sprite.png"))
	requireItem(t, result, 1, job.StateCompleted, filepath.Join(out, "photo.jpg"))
	// The corrupt PNG is checked again before the job, as the view's job
	// does (RSZ-049), and is still damaged; the GIF's name refuses it, so it
	// is not. The list could use neither, so the job plans no destination for
	// them.
	if !slices.Equal(result.rechecked, []int{2}) {
		t.Fatalf("the job checked items %v again, want only the corrupt PNG", result.rechecked)
	}
	requireItem(t, result, 2, job.StateFailed, "")
	requireItem(t, result, 3, job.StateFailed, "")
	wantRows := []string{
		"sprite.png — 90 × 30 px — Completed",
		"photo.jpg — 64 × 48 px — Completed",
		"damaged.png — Failed",
		"sheet.gif — Failed",
	}
	for index, want := range wantRows {
		if got := result.item(index).RowText(); got != want {
			t.Fatalf("row %d = %q, want %q", index, got, want)
		}
	}
	if got := result.item(2).DetailText(); !strings.HasPrefix(got, "Failed: "+ui.ErrorReadDecode.Label()+": ") || !strings.Contains(got, "damaged or incomplete") {
		t.Fatalf("corrupt detail = %q, want the read/decode category and what is wrong", got)
	}
	if got, want := result.item(3).DetailText(), "Failed: "+ui.ErrorUnsupportedFormat.Label()+": "+ui.SourceUnsupportedDetail; got != want {
		t.Fatalf("unsupported detail = %q, want %q", got, want)
	}
	if got := result.summary.Progress; got.Completed != 2 || got.Failed != 2 || got.Skipped != 0 || got.Cancelled != 0 {
		t.Fatalf("summary counts = %+v", got)
	}
	requireSize(t, "sprite.png", decodeOutput(t, filepath.Join(out, "sprite.png")), 32, 11)
	requireSize(t, "photo.jpg", decodeOutput(t, filepath.Join(out, "photo.jpg")), 32, 24)
	requireNames(t, out, "sprite.png", "photo.jpg")
	requireUnchanged(t, before)
}

// TestAcceptanceR07OriginalsPreserved pins AC-R07: with the output folder
// set to the sources' own folder, so every destination equals a source path,
// no source is overwritten under any policy — not even a confirmed
// Overwrite. Rename and Overwrite save a renamed output, saying why, and Skip
// leaves the item Skipped, saying the name is a source, so the user chooses
// another folder.
func TestAcceptanceR07OriginalsPreserved(t *testing.T) {
	for _, policy := range []CollisionPolicy{CollisionRename, CollisionSkip, CollisionOverwrite} {
		t.Run(string(policy), func(t *testing.T) {
			folder := t.TempDir()
			paths := []string{
				writeFixture(t, folder, "photo.png", fixturePNG(t, fixtureQuadrants(40, 30))),
				writeFixture(t, folder, "shot.jpg", fixtureJPEG(t, fixtureQuadrants(30, 40))),
			}
			before := snapshot(t, paths...)
			result := runAcceptance(t, context.Background(), paths, acceptanceOptions(folder, FormatKeepSource, policy), acceptancePreset(16, 16, preset.ResizeFit), nil)

			if policy == CollisionSkip {
				for index, path := range paths {
					status := requireItem(t, result, index, job.StateSkipped, path)
					if status.Reason != output.ReasonSource {
						t.Fatalf("%s skipped for %v, want ReasonSource", path, status.Reason)
					}
					if got, want := result.item(index).DetailText(), fmt.Sprintf(ui.ResizeDetailSkipped, path, ui.ResizeSkippedSource); got != want {
						t.Fatalf("skipped detail = %q, want %q", got, want)
					}
				}
				requireNames(t, folder, "photo.png", "shot.jpg")
			} else {
				for index, renamed := range []string{"photo_2.png", "shot_2.jpg"} {
					path := filepath.Join(folder, renamed)
					status := requireItem(t, result, index, job.StateCompleted, path)
					if status.Reason != output.ReasonSource {
						t.Fatalf("%s renamed for %v, want ReasonSource", renamed, status.Reason)
					}
					if got := result.item(index).DetailText(); !strings.Contains(got, ui.ResizeRenamedSource) {
						t.Fatalf("renamed detail = %q, want it to say the name belongs to a source", got)
					}
					decodeOutput(t, path)
				}
				requireNames(t, folder, "photo.png", "photo_2.png", "shot.jpg", "shot_2.jpg")
			}
			requireUnchanged(t, before)
		})
	}
}

// TestAcceptanceR08RenameCollisions pins AC-R08: two sources from different
// folders that map to one destination are both saved under Rename, the
// first under its own name and the second with the numeric suffix _2, each
// holding its own source's result, and running the same job into another
// folder assigns the same names to the same results, byte for byte.
func TestAcceptanceR08RenameCollisions(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		writeFixture(t, filepath.Join(root, "a"), "sprite.png", fixturePNG(t, fixtureFill(40, 20, fixtureRed))),
		writeFixture(t, filepath.Join(root, "b"), "sprite.png", fixturePNG(t, fixtureFill(40, 20, fixtureBlue))),
	}
	var results [][]byte
	for range 2 {
		out := t.TempDir()
		result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(16, 16, preset.ResizeFit), nil)
		requireItem(t, result, 0, job.StateCompleted, filepath.Join(out, "sprite.png"))
		if status := requireItem(t, result, 1, job.StateCompleted, filepath.Join(out, "sprite_2.png")); status.Reason != output.ReasonDuplicate {
			t.Fatalf("the second source was renamed for %v, want ReasonDuplicate", status.Reason)
		}
		if got := result.item(1).DetailText(); !strings.Contains(got, ui.ResizeRenamedDuplicate) {
			t.Fatalf("renamed detail = %q, want it to say an earlier image saves under the name", got)
		}
		requireNames(t, out, "sprite.png", "sprite_2.png")
		requirePixel(t, "sprite.png", decodeOutput(t, filepath.Join(out, "sprite.png")).Pixels, 8, 4, fixtureRed, 0)
		requirePixel(t, "sprite_2.png", decodeOutput(t, filepath.Join(out, "sprite_2.png")).Pixels, 8, 4, fixtureBlue, 0)
		for _, name := range []string{"sprite.png", "sprite_2.png"} {
			content, err := os.ReadFile(filepath.Join(out, name))
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, content)
		}
	}
	if !bytes.Equal(results[0], results[2]) || !bytes.Equal(results[1], results[3]) {
		t.Fatal("running the same job again saved different results under the same names")
	}
}

// TestAcceptanceR09SkipCollisions pins AC-R09: under Skip, an existing
// destination keeps its content and modification time, and its item is
// Skipped, not Completed, saying why, while another item of the job is
// still saved.
func TestAcceptanceR09SkipCollisions(t *testing.T) {
	sources := t.TempDir()
	paths := []string{
		writeFixture(t, sources, "photo.png", fixturePNG(t, fixtureQuadrants(40, 30))),
		writeFixture(t, sources, "other.png", fixturePNG(t, fixtureQuadrants(40, 30))),
	}
	out := t.TempDir()
	existing := writeFixture(t, out, "photo.png", fixturePNG(t, fixtureFill(10, 10, fixtureMagenta)))
	earlier := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(existing, earlier, earlier); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, append([]string{existing}, paths...)...)
	result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionSkip), acceptancePreset(16, 16, preset.ResizeFit), nil)

	if status := requireItem(t, result, 0, job.StateSkipped, existing); status.Reason != output.ReasonExists {
		t.Fatalf("skipped for %v, want ReasonExists", status.Reason)
	}
	if got, want := result.item(0).DetailText(), fmt.Sprintf(ui.ResizeDetailSkipped, existing, ui.ResizeSkippedExists); got != want {
		t.Fatalf("skipped detail = %q, want %q", got, want)
	}
	if got := result.item(0).RowText(); got != "photo.png — 40 × 30 px — Skipped" {
		t.Fatalf("skipped row = %q", got)
	}
	requireItem(t, result, 1, job.StateCompleted, filepath.Join(out, "other.png"))
	if got := result.summary.Progress; got.Skipped != 1 || got.Completed != 1 {
		t.Fatalf("summary counts = %+v", got)
	}
	requireNames(t, out, "photo.png", "other.png")
	requireUnchanged(t, before)
}

// TestAcceptanceR10ConfirmedOverwrite pins AC-R10: an Overwrite job is
// refused before anything is touched until the user confirms it, and once
// confirmed replaces only the existing files at its destinations — neither
// another file in the output folder, a numbered name included, nor a
// source.
func TestAcceptanceR10ConfirmedOverwrite(t *testing.T) {
	sources := t.TempDir()
	paths := []string{
		writeFixture(t, sources, "photo.png", fixturePNG(t, fixtureQuadrants(40, 30))),
		writeFixture(t, sources, "shot.jpg", fixtureJPEG(t, fixtureQuadrants(30, 40))),
	}
	out := t.TempDir()
	stale := fixtureFill(10, 10, fixtureMagenta)
	destinations := []string{
		writeFixture(t, out, "photo.png", fixturePNG(t, stale)),
		writeFixture(t, out, "shot.jpg", fixtureJPEG(t, stale)),
	}
	others := []string{
		writeFixture(t, out, "notes.png", fixturePNG(t, stale)),
		writeFixture(t, out, "photo_2.png", fixturePNG(t, stale)),
	}
	sourcesBefore := snapshot(t, paths...)
	destinationsBefore := snapshot(t, destinations...)
	othersBefore := snapshot(t, others...)
	options := acceptanceOptions(out, FormatKeepSource, CollisionOverwrite)

	inventory := listSources(t, paths)
	_, err := startAcceptance(t, context.Background(), inventory, options, false, acceptancePreset(16, 16, preset.ResizeFit), nil)
	var outputErr *output.Error
	if !errors.As(err, &outputErr) || outputErr.Kind != output.KindConfirmation {
		t.Fatalf("an unconfirmed Overwrite job = %v, want a confirmation failure", err)
	}
	requireUnchanged(t, destinationsBefore)
	requireUnchanged(t, othersBefore)

	result, err := startAcceptance(t, context.Background(), inventory, options, true, acceptancePreset(16, 16, preset.ResizeFit), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for index, size := range []struct{ width, height int }{{16, 12}, {12, 16}} {
		if status := requireItem(t, result, index, job.StateCompleted, destinations[index]); status.Reason != output.ReasonNone {
			t.Fatalf("%s replaced under %v, want its own name", destinations[index], status.Reason)
		}
		requireSize(t, destinations[index], decodeOutput(t, destinations[index]), size.width, size.height)
	}
	requireNames(t, out, "notes.png", "photo.png", "photo_2.png", "shot.jpg")
	requireUnchanged(t, othersBefore)
	requireUnchanged(t, sourcesBefore)
}

// TestAcceptanceR11PNGAlpha pins AC-R11: a PNG with transparency resized to
// PNG keeps its alpha — transparent, half-transparent, and opaque where the
// source is — and resized to JPEG is flattened against the selected
// background color in proportion to each pixel's alpha. The green the
// source stores under its transparent third lends no color: no visible pixel
// of the PNG result holds any, at the edge with the half-transparent third
// either.
func TestAcceptanceR11PNGAlpha(t *testing.T) {
	translucent := fixtureTranslucent()
	if hidden := translucent.NRGBAAt(0, 0); hidden != (color.NRGBA{G: 0xff}) {
		t.Fatalf("the transparent third stores %v, want green under alpha 0", hidden)
	}
	source := writeFixture(t, t.TempDir(), "translucent.png", fixturePNG(t, translucent))

	pngOut := t.TempDir()
	result := runAcceptance(t, context.Background(), []string{source}, acceptanceOptions(pngOut, FormatPNG, CollisionRename), acceptancePreset(512, 512, preset.ResizeFit), nil)
	path := filepath.Join(pngOut, "translucent.png")
	requireItem(t, result, 0, job.StateCompleted, path)
	img := decodeOutput(t, path)
	requireSize(t, path, img, 512, 171)
	if !img.HasAlpha {
		t.Fatal("the PNG result has no alpha channel")
	}
	if alpha := color.NRGBAModel.Convert(img.Pixels.At(85, 85)).(color.NRGBA).A; alpha > 2 {
		t.Fatalf("the transparent third has alpha %d", alpha)
	}
	requirePixel(t, "PNG", img.Pixels, 256, 85, color.NRGBA{R: 0xff, A: 0x80}, 3)
	requirePixel(t, "PNG", img.Pixels, 426, 85, fixtureBlue, 2)
	bounds := img.Pixels.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if pixel := color.NRGBAModel.Convert(img.Pixels.At(x, y)).(color.NRGBA); pixel.A > 0 && pixel.G > 2 {
				t.Fatalf("PNG pixel (%d, %d) = %v shows the green stored under the transparent third", x, y, pixel)
			}
		}
	}

	jpegOut := t.TempDir()
	options := acceptanceOptions(jpegOut, FormatJPEG, CollisionRename)
	options.Background = "#3366CC"
	result = runAcceptance(t, context.Background(), []string{source}, options, acceptancePreset(512, 512, preset.ResizeFit), nil)
	path = filepath.Join(jpegOut, "translucent.jpg")
	requireItem(t, result, 0, job.StateCompleted, path)
	img = decodeOutput(t, path)
	requireSize(t, path, img, 512, 171)
	if img.Format != decode.FormatJPEG || img.HasAlpha {
		t.Fatalf("the JPEG result is %v with alpha %v", img.Format, img.HasAlpha)
	}
	// The half-transparent red over #3366CC: 255·128/255 + 0x33·127/255 for
	// red, and the background's share alone for green and blue.
	requirePixel(t, "JPEG", img.Pixels, 85, 85, color.NRGBA{R: 0x33, G: 0x66, B: 0xcc, A: 0xff}, 10)
	requirePixel(t, "JPEG", img.Pixels, 256, 85, color.NRGBA{R: 153, G: 51, B: 102, A: 0xff}, 10)
	requirePixel(t, "JPEG", img.Pixels, 426, 85, fixtureBlue, 10)
}

// TestAcceptanceR12EXIFOrientation pins AC-R12 for every standard EXIF
// orientation: a JPEG storing an upright 96 × 64 picture under each of the
// eight orientations is listed at the upright 96 × 64, and resized upright —
// at the 192 × 128 its upright dimensions fit, with every quadrant where the
// upright picture has it.
func TestAcceptanceR12EXIFOrientation(t *testing.T) {
	sources := t.TempDir()
	upright := fixtureQuadrants(96, 64)
	var paths []string
	for orientation := uint16(1); orientation <= 8; orientation++ {
		content := withOrientation(fixtureJPEG(t, fixtureStored(upright, orientation)), orientation)
		paths = append(paths, writeFixture(t, sources, fmt.Sprintf("orientation-%d.jpg", orientation), content))
	}
	out := t.TempDir()
	result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(192, 192, preset.ResizeFit), nil)

	for index, source := range paths {
		item := result.item(index)
		if item.Info.Width != 96 || item.Info.Height != 64 || item.Info.Orientation != decode.Orientation(index+1) {
			t.Fatalf("%s listed as %d × %d under orientation %d", item.Name, item.Info.Width, item.Info.Height, item.Info.Orientation)
		}
		path := filepath.Join(out, filepath.Base(source))
		requireItem(t, result, index, job.StateCompleted, path)
		img := decodeOutput(t, path)
		requireSize(t, path, img, 192, 128)
		requirePixel(t, item.Name, img.Pixels, 48, 32, fixtureRed, 12)
		requirePixel(t, item.Name, img.Pixels, 144, 32, fixtureGreen, 12)
		requirePixel(t, item.Name, img.Pixels, 48, 96, fixtureBlue, 12)
		requirePixel(t, item.Name, img.Pixels, 144, 96, fixtureWhite, 12)
	}
}

// TestAcceptanceR13Cancellation pins AC-R13 for the job: once a running
// batch is cancelled, no further item starts, every completed result stays
// valid, and the unfinished items end Cancelled, with nothing saved for
// them and no temporary file left. The GUI's part — Cancel Resize, and the
// window staying usable — is TestNativeViewStaysResponsiveDuringARealJob's.
func TestAcceptanceR13Cancellation(t *testing.T) {
	sources := t.TempDir()
	content := fixtureJPEG(t, fixtureQuadrants(640, 480))
	var paths []string
	for index := range 12 {
		paths = append(paths, writeFixture(t, sources, fmt.Sprintf("photo-%02d.jpg", index), content))
	}
	out := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(map[int]bool)
	cancelled := false
	var lateStarts []int
	observe := func(event job.Event) {
		if event.Status.State == job.StateProcessing {
			if cancelled && !started[event.Index] {
				lateStarts = append(lateStarts, event.Index)
			}
			started[event.Index] = true
		}
		if event.Status.State == job.StateCompleted && !cancelled {
			cancelled = true
			cancel()
		}
	}
	result := runAcceptance(t, ctx, paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), acceptancePreset(16, 16, preset.ResizeFit), observe)

	if len(lateStarts) != 0 {
		t.Fatalf("items %v started after the job was cancelled", lateStarts)
	}
	progress := result.summary.Progress
	if progress.Completed < 1 || progress.Cancelled < 1 || progress.Completed+progress.Cancelled != len(paths) {
		t.Fatalf("summary counts = %+v, want only completed and cancelled items, at least one of each", progress)
	}
	var saved []string
	for index, status := range result.summary.Items {
		item := result.item(index)
		switch status.State {
		case job.StateCompleted:
			requireSize(t, status.Destination, decodeOutput(t, status.Destination), 16, 12)
			saved = append(saved, filepath.Base(status.Destination))
		case job.StateCancelled:
			if status.Err != nil {
				t.Fatalf("%s cancelled with %v", item.Name, status.Err)
			}
			if got := item.DetailText(); got != ui.ResizeDetailCancelled {
				t.Fatalf("cancelled detail = %q", got)
			}
			if !strings.HasSuffix(item.RowText(), " — Cancelled") {
				t.Fatalf("cancelled row = %q", item.RowText())
			}
		default:
			t.Fatalf("%s ended %v", item.Name, status.State)
		}
	}
	requireNames(t, out, saved...)
}

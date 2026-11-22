//go:build windows

package imageresizer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// queuedDispatcher is a shell dispatcher test double: posted tasks queue on
// a channel and run only when the test — on the GUI thread that owns the
// controls — drains them, mirroring the production hand-off.
type queuedDispatcher struct {
	tasks chan func()
}

func (d *queuedDispatcher) Post(task func()) error {
	select {
	case d.tasks <- task:
		return nil
	default:
		return errors.New("test dispatcher queue is full")
	}
}

// runNext runs the next posted task, failing if none arrives in time.
func (d *queuedDispatcher) runNext(t *testing.T) {
	t.Helper()
	select {
	case task := <-d.tasks:
		task()
	case <-time.After(10 * time.Second):
		t.Fatal("no content-check result was posted to the dispatcher")
	}
}

func newSourcesTestView(t *testing.T) (*NativeView, *queuedDispatcher) {
	t.Helper()
	parent := newViewTestParent(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, _ := newNativeViewForTest(t, store, &value, testFolder(t), parent)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1024)}
	view.NativeDispatcher(dispatcher)
	return view, dispatcher
}

// sourceDir returns a temporary folder for the view's test sources. Cleanups
// run last-in, first-out, so the folder's removal is registered first and the
// view's content checks are stopped and joined after it, before it runs:
// Windows cannot delete a file a check still holds open.
func sourceDir(t *testing.T, view *NativeView) string {
	t.Helper()
	directory := t.TempDir()
	t.Cleanup(func() {
		view.inspector.Close()
		view.inspector.Wait()
	})
	return directory
}

// settle runs posted content-check results until no item is still Checking.
func settle(t *testing.T, view *NativeView, dispatcher *queuedDispatcher) {
	t.Helper()
	for view.inventory.Checking() > 0 {
		dispatcher.runNext(t)
	}
}

// encodeTestPNG is a blank width × height PNG, encoded as every fixture is
// (fixturePNG).
func encodeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	return fixturePNG(t, image.NewNRGBA(image.Rect(0, 0, width, height)))
}

// encodeTestJPEG is a blank width × height grayscale JPEG, encoded as every
// fixture is (fixtureJPEG).
func encodeTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	return fixtureJPEG(t, image.NewGray(image.Rect(0, 0, width, height)))
}

// writeViewSource writes a valid 4 × 3 PNG, whatever the extension.
func writeViewSource(t *testing.T, directory, name string) string {
	t.Helper()
	return writeFixture(t, directory, name, encodeTestPNG(t, 4, 3))
}

func listContents(list *ui.ListBox) []string {
	rows := make([]string, 0, list.Count())
	for index := 0; index < list.Count(); index++ {
		rows = append(rows, list.ItemText(index))
	}
	return rows
}

func TestNativeViewAddImagesThroughPicker(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	ready := writeViewSource(t, directory, "ready.png")
	unsupported := writeViewSource(t, directory, "notes.bmp")
	missing := filepath.Join(directory, "missing.jpg")

	// A cancelled picker changes nothing.
	view.pickFiles = func(windows.Handle, string) ([]string, bool, error) {
		return nil, false, nil
	}
	view.NativeCommand(sourcesCommandBase+addImagesCommand, 0)
	if view.list.Count() != 0 {
		t.Fatalf("cancelled picker added %d items", view.list.Count())
	}

	// A picker failure is reported, not swallowed.
	view.pickFiles = func(windows.Handle, string) ([]string, bool, error) {
		return nil, false, errors.New("dialog unavailable")
	}
	view.NativeCommand(sourcesCommandBase+addImagesCommand, 0)
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("status after picker failure = %q", got)
	}

	original, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	view.pickFiles = func(windows.Handle, string) ([]string, bool, error) {
		return []string{ready, unsupported, missing, ready}, true, nil
	}
	view.NativeCommand(sourcesCommandBase+addImagesCommand, 0)
	rows := listContents(view.list)
	if len(rows) != 3 {
		t.Fatalf("list rows = %v, want 3 (the duplicate path is not listed)", rows)
	}
	// The supported file waits for its content check; the others are
	// classified immediately.
	if rows[0] != "ready.png — Checking…" || rows[1] != "notes.bmp — Unsupported format" || rows[2] != "missing.jpg — Cannot be read" {
		t.Fatalf("list rows = %v", rows)
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Warning:") || !strings.Contains(got, "Added 1 image(s).") ||
		!strings.Contains(got, "1 already in the list") || !strings.Contains(got, "1 listed with an unsupported format") ||
		!strings.Contains(got, "1 could not be read") || !strings.HasSuffix(got, "Checking image content…") {
		t.Fatalf("status after mixed add = %q", got)
	}

	settle(t, view, dispatcher)
	if got := view.list.ItemText(0); got != "ready.png — 4 × 3 px — Ready" {
		t.Fatalf("row after the content check = %q", got)
	}
	// The finishing summary restates the add results the quick check would
	// otherwise have replaced.
	if got := view.status.Text(); got != "Warning: Added 1 image(s). 1 already in the list. 1 listed with an unsupported format. 1 could not be read. Checked 1 image(s): 1 ready." {
		t.Fatalf("status after checking = %q", got)
	}

	// The original is untouched: content checks open sources read-only
	// (RSZ-009).
	content, err := os.ReadFile(ready)
	if err != nil || !bytes.Equal(content, original) {
		t.Fatalf("source after add = %v, %v", content, err)
	}
}

func TestNativeViewDropFilesAndFolder(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	first := writeViewSource(t, directory, "one.png")
	writeFixture(t, directory, "two.JPG", encodeTestJPEG(t, 8, 6))
	writeViewSource(t, directory, "skipped.bmp")
	nested := filepath.Join(directory, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	writeViewSource(t, nested, "nested.png")

	view.NativeDropFiles([]string{directory, first})
	if got := view.status.Text(); !strings.Contains(got, "Added 2 image(s).") || !strings.Contains(got, "1 already in the list") {
		t.Fatalf("status after folder drop = %q", got)
	}
	settle(t, view, dispatcher)
	rows := listContents(view.list)
	if len(rows) != 2 || rows[0] != "one.png — 4 × 3 px — Ready" || rows[1] != "two.JPG — 8 × 6 px — Ready" {
		t.Fatalf("rows after folder drop = %v, want the two supported files", rows)
	}
	if got := view.status.Text(); got != "Success: Added 2 image(s). 1 already in the list. Checked 2 image(s): 2 ready." {
		t.Fatalf("status after checking = %q", got)
	}
}

// TestNativeViewIdentifiesContentProblems pins RSZ-003 and RSZ-004 through
// the view: content is validated rather than trusted from the extension,
// each problem is identified in the list with its reason, a renamed file is
// processed as what it contains, and an oriented JPEG lists its upright
// dimensions (RSZ-018).
func TestNativeViewIdentifiesContentProblems(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	valid := encodeTestPNG(t, 20, 10)
	paths := []string{
		writeFixture(t, directory, "text.png", []byte("not an image")),
		writeFixture(t, directory, "truncated.png", valid[:len(valid)/2]),
		writeFixture(t, directory, "renamed.png", encodeTestJPEG(t, 12, 9)),
		writeFixture(t, directory, "portrait.jpg", withOrientation(encodeTestJPEG(t, 40, 30), 6)),
		writeFixture(t, directory, "empty.jpeg", nil),
	}
	view.NativeDropFiles(paths)
	settle(t, view, dispatcher)

	want := []string{
		"text.png — Unsupported format",
		"truncated.png — Damaged or incomplete",
		"renamed.png — 12 × 9 px — Ready",
		"portrait.jpg — 30 × 40 px — Ready",
		"empty.jpeg — Damaged or incomplete",
	}
	if rows := listContents(view.list); strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	if got := view.status.Text(); got != "Warning: Added 5 image(s). Checked 5 image(s): 2 ready. 3 cannot be resized — select an item to see why." {
		t.Fatalf("status after checking = %q", got)
	}

	details := []string{
		"Unsupported format: The file content is not PNG or JPEG image data",
		"Damaged or incomplete: The PNG data is damaged or incomplete",
		"The content is JPEG data despite the .png extension, so it is processed as JPEG.",
		"upright after applying its EXIF orientation (stored as 40 × 30 px)",
		"Damaged or incomplete: The file is empty.",
	}
	for index, detail := range details {
		for other := range paths {
			view.list.SetSelected(other, other == index)
		}
		view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
		if got := view.pathDetail.Text(); !strings.Contains(got, detail) {
			t.Fatalf("detail for %s = %q, want it to contain %q", filepath.Base(paths[index]), got, detail)
		}
	}
}

// gatedInspect is a controllable content check: each check reports its path
// on started, then waits for a release or for its cancellation.
type gatedInspect struct {
	started  chan string
	release  chan struct{}
	canceled chan string
}

func newGatedInspect() *gatedInspect {
	return &gatedInspect{started: make(chan string, 16), release: make(chan struct{}, 16), canceled: make(chan string, 16)}
}

// inspect reports a released check as Ready, or as damaged when the file
// name says so.
func (g *gatedInspect) inspect(ctx context.Context, path string) (decode.Info, error) {
	g.started <- path
	select {
	case <-ctx.Done():
		g.canceled <- path
		return decode.Info{}, &decode.Error{Kind: decode.KindCanceled, Err: ctx.Err()}
	case <-g.release:
		if strings.Contains(filepath.Base(path), "damaged") {
			return decode.Info{}, &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}
		}
		return decode.Info{Format: decode.FormatPNG, Width: 4, Height: 3, Orientation: decode.OrientationNormal}, nil
	}
}

func (g *gatedInspect) waitFor(t *testing.T, events chan string) string {
	t.Helper()
	select {
	case path := <-events:
		return path
	case <-time.After(10 * time.Second):
		t.Fatal("the content check did not reach the expected point")
		return ""
	}
}

func useGatedInspector(view *NativeView) *gatedInspect {
	gate := newGatedInspect()
	view.inspector.Close()
	view.inspector = NewInspector(gate.inspect, view.deliverInspection)
	return gate
}

// TestNativeViewCheckResultsKeepSelection pins that a content check
// resolving in the background rewrites only its own row: the user's
// selection survives, and the detail for the selected item follows its new
// status.
func TestNativeViewCheckResultsKeepSelection(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	gate := useGatedInspector(view)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{
		writeViewSource(t, directory, "one.png"),
		writeViewSource(t, directory, "two.png"),
		writeViewSource(t, directory, "three.png"),
	})
	gate.waitFor(t, gate.started)

	view.list.SetSelected(0, true)
	view.list.SetSelected(2, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	if got := view.pathDetail.Text(); got != ui.SourceCheckingDetail {
		t.Fatalf("detail while checking = %q", got)
	}

	gate.release <- struct{}{}
	dispatcher.runNext(t)
	if got := view.list.SelectedIndices(); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("selection after the first result = %v, want [0 2]", got)
	}
	if got := view.list.ItemText(0); got != "one.png — 4 × 3 px — Ready" {
		t.Fatalf("first row = %q", got)
	}
	if got := view.pathDetail.Text(); got != "PNG image, 4 × 3 px." {
		t.Fatalf("detail after the selected item resolved = %q", got)
	}

	gate.waitFor(t, gate.started)
	gate.release <- struct{}{}
	dispatcher.runNext(t)
	if got := view.list.SelectedIndices(); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("selection after an unselected item resolved = %v, want [0 2]", got)
	}
	gate.waitFor(t, gate.started)
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	if got := listContents(view.list); got[1] != "two.png — 4 × 3 px — Ready" || got[2] != "three.png — 4 × 3 px — Ready" {
		t.Fatalf("rows after checking = %v", got)
	}
}

// TestNativeViewRemovingCheckingItemsAbandonsTheirChecks pins that removed
// or cleared items stop consuming decode work and never resurface.
func TestNativeViewRemovingCheckingItemsAbandonsTheirChecks(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	gate := useGatedInspector(view)
	directory := sourceDir(t, view)
	first := writeViewSource(t, directory, "one.png")
	view.NativeDropFiles([]string{first, writeViewSource(t, directory, "two.png")})
	if path := gate.waitFor(t, gate.started); path != first {
		t.Fatalf("first check = %q", path)
	}

	view.list.SetSelected(0, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)
	if path := gate.waitFor(t, gate.canceled); path != first {
		t.Fatalf("cancelled check = %q, want the removed item", path)
	}
	gate.waitFor(t, gate.started)
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	if rows := listContents(view.list); len(rows) != 1 || rows[0] != "two.png — 4 × 3 px — Ready" {
		t.Fatalf("rows after removal = %v", rows)
	}
	// The summary restates only what is still listed.
	if got := view.status.Text(); got != "Success: Added 1 image(s). Checked 1 image(s): 1 ready." {
		t.Fatalf("status after checking = %q", got)
	}

	view.NativeDropFiles([]string{writeViewSource(t, directory, "three.png")})
	gate.waitFor(t, gate.started)
	view.NativeCommand(sourcesCommandBase+clearListCommand, 0)
	gate.waitFor(t, gate.canceled)
	select {
	case task := <-dispatcher.tasks:
		task()
		t.Fatal("a cleared item's check was delivered")
	case <-time.After(50 * time.Millisecond):
	}
	if view.list.Count() != 0 || view.inventory.Checking() != 0 {
		t.Fatalf("after clear: %d rows, %d checking", view.list.Count(), view.inventory.Checking())
	}
}

// TestNativeViewBatchesCheckResults pins that checks finishing faster than
// the GUI thread drains them share one dispatcher post, so a large drop can
// neither flood the GUI thread's message queue nor lose results to a full
// one.
func TestNativeViewBatchesCheckResults(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	paths := make([]string, 0, 5)
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png", "e.png"} {
		paths = append(paths, writeViewSource(t, directory, name))
	}
	view.NativeDropFiles(paths)
	// The worker exits once every check has been delivered.
	view.inspector.Wait()
	if posted := len(dispatcher.tasks); posted != 1 {
		t.Fatalf("%d drain tasks posted for 5 undrained results, want 1", posted)
	}
	dispatcher.runNext(t)
	if view.inventory.Checking() != 0 {
		t.Fatalf("%d items still checking after one drain", view.inventory.Checking())
	}
	if got := view.status.Text(); got != "Success: Added 5 image(s). Checked 5 image(s): 5 ready." {
		t.Fatalf("status after checking = %q", got)
	}
}

// failingDispatcher refuses the first fails posts, then queues like
// queuedDispatcher, counting every attempt.
type failingDispatcher struct {
	queuedDispatcher
	mu       sync.Mutex
	fails    int
	attempts int
}

func (d *failingDispatcher) Post(task func()) error {
	d.mu.Lock()
	d.attempts++
	refuse := d.fails > 0
	if refuse {
		d.fails--
	}
	d.mu.Unlock()
	if refuse {
		return errors.New("PostMessageW: not enough quota")
	}
	return d.queuedDispatcher.Post(task)
}

func (d *failingDispatcher) attempted() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.attempts
}

// TestNativeViewRetriesARefusedPost pins that a refused post loses nothing,
// even for the last result, which no later result follows to post again:
// the post is retried until it is accepted, so no item stays Checking for
// good. Once the view is closed a post can only fail, and it is not retried.
func TestNativeViewRetriesARefusedPost(t *testing.T) {
	view, _ := newSourcesTestView(t)
	dispatcher := &failingDispatcher{queuedDispatcher: queuedDispatcher{tasks: make(chan func(), 16)}, fails: 2}
	view.NativeDispatcher(dispatcher)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "only.png")})
	dispatcher.runNext(t)
	if got := view.list.ItemText(0); got != "only.png — 4 × 3 px — Ready" {
		t.Fatalf("row after the retried post = %q", got)
	}
	if view.inventory.Checking() != 0 {
		t.Fatalf("%d items still checking", view.inventory.Checking())
	}
	if attempts := dispatcher.attempted(); attempts != 3 {
		t.Fatalf("%d post attempts, want two refused and one accepted", attempts)
	}

	closed, _ := newSourcesTestView(t)
	refusing := &failingDispatcher{queuedDispatcher: queuedDispatcher{tasks: make(chan func(), 16)}, fails: 1 << 30}
	closed.NativeDispatcher(refusing)
	closed.inspector.Close()
	closed.deliverInspection(InspectionResult{ID: 1})
	time.Sleep(3 * postRetryDelay)
	if attempts := refusing.attempted(); attempts != 1 {
		t.Fatalf("%d post attempts after the view closed, want 1", attempts)
	}
}

// TestNativeViewCheckSummaryKeepsOtherNotices pins that the finishing
// summary never hides a notice raised after the add, such as a failed
// settings save.
func TestNativeViewCheckSummaryKeepsOtherNotices(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	gate := useGatedInspector(view)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "one.png")})
	gate.waitFor(t, gate.started)

	view.presentResult(fmt.Errorf("%w: the disk is full", ErrPersistence), "")
	warning := view.status.Text()
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	if got := view.status.Text(); got != warning {
		t.Fatalf("status after checking = %q, want the save warning %q kept", got, warning)
	}
	if got := view.list.ItemText(0); got != "one.png — 4 × 3 px — Ready" {
		t.Fatalf("row after checking = %q", got)
	}

	// Ownership follows the notice shown, not its text: the same text shown
	// elsewhere is kept, while an area left empty takes the summary.
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{writeViewSource(t, directory, "two.png")})
	gate.waitFor(t, gate.started)
	same := ui.Notice{Kind: ui.NoticeSuccess, Message: "Added 1 image(s). Checking image content…"}
	if view.status.Text() != same.Text() {
		t.Fatalf("add notice = %q, want %q", view.status.Text(), same.Text())
	}
	_ = view.status.Show(same, 0)
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	if got := view.status.Text(); got != same.Text() {
		t.Fatalf("status after checking = %q, want the other notice %q kept", got, same.Text())
	}
	view.NativeDropFiles([]string{writeViewSource(t, directory, "three.png")})
	gate.waitFor(t, gate.started)
	view.status.Clear()
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	if got := view.status.Text(); got != "Success: Added 1 image(s). Checked 1 image(s): 1 ready." {
		t.Fatalf("status after checking into an empty area = %q", got)
	}
}

// TestNativeViewRemovingTheLastCheckSummarizes pins that removing the last
// items still being checked finishes checking with the summary of the other
// checks, instead of losing it behind the removal notice.
func TestNativeViewRemovingTheLastCheckSummarizes(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	gate := useGatedInspector(view)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{
		writeViewSource(t, directory, "one.png"),
		writeViewSource(t, directory, "damaged.png"),
		writeViewSource(t, directory, "slow.png"),
	})
	for range 2 {
		gate.waitFor(t, gate.started)
		gate.release <- struct{}{}
		dispatcher.runNext(t)
	}
	gate.waitFor(t, gate.started)
	view.list.SetSelected(2, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)
	gate.waitFor(t, gate.canceled)
	if got := view.status.Text(); got != "Warning: Added 2 image(s). Checked 2 image(s): 1 ready. 1 cannot be resized — select an item to see why. Removed 1 image(s)." {
		t.Fatalf("status after removing the last check = %q", got)
	}

	// With no other check resolved, the removal is all there is to report.
	view.NativeDropFiles([]string{writeViewSource(t, directory, "four.png")})
	gate.waitFor(t, gate.started)
	view.list.SetSelected(2, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)
	gate.waitFor(t, gate.canceled)
	if got := view.status.Text(); got != "Success: Removed 1 image(s)." {
		t.Fatalf("status after removing the only check = %q", got)
	}
}

// TestNativeViewCheckSummaryCountsListedItems pins that items removed while
// checks are pending drop out of the summary — out of the restated add
// results, the duplicates they matched included, and out of the check
// outcomes alike — so it never counts images, or points at failures, that
// are no longer listed.
func TestNativeViewCheckSummaryCountsListedItems(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	gate := useGatedInspector(view)
	directory := sourceDir(t, view)
	listed := writeViewSource(t, directory, "listed.png")
	view.NativeDropFiles([]string{listed})
	gate.waitFor(t, gate.started)
	gate.release <- struct{}{}
	settle(t, view, dispatcher)

	view.NativeDropFiles([]string{
		writeViewSource(t, directory, "one.png"),
		writeViewSource(t, directory, "damaged.png"),
		writeViewSource(t, directory, "three.png"),
		writeViewSource(t, directory, "notes.txt"),
		listed,
	})
	if got := view.status.Text(); got != "Warning: Added 3 image(s). 1 already in the list. 1 listed with an unsupported format. Checking image content…" {
		t.Fatalf("add notice = %q", got)
	}
	for range 2 {
		gate.waitFor(t, gate.started)
		gate.release <- struct{}{}
		dispatcher.runNext(t)
	}
	if got := view.list.ItemText(2); got != "damaged.png — Damaged or incomplete" {
		t.Fatalf("damaged row = %q", got)
	}
	// Remove the damaged image and the unsupported file, and the item listed
	// before this add that its duplicate matched, so the summary no longer
	// reports that path as already in the list.
	for _, index := range []int{0, 2, 4} {
		view.list.SetSelected(index, true)
	}
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)

	gate.waitFor(t, gate.started)
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	if got := view.status.Text(); got != "Success: Added 2 image(s). Checked 2 image(s): 2 ready." {
		t.Fatalf("status after checking = %q", got)
	}
}

// TestNativeViewShutdownStopsInFlightChecks pins that closing the window
// cancels an in-flight check, so its worker stops at the next read.
func TestNativeViewShutdownStopsInFlightChecks(t *testing.T) {
	view, _ := newSourcesTestView(t)
	gate := useGatedInspector(view)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "slow.png")})
	gate.waitFor(t, gate.started)

	done := make(chan struct{})
	go func() {
		view.inspector.Close()
		view.inspector.Wait()
		close(done)
	}()
	gate.waitFor(t, gate.canceled)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish after the in-flight check was cancelled")
	}
}

func TestNativeViewRemoveSelectedAndClearSources(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	first := writeViewSource(t, directory, "one.png")
	second := writeViewSource(t, directory, "two.png")
	third := writeViewSource(t, directory, "three.png")
	view.NativeDropFiles([]string{first, second, third})
	settle(t, view, dispatcher)

	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.removeButton.Handle())); enabled != 0 {
		t.Fatal("Remove is enabled with no selection")
	}

	// Multi-selection removal keeps unselected entries (RSZ-006).
	view.list.SetSelected(0, true)
	view.list.SetSelected(2, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.removeButton.Handle())); enabled == 0 {
		t.Fatal("Remove stayed disabled with a selection")
	}
	// The full path and the decoded description of the first selected item
	// are shown on demand (RSZ-005).
	if got := view.path.Text(); got != first {
		t.Fatalf("full path display = %q, want %q", got, first)
	}
	if got := view.pathDetail.Text(); got != "PNG image, 4 × 3 px, with an alpha channel." {
		t.Fatalf("detail for a ready item = %q", got)
	}
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)
	rows := listContents(view.list)
	if len(rows) != 1 || rows[0] != "two.png — 4 × 3 px — Ready" {
		t.Fatalf("rows after removal = %v", rows)
	}
	if got := view.status.Text(); !strings.Contains(got, "Removed 2 image(s).") {
		t.Fatalf("status after removal = %q", got)
	}

	// Clearing disables the Clear button itself, so focus must move off it
	// rather than being stranded on a disabled control.
	view.clearButton.Focus()
	view.NativeCommand(sourcesCommandBase+clearListCommand, 0)
	if view.list.Count() != 0 {
		t.Fatalf("rows after clear = %d", view.list.Count())
	}
	getFocus := windows.NewLazySystemDLL("user32.dll").NewProc("GetFocus")
	if focus, _, _ := getFocus.Call(); windows.Handle(focus) != view.list.Handle() {
		t.Fatalf("focus after clear = %#x, want the source list %#x", focus, view.list.Handle())
	}
	if got := view.path.Text(); got != "" {
		t.Fatalf("path display after clear = %q", got)
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.clearButton.Handle())); enabled != 0 {
		t.Fatal("Clear stayed enabled for an empty list")
	}
	// Removed and cleared sources can be added again.
	view.NativeDropFiles([]string{first})
	if view.list.Count() != 1 {
		t.Fatalf("rows after re-adding = %d", view.list.Count())
	}
	settle(t, view, dispatcher)
}

func TestNativeViewSelectionShowsStatusDetail(t *testing.T) {
	view, _ := newSourcesTestView(t)
	directory := sourceDir(t, view)
	unsupported := writeViewSource(t, directory, "notes.bmp")
	view.NativeDropFiles([]string{unsupported})

	view.list.SetSelected(0, true)
	view.NativeCommand(sourcesCommandBase+sourceListCommand, ui.NotificationListSelChange)
	if got := view.path.Text(); got != unsupported {
		t.Fatalf("full path display = %q, want %q", got, unsupported)
	}
	if got := view.pathDetail.Text(); !strings.Contains(got, "Unsupported format") || !strings.Contains(got, ".png, .jpg, or .jpeg") {
		t.Fatalf("status detail = %q", got)
	}
}

func TestNativeViewEmptyDropReportsNothingFound(t *testing.T) {
	view, _ := newSourcesTestView(t)
	directory := sourceDir(t, view)
	writeViewSource(t, directory, "only.bmp")
	view.NativeDropFiles([]string{directory})
	if view.list.Count() != 0 {
		t.Fatalf("unsupported folder contents listed %d items", view.list.Count())
	}
	if got := view.status.Text(); !strings.Contains(got, "No supported PNG or JPEG files") {
		t.Fatalf("status after empty folder drop = %q", got)
	}
}

// TestInaccessibleShareRootIsListedByItsName pins that a share root that
// cannot be read, named by an extended-length path, is listed under the
// share's name, where Go 1.27's filepath.Base gives a lone separator.
func TestInaccessibleShareRootIsListedByItsName(t *testing.T) {
	for _, path := range []string{`\\?\UNC\server\share`, `\??\UNC\server\share`} {
		inventory := NewInventory()
		var summary AddSummary
		inventory.addInaccessible(path, errors.New("the share cannot be read"), &summary)
		if got := inventory.Item(0).Name; got != "share" {
			t.Errorf("%s is listed as %q, want share", path, got)
		}
	}
}

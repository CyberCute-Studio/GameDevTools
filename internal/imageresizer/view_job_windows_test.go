//go:build windows

package imageresizer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// enabled reports whether a control accepts input.
func enabled(control interface{ Handle() windows.Handle }) bool {
	result, _, _ := procIsWindowEnabled.Call(uintptr(control.Handle()))
	return result != 0
}

// drainJob runs the dispatcher's tasks until the view's job has finished.
func drainJob(t *testing.T, view *NativeView, dispatcher *queuedDispatcher) {
	t.Helper()
	for view.active != nil {
		dispatcher.runNext(t)
	}
}

// waitJobDone waits for the jobs' goroutines to return, so a test's folders
// are no longer in use when they are removed.
func waitJobDone(t *testing.T, view *NativeView) {
	t.Helper()
	stopped := make(chan struct{})
	go func() {
		view.jobs.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("a job's goroutine did not return")
	}
}

// selectRow selects only the row at index, as a click does, and refreshes
// the detail the view shows for it.
func selectRow(view *NativeView, index int) {
	for row := 0; row < view.list.Count(); row++ {
		view.list.SetSelected(row, row == index)
	}
	view.refreshSourceSelection()
}

// imageSize decodes an output file's dimensions.
func imageSize(t *testing.T, path string) (int, int) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return config.Width, config.Height
}

// expectedSize is the output size of a source under the selected preset.
func expectedSize(t *testing.T, view *NativeView, width, height int) (int, int) {
	t.Helper()
	selected, exists := view.presets.SelectedPreset()
	if !exists {
		t.Fatal("no preset is selected")
	}
	geometry, err := resize.Plan(width, height, resize.Target{Width: selected.Width, Height: selected.Height, Mode: selected.ResizeMode})
	if err != nil {
		t.Fatal(err)
	}
	return geometry.Width, geometry.Height
}

// TestNativeViewResizesEveryReadyImageEndToEnd runs a mixed batch through
// the view (AC-R06): every valid image is resized and saved, each invalid
// one ends Failed with its reason, the rows show each outcome, the summary
// counts them (RSZ-046), and the output folder opens only on request
// (RSZ-047, RSZ-048), off the GUI thread, so a folder that does not respond
// cannot freeze the window.
func TestNativeViewResizesEveryReadyImageEndToEnd(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	folder := view.options.Options().OutputFolder
	opened := make(chan string, 4)
	view.openFolder = func(path string) error {
		opened <- path
		return nil
	}
	view.NativeDropFiles([]string{
		writeViewFile(t, directory, "photo.png", encodeTestPNG(t, 8, 6)),
		writeViewFile(t, directory, "shot.jpg", encodeTestJPEG(t, 6, 8)),
		writeViewFile(t, directory, "damaged.png", []byte("\x89PNG\r\n\x1a\nnot really a PNG")),
		writeViewFile(t, directory, "notes.txt", []byte("text")),
	})
	settle(t, view, dispatcher)
	if enabled(view.cancelButton) || enabled(view.openButton) {
		t.Fatal("Cancel Resize or Open Output Folder was enabled before any job")
	}

	view.NativeAction()
	if view.active == nil {
		t.Fatalf("the job did not start: %q", view.jobStatus.Text())
	}
	if view.NativeActionAvailable() {
		t.Fatal("Start Resize stayed available while the job runs")
	}
	if enabled(view.addButton) || enabled(view.clearButton) || !enabled(view.cancelButton) {
		t.Fatal("the source list stayed editable, or Cancel Resize stayed disabled, while the job runs")
	}
	if !view.cancelButton.HasFocus() {
		t.Fatal("the keyboard focus did not move to Cancel Resize")
	}
	// The job's events reach the view only through the dispatcher, which
	// the test has not drained yet. damaged.png is checked again first.
	if got := view.list.ItemText(0); got != "photo.png — 8 × 6 px — Pending" || view.jobStatus.Text() != "Information: Starting to resize 4 image(s)…"+fmt.Sprintf(ui.ResizeJobRechecking, 1) {
		t.Fatalf("row at the start = %q, status = %q", got, view.jobStatus.Text())
	}
	drainJob(t, view, dispatcher)

	want := []string{
		"photo.png — 8 × 6 px — Completed",
		"shot.jpg — 6 × 8 px — Completed",
		"damaged.png — Failed",
		"notes.txt — Failed",
	}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows after the job = %q, want %q", got, want)
	}
	width, height := expectedSize(t, view, 8, 6)
	if gotWidth, gotHeight := imageSize(t, filepath.Join(folder, "photo.png")); gotWidth != width || gotHeight != height {
		t.Fatalf("photo.png output = %d × %d, want %d × %d", gotWidth, gotHeight, width, height)
	}
	width, height = expectedSize(t, view, 6, 8)
	if gotWidth, gotHeight := imageSize(t, filepath.Join(folder, "shot.jpg")); gotWidth != width || gotHeight != height {
		t.Fatalf("shot.jpg output = %d × %d, want %d × %d", gotWidth, gotHeight, width, height)
	}
	if got := view.jobStatus.Text(); got != "Warning: Resize finished: 2 completed, 0 skipped, 2 failed, 0 cancelled, of 4 image(s). Select an image in the list to see its result." {
		t.Fatalf("summary = %q", got)
	}

	selectRow(view, 0)
	if got, want := view.pathDetail.Text(), "Completed: saved to "+filepath.Join(folder, "photo.png")+"."; got != want {
		t.Fatalf("completed detail = %q, want %q", got, want)
	}
	selectRow(view, 2)
	if got := view.pathDetail.Text(); !strings.HasPrefix(got, "Failed: Read/decode error: ") {
		t.Fatalf("damaged detail = %q", got)
	}
	selectRow(view, 3)
	if got := view.pathDetail.Text(); got != "Failed: Unsupported format: "+ui.SourceUnsupportedDetail {
		t.Fatalf("unsupported detail = %q", got)
	}

	if len(opened) != 0 {
		t.Fatalf("the output folder was opened without a request: %q", <-opened)
	}
	if !view.NativeActionAvailable() || !enabled(view.addButton) || !enabled(view.clearButton) || enabled(view.cancelButton) || !enabled(view.openButton) {
		t.Fatal("the controls did not return to their idle state, with Open Output Folder offered")
	}
	if view.cancelButton.HasFocus() || !view.openButton.HasFocus() {
		t.Fatal("the keyboard focus did not move from Cancel Resize to Open Output Folder")
	}
	view.NativeCommand(jobCommandBase+openOutputFolderCommand, 0)
	select {
	case got := <-opened:
		if got != filepath.Clean(folder) {
			t.Fatalf("Open Output Folder opened %q, want %q", got, folder)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open Output Folder did not open the folder")
	}

	// A folder that does not respond holds only the goroutine opening it.
	unblock := make(chan struct{})
	view.openFolder = func(string) error {
		<-unblock
		return errors.New("explorer is unavailable")
	}
	view.NativeCommand(jobCommandBase+openOutputFolderCommand, 0)
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Warning: Resize finished:") {
		t.Fatalf("status while the folder is being opened = %q", got)
	}
	close(unblock)
	dispatcher.runNext(t)
	if got := view.jobStatus.Text(); got != "Error: The output folder could not be opened: explorer is unavailable" {
		t.Fatalf("status after a failed open = %q", got)
	}
}

// TestNativeViewRerunsPlanAroundEarlierResults pins that a re-run plans
// afresh (RSZ-049): under Rename the results take numbered names, which the
// detail explains, and under Skip the existing files are left untouched and
// the items are Skipped (AC-R09).
func TestNativeViewRerunsPlanAroundEarlierResults(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	folder := view.options.Options().OutputFolder
	view.NativeDropFiles([]string{writeViewFile(t, directory, "photo.png", encodeTestPNG(t, 8, 6))})
	settle(t, view, dispatcher)
	view.NativeAction()
	drainJob(t, view, dispatcher)

	view.NativeAction()
	drainJob(t, view, dispatcher)
	selectRow(view, 0)
	if got, want := view.pathDetail.Text(), "Completed: saved to "+filepath.Join(folder, "photo_2.png")+". A numbered name was used because "+ui.ResizeRenamedExists+"."; got != want {
		t.Fatalf("renamed detail = %q, want %q", got, want)
	}

	if err := view.options.SetCollision(CollisionSkip); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(folder, "photo.png"))
	if err != nil {
		t.Fatal(err)
	}
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if got := view.list.ItemText(0); got != "photo.png — 8 × 6 px — Skipped" {
		t.Fatalf("row after the Skip run = %q", got)
	}
	if got := view.pathDetail.Text(); got != "Skipped: "+filepath.Join(folder, "photo.png")+" was left untouched because "+ui.ResizeSkippedExists+"." {
		t.Fatalf("skipped detail = %q", got)
	}
	after, err := os.ReadFile(filepath.Join(folder, "photo.png"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the Skip run changed the existing file: %v", err)
	}
	if got := view.jobStatus.Text(); got != "Success: Resize finished: 0 completed, 1 skipped, 0 failed, 0 cancelled, of 1 image(s). Select an image in the list to see its result." {
		t.Fatalf("summary after the Skip run = %q", got)
	}
	if !enabled(view.openButton) {
		t.Fatal("Open Output Folder was not offered for a job whose results were skipped")
	}
}

// TestNativeViewOverwriteNeedsConfirmation pins that an Overwrite job asks
// first (RSZ-025, UX-002): declining starts nothing, and confirming passes
// the confirmation to the job, which replaces the existing file (AC-R10).
func TestNativeViewOverwriteNeedsConfirmation(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	folder := view.options.Options().OutputFolder
	existing := writeViewFile(t, folder, "photo.png", encodeTestPNG(t, 2, 2))
	view.NativeDropFiles([]string{writeViewFile(t, directory, "photo.png", encodeTestPNG(t, 8, 6))})
	settle(t, view, dispatcher)
	if err := view.options.SetCollision(CollisionOverwrite); err != nil {
		t.Fatal(err)
	}
	var requests []job.Request
	view.run = func(ctx context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
		requests = append(requests, request)
		return job.Run(ctx, request, observe)
	}
	answer := false
	var messages []string
	view.confirm = func(_ windows.Handle, title, message string) bool {
		messages = append(messages, title+"\n"+message)
		return answer
	}

	view.NativeAction()
	if view.active != nil || len(requests) != 0 {
		t.Fatal("a declined Overwrite job started")
	}
	if len(messages) != 1 || !strings.Contains(messages[0], ui.ResizeOverwriteTitle) || !strings.Contains(messages[0], folder) {
		t.Fatalf("confirmation = %q", messages)
	}
	if got := view.jobStatus.Text(); got != "Information: "+ui.ResizeJobNotConfirmed {
		t.Fatalf("status after declining = %q", got)
	}
	if width, height := imageSize(t, existing); width != 2 || height != 2 {
		t.Fatal("declining changed the existing file")
	}

	answer = true
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if len(requests) != 1 || !requests[0].ConfirmOverwrite || requests[0].Collision != CollisionOverwrite {
		t.Fatalf("requests = %+v, want one confirmed Overwrite job", requests)
	}
	width, height := expectedSize(t, view, 8, 6)
	if gotWidth, gotHeight := imageSize(t, existing); gotWidth != width || gotHeight != height {
		t.Fatalf("replaced file = %d × %d, want %d × %d", gotWidth, gotHeight, width, height)
	}
	if got := view.list.ItemText(0); got != "photo.png — 8 × 6 px — Completed" {
		t.Fatalf("row after the confirmed job = %q", got)
	}
}

// TestNativeViewChecksTheListAgainAfterTheOverwriteConfirmation pins that
// an image dropped while the Overwrite confirmation's modal loop runs cannot
// join a job while it is still being checked: the list is checked again once
// the confirmation is answered (RSZ-040), and the next start takes it.
func TestNativeViewChecksTheListAgainAfterTheOverwriteConfirmation(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{writeViewSource(t, directory, "photo.png")})
	settle(t, view, dispatcher)
	if err := view.options.SetCollision(CollisionOverwrite); err != nil {
		t.Fatal(err)
	}
	var requests []job.Request
	view.run = func(ctx context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
		requests = append(requests, request)
		return job.Run(ctx, request, observe)
	}
	late := writeViewSource(t, directory, "late.png")
	view.confirm = func(windows.Handle, string, string) bool {
		view.NativeDropFiles([]string{late})
		return true
	}

	view.NativeAction()
	if view.active != nil {
		t.Fatal("a job started while an image dropped during the confirmation was still being checked")
	}
	if got := view.jobStatus.Text(); got != "Error: Validation error: "+fmt.Sprintf(ui.ResizeJobChecking, 1) {
		t.Fatalf("status after the confirmation = %q", got)
	}
	settle(t, view, dispatcher)
	view.confirm = func(windows.Handle, string, string) bool { return true }
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if len(requests) != 1 || len(requests[0].Inputs) != 2 {
		t.Fatalf("requests = %+v, want one job of both images", requests)
	}
}

// cancellableRunner reports its first item Processing, waits for the job to
// be cancelled, and then reports every item Cancelled, as job.Run does for a
// job cancelled while its first item is in flight.
type cancellableRunner struct {
	started chan struct{}
}

func (r *cancellableRunner) run(ctx context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
	count := len(request.Inputs)
	statuses := make([]job.Status, count)
	progress := job.Progress{Total: count, Pending: count - 1, Processing: 1}
	statuses[0] = job.Status{State: job.StateProcessing, Destination: `C:\Out\first.png`}
	observe(job.Event{Index: 0, Source: request.Inputs[0].Path, Status: statuses[0], Progress: progress})
	close(r.started)
	<-ctx.Done()
	for index := range statuses {
		if statuses[index].State == job.StateProcessing {
			progress.Processing--
		} else {
			progress.Pending--
		}
		progress.Cancelled++
		statuses[index] = job.Status{State: job.StateCancelled}
		observe(job.Event{Index: index, Source: request.Inputs[index].Path, Status: statuses[index], Progress: progress})
	}
	return job.Summary{Items: statuses, Progress: progress}, nil
}

// TestNativeViewCancelStopsTheJob pins the running job's interaction
// (AC-A04, AC-R13): its progress names the file being processed, selecting
// an image shows its state, the source list cannot change and Start Resize
// is refused, with a notice the job's end clears, and Cancel Resize stops
// the job, which ends every unfinished image Cancelled (RSZ-045).
func TestNativeViewCancelStopsTheJob(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{writeViewSource(t, directory, "first.png"), writeViewSource(t, directory, "second.png")})
	settle(t, view, dispatcher)
	runner := &cancellableRunner{started: make(chan struct{})}
	view.run = runner.run
	t.Cleanup(func() { waitJobDone(t, view) })

	view.NativeAction()
	<-runner.started
	dispatcher.runNext(t)
	if got := view.jobStatus.Text(); got != "Information: Resizing: 0 of 2 image(s) done — 0 completed, 0 skipped, 0 failed. Processing first.png." {
		t.Fatalf("progress = %q", got)
	}
	selectRow(view, 0)
	if got := view.pathDetail.Text(); got != `Processing: being resized and saved to C:\Out\first.png.` {
		t.Fatalf("processing detail = %q", got)
	}
	if got := listContents(view.list); got[0] != "first.png — 4 × 3 px — Processing…" || got[1] != "second.png — 4 × 3 px — Pending" {
		t.Fatalf("rows while processing = %q", got)
	}

	view.NativeAction()
	if got := view.jobStatus.Text(); got != "Warning: "+ui.ResizeJobRunning {
		t.Fatalf("status after a second start = %q", got)
	}
	// The shell keeps the no-drop cursor while the job runs; a drop that
	// arrives all the same is refused.
	if view.NativeDropAvailable() {
		t.Fatal("drops stayed available while the job runs")
	}
	view.NativeDropFiles([]string{writeViewSource(t, directory, "third.png")})
	view.NativeCommand(sourcesCommandBase+clearListCommand, 0)
	view.NativeCommand(sourcesCommandBase+removeSelectedCommand, 0)
	if view.inventory.Len() != 2 {
		t.Fatalf("the source list changed while the job runs: %d items", view.inventory.Len())
	}
	if got := view.status.Text(); got != "Warning: "+ui.ImagesBusy {
		t.Fatalf("status after changing the list = %q", got)
	}

	view.cancelButton.Focus()
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	if enabled(view.cancelButton) || view.cancelButton.HasFocus() || !view.list.HasFocus() {
		t.Fatal("Cancel Resize stayed enabled, or kept the focus, once the job was cancelled")
	}
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Information: Cancelling:") {
		t.Fatalf("status while cancelling = %q", got)
	}
	drainJob(t, view, dispatcher)
	if got := listContents(view.list); got[0] != "first.png — 4 × 3 px — Cancelled" || got[1] != "second.png — 4 × 3 px — Cancelled" {
		t.Fatalf("rows after cancelling = %q", got)
	}
	if got := view.pathDetail.Text(); got != ui.ResizeDetailCancelled {
		t.Fatalf("cancelled detail = %q", got)
	}
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Warning: Resize cancelled: 0 completed, 0 skipped, 0 failed, 2 cancelled, of 2 image(s).") {
		t.Fatalf("summary after cancelling = %q", got)
	}
	if !view.NativeActionAvailable() || !view.NativeDropAvailable() || enabled(view.openButton) {
		t.Fatal("Start Resize or drops were not available again, or Open Output Folder was offered without results")
	}
	if got := view.status.Text(); got != "" {
		t.Fatalf("status once the job ended = %q, want the notice that the list cannot change cleared", got)
	}
}

// TestNativeViewKeepsANoticeRaisedAfterTheBusyOne pins that the job's end
// clears only the notice that the source list cannot change: one the view
// raised after it, such as a failed settings save, stays.
func TestNativeViewKeepsANoticeRaisedAfterTheBusyOne(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "only.png")})
	settle(t, view, dispatcher)
	runner := &cancellableRunner{started: make(chan struct{})}
	view.run = runner.run
	t.Cleanup(func() { waitJobDone(t, view) })
	view.NativeAction()
	<-runner.started
	view.NativeCommand(sourcesCommandBase+clearListCommand, 0)
	view.presentResult(fmt.Errorf("%w: disk is read-only", ErrPersistence), "")
	later := view.status.Text()
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	if got := view.status.Text(); got != later || got == "" {
		t.Fatalf("status once the job ended = %q, want %q", got, later)
	}
}

// TestNativeViewRefusesJobsThatCannotStart pins the start checks (RSZ-040):
// an empty list, images still being checked, and invalid JPEG text each
// explain why nothing started, and an invalid field is flagged beside it
// (UX-003). JPEG text is ignored for PNG output and applied otherwise.
func TestNativeViewRefusesJobsThatCannotStart(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	var requests []job.Request
	view.run = func(_ context.Context, request job.Request, _ func(job.Event)) (job.Summary, error) {
		requests = append(requests, request)
		return job.Summary{Items: make([]job.Status, len(request.Inputs)), Progress: job.Progress{Total: len(request.Inputs)}}, nil
	}
	view.NativeAction()
	if got := view.jobStatus.Text(); got != "Error: Validation error: "+ui.ResizeJobNoImages {
		t.Fatalf("status with no images = %q", got)
	}

	gate := useGatedInspector(view)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{writeViewSource(t, directory, "slow.png")})
	gate.waitFor(t, gate.started)
	view.NativeAction()
	if got := view.jobStatus.Text(); got != "Error: Validation error: 1 image(s) are still being checked. Wait until checking finishes, or remove those images, then start again." {
		t.Fatalf("status while checking = %q", got)
	}
	gate.release <- struct{}{}
	settle(t, view, dispatcher)

	view.quality.Edit.SetText("0")
	view.NativeAction()
	if !strings.HasPrefix(view.quality.Error.Text(), "Validation error: JPEG quality") || view.jobStatus.Text() != "Error: "+view.quality.Error.Text() {
		t.Fatalf("quality error = %q, status = %q", view.quality.Error.Text(), view.jobStatus.Text())
	}
	view.quality.Edit.SetText("90")
	view.background.Edit.SetText("white")
	view.NativeAction()
	if !strings.HasPrefix(view.background.Error.Text(), "Validation error: Enter a background color") {
		t.Fatalf("background error = %q", view.background.Error.Text())
	}
	if len(requests) != 0 {
		t.Fatalf("%d jobs started despite the refusals", len(requests))
	}

	view.quality.Edit.SetText("75")
	view.background.Edit.SetText("#00ff00")
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if len(requests) != 1 || requests[0].Settings.JPEGQuality != 75 || requests[0].Settings.Background.G != 0xff || view.options.Options().JPEGQuality != 75 || view.options.Options().Background != "#00FF00" {
		t.Fatalf("job settings = %+v, options = %+v", requests, view.options.Options())
	}
	if view.quality.Error.Text() != "" || view.background.Error.Text() != "" || view.background.Edit.Text() != "#00FF00" {
		t.Fatal("the applied fields kept their errors or their uncanonical text")
	}

	if err := view.options.SetFormat(FormatPNG); err != nil {
		t.Fatal(err)
	}
	view.quality.Edit.SetText("not a number")
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if len(requests) != 2 {
		t.Fatalf("a PNG job was refused for JPEG text it ignores: %q", view.jobStatus.Text())
	}
}

// TestNativeViewReportsAJobThatCannotStart pins that a job the planner
// refuses as a whole — here an output folder a file stands in the way of —
// is reported with its category and recovery (UX-010), and that every row
// shows its content check again.
func TestNativeViewReportsAJobThatCannotStart(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	blocker := writeViewFile(t, t.TempDir(), "blocker", []byte("a file"))
	if err := view.options.SetOutputFolder(filepath.Join(blocker, "Resized")); err != nil {
		t.Fatal(err)
	}
	view.NativeDropFiles([]string{writeViewSource(t, directory, "photo.png")})
	settle(t, view, dispatcher)
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Error: Write/encode error: The output folder could not be created or used: ") {
		t.Fatalf("status = %q", got)
	}
	if got := view.list.ItemText(0); got != "photo.png — 4 × 3 px — Ready" {
		t.Fatalf("row after the failed job = %q", got)
	}
	if !view.NativeActionAvailable() || enabled(view.openButton) || enabled(view.cancelButton) {
		t.Fatal("the controls did not return to their idle state")
	}
}

// TestNativeViewKeepsOfferingTheLatestResults pins that Open Output Folder
// is the latest job's once it saved a result (RSZ-048): a job is offered no
// folder before it saves one, and a job that saves none — here one whose
// output folder cannot be created — leaves offered the folder the previous
// job saved its results in.
func TestNativeViewKeepsOfferingTheLatestResults(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	folder := view.options.Options().OutputFolder
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "photo.png")})
	settle(t, view, dispatcher)
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if !enabled(view.openButton) {
		t.Fatal("Open Output Folder was not offered for the saved result")
	}

	blocker := writeViewFile(t, t.TempDir(), "blocker", []byte("a file"))
	if err := view.options.SetOutputFolder(filepath.Join(blocker, "Resized")); err != nil {
		t.Fatal(err)
	}
	view.NativeAction()
	if enabled(view.openButton) {
		t.Fatal("Open Output Folder stayed offered while the next job had saved nothing")
	}
	drainJob(t, view, dispatcher)
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Error: Write/encode error: The output folder could not be created or used: ") {
		t.Fatalf("status = %q", got)
	}
	if !enabled(view.openButton) {
		t.Fatal("a job that saved nothing withdrew the previous job's results folder")
	}
	opened := make(chan string, 1)
	view.openFolder = func(path string) error {
		opened <- path
		return nil
	}
	view.NativeCommand(jobCommandBase+openOutputFolderCommand, 0)
	select {
	case got := <-opened:
		if got != filepath.Clean(folder) {
			t.Fatalf("Open Output Folder opened %q, want %q", got, folder)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open Output Folder did not open the folder")
	}
}

// TestNativeViewShowsNoEarlierOpenFailureOverANewJob pins that a failure to
// open the output folder that arrives once another job has started leaves
// that job's progress in the job section.
func TestNativeViewShowsNoEarlierOpenFailureOverANewJob(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "only.png")})
	settle(t, view, dispatcher)
	view.NativeAction()
	drainJob(t, view, dispatcher)
	opening, unblock := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	t.Cleanup(func() { unblockOnce.Do(func() { close(unblock) }) })
	view.openFolder = func(string) error {
		close(opening)
		<-unblock
		return errors.New("the share is offline")
	}
	view.NativeCommand(jobCommandBase+openOutputFolderCommand, 0)
	<-opening

	runner := &cancellableRunner{started: make(chan struct{})}
	view.run = runner.run
	t.Cleanup(func() { waitJobDone(t, view) })
	view.NativeAction()
	<-runner.started
	dispatcher.runNext(t)
	progress := view.jobStatus.Text()
	unblockOnce.Do(func() { close(unblock) })
	dispatcher.runNext(t)
	if got := view.jobStatus.Text(); got != progress {
		t.Fatalf("status once the earlier open failed = %q, want the new job's progress %q", got, progress)
	}
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
}

// TestNativeViewRetriesARefusedJobPost pins that a refused post of a job's
// events loses nothing: the post is retried until it is accepted, so the
// job's outcome always reaches the view.
func TestNativeViewRetriesARefusedJobPost(t *testing.T) {
	view, queued := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "only.png")})
	settle(t, view, queued)
	dispatcher := &failingDispatcher{queuedDispatcher: queuedDispatcher{tasks: make(chan func(), 16)}, fails: 2}
	view.NativeDispatcher(dispatcher)
	view.NativeAction()
	drainJob(t, view, &dispatcher.queuedDispatcher)
	if got := view.list.ItemText(0); got != "only.png — 4 × 3 px — Completed" {
		t.Fatalf("row after the retried post = %q", got)
	}
	if attempts := dispatcher.attempted(); attempts < 3 {
		t.Fatalf("%d post attempts, want two refused and then accepted ones", attempts)
	}
}

// TestNativeViewShutdownCancelsAndWaitsForTheJob pins that closing the
// window cancels a running job and that shutdown waits for it to stop, so
// its items in flight remove their temporary files before the process
// exits.
func TestNativeViewShutdownCancelsAndWaitsForTheJob(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "photo.png")})
	settle(t, view, dispatcher)
	view.NativeShutdown()

	var mu sync.Mutex
	stopped := false
	view.run = func(ctx context.Context, request job.Request, _ func(job.Event)) (job.Summary, error) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		stopped = true
		mu.Unlock()
		return job.Summary{}, nil
	}
	view.NativeAction()
	view.NativeDestroy()
	view.NativeShutdown()
	mu.Lock()
	defer mu.Unlock()
	if !stopped {
		t.Fatal("shutdown returned before the cancelled job stopped")
	}
}

// TestNativeViewChecksFailedImagesAgainBeforeAJob pins that a run takes the
// listed images as they now are (RSZ-049): an image whose content check
// failed is checked again before the job starts, so a list of nothing else
// still starts one, which says that no image is ready while none can be used,
// and resizes an image replaced by a valid one, or one that has come back,
// once they can. An image refused for its extension is not checked again.
func TestNativeViewChecksFailedImagesAgainBeforeAJob(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	folder := view.options.Options().OutputFolder
	fixed := writeViewFile(t, directory, "fixed.png", []byte("\x89PNG\r\n\x1a\nnot yet a PNG"))
	missing := filepath.Join(directory, "missing.png")
	view.NativeDropFiles([]string{fixed, missing, writeViewFile(t, directory, "notes.txt", []byte("text"))})
	settle(t, view, dispatcher)
	before := []string{"fixed.png — Damaged or incomplete", "missing.png — Cannot be read", "notes.txt — Unsupported format"}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(before, "\n") {
		t.Fatalf("rows after adding = %q, want %q", got, before)
	}

	view.NativeAction()
	drainJob(t, view, dispatcher)
	if got := view.jobStatus.Text(); got != "Error: Validation error: "+ui.ResizeJobNoImages {
		t.Fatalf("status while no image can be used = %q", got)
	}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(before, "\n") {
		t.Fatalf("rows after the refused job = %q, want %q", got, before)
	}

	writeViewFile(t, directory, "fixed.png", encodeTestPNG(t, 8, 6))
	writeViewFile(t, directory, "missing.png", encodeTestPNG(t, 6, 8))
	view.NativeAction()
	drainJob(t, view, dispatcher)
	after := []string{"fixed.png — 8 × 6 px — Completed", "missing.png — 6 × 8 px — Completed", "notes.txt — Failed"}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(after, "\n") {
		t.Fatalf("rows after the job = %q, want %q (status %q)", got, after, view.jobStatus.Text())
	}
	for _, name := range []string{"fixed.png", "missing.png"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("%s was not resized: %v", name, err)
		}
	}
	if !view.NativeActionAvailable() {
		t.Fatal("Start Resize stayed unavailable")
	}
}

// TestNativeViewExplainsAFieldTheOptionsRefuse pins that a JPEG field the
// options refuse to apply at Start Resize — the options are invalid as a
// whole while no output folder is selected — is explained beside the job's
// progress, where the user looks, and starts nothing (RSZ-040).
func TestNativeViewExplainsAFieldTheOptionsRefuse(t *testing.T) {
	parent := newViewTestParent(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view, _ := newNativeViewForTest(t, store, &value, "", parent)
	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1024)}
	view.NativeDispatcher(dispatcher)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "photo.png")})
	settle(t, view, dispatcher)
	refusal := view.options.SetJPEGQuality(80)
	if refusal == nil || errors.Is(refusal, ErrPersistence) {
		t.Fatalf("SetJPEGQuality(80) = %v, want a validation refusal", refusal)
	}

	view.quality.Edit.SetText("80")
	view.NativeAction()
	if view.active != nil {
		t.Fatal("a job started with options the view could not apply")
	}
	if got, want := view.jobStatus.Text(), "Error: "+ui.FormatError(ui.ErrorValidation, refusal); got != want {
		t.Fatalf("job status = %q, want %q", got, want)
	}
}

// stuckRunner stands for a job whose first item's read is blocked in the
// operating system: it reports the item Processing and, once cancelled, the
// others Cancelled, as job.Run does, but the stuck item never stops until
// the test releases it. It then claims every item Completed, which the view
// must ignore once it has let the job go.
type stuckRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *stuckRunner) run(ctx context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
	count := len(request.Inputs)
	progress := job.Progress{Total: count, Pending: count - 1, Processing: 1}
	observe(job.Event{Index: 0, Source: request.Inputs[0].Path, Status: job.Status{State: job.StateProcessing, Destination: `C:\Out\stuck.png`}, Progress: progress})
	close(r.started)
	<-ctx.Done()
	for index := 1; index < count; index++ {
		progress.Pending--
		progress.Cancelled++
		observe(job.Event{Index: index, Source: request.Inputs[index].Path, Status: job.Status{State: job.StateCancelled}, Progress: progress})
	}
	<-r.release
	statuses := make([]job.Status, count)
	for index := range statuses {
		statuses[index] = job.Status{State: job.StateCompleted, Destination: `C:\Out\late.png`}
	}
	return job.Summary{Folder: `C:\Out`, Items: statuses, Progress: job.Progress{Total: count, Completed: count}}, nil
}

// TestNativeViewLetsGoOfAJobThatDoesNotStop pins that a cancelled job whose
// read or write is not responding cannot lock the view (RSZ-045, AC-A04):
// once the release delay has passed, every item that had not ended is
// Cancelled, the one in flight with why its outcome is unknown, the summary
// says the job did not stop, the list shows the content checks the job made
// again before it stopped responding, and Start Resize and the source list
// are available again; what the job reports once its blocked call returns
// is ignored, and the next job runs.
func TestNativeViewLetsGoOfAJobThatDoesNotStop(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	fixed := writeViewFile(t, directory, "fixed.png", []byte("\x89PNG\r\n\x1a\nnot yet a PNG"))
	view.NativeDropFiles([]string{writeViewSource(t, directory, "stuck.png"), writeViewSource(t, directory, "waiting.png"), fixed})
	settle(t, view, dispatcher)
	writeViewFile(t, directory, "fixed.png", encodeTestPNG(t, 8, 6))
	runner := &stuckRunner{started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.release) }) }
	t.Cleanup(func() { waitJobDone(t, view) })
	t.Cleanup(release)
	view.run = runner.run
	view.releaseDelay = 50 * time.Millisecond

	view.NativeAction()
	<-runner.started
	dispatcher.runNext(t)
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	want := []string{"stuck.png — 4 × 3 px — Cancelled", "waiting.png — 4 × 3 px — Cancelled", "fixed.png — 8 × 6 px — Cancelled"}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows after letting the job go = %q, want %q", got, want)
	}
	if view.inventory.Ready() != 3 {
		t.Fatalf("%d images Ready after letting the job go, want 3: the check made again was lost", view.inventory.Ready())
	}
	selectRow(view, 0)
	if got := view.pathDetail.Text(); got != ui.ResizeDetailReleased {
		t.Fatalf("stuck detail = %q", got)
	}
	selectRow(view, 1)
	if got := view.pathDetail.Text(); got != ui.ResizeDetailCancelled {
		t.Fatalf("waiting detail = %q", got)
	}
	status := view.jobStatus.Text()
	if !strings.HasPrefix(status, "Warning: Resize cancelled: 0 completed, 0 skipped, 0 failed, 3 cancelled, of 3 image(s).") || !strings.HasSuffix(status, ui.ResizeJobReleased) {
		t.Fatalf("summary after letting the job go = %q", status)
	}
	if !view.NativeActionAvailable() || !view.NativeDropAvailable() || !enabled(view.addButton) || enabled(view.cancelButton) || enabled(view.openButton) {
		t.Fatal("the controls did not return to their idle state")
	}

	release()
	waitJobDone(t, view)
	select {
	case task := <-dispatcher.tasks:
		task()
	case <-time.After(time.Second):
		t.Fatal("the job's outcome was not posted once its blocked call returned")
	}
	if got := listContents(view.list); got[0] != "stuck.png — 4 × 3 px — Cancelled" || view.jobStatus.Text() != status {
		t.Fatalf("the late outcome of a job let go changed the view: rows %q, status %q", got, view.jobStatus.Text())
	}

	view.run = job.Run
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if got := listContents(view.list); got[0] != "stuck.png — 4 × 3 px — Completed" || got[1] != "waiting.png — 4 × 3 px — Completed" || got[2] != "fixed.png — 8 × 6 px — Completed" {
		t.Fatalf("rows after the next job = %q", got)
	}
}

// TestNativeViewLetsGoOfAJobStuckCheckingAgain pins the outcome of a job let
// go while a check it makes again is blocked (RSZ-045): as job.Run ends the
// items of a job cancelled before it planned, an image refused for its
// extension ends Failed with why, while the image not checked again and the
// Ready one are Cancelled; the job never runs, and what its goroutine
// reports once the check returns is ignored.
func TestNativeViewLetsGoOfAJobStuckCheckingAgain(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{
		writeViewSource(t, directory, "ready.png"),
		writeViewFile(t, directory, "damaged.png", []byte("\x89PNG\r\n\x1a\nnot really a PNG")),
		writeViewFile(t, directory, "notes.txt", []byte("text")),
	})
	settle(t, view, dispatcher)
	// The check made again blocks without observing the cancellation, as a
	// read on a drive that stopped responding does.
	blocked, unblock := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	release := func() { unblockOnce.Do(func() { close(unblock) }) }
	view.inspector.Close()
	view.inspector = NewInspector(func(context.Context, string) (decode.Info, error) {
		close(blocked)
		<-unblock
		return decode.Info{}, &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}
	}, view.deliverInspection)
	t.Cleanup(func() { waitJobDone(t, view) })
	t.Cleanup(release)
	var ran atomic.Bool
	view.run = func(context.Context, job.Request, func(job.Event)) (job.Summary, error) {
		ran.Store(true)
		return job.Summary{}, nil
	}
	view.releaseDelay = 50 * time.Millisecond

	view.NativeAction()
	<-blocked
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	want := []string{"ready.png — 4 × 3 px — Cancelled", "damaged.png — Cancelled", "notes.txt — Failed"}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows after letting the job go = %q, want %q", got, want)
	}
	selectRow(view, 2)
	if got := view.pathDetail.Text(); got != "Failed: Unsupported format: "+ui.SourceUnsupportedDetail {
		t.Fatalf("unsupported detail = %q", got)
	}
	status := view.jobStatus.Text()
	if !strings.HasPrefix(status, "Warning: Resize cancelled: 0 completed, 0 skipped, 1 failed, 2 cancelled, of 3 image(s).") || !strings.HasSuffix(status, ui.ResizeJobReleased) {
		t.Fatalf("summary after letting the job go = %q", status)
	}

	release()
	waitJobDone(t, view)
	if ran.Load() {
		t.Fatal("the job ran although it was cancelled while it checked an image again")
	}
	select {
	case task := <-dispatcher.tasks:
		task()
	case <-time.After(time.Second):
		t.Fatal("the job's outcome was not posted once its blocked check returned")
	}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(want, "\n") || view.jobStatus.Text() != status {
		t.Fatalf("the late outcome of a job let go changed the view: rows %q, status %q", got, view.jobStatus.Text())
	}
}

// TestNativeViewCancelledWhileCheckingAgain pins a job cancelled while it
// checks failed images again (RSZ-045, RSZ-049): an image it checked again
// fails with its current reason, one it had not checked yet is Cancelled
// rather than Failed with the reason of a check it never repeated — the job,
// which would report it so, never runs — and the summary reads as
// cancelled. The job section says, until the job reports, that it checks
// those images first.
func TestNativeViewCancelledWhileCheckingAgain(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	gate := useGatedInspector(view)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{writeViewSource(t, directory, "damaged_first.png"), writeViewSource(t, directory, "damaged_second.png")})
	gate.release <- struct{}{}
	gate.release <- struct{}{}
	settle(t, view, dispatcher)
	gate.waitFor(t, gate.started)
	gate.waitFor(t, gate.started)
	t.Cleanup(func() { waitJobDone(t, view) })
	var ran atomic.Bool
	view.run = func(ctx context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
		ran.Store(true)
		return job.Run(ctx, request, observe)
	}

	view.NativeAction()
	if got, want := view.jobStatus.Text(), "Information: Starting to resize 2 image(s)…"+fmt.Sprintf(ui.ResizeJobRechecking, 2); got != want {
		t.Fatalf("status at the start = %q, want %q", got, want)
	}
	if first := gate.waitFor(t, gate.started); filepath.Base(first) != "damaged_first.png" {
		t.Fatalf("checked %q again first", first)
	}
	gate.release <- struct{}{}
	gate.waitFor(t, gate.started)
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	want := []string{"damaged_first.png — Failed", "damaged_second.png — Cancelled"}
	if got := listContents(view.list); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows after cancelling = %q, want %q", got, want)
	}
	selectRow(view, 1)
	if got := view.pathDetail.Text(); got != ui.ResizeDetailCancelled {
		t.Fatalf("unchecked detail = %q", got)
	}
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Warning: Resize cancelled: 0 completed, 0 skipped, 1 failed, 1 cancelled, of 2 image(s).") {
		t.Fatalf("summary after cancelling = %q", got)
	}
	waitJobDone(t, view)
	if ran.Load() {
		t.Fatal("the job ran although it was cancelled while it checked images again")
	}
}

// TestNativeViewKeepsTheResultsOfAJobThatStopsUnexpectedly pins NFR-021 for
// a panic that escapes the job after it started: the images that finished
// keep their results, the others are Failed with the unexpected error, and
// the job says it stopped rather than that it could not start. A panic
// before the job reported anything is a job that could not start, and the
// rows show their content checks again.
func TestNativeViewKeepsTheResultsOfAJobThatStopsUnexpectedly(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	view.NativeDropFiles([]string{writeViewSource(t, directory, "done.png"), writeViewSource(t, directory, "next.png")})
	settle(t, view, dispatcher)
	view.run = func(_ context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
		observe(job.Event{Index: 0, Source: request.Inputs[0].Path, Status: job.Status{State: job.StateCompleted, Destination: `C:\Out\done.png`}, Progress: job.Progress{Total: 2, Pending: 1, Completed: 1}})
		panic("the coordinator failed")
	}
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if got := listContents(view.list); got[0] != "done.png — 4 × 3 px — Completed" || got[1] != "next.png — 4 × 3 px — Failed" {
		t.Fatalf("rows after the interrupted job = %q", got)
	}
	selectRow(view, 1)
	if got := view.pathDetail.Text(); !strings.HasPrefix(got, "Failed: Unexpected error: ") || !strings.Contains(got, "the coordinator failed") {
		t.Fatalf("unfinished detail = %q", got)
	}
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Error: Unexpected error: The resize job stopped unexpectedly: ") {
		t.Fatalf("status after the interrupted job = %q", got)
	}
	if !enabled(view.openButton) {
		t.Fatal("Open Output Folder was not offered for the result the job saved")
	}

	view.run = func(context.Context, job.Request, func(job.Event)) (job.Summary, error) {
		panic("the job failed at once")
	}
	view.NativeAction()
	drainJob(t, view, dispatcher)
	if got := listContents(view.list); got[0] != "done.png — 4 × 3 px — Ready" || got[1] != "next.png — 4 × 3 px — Ready" {
		t.Fatalf("rows after a job that could not start = %q", got)
	}
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Error: Unexpected error: The resize job could not start: ") {
		t.Fatalf("status after a job that could not start = %q", got)
	}
}

const (
	lbGetItemData = 0x0199
	lbSetItemData = 0x019a
)

var procSendMessageW = testUser32.NewProc("SendMessageW")

// TestNativeViewFinishesAJobThatEndedBeforeItsCancel pins two things about
// a job whose last item ended before Cancel Resize reached it: its summary
// reads as finished, as nothing was cancelled, and the outcome leaves alone
// the rows the job's events already show, which rewriting would delete and
// reinsert — dropping the row's data, and announcing the focused row again.
func TestNativeViewFinishesAJobThatEndedBeforeItsCancel(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "only.png")})
	settle(t, view, dispatcher)
	completed := job.Status{State: job.StateCompleted, Destination: `C:\Out\only.png`}
	view.run = func(ctx context.Context, request job.Request, observe func(job.Event)) (job.Summary, error) {
		observe(job.Event{Index: 0, Source: request.Inputs[0].Path, Status: completed, Progress: job.Progress{Total: 1, Completed: 1}})
		<-ctx.Done()
		return job.Summary{Folder: `C:\Out`, Items: []job.Status{completed}, Progress: job.Progress{Total: 1, Completed: 1}}, nil
	}
	t.Cleanup(func() { waitJobDone(t, view) })

	view.NativeAction()
	dispatcher.runNext(t)
	if got := view.list.ItemText(0); got != "only.png — 4 × 3 px — Completed" {
		t.Fatalf("row after the last event = %q", got)
	}
	const marker = 0x5eed
	procSendMessageW.Call(uintptr(view.list.Handle()), lbSetItemData, 0, marker)
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	if got := view.jobStatus.Text(); !strings.HasPrefix(got, "Success: Resize finished: 1 completed, 0 skipped, 0 failed, 0 cancelled, of 1 image(s).") {
		t.Fatalf("summary = %q", got)
	}
	if data, _, _ := procSendMessageW.Call(uintptr(view.list.Handle()), lbGetItemData, 0, 0); data != marker {
		t.Fatal("the outcome rewrote a row its last event already showed")
	}
}

// TestNativeViewJobSectionKeepsItsHeightWhileAJobRuns pins AC-A04's review
// of the list while a job runs: the job section keeps room for the longest
// progress the job can show, measured once for the width, so a progress that
// wraps differently for each file does not move the source list between the
// user's clicks, and a progress taller than that room, for a name that
// renders wider than the longest by bytes, grows the section once, never
// shrinking it again while the job runs.
func TestNativeViewJobSectionKeepsItsHeightWhileAJobRuns(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	long := "a_much_longer_source_file_name_that_wraps_the_progress_onto_more_lines.png"
	view.NativeDropFiles([]string{writeViewSource(t, directory, "a.png"), writeViewSource(t, directory, long)})
	settle(t, view, dispatcher)
	runner := &cancellableRunner{started: make(chan struct{})}
	view.run = runner.run
	t.Cleanup(func() { waitJobDone(t, view) })
	scale := func(value int32) int32 { return value }
	// Each line holds 40 columns, 20 pixels high; a W takes 4 columns.
	cancellingMeasures := 0
	measure := func(text string, width int32) int32 {
		if text == "" {
			return 0
		}
		if strings.HasPrefix(text, "Information: Cancelling:") && !strings.HasPrefix(view.jobStatus.Text(), "Information: Cancelling:") {
			cancellingMeasures++
		}
		columns := len([]rune(text)) + 3*strings.Count(text, "W")
		return 20 * int32((columns+39)/40)
	}

	view.NativeAction()
	<-runner.started
	height := view.NativeHeight(600, scale, measure)
	dispatcher.runNext(t)
	if got := view.NativeHeight(600, scale, measure); got != height {
		t.Fatalf("height with the first progress = %d, want %d", got, height)
	}
	view.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: progressMessage(view.active.progress, long, 1, false)})
	if got := view.NativeHeight(600, scale, measure); got != height {
		t.Fatalf("height with the long file in progress = %d, want %d", got, height)
	}
	if cancellingMeasures != 1 {
		t.Fatalf("the reserved progress was measured %d times at one width, want once", cancellingMeasures)
	}

	wide := strings.Repeat("W", 30) + ".png"
	if len(wide) >= len(long) {
		t.Fatal("the wide name must be shorter by bytes than the longest")
	}
	view.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: progressMessage(view.active.progress, wide, 1, false)})
	grown := view.NativeHeight(600, scale, measure)
	if grown <= height {
		t.Fatalf("height with the wide file in progress = %d, want more than %d", grown, height)
	}
	view.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: progressMessage(view.active.progress, "a.png", 1, false)})
	if got := view.NativeHeight(600, scale, measure); got != grown {
		t.Fatalf("height once the wide file ended = %d, want %d", got, grown)
	}
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	if got := view.NativeHeight(600, scale, measure); got != grown {
		t.Fatalf("height while cancelling = %d, want %d", got, grown)
	}
	drainJob(t, view, dispatcher)
}

const (
	wmCommand   = 0x0111
	gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC, -4
)

var (
	procSetWindowLongPtrW = testUser32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW   = testUser32.NewProc("CallWindowProcW")
	// commandRelay is the view and the original window procedure of the test
	// parent while relayCommands relays its controls' notifications.
	commandRelay struct {
		original uintptr
		command  func(id int32, notification uint16)
	}
	commandRelayCallback = windows.NewCallback(relayCommand)
)

// relayCommand is the test parent's window procedure while relayCommands
// applies: it hands every WM_COMMAND to the view before the parent's own
// procedure.
func relayCommand(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	if message == wmCommand && commandRelay.command != nil {
		commandRelay.command(int32(wParam&0xffff), uint16(wParam>>16))
	}
	result, _, _ := procCallWindowProcW.Call(commandRelay.original, uintptr(hwnd), uintptr(message), wParam, lParam)
	return result
}

// relayCommands makes the view's parent relay its controls' notifications
// to the view and, after one the view handles, call layout, as the shell
// lays the view out again after a control's focus notification.
func relayCommands(t *testing.T, view *NativeView, layout func()) {
	t.Helper()
	original, _, err := procSetWindowLongPtrW.Call(uintptr(view.parent), gwlpWndProc, commandRelayCallback)
	if original == 0 {
		t.Fatalf("SetWindowLongPtrW: %v", err)
	}
	commandRelay.original = original
	commandRelay.command = func(id int32, notification uint16) {
		if view.NativeCommand(id, notification) {
			layout()
		}
	}
	t.Cleanup(func() {
		procSetWindowLongPtrW.Call(uintptr(view.parent), gwlpWndProc, original)
		commandRelay.command = nil
	})
}

// TestNativeViewReservesNoRoomForThePreviousSummary pins that a job's room
// in the job section is not sized by the previous job's summary: moving the
// focus to Cancel Resize lays the view out again, as the shell does on the
// button's focus notification, and the section already shows the starting
// notice then, so it keeps room only for what the job can show (AC-A04).
func TestNativeViewReservesNoRoomForThePreviousSummary(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "only.png")})
	settle(t, view, dispatcher)
	runner := &cancellableRunner{started: make(chan struct{})}
	view.run = runner.run
	t.Cleanup(func() { waitJobDone(t, view) })
	scale := func(value int32) int32 { return value }
	// Each line holds 40 columns, 20 pixels high.
	measure := func(text string, width int32) int32 {
		return 20 * int32((len([]rune(text))+39)/40)
	}
	relayed := 0
	relayCommands(t, view, func() {
		relayed++
		view.NativeHeight(600, scale, measure)
	})
	previous := ui.Notice{Kind: ui.NoticeWarning, Message: strings.Repeat("The previous job's long summary. ", 20)}
	view.showJobNotice(previous)
	stale := measure(previous.Text(), 600)

	view.NativeAction()
	<-runner.started
	focused, laidOut := view.cancelButton.HasFocus(), relayed
	height := view.jobStatusHeight(600, scale, measure)
	view.NativeCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	if !focused || laidOut == 0 {
		t.Fatalf("the focus move to Cancel Resize (focused %v) laid the view out %d times, want at least once", focused, laidOut)
	}
	if height >= stale {
		t.Fatalf("job section height = %d, want less than the previous summary's %d", height, stale)
	}
}

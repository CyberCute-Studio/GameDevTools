//go:build windows

package imageresizer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// jobCommandBase keeps the job command identifiers clear of the other
// command ranges.
const jobCommandBase = 700

const (
	cancelResizeCommand = iota + 1
	openOutputFolderCommand
)

// jobShutdownWait bounds how long closing the application waits for a
// cancelled job to stop. Its items in flight stop at their next cancellation
// check, so the wait normally ends at once and no temporary file, or file
// moved aside for a replacement, is left; a read blocked in the operating
// system, on an unresponsive network share, cannot observe the cancellation,
// and must not hold the process — and its single-instance lock — open.
const jobShutdownWait = 10 * time.Second

// jobRunner runs a resize job as job.Run does; tests substitute it.
type jobRunner func(context.Context, job.Request, func(job.Event)) (job.Summary, error)

// activeJob is the running job, as the GUI thread tracks it.
type activeJob struct {
	// ids are the inventory IDs of the job's items, and paths their source
	// paths, by input index.
	ids   []uint64
	paths []string
	// folder is the output folder as the job plans into it.
	folder string
	cancel context.CancelFunc
	// cancelling is set once the user cancelled the job.
	cancelling bool
	// processing lists the input indexes of the items being processed, in
	// the order they started.
	processing []int
	progress   job.Progress
}

// track applies one item's event to the job's counts and to the items being
// processed.
func (a *activeJob) track(event job.Event) {
	a.progress = event.Progress
	if at := slices.Index(a.processing, event.Index); at >= 0 {
		a.processing = slices.Delete(a.processing, at, at+1)
	}
	if event.Status.State == job.StateProcessing {
		a.processing = append(a.processing, event.Index)
	}
}

// NativeAction runs the shell's primary Start Resize action (RSZ-040). It
// checks, before anything starts, that no image is still being checked and
// at least one is Ready, that a valid preset is selected, and that the JPEG
// fields and the output options are valid; under Overwrite it asks for the
// job's explicit confirmation, its safe answer the default (RSZ-025,
// UX-002). Every refusal is explained beside the job's progress, and a field
// that is invalid also beside the field (UX-003). The job then runs off the
// GUI thread (RSZ-041, TECH-005), and its progress and each image's result
// come back through the shell dispatcher.
func (n *NativeView) NativeAction() {
	if n.active != nil {
		n.showJobNotice(ui.Notice{Kind: ui.NoticeWarning, Message: ui.ResizeJobRunning})
		return
	}
	request, ids, ok := n.jobRequest()
	if !ok {
		return
	}
	n.startJob(request, ids)
}

// NativeActionAvailable reports Start Resize unavailable while a job runs,
// so the shell disables it until the job has finished.
func (n *NativeView) NativeActionAvailable() bool {
	return n.active == nil
}

// jobRequest builds the job for every listed image from the current
// options, or explains why no job can start.
func (n *NativeView) jobRequest() (job.Request, []uint64, bool) {
	refuse := func(kind ui.ErrorKind, message string) (job.Request, []uint64, bool) {
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(kind, errors.New(message))})
		return job.Request{}, nil, false
	}
	if n.dispatcher == nil {
		return refuse(ui.ErrorUnexpected, ui.ResizeJobUnavailable)
	}
	if checking := n.inventory.Checking(); checking > 0 {
		return refuse(ui.ErrorValidation, fmt.Sprintf(ui.ResizeJobChecking, checking))
	}
	if n.inventory.Ready() == 0 {
		return refuse(ui.ErrorValidation, ui.ResizeJobNoImages)
	}
	selected, exists := n.presets.SelectedPreset()
	if !exists {
		return refuse(ui.ErrorValidation, ui.ResizePresetRequired)
	}
	target := resize.Target{Width: selected.Width, Height: selected.Height, Mode: selected.ResizeMode}
	if err := target.Validate(); err != nil {
		return refuse(ui.ErrorValidation, fmt.Sprintf(ui.ResizePresetInvalid, err))
	}
	if !n.commitJobFields() {
		return job.Request{}, nil, false
	}
	options := n.options.Options()
	if err := options.Validate(); err != nil {
		return refuse(ui.ErrorValidation, err.Error())
	}
	background, err := BackgroundColor(options.Background)
	if err != nil {
		return refuse(ui.ErrorValidation, err.Error())
	}
	confirmed := false
	if options.Collision == CollisionOverwrite {
		if !n.confirm(n.parent, ui.ResizeOverwriteTitle, fmt.Sprintf(ui.ResizeOverwriteConfirm, options.OutputFolder)) {
			n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: ui.ResizeJobNotConfirmed})
			return job.Request{}, nil, false
		}
		confirmed = true
	}
	inputs, ids := n.inventory.JobInputs()
	return job.Request{
		Inputs:           inputs,
		Folder:           options.OutputFolder,
		Format:           options.Format,
		Collision:        options.Collision,
		ConfirmOverwrite: confirmed,
		Settings: job.Settings{
			Target:      target,
			JPEGQuality: options.JPEGQuality,
			Background:  background,
		},
	}, ids, true
}

// commitJobFields applies the JPEG quality and background fields' text
// before a job starts. The fields otherwise commit only when they lose
// focus, and starting a job need not move the focus, so a job must not run
// with stale or invalid text. They are disabled, and ignored, while the
// format forces PNG output. A change that could not be saved still applies
// to the job, as it stays in memory; one the options refuse stops the job.
func (n *NativeView) commitJobFields() bool {
	options := n.options.Options()
	if options.Format == FormatPNG {
		return true
	}
	quality, err := ParseJPEGQuality(n.quality.Edit.Text())
	if err != nil {
		message := ui.FormatError(ui.ErrorValidation, err)
		n.quality.Error.SetText(message)
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: message})
		return false
	}
	background, err := ParseBackground(n.background.Edit.Text())
	if err != nil {
		message := ui.FormatError(ui.ErrorValidation, err)
		n.background.Error.SetText(message)
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: message})
		return false
	}
	applied := func(err error) bool {
		if err == nil {
			return true
		}
		n.presentResult(err, "")
		return errors.Is(err, ErrPersistence)
	}
	if quality != options.JPEGQuality && !applied(n.options.SetJPEGQuality(quality)) {
		return false
	}
	n.quality.Error.SetText("")
	if background != options.Background && !applied(n.options.SetBackground(background)) {
		return false
	}
	n.background.Error.SetText("")
	n.background.Edit.SetText(background)
	return true
}

// startJob shows every listed image Pending, moves the keyboard focus to
// Cancel Resize — Start Resize, which had it, is disabled while the job runs
// — and runs the job on its own goroutine. The source list cannot change
// while the job runs, as its rows are the job's items; selecting an image
// still shows its state and result (AC-A04).
func (n *NativeView) startJob(request job.Request, ids []uint64) {
	ctx, cancel := context.WithCancel(context.Background())
	paths := make([]string, len(request.Inputs))
	for index, input := range request.Inputs {
		paths[index] = input.Path
	}
	active := &activeJob{
		ids:      ids,
		paths:    paths,
		folder:   filepath.Clean(request.Folder),
		cancel:   cancel,
		progress: job.Progress{Total: len(ids), Pending: len(ids)},
	}
	n.active = active
	n.inventory.ClearResults()
	for _, id := range ids {
		n.inventory.SetResult(id, job.Status{State: job.StatePending})
	}
	n.refreshRows()
	n.cancelButton.Enable(true)
	n.cancelButton.Focus()
	n.outputFolder = ""
	n.openButton.Enable(false)
	n.refreshSourceSelection()
	n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: fmt.Sprintf(ui.ResizeJobStarting, len(ids))})
	done := make(chan struct{})
	n.jobDone = done
	go n.runJob(ctx, request, done)
}

// runJob runs the job on its own goroutine and hands its outcome to the GUI
// thread after its last event. A panic that escapes the job is reported as
// its failure rather than ending the process (NFR-021). done is closed once
// the job has returned, for NativeShutdown.
func (n *NativeView) runJob(ctx context.Context, request job.Request, done chan<- struct{}) {
	defer close(done)
	var finish jobFinish
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				finish = jobFinish{err: fmt.Errorf("%w: %v", job.ErrUnexpected, recovered)}
			}
		}()
		finish.summary, finish.err = n.run(ctx, request, n.deliverJobEvent)
	}()
	if n.jobEvents.end(finish) {
		n.postJobEvents()
	}
}

// deliverJobEvent runs on the job's goroutine for every change of an item's
// state, and only hands the event on, as job.Run requires: the GUI thread
// applies it.
func (n *NativeView) deliverJobEvent(event job.Event) {
	if n.jobEvents.put(event) {
		n.postJobEvents()
	}
}

// postJobEvents posts the drain task for the job's queued events. A refused
// post is retried after a pause, as postResults retries one, since the job
// may report nothing more that would post again, and its last events and
// outcome would otherwise never reach the view; once the view is destroyed
// a post can only fail, so the retries stop.
func (n *NativeView) postJobEvents() {
	if err := n.dispatcher.Post(n.applyJobEvents); err == nil || n.closed.Load() {
		return
	}
	time.AfterFunc(postRetryDelay, n.postJobEvents)
}

// applyJobEvents applies the job's queued events on the GUI thread: each
// changed item's row shows its new state (RSZ-043), rewritten in place so
// the user's selection and scroll position survive; the detail follows the
// selected item; the progress shows the completed and total counts and the
// file being processed (RSZ-042); and Open Output Folder is offered once the
// output folder holds a result, as hasResults reports (RSZ-048). The job's
// outcome, which follows its last event, finishes it.
func (n *NativeView) applyJobEvents() {
	events, finish := n.jobEvents.take()
	active := n.active
	if active == nil {
		return
	}
	selection := n.list.SelectedIndices()
	refreshDetail := false
	for _, event := range events {
		if event.Index < 0 || event.Index >= len(active.ids) {
			continue
		}
		active.track(event)
		index, ok := n.inventory.SetResult(active.ids[event.Index], event.Status)
		if !ok {
			continue
		}
		n.list.SetItemText(index, n.inventory.Item(index).RowText())
		if len(selection) > 0 && selection[0] == index {
			refreshDetail = true
		}
	}
	if refreshDetail {
		n.refreshSourceSelection()
	}
	if hasResults(active.progress) && n.outputFolder == "" {
		n.outputFolder = active.folder
		n.openButton.Enable(true)
	}
	if finish != nil {
		n.finishJob(active, *finish)
		return
	}
	n.showJobProgress(active)
}

// finishJob ends the job on the GUI thread. A job that failed before any
// item was processed shows why, and every row its content check again; a
// finished or cancelled one shows every item's final state and the summary
// of their counts (RSZ-046), and offers Open Output Folder when the output
// folder holds a result, without opening it (RSZ-047). The keyboard focus
// leaves Cancel Resize before it is disabled, for Open Output Folder when it
// is offered and the source list otherwise.
func (n *NativeView) finishJob(active *activeJob, finish jobFinish) {
	n.active = nil
	active.cancel()
	if finish.err != nil {
		n.inventory.ClearResults()
		n.refreshRows()
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: jobFailureMessage(finish.err)})
	} else {
		for index, status := range finish.summary.Items {
			if index < len(active.ids) {
				n.inventory.SetResult(active.ids[index], status)
			}
		}
		n.refreshRows()
		if hasResults(finish.summary.Progress) && finish.summary.Folder != "" {
			n.outputFolder = finish.summary.Folder
		}
		n.showJobNotice(summaryNotice(finish.summary, active.cancelling))
	}
	n.openButton.Enable(n.outputFolder != "")
	if n.cancelButton.HasFocus() {
		if n.outputFolder != "" {
			n.openButton.Focus()
		} else {
			n.list.Focus()
		}
	}
	n.cancelButton.Enable(false)
	n.refreshSourceSelection()
}

// cancelJob cancels the running job (RSZ-045): no further item starts, the
// items in flight stop at their next safe point, completed results are kept,
// and the job reports the rest Cancelled once nothing is in flight. The
// keyboard focus leaves Cancel Resize for the source list before the button
// is disabled.
func (n *NativeView) cancelJob() {
	active := n.active
	if active == nil || active.cancelling {
		return
	}
	active.cancelling = true
	active.cancel()
	if n.cancelButton.HasFocus() {
		n.list.Focus()
	}
	n.cancelButton.Enable(false)
	n.showJobProgress(active)
}

// showJobProgress shows the running job's progress, naming the file that
// most recently started processing.
func (n *NativeView) showJobProgress(active *activeJob) {
	current, others := "", 0
	if count := len(active.processing); count > 0 {
		current = filepath.Base(active.paths[active.processing[count-1]])
		others = count - 1
	}
	n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: progressMessage(active.progress, current, others, active.cancelling)})
}

func (n *NativeView) showJobNotice(notice ui.Notice) {
	_ = n.jobStatus.Show(notice, 0)
}

// openOutputFolder opens the folder the latest job saved its results in,
// only on the user's request (RSZ-047, RSZ-048).
func (n *NativeView) openOutputFolder() {
	if n.outputFolder == "" {
		return
	}
	if err := n.openFolder(n.outputFolder); err != nil {
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.ResizeOpenFolderFailed, err)})
	}
}

// refreshRows rewrites every row in place from the inventory, keeping the
// user's selection and scroll position.
func (n *NativeView) refreshRows() {
	for index := 0; index < n.inventory.Len(); index++ {
		n.list.SetItemText(index, n.inventory.Item(index).RowText())
	}
}

// hasResults reports whether a job has results in its output folder to
// show: an item saved its result there, or was skipped because a file there
// already has its name.
func hasResults(progress job.Progress) bool {
	return progress.Completed+progress.Skipped > 0
}

// jobCommand handles the job section's buttons.
func (n *NativeView) jobCommand(id int32, notification uint16) bool {
	switch id - jobCommandBase {
	case cancelResizeCommand:
		if notification == 0 {
			n.cancelJob()
		}
		return true
	case openOutputFolderCommand:
		if notification == 0 {
			n.openOutputFolder()
		}
		return true
	}
	return false
}

// NativeShutdown waits, within jobShutdownWait, for a job NativeDestroy
// cancelled to stop. The shell calls it only after the message loop has
// ended and the window is destroyed, so the job's goroutine never waits on
// the GUI thread here; its final dispatcher post fails harmlessly. Content
// checks are not waited for: a check only reads its source, so nothing is
// lost when the process exits around one.
func (n *NativeView) NativeShutdown() {
	if n.jobDone == nil {
		return
	}
	select {
	case <-n.jobDone:
	case <-time.After(jobShutdownWait):
	}
}

// createJobControls creates the job section: its progress or result, and
// the Cancel Resize and Open Output Folder buttons, both disabled until they
// apply.
func (n *NativeView) createJobControls(parent windows.Handle) error {
	var err error
	if n.jobStatus, err = ui.NewStatusArea(parent); err != nil {
		return err
	}
	if n.cancelButton, err = ui.NewButton(parent, ui.CancelResizeAction, ui.ButtonOptions{ID: jobCommandBase + cancelResizeCommand, Group: true, Disabled: true}); err != nil {
		return err
	}
	if n.openButton, err = ui.NewButton(parent, ui.OpenOutputFolderAction, ui.ButtonOptions{ID: jobCommandBase + openOutputFolderCommand, Disabled: true}); err != nil {
		return err
	}
	return nil
}

// jobSectionHeight mirrors layoutJobSection.
func (n *NativeView) jobSectionHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	height := int32(0)
	if text := n.jobStatus.Text(); text != "" {
		height += max(scale(20), measure(text, width)) + scale(8)
	}
	if stackedOptionsLayout(width, scale) {
		return height + 2*scale(36) + scale(8)
	}
	return height + scale(36)
}

// layoutJobSection places the job's progress or result, when there is one,
// above its buttons, and returns the section's bottom. The buttons line up
// with the source list's first two buttons below them.
func (n *NativeView) layoutJobSection(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	statusHeight := int32(0)
	if text := n.jobStatus.Text(); text != "" {
		statusHeight = max(scale(20), measure(text, width))
	}
	n.jobStatus.Move(x, y, width, statusHeight)
	if statusHeight > 0 {
		y += statusHeight + scale(8)
	}
	if stackedOptionsLayout(width, scale) {
		n.cancelButton.Move(x, y, width, scale(36))
		n.openButton.Move(x, y+scale(36)+scale(8), width, scale(36))
		return y + 2*scale(36) + scale(8)
	}
	column := max(int32(1), width/3)
	n.cancelButton.Move(x, y, column, scale(36))
	n.openButton.Move(x+column, y, column, scale(36))
	return y + scale(36)
}

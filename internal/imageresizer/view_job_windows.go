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

// jobShutdownWait bounds how long closing the application waits for the
// cancelled jobs to stop. Their items in flight stop at their next
// cancellation check, so the wait normally ends at once and no temporary
// file, or file moved aside for a replacement, is left; a read blocked in the
// operating system, on an unresponsive network share, cannot observe the
// cancellation, and must not hold the process — and its single-instance
// lock — open.
const jobShutdownWait = 10 * time.Second

// jobReleaseDelay bounds how long a cancelled job keeps the view waiting for
// it to stop. Its items in flight stop at their next cancellation check, so
// a cancelled job normally ends at once; a read or write blocked in the
// operating system — on an unresponsive network share or an online-only
// file — cannot observe the cancellation, so once the delay has passed the
// view lets the job go, as the inspector detaches a stuck check, rather than
// keeping Start Resize and the source list locked until the call returns.
const jobReleaseDelay = 5 * time.Second

// jobRunner runs a resize job as job.Run does; tests substitute it.
type jobRunner func(context.Context, job.Request, func(job.Event)) (job.Summary, error)

// jobStart is a job ready to start: its request, the inventory IDs of its
// items by input index, and the indexes of the inputs it checks again first
// (Inventory.Recheckable).
type jobStart struct {
	request  job.Request
	ids      []uint64
	rechecks []int
}

// activeJob is a job the view started, as the GUI thread tracks it, but for
// events and done, which the job's goroutine uses too, and ids and rechecks,
// which it reads and nothing changes.
type activeJob struct {
	ids      []uint64
	rechecks []int
	// longest is the longest file name among the job's inputs, which the job
	// section keeps room for, and reserved the room it keeps, as
	// jobStatusHeight describes.
	longest  string
	reserved jobReserve
	// folder is the output folder as the job plans into it.
	folder string
	cancel context.CancelFunc
	// cancelling is set once the user cancelled the job.
	cancelling bool
	// reported is set once the job reported an event: it started, so an
	// error that ends it, a contained panic, leaves what it reported.
	reported bool
	// processing lists the items being processed, in the order they started.
	processing []inFlight
	progress   job.Progress
	// events carries the job's events and outcome to the GUI thread. Each job
	// has its own, so a job the view let go never reaches the next job's
	// items.
	events jobMailbox
	// done is closed once the job's goroutine has returned.
	done chan struct{}
}

// inFlight is an item being processed: its input index and source path.
type inFlight struct {
	index  int
	source string
}

// jobReserve is the height the job section keeps for a running job's
// progress, and the content width and the scale, as the scaled 20, it was
// measured at.
type jobReserve struct {
	width, unit, height int32
}

// track applies one item's event to the job's counts and to the items being
// processed.
func (a *activeJob) track(event job.Event) {
	a.reported = true
	a.progress = event.Progress
	a.processing = slices.DeleteFunc(a.processing, func(item inFlight) bool { return item.index == event.Index })
	if event.Status.State == job.StateProcessing {
		a.processing = append(a.processing, inFlight{index: event.Index, source: event.Source})
	}
}

// NativeAction runs the shell's primary Start Resize action (RSZ-040). It
// checks, before anything starts, that no image is still being checked and
// at least one is Ready or is to be checked again, that a valid preset is
// selected, and that the JPEG fields and the output options are valid; under
// Overwrite it asks for the job's explicit confirmation, its safe answer the
// default (RSZ-025, UX-002). Every refusal is explained beside the job's
// progress, and a field that is invalid also beside the field (UX-003). The
// job then runs off the GUI thread (RSZ-041, TECH-005), and its progress and
// each image's result come back through the shell dispatcher.
func (n *NativeView) NativeAction() {
	if n.active != nil {
		n.showJobNotice(ui.Notice{Kind: ui.NoticeWarning, Message: ui.ResizeJobRunning})
		return
	}
	start, ok := n.jobRequest()
	if !ok {
		return
	}
	n.startJob(start)
}

// NativeActionAvailable reports Start Resize unavailable while a job runs,
// so the shell disables it until the job has finished.
func (n *NativeView) NativeActionAvailable() bool {
	return n.active == nil
}

// jobRequest builds the job for every listed image from the current
// options, or explains why no job can start. An image whose content check
// failed does not count as Ready, but the job checks it again before it
// starts (RSZ-049), so a list of nothing else still starts one, which
// reports, when none is usable after all, that no image is ready.
func (n *NativeView) jobRequest() (jobStart, bool) {
	refuse := func(kind ui.ErrorKind, message string) (jobStart, bool) {
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(kind, errors.New(message))})
		return jobStart{}, false
	}
	if n.dispatcher == nil {
		return refuse(ui.ErrorUnexpected, ui.ResizeJobUnavailable)
	}
	if checking := n.inventory.Checking(); checking > 0 {
		return refuse(ui.ErrorValidation, fmt.Sprintf(ui.ResizeJobChecking, checking))
	}
	rechecks := n.inventory.Recheckable()
	if n.inventory.Ready() == 0 && len(rechecks) == 0 {
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
		return jobStart{}, false
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
			return jobStart{}, false
		}
		confirmed = true
	}
	inputs, ids := n.inventory.JobInputs()
	return jobStart{
		request: job.Request{
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
		},
		ids:      ids,
		rechecks: rechecks,
	}, true
}

// commitJobFields applies the JPEG quality and background fields' text
// before a job starts. The fields otherwise commit only when they lose
// focus, and starting a job need not move the focus, so a job must not run
// with stale or invalid text. They are disabled, and ignored, while the
// format forces PNG output. A change that could not be saved still applies
// to the job, as it stays in memory, and the failed save is reported where
// every options save is; one the options refuse — they are invalid as a
// whole, as while no output folder is selected — stops the job, and the
// refusal is explained beside the job's progress, where the user looks after
// Start Resize.
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
		switch {
		case err == nil:
			return true
		case errors.Is(err, ErrPersistence):
			n.presentResult(err, "")
			return true
		}
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)})
		return false
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
// still shows its state and result (AC-A04). Until the job reports its
// first event, the job section says that it is starting, and, when it
// checks images again first, that no image starts until those checks are
// done, as a check of a source on a drive that is not responding takes as
// long as the drive does.
func (n *NativeView) startJob(start jobStart) {
	ctx, cancel := context.WithCancel(context.Background())
	active := &activeJob{
		ids:      start.ids,
		rechecks: start.rechecks,
		folder:   filepath.Clean(start.request.Folder),
		cancel:   cancel,
		progress: job.Progress{Total: len(start.ids), Pending: len(start.ids)},
		done:     make(chan struct{}),
	}
	for _, input := range start.request.Inputs {
		if name := filepath.Base(input.Path); len(name) > len(active.longest) {
			active.longest = name
		}
	}
	n.active = active
	n.inventory.ClearResults()
	for _, id := range start.ids {
		n.inventory.SetResult(id, job.Status{State: job.StatePending})
	}
	n.refreshRows()
	n.cancelButton.Enable(true)
	n.cancelButton.Focus()
	n.outputFolder = ""
	n.openButton.Enable(false)
	n.refreshSourceSelection()
	starting := fmt.Sprintf(ui.ResizeJobStarting, len(start.ids))
	if count := len(start.rechecks); count > 0 {
		starting += fmt.Sprintf(ui.ResizeJobRechecking, count)
	}
	n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: starting})
	n.jobs.Add(1)
	go n.runJob(ctx, active, start.request)
}

// runJob runs the job on its own goroutine — first checking again the
// inputs whose content check had failed, then the job itself — and hands its
// outcome to the GUI thread after its last event. A job cancelled while it
// checked them again reports the inputs it had not checked yet Cancelled
// (cancelUnchecked). A panic that escapes the job is reported as its failure
// rather than ending the process (NFR-021).
func (n *NativeView) runJob(ctx context.Context, active *activeJob, request job.Request) {
	defer n.jobs.Done()
	defer close(active.done)
	var finish jobFinish
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				finish.summary, finish.err = job.Summary{}, fmt.Errorf("%w: %v", job.ErrUnexpected, recovered)
			}
		}()
		checked := n.recheck(ctx, active, request.Inputs)
		finish.summary, finish.err = n.run(ctx, request, func(event job.Event) {
			// job.Run calls this for every change of an item's state, and it
			// only hands the event on, as Run requires: the GUI thread applies
			// it.
			if active.events.put(event) {
				n.postJobEvents(active)
			}
		})
		if finish.err == nil {
			cancelUnchecked(&finish.summary, active.rechecks[checked:])
		}
	}()
	if active.events.end(finish) {
		n.postJobEvents(active)
	}
}

// recheck checks again, on the job's goroutine and before the job starts,
// the content of the inputs whose check had failed, as Recheckable lists
// them, so the job takes each as it now is (RSZ-049): one that has since
// become readable, or was replaced by a valid image, is resized, and one
// that still cannot be used fails with its current reason. The list's
// content checks are idle meanwhile, as no image is being checked when a job
// starts and none can be added while it runs, so this decodes one source at
// a time as they do (NFR-014). Each check made goes to the job's mailbox,
// which the view applies to the list when the job ends, however it ends. It
// returns how many of the inputs it checked again, in Recheckable's order:
// once the job is cancelled the rest are not checked, and the job reports
// them Cancelled.
func (n *NativeView) recheck(ctx context.Context, active *activeJob, inputs []job.Input) int {
	for checked, index := range active.rechecks {
		input := &inputs[index]
		info, err := n.inspector.check(ctx, input.Path)
		if ctx.Err() != nil {
			return checked
		}
		result := InspectionResult{ID: active.ids[index], Info: info, Err: err}
		active.events.recheck(result)
		*input = recheckedInput(input.Path, result)
	}
	return len(active.rechecks)
}

// postJobEvents posts the drain task for the job's queued events, retried
// when refused, as postDrain describes.
func (n *NativeView) postJobEvents(active *activeJob) {
	n.postDrain(func() { n.applyJobEvents(active) }, n.destroyed)
}

// applyJobEvents applies the job's queued events on the GUI thread: each
// changed item's row shows its new state (RSZ-043), rewritten in place so
// the user's selection and scroll position survive; the detail follows the
// selected item; the progress shows the completed and total counts and the
// file being processed (RSZ-042); and Open Output Folder is offered once the
// output folder holds a result, as hasResults reports (RSZ-048). The job's
// outcome, which follows its last event, finishes it. The events of a job
// that is no longer the running one — the view let it go — are dropped:
// they no longer describe the listed items.
func (n *NativeView) applyJobEvents(active *activeJob) {
	events, finish := active.events.take()
	if n.active != active {
		return
	}
	selection := n.list.SelectedIndices()
	refreshDetail := false
	for _, event := range events {
		if event.Index < 0 || event.Index >= len(active.ids) {
			continue
		}
		active.track(event)
		index, ok := n.setResult(active.ids[event.Index], event.Status)
		if ok && len(selection) > 0 && selection[0] == index {
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

// finishJob ends the job on the GUI thread, after applying the content
// checks it made again to the source list — those it made before it stopped
// responding, for a job the view let go. A job that failed before it
// reported anything shows why, and every row its content check again; one
// that stopped unexpectedly after it started keeps what it reported, and its
// items that had not ended are Failed with its error; a finished or
// cancelled one shows every item's final state and the summary of their
// counts (RSZ-046), and says so when the view let it go. Open Output Folder
// is offered when the output folder holds a result, without opening it
// (RSZ-047). Only rows whose text changed are rewritten, so a row the job's
// last events already show is left alone. A notice that the source list
// cannot change while the job runs no longer holds, and is cleared while the
// status area still shows it. The keyboard focus leaves Cancel Resize before
// it is disabled, for Open Output Folder when it is offered and the source
// list otherwise.
func (n *NativeView) finishJob(active *activeJob, finish jobFinish) {
	n.active = nil
	active.cancel()
	for _, result := range active.events.takeRechecked() {
		n.inventory.Recheck(result)
	}
	if n.busyNotice != 0 && n.status.Revision() == n.busyNotice {
		n.status.Clear()
	}
	switch {
	case finish.err != nil && !active.reported:
		n.inventory.ClearResults()
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: jobFailureMessage(finish.err)})
	case finish.err != nil:
		for _, id := range active.ids {
			if index, ok := n.inventory.IndexOf(id); ok {
				if last := n.inventory.Item(index).Result; !last.State.Final() {
					n.inventory.SetResult(id, job.Status{State: job.StateFailed, Destination: last.Destination, Err: finish.err})
				}
			}
		}
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(errorKind(finish.err), fmt.Errorf(ui.ResizeJobInterrupted, finish.err))})
	default:
		for index, status := range finish.summary.Items {
			if index < len(active.ids) {
				n.inventory.SetResult(active.ids[index], status)
			}
		}
		if hasResults(finish.summary.Progress) && finish.summary.Folder != "" {
			n.outputFolder = finish.summary.Folder
		}
		notice := summaryNotice(finish.summary)
		if finish.released {
			notice.Message += ui.ResizeJobReleased
		}
		n.showJobNotice(notice)
	}
	n.refreshRows()
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
// and the job reports the rest Cancelled once nothing is in flight; should
// it not stop within the release delay, the view lets it go (releaseJob).
// The keyboard focus leaves Cancel Resize for the source list before the
// button is disabled.
func (n *NativeView) cancelJob() {
	active := n.active
	if active == nil || active.cancelling {
		return
	}
	active.cancelling = true
	active.cancel()
	go n.awaitCancelled(active)
	if n.cancelButton.HasFocus() {
		n.list.Focus()
	}
	n.cancelButton.Enable(false)
	n.showJobProgress(active)
}

// awaitCancelled waits, on its own goroutine, for a cancelled job to stop,
// and once the release delay has passed without it stopping asks the GUI
// thread to let it go.
func (n *NativeView) awaitCancelled(active *activeJob) {
	timer := time.NewTimer(n.releaseDelay)
	defer timer.Stop()
	select {
	case <-active.done:
	case <-timer.C:
		n.postDrain(func() { n.releaseJob(active) }, n.destroyed)
	}
}

// releaseJob lets go of a cancelled job that has not stopped within the
// release delay, unless it finished meanwhile, so a read or write blocked in
// the operating system cannot keep Start Resize and the source list locked
// (RSZ-045, AC-A04). The events the job reported are applied first; every
// item that had not ended is then Cancelled — one still in flight with
// errReleased, whose detail says that a read or write of it was not
// responding — and the job ends as a cancelled one does, its summary saying
// that it did not stop. Its goroutine goes on until the blocked call returns
// and stops at its next cancellation check, before its result takes a name,
// unless the call was that rename; what it reports then is dropped, as
// applyJobEvents drops any job's but the running one's, and NativeShutdown
// still waits for it within its bound.
func (n *NativeView) releaseJob(active *activeJob) {
	n.applyJobEvents(active)
	if n.active != active {
		return
	}
	summary := job.Summary{Folder: active.folder, Items: make([]job.Status, len(active.ids)), Progress: active.progress}
	for index, id := range active.ids {
		status := job.Status{State: job.StateCancelled}
		if at, ok := n.inventory.IndexOf(id); ok {
			switch last := n.inventory.Item(at).Result; {
			case last.State.Final():
				status = last
			case last.State == job.StateProcessing:
				status = job.Status{State: job.StateCancelled, Destination: last.Destination, Err: errReleased}
			}
		}
		summary.Items[index] = status
	}
	summary.Progress.Cancelled += summary.Progress.Pending + summary.Progress.Processing
	summary.Progress.Pending, summary.Progress.Processing = 0, 0
	n.finishJob(active, jobFinish{summary: summary, released: true})
}

// showJobProgress shows the running job's progress, naming the file that
// most recently started processing.
func (n *NativeView) showJobProgress(active *activeJob) {
	current, others := "", 0
	if count := len(active.processing); count > 0 {
		current = filepath.Base(active.processing[count-1].source)
		others = count - 1
	}
	n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: progressMessage(active.progress, current, others, active.cancelling)})
}

func (n *NativeView) showJobNotice(notice ui.Notice) {
	_ = n.jobStatus.Show(notice, 0)
}

// openOutputFolder opens the folder the latest job saved its results in,
// only on the user's request (RSZ-047, RSZ-048). Looking at the folder and
// handing it to the shell each block for as long as its drive does not
// respond, as a network share that went offline after the job, so both run
// on a goroutine of their own, which must not freeze the window (TECH-005);
// a failure is shown in the job section through the shell dispatcher.
func (n *NativeView) openOutputFolder() {
	folder, open := n.outputFolder, n.openFolder
	if folder == "" || n.dispatcher == nil {
		return
	}
	go func() {
		if err := open(folder); err != nil {
			n.postDrain(func() {
				n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.ResizeOpenFolderFailed, err)})
			}, n.destroyed)
		}
	}()
}

// setResult records the job's status for the item with the given ID and
// rewrites its row when its text changed, as rewriteRow does.
func (n *NativeView) setResult(id uint64, status job.Status) (int, bool) {
	index, ok := n.inventory.SetResult(id, status)
	if ok {
		n.rewriteRow(index)
	}
	return index, ok
}

// refreshRows rewrites, in place, every row whose text no longer matches
// the inventory, keeping the user's selection and scroll position.
func (n *NativeView) refreshRows() {
	for index := 0; index < n.inventory.Len(); index++ {
		n.rewriteRow(index)
	}
}

// rewriteRow rewrites the row at index in place when its text changed.
// Rewriting a row deletes and reinserts it, and the focused row's raises an
// accessibility focus event, so a row that already shows its text is left
// alone.
func (n *NativeView) rewriteRow(index int) {
	if text := n.inventory.Item(index).RowText(); n.list.ItemText(index) != text {
		n.list.SetItemText(index, text)
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

// NativeShutdown waits, within jobShutdownWait, for the jobs NativeDestroy
// cancelled, and any the view let go, to stop. The shell calls it only after
// the message loop has ended and the window is destroyed, so no job's
// goroutine waits on the GUI thread here; their final dispatcher posts fail
// harmlessly. When the wait runs out, the goroutine waiting for the jobs is
// left to the process's exit. Content checks are not waited for: a check
// only reads its source, so nothing is lost when the process exits around
// one.
func (n *NativeView) NativeShutdown() {
	stopped := make(chan struct{})
	go func() {
		n.jobs.Wait()
		close(stopped)
	}()
	timer := time.NewTimer(jobShutdownWait)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
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

// jobStatusHeight is the height of the job's progress or result, or 0 when
// there is none. While a job runs it is the height the job reserves at this
// width and scale: at first that of the longest progress the job can show —
// widestProgress for its total and longest file name, and the cancelling
// progress, measured once for each width and scale — and then also that of
// every progress the job has shown, so it never shrinks: a progress that
// wraps differently for each file does not move the source list below it
// between the user's clicks (AC-A04). A file name that renders wider than
// the longest by bytes, such as one of wide letters, can make a progress
// taller than the first reservation: the section then grows once and keeps
// that height until the job ends, rather than changing with every file.
func (n *NativeView) jobStatusHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	text := n.jobStatus.Text()
	if text == "" {
		return 0
	}
	height := max(scale(20), measure(text, width))
	active := n.active
	if active == nil {
		return height
	}
	if unit := scale(20); active.reserved.width != width || active.reserved.unit != unit {
		total := len(active.ids)
		active.reserved = jobReserve{width: width, unit: unit}
		for _, message := range []string{widestProgress(total, active.longest), fmt.Sprintf(ui.ResizeJobCancelling, total, total)} {
			active.reserved.height = max(active.reserved.height, measure(ui.Notice{Kind: ui.NoticeInfo, Message: message}.Text(), width))
		}
	}
	active.reserved.height = max(active.reserved.height, height)
	return active.reserved.height
}

// jobSectionHeight mirrors layoutJobSection.
func (n *NativeView) jobSectionHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	height := int32(0)
	if status := n.jobStatusHeight(width, scale, measure); status > 0 {
		height += status + scale(8)
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
	statusHeight := n.jobStatusHeight(width, scale, measure)
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

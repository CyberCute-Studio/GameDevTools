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
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// jobCommandBase keeps the job command identifiers clear of the other
// command ranges.
const jobCommandBase = 700

const (
	cancelResizeCommand = iota + 1
	openOutputFolderCommand
)

// JobShutdownWait bounds how long closing the application waits for the
// cancelled jobs to stop. Their items in flight stop at their next
// cancellation check, so the wait normally ends at once and no temporary
// file, or file moved aside for a replacement, is left; a read blocked in the
// operating system, on an unresponsive network share, cannot observe the
// cancellation, and must not hold the process — and its single-instance
// lock — open. A second launch waits longer than this for the closing
// instance to release that lock.
const JobShutdownWait = 10 * time.Second

// jobReleaseDelay bounds how long a cancelled job keeps the view waiting for
// it to stop. Its items in flight stop at their next cancellation check, so
// a cancelled job normally ends at once; a read or write blocked in the
// operating system — on an unresponsive network share or an online-only
// file — cannot observe the cancellation, so once the delay has passed the
// view lets the job go, as the inspector detaches a stuck check, rather than
// keeping Start Resize and the source list locked until the call returns.
// The view cannot tell such a call from a step only slow to reach its next
// check, such as flushing a large result to a slow drive, which is let go
// too, so the view says of a job it let go only that a read or write may not
// be responding.
const jobReleaseDelay = 5 * time.Second

// jobRunner runs a resize job as job.Run does; tests substitute it.
type jobRunner func(context.Context, job.Request, func(job.Event)) (job.Summary, error)

// activeJob is a job the view started, as the GUI thread tracks it, but for
// events and done, which the job's goroutine uses too. ids and rechecks are
// its jobStart's, which the job's goroutine reads through the jobStart it
// runs (recheckInputs) and nothing changes.
type activeJob struct {
	ids      []uint64
	rechecks []int
	// checkedAgain holds the IDs of the items whose content check the job
	// made again and the view applied to the source list (applyRechecks).
	checkedAgain map[uint64]bool
	// longest is the longest file name among the job's inputs, which the job
	// section keeps room for, and reserved the room it keeps at each content
	// width and scale it was laid out at, as jobStatusHeight describes.
	longest  string
	reserved map[jobLayout]int32
	// folder is the output folder as the job plans into it.
	folder string
	// previousFolder is the folder Open Output Folder offered when the job
	// started, which it offers again should the job save no result.
	previousFolder string
	cancel         context.CancelFunc
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

// jobLayout is a content width and a scale, as the scaled 20, the job
// section was measured at.
type jobLayout struct {
	width, unit int32
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
// progress, and a field that is invalid also beside the field (UX-003) — but
// for a Start Resize that arrives while a job runs, as the button is
// disabled then, which is refused in the status area, as refuseBusy
// describes, so the running job's progress stays. The job then runs off the
// GUI thread (RSZ-041, TECH-005), and its progress and each image's result
// come back through the shell dispatcher.
func (n *NativeView) NativeAction() {
	if n.refuseBusy(ui.ResizeJobRunning) {
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
// reports, when none is usable after all, that no image is ready. The
// Overwrite confirmation's modal loop dispatches messages — a drop forwarded
// to the window among them — so the list is checked again once it is
// answered, and the job takes the list that was checked.
func (n *NativeView) jobRequest() (jobStart, bool) {
	refuse := func(kind ui.ErrorKind, message string) (jobStart, bool) {
		n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(kind, errors.New(message))})
		return jobStart{}, false
	}
	if n.dispatcher == nil {
		return refuse(ui.ErrorUnexpected, ui.ResizeJobUnavailable)
	}
	if refusal := n.inventory.jobRefusal(); refusal != "" {
		return refuse(ui.ErrorValidation, refusal)
	}
	selected, exists := n.presets.SelectedPreset()
	if !exists {
		return refuse(ui.ErrorValidation, ui.ResizePresetRequired)
	}
	target, err := jobTarget(selected)
	if err != nil {
		return refuse(ui.ErrorValidation, fmt.Sprintf(ui.ResizePresetInvalid, err))
	}
	if !n.commitJobFields() {
		return jobStart{}, false
	}
	options := n.options.Options()
	background, err := jobBackground(options)
	if err != nil {
		n.refuseOptions(ui.FormatError(ui.ErrorValidation, err))
		return jobStart{}, false
	}
	confirmed := false
	if options.Collision == CollisionOverwrite {
		if !n.confirm(n.parent, ui.ResizeOverwriteTitle, fmt.Sprintf(ui.ResizeOverwriteConfirm, options.OutputFolder)) {
			n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: ui.ResizeJobNotConfirmed})
			return jobStart{}, false
		}
		confirmed = true
		if refusal := n.inventory.jobRefusal(); refusal != "" {
			return refuse(ui.ErrorValidation, refusal)
		}
	}
	return n.inventory.newJobStart(options, target, background, confirmed), true
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
// Start Resize. A field's refusal is also shown beside the field, quietly, as
// the job section announces the same message, which would otherwise be read
// twice in a row when the field's message was not already shown.
func (n *NativeView) commitJobFields() bool {
	options := n.options.Options()
	if options.Format == FormatPNG {
		return true
	}
	quality, err := ParseJPEGQuality(n.quality.Edit.Text())
	if err != nil {
		message := ui.FormatError(ui.ErrorValidation, err)
		n.quality.Error.SetTextQuietly(message)
		n.refuseOptions(message)
		return false
	}
	background, err := ParseBackground(n.background.Edit.Text())
	if err != nil {
		message := ui.FormatError(ui.ErrorValidation, err)
		n.background.Error.SetTextQuietly(message)
		n.refuseOptions(message)
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
		n.refuseOptions(ui.FormatError(ui.ErrorValidation, err))
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
// — and runs the job on its own goroutine, with the runner and the content
// check the view has now. The source list cannot change while the job runs,
// as its rows are the job's items; selecting an image still shows its state
// and result (AC-A04). Until the job reports its first event, the job
// section says that it is starting, and, when it checks images again first,
// that no image starts until those checks are done, as a check of a source on
// a drive that is not responding takes as long as the drive does. That
// notice is shown before the focus moves, as the button's focus notification
// lays the view out again, and the job section would otherwise keep room, for
// the whole job, for the previous job's summary.
func (n *NativeView) startJob(start jobStart) {
	ctx, cancel := context.WithCancel(context.Background())
	active := &activeJob{
		ids:            start.ids,
		rechecks:       start.rechecks,
		checkedAgain:   make(map[uint64]bool, len(start.rechecks)),
		reserved:       make(map[jobLayout]int32),
		folder:         output.CleanFolder(start.request.Folder),
		previousFolder: n.outputFolder,
		cancel:         cancel,
		progress:       job.Progress{Total: len(start.ids), Pending: len(start.ids)},
		done:           make(chan struct{}),
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
	starting := fmt.Sprintf(ui.ResizeJobStarting, len(start.ids))
	if count := len(start.rechecks); count > 0 {
		starting += fmt.Sprintf(ui.ResizeJobRechecking, count)
	}
	n.showJobNotice(ui.Notice{Kind: ui.NoticeInfo, Message: starting})
	n.cancelButton.Enable(true)
	n.cancelButton.Focus()
	n.outputFolder = ""
	n.openButton.Enable(false)
	n.showSourceSelection(false)
	n.jobs.Add(1)
	go n.runJob(ctx, active, start, n.run, n.inspector.check)
}

// runJob runs the job on its own goroutine — first checking again the
// inputs whose content check had failed, as recheckInputs describes
// (RSZ-049), then the job itself — and hands its outcome to the GUI thread
// after its last event. The list's content checks are idle while it checks
// them again, as no image is being checked when a job starts and none can be
// added while it runs, so it decodes one source at a time as they do
// (NFR-014) — but for a check of a job the view let go, which the list's
// checks of images added since may run beside. Each check made goes to the
// job's mailbox, which the view applies to the list with the job's events,
// or when the job ends, however it ends (applyRechecks). A job cancelled
// while it checked them again has planned nothing, so it does not run the
// job, which would end the inputs it had not checked yet Failed with reasons
// that no longer hold: the GUI thread makes up its outcome
// (jobFinish.unplanned). A panic that escapes the job is reported as its
// failure rather than ending the process (NFR-021).
func (n *NativeView) runJob(ctx context.Context, active *activeJob, start jobStart, run jobRunner, check inspectFunc) {
	defer n.jobs.Done()
	defer close(active.done)
	var finish jobFinish
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				finish = jobFinish{err: fmt.Errorf("%w: %v", job.ErrUnexpected, recovered)}
			}
		}()
		if !recheckInputs(ctx, start, check, active.events.recheck) {
			finish.unplanned = true
			return
		}
		finish.summary, finish.err = run(ctx, start.request, func(event job.Event) {
			// job.Run calls this for every change of an item's state, and it
			// only hands the event on, as Run requires: the GUI thread applies
			// it.
			if active.events.put(event) {
				n.postJobEvents(active)
			}
		})
	}()
	if active.events.end(finish) {
		n.postJobEvents(active)
	}
}

// postJobEvents posts the drain task for the job's queued events, retried
// when refused, as postDrain describes.
func (n *NativeView) postJobEvents(active *activeJob) {
	n.postDrain(func() { n.applyJobEvents(active) })
}

// applyJobEvents applies the job's queued events on the GUI thread, after
// the content checks it made again (applyRechecks): each changed item's row
// shows its new state (RSZ-043), rewritten in place so the user's selection
// and scroll position survive; the detail follows the selected item; the
// progress shows the completed and total counts and the file being processed
// (RSZ-042); and Open Output Folder is offered once the output folder holds
// a result, as hasResults reports (RSZ-048). The job's outcome, which
// follows its last event, finishes it. The events of a job that is no longer
// the running one — the view let it go — are dropped: they no longer
// describe the listed items.
func (n *NativeView) applyJobEvents(active *activeJob) {
	events, finish := active.events.take()
	if n.active != active {
		return
	}
	n.applyRechecks(active)
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
		n.showSourceSelection(false)
	}
	if hasResults(active.progress) && n.outputFolder == "" {
		n.outputFolder = active.folder
		n.openButton.Enable(true)
	}
	if finish != nil {
		n.finishJob(active, *finish)
		return
	}
	n.showJobProgress(active, false)
}

// applyRechecks applies to the source list the content checks the job made
// again that the view has not applied yet, and records their items. Each
// such image's row then shows what its check found — the dimensions of one
// now Ready — beside its job state while the job runs, rather than only once
// the job ends; the job's first event follows its checks, so its drain
// applies them, and finishJob applies any made since, for a job that ended
// before it reported or that the view let go.
func (n *NativeView) applyRechecks(active *activeJob) {
	for _, result := range active.events.takeRechecked() {
		if index, ok := n.inventory.Recheck(result); ok {
			n.rewriteRow(index)
		}
		active.checkedAgain[result.ID] = true
	}
}

// finishJob ends the job on the GUI thread, after applying the content
// checks it made again to the source list — those it made before it stopped
// responding, for a job the view let go. A job that failed before it
// reported anything shows why, and every row its content check again; one
// that stopped unexpectedly after it started keeps what it reported, and its
// items that had not ended are Failed with its error; a finished or
// cancelled one shows every item's final state — as cancelledSummary makes
// them up for a job cancelled while it checked images again and for one the
// view let go — and the summary of their counts (RSZ-046), and says so when
// the view let it go. Open Output Folder is offered when the
// output folder holds a result, without opening it (RSZ-047), and, for a job
// that saved none, keeps offering the folder it offered before the job. Only
// rows whose text changed are rewritten, so a row the job's last events
// already show is left alone. A notice that the source list cannot change,
// or Start Resize run, while the job runs no longer holds, and is cleared
// while the status area still shows it. The keyboard focus leaves Cancel Resize before it is
// disabled, for Open Output Folder when it is offered and the source list
// otherwise.
func (n *NativeView) finishJob(active *activeJob, finish jobFinish) {
	n.active = nil
	active.cancel()
	n.applyRechecks(active)
	if finish.unplanned || finish.released {
		unchecked := slices.DeleteFunc(slices.Clone(active.rechecks), func(index int) bool { return active.checkedAgain[active.ids[index]] })
		finish.summary = n.inventory.cancelledSummary(active.ids, unchecked, active.folder)
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
	if n.outputFolder == "" {
		n.outputFolder = active.previousFolder
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
	n.showSourceSelection(false)
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
	n.showJobProgress(active, true)
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
		n.postDrain(func() { n.releaseJob(active) })
	}
}

// releaseJob lets go of a cancelled job that has not stopped within the
// release delay, unless it finished meanwhile, so a read or write blocked in
// the operating system cannot keep Start Resize and the source list locked
// (RSZ-045, AC-A04). The events the job reported are applied first; the job
// then ends as a cancelled one does, as cancelledSummary makes up its
// outcome — an item still in flight Cancelled with errReleased, whose detail
// says that a read or write of it may not be responding — its summary saying
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
	n.finishJob(active, jobFinish{released: true})
}

// showJobProgress shows the running job's progress, naming the file that
// most recently started processing. announce is whether a screen reader
// reads it: the progress that follows each batch of the job's events changes
// faster than it could be read, so only the cancelling progress the user's
// Cancel Resize brings is announced, between the job's announced start and
// its announced summary.
func (n *NativeView) showJobProgress(active *activeJob, announce bool) {
	current, others := "", 0
	if count := len(active.processing); count > 0 {
		current = filepath.Base(active.processing[count-1].source)
		others = count - 1
	}
	notice := ui.Notice{Kind: ui.NoticeInfo, Message: progressMessage(active.progress, current, others, active.cancelling)}
	if announce {
		n.showJobNotice(notice)
		return
	}
	n.jobStatus.Update(notice)
}

func (n *NativeView) showJobNotice(notice ui.Notice) {
	_ = n.jobStatus.Show(notice, 0)
}

// refuseOptions shows Start Resize's refusal of the resize options, or of a
// JPEG field's text the options would take, in the job section, and records
// it as the options refusal (optionsRefusal), as the restore of the default
// settings replaces what it refuses.
func (n *NativeView) refuseOptions(message string) {
	n.showJobNotice(ui.Notice{Kind: ui.NoticeError, Message: message})
	n.optionsRefusal = n.jobStatus.Revision()
}

// openOutputFolder opens the folder the latest job saved its results in,
// only on the user's request (RSZ-047, RSZ-048). Looking at the folder and
// handing it to the shell each block for as long as its drive does not
// respond, as a network share that went offline after the job, so both run
// on a goroutine of their own, which must not freeze the window (TECH-005).
// A failure is shown through the shell dispatcher in the status area, as the
// view shows the Add Images picker's and the Screenshot view its own Open
// Folder's, not in the job section, whose progress replaces any notice with
// every event of a running job, and which keeps a finished job's summary.
func (n *NativeView) openOutputFolder() {
	folder, open := n.outputFolder, n.openFolder
	if folder == "" || n.dispatcher == nil {
		return
	}
	go func() {
		if err := open(folder); err != nil {
			n.postDrain(func() {
				_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.ResizeOpenFolderFailed, err)}, 0)
			})
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

// NativeShutdown waits, within JobShutdownWait, for the jobs NativeDestroy
// cancelled, and any the view let go, to stop. The shell calls it only once
// the window is destroyed, on a goroutine of its own, so no job's goroutine
// waits on the GUI thread here; their final dispatcher posts fail
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
	timer := time.NewTimer(JobShutdownWait)
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
// every progress the job has shown there, so it never shrinks: a progress
// that wraps differently for each file does not move the source list below
// it between the user's clicks (AC-A04). A file name that renders wider than
// the longest by bytes, such as one of wide letters, can make a progress
// taller than the first reservation: the section then grows once and keeps
// that height until the job ends, rather than changing with every file. The
// room is kept for each width and scale on its own, so measuring the view at
// another width — a layout that tries one — leaves the room kept at this one
// as it was.
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
	layout := jobLayout{width: width, unit: scale(20)}
	reserved, measured := active.reserved[layout]
	if !measured {
		total := len(active.ids)
		for _, message := range []string{widestProgress(total, active.longest), fmt.Sprintf(ui.ResizeJobCancelling, total, total)} {
			reserved = max(reserved, measure(ui.Notice{Kind: ui.NoticeInfo, Message: message}.Text(), width))
		}
	}
	reserved = max(reserved, height)
	active.reserved[layout] = reserved
	return reserved
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

package imageresizer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"slices"
	"sync"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// sourceError is why the source list could not use an item, handed to a
// job as the item's Input.Err. The job ends such an item Failed with it
// (AC-R06), and its message is the explanation the list already shows, so
// the item's Failed status stays as actionable as its list status was.
type sourceError struct {
	status SourceStatus
	detail string
}

func (e *sourceError) Error() string {
	if e.detail == "" {
		return ui.ResizeSourceUnavailableDetail
	}
	return e.detail
}

// unusable is why a job cannot resize the item, as job.Run ends such an
// input Failed: nil for a Ready item, job.ErrUnchecked for one still being
// checked, and the list's own reason for any other.
func (s SourceItem) unusable() error {
	switch s.Status {
	case StatusReady:
		return nil
	case StatusChecking:
		return job.ErrUnchecked
	}
	return &sourceError{status: s.Status, detail: s.Detail}
}

// errReleased marks an item that was still in flight when the view let its
// cancelled job go, as it had not reached its next cancellation check within
// the release delay — a read or write of it may not be responding. It is the
// item's Cancelled status's Err, which resultDetail explains, and names no
// file left behind.
var errReleased = errors.New("the image had not stopped when its cancelled job was let go")

// SetResult records what the running job reported for the item with the
// given ID and returns its index; ok is false when no listed item has the
// ID. The item's row and detail then show the job's state for it instead of
// its content check, until ClearResults forgets the results.
func (v *Inventory) SetResult(id uint64, status job.Status) (index int, ok bool) {
	index, ok = v.IndexOf(id)
	if !ok {
		return -1, false
	}
	v.items[index].Result, v.items[index].InJob = status, true
	return index, true
}

// ClearResults forgets every item's job result, so each row shows its
// content check again.
func (v *Inventory) ClearResults() {
	for index := range v.items {
		v.items[index].Result, v.items[index].InJob = job.Status{}, false
	}
}

// Ready reports how many items a job can resize.
func (v *Inventory) Ready() int {
	ready := 0
	for _, item := range v.items {
		if item.Status == StatusReady {
			ready++
		}
	}
	return ready
}

// JobInputs lists every item as a job input, in list order, with the item
// IDs at the same indexes, so the job's events can be matched to their items
// whatever the list does meanwhile. A Ready item carries its content check's
// description; any other carries why the job cannot resize it, as unusable
// gives it: the list's reason for an item the list could not use, so the job
// ends it Failed with that reason and keeps protecting its path (RSZ-026),
// unless the job checks it again first, as Recheckable describes, and
// job.ErrUnchecked for one still being checked, as the job reports an input
// without a check; the view starts no job while any is.
func (v *Inventory) JobInputs() ([]job.Input, []uint64) {
	inputs := make([]job.Input, len(v.items))
	ids := make([]uint64, len(v.items))
	for index, item := range v.items {
		ids[index] = item.ID
		inputs[index] = job.Input{Path: item.Path, Info: item.Info, Err: item.unusable()}
	}
	return inputs, ids
}

// Recheckable lists, by index and in list order, the items a job checks
// again before it starts, so that it takes each as it now is rather than as
// it was when it was added (RSZ-049): every item whose content check found it
// unusable, or that could not be found when it was added, and whose
// extension is supported, as the file may since have become readable, come
// back, or been replaced. An item refused for its extension keeps its status,
// which its name decides.
func (v *Inventory) Recheckable() []int {
	var indexes []int
	for index, item := range v.items {
		if item.recheckable() {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// recheckable reports whether a job checks the item again before it starts,
// as Recheckable describes.
func (s SourceItem) recheckable() bool {
	return s.Status != StatusReady && s.Status != StatusChecking && SupportedExtension(s.Path)
}

// Recheck applies a content check made again before a job, for an item
// Recheckable listed, and returns the item's index: the item is then Ready,
// or unusable for the check's reason, as Resolve would make it. ok is false
// when no listed item has the ID, or the item is being checked, which its own
// check settles.
func (v *Inventory) Recheck(result InspectionResult) (index int, ok bool) {
	index, ok = v.IndexOf(result.ID)
	if !ok || v.items[index].Status == StatusChecking {
		return -1, false
	}
	v.items[index].Inspected = true
	v.items[index].resolve(result)
	return index, true
}

// recheckedInput is the job input for an item checked again before its job:
// the content the check found, or why the item still cannot be used, as
// JobInputs gives an item the list could not use.
func recheckedInput(path string, result InspectionResult) job.Input {
	if result.Err == nil {
		return job.Input{Path: path, Info: result.Info}
	}
	status, detail := failureStatus(result.Err)
	return job.Input{Path: path, Err: &sourceError{status: status, detail: detail}}
}

// jobStart is a job ready to start: its request, the inventory IDs of its
// items by input index, and the indexes of the inputs it checks again first
// (Inventory.Recheckable).
type jobStart struct {
	request  job.Request
	ids      []uint64
	rechecks []int
}

// The checks and the request below are Start Resize's, shared by the view
// and the acceptance suite (RSZ-09), so the suite checks and builds every job
// as the user's Start Resize does; what stays in the view is what needs the
// window — committing the JPEG fields' text and confirming an Overwrite job.

// jobRefusal explains why the source list cannot start a job — an image is
// still being checked, or none is Ready or to be checked again — or is empty
// when it can. Start Resize asks it before it builds a job, and again once an
// Overwrite job is confirmed, as the confirmation's modal loop may change the
// list.
func (v *Inventory) jobRefusal() string {
	if checking := v.Checking(); checking > 0 {
		return fmt.Sprintf(ui.ResizeJobChecking, checking)
	}
	if !slices.ContainsFunc(v.items, func(item SourceItem) bool { return item.Status == StatusReady || item.recheckable() }) {
		return ui.ResizeJobNoImages
	}
	return ""
}

// jobTarget is the size and fitting mode a job resizes to under the selected
// preset, and why the preset cannot be used when it cannot.
func jobTarget(selected preset.Preset) (resize.Target, error) {
	target := resize.Target{Width: selected.Width, Height: selected.Height, Mode: selected.ResizeMode}
	return target, target.Validate()
}

// jobBackground checks the options a job starts with, once the JPEG fields'
// text is applied, and returns the background color JPEG results are
// flattened against. Start Resize refuses the job with either error.
func jobBackground(options ResizeOptions) (color.RGBA, error) {
	if err := options.Validate(); err != nil {
		return color.RGBA{}, err
	}
	return BackgroundColor(options.Background)
}

// newJobStart builds the job Start Resize starts for every listed item
// (RSZ-040), once jobRefusal, jobTarget, and jobBackground have let it start
// and an Overwrite job has been confirmed or not: each item is an input, as
// JobInputs lists them, and the items whose content check failed are checked
// again first, as Recheckable lists them.
func (v *Inventory) newJobStart(options ResizeOptions, target resize.Target, background color.RGBA, confirmed bool) jobStart {
	inputs, ids := v.JobInputs()
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
		rechecks: v.Recheckable(),
	}
}

// recheckInputs checks again, before the job starts, the content of its
// inputs whose check had failed, at the indexes start.rechecks lists, as
// Recheckable lists them, so the job takes each as it now is (RSZ-049): one
// that has since become readable, or was replaced by a valid image, is
// resized, and one that still cannot be used fails with its current reason.
// Each check made replaces its input in start.request.Inputs, which the
// caller's copy of the job shares, and is handed to report, with the item's
// ID from start.ids, for the source list to apply (Inventory.Recheck). It
// reports whether it checked every one: once ctx is cancelled no further
// check starts, and a check that returns once ctx is cancelled is neither
// reported nor applied, as the cancellation may have cut it short — even one
// that had finished first, as the two cannot be told apart — so its item
// counts as one the job did not check again.
func recheckInputs(ctx context.Context, start jobStart, check inspectFunc, report func(InspectionResult)) bool {
	for _, index := range start.rechecks {
		if ctx.Err() != nil {
			return false
		}
		input := &start.request.Inputs[index]
		info, err := check(ctx, input.Path)
		if ctx.Err() != nil {
			return false
		}
		result := InspectionResult{ID: start.ids[index], Info: info, Err: err}
		report(result)
		*input = recheckedInput(input.Path, result)
	}
	return true
}

// stateLabel is the source-list status of an item in a job state
// (RSZ-043). A value outside the defined states reads as Pending rather
// than claiming an outcome.
func stateLabel(state job.State) string {
	switch state {
	case job.StateProcessing:
		return ui.ResizeStateProcessing
	case job.StateCompleted:
		return ui.ResizeStateCompleted
	case job.StateSkipped:
		return ui.ResizeStateSkipped
	case job.StateFailed:
		return ui.ResizeStateFailed
	case job.StateCancelled:
		return ui.ResizeStateCancelled
	}
	return ui.ResizeStatePending
}

// resultDetail explains a job result below the selected item's full path
// (RSZ-046): where a result was saved, and why under a numbered name; which
// file a skip left untouched, and why; why an item failed, by the category
// of its error (UX-010) and with how to recover where one applies; and that
// a cancelled item saved nothing, or, for one still in flight when the view
// let its job go (errReleased), why its outcome is not known. A file a write
// could not remove is named whatever the item ended in.
func resultDetail(status job.Status) string {
	switch status.State {
	case job.StateProcessing:
		return fmt.Sprintf(ui.ResizeDetailProcessing, status.Destination)
	case job.StateCompleted:
		detail := fmt.Sprintf(ui.ResizeDetailCompleted, status.Destination)
		if status.Reason != output.ReasonNone {
			detail += fmt.Sprintf(ui.ResizeDetailRenamed, renamedReason(status.Reason))
		}
		return detail + leftBehindDetail(status.Err)
	case job.StateSkipped:
		return fmt.Sprintf(ui.ResizeDetailSkipped, status.Destination, skippedReason(status.Reason)) + leftBehindDetail(status.Err)
	case job.StateFailed:
		return fmt.Sprintf(ui.ResizeDetailFailed, failureText(status.Err))
	case job.StateCancelled:
		if errors.Is(status.Err, errReleased) {
			return ui.ResizeDetailReleased
		}
		return ui.ResizeDetailCancelled + leftBehindDetail(status.Err)
	}
	return ui.ResizeDetailPending
}

// leftBehindDetail names the file a write could not remove, which a
// Completed, Skipped, or Cancelled item reports as its Err, or is empty.
func leftBehindDetail(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf(ui.ResizeDetailLeftBehind, err)
}

func renamedReason(reason output.Reason) string {
	switch reason {
	case output.ReasonExists:
		return ui.ResizeRenamedExists
	case output.ReasonDuplicate:
		return ui.ResizeRenamedDuplicate
	case output.ReasonSource:
		return ui.ResizeRenamedSource
	case output.ReasonUnverified:
		return ui.ResizeRenamedUnverified
	case output.ReasonDevice:
		return ui.ResizeRenamedDevice
	}
	return ui.ResizeRenamedOther
}

func skippedReason(reason output.Reason) string {
	switch reason {
	case output.ReasonExists:
		return ui.ResizeSkippedExists
	case output.ReasonDuplicate:
		return ui.ResizeSkippedDuplicate
	case output.ReasonSource:
		return ui.ResizeSkippedSource
	case output.ReasonUnverified:
		return ui.ResizeSkippedUnverified
	case output.ReasonDevice:
		return ui.ResizeSkippedDevice
	}
	return ui.ResizeSkippedOther
}

// failureText is a failed item's error with its category (UX-010) and, for
// a failure the user can act on, how to recover.
func failureText(err error) string {
	if err == nil {
		err = job.ErrUnexpected
	}
	return ui.FormatError(errorKind(err), err) + recoveryHint(err)
}

// primaryError is the error that ended an item. A joined error lists it
// first, followed only by errors that name a file left behind, as
// job.Status.Err documents and the job's tests pin; any other error is its
// own.
func primaryError(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		if errs := joined.Unwrap(); len(errs) > 0 && errs[0] != nil {
			return errs[0]
		}
	}
	return err
}

// errorKind classifies a failed item's error, or a failed job's, so
// validation, unsupported-format, read/decode, and write/encode failures
// stay distinguishable (UX-010), by the error that ended the item, as
// primaryError finds it. A failure no step reported as its own, such as a
// contained panic, is unexpected.
func errorKind(err error) ui.ErrorKind {
	err = primaryError(err)
	var (
		source    *sourceError
		decodeErr *decode.Error
		resizeErr *resize.Error
		encodeErr *encode.Error
		outputErr *output.Error
	)
	switch {
	case errors.Is(err, job.ErrUnexpected):
		return ui.ErrorUnexpected
	case errors.As(err, &source):
		if source.status == StatusUnsupported {
			return ui.ErrorUnsupportedFormat
		}
		return ui.ErrorReadDecode
	case errors.Is(err, job.ErrUnchecked):
		return ui.ErrorReadDecode
	case errors.As(err, &decodeErr):
		if decodeErr.Kind == decode.KindUnsupported {
			return ui.ErrorUnsupportedFormat
		}
		return ui.ErrorReadDecode
	case errors.As(err, &resizeErr):
		switch resizeErr.Kind {
		case resize.KindInvalid:
			return ui.ErrorValidation
		case resize.KindTooLarge:
			return ui.ErrorReadDecode
		}
		return ui.ErrorUnexpected
	case errors.As(err, &encodeErr):
		if errors.Is(err, encode.ErrQuality) || errors.Is(err, encode.ErrBackground) || errors.Is(err, encode.ErrFormat) {
			return ui.ErrorValidation
		}
		return ui.ErrorWriteEncode
	case errors.As(err, &outputErr):
		if outputErr.Kind == output.KindValidation || outputErr.Kind == output.KindConfirmation {
			return ui.ErrorValidation
		}
		return ui.ErrorWriteEncode
	}
	return ui.ErrorUnexpected
}

// recoveryHint says how to recover from an output failure the user can act
// on, by the error that ended the item: a full drive, a denied write, a
// destination that changed after planning, or an output folder that has
// gone. A source's read failure is explained by its own error, and the hint
// never blames the output folder for it; a read-only file and a file left
// behind are named by their own errors.
func recoveryHint(err error) string {
	var outputErr *output.Error
	if !errors.As(primaryError(err), &outputErr) {
		return ""
	}
	switch outputErr.Kind {
	case output.KindWrite:
		switch {
		case errors.Is(outputErr, output.ErrDiskFull):
			return ui.ResizeHintDiskFull
		case errors.Is(outputErr, output.ErrReadOnly):
			return ""
		case errors.Is(outputErr, output.ErrLeftBehind):
			return ""
		case errors.Is(outputErr, fs.ErrPermission):
			return ui.ResizeHintPermission
		}
	case output.KindChanged:
		return ui.ResizeHintChanged
	case output.KindFolder:
		return ui.ResizeHintFolder
	}
	return ""
}

// jobFailureMessage presents a job that failed before any item was
// processed — an invalid preset or JPEG option, an output folder that
// cannot be created or used, or a request the planner refused — with its
// category (UX-010) and how to recover.
func jobFailureMessage(err error) string {
	var (
		resizeErr *resize.Error
		encodeErr *encode.Error
		outputErr *output.Error
	)
	switch {
	case errors.As(err, &resizeErr) && resizeErr.Kind == resize.KindInvalid:
		return ui.FormatError(ui.ErrorValidation, fmt.Errorf(ui.ResizePresetInvalid, err))
	case errors.As(err, &encodeErr):
		return ui.FormatError(ui.ErrorValidation, fmt.Errorf(ui.ResizeJobJPEGInvalid, err))
	case errors.As(err, &outputErr) && outputErr.Kind == output.KindFolder:
		return ui.FormatError(ui.ErrorWriteEncode, fmt.Errorf(ui.ResizeJobFolderFailed, err))
	case errors.Is(err, output.ErrNoSources):
		return ui.FormatError(ui.ErrorValidation, errors.New(ui.ResizeJobNoImages))
	}
	return ui.FormatError(errorKind(err), fmt.Errorf(ui.ResizeJobFailed, err))
}

// summaryNotice summarizes a finished job (RSZ-046): the Completed, Skipped,
// Failed, and Cancelled counts, whether it was cancelled, and how many items
// left a file that could not be removed, as leftFile tells; per-file details
// are in the list. Only a cancellation ends items Cancelled, so the counts
// say whether the job was cancelled: a Cancel Resize that came once every
// item had ended changed nothing, and the job reads as finished. A job in
// which every item failed is an error, one in which any failed or was
// cancelled a warning, and any other a success.
func summaryNotice(summary job.Summary) ui.Notice {
	progress := summary.Progress
	format := ui.ResizeJobFinished
	if progress.Cancelled > 0 {
		format = ui.ResizeJobCancelled
	}
	message := fmt.Sprintf(format, progress.Completed, progress.Skipped, progress.Failed, progress.Cancelled, progress.Total)
	left := 0
	for _, status := range summary.Items {
		if leftFile(status) {
			left++
		}
	}
	if left > 0 {
		message += fmt.Sprintf(ui.ResizeJobLeftBehind, left)
	}
	kind := ui.NoticeSuccess
	switch {
	case progress.Failed > 0 && progress.Failed == progress.Total:
		kind = ui.NoticeError
	case progress.Failed > 0 || progress.Cancelled > 0 || left > 0:
		kind = ui.NoticeWarning
	}
	return ui.Notice{Kind: kind, Message: message}
}

// leftFile reports whether an item left a file that could not be removed, as
// resultDetail names it. An item of a final state other than Failed carries
// an error only when it did, as job.Status.Err documents — a temporary file,
// or, for a Completed item, the file its result replaced, moved aside, that
// could not then be deleted, which output.Write does not mark
// output.ErrLeftBehind — but for errReleased, which names none; a Failed
// item's error names one when it wraps output.ErrLeftBehind.
func leftFile(status job.Status) bool {
	switch {
	case status.Err == nil || errors.Is(status.Err, errReleased):
		return false
	case status.State == job.StateFailed:
		return errors.Is(status.Err, output.ErrLeftBehind)
	}
	return status.State.Final()
}

// cancelledSummary is the summary of a cancelled job that ended without an
// outcome of job.Run's — one cancelled while it checked its inputs again,
// which then planned and processed nothing, or one the view let go — for its
// items' IDs by input index, as the inventory shows them once the content
// checks the job made again are applied. It ends each item as job.Run ends
// it for a cancelled job: an item keeps the final state the job reported for
// it, and one still in flight is Cancelled with errReleased; one the job
// cannot resize ends Failed with why, as JobInputs hands it on, but for an
// input listed in unchecked — the job was to check it again (Recheckable)
// and did not, so its earlier reason no longer says whether it can be used;
// and every other item is Cancelled (RSZ-045).
func (v *Inventory) cancelledSummary(ids []uint64, unchecked []int, folder string) job.Summary {
	stale := make(map[int]bool, len(unchecked))
	for _, index := range unchecked {
		stale[index] = true
	}
	summary := job.Summary{Folder: folder, Items: make([]job.Status, len(ids)), Progress: job.Progress{Total: len(ids)}}
	for index, id := range ids {
		status := job.Status{State: job.StateCancelled}
		if at, ok := v.IndexOf(id); ok {
			switch item := v.items[at]; {
			case item.InJob && item.Result.State.Final():
				status = item.Result
			case item.InJob && item.Result.State == job.StateProcessing:
				status.Destination, status.Err = item.Result.Destination, errReleased
			case item.Status != StatusReady && !stale[index]:
				status = job.Status{State: job.StateFailed, Err: item.unusable()}
			}
		}
		summary.Items[index] = status
		switch status.State {
		case job.StateCompleted:
			summary.Progress.Completed++
		case job.StateSkipped:
			summary.Progress.Skipped++
		case job.StateFailed:
			summary.Progress.Failed++
		default:
			summary.Progress.Cancelled++
		}
	}
	return summary
}

// progressMessage is the running job's progress (RSZ-042): how many of its
// items are done, by outcome, and the file being processed, named by the
// item that most recently started, with how many others are in flight.
func progressMessage(progress job.Progress, current string, others int, cancelling bool) string {
	if cancelling {
		return fmt.Sprintf(ui.ResizeJobCancelling, progress.Done(), progress.Total)
	}
	message := fmt.Sprintf(ui.ResizeJobProgress, progress.Done(), progress.Total, progress.Completed, progress.Skipped, progress.Failed)
	switch {
	case current == "":
	case others > 0:
		message += fmt.Sprintf(ui.ResizeJobProcessingMore, current, others)
	default:
		message += fmt.Sprintf(ui.ResizeJobProcessing, current)
	}
	return message
}

// widestProgress is the longest progress a running job of total items can
// show, as progressMessage words it, with every count at the total and its
// longest file name in flight beside total others, so the job section can
// keep room for any of them (AC-A04).
func widestProgress(total int, longest string) string {
	message := fmt.Sprintf(ui.ResizeJobProgress, total, total, total, total, total)
	if longest != "" {
		message += fmt.Sprintf(ui.ResizeJobProcessingMore, longest, total)
	}
	return message
}

// jobFinish is a job's outcome: what job.Run returned, or which job's
// summary the view makes up itself, as cancelledSummary describes. unplanned
// marks a job cancelled while it checked its inputs again, which then ran no
// job.Run, and released a cancelled job the view let go, as releaseJob
// describes.
type jobFinish struct {
	summary   job.Summary
	err       error
	unplanned bool
	released  bool
}

// jobMailbox hands a running job's events, and then its outcome, from the
// job's goroutine to the GUI thread in batches, posting its drain tasks as
// drainSlot describes, so however quickly items change state, the job adds
// at most one message to the GUI thread's queue and can never fill it. It
// keeps only each item's latest event, as every event carries the counts of
// every state, so a drain applies the latest state of each item that changed
// and the latest counts. The outcome is taken with the events that precede
// it, so it is applied after them. It also keeps the content checks the job
// makes again before it starts, which the view applies to the source list
// when the job ends, however it ends. It is safe for concurrent use.
type jobMailbox struct {
	mu        sync.Mutex
	latest    map[int]mailedEvent
	next      uint64
	finish    *jobFinish
	rechecked []InspectionResult
	slot      drainSlot
}

// recheck keeps a content check the job made again. It posts no drain task,
// as the view takes the checks only once the job ends (takeRechecked).
func (m *jobMailbox) recheck(result InspectionResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rechecked = append(m.rechecked, result)
}

// takeRechecked removes and returns the content checks the job made again so
// far, in the order it made them: every one when the job reported its
// outcome, and those made before it stopped responding when the view lets
// it go.
func (m *jobMailbox) takeRechecked() []InspectionResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	rechecked := m.rechecked
	m.rechecked = nil
	return rechecked
}

// mailedEvent is an item's latest event, numbered in arrival order.
type mailedEvent struct {
	seq   uint64
	event job.Event
}

// put queues an event and reports whether the caller must post a drain
// task: true only when none is outstanding.
func (m *jobMailbox) put(event job.Event) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil {
		m.latest = make(map[int]mailedEvent)
	}
	m.next++
	m.latest[event.Index] = mailedEvent{seq: m.next, event: event}
	return m.slot.claim()
}

// end queues the job's outcome, after every event it reported, and reports
// whether the caller must post a drain task.
func (m *jobMailbox) end(finish jobFinish) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finish = &finish
	return m.slot.claim()
}

// take removes and returns the queued events, each item's latest in the
// order they arrived, and the outcome once the job reported it; the next put
// or end posts a new drain task.
func (m *jobMailbox) take() ([]job.Event, *jobFinish) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]mailedEvent, 0, len(m.latest))
	for _, entry := range m.latest {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b mailedEvent) int { return cmp.Compare(a.seq, b.seq) })
	events := make([]job.Event, len(entries))
	for index, entry := range entries {
		events[index] = entry.event
	}
	finish := m.finish
	m.latest, m.finish = nil, nil
	m.slot.release()
	return events, finish
}

package imageresizer

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
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

// errReleased marks an item that was still in flight when the view let its
// cancelled job go, because a read or write of it was not responding and so
// could not observe the cancellation. It is the item's Cancelled status's
// Err, which resultDetail explains, and names no file left behind.
var errReleased = errors.New("a read or write of the image was not responding when the job was let go")

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
// description; an item the list could not use carries why, so the job ends
// it Failed with that reason and keeps protecting its path (RSZ-026), unless
// the job checks it again first, as Recheckable describes. An item still
// being checked carries neither, which the job reports as job.ErrUnchecked;
// the view starts no job while any is.
func (v *Inventory) JobInputs() ([]job.Input, []uint64) {
	inputs := make([]job.Input, len(v.items))
	ids := make([]uint64, len(v.items))
	for index, item := range v.items {
		ids[index] = item.ID
		inputs[index] = job.Input{Path: item.Path}
		switch item.Status {
		case StatusReady:
			inputs[index].Info = item.Info
		case StatusChecking:
		default:
			inputs[index].Err = &sourceError{status: item.Status, detail: item.Detail}
		}
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
		if item.Status != StatusReady && item.Status != StatusChecking && SupportedExtension(item.Path) {
			indexes = append(indexes, index)
		}
	}
	return indexes
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
// left a file that could not be removed; per-file details are in the list.
// Only a cancellation ends items Cancelled, so the counts say whether the job
// was cancelled: a Cancel Resize that came once every item had ended changed
// nothing, and the job reads as finished. A job in which every item failed
// is an error, one in which any failed or was cancelled a warning, and any
// other a success.
func summaryNotice(summary job.Summary) ui.Notice {
	progress := summary.Progress
	format := ui.ResizeJobFinished
	if progress.Cancelled > 0 {
		format = ui.ResizeJobCancelled
	}
	message := fmt.Sprintf(format, progress.Completed, progress.Skipped, progress.Failed, progress.Cancelled, progress.Total)
	left := 0
	for _, status := range summary.Items {
		if errors.Is(status.Err, output.ErrLeftBehind) {
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

// jobFinish is a job's outcome: what job.Run returned, and the content
// checks the job made again before it started, which the view applies to the
// source list. released marks the outcome the view made up itself for a
// cancelled job it let go, as releaseJob describes.
type jobFinish struct {
	summary   job.Summary
	err       error
	rechecked []InspectionResult
	released  bool
}

// jobMailbox hands a running job's events, and then its outcome, from the
// job's goroutine to the GUI thread in batches, posting its drain tasks as
// drainSlot describes, so however quickly items change state, the job adds
// at most one message to the GUI thread's queue and can never fill it. It
// keeps only each item's latest event, as every event carries the counts of
// every state, so a drain applies the latest state of each item that changed
// and the latest counts. The outcome is taken with the events that precede
// it, so it is applied after them. It is safe for concurrent use.
type jobMailbox struct {
	mu     sync.Mutex
	latest map[int]mailedEvent
	next   uint64
	finish *jobFinish
	slot   drainSlot
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
	slices.SortFunc(entries, func(a, b mailedEvent) int {
		switch {
		case a.seq < b.seq:
			return -1
		case a.seq > b.seq:
			return 1
		}
		return 0
	})
	events := make([]job.Event, len(entries))
	for index, entry := range entries {
		events[index] = entry.event
	}
	finish := m.finish
	m.latest, m.finish = nil, nil
	m.slot.release()
	return events, finish
}

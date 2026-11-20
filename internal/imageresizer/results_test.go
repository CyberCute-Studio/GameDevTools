package imageresizer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// TestInventoryJobInputsCarryEachItemsCheck pins that a job lists every
// item in list order with its ID: a Ready item with its content check, an
// item the list could not use with the list's own explanation as its error,
// and one still being checked with no check and job.ErrUnchecked.
func TestInventoryJobInputsCarryEachItemsCheck(t *testing.T) {
	directory := t.TempDir()
	ready := writeSource(t, directory, "ready.png")
	damaged := writeSource(t, directory, "damaged.png")
	pending := writeSource(t, directory, "pending.png")
	unsupported := writeSource(t, directory, "notes.txt")
	inventory := NewInventory()
	summary := inventory.Add([]string{ready, damaged, pending, unsupported})
	info := decode.Info{Format: decode.FormatPNG, Width: 4, Height: 3, DecodedBytes: 48}
	inventory.Resolve(InspectionResult{ID: summary.Inspect[0].ID, Info: info})
	inventory.Resolve(InspectionResult{ID: summary.Inspect[1].ID, Err: &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}})

	if got := inventory.Ready(); got != 1 {
		t.Fatalf("Ready() = %d, want 1", got)
	}
	inputs, ids := inventory.JobInputs()
	if len(inputs) != 4 || len(ids) != 4 {
		t.Fatalf("JobInputs() = %d inputs, %d ids, want 4 each", len(inputs), len(ids))
	}
	for index, item := range inventory.Items() {
		if ids[index] != item.ID || inputs[index].Path != item.Path {
			t.Fatalf("input %d = (%d, %q), want (%d, %q)", index, ids[index], inputs[index].Path, item.ID, item.Path)
		}
	}
	if inputs[0].Err != nil || inputs[0].Info != info {
		t.Fatalf("ready input = %+v", inputs[0])
	}
	var source *sourceError
	if !errors.As(inputs[1].Err, &source) || source.status != StatusCorrupt || !strings.Contains(inputs[1].Err.Error(), "invalid checksum") {
		t.Fatalf("damaged input error = %v", inputs[1].Err)
	}
	if !errors.Is(inputs[2].Err, job.ErrUnchecked) || inputs[2].Info.Format != decode.FormatUnknown {
		t.Fatalf("checking input = %+v, want no check and job.ErrUnchecked", inputs[2])
	}
	for _, index := range []int{1, 3} {
		if inputs[index].Info != (decode.Info{}) {
			t.Fatalf("input %d the list could not use carries a check: %+v", index, inputs[index])
		}
	}
	if !errors.As(inputs[3].Err, &source) || source.status != StatusUnsupported || inputs[3].Err.Error() != ui.SourceUnsupportedDetail {
		t.Fatalf("unsupported input error = %v", inputs[3].Err)
	}
	if got := (&sourceError{}).Error(); got != ui.ResizeSourceUnavailableDetail {
		t.Fatalf("error without a detail = %q", got)
	}
}

// TestInventoryRechecksFailedItems pins which items a job checks again
// before it starts (RSZ-049) — every item whose check failed, or that could
// not be found when added, but not one refused for its extension, nor a Ready
// or Checking one — and that a check made again settles the item as its
// first check would have.
func TestInventoryRechecksFailedItems(t *testing.T) {
	directory := t.TempDir()
	inventory := NewInventory()
	summary := inventory.Add([]string{
		writeSource(t, directory, "ready.png"),
		writeSource(t, directory, "damaged.png"),
		writeSource(t, directory, "pending.png"),
		writeSource(t, directory, "notes.txt"),
		filepath.Join(directory, "missing.jpg"),
	})
	inventory.Resolve(InspectionResult{ID: summary.Inspect[0].ID, Info: decode.Info{Format: decode.FormatPNG, Width: 4, Height: 3}})
	inventory.Resolve(InspectionResult{ID: summary.Inspect[1].ID, Err: &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}})

	if got := inventory.Recheckable(); len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("Recheckable() = %v, want [1 4]", got)
	}
	info := decode.Info{Format: decode.FormatPNG, Width: 8, Height: 6}
	if index, ok := inventory.Recheck(InspectionResult{ID: inventory.Item(1).ID, Info: info}); !ok || index != 1 {
		t.Fatalf("Recheck(damaged) = %d, %v", index, ok)
	}
	if item := inventory.Item(1); item.Status != StatusReady || item.Detail != "" || item.Info != info || !item.Inspected {
		t.Fatalf("item checked again = %+v, want Ready with its new description", item)
	}
	if _, ok := inventory.Recheck(InspectionResult{ID: inventory.Item(4).ID, Err: &decode.Error{Kind: decode.KindRead, Err: fs.ErrNotExist}}); !ok {
		t.Fatal("Recheck(missing) was refused")
	}
	if item := inventory.Item(4); item.Status != StatusInaccessible || !strings.Contains(item.Detail, "not exist") {
		t.Fatalf("missing item checked again = %+v", item)
	}
	if _, ok := inventory.Recheck(InspectionResult{ID: inventory.Item(2).ID, Info: info}); ok || inventory.Item(2).Status != StatusChecking {
		t.Fatal("Recheck settled an item its own check is still running for")
	}
	if _, ok := inventory.Recheck(InspectionResult{ID: 1 << 40}); ok {
		t.Fatal("Recheck found an item for an unknown ID")
	}
	if input := recheckedInput(`C:\In\a.png`, InspectionResult{Info: info}); input.Err != nil || input.Info != info {
		t.Fatalf("recheckedInput(ready) = %+v", input)
	}
	var source *sourceError
	if input := recheckedInput(`C:\In\a.png`, InspectionResult{Err: &decode.Error{Kind: decode.KindUnsupported, Err: decode.ErrUnrecognized}}); !errors.As(input.Err, &source) || source.status != StatusUnsupported {
		t.Fatalf("recheckedInput(unsupported) = %+v", input)
	}
}

// TestSourceItemShowsJobResults pins that a row shows the job's state for
// its item in words (RSZ-043, UX-005) and the detail the item's result,
// until ClearResults restores the content check.
func TestSourceItemShowsJobResults(t *testing.T) {
	directory := t.TempDir()
	inventory := NewInventory()
	summary := inventory.Add([]string{writeSource(t, directory, "photo.png"), writeSource(t, directory, "notes.txt")})
	inventory.Resolve(InspectionResult{ID: summary.Inspect[0].ID, Info: decode.Info{Format: decode.FormatPNG, Width: 40, Height: 30}})
	photo, notes := inventory.Item(0), inventory.Item(1)

	tests := []struct {
		name   string
		id     uint64
		status job.Status
		row    string
		detail string
	}{
		{name: "pending", id: photo.ID, status: job.Status{State: job.StatePending}, row: "photo.png — 40 × 30 px — Pending", detail: "Pending: waiting to be resized."},
		{name: "processing", id: photo.ID, status: job.Status{State: job.StateProcessing, Destination: `C:\Out\photo.png`}, row: "photo.png — 40 × 30 px — Processing…", detail: `Processing: being resized and saved to C:\Out\photo.png.`},
		{name: "completed", id: photo.ID, status: job.Status{State: job.StateCompleted, Destination: `C:\Out\photo.png`}, row: "photo.png — 40 × 30 px — Completed", detail: `Completed: saved to C:\Out\photo.png.`},
		{name: "renamed", id: photo.ID, status: job.Status{State: job.StateCompleted, Destination: `C:\Out\photo_2.png`, Reason: output.ReasonExists}, row: "photo.png — 40 × 30 px — Completed", detail: `Completed: saved to C:\Out\photo_2.png. A numbered name was used because a file with the image's own name already exists in the output folder.`},
		{name: "skipped", id: photo.ID, status: job.Status{State: job.StateSkipped, Destination: `C:\Out\photo.png`, Reason: output.ReasonExists}, row: "photo.png — 40 × 30 px — Skipped", detail: `Skipped: C:\Out\photo.png was left untouched because a file with that name already exists.`},
		{name: "skipped duplicate", id: photo.ID, status: job.Status{State: job.StateSkipped, Destination: `C:\Out\photo.png`, Reason: output.ReasonDuplicate}, row: "photo.png — 40 × 30 px — Skipped", detail: `Skipped: C:\Out\photo.png was left untouched because another image in this job saved that name.`},
		{name: "cancelled", id: photo.ID, status: job.Status{State: job.StateCancelled}, row: "photo.png — 40 × 30 px — Cancelled", detail: ui.ResizeDetailCancelled},
		{name: "let go", id: photo.ID, status: job.Status{State: job.StateCancelled, Destination: `C:\Out\photo.png`, Err: errReleased}, row: "photo.png — 40 × 30 px — Cancelled", detail: ui.ResizeDetailReleased},
		{name: "failed source", id: notes.ID, status: job.Status{State: job.StateFailed, Err: &sourceError{status: StatusUnsupported, detail: ui.SourceUnsupportedDetail}}, row: "notes.txt — Failed", detail: "Failed: Unsupported format: " + ui.SourceUnsupportedDetail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index, ok := inventory.SetResult(test.id, test.status)
			if !ok {
				t.Fatal("SetResult did not find the item")
			}
			item := inventory.Item(index)
			if got := item.RowText(); got != test.row {
				t.Fatalf("RowText() = %q, want %q", got, test.row)
			}
			if got := item.DetailText(); got != test.detail {
				t.Fatalf("DetailText() = %q, want %q", got, test.detail)
			}
		})
	}
	if _, ok := inventory.SetResult(1<<40, job.Status{}); ok {
		t.Fatal("SetResult found an item for an unknown ID")
	}
	inventory.ClearResults()
	if got := inventory.Item(0).RowText(); got != "photo.png — 40 × 30 px — Ready" {
		t.Fatalf("row after ClearResults = %q", got)
	}
	if got := inventory.Item(1).DetailText(); got != "Unsupported format: "+ui.SourceUnsupportedDetail {
		t.Fatalf("detail after ClearResults = %q", got)
	}
}

// TestResultDetailNamesEveryReasonAndLeftoverFile pins the explanation of
// every reason a result took a numbered name or was skipped, and that a file
// a write could not remove is named whatever the item ended in.
func TestResultDetailNamesEveryReasonAndLeftoverFile(t *testing.T) {
	reasons := []output.Reason{output.ReasonExists, output.ReasonDuplicate, output.ReasonSource, output.ReasonUnverified, output.ReasonDevice, output.Reason(99)}
	renamed := make(map[string]bool)
	skipped := make(map[string]bool)
	for _, reason := range reasons {
		renamed[resultDetail(job.Status{State: job.StateCompleted, Destination: "x_2.png", Reason: reason})] = true
		skipped[resultDetail(job.Status{State: job.StateSkipped, Destination: "x.png", Reason: reason})] = true
	}
	if len(renamed) != len(reasons) || len(skipped) != len(reasons) {
		t.Fatalf("reasons share explanations: %d renamed, %d skipped, want %d each", len(renamed), len(skipped), len(reasons))
	}
	leftover := &output.Error{Kind: output.KindWrite, Op: "remove temporary file", Err: fmt.Errorf("%w: %s", output.ErrLeftBehind, `C:\Out\.gamedevtools-1.tmp`)}
	for _, state := range []job.State{job.StateCompleted, job.StateSkipped, job.StateCancelled} {
		detail := resultDetail(job.Status{State: state, Destination: "x.png", Err: leftover})
		if !strings.Contains(detail, "A file could not be removed afterwards") || !strings.Contains(detail, ".gamedevtools-1.tmp") {
			t.Fatalf("%v detail = %q, want the file left behind named", state, detail)
		}
	}
	if got := resultDetail(job.Status{State: job.State(42)}); got != ui.ResizeDetailPending {
		t.Fatalf("unknown state detail = %q", got)
	}
	if got := stateLabel(job.State(42)); got != ui.ResizeStatePending {
		t.Fatalf("unknown state label = %q", got)
	}
	if got := resultDetail(job.Status{State: job.StateFailed}); !strings.HasPrefix(got, "Failed: Unexpected error: ") {
		t.Fatalf("failed detail without an error = %q", got)
	}
}

// TestErrorKindClassifiesFailures pins that each kind of failure keeps its
// own category (UX-010), judged by the error that ended the item.
func TestErrorKindClassifiesFailures(t *testing.T) {
	leftover := &output.Error{Kind: output.KindWrite, Op: "remove temporary file", Err: output.ErrLeftBehind}
	tests := []struct {
		name string
		err  error
		want ui.ErrorKind
	}{
		{name: "unsupported source", err: &sourceError{status: StatusUnsupported}, want: ui.ErrorUnsupportedFormat},
		{name: "damaged source", err: &sourceError{status: StatusCorrupt}, want: ui.ErrorReadDecode},
		{name: "unreadable source", err: &sourceError{status: StatusInaccessible}, want: ui.ErrorReadDecode},
		{name: "unchecked", err: job.ErrUnchecked, want: ui.ErrorReadDecode},
		{name: "decode unsupported", err: &decode.Error{Kind: decode.KindUnsupported, Err: decode.ErrUnrecognized}, want: ui.ErrorUnsupportedFormat},
		{name: "decode read", err: &decode.Error{Kind: decode.KindRead, Err: fs.ErrNotExist}, want: ui.ErrorReadDecode},
		{name: "decode corrupt", err: &decode.Error{Kind: decode.KindCorrupt, Err: errors.New("bad")}, want: ui.ErrorReadDecode},
		{name: "resize invalid", err: &resize.Error{Kind: resize.KindInvalid, Err: resize.ErrTargetArea}, want: ui.ErrorValidation},
		{name: "resize too large", err: &resize.Error{Kind: resize.KindTooLarge, Err: resize.ErrSourceDimension}, want: ui.ErrorReadDecode},
		{name: "resize failed", err: &resize.Error{Kind: resize.KindFailed, Err: resize.ErrUnexpected}, want: ui.ErrorUnexpected},
		{name: "encode quality", err: &encode.Error{Kind: encode.KindInvalid, Err: encode.ErrQuality}, want: ui.ErrorValidation},
		{name: "encode too large", err: &encode.Error{Kind: encode.KindInvalid, Err: encode.ErrTooLargeForJPEG}, want: ui.ErrorWriteEncode},
		{name: "encode write", err: &encode.Error{Kind: encode.KindWrite, Err: errors.New("disk")}, want: ui.ErrorWriteEncode},
		{name: "output write", err: &output.Error{Kind: output.KindWrite, Err: output.ErrDiskFull}, want: ui.ErrorWriteEncode},
		{name: "output changed", err: &output.Error{Kind: output.KindChanged, Err: output.ErrDestinationChanged}, want: ui.ErrorWriteEncode},
		{name: "output destination", err: &output.Error{Kind: output.KindDestination, Err: output.ErrNameTooLong}, want: ui.ErrorWriteEncode},
		{name: "output folder", err: &output.Error{Kind: output.KindFolder, Err: output.ErrNotFolder}, want: ui.ErrorWriteEncode},
		{name: "output validation", err: &output.Error{Kind: output.KindValidation, Err: output.ErrNoSources}, want: ui.ErrorValidation},
		{name: "output confirmation", err: &output.Error{Kind: output.KindConfirmation, Err: output.ErrOverwriteUnconfirmed}, want: ui.ErrorValidation},
		{name: "contained panic", err: fmt.Errorf("%w: %w", job.ErrUnexpected, &decode.Error{Kind: decode.KindCorrupt}), want: ui.ErrorUnexpected},
		{name: "untyped", err: errors.New("boom"), want: ui.ErrorUnexpected},
		{name: "joined with an earlier leftover", err: errors.Join(&decode.Error{Kind: decode.KindRead, Err: fs.ErrPermission}, leftover), want: ui.ErrorReadDecode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := errorKind(test.err); got != test.want {
				t.Fatalf("errorKind(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

// TestRecoveryHintOnlyForOutputFailures pins that the recovery hints name
// the output failure that ended the item, and never blame the output folder
// for a source that cannot be read.
func TestRecoveryHintOnlyForOutputFailures(t *testing.T) {
	permission := &output.Error{Kind: output.KindWrite, Op: "create temporary file", Err: fs.ErrPermission}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "disk full", err: &output.Error{Kind: output.KindWrite, Err: fmt.Errorf("%w: %w", output.ErrDiskFull, errors.New("os"))}, want: ui.ResizeHintDiskFull},
		{name: "permission", err: permission, want: ui.ResizeHintPermission},
		{name: "read-only", err: &output.Error{Kind: output.KindWrite, Err: output.ErrReadOnly}, want: ""},
		{name: "left behind", err: &output.Error{Kind: output.KindWrite, Err: output.ErrLeftBehind}, want: ""},
		{name: "changed", err: &output.Error{Kind: output.KindChanged, Err: output.ErrDestinationChanged}, want: ui.ResizeHintChanged},
		{name: "folder lost", err: &output.Error{Kind: output.KindFolder, Err: fs.ErrNotExist}, want: ui.ResizeHintFolder},
		{name: "source permission", err: &decode.Error{Kind: decode.KindRead, Err: fs.ErrPermission}, want: ""},
		{name: "source failure joined with a leftover", err: errors.Join(&decode.Error{Kind: decode.KindRead, Err: fs.ErrPermission}, permission), want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recoveryHint(test.err); got != test.want {
				t.Fatalf("recoveryHint() = %q, want %q", got, test.want)
			}
		})
	}
	if got := failureText(permission); got != "Write/encode error: create temporary file: permission denied"+ui.ResizeHintPermission {
		t.Fatalf("failureText() = %q", got)
	}
}

// TestJobFailureMessagePresentsEachCause pins the category and recovery of
// a job that failed before any item was processed (UX-010).
func TestJobFailureMessagePresentsEachCause(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		prefix string
		text   string
	}{
		{name: "preset", err: &resize.Error{Kind: resize.KindInvalid, Err: resize.ErrTargetArea}, prefix: "Validation error: The selected preset cannot be used", text: "100 megapixels"},
		{name: "jpeg", err: &encode.Error{Kind: encode.KindInvalid, Err: encode.ErrQuality}, prefix: "Validation error: The JPEG options cannot be used", text: "between 1 and 100"},
		{name: "folder", err: &output.Error{Kind: output.KindFolder, Op: "create output folder", Err: output.ErrNotFolder}, prefix: "Write/encode error: The output folder could not be created or used", text: "Choose another folder with Browse"},
		{name: "no sources", err: &output.Error{Kind: output.KindValidation, Op: "validate request", Err: output.ErrNoSources}, prefix: "Validation error: " + ui.ResizeJobNoImages},
		{name: "confirmation", err: &output.Error{Kind: output.KindConfirmation, Op: "validate request", Err: output.ErrOverwriteUnconfirmed}, prefix: "Validation error: The resize job could not start", text: "only after the job is confirmed"},
		{name: "unexpected", err: errors.New("boom"), prefix: "Unexpected error: The resize job could not start: boom"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := jobFailureMessage(test.err)
			if !strings.HasPrefix(got, test.prefix) || !strings.Contains(got, test.text) {
				t.Fatalf("jobFailureMessage() = %q, want prefix %q containing %q", got, test.prefix, test.text)
			}
		})
	}
}

// TestSummaryNoticeCountsEveryOutcome pins the completion summary (RSZ-046):
// every outcome's count, whether the job was cancelled — as its Cancelled
// count says, so a cancellation that came once every item had ended reads as
// finished — items that left a file behind, and a severity that never relies
// on color alone.
func TestSummaryNoticeCountsEveryOutcome(t *testing.T) {
	leftover := &output.Error{Kind: output.KindWrite, Err: output.ErrLeftBehind}
	tests := []struct {
		name    string
		summary job.Summary
		kind    ui.NoticeKind
		text    string
	}{
		{name: "all completed", summary: job.Summary{Progress: job.Progress{Total: 2, Completed: 2}}, kind: ui.NoticeSuccess, text: "Success: Resize finished: 2 completed, 0 skipped, 0 failed, 0 cancelled, of 2 image(s)."},
		{name: "skipped only", summary: job.Summary{Progress: job.Progress{Total: 2, Completed: 1, Skipped: 1}}, kind: ui.NoticeSuccess, text: "1 skipped"},
		{name: "some failed", summary: job.Summary{Progress: job.Progress{Total: 3, Completed: 2, Failed: 1}}, kind: ui.NoticeWarning, text: "1 failed"},
		{name: "all failed", summary: job.Summary{Progress: job.Progress{Total: 2, Failed: 2}}, kind: ui.NoticeError, text: "2 failed"},
		{name: "cancelled", summary: job.Summary{Progress: job.Progress{Total: 3, Completed: 1, Cancelled: 2}}, kind: ui.NoticeWarning, text: "Warning: Resize cancelled: 1 completed, 0 skipped, 0 failed, 2 cancelled, of 3 image(s). Completed files were kept"},
		{name: "left behind", summary: job.Summary{Items: []job.Status{{State: job.StateCompleted, Err: leftover}, {State: job.StateCompleted}}, Progress: job.Progress{Total: 2, Completed: 2}}, kind: ui.NoticeWarning, text: "1 image(s) left a file that could not be removed"},
		// The file a replacement moved aside and could not delete is named by
		// a write error that does not wrap ErrLeftBehind; a Failed item's own
		// error names no file left behind, and errReleased none either.
		{name: "replaced file left", summary: job.Summary{Items: []job.Status{
			{State: job.StateCompleted, Err: &output.Error{Kind: output.KindWrite, Op: "remove replaced file", Err: &fs.PathError{Op: "delete", Path: `C:\Out\.gamedevtools-replaced-1.png`, Err: fs.ErrPermission}}},
			{State: job.StateFailed, Err: &output.Error{Kind: output.KindWrite, Err: output.ErrDiskFull}},
			{State: job.StateFailed, Err: errors.Join(errors.New("decode failed"), leftover)},
			{State: job.StateCancelled, Err: errReleased},
		}, Progress: job.Progress{Total: 4, Completed: 1, Failed: 2, Cancelled: 1}}, kind: ui.NoticeWarning, text: "2 image(s) left a file that could not be removed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notice := summaryNotice(test.summary)
			if notice.Kind != test.kind || !strings.Contains(notice.Text(), test.text) {
				t.Fatalf("summaryNotice() = %v %q, want %v containing %q", notice.Kind, notice.Text(), test.kind, test.text)
			}
		})
	}
}

// TestProgressMessageNamesTheCurrentFile pins the progress (RSZ-042): the
// done and total counts and the file being processed where practical.
func TestProgressMessageNamesTheCurrentFile(t *testing.T) {
	progress := job.Progress{Total: 10, Pending: 5, Processing: 2, Completed: 2, Skipped: 0, Failed: 1}
	if got := progressMessage(progress, "", 0, false); got != "Resizing: 3 of 10 image(s) done — 2 completed, 0 skipped, 1 failed." {
		t.Fatalf("progress without a current file = %q", got)
	}
	if got := progressMessage(progress, "b.png", 0, false); !strings.HasSuffix(got, " Processing b.png.") {
		t.Fatalf("progress with one file = %q", got)
	}
	if got := progressMessage(progress, "b.png", 1, false); !strings.HasSuffix(got, " Processing b.png and 1 more.") {
		t.Fatalf("progress with two files = %q", got)
	}
	if got := progressMessage(progress, "b.png", 1, true); !strings.HasPrefix(got, "Cancelling:") || !strings.Contains(got, "3 of 10 image(s) done") {
		t.Fatalf("progress while cancelling = %q", got)
	}
	// The widest progress is at least as long as any the job can show.
	widest := widestProgress(10, "longest.png")
	for _, got := range []string{progressMessage(progress, "b.png", 3, false), progressMessage(job.Progress{Total: 10, Completed: 10}, "longest.png", 9, false)} {
		if len(got) > len(widest) {
			t.Fatalf("progress %q is longer than the widest %q", got, widest)
		}
	}
}

// TestJobMailboxCoalescesEventsAndEndsLast pins the hand-off of a job's
// events: one drain task per batch, only each item's latest event, in the
// order those arrived, and the outcome after the events that preceded it.
func TestJobMailboxCoalescesEventsAndEndsLast(t *testing.T) {
	var mailbox jobMailbox
	event := func(index int, state job.State, done int) job.Event {
		return job.Event{Index: index, Status: job.Status{State: state}, Progress: job.Progress{Total: 3, Completed: done}}
	}
	if !mailbox.put(event(0, job.StateProcessing, 0)) {
		t.Fatal("the first event did not ask for a drain task")
	}
	if mailbox.put(event(1, job.StateProcessing, 0)) || mailbox.put(event(0, job.StateCompleted, 1)) {
		t.Fatal("an event asked for a second drain task while one was outstanding")
	}
	events, finish := mailbox.take()
	if finish != nil || len(events) != 2 {
		t.Fatalf("take() = %d events, finish %v; want 2 events and no finish", len(events), finish)
	}
	if events[0].Index != 1 || events[1].Index != 0 || events[1].Status.State != job.StateCompleted || events[1].Progress.Completed != 1 {
		t.Fatalf("take() = %+v, want item 1's event, then item 0's latest", events)
	}
	if !mailbox.put(event(2, job.StateFailed, 1)) {
		t.Fatal("an event after a drain did not ask for a new drain task")
	}
	if mailbox.end(jobFinish{summary: job.Summary{Folder: "out"}}) {
		t.Fatal("the outcome asked for a second drain task while one was outstanding")
	}
	events, finish = mailbox.take()
	if len(events) != 1 || events[0].Index != 2 || finish == nil || finish.summary.Folder != "out" {
		t.Fatalf("take() = %+v, %+v; want item 2's event and the outcome", events, finish)
	}
	if events, finish = mailbox.take(); len(events) != 0 || finish != nil {
		t.Fatalf("an empty mailbox returned %+v, %+v", events, finish)
	}
	if !mailbox.end(jobFinish{err: errors.New("failed")}) {
		t.Fatal("an outcome with nothing outstanding did not ask for a drain task")
	}

	// The content checks made again are kept, in order, until the view takes
	// them when the job ends.
	mailbox.recheck(InspectionResult{ID: 1})
	mailbox.recheck(InspectionResult{ID: 2})
	if events, finish = mailbox.take(); len(events) != 0 || finish == nil {
		t.Fatalf("take() = %+v, %+v; want the outcome alone", events, finish)
	}
	if rechecked := mailbox.takeRechecked(); len(rechecked) != 2 || rechecked[0].ID != 1 || rechecked[1].ID != 2 {
		t.Fatalf("takeRechecked() = %+v, want checks 1 and 2", rechecked)
	}
	if rechecked := mailbox.takeRechecked(); len(rechecked) != 0 {
		t.Fatalf("takeRechecked() again = %+v, want none", rechecked)
	}
}

// TestInventoryCancelledSummaryEndsItemsAsTheJobWould pins the outcome the
// view makes up for a job cancelled while it checked images again, or one it
// let go (RSZ-045): an item keeps the final state the job reported, one in
// flight is Cancelled with errReleased, one the job cannot resize is Failed
// with why — the list's reason, or job.ErrUnchecked — unless it was to be
// checked again and was not, and every other item, one no longer listed
// included, is Cancelled; the counts follow the items.
func TestInventoryCancelledSummaryEndsItemsAsTheJobWould(t *testing.T) {
	directory := t.TempDir()
	inventory := NewInventory()
	summary := inventory.Add([]string{
		writeSource(t, directory, "ready.png"),
		writeSource(t, directory, "done.png"),
		writeSource(t, directory, "flight.png"),
		writeSource(t, directory, "notes.txt"),
		writeSource(t, directory, "unchecked.png"),
		writeSource(t, directory, "broken.png"),
		writeSource(t, directory, "checking.png"),
	})
	info := decode.Info{Format: decode.FormatPNG, Width: 4, Height: 3}
	corrupt := &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}
	for index, check := range summary.Inspect[:5] {
		result := InspectionResult{ID: check.ID, Info: info}
		if index >= 3 {
			result = InspectionResult{ID: check.ID, Err: corrupt}
		}
		inventory.Resolve(result)
	}
	_, ids := inventory.JobInputs()
	for _, id := range ids {
		inventory.SetResult(id, job.Status{State: job.StatePending})
	}
	completed := job.Status{State: job.StateCompleted, Destination: `C:\Out\done.png`}
	inventory.SetResult(ids[1], completed)
	inventory.SetResult(ids[2], job.Status{State: job.StateProcessing, Destination: `C:\Out\flight.png`})
	ids = append(ids, 1<<40)

	got := inventory.cancelledSummary(ids, []int{4}, `C:\Out`)
	if got.Folder != `C:\Out` || len(got.Items) != len(ids) {
		t.Fatalf("summary = %+v", got)
	}
	cancelled := job.Status{State: job.StateCancelled}
	if got.Items[0] != cancelled || got.Items[1] != completed || got.Items[4] != cancelled || got.Items[7] != cancelled {
		t.Fatalf("ready, completed, unchecked, and unlisted items = %+v", got.Items)
	}
	if flight := got.Items[2]; flight.State != job.StateCancelled || flight.Destination != `C:\Out\flight.png` || !errors.Is(flight.Err, errReleased) {
		t.Fatalf("item in flight = %+v", flight)
	}
	var source *sourceError
	if unsupported := got.Items[3]; unsupported.State != job.StateFailed || !errors.As(unsupported.Err, &source) || source.status != StatusUnsupported {
		t.Fatalf("unsupported item = %+v", unsupported)
	}
	if broken := got.Items[5]; broken.State != job.StateFailed || !errors.As(broken.Err, &source) || source.status != StatusCorrupt {
		t.Fatalf("item still damaged = %+v", broken)
	}
	if checking := got.Items[6]; checking.State != job.StateFailed || !errors.Is(checking.Err, job.ErrUnchecked) {
		t.Fatalf("item still being checked = %+v", checking)
	}
	if got.Progress != (job.Progress{Total: 8, Completed: 1, Failed: 3, Cancelled: 4}) {
		t.Fatalf("progress = %+v", got.Progress)
	}
}

// TestInventoryCancelledSummaryAgreesWithTheJob pins cancelledSummary to
// job.Run's own outcome for a job cancelled before it starts, on the same
// list: an image the job cannot resize — refused for its extension, damaged,
// or still being checked — ends Failed with the same error, and a Ready one
// Cancelled, so the outcome the view makes up for a job it never ran, or let
// go, cannot drift from the job's.
func TestInventoryCancelledSummaryAgreesWithTheJob(t *testing.T) {
	directory := t.TempDir()
	inventory := NewInventory()
	summary := inventory.Add([]string{
		writeSource(t, directory, "ready.png"),
		writeSource(t, directory, "notes.txt"),
		writeSource(t, directory, "broken.png"),
		writeSource(t, directory, "checking.png"),
	})
	inventory.Resolve(InspectionResult{ID: summary.Inspect[0].ID, Info: decode.Info{Format: decode.FormatPNG, Width: 4, Height: 3}})
	inventory.Resolve(InspectionResult{ID: summary.Inspect[1].ID, Err: &decode.Error{Kind: decode.KindCorrupt, Format: decode.FormatPNG, Err: errors.New("invalid checksum")}})
	inputs, ids := inventory.JobInputs()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want, err := job.Run(ctx, job.Request{Inputs: inputs}, nil)
	if err != nil {
		t.Fatalf("job.Run() error = %v", err)
	}

	got := inventory.cancelledSummary(ids, nil, "")
	if got.Progress != want.Progress || len(got.Items) != len(want.Items) {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
	for index, status := range want.Items {
		mine := got.Items[index]
		if mine.State != status.State || fmt.Sprint(mine.Err) != fmt.Sprint(status.Err) || errorKind(mine.Err) != errorKind(status.Err) {
			t.Fatalf("item %d = %+v, want %+v", index, mine, status)
		}
	}
}

func TestBackgroundColor(t *testing.T) {
	got, err := BackgroundColor(" #12abEF ")
	if err != nil || got.R != 0x12 || got.G != 0xab || got.B != 0xef || got.A != 0xff {
		t.Fatalf("BackgroundColor() = %+v, %v", got, err)
	}
	if _, err := BackgroundColor("#12345"); err == nil || err.Error() != ui.ResizeBackgroundInvalid {
		t.Fatalf("BackgroundColor(invalid) error = %v", err)
	}
}

//go:build windows

package imageresizer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// TestNativeViewAsksBeforeClosingCancelsAJob pins the close guard closing
// the application consults (NativeCloseConfirmation): it asks only while a
// job runs that the user has not already cancelled, as closing cancels it.
func TestNativeViewAsksBeforeClosingCancelsAJob(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "photo.png")})
	settle(t, view, dispatcher)
	if _, message := view.NativeCloseConfirmation(); message != "" {
		t.Fatalf("close question without a job = %q, want none", message)
	}

	view.run = func(ctx context.Context, request job.Request, _ func(job.Event)) (job.Summary, error) {
		<-ctx.Done()
		return job.Summary{}, nil
	}
	view.NativeAction()
	if view.active == nil {
		t.Fatalf("the job did not start: %q", view.jobStatus.Text())
	}
	if title, message := view.NativeCloseConfirmation(); title != ui.ResizeCloseTitle || message != ui.ResizeCloseConfirmation {
		t.Fatalf("close question while the job runs = %q, %q", title, message)
	}
	view.jobCommand(jobCommandBase+cancelResizeCommand, 0)
	if _, message := view.NativeCloseConfirmation(); message != "" {
		t.Fatalf("close question once the job was cancelled = %q, want none", message)
	}
	drainJob(t, view, dispatcher)
	waitJobDone(t, view)
	if _, message := view.NativeCloseConfirmation(); message != "" {
		t.Fatalf("close question once the job ended = %q, want none", message)
	}
}

// TestNativeViewRestoreDefaultsKeepsTheListAndTheJob pins the Image Resizer
// half of Restore Default Settings (SET-005, APP-004): the resize options
// return to their defaults in the controls, while the source list, its
// status, and a running job and its progress are kept.
func TestNativeViewRestoreDefaultsKeepsTheListAndTheJob(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	defaults := view.options.Options()
	view.NativeDropFiles([]string{writeViewSource(t, sourceDir(t, view), "photo.png")})
	settle(t, view, dispatcher)
	other := filepath.Join(t.TempDir(), "Other")
	for _, change := range []func() error{
		func() error { return view.options.SetFormat(FormatJPEG) },
		func() error { return view.options.SetCollision(CollisionSkip) },
		func() error { return view.options.SetJPEGQuality(55) },
		func() error { return view.options.SetBackground("#123456") },
		func() error { return view.options.SetOutputFolder(other) },
	} {
		if err := change(); err != nil {
			t.Fatal(err)
		}
	}
	view.refresh()
	status := ui.Notice{Kind: ui.NoticeInfo, Message: "1 image added."}
	view.showSourceNotice(status)
	view.run = func(ctx context.Context, request job.Request, _ func(job.Event)) (job.Summary, error) {
		<-ctx.Done()
		return job.Summary{}, nil
	}
	view.NativeAction()
	progress := view.jobStatus.Text()

	delete(view.options.settings.Modules, string(moduleID))
	if followUp := view.NativeRestoreDefaults(); followUp != "" {
		t.Fatalf("follow-up with a default folder = %q, want none", followUp)
	}
	if got := view.options.Options(); got != defaults {
		t.Fatalf("options after the restore = %+v, want the defaults %+v", got, defaults)
	}
	if got := view.folder.Text(); got != defaults.OutputFolder {
		t.Fatalf("folder shown after the restore = %q, want %q", got, defaults.OutputFolder)
	}
	if got := view.quality.Edit.Text(); got != "90" {
		t.Fatalf("JPEG quality shown after the restore = %q, want 90", got)
	}
	if got := view.background.Edit.Text(); got != defaults.Background {
		t.Fatalf("background shown after the restore = %q, want %q", got, defaults.Background)
	}
	if got := view.collisionNote.Text(); got != collisionPolicyDescription(CollisionRename) {
		t.Fatalf("collision policy shown after the restore = %q, want Rename's", got)
	}
	if got := listContents(view.list); len(got) != 1 || !strings.HasPrefix(got[0], "photo.png") {
		t.Fatalf("source list after the restore = %q", got)
	}
	if got := view.status.Text(); got != status.Text() {
		t.Fatalf("status after the restore = %q, want it kept as %q", got, status.Text())
	}
	if view.active == nil || view.jobStatus.Text() != progress {
		t.Fatalf("the running job or its progress changed with the restore: %q, want %q", view.jobStatus.Text(), progress)
	}
	view.jobCommand(jobCommandBase+cancelResizeCommand, 0)
	drainJob(t, view, dispatcher)
	waitJobDone(t, view)
}

// TestNativeViewRestoreDefaultsWithoutADefaultFolder pins that a restore
// with no default output folder known, as when the Pictures folder cannot
// be resolved, returns the folder warning for the Settings view to report.
func TestNativeViewRestoreDefaultsWithoutADefaultFolder(t *testing.T) {
	view, _ := newSourcesTestView(t)
	view.options.store.defaultFolder = ""
	delete(view.options.settings.Modules, string(moduleID))
	followUp := view.NativeRestoreDefaults()
	if followUp == "" || !strings.Contains(followUp, ui.ResizeDefaultFolderUnknown) {
		t.Fatalf("follow-up without a default folder = %q, want the folder warning", followUp)
	}
	if got := view.folder.Text(); got != "" {
		t.Fatalf("folder shown = %q, want none", got)
	}
}

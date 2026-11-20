//go:build windows

package imageresizer

import (
	"fmt"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

// TestNativeViewAnnouncesAJobsStartAndSummaryOnly pins what a screen reader
// hears of a resize job: its start and its summary, which the job section
// announces as a live region, and not the progress for every image between
// them, which would queue one announcement per image, nor the selected
// image's state changing in the detail line beside them. The detail line is
// a live region too, which announces how an image's resize ended as the
// selection moves to it.
func TestNativeViewAnnouncesAJobsStartAndSummaryOnly(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	var paths []string
	for index := range 6 {
		paths = append(paths, writeFixture(t, directory, fmt.Sprintf("image%d.png", index), encodeTestPNG(t, 8, 6)))
	}
	view.NativeDropFiles(paths)
	settle(t, view, dispatcher)
	selectRow(view, 0)
	before := view.pathDetail.Text()

	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)
	view.NativeAction()
	if view.active == nil {
		t.Fatalf("the job did not start: %q", view.jobStatus.Text())
	}
	progressShown := 0
	for view.active != nil {
		dispatcher.runNext(t)
		progressShown++
	}
	waitJobDone(t, view)
	if progressShown < 2 {
		t.Fatalf("the job reported in %d batch(es); the test needs progress between its start and its end", progressShown)
	}
	if got := watch.Count(view.jobStatus.Handle()); got != 2 {
		t.Fatalf("the job section was announced %d times, want 2: the start and the summary", got)
	}
	if view.pathDetail.Text() == before {
		t.Fatalf("the selected image's detail stayed %q through the job; the test needs it to change", before)
	}
	if got := watch.Count(view.pathDetail.Handle()); got != 0 {
		t.Fatalf("the detail line was announced %d time(s) during the job, want none", got)
	}
	selectRow(view, 1)
	if got := watch.Count(view.pathDetail.Handle()); got != 1 {
		t.Fatalf("moving the selection to another resized image announced the detail line %d time(s), want once", got)
	}

	setting, err := uiatest.LiveSetting(view.pathDetail.Handle())
	if err != nil {
		t.Fatal(err)
	}
	if setting != 1 {
		t.Fatalf("the source detail line's live setting = %d, want Polite (1)", setting)
	}
}

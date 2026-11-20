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
// them, which would queue one announcement per image. The detail line for
// the selected image is a live region too, as it says how that image's
// resize ended.
func TestNativeViewAnnouncesAJobsStartAndSummaryOnly(t *testing.T) {
	view, dispatcher := newSourcesTestView(t)
	directory := sourceDir(t, view)
	var paths []string
	for index := range 6 {
		paths = append(paths, writeFixture(t, directory, fmt.Sprintf("image%d.png", index), encodeTestPNG(t, 8, 6)))
	}
	view.NativeDropFiles(paths)
	settle(t, view, dispatcher)

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

	setting, err := uiatest.LiveSetting(view.pathDetail.Handle())
	if err != nil {
		t.Fatal(err)
	}
	if setting != 1 {
		t.Fatalf("the source detail line's live setting = %d, want Polite (1)", setting)
	}
}

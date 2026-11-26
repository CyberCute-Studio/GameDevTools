//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/release"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/update"
)

const vkEscape = 0x1b

// fakeUpdate is a newer release the fake steps find, and what each step did.
type fakeUpdate struct {
	found       update.Release
	checkErr    error
	none        bool
	downloadErr error
	stageErr    error
	installErr  error
	skipped     []string
	calls       []string
	staging     string
}

func newFakeUpdate() *fakeUpdate {
	return &fakeUpdate{found: update.Release{Version: update.Version{9, 1, 0}, Tag: "v9.1.0", Archive: update.Asset{Name: "update.zip", Size: 2 << 20}}}
}

func (f *fakeUpdate) steps() updateSteps {
	return updateSteps{
		latest: func(ctx context.Context) (update.Release, bool, error) {
			f.calls = append(f.calls, "latest")
			if _, ok := ctx.Deadline(); !ok {
				return update.Release{}, false, errors.New("the check has no deadline")
			}
			return f.found, !f.none, f.checkErr
		},
		download: func(ctx context.Context, found update.Release, staging string, progress func(received, total int64)) (string, error) {
			f.calls = append(f.calls, "download")
			f.staging = staging
			progress(found.Archive.Size/2, found.Archive.Size)
			if f.downloadErr != nil {
				return "", f.downloadErr
			}
			path := filepath.Join(staging, found.Archive.Name)
			return path, os.WriteFile(path, []byte("zip"), 0o644)
		},
		stage: func(archive, staging string) ([]string, error) {
			f.calls = append(f.calls, "stage")
			return []string{release.ExecutableName()}, f.stageErr
		},
		install: func(executable, staging string, names []string) ([]string, error) {
			f.calls = append(f.calls, "install")
			return f.skipped, f.installErr
		},
	}
}

func TestRunUpdateStepsInstallsANewerRelease(t *testing.T) {
	folder := t.TempDir()
	fake := newFakeUpdate()
	var reports []updateStatus
	outcome := runUpdateSteps(context.Background(), fake.steps(), filepath.Join(folder, release.ExecutableName()), folder, func(status updateStatus) {
		reports = append(reports, status)
	})
	if outcome.result != updateInstalled || outcome.version != "9.1.0" {
		t.Fatalf("outcome = %+v, want installed 9.1.0", outcome)
	}
	if want := []string{"latest", "download", "stage", "install"}; !slices.Equal(fake.calls, want) {
		t.Errorf("steps = %v, want %v", fake.calls, want)
	}
	want := []updateStatus{
		{phase: updateDownloading, version: "9.1.0", total: 2 << 20},
		{phase: updateDownloading, version: "9.1.0", received: 1 << 20, total: 2 << 20},
		{phase: updateInstalling, version: "9.1.0", received: 2 << 20, total: 2 << 20},
	}
	if !slices.Equal(reports, want) {
		t.Errorf("reports = %+v, want %+v", reports, want)
	}
	if filepath.Dir(fake.staging) != folder {
		t.Errorf("the update was staged in %s, not in the folder it updates", fake.staging)
	}
	// The staging folder keeps the executable this process still runs.
	if _, err := os.Stat(fake.staging); err != nil {
		t.Errorf("the staging folder of an installed update was removed: %v", err)
	}
}

func TestRunUpdateStepsStartsAsItIsWithoutANewerRelease(t *testing.T) {
	for name, configure := range map[string]func(*fakeUpdate){
		"no newer release": func(f *fakeUpdate) { f.none = true },
		"check failed":     func(f *fakeUpdate) { f.checkErr = errors.New("no network") },
	} {
		folder := t.TempDir()
		fake := newFakeUpdate()
		configure(fake)
		reported := false
		outcome := runUpdateSteps(context.Background(), fake.steps(), filepath.Join(folder, release.ExecutableName()), folder, func(updateStatus) { reported = true })
		if outcome.result != updateNone && outcome.result != updateCheckFailed || reported || !slices.Equal(fake.calls, []string{"latest"}) {
			t.Errorf("%s: outcome = %+v, reported = %v, steps = %v", name, outcome, reported, fake.calls)
		}
		if outcome.warning(folder) != "" {
			t.Errorf("%s: warned %q", name, outcome.warning(folder))
		}
		if entries, _ := os.ReadDir(folder); len(entries) != 0 {
			t.Errorf("%s: the folder holds %v", name, entries)
		}
	}
}

func TestRunUpdateStepsLeavesTheFolderAsItWasWhenAnUpdateFails(t *testing.T) {
	for name, test := range map[string]struct {
		configure func(*fakeUpdate)
		result    updateResult
	}{
		"download failed": {func(f *fakeUpdate) { f.downloadErr = errors.New("checksum mismatch") }, updateDownloadFailed},
		"ZIP refused":     {func(f *fakeUpdate) { f.stageErr = errors.New("holds ../evil.exe") }, updateRefused},
		"install failed":  {func(f *fakeUpdate) { f.installErr = &update.InstallError{Err: errors.New("in use"), Restored: true} }, updateInstallFailed},
	} {
		folder := t.TempDir()
		fake := newFakeUpdate()
		test.configure(fake)
		outcome := runUpdateSteps(context.Background(), fake.steps(), filepath.Join(folder, release.ExecutableName()), folder, func(updateStatus) {})
		if outcome.result != test.result || outcome.previous != "" {
			t.Errorf("%s: outcome = %+v, want result %d", name, outcome, test.result)
		}
		if entries, _ := os.ReadDir(folder); len(entries) != 0 {
			t.Errorf("%s: a failed update left %v", name, entries)
		}
		if warning := outcome.warning(folder); !strings.Contains(warning, "9.1.0") || !strings.Contains(warning, release.Version) {
			t.Errorf("%s: warning = %q, want the versions named", name, warning)
		}
	}
}

func TestRunUpdateStepsKeepsTheOnlyCopyOfTheRunningVersion(t *testing.T) {
	folder := t.TempDir()
	fake := newFakeUpdate()
	previous := filepath.Join(folder, "staging", "previous.exe")
	fake.installErr = &update.InstallError{Err: errors.New("in use"), Previous: previous, Restored: false}
	outcome := runUpdateSteps(context.Background(), fake.steps(), filepath.Join(folder, release.ExecutableName()), folder, func(updateStatus) {})
	if outcome.result != updateInstallFailed || outcome.previous != previous || outcome.err.Error() != "in use" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := os.Stat(fake.staging); err != nil {
		t.Errorf("the staging folder holding the running version was removed: %v", err)
	}
	if warning := outcome.warning(folder); !strings.Contains(warning, previous) || !strings.Contains(warning, release.ExecutableName()) {
		t.Errorf("warning = %q, want it to say where the running version is and what to name it", warning)
	}
}

func TestRunUpdateStepsReportsAFolderItCannotWrite(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "gone")
	fake := newFakeUpdate()
	outcome := runUpdateSteps(context.Background(), fake.steps(), filepath.Join(folder, release.ExecutableName()), folder, func(updateStatus) {})
	if outcome.result != updateUnwritable || !slices.Equal(fake.calls, []string{"latest"}) {
		t.Fatalf("outcome = %+v, steps = %v", outcome, fake.calls)
	}
	warning := outcome.warning(folder)
	if !strings.Contains(warning, folder) || strings.Count(warning, folder) != 1 {
		t.Errorf("warning = %q, want the folder named once", warning)
	}
}

func TestRunUpdateStepsStopsWhenSkipped(t *testing.T) {
	folder := t.TempDir()
	fake := newFakeUpdate()
	ctx, cancel := context.WithCancel(context.Background())
	steps := fake.steps()
	download := steps.download
	steps.download = func(ctx context.Context, found update.Release, staging string, progress func(received, total int64)) (string, error) {
		cancel()
		path, _ := download(ctx, found, staging, progress)
		return path, ctx.Err()
	}
	outcome := runUpdateSteps(ctx, steps, filepath.Join(folder, release.ExecutableName()), folder, func(updateStatus) {})
	if outcome.result != updateDownloadCancelled || slices.Contains(fake.calls, "install") {
		t.Errorf("outcome = %+v, steps = %v", outcome, fake.calls)
	}
	if outcome.warning(folder) != "" {
		t.Errorf("a skipped update warned %q", outcome.warning(folder))
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Errorf("a skipped update left %v", entries)
	}
}

func TestRecordUpdateLogsFixedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	logger, err := diagnostics.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []updateOutcome{
		{result: updateNone}, {result: updateCheckFailed}, {result: updateInstalled, skipped: []string{"README.txt"}},
		{result: updateInstallFailed, previous: `C:\x\previous.exe`}, {result: updateUnwritable},
	} {
		recordUpdate(logger, outcome)
	}
	recordUpdate(nil, updateOutcome{result: updateInstalled})
	logger.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"update.check","code":"failed"`, `"update.install","code":"installed"`, `"update.install","code":"documents-skipped"`, `"update.install","code":"not-restored"`, `"update.install","code":"unwritable"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the log lacks %s:\n%s", want, data)
		}
	}
	if lines := strings.Count(string(data), "\n"); lines != 5 {
		t.Errorf("the log holds %d events, want 5 (none for no update):\n%s", lines, data)
	}
}

func TestUpdateWarningUnwrapsThePathOfAFolderError(t *testing.T) {
	err := &fs.PathError{Op: "mkdir", Path: `C:\Program Files\CyberCute Dev Tools\.cybercute-update-1`, Err: windows.ERROR_ACCESS_DENIED}
	warning := updateOutcome{result: updateUnwritable, version: "9.1.0", err: err}.warning(`C:\Program Files\CyberCute Dev Tools`)
	if want := fmt.Sprintf(ui.UpdateUnwritable, "9.1.0", `C:\Program Files\CyberCute Dev Tools`, windows.ERROR_ACCESS_DENIED); warning != want {
		t.Errorf("warning = %q, want %q", warning, want)
	}
}

// watchUpdateWindow records the update window runUpdateWindow creates and the
// handles of its status text and its button.
func watchUpdateWindow(t *testing.T) (hwnd, status, skip *windows.Handle) {
	t.Helper()
	hwnd, status, skip = new(windows.Handle), new(windows.Handle), new(windows.Handle)
	updateWindowCreated = func(created windows.Handle) {
		*hwnd = created
		window := updateWindows[created]
		*status, *skip = window.status.Handle(), window.skip.Handle()
	}
	t.Cleanup(func() { updateWindowCreated = nil })
	return hwnd, status, skip
}

// eventually reports whether condition holds within ten seconds.
func eventually(condition func() bool) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if condition() {
			return true
		}
	}
	return condition()
}

func isVisible(hwnd windows.Handle) bool {
	visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return visible != 0
}

func isEnabled(hwnd windows.Handle) bool {
	enabled, _, _ := procIsWindowEnabled.Call(uintptr(hwnd))
	return enabled != 0
}

func TestUpdateWindowStaysHiddenForAQuickCheck(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd, _, _ := watchUpdateWindow(t)
	everShown := false
	outcome, err := runUpdateWindow(func(ctx context.Context, report func(updateStatus)) updateOutcome {
		everShown = isVisible(*hwnd)
		return updateOutcome{result: updateNone}
	})
	if err != nil || outcome.result != updateNone {
		t.Fatalf("runUpdateWindow = %+v, %v", outcome, err)
	}
	if everShown || isVisible(*hwnd) {
		t.Error("the window appeared for a check that ended at once")
	}
	if alive, _, _ := procIsWindow.Call(uintptr(*hwnd)); alive != 0 {
		t.Error("the window outlived the update")
	}
	if len(updateWindows) != 0 {
		t.Errorf("%d update windows are still tracked", len(updateWindows))
	}
}

func TestUpdateWindowAppearsForASlowCheck(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd, status, _ := watchUpdateWindow(t)
	var text string
	var seen time.Time
	started := time.Now()
	outcome, err := runUpdateWindow(func(ctx context.Context, report func(updateStatus)) updateOutcome {
		if !eventually(func() bool { return isVisible(*hwnd) }) {
			return updateOutcome{result: updateCheckFailed}
		}
		seen = time.Now()
		text = windowText(*status)
		return updateOutcome{result: updateNone}
	})
	if early := seen.Sub(started); early < updateShowDelay-50*time.Millisecond {
		t.Errorf("the window appeared %v into the check, before %v", early, updateShowDelay)
	}
	// A check that ends just after the window appears does not flash it.
	if stayed := time.Since(seen); stayed < updateMinimumShow-50*time.Millisecond {
		t.Errorf("the window closed %v after it appeared, before %v", stayed, updateMinimumShow)
	}
	if err != nil || outcome.result != updateNone {
		t.Fatalf("the window did not appear while the check went on: %+v, %v", outcome, err)
	}
	if text != ui.UpdateChecking {
		t.Errorf("the window said %q while checking", text)
	}
}

func TestUpdateWindowShowsTheDownloadAndEscapeSkipsIt(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd, status, skip := watchUpdateWindow(t)
	want := fmt.Sprintf(ui.UpdateDownloading, "9.1.0", "1.0 MB", "4.0 MB")
	var title, shown, after string
	var skipEnabled bool
	outcome, err := runUpdateWindow(func(ctx context.Context, report func(updateStatus)) updateOutcome {
		report(updateStatus{phase: updateDownloading, version: "9.1.0", received: 1 << 20, total: 4 << 20})
		// The window appears as soon as an update is found, before the
		// delay a check is given.
		if !eventually(func() bool { return isVisible(*hwnd) && windowText(*status) == want }) {
			return updateOutcome{result: updateDownloadFailed}
		}
		title, shown, skipEnabled = windowText(*hwnd), windowText(*status), isEnabled(*skip)
		// Escape reaches the dialog manager through the focused button.
		procPostMessageW.Call(uintptr(*skip), wmKeyDown, vkEscape, 0)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			return updateOutcome{result: updateDownloadFailed}
		}
		eventually(func() bool { return windowText(*status) == ui.UpdateSkipping })
		after = windowText(*status)
		return updateOutcome{result: updateDownloadCancelled, version: "9.1.0"}
	})
	if err != nil || outcome.result != updateDownloadCancelled {
		t.Fatalf("Escape did not skip the update: %+v, %v", outcome, err)
	}
	if title != ui.UpdateWindowTitle || shown != want || !skipEnabled {
		t.Errorf("the window showed title %q, text %q, Skip Update enabled %v", title, shown, skipEnabled)
	}
	if after != ui.UpdateSkipping {
		t.Errorf("after Escape the window said %q", after)
	}
}

func TestUpdateWindowFinishesAnInstallItHasStarted(t *testing.T) {
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	hwnd, status, skip := watchUpdateWindow(t)
	replaced := false
	outcome, err := runUpdateWindow(func(ctx context.Context, report func(updateStatus)) updateOutcome {
		report(updateStatus{phase: updateInstalling, version: "9.1.0", received: 1, total: 1})
		if !eventually(func() bool { return isVisible(*hwnd) && !isEnabled(*skip) }) {
			return updateOutcome{result: updateInstallFailed}
		}
		// Closing the window asks to skip, which an install refuses.
		procPostMessageW.Call(uintptr(*hwnd), wmClose, 0, 0)
		time.Sleep(200 * time.Millisecond)
		if ctx.Err() != nil {
			return updateOutcome{result: updateDownloadCancelled}
		}
		if windowText(*status) != fmt.Sprintf(ui.UpdateInstalling, "9.1.0") {
			replaced = true
		}
		return updateOutcome{result: updateInstalled, version: "9.1.0"}
	})
	if err != nil || outcome.result != updateInstalled || replaced {
		t.Errorf("an install was stopped or its text replaced: %+v, %v", outcome, err)
	}
}

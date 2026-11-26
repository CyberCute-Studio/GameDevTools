//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/release"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/update"
)

const (
	// updateClassName is the update window's class. It is not the main
	// window's, so a second launch during an update waits for the main
	// window rather than taking the update window for it (claimInstance).
	updateClassName = "CyberCuteDevTools.Update"
	// wmAppUpdate tells the update window that the update's status changed.
	wmAppUpdate = wmApp + 3

	// updateShowDelay is how long the check for an update may take before
	// the window appears to say what the start waits for. A check that ends
	// sooner, as one that finds nothing newer typically does in about half a
	// second and one without a network at once, shows nothing. The window
	// appears at once when an update is found.
	updateShowDelay = time.Second
	// updateMinimumShow is how long the window stays once it has appeared,
	// so that a check ending just after updateShowDelay does not flash it.
	updateMinimumShow = 500 * time.Millisecond
	// updateRefreshInterval is how often the window reads the status even
	// without a wmAppUpdate, so that a message Windows could not post delays
	// the window rather than stopping it.
	updateRefreshInterval = 500 * time.Millisecond
	// updateCleanupWait bounds how long a start waits to remove what an
	// earlier update left, which the process it replaced holds as it exits.
	updateCleanupWait = 2 * time.Second

	updateShowTimer    = 1
	updateRefreshTimer = 2
	updateCloseTimer   = 3

	// idOK and idCancel are the commands IsDialogMessageW sends for Enter and
	// Escape; the Skip Update button sends idCancel too.
	idOK     = 1
	idCancel = 2
	// bnClicked is the notification code of a click.
	bnClicked = 0

	wsCaption     = 0x00c00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000

	// The update window's layout at 96 DPI.
	updateClientWidth   = 440
	updatePadding       = 20
	updateFontSize      = 15
	updateStatusHeight  = 44
	updateStatusGap     = 10
	updateBarHeight     = 6
	updateButtonGap     = 18
	updateButtonWidth   = 128
	updateButtonHeight  = 32
	updateBytesPerMByte = 1 << 20
)

var (
	procSetTimer  = user32.NewProc("SetTimer")
	procKillTimer = user32.NewProc("KillTimer")
)

// updatePhase is how far an update has come.
type updatePhase int

const (
	updateChecking updatePhase = iota
	updateDownloading
	updateInstalling
)

// updateStatus is what the update window shows.
type updateStatus struct {
	phase           updatePhase
	version         string
	received, total int64
}

// updateResult is what the update at startup came to.
type updateResult int

const (
	// updateNone found no newer release, or found one without a network.
	updateNone updateResult = iota
	updateCheckFailed
	updateCheckCancelled
	updateDownloadCancelled
	// updateUnwritable found a newer release that the folder cannot hold.
	updateUnwritable
	updateDownloadFailed
	// updateRefused downloaded a ZIP that an update does not install.
	updateRefused
	updateInstallFailed
	updateInstalled
)

// updateOutcome is the update's result, with the version it found, why it
// failed, and, when it failed with the running version set aside, where that
// is; when it installed, the documents it could not replace.
type updateOutcome struct {
	result   updateResult
	version  string
	err      error
	previous string
	skipped  []string
}

// updateSteps are the update's steps, which tests replace.
type updateSteps struct {
	latest   func(ctx context.Context) (update.Release, bool, error)
	download func(ctx context.Context, found update.Release, staging string, progress func(received, total int64)) (string, error)
	stage    func(archive, staging string) ([]string, error)
	install  func(executable, staging string, names []string) ([]string, error)
}

// startupUpdate brings an installation extracted from a release ZIP up to date
// before anything else starts (APP-006 through APP-008). It reports whether it
// started the version it installed, when this process must exit at once, and
// the notes for the main window's that an update which could not finish
// leaves. Anything else, a build run from another folder among it, starts as
// it is.
func startupUpdate(logger *diagnostics.Logger) (relaunched bool, warnings []string) {
	executable, err := os.Executable()
	if err != nil {
		return false, nil
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	folder, ok := update.Folder(executable)
	current, known := update.Current()
	machine, supported := update.Machine()
	if !ok || !known || !supported {
		return false, nil
	}
	removeLeftovers(folder)
	client := update.NewClient()
	steps := updateSteps{
		latest: func(ctx context.Context) (update.Release, bool, error) {
			return client.Latest(ctx, current, machine)
		},
		download: client.Download,
		stage: func(archive, staging string) ([]string, error) {
			return update.Stage(archive, staging, machine)
		},
		install: update.Install,
	}
	outcome, err := runUpdateWindow(func(ctx context.Context, report func(updateStatus)) updateOutcome {
		return runUpdateSteps(ctx, steps, executable, folder, report)
	})
	if err != nil {
		return false, nil
	}
	recordUpdate(logger, outcome)
	if outcome.result != updateInstalled {
		if warning := outcome.warning(folder); warning != "" {
			warnings = append(warnings, warning)
		}
		return false, warnings
	}
	if err := update.Relaunch(executable); err != nil {
		if logger != nil {
			_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelError, Operation: "update.relaunch", Code: "failed"})
		}
		return false, []string{fmt.Sprintf(ui.UpdateRelaunchFailed, outcome.version, err, release.Version)}
	}
	return true, nil
}

// removeLeftovers removes what earlier updates left in folder. Right after an
// update, the process it replaced may still be exiting, holding its
// executable there, so a failure is retried for a moment and then left for
// the next start.
func removeLeftovers(folder string) {
	for deadline := time.Now().Add(updateCleanupWait); ; time.Sleep(100 * time.Millisecond) {
		if update.Cleanup(folder) == nil || time.Now().After(deadline) {
			return
		}
	}
}

// runUpdateSteps looks for a newer release, and downloads, checks, and
// installs one it finds in folder, the executable's, reporting each step. It
// stages the update in a folder inside folder, which it removes unless the
// update installed, when that folder keeps the replaced executable this
// process still runs until a later start removes it, or the running version
// could not be put back, when it is the only copy of it. Cancelling ctx stops
// the update until it starts to install.
func runUpdateSteps(ctx context.Context, steps updateSteps, executable, folder string, report func(updateStatus)) updateOutcome {
	checkCtx, cancelCheck := context.WithTimeout(ctx, update.CheckTimeout)
	found, ok, err := steps.latest(checkCtx)
	cancelCheck()
	switch {
	case ctx.Err() != nil:
		return updateOutcome{result: updateCheckCancelled}
	case err != nil:
		return updateOutcome{result: updateCheckFailed, err: err}
	case !ok:
		return updateOutcome{result: updateNone}
	}
	version := found.Version.String()
	report(updateStatus{phase: updateDownloading, version: version, total: found.Archive.Size})
	staging, err := update.NewStaging(folder)
	if err != nil {
		return updateOutcome{result: updateUnwritable, version: version, err: err}
	}
	keep := false
	defer func() {
		if !keep {
			update.Discard(staging)
		}
	}()
	archive, err := steps.download(ctx, found, staging, func(received, total int64) {
		report(updateStatus{phase: updateDownloading, version: version, received: received, total: total})
	})
	if ctx.Err() != nil {
		return updateOutcome{result: updateDownloadCancelled, version: version}
	}
	if err != nil {
		return updateOutcome{result: updateDownloadFailed, version: version, err: err}
	}
	report(updateStatus{phase: updateInstalling, version: version, received: found.Archive.Size, total: found.Archive.Size})
	names, err := steps.stage(archive, staging)
	if err != nil {
		return updateOutcome{result: updateRefused, version: version, err: err}
	}
	if ctx.Err() != nil {
		return updateOutcome{result: updateDownloadCancelled, version: version}
	}
	skipped, err := steps.install(executable, staging, names)
	if err != nil {
		outcome := updateOutcome{result: updateInstallFailed, version: version, err: err}
		var failure *update.InstallError
		if errors.As(err, &failure) {
			outcome.err = failure.Err
			if !failure.Restored {
				outcome.previous = failure.Previous
				keep = true
			}
		}
		return outcome
	}
	keep = true
	return updateOutcome{result: updateInstalled, version: version, skipped: skipped}
}

// recordUpdate logs what the update came to (SET-007): nothing when there was
// no newer release, as at nearly every start.
func recordUpdate(logger *diagnostics.Logger, outcome updateOutcome) {
	if logger == nil {
		return
	}
	switch outcome.result {
	case updateCheckFailed:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelInfo, Operation: "update.check", Code: "failed"})
	case updateCheckCancelled:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelInfo, Operation: "update.check", Code: "cancelled"})
	case updateDownloadCancelled:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelInfo, Operation: "update.download", Code: "cancelled"})
	case updateUnwritable:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelWarning, Operation: "update.install", Code: "unwritable"})
	case updateDownloadFailed:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelWarning, Operation: "update.download", Code: "failed"})
	case updateRefused:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelError, Operation: "update.install", Code: "refused"})
	case updateInstallFailed:
		if outcome.previous != "" {
			_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelError, Operation: "update.install", Code: "not-restored"})
		} else {
			_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelError, Operation: "update.install", Code: "failed"})
		}
	case updateInstalled:
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelInfo, Operation: "update.install", Code: "installed"})
		if len(outcome.skipped) != 0 {
			_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelWarning, Operation: "update.install", Code: "documents-skipped"})
		}
	}
}

// warning is the main window's note on an update that found a newer release
// and could not install it, or "". A check that failed says nothing, as it
// does at every start without a network, and neither does one the user
// skipped.
func (o updateOutcome) warning(folder string) string {
	reason := o.err
	var pathErr *fs.PathError
	if errors.As(reason, &pathErr) {
		reason = pathErr.Err
	}
	switch o.result {
	case updateUnwritable:
		return fmt.Sprintf(ui.UpdateUnwritable, o.version, folder, reason)
	case updateDownloadFailed, updateRefused:
		return fmt.Sprintf(ui.UpdateFailed, o.version, reason, release.Version)
	case updateInstallFailed:
		if o.previous != "" {
			return fmt.Sprintf(ui.UpdateNotRestored, o.version, reason, o.previous, release.ExecutableName())
		}
		return fmt.Sprintf(ui.UpdateFailed, o.version, reason, release.Version)
	}
	return ""
}

// updateStatusText is the update window's text for a status.
func updateStatusText(status updateStatus) string {
	switch status.phase {
	case updateDownloading:
		return fmt.Sprintf(ui.UpdateDownloading, status.version, megabytes(status.received), megabytes(status.total))
	case updateInstalling:
		return fmt.Sprintf(ui.UpdateInstalling, status.version)
	}
	return ui.UpdateChecking
}

// megabytes writes a size in megabytes, to one decimal.
func megabytes(size int64) string {
	return fmt.Sprintf("%.1f MB", float64(size)/updateBytesPerMByte)
}

// updateWindow is the window an update shows at startup, in the style of a
// game launcher's: what the update is doing, its progress, and Skip Update,
// which stops it and starts the running version.
type updateWindow struct {
	hwnd   windows.Handle
	status *ui.Label
	skip   *ui.Button
	font   windows.Handle
	dpi    uint32
	cancel context.CancelFunc

	// mu guards what the update's goroutine reports: its latest status,
	// whether it has finished, and whether a wmAppUpdate is on its way.
	mu       sync.Mutex
	latest   updateStatus
	finished bool
	posted   bool

	// The rest belongs to the window's thread: the status shown, whether the
	// window is visible and since when, whether the user skipped the update,
	// and whether its message loop runs, which its destruction ends.
	shown    updateStatus
	visible  bool
	shownAt  time.Time
	skipping bool
	looping  bool
}

// updateWindows maps each update window to its state, for its procedure,
// which runs on the window's thread, as every access does.
var updateWindows = map[windows.Handle]*updateWindow{}

// updateWindowCreated is called with each update window as it is created,
// for tests.
var updateWindowCreated func(hwnd windows.Handle)

// runUpdateWindow runs work, the update, on another goroutine while its
// window runs on this thread, which owns no other window yet, and returns
// what work came to. The window appears once the check takes longer than
// updateShowDelay, or at once when an update is found, and closes when work
// returns.
func runUpdateWindow(work func(ctx context.Context, report func(updateStatus)) updateOutcome) (updateOutcome, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	window, err := newUpdateWindow(cancel)
	if err != nil {
		return updateOutcome{}, err
	}
	hwnd := uintptr(window.hwnd)
	procSetTimer.Call(hwnd, updateShowTimer, uintptr(updateShowDelay/time.Millisecond), 0)
	procSetTimer.Call(hwnd, updateRefreshTimer, uintptr(updateRefreshInterval/time.Millisecond), 0)
	window.looping = true
	done := make(chan updateOutcome, 1)
	go func() {
		outcome := work(ctx, window.report)
		done <- outcome
		window.finish()
	}()
	var message msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		if handled, _, _ := procIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&message))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
	if updateWindows[window.hwnd] != nil {
		// GetMessageW failed, which it does only for invalid arguments: stop
		// the update and close the window without waiting for its loop.
		cancel()
		outcome := <-done
		window.looping = false
		procDestroyWindow.Call(hwnd)
		return outcome, nil
	}
	return <-done, nil
}

// newUpdateWindow creates the update window, hidden, sized for its display and
// centered on it.
func newUpdateWindow(cancel context.CancelFunc) (*updateWindow, error) {
	if err := registerUpdateClass(); err != nil {
		return nil, err
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(updateClassName))),
		uintptr(unsafe.Pointer(utf16(ui.UpdateWindowTitle))),
		wsCaption|wsSysMenu|wsMinimizeBox,
		cwUseDefault, cwUseDefault, updateClientWidth, 200,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return nil, callError("CreateWindowExW(update)", err)
	}
	window := &updateWindow{hwnd: windows.Handle(hwnd), cancel: cancel, dpi: 96}
	updateWindows[window.hwnd] = window
	ui.ThemeWindow(window.hwnd, ui.WindowMain)
	if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
		window.dpi = uint32(dpi)
	}
	setWindowIcons(window.hwnd, window.dpi)
	if window.status, err = ui.NewWrappingLiveLabel(window.hwnd, ui.UpdateChecking); err == nil {
		window.skip, err = ui.NewButton(window.hwnd, ui.UpdateSkipAction, ui.ButtonOptions{ID: idCancel})
	}
	if err != nil {
		procDestroyWindow.Call(hwnd)
		return nil, err
	}
	window.applyFont()
	window.place()
	if updateWindowCreated != nil {
		updateWindowCreated(window.hwnd)
	}
	return window, nil
}

var (
	registerUpdateClassOnce sync.Once
	registerUpdateClassErr  error
)

// registerUpdateClass registers the update window's class once per process.
func registerUpdateClass() error {
	registerUpdateClassOnce.Do(func() {
		instance, _, err := procGetModuleHandleW.Call(0)
		if instance == 0 {
			registerUpdateClassErr = callError("GetModuleHandleW", err)
			return
		}
		cursor, _, _ := procLoadCursorW.Call(0, 32512)
		large, _, _ := procGetSystemMetrics.Call(smCxIcon)
		small, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		class := wndClassEx{
			LpfnWndProc:   windows.NewCallback(updateWindowProc),
			HInstance:     windows.Handle(instance),
			HIcon:         loadAppIcon(instance, large),
			HCursor:       windows.Handle(cursor),
			HbrBackground: windows.Handle(colorWindow + 1),
			LpszClassName: utf16(updateClassName),
			HIconSm:       loadAppIcon(instance, small),
		}
		class.CbSize = uint32(unsafe.Sizeof(class))
		if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
			registerUpdateClassErr = callError("RegisterClassExW", err)
		}
	})
	return registerUpdateClassErr
}

// report is called by the update's goroutine with its latest status.
func (w *updateWindow) report(status updateStatus) {
	w.mu.Lock()
	w.latest = status
	w.mu.Unlock()
	w.notify()
}

// finish is called by the update's goroutine once it has returned.
func (w *updateWindow) finish() {
	w.mu.Lock()
	w.finished = true
	w.mu.Unlock()
	w.notify()
}

// notify posts the window a wmAppUpdate unless one is already on its way, so
// a fast download fills its queue with no more than one.
func (w *updateWindow) notify() {
	w.mu.Lock()
	post := !w.posted
	w.posted = true
	w.mu.Unlock()
	if !post {
		return
	}
	if r, _, _ := procPostMessageW.Call(uintptr(w.hwnd), wmAppUpdate, 0, 0); r == 0 {
		// The refresh timer reads the status instead.
		w.mu.Lock()
		w.posted = false
		w.mu.Unlock()
	}
}

// isFinished reports whether the update's goroutine has returned.
func (w *updateWindow) isFinished() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finished
}

// refresh shows the latest status, or closes the window once the update has
// returned.
func (w *updateWindow) refresh() {
	w.mu.Lock()
	status, finished := w.latest, w.finished
	w.posted = false
	w.mu.Unlock()
	if finished {
		w.close()
		return
	}
	if status == w.shown {
		return
	}
	phaseChanged := status.phase != w.shown.phase
	w.shown = status
	if !w.skipping {
		// Download progress changes faster than a screen reader could read
		// it, so only a new step is announced.
		if text := updateStatusText(status); phaseChanged {
			w.status.SetText(text)
		} else {
			w.status.SetTextQuietly(text)
		}
	}
	if status.phase == updateInstalling {
		w.skip.Enable(false)
	}
	if status.phase != updateChecking && !w.visible {
		w.show()
	}
	procInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
}

// close destroys the window once the update has returned, which ends its
// message loop, unless the window appeared less than updateMinimumShow ago:
// then a timer closes it once it has been seen that long.
func (w *updateWindow) close() {
	if w.visible {
		if remaining := updateMinimumShow - time.Since(w.shownAt); remaining > 0 {
			procSetTimer.Call(uintptr(w.hwnd), updateCloseTimer, uintptr(remaining/time.Millisecond)+1, 0)
			return
		}
	}
	// The live region's marking outlives the window otherwise
	// (ui.Label.ClearLive).
	w.status.ClearLive()
	procDestroyWindow.Call(uintptr(w.hwnd))
}

// show makes the window visible and gives Skip Update the keyboard focus.
func (w *updateWindow) show() {
	w.visible = true
	w.shownAt = time.Now()
	procShowWindow.Call(uintptr(w.hwnd), swShow)
	procUpdateWindow.Call(uintptr(w.hwnd))
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(w.skip.Handle())); enabled != 0 {
		w.skip.Focus()
	}
	w.status.Announce()
}

// requestSkip stops the update, which then starts the running version, unless
// it has started to install, which it then finishes.
func (w *updateWindow) requestSkip() {
	if w.skipping {
		return
	}
	if w.shown.phase == updateInstalling {
		procMessageBeep.Call(0)
		return
	}
	w.skipping = true
	w.cancel()
	w.status.SetText(ui.UpdateSkipping)
	w.skip.Enable(false)
}

// scale converts a length at 96 DPI to the window's DPI.
func (w *updateWindow) scale(value int32) int32 {
	return int32((int64(value)*int64(w.dpi) + 48) / 96)
}

// applyFont gives the window's controls a font for its DPI.
func (w *updateWindow) applyFont() {
	font, err := ui.NewFont(w.scale(updateFontSize), fwNormal, ui.FontText)
	if err != nil {
		return
	}
	w.status.SetFont(font)
	w.skip.SetFont(font)
	if w.font != 0 {
		procDeleteObject.Call(uintptr(w.font))
	}
	w.font = font
}

// clientHeight is the height of the window's client area at its DPI.
func (w *updateWindow) clientHeight() int32 {
	return w.scale(updatePadding + updateStatusHeight + updateStatusGap + updateBarHeight + updateButtonGap + updateButtonHeight + updatePadding)
}

// barBounds is where the progress bar is in the client area.
func (w *updateWindow) barBounds() (x, y, width, height int32) {
	pad := w.scale(updatePadding)
	return pad, pad + w.scale(updateStatusHeight+updateStatusGap), w.scale(updateClientWidth) - 2*pad, w.scale(updateBarHeight)
}

// layout places the controls in the client area.
func (w *updateWindow) layout() {
	x, y, width, height := w.barBounds()
	w.status.Move(x, w.scale(updatePadding), width, w.scale(updateStatusHeight))
	buttonWidth := w.scale(updateButtonWidth)
	w.skip.Move(x+width-buttonWidth, y+height+w.scale(updateButtonGap), buttonWidth, w.scale(updateButtonHeight))
}

// place sizes the window for its client area and centers it on the work area
// of its display.
func (w *updateWindow) place() {
	var window, client rect
	procGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&window)))
	procGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&client)))
	width := w.scale(updateClientWidth) + (window.Right - window.Left) - (client.Right - client.Left)
	height := w.clientHeight() + (window.Bottom - window.Top) - (client.Bottom - client.Top)
	x, y := window.Left, window.Top
	if monitor, _, _ := procMonitorFromWindow.Call(uintptr(w.hwnd), monitorDefaultToNearest); monitor != 0 {
		info := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
			x = info.Work.Left + (info.Work.Right-info.Work.Left-width)/2
			y = info.Work.Top + (info.Work.Bottom-info.Work.Top-height)/2
		}
	}
	procSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), swpNoZOrder|swpNoActivate)
	w.layout()
}

// paint draws the progress bar: empty while checking, filled as far as the
// download has come, and full while installing.
func (w *updateWindow) paint() {
	fraction := 0.0
	switch w.shown.phase {
	case updateDownloading:
		if w.shown.total > 0 {
			fraction = float64(w.shown.received) / float64(w.shown.total)
		}
	case updateInstalling:
		fraction = 1
	}
	x, y, width, height := w.barBounds()
	ui.PaintProgressBar(w.hwnd, x, y, width, height, fraction)
}

func updateWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	if result, handled := ui.ThemeMessage(hwnd, message, wParam, lParam); handled {
		return result
	}
	if window := updateWindows[hwnd]; window != nil {
		switch message {
		case wmAppUpdate:
			window.refresh()
			return 0
		case wmTimer:
			switch wParam {
			case updateShowTimer:
				procKillTimer.Call(uintptr(hwnd), updateShowTimer)
				if !window.visible && !window.isFinished() {
					window.show()
				}
			case updateRefreshTimer:
				window.refresh()
			case updateCloseTimer:
				procKillTimer.Call(uintptr(hwnd), updateCloseTimer)
				window.close()
			}
			return 0
		case wmCommand:
			// A click, or Enter or Escape, which IsDialogMessageW sends as a
			// click; the button's other notifications, such as its focus,
			// carry its ID too.
			if id := lowWord(wParam); highWord(wParam) == bnClicked && (id == idOK || id == idCancel) {
				window.requestSkip()
			}
			return 0
		case wmClose:
			// Closing the window skips the update; the window closes once
			// the update has stopped.
			window.requestSkip()
			return 0
		case wmPaint:
			window.paint()
			return 0
		case wmDPIChanged:
			window.dpi = uint32(lowWord(wParam))
			setWindowIcons(hwnd, window.dpi)
			window.applyFont()
			if lParam != 0 {
				suggested := *(**rect)(unsafe.Pointer(&lParam))
				procSetWindowPos.Call(uintptr(hwnd), 0,
					uintptr(suggested.Left), uintptr(suggested.Top),
					uintptr(suggested.Right-suggested.Left), uintptr(suggested.Bottom-suggested.Top),
					swpNoZOrder|swpNoActivate)
			}
			window.layout()
			return 0
		case wmDestroy:
			procKillTimer.Call(uintptr(hwnd), updateShowTimer)
			procKillTimer.Call(uintptr(hwnd), updateRefreshTimer)
			procKillTimer.Call(uintptr(hwnd), updateCloseTimer)
			delete(updateWindows, hwnd)
			if window.font != 0 {
				procDeleteObject.Call(uintptr(window.font))
				window.font = 0
			}
			if window.looping {
				procPostQuitMessage.Call(0)
			}
			return 0
		}
	}
	result, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return result
}

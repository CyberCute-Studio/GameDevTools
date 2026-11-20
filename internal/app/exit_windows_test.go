//go:build windows

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var (
	procIsWindow           = user32.NewProc("IsWindow")
	procEnumThreadWindows  = user32.NewProc("EnumThreadWindows")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")

	// threadWindowsFound collects the thread's top-level windows for
	// threadWindows; the callback is created once, as the runtime never
	// releases one.
	threadWindowsFound    []windows.Handle
	threadWindowsCallback = windows.NewCallback(func(hwnd windows.Handle, _ uintptr) uintptr {
		threadWindowsFound = append(threadWindowsFound, hwnd)
		return 1
	})
)

// threadWindows returns this thread's top-level windows of the class, shown
// or hidden. Other packages' tests run alongside in their own processes with
// capture sessions of their own, so the system's windows are not searched.
func threadWindows(class string) []windows.Handle {
	threadWindowsFound = nil
	thread, _, _ := procGetCurrentThreadID.Call()
	procEnumThreadWindows.Call(thread, threadWindowsCallback, 0)
	var found []windows.Handle
	for _, hwnd := range threadWindowsFound {
		if className(hwnd) == class {
			found = append(found, hwnd)
		}
	}
	return found
}

// sessionWindows counts the capture overlay and toolbar windows this thread
// owns, shown or hidden.
func sessionWindows() int {
	return len(threadWindows("GameDevToolsRectangleOverlay")) + len(threadWindows("GameDevToolsRectangleToolbar"))
}

func isWindow(hwnd windows.Handle) bool {
	alive, _, _ := procIsWindow.Call(uintptr(hwnd))
	return alive != 0
}

// drainThreadMessages removes the messages left for the thread, such as the
// quit message a destroyed shell posts, so none reaches a later test.
func drainThreadMessages() {
	var message msg
	for {
		if available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, pmRemove); available == 0 {
			return
		}
	}
}

// exitRecordingView records the shell's shutdown of a module view and asks,
// as a close guard, the question guard holds, if any.
type exitRecordingView struct {
	recordingNativeView
	guard               string
	destroys, shutdowns int
}

func (v *exitRecordingView) NativeCloseConfirmation() (string, string) {
	return "Work running", v.guard
}
func (v *exitRecordingView) NativeDestroy()  { v.destroys++ }
func (v *exitRecordingView) NativeShutdown() { v.shutdowns++ }

// newExitTestShell is a command test shell with view as its module, a
// settings store on a temporary file, and a window large enough that its
// placement is saved; it returns the settings file's path.
func newExitTestShell(t *testing.T, view nativeView) (*nativeShell, string) {
	t.Helper()
	shell := newCommandTestShell(t, view)
	path := filepath.Join(t.TempDir(), "settings.json")
	value := settings.Default()
	shell.store = settings.NewStore(settings.Paths{ConfigFile: path})
	shell.settings = &value
	moveWindow(shell.hwnd, 40, 40, 640, 480)
	t.Cleanup(drainThreadMessages)
	return shell, path
}

// savedWindow reads the window settings the shell saved at path.
func savedWindow(t *testing.T, path string) settings.WindowSettings {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no settings were saved: %v", err)
	}
	var saved settings.Settings
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	return saved.Shell.Window
}

// TestCloseAsksBeforeCancellingWork pins the close guard in the shell
// (nativeCloseGuard): with work a module view would cancel, closing asks
// first, and No keeps the window and its work, saving nothing; Yes closes,
// saving the settings with the window's placement. Without such work,
// closing asks nothing.
func TestCloseAsksBeforeCancellingWork(t *testing.T) {
	view := &exitRecordingView{guard: "A job is running. Close anyway?"}
	shell, path := newExitTestShell(t, view)
	var questions []string
	answer := false
	shell.confirm = func(_ windows.Handle, title, message string) bool {
		questions = append(questions, title+": "+message)
		return answer
	}

	procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
	if len(questions) != 1 || questions[0] != "Work running: "+view.guard {
		t.Fatalf("questions = %q, want the guard's", questions)
	}
	if !isWindow(shell.hwnd) || view.destroys != 0 {
		t.Fatal("No closed the window")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("No saved the settings: %v", err)
	}

	answer = true
	procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
	if isWindow(shell.hwnd) || view.destroys != 1 {
		t.Fatalf("Yes left the window open (destroys = %d)", view.destroys)
	}
	if window := savedWindow(t, path); !window.HasPosition || window.Width != 640 || window.Height != 480 {
		t.Fatalf("saved window = %+v, want the closing window's placement", window)
	}

	idle := &exitRecordingView{}
	quiet, _ := newExitTestShell(t, idle)
	quiet.confirm = func(windows.Handle, string, string) bool {
		t.Error("closing without work asked a question")
		return false
	}
	procSendMessageW.Call(uintptr(quiet.hwnd), wmClose, 0, 0)
	if isWindow(quiet.hwnd) || idle.destroys != 1 {
		t.Fatal("closing without work did not close the window")
	}
}

// TestEndSessionSavesAndStopsWithoutAsking pins the end of the Windows
// session (endSession): the session's end is never refused, a session that
// goes on after all changes nothing, and one that ends saves the settings
// with the window's placement, destroys the window — shutting every module
// view down — and waits for the background work, asking nothing although
// work is running. The message loop's end, which the quit message the
// window's destruction posted brings when the process outlives the
// session's end, does not wait for the work a second time.
func TestEndSessionSavesAndStopsWithoutAsking(t *testing.T) {
	view := &exitRecordingView{guard: "A job is running. Close anyway?"}
	shell, path := newExitTestShell(t, view)
	shell.confirm = func(windows.Handle, string, string) bool {
		t.Error("the end of the session asked a question")
		return false
	}

	if allowed, _, _ := procSendMessageW.Call(uintptr(shell.hwnd), wmQueryEndSession, 0, 0); allowed != 1 {
		t.Fatalf("WM_QUERYENDSESSION returned %d, want the session's end allowed", allowed)
	}
	procSendMessageW.Call(uintptr(shell.hwnd), wmEndSession, 0, 0)
	if !isWindow(shell.hwnd) || view.destroys != 0 || view.shutdowns != 0 {
		t.Fatal("a session that goes on shut the application down")
	}

	procSendMessageW.Call(uintptr(shell.hwnd), wmEndSession, 1, 0)
	if isWindow(shell.hwnd) || view.destroys != 1 || view.shutdowns != 1 {
		t.Fatalf("after the session ended: window alive = %v, destroys = %d, shutdowns = %d; want it destroyed and shut down once", isWindow(shell.hwnd), view.destroys, view.shutdowns)
	}
	if window := savedWindow(t, path); !window.HasPosition || window.Width != 640 {
		t.Fatalf("saved window = %+v, want the window's placement", window)
	}
	shell.waitForBackgroundWork()
	if view.shutdowns != 1 {
		t.Fatalf("the message loop's end waited %d more time(s) for the work, want none", view.shutdowns-1)
	}
}

// TestEndSessionIsNotHeldUpByWorkThatDoesNotStop pins the bound on the end
// of the Windows session's wait (endSessionWait): background work that has
// not finished by then, such as a capture whose outputs a drive that does
// not respond holds, does not keep WM_ENDSESSION from returning, so Windows
// never lists the application as holding up the session's end, and the
// message loop's end, should the process outlive the message, waits for
// that work and does not start a second wait.
func TestEndSessionIsNotHeldUpByWorkThatDoesNotStop(t *testing.T) {
	previous := endSessionWait
	endSessionWait = 200 * time.Millisecond
	t.Cleanup(func() { endSessionWait = previous })
	view := &shutdownRecordingView{started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(view.release) }) }
	// Without the bound the message would wait until this lets the work go.
	timer := time.AfterFunc(5*time.Second, release)
	t.Cleanup(func() {
		timer.Stop()
		release()
	})
	shell, path := newExitTestShell(t, view)

	start := time.Now()
	procSendMessageW.Call(uintptr(shell.hwnd), wmEndSession, 1, 0)
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("WM_ENDSESSION returned after %v, want it within the bound of %v", elapsed, endSessionWait)
	}
	select {
	case <-view.started:
	default:
		t.Fatal("the end of the session did not wait for the work")
	}
	if isWindow(shell.hwnd) {
		t.Fatal("the end of the session left the window")
	}
	if window := savedWindow(t, path); !window.HasPosition {
		t.Fatalf("saved window = %+v, want the window's placement", window)
	}

	done := make(chan struct{})
	go func() {
		shell.waitForBackgroundWork()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the message loop's end did not wait for the work still running")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the message loop's end did not return once the work finished")
	}
}

// TestCloseQuestionEndedBySessionEnd pins that a close question the end of
// the Windows session interrupts, by destroying the window under it, does
// nothing more once answered: closing neither saves the settings again nor
// asks about a save that failed, and the window is shut down once. The
// question answers as ui.Confirm does, No once its window is gone, even
// after a Yes (ui.TestConfirmAnswersNoOnceItsParentIsDestroyed). The
// settings file is made unwritable once the session has ended, so a save
// the answer still ran would fail and ask its own question.
func TestCloseQuestionEndedBySessionEnd(t *testing.T) {
	view := &exitRecordingView{guard: "A job is running. Close anyway?"}
	shell, path := newExitTestShell(t, view)
	questions := 0
	shell.confirm = func(hwnd windows.Handle, _, _ string) bool {
		questions++
		if questions == 1 {
			procSendMessageW.Call(uintptr(shell.hwnd), wmEndSession, 1, 0)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return isWindow(hwnd)
	}
	procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
	if questions != 1 {
		t.Fatalf("questions = %d, want only the interrupted one", questions)
	}
	if isWindow(shell.hwnd) || view.destroys != 1 {
		t.Fatalf("window alive = %v, destroys = %d; want it destroyed once", isWindow(shell.hwnd), view.destroys)
	}
}

// TestFailedSaveWarnsAndClosingAsks pins SET-004 in the shell: a settings
// write that fails shows a warning and keeps the session's change in
// memory, and closing then asks before the unsaved settings are lost, No
// keeping the window open.
func TestFailedSaveWarnsAndClosingAsks(t *testing.T) {
	shell, path := newExitTestShell(t, &exitRecordingView{})
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	shell.settings.Shell.Window.Maximized = true
	if err := shell.saveSettings(false); err == nil {
		t.Fatal("saving over a folder succeeded")
	}
	if got := shell.note.Text(); !strings.Contains(got, "Settings could not be saved") || !strings.Contains(got, ui.NoticeWarningLabel) {
		t.Fatalf("note after the failed save = %q, want the warning", got)
	}
	if !shell.settings.Shell.Window.Maximized {
		t.Fatal("the failed save discarded the session's change")
	}

	var questions []string
	answer := false
	shell.confirm = func(_ windows.Handle, title, message string) bool {
		questions = append(questions, title)
		if title != ui.SettingsCloseTitle || message != ui.SettingsCloseConfirmation {
			t.Errorf("question = %q, %q, want the unsaved settings'", title, message)
		}
		return answer
	}
	procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
	if len(questions) != 1 || !isWindow(shell.hwnd) {
		t.Fatalf("questions = %q, window alive = %v; want one question and the window kept", questions, isWindow(shell.hwnd))
	}
	answer = true
	procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
	if len(questions) != 2 || isWindow(shell.hwnd) {
		t.Fatalf("questions = %q, window alive = %v; want Yes to close", questions, isWindow(shell.hwnd))
	}
}

// shortcutIsFree reports whether the combination the audit shell registers,
// Ctrl+Alt+Shift+F24, can be registered again, as it can once no window
// holds it (AC-A05).
func shortcutIsFree(t *testing.T) bool {
	t.Helper()
	const (
		modAltControlShift = 0x0001 | 0x0002 | 0x0004
		vkF24              = 0x87
		probeID            = 0xbeef
	)
	registered, _, _ := procRegisterHotKey.Call(0, probeID, modAltControlShift, vkF24)
	if registered == 0 {
		return false
	}
	procUnregisterHotKey.Call(0, probeID)
	return true
}

// exitShell closes the audit shell's window as the user does, or ends the
// Windows session under it, and waits for the background work as the end
// of the message loop does.
func exitShell(t *testing.T, shell *auditShell, endSession bool) {
	t.Helper()
	if endSession {
		procSendMessageW.Call(uintptr(shell.hwnd), wmEndSession, 1, 0)
	} else {
		procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
	}
	if isWindow(shell.hwnd) {
		t.Fatal("the main window survived the exit")
	}
	shell.waitForBackgroundWork()
}

var exits = []struct {
	name       string
	endSession bool
}{
	{"close", false},
	{"end of session", true},
}

// TestExitDuringACaptureSessionLeavesNoOverlayOrShortcut pins NFR-022,
// SCR-056, and AC-A05 on the real main window: exiting — closing the
// window, or the end of the Windows session — while a Free Rectangle or
// Preset Rectangle selection is on screen and the main window is hidden
// asks nothing, removes every session window, releases the global
// shortcut, and saves the settings, which a restart loads.
func TestExitDuringACaptureSessionLeavesNoOverlayOrShortcut(t *testing.T) {
	for _, mode := range []screenshot.CaptureMode{screenshot.ModeFreeRectangle, screenshot.ModePresetRectangle} {
		for _, exit := range exits {
			t.Run(fmt.Sprintf("%s, %s", mode, exit.name), func(t *testing.T) {
				shell := newAuditShell(t)
				shell.confirm = func(windows.Handle, string, string) bool {
					t.Error("exiting asked a question")
					return false
				}
				if err := shell.capture.SetMode(mode); err != nil {
					t.Fatal(err)
				}
				shell.selectModule(t, core.ModuleScreenshot)
				if shortcutIsFree(t) {
					t.Fatal("the global shortcut is not registered while the application runs")
				}
				shell.nativeViews[core.ModuleScreenshot].(nativeActionHandler).NativeAction()
				if sessionWindows() == 0 {
					t.Fatal("Start Capture opened no selection session")
				}
				if visible, _, _ := procIsWindowVisible.Call(uintptr(shell.hwnd)); visible != 0 {
					t.Fatal("the main window is shown during the selection (SCR-050)")
				}

				exitShell(t, shell, exit.endSession)
				if remaining := sessionWindows(); remaining != 0 {
					t.Fatalf("%d session window(s) remain after the exit", remaining)
				}
				if !shortcutIsFree(t) {
					t.Fatal("the global shortcut is still registered after the exit")
				}
				restarted := reopenSession(t, shell.path)
				if len(restarted.warnings()) != 0 || !restarted.value.Shell.Window.HasPosition || restarted.value.Shell.SelectedModule != string(core.ModuleScreenshot) {
					t.Fatalf("settings after the exit: warnings %v, shell %+v", restarted.warnings(), restarted.value.Shell)
				}
				if restarted.capture.Options().Mode != mode {
					t.Fatalf("capture mode after a restart = %v, want %v", restarted.capture.Options().Mode, mode)
				}
			})
		}
	}
}

// TestExitDuringAResizeJob pins exiting while a resize job runs on the real
// main window (NFR-020, NFR-022, NFR-023, RSZ-045): closing asks first, and
// No keeps the job running to its end; Yes, or the end of the Windows
// session, which asks nothing, cancels it and waits for it, leaving no
// temporary file in the output folder, only complete results, and every
// source unchanged, with the settings saved.
func TestExitDuringAResizeJob(t *testing.T) {
	cases := []struct {
		name       string
		endSession bool
		decline    bool
	}{
		{"close declined", false, true},
		{"close", false, false},
		{"end of session", true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			shell := newAuditShell(t)
			directory := filepath.Dir(shell.path)
			sources := make([]string, 0, 12)
			originals := map[string][]byte{}
			for index := range 12 {
				source := writePNG(t, filepath.Join(directory, "Sources", fmt.Sprintf("image-%02d.png", index)), 1600, 1200)
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				sources, originals[source] = append(sources, source), data
			}
			addSources(t, shell, sources...)
			var questions []string
			shell.confirm = func(_ windows.Handle, title, message string) bool {
				questions = append(questions, title)
				if title != ui.ResizeCloseTitle || message != ui.ResizeCloseConfirmation {
					t.Errorf("question = %q, %q, want the running job's", title, message)
				}
				return !test.decline
			}
			resizer := shell.nativeViews[core.ModuleImageResizer]
			resizer.(nativeActionHandler).NativeAction()
			running := func() bool {
				_, message := resizer.(nativeCloseGuard).NativeCloseConfirmation()
				return message != ""
			}
			if !running() {
				t.Fatal("Start Resize started no job")
			}

			output := filepath.Join(directory, "Resized")
			if test.decline {
				procSendMessageW.Call(uintptr(shell.hwnd), wmClose, 0, 0)
				if len(questions) != 1 || !isWindow(shell.hwnd) {
					t.Fatalf("questions = %q, window alive = %v; want one question and the window kept", questions, isWindow(shell.hwnd))
				}
				shell.pump(t, 60*time.Second, func() bool { return !running() })
				if results := checkResults(t, output); results != len(sources) {
					t.Fatalf("%d result(s) after the declined close, want the job to finish all %d", results, len(sources))
				}
			} else {
				exitShell(t, shell, test.endSession)
				if want := boolCount(!test.endSession); len(questions) != want {
					t.Fatalf("questions = %q, want %d", questions, want)
				}
				checkResults(t, output)
				restarted := reopenSession(t, shell.path)
				if len(restarted.warnings()) != 0 || !restarted.value.Shell.Window.HasPosition || restarted.value.Shell.SelectedModule != string(core.ModuleImageResizer) {
					t.Fatalf("settings after the exit: warnings %v, shell %+v", restarted.warnings(), restarted.value.Shell)
				}
			}
			for source, data := range originals {
				if current, err := os.ReadFile(source); err != nil || !bytes.Equal(current, data) {
					t.Fatalf("source %s changed: err = %v", filepath.Base(source), err)
				}
			}
		})
	}
}

// checkResults checks that the output folder holds no temporary file and
// that every result in it is a complete PNG at the first built-in preset's
// size, and returns how many results there are.
func checkResults(t *testing.T, folder string) int {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".gamedevtools-") || strings.HasSuffix(name, ".tmp") {
			t.Fatalf("temporary file %s remains in the output folder", name)
		}
		file, err := os.Open(filepath.Join(folder, name))
		if err != nil {
			t.Fatal(err)
		}
		picture, err := png.Decode(file)
		file.Close()
		if err != nil {
			t.Fatalf("result %s is not a complete PNG: %v", name, err)
		}
		if bounds := picture.Bounds(); bounds.Dx() != 16 || bounds.Dy() != 12 {
			t.Fatalf("result %s is %v, want 16 × 12", name, bounds)
		}
	}
	return len(entries)
}

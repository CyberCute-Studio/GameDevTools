//go:build windows

package app

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
)

// instanceMutexName is the per-session named mutex marking the running
// instance (APP-005). The Local namespace keeps instances per user session,
// so two users sharing a machine each get their own application.
const instanceMutexName = `Local\GameDevTools.SingleInstance`

// instanceWait bounds how long a second launch waits for the instance that
// holds the mutex but has no window: one still starting, which takes the
// mutex before its window exists, or one closing, which holds it after its
// window is gone until its background work has stopped — the Image Resizer
// waits up to imageresizer.JobShutdownWait for a cancelled job's images in
// flight, which the wait outlasts by five seconds for the rest of the
// shutdown, so raising that bound raises this one.
const instanceWait = imageresizer.JobShutdownWait + 5*time.Second

// enforceSingleInstance guarantees one running instance per user session
// (APP-005). When another instance already holds the mutex, its window is
// activated and activated reports true so the caller exits immediately;
// otherwise the returned release function must run when the process exits.
// A launch that lands while the other instance is closing waits for it to
// exit and then runs, as claimInstance describes.
func enforceSingleInstance() (release func(), activated bool, err error) {
	handle, activated, err := claimInstance(instanceMutexName, shellClassName, instanceWait)
	if err != nil || activated {
		return nil, activated, err
	}
	return func() { procCloseHandle.Call(handle) }, false, nil
}

// claimInstance acquires the named single-instance mutex, or activates the
// window of the instance that holds it and reports activated. While the
// other instance holds the mutex without a window, the claim tries again
// every 50 ms for up to wait: a window that appears is activated, and a
// mutex the other instance released by exiting is acquired, so a launch
// during its shutdown starts once it is gone rather than exiting without
// showing anything. A claim still blocked after wait gives up as activated,
// so a launch never runs beside another instance. When activated is false
// the caller owns the returned handle and must close it.
func claimInstance(name, className string, wait time.Duration) (handle uintptr, activated bool, err error) {
	deadline := time.Now().Add(wait)
	for {
		handle, already, err := acquireInstance(name)
		if err != nil {
			return 0, false, err
		}
		if !already {
			return handle, false, nil
		}
		// The duplicate handle keeps the mutex alive, so it is closed before
		// the next attempt: the other instance's exit frees the name only
		// once no handle to it remains.
		procCloseHandle.Call(handle)
		if activateExistingInstance(className, 0) || !time.Now().Before(deadline) {
			return 0, true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// acquireInstance creates the named single-instance mutex and reports
// whether another process already owned it. The caller owns the returned
// handle in both cases and must close it.
func acquireInstance(name string) (handle uintptr, already bool, err error) {
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(utf16(name))))
	if handle == 0 {
		return 0, false, callError("CreateMutexW", callErr)
	}
	if callErr == windows.ERROR_ALREADY_EXISTS {
		return handle, true, nil
	}
	return handle, false, nil
}

// activateExistingInstance brings the running instance's window back to the
// user (APP-005): a minimized window is restored first, then the window is
// moved to the foreground. It reports whether the window was found. The
// running instance takes the instance mutex before its window exists, so a
// second launch that lands in that startup gap retries for up to wait
// instead of exiting without showing anything. While the existing instance
// is capturing, its main window is intentionally hidden; activating a hidden
// window changes nothing on screen, and the running capture is left
// undisturbed.
func activateExistingInstance(className string, wait time.Duration) bool {
	hwnd := findInstanceWindow(className)
	for deadline := time.Now().Add(wait); hwnd == 0 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		hwnd = findInstanceWindow(className)
	}
	if hwnd == 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	procSetForegroundWindow.Call(hwnd)
	return true
}

// findInstanceWindow resolves the running instance's top-level window by its
// class name, or zero while that window does not exist yet.
func findInstanceWindow(className string) uintptr {
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16(className))), 0)
	return hwnd
}

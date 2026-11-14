//go:build windows

package app

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// instanceMutexName is the per-session named mutex marking the running
// instance (APP-005). The Local namespace keeps instances per user session,
// so two users sharing a machine each get their own application.
const instanceMutexName = `Local\GameDevTools.SingleInstance`

// enforceSingleInstance guarantees one running instance per user session
// (APP-005). When another instance already holds the mutex, its window is
// activated and activated reports true so the caller exits immediately;
// otherwise the returned release function must run when the process exits.
func enforceSingleInstance() (release func(), activated bool, err error) {
	handle, already, err := acquireInstance(instanceMutexName)
	if err != nil {
		return nil, false, err
	}
	if already {
		// The duplicate handle is not needed to activate the other
		// instance, and this process exits right after.
		procCloseHandle.Call(handle)
		activateExistingInstance(shellClassName, 2*time.Second)
		return nil, true, nil
	}
	return func() { procCloseHandle.Call(handle) }, false, nil
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

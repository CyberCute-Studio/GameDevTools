//go:build windows

package ui

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	bifReturnOnlyFsDirs     = 0x0001
	bifNewDialogStyle       = 0x0040
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	seeMaskNoAsync          = 0x00000100
	seeMaskFlagNoUI         = 0x00000400
	swShowNormal            = 1
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	ole32   = windows.NewLazySystemDLL("ole32.dll")

	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procShellExecuteExW      = shell32.NewProc("ShellExecuteExW")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

// shellExecuteInfo is SHELLEXECUTEINFOW, which ShellExecuteExW takes.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

type browseInfo struct {
	hwndOwner      windows.Handle
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// initializeCOM initializes a single-threaded COM apartment on the calling
// thread, as the folder picker and the shell extensions a shell verb loads
// require, without OLE 1.0's DDE, as the ShellExecute documentation asks,
// and returns the call that balances it. Every successful CoInitializeEx
// must be balanced by CoUninitialize — S_FALSE, for a thread already
// initialized, as much as S_OK — and a failed one, such as
// RPC_E_CHANGED_MODE on a thread initialized for another model, must not
// be, so the call returned then does nothing.
func initializeCOM() (uninitialize func()) {
	result, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if int32(result) < 0 {
		return func() {}
	}
	return func() { procCoUninitialize.Call() }
}

// BrowseForFolder opens the modal folder picker and returns the chosen
// absolute folder. It reports false without an error when the user cancels.
func BrowseForFolder(parent windows.Handle, title string) (string, bool, error) {
	// The picker requires an initialized COM apartment on the calling thread.
	defer initializeCOM()()
	displayName := make([]uint16, windows.MAX_PATH)
	info := browseInfo{
		hwndOwner:      parent,
		pszDisplayName: &displayName[0],
		lpszTitle:      utf16(title),
		ulFlags:        bifReturnOnlyFsDirs | bifNewDialogStyle,
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return "", false, nil
	}
	defer procCoTaskMemFree.Call(pidl)
	path := make([]uint16, windows.MAX_PATH)
	result, _, err := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if result == 0 {
		return "", false, callError("SHGetPathFromIDListW", err)
	}
	return windows.UTF16ToString(path), true, nil
}

// OpenFolder opens the given folder in File Explorer. It runs only on
// explicit user request, such as the Image Resizer's Open Output Folder
// action: the application never opens folders on its own after an operation
// completes (RSZ-047). A path that is not a folder now — the folder was
// deleted, or a file took its name — is refused rather than handed to the
// shell, and the shell's explore verb, which only folders have, is used, so
// a file is never opened or run in a folder's place. Looking at the folder
// and the shell's call each block for as long as its drive does not
// respond, so the GUI thread calls it on a goroutine of its own; it keeps
// that goroutine on one thread while it holds the COM apartment it
// initializes, and returns only once the shell has finished opening the
// folder, as shellExecute describes, since the thread, which runs no message
// loop, is released then.
func OpenFolder(folder string) error {
	info, err := os.Stat(folder)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", folder)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// As for the folder picker, shell extensions the verb may load expect an
	// initialized COM apartment.
	defer initializeCOM()()
	return shellExecute("explore", utf16("explore"), utf16(folder), nil)
}

// RevealInFolder opens File Explorer with the given file selected. It runs
// only on explicit user request: the application never opens folders on its
// own after an operation completes.
func RevealInFolder(path string) error {
	return shellExecute("explorer.exe", nil, utf16("explorer.exe"), utf16(`/select,"`+path+`"`))
}

// shellExecute runs ShellExecuteExW with no owner window for the verb, which
// is the default when nil, on the file with the parameters, which may be
// nil, showing no error dialog, as ShellExecuteW does: the caller reports
// the failure. It waits for the shell to finish the activation
// (SEE_MASK_NOASYNC), as a caller on a thread without a message loop must:
// the shell may otherwise finish it — a DDE conversation, say — through the
// thread's messages after the call returns, which a goroutine's thread,
// released then, never dispatches, so the folder or file would silently
// not open. A failure names the call by what, with the SE_ERR code the call
// leaves in hInstApp and the thread's last error, which it sets.
func shellExecute(what string, verb, file, parameters *uint16) error {
	info := shellExecuteInfo{
		fMask:        seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: parameters,
		nShow:        swShowNormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	succeeded, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if succeeded == 0 {
		if err == windows.ERROR_SUCCESS {
			return fmt.Errorf("ShellExecuteExW(%s) failed with code %d", what, info.hInstApp)
		}
		return fmt.Errorf("ShellExecuteExW(%s) failed with code %d: %w", what, info.hInstApp, err)
	}
	return nil
}

//go:build windows

package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	bifReturnOnlyFsDirs     = 0x0001
	bifNewDialogStyle       = 0x0040
	coinitApartmentThreaded = 0x2
	swShowNormal            = 1
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	ole32   = windows.NewLazySystemDLL("ole32.dll")

	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

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

// BrowseForFolder opens the modal folder picker and returns the chosen
// absolute folder. It reports false without an error when the user cancels.
func BrowseForFolder(parent windows.Handle, title string) (string, bool, error) {
	// The picker requires an initialized COM apartment on the calling thread.
	// S_FALSE means the thread is already initialized; only S_OK must be
	// balanced with CoUninitialize.
	initialized, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if initialized == 0 {
		defer procCoUninitialize.Call()
	}
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

// RevealInFolder opens File Explorer with the given file selected. It runs
// only on explicit user request: the application never opens folders on its
// own after an operation completes.
func RevealInFolder(path string) error {
	parameters := `/select,"` + path + `"`
	// ShellExecuteW reports success as a value greater than 32.
	result, _, err := procShellExecuteW.Call(
		0, 0,
		uintptr(unsafe.Pointer(utf16("explorer.exe"))),
		uintptr(unsafe.Pointer(utf16(parameters))),
		0, swShowNormal,
	)
	if result <= 32 {
		return callError("ShellExecuteW(explorer.exe)", err)
	}
	return nil
}

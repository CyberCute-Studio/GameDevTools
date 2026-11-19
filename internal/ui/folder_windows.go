//go:build windows

package ui

import (
	"fmt"
	"os"
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

// OpenFolder opens the given folder in File Explorer. It runs only on
// explicit user request, such as the Image Resizer's Open Output Folder
// action: the application never opens folders on its own after an operation
// completes (RSZ-047). A path that is not a folder now — the folder was
// deleted, or a file took its name — is refused rather than handed to the
// shell, and the shell's explore verb, which only folders have, is used, so
// a file is never opened or run in a folder's place.
func OpenFolder(folder string) error {
	info, err := os.Stat(folder)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", folder)
	}
	// As for the folder picker, shell extensions the verb may load expect an
	// initialized COM apartment.
	initialized, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if initialized == 0 {
		defer procCoUninitialize.Call()
	}
	result, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(utf16("explore"))),
		uintptr(unsafe.Pointer(utf16(folder))),
		0, 0, swShowNormal,
	)
	if result <= 32 {
		if callErr == windows.ERROR_SUCCESS {
			return fmt.Errorf("ShellExecuteW(explore) failed with code %d", result)
		}
		return fmt.Errorf("ShellExecuteW(explore) failed with code %d: %w", result, callErr)
	}
	return nil
}

// RevealInFolder opens File Explorer with the given file selected. It runs
// only on explicit user request: the application never opens folders on its
// own after an operation completes.
func RevealInFolder(path string) error {
	parameters := `/select,"` + path + `"`
	// ShellExecuteW reports success as a value greater than 32; a failure
	// value IS the error code and the thread's last error is unreliable,
	// so the code must be part of the reported error.
	result, _, err := procShellExecuteW.Call(
		0, 0,
		uintptr(unsafe.Pointer(utf16("explorer.exe"))),
		uintptr(unsafe.Pointer(utf16(parameters))),
		0, swShowNormal,
	)
	if result <= 32 {
		if err == windows.ERROR_SUCCESS {
			return fmt.Errorf("ShellExecuteW(explorer.exe) failed with code %d", result)
		}
		return fmt.Errorf("ShellExecuteW(explorer.exe) failed with code %d: %w", result, err)
	}
	return nil
}

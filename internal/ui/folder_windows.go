//go:build windows

package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
// a file is never opened or run in a folder's place. The folder is named as
// shellPath spells it, and one Explorer would show as another place is
// refused with an error. Looking at the folder
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
	target, err := shellPath(folder)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// As for the folder picker, shell extensions the verb may load expect an
	// initialized COM apartment.
	defer initializeCOM()()
	return shellExecute("explore", utf16("explore"), utf16(target), nil)
}

// RevealInFolder opens File Explorer with the given file selected. It runs
// only on explicit user request: the application never opens folders on its
// own after an operation completes. Explorer is named by its full path, as a
// bare name would be looked up through a search path that can include the
// application's and the current folder, where another explorer.exe could be
// waiting. The file is named as shellPath spells it. Explorer's /select does
// not read a path through a volume GUID, opening another folder without
// reporting a failure, so such a file is refused with an error.
func RevealInFolder(path string) error {
	target, err := shellPath(path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(target, `\\?\`) {
		return errNoDriveOrShare
	}
	explorer, err := explorerPath()
	if err != nil {
		return err
	}
	return shellExecute("explorer.exe", nil, utf16(explorer), utf16(`/select,"`+target+`"`))
}

var (
	// errNoDriveOrShare is shellPath's and RevealInFolder's failure for a
	// path File Explorer cannot be pointed at, as it has no drive or share.
	errNoDriveOrShare = errors.New("File Explorer cannot show a path that has no drive letter or network share")
	// errShellReadsAnotherName is shellPath's failure for a path the shell
	// would read as another file or folder.
	errShellReadsAnotherName = errors.New("File Explorer would show another location, because a name in the path ends in a space or a period or is reserved by Windows")
)

// shellPath is the spelling of a path that File Explorer reads as the same
// file or folder: the ordinary drive or UNC spelling, which the shell's
// parser accepts where it refuses \\?\, \??\, and \\.\, or, for a path
// through a volume GUID, which has no ordinary spelling, the \\?\ spelling
// the explore verb accepts. A plain path is first resolved as Windows
// resolves it, which is where the file was saved.
//
// The shell's parser drops trailing spaces and periods from every name in a
// path, even under \\?\, while Windows keeps them in a name a longer path
// leads through, so C:\t\v2..\a.png would select C:\t\v2\a.png. Such a path,
// one that the ordinary spelling would resolve to something else, as with a
// reserved name such as NUL, and one through any other device are refused
// with an error, since Explorer would otherwise show the wrong place without
// reporting a failure. Resolving the spelling reads no disk or network.
func shellPath(path string) (string, error) {
	full := path
	switch {
	case strings.HasPrefix(path, `\\?\`):
	case strings.HasPrefix(path, `\??\`):
		full = `\\?\` + path[4:]
	default:
		resolved, err := windows.FullPath(path)
		if err != nil {
			return "", err
		}
		full = resolved
	}
	target := full
	if strings.HasPrefix(full, `\\?\`) || strings.HasPrefix(full, `\\.\`) {
		switch rest := full[4:]; {
		case len(rest) > 4 && strings.EqualFold(rest[:4], `UNC\`):
			target = `\\` + rest[4:]
		case len(rest) >= 3 && rest[1] == ':' && rest[2] == '\\':
			target = rest
		case len(rest) > 7 && strings.EqualFold(rest[:7], `Volume{`):
			target = `\\?\` + rest
		default:
			return "", errNoDriveOrShare
		}
	}
	if !strings.HasPrefix(target, `\\?\`) {
		if resolved, err := windows.FullPath(target); err != nil || resolved != target {
			return "", errShellReadsAnotherName
		}
	}
	names := strings.Split(target, `\`)
	// One trailing period on a server's name is the root of its fully
	// qualified name, nas.example.com., and leads to the same server. The name
	// "." alone is a device prefix, never a server, so it stays refused.
	if strings.HasPrefix(target, `\\`) && !strings.HasPrefix(target, `\\?\`) && len(names[2]) > 1 {
		names[2] = strings.TrimSuffix(names[2], ".")
	}
	for _, name := range names {
		if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
			return "", errShellReadsAnotherName
		}
	}
	return target, nil
}

// explorerPath returns File Explorer's full path in the Windows folder.
func explorerPath() (string, error) {
	folder, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return "", fmt.Errorf("locate the Windows folder: %w", err)
	}
	return filepath.Join(folder, "explorer.exe"), nil
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

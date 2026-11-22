//go:build windows

package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/pathform"
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
// The shell reports a folder without the periods its name ends in, so a
// folder named v2.. comes back as v2, and one named only periods as its
// parent with a separator after it. A reported path that does not exist, or
// that has that separator, is refused with an error rather than returned, as
// is one whose drive does not answer within shellNameWait.
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
	folder := windows.UTF16ToString(path)
	if err := awaitShellNameCheck(folder, checkShellName, shellNameWait); err != nil {
		return "", false, err
	}
	return folder, true, nil
}

// shellNameWait is how long the GUI thread, which the picker runs on, waits
// for checkShellName. The picker has just read the folder, so its drive
// answers at once unless it has stopped responding, and the wait stays short
// of the five seconds after which Windows reports the window as hung.
const shellNameWait = 2 * time.Second

// awaitShellNameCheck runs the check on a goroutine of its own, as looking at
// a drive blocks for as long as the drive does not respond, and waits for it
// no longer than wait. A check still running then is abandoned, and the
// choice is refused, since its name was not confirmed.
func awaitShellNameCheck(folder string, check func(string) error, wait time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- check(folder) }()
	select {
	case err := <-result:
		return err
	case <-time.After(wait):
		return errFolderNotResponding
	}
}

// verbatimFolder is a path the shell gave in the verbatim spelling, which
// keeps a name's trailing space that any other spelling would lose, and the
// length of its volume name there. A path through a volume GUID is already
// in that spelling.
func verbatimFolder(folder string) (string, int) {
	volume := len(pathform.VolumeName(folder))
	switch {
	case strings.HasPrefix(folder, `\\?\`):
		return folder, volume
	case strings.HasPrefix(folder, `\\`):
		return `\\?\UNC\` + folder[len(`\\`):], len(`\\?\UNC\`) + volume - len(`\\`)
	}
	return `\\?\` + folder, len(`\\?\`) + volume
}

// checkShellName reports why a path the shell gave for a chosen folder cannot
// be stored. The shell does not say when it has shortened a name, so a path
// that is not there, or that has an empty name, is refused. A path that cannot
// be inspected for another reason, such as a denied folder, is left for the
// save to report. A shortened name that happens to match a real folder, as
// when v2 and v2.. stand side by side, cannot be told from that folder; File
// Explorer cannot create such names.
func checkShellName(folder string) error {
	verbatim, volume := verbatimFolder(folder)
	rest := verbatim[volume:]
	if len(rest) > 1 && (strings.HasSuffix(rest, `\`) || strings.Contains(rest, `\\`)) {
		return errShellNameUnconfirmed
	}
	if _, err := os.Stat(verbatim); errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return errShellNameUnconfirmed
	}
	return nil
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
// reporting a failure, so such a file is refused with an error, and so is a
// file that cannot be found, for which Explorer opens some other folder and
// reports success. Looking at the file and the shell's call each
// block for as long as its drive does not respond, so the GUI thread calls it
// on a goroutine of its own, which it keeps on one thread while it holds the
// COM apartment it initializes, as OpenFolder does.
func RevealInFolder(path string) error {
	target, err := shellPath(path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(target, `\\?\`) {
		return errNoDriveOrShare
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist), err == nil && info.IsDir():
		return errSavedFileNotFound
	case err != nil:
		return err
	}
	explorer, err := explorerPath()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer initializeCOM()()
	return shellExecute("explorer.exe", nil, utf16(explorer), utf16(`/select,"`+target+`"`))
}

var (
	// errShellNameUnconfirmed is BrowseForFolder's failure for a folder whose
	// exact name the shell's report could not confirm.
	errShellNameUnconfirmed = errors.New("Windows could not confirm this folder's exact name, which happens when a name in its path ends in a period or the folder has been removed. Choose another folder")
	// errFolderNotResponding is BrowseForFolder's failure for a folder whose
	// drive did not answer in time for its name to be confirmed.
	errFolderNotResponding = errors.New("the drive of the chosen folder is not responding. Try again, or choose another folder")
	// errSavedFileNotFound is RevealInFolder's failure for a file it cannot
	// find. Windows reports a deleted file, an unplugged drive, an offline
	// server, and a broken junction alike, so the message names them all.
	errSavedFileNotFound = errors.New("the saved file could not be found. It may have been moved or deleted, or its drive or network share may not be available")
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
// leads through, so C:\t\v2..\a.png would select C:\t\v2\a.png. It also
// refuses a reserved name such as COM1 wherever it falls, though Windows keeps
// one in a name a longer path leads through. Such a path, one that the
// ordinary spelling would resolve to something else, and one through any
// other device are refused with an error, since Explorer would otherwise show
// the wrong place without reporting a failure. Resolving the spelling reads
// no disk or network.
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
	if pathform.HasDeviceName(target) {
		return "", errShellReadsAnotherName
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

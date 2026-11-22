//go:build windows

package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestInitializeCOMBalancesEverySuccessfulCall pins the COM rule the folder
// picker and OpenFolder follow: a CoInitializeEx that finds the thread
// already initialized returns S_FALSE and must still be balanced, or the
// apartment never shuts down. The check runs on a thread of its own, locked
// and never unlocked, so the runtime ends the thread with its goroutine.
func TestInitializeCOMBalancesEverySuccessfulCall(t *testing.T) {
	const sOK, sFalse = 0, 1
	failure := make(chan string, 1)
	go func() {
		runtime.LockOSThread()
		first, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
		if first != sOK {
			if int32(first) >= 0 {
				procCoUninitialize.Call()
			}
			failure <- "skip"
			return
		}
		nested, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
		if nested != sFalse {
			failure <- "a nested CoInitializeEx did not report S_FALSE"
			return
		}
		procCoUninitialize.Call()
		// The thread is initialized once now, so initializeCOM's call returns
		// S_FALSE, which its returned call must balance.
		initializeCOM()()
		procCoUninitialize.Call()
		again, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
		if int32(again) >= 0 {
			procCoUninitialize.Call()
		}
		if again != sOK {
			failure <- "the apartment stayed initialized: initializeCOM left an S_FALSE call unbalanced"
			return
		}
		failure <- ""
	}()
	switch message := <-failure; message {
	case "":
	case "skip":
		t.Skip("the test thread already had a COM apartment")
	default:
		t.Fatal(message)
	}
}

// TestCheckShellNameFindsAFolderTheShellMisnames pins the picker's check. The
// shell reports a folder without the periods its name ends in, so each path
// below is what it gives for the folder in the comment.
func TestCheckShellNameFindsAFolderTheShellMisnames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{`v2..`, `Real.`, `both`, `both..`, `Mid.`, `Mid.\Sub`, `plain`, `plain.d`, `Shots `, `x .`, `dots`, `dots\...`, `dots\beside`} {
		if err := os.Mkdir(`\\?\`+root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		path string
		want error
	}{
		{root + `\v2`, errShellShortenedName},      // v2.., with no v2 beside it
		{root + `\Real`, errShellShortenedName},    // Real.
		{root + `\both`, errShellShortenedName},    // both.. or both, which cannot be told apart
		{root + `\Mid\Sub`, errShellShortenedName}, // Sub, inside Mid.
		{root + `\x `, errShellShortenedName},      // x .
		{root + `\dots\`, errShellShortenedName},   // ..., which it gives as its parent
		{root + `\plain`, nil},                     // a sibling with an extension is another name
		{root + `\plain.d`, nil},
		{root + `\Shots `, nil}, // reported whole; the output rules refuse it
		{root + `\dots`, nil},   // the parent itself, reported without a separator
		{root + `\dots\beside`, nil},
		{root, nil},
		{filepath.VolumeName(root) + `\`, nil},
		{root + `\deleted`, errChosenFolderGone}, // no name in the path ends in a period
		{root + `\deleted\inner`, errChosenFolderGone},
	} {
		if got := checkShellName(test.path); !errors.Is(got, test.want) {
			t.Errorf("checkShellName(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

// TestCheckShellNameIgnoresAFileWithPeriods pins that a file whose name is a
// folder's plus periods does not refuse that folder: the picker returns only
// folders, so such a file cannot be what the shell shortened.
func TestCheckShellNameIgnoresAFileWithPeriods(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{`plain`, `parent`, `parent\child`} {
		if err := os.Mkdir(`\\?\`+root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{`plain.`, `parent..`, `parent\child.`} {
		if err := os.WriteFile(`\\?\`+root+`\`+name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{root + `\plain`, root + `\parent`, root + `\parent\child`} {
		if got := checkShellName(path); got != nil {
			t.Errorf("checkShellName(%q) = %v, want none", path, got)
		}
	}
}

// TestHasPeriodSiblingFindsAFolderNamedOnlyPeriods pins what a drive or share
// root is checked for, as the shell reports a folder named only periods there
// as the root itself: such a folder counts, and neither a file of that name
// nor the . and .. entries of a directory do.
func TestHasPeriodSiblingFindsAFolderNamedOnlyPeriods(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{`holds`, `holds\...`, `file`, `empty`, `other`, `other\.config`} {
		if err := os.Mkdir(`\\?\`+root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(`\\?\`+root+`\file\...`, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for folder, want := range map[string]bool{`holds`: true, `file`: false, `empty`: false, `other`: false} {
		if got := hasPeriodSibling(`\\?\`+root+`\`+folder, ""); got != want {
			t.Errorf("hasPeriodSibling(%q, \"\") = %v, want %v", folder, got, want)
		}
	}
}

// TestShortenedNameInChecksARootAndEveryName pins which folders are asked
// about: a root for a folder of periods, and otherwise each name's parent.
func TestShortenedNameInChecksARootAndEveryName(t *testing.T) {
	for _, rest := range []string{``, `\`} {
		asked := ""
		shortenedNameIn(`\\?\D:`, rest, func(parent, name string) bool {
			asked = parent + "|" + name
			return false
		})
		if asked != `\\?\D:|` {
			t.Errorf("for the rest %q the root check asked %q", rest, asked)
		}
	}
	var mu sync.Mutex
	asked := map[string]bool{}
	got := shortenedNameIn(`\\?\D:`, `\a\b\c`, func(parent, name string) bool {
		mu.Lock()
		defer mu.Unlock()
		asked[parent+"|"+name] = true
		return name == "b"
	})
	if !got {
		t.Error("a sibling of the middle name was not reported")
	}
	for _, want := range []string{`\\?\D:|a`, `\\?\D:\a|b`, `\\?\D:\a\b|c`} {
		if !asked[want] {
			t.Errorf("%q was not asked; asked %v", want, asked)
		}
	}
}

// TestShortenedNameInAsksAboutNamesTogether pins that a deep path's folders
// are queried at once: each query here waits for all the others to start, so
// asking one after another would never finish.
func TestShortenedNameInAsksAboutNamesTogether(t *testing.T) {
	var started sync.WaitGroup
	started.Add(shellNameQueries)
	finished := make(chan bool, 1)
	go func() {
		finished <- shortenedNameIn(`\\?\D:`, strings.Repeat(`\n`, shellNameQueries), func(string, string) bool {
			started.Done()
			started.Wait()
			return false
		})
	}()
	select {
	case got := <-finished:
		if got {
			t.Error("no name has a sibling here")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the names were not queried together")
	}
}

// TestCheckShellNameReadsAVolumeGUIDPath pins that a path the shell gives
// through a volume GUID is checked as it is written, rather than rewritten as
// a server's, which no name in it could then be found under.
func TestCheckShellNameReadsAVolumeGUIDPath(t *testing.T) {
	root := t.TempDir()
	mount, err := windows.UTF16PtrFromString(filepath.VolumeName(root) + `\`)
	if err != nil {
		t.Fatal(err)
	}
	guid := make([]uint16, windows.MAX_PATH)
	if err := windows.GetVolumeNameForVolumeMountPoint(mount, &guid[0], uint32(len(guid))); err != nil {
		t.Skipf("the volume has no GUID name: %v", err)
	}
	volume := strings.TrimSuffix(windows.UTF16ToString(guid), `\`)
	if err := os.Mkdir(`\\?\`+root+`\v2..`, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root+`\plain`, 0o755); err != nil {
		t.Fatal(err)
	}
	below := root[len(filepath.VolumeName(root)):]
	if got := checkShellName(volume + below + `\v2`); !errors.Is(got, errShellShortenedName) {
		t.Errorf("a shortened name through a volume GUID = %v, want %v", got, errShellShortenedName)
	}
	if got := checkShellName(volume + below + `\plain`); got != nil {
		t.Errorf("an ordinary folder through a volume GUID = %v, want none", got)
	}
}

// TestAwaitShellNameCheckDoesNotWaitForAStalledDrive pins that the GUI thread
// is released when a drive does not answer, and that the choice is refused
// rather than stored unconfirmed.
func TestAwaitShellNameCheckDoesNotWaitForAStalledDrive(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stalled := func(string) error {
		<-release
		return nil
	}
	if err := awaitShellNameCheck(`C:\Shots`, stalled, 10*time.Millisecond); !errors.Is(err, errFolderNotResponding) {
		t.Errorf("a stalled check = %v, want %v", err, errFolderNotResponding)
	}
	refused := func(string) error { return errShellShortenedName }
	if err := awaitShellNameCheck(`C:\Shots`, refused, time.Minute); !errors.Is(err, errShellShortenedName) {
		t.Errorf("a finished check = %v, want its own result", err)
	}
	accepted := func(string) error { return nil }
	if err := awaitShellNameCheck(`C:\Shots`, accepted, time.Minute); err != nil {
		t.Errorf("a passing check = %v, want none", err)
	}
}

// TestRevealRefusesAFileThatHasGone pins that a saved file since moved or
// deleted is reported rather than handed to Explorer, which would open some
// other folder and report success. Neither case starts Explorer.
func TestRevealRefusesAFileThatHasGone(t *testing.T) {
	folder := t.TempDir()
	for _, path := range []string{filepath.Join(folder, "gone.png"), filepath.Join(folder, "gone", "a.png"), folder} {
		if err := RevealInFolder(path); !errors.Is(err, errSavedFileGone) {
			t.Errorf("RevealInFolder(%q) = %v, want %v", path, err, errSavedFileGone)
		}
	}
}

// TestSavedFileGoneDoesNotBlameAnOfflineServer pins that a server that cannot
// be reached is not reported as the file having been moved or deleted, though
// Go counts its error as fs.ErrNotExist: the file may be intact there.
func TestSavedFileGoneDoesNotBlameAnOfflineServer(t *testing.T) {
	failure := func(code windows.Errno) error {
		return &fs.PathError{Op: "CreateFile", Path: `\\nas\art\a.png`, Err: code}
	}
	if !errors.Is(failure(windows.ERROR_BAD_NETPATH), fs.ErrNotExist) {
		t.Fatal("Go no longer counts ERROR_BAD_NETPATH as fs.ErrNotExist; this test no longer pins anything")
	}
	for _, test := range []struct {
		code windows.Errno
		want bool
	}{
		{windows.ERROR_FILE_NOT_FOUND, true},
		{windows.ERROR_PATH_NOT_FOUND, true},
		{windows.ERROR_BAD_NETPATH, false},
		{windows.ERROR_BAD_NET_NAME, false},
		{windows.ERROR_ACCESS_DENIED, false},
	} {
		if got := savedFileGone(nil, failure(test.code)); got != test.want {
			t.Errorf("savedFileGone(%v) = %v, want %v", test.code, got, test.want)
		}
	}
}

// TestSavedFileFailureDoesNotBlameAMissingDrive pins that a path not found on
// a drive that is not there is reported as the drive, as the file may be
// intact on it, while a path not found on a drive that answers is a file that
// has gone. The volume's root is asked through a stand-in, so no drive or
// server is touched.
func TestSavedFileFailureDoesNotBlameAMissingDrive(t *testing.T) {
	notFound := func(code windows.Errno) error {
		return &fs.PathError{Op: "CreateFile", Path: `Z:\Shots\a.png`, Err: code}
	}
	missing := func(string) (fs.FileInfo, error) { return nil, notFound(windows.ERROR_PATH_NOT_FOUND) }
	present := func(string) (fs.FileInfo, error) { return nil, nil }
	for _, test := range []struct {
		code windows.Errno
		stat func(string) (fs.FileInfo, error)
		want error
	}{
		{windows.ERROR_PATH_NOT_FOUND, missing, errSavedDriveMissing},
		{windows.ERROR_PATH_NOT_FOUND, present, errSavedFileGone},
		{windows.ERROR_FILE_NOT_FOUND, present, errSavedFileGone},
	} {
		if got := savedFileFailure(`Z:\Shots\a.png`, nil, notFound(test.code), test.stat); !errors.Is(got, test.want) {
			t.Errorf("savedFileFailure(%v) = %v, want %v", test.code, got, test.want)
		}
	}
	for target, root := range map[string]string{`Z:\Shots\a.png`: `Z:\`, `\\nas\art\Shots\a.png`: `\\nas\art\`} {
		asked := ""
		savedFileFailure(target, nil, notFound(windows.ERROR_PATH_NOT_FOUND), func(path string) (fs.FileInfo, error) {
			asked = path
			return nil, nil
		})
		if asked != root {
			t.Errorf("for %q the root asked was %q, want %q", target, asked, root)
		}
	}
	denied := notFound(windows.ERROR_ACCESS_DENIED)
	if got := savedFileFailure(`Z:\Shots\a.png`, nil, denied, present); !errors.Is(got, denied) {
		t.Errorf("another failure = %v, want it reported as it is", got)
	}
}

// TestExplorerPathIsTheWindowsFoldersExplorer pins that RevealInFolder runs
// Explorer by its full path in the Windows folder, so no explorer.exe in the
// application's or the current folder can be run in its place.
func TestExplorerPathIsTheWindowsFoldersExplorer(t *testing.T) {
	path, err := explorerPath()
	if err != nil {
		t.Fatal(err)
	}
	folder, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Dir(path), filepath.Clean(folder)) || !strings.EqualFold(filepath.Base(path), "explorer.exe") {
		t.Fatalf("explorerPath = %q, want explorer.exe in %q", path, folder)
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		t.Fatalf("explorerPath %q is not a file: %v", path, err)
	}
}

// TestShellPathIsWhatTheShellReads pins that File Explorer is given a file or
// folder by a spelling its parser reads as that same place: the ordinary one
// where there is one, the \\?\ one for a volume GUID, and an error for a path
// it would show as another place.
func TestShellPathIsWhatTheShellReads(t *testing.T) {
	const volume = `\\?\Volume{00000000-0000-0000-0000-000000000000}\a.png`
	for _, test := range []struct{ path, want string }{
		{`C:\Shots\a.png`, `C:\Shots\a.png`},
		{`C:/Shots/a.png`, `C:\Shots\a.png`},
		{`C:\Shots.\a.png`, `C:\Shots\a.png`},
		{`C:\`, `C:\`},
		{`\\server\share\a.png`, `\\server\share\a.png`},
		{`\\?\C:\Shots\a.png`, `C:\Shots\a.png`},
		{`\??\C:\Shots\a.png`, `C:\Shots\a.png`},
		{`\\.\C:\Shots\a.png`, `C:\Shots\a.png`},
		{`\\?\C:\`, `C:\`},
		{`\\?\UNC\server\share\a.png`, `\\server\share\a.png`},
		{`\??\unc\server\share\a.png`, `\\server\share\a.png`},
		{`\\.\UNC\server\share\a.png`, `\\server\share\a.png`},
		{`\\nas.example.com.\share\a.png`, `\\nas.example.com.\share\a.png`},
		{`\\?\UNC\nas.example.com.\share\a.png`, `\\nas.example.com.\share\a.png`},
		{volume, volume},
		{`\??\Volume{00000000-0000-0000-0000-000000000000}\a.png`, volume},
		{`\\.\Volume{00000000-0000-0000-0000-000000000000}\a.png`, volume},
		{`C:\t\COM10\a.png`, `C:\t\COM10\a.png`},
		{`C:\t\con.d\a.png`, `C:\t\con.d\a.png`},
		{`\\aux\share\a.png`, `\\aux\share\a.png`},
	} {
		if got, err := shellPath(test.path); err != nil || got != test.want {
			t.Errorf("shellPath(%q) = %q, %v, want %q", test.path, got, err, test.want)
		}
	}
	for _, path := range []string{
		`C:\t\v2..\a.png`,
		`C:\t\Photos \a.png`,
		`\\?\C:\t\v2..\a.png`,
		`\\?\C:\t\Photos \a.png`,
		`\\?\C:\Shots.\a.png`,
		`\\?\C:\Shots\a.png.`,
		`\\?\C:\Shots\NUL`,
		`\\?\UNC\server\share\v2..\a.png`,
		`\\server\share.\a.png`,
		`\\nas.example.com..\share\a.png`,
		`\\?\UNC\nas.example.com \share\a.png`,
		`\\?\UNC\.\share\a.png`,
		`\\?\UNC\..\share\a.png`,
		`\\?\Volume{00000000-0000-0000-0000-000000000000}\v2..\a.png`,
		`\\?\GLOBALROOT\Device\HarddiskVolume1\a.png`,
		`\\.\pipe\a`,
		`C:\t\COM1\a.png`,
		`C:\t\con\a.png`,
		`C:\t\CONOUT$\a.png`,
		`\\?\C:\t\AUX\a.png`,
		`\\server\share\LPT1\a.png`,
		`\\?\Volume{00000000-0000-0000-0000-000000000000}\COM1\a.png`,
	} {
		if got, err := shellPath(path); err == nil {
			t.Errorf("shellPath(%q) = %q, want an error", path, got)
		}
	}
	if err := RevealInFolder(volume); !errors.Is(err, errNoDriveOrShare) {
		t.Errorf("RevealInFolder through a volume GUID = %v, want %v", err, errNoDriveOrShare)
	}
	explorer, err := explorerPath()
	if err != nil {
		t.Fatal(err)
	}
	target, err := shellPath(`\\?\` + explorer)
	if err != nil {
		t.Fatal(err)
	}
	parse := shell32.NewProc("SHParseDisplayName")
	var list uintptr
	result, _, _ := parse.Call(uintptr(unsafe.Pointer(utf16(target))), 0, uintptr(unsafe.Pointer(&list)), 0, 0)
	if result != 0 || list == 0 {
		t.Fatalf("the shell could not parse the target for %q: HRESULT %#x", explorer, uint32(result))
	}
	procCoTaskMemFree.Call(list)
}

// TestShellExecuteInfoMatchesTheWindowsLayout pins shellExecuteInfo to
// SHELLEXECUTEINFOW's layout: ShellExecuteExW refuses a call whose cbSize it
// does not know, and reads and writes its fields at their C offsets.
func TestShellExecuteInfoMatchesTheWindowsLayout(t *testing.T) {
	size, instance, process := uintptr(112), uintptr(56), uintptr(104)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		size, instance, process = 60, 32, 56
	}
	var info shellExecuteInfo
	if got := unsafe.Sizeof(info); got != size {
		t.Fatalf("size = %d, want %d", got, size)
	}
	if got := unsafe.Offsetof(info.hInstApp); got != instance {
		t.Fatalf("hInstApp offset = %d, want %d", got, instance)
	}
	if got := unsafe.Offsetof(info.hProcess); got != process {
		t.Fatalf("hProcess offset = %d, want %d", got, process)
	}
}

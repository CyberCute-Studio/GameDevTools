//go:build windows

package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

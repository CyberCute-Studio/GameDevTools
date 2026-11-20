//go:build windows

package ui

import (
	"runtime"
	"testing"
	"unsafe"
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

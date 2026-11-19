//go:build windows

package ui

import (
	"runtime"
	"testing"
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

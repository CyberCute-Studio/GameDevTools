//go:build windows

package app

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestAcquireInstanceDetectsExisting covers the APP-005 mechanism: the
// first acquisition owns the named mutex, a second process would observe it
// as already held, and releasing the handle frees it again.
func TestAcquireInstanceDetectsExisting(t *testing.T) {
	name := fmt.Sprintf(`Local\GameDevTools.TestInstance.%d`, time.Now().UnixNano())

	first, already, err := acquireInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if already {
		t.Fatal("first acquisition reported an existing instance")
	}

	second, already, err := acquireInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if !already {
		t.Fatal("second acquisition did not detect the running instance")
	}
	procCloseHandle.Call(second)

	procCloseHandle.Call(first)
	third, already, err := acquireInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if already {
		t.Fatal("acquisition after release still reported an existing instance")
	}
	procCloseHandle.Call(third)
}

// TestClaimInstanceWaitsForAClosingInstance covers a launch during the
// running instance's shutdown, which holds the mutex after its window is
// gone while a module's background work stops: the claim takes the mutex
// once that instance exits rather than exiting without showing anything,
// and gives up as activated, never running beside it, once the wait is
// over.
func TestClaimInstanceWaitsForAClosingInstance(t *testing.T) {
	name := fmt.Sprintf(`Local\GameDevTools.TestInstance.%d`, time.Now().UnixNano())
	const noWindow = "GameDevToolsNoSuchWindowClass"
	closing, already, err := acquireInstance(name)
	if err != nil || already {
		t.Fatalf("acquireInstance() = %v, %v", already, err)
	}
	// The closing instance exits once, by the goroutine below or, should the
	// test stop first, by the cleanup.
	exit := sync.OnceFunc(func() { procCloseHandle.Call(closing) })
	t.Cleanup(exit)

	if handle, activated, err := claimInstance(name, noWindow, 100*time.Millisecond); err != nil || !activated || handle != 0 {
		t.Fatalf("claim while held = (%d, %v, %v), want to give up as activated", handle, activated, err)
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		exit()
	}()
	handle, activated, err := claimInstance(name, noWindow, 5*time.Second)
	if err != nil || activated || handle == 0 {
		t.Fatalf("claim after the instance exited = (%d, %v, %v), want the mutex", handle, activated, err)
	}
	defer procCloseHandle.Call(handle)
	other, already, err := acquireInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	procCloseHandle.Call(other)
	if !already {
		t.Fatal("the claimed mutex was not held")
	}
}

// TestActivateExistingInstanceRestoresWindow covers the APP-005 activation:
// a second launch restores the running instance's minimized window. A
// missing window reports false instead of failing.
func TestActivateExistingInstanceRestoresWindow(t *testing.T) {
	// The window belongs to the thread that creates it, and this thread pumps
	// no messages, so ShowWindow from any other thread would wait forever.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if activateExistingInstance("GameDevToolsNoSuchWindowClass", 0) {
		t.Fatal("activation reported a window that does not exist")
	}

	className, hwnd := newInstanceTestWindow(t)
	procShowWindow.Call(hwnd, swMinimizeInstanceTest)
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic == 0 {
		t.Fatal("test window did not minimize")
	}
	if !activateExistingInstance(className, time.Second) {
		t.Fatal("activation did not find the existing window")
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		t.Fatal("activation left the existing window minimized")
	}
}

// TestActivateExistingInstanceLeavesACaptureHiddenWindow covers a launch
// during a capture started while the window was minimized: hiding keeps the
// window iconic, and restoring it would put it under the overlay and in the
// image (SCR-044).
func TestActivateExistingInstanceLeavesACaptureHiddenWindow(t *testing.T) {
	runtime.LockOSThread()
	// The windows' cleanups run on this thread, which owns them.
	t.Cleanup(runtime.UnlockOSThread)
	className, hwnd := newInstanceTestWindow(t)
	procShowWindow.Call(hwnd, swMinimizeInstanceTest)
	procShowWindow.Call(hwnd, swHide)
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic == 0 {
		t.Fatal("hiding the test window ended its minimized state")
	}
	if !activateExistingInstance(className, time.Second) {
		t.Fatal("activation did not find the existing window")
	}
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible != 0 {
		t.Fatal("activation showed a window the capture had hidden")
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic == 0 {
		t.Fatal("activation changed the hidden window's minimized state")
	}
}

// TestActivateExistingInstanceWaitsForAWindowStillStarting is a launch while
// the first one's window exists but has not been shown: it is brought forward
// once it appears, where leaving it would let it open behind other windows.
func TestActivateExistingInstanceWaitsForAWindowStillStarting(t *testing.T) {
	runtime.LockOSThread()
	// The windows' cleanups run on this thread, which owns them.
	t.Cleanup(runtime.UnlockOSThread)
	className, hwnd := newInstanceTestWindow(t)
	const swShowNAInstanceTest = 8
	offScreen := int32(-22000)
	procSetWindowPos.Call(hwnd, 0, uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 50, 50, swpNoZOrder|swpNoActivate)
	// The window's own thread shows it, as it does not pump messages for a
	// ShowWindow from another; the second launch runs beside it.
	started := time.Now()
	done := make(chan time.Duration, 1)
	go func() {
		activateExistingInstance(className, 0)
		done <- time.Since(started)
	}()
	// Longer than a quick start, as antivirus scanning can make a first launch.
	time.Sleep(1500 * time.Millisecond)
	procShowWindow.Call(hwnd, swShowNAInstanceTest)
	select {
	case elapsed := <-done:
		if elapsed >= instanceShowWait {
			t.Fatalf("activation took %v, so it did not see the window appear", elapsed)
		}
	case <-time.After(instanceShowWait + 5*time.Second):
		t.Fatal("activation did not return")
	}
}

// TestActivateExistingInstanceSeesAWindowDestroyed is a launch that found the
// window of an instance now closing: it must report no window, so that
// claimInstance takes the mutex once that instance exits, rather than exit
// with nothing left running.
func TestActivateExistingInstanceSeesAWindowDestroyed(t *testing.T) {
	runtime.LockOSThread()
	// The windows' cleanups run on this thread, which owns them.
	t.Cleanup(runtime.UnlockOSThread)
	className, hwnd := newInstanceTestWindow(t)
	done := make(chan bool, 1)
	go func() { done <- activateExistingInstance(className, 0) }()
	time.Sleep(200 * time.Millisecond)
	procDestroyWindow.Call(hwnd)
	select {
	case activated := <-done:
		if activated {
			t.Fatal("activation reported a window that was destroyed")
		}
	case <-time.After(instanceShowWait - time.Second):
		t.Fatal("activation kept waiting for a destroyed window")
	}
}

const swMinimizeInstanceTest = 6

// newInstanceTestWindow creates a hidden top-level window of a unique class on
// the calling thread, which must stay locked to it and need not pump messages.
func newInstanceTestWindow(t *testing.T) (className string, hwnd uintptr) {
	t.Helper()
	className = fmt.Sprintf("GameDevToolsInstanceTest%d", time.Now().UnixNano())
	instance, _, err := procGetModuleHandleW.Call(0)
	if instance == 0 {
		t.Fatalf("GetModuleHandleW: %v", err)
	}
	class := wndClassEx{
		LpfnWndProc:   windows.NewCallback(defWindowProcCallback),
		HInstance:     windows.Handle(instance),
		LpszClassName: utf16(className),
	}
	class.CbSize = uint32(unsafe.Sizeof(class))
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		t.Fatalf("RegisterClassExW: %v", err)
	}
	hwnd, _, err = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(className))),
		uintptr(unsafe.Pointer(utf16("instance activation test"))),
		wsOverlappedWindow,
		0, 0, 320, 240,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(hwnd) })
	return className, hwnd
}

// TestActivateExistingInstanceWaitsForStartupWindow covers the launch race:
// the running instance takes the mutex before its window exists, so a second
// launch landing in that startup gap waits for the window instead of exiting
// without showing anything.
func TestActivateExistingInstanceWaitsForStartupWindow(t *testing.T) {
	className := fmt.Sprintf("GameDevToolsInstanceTest%d", time.Now().UnixNano())
	created := make(chan uintptr, 1)
	done := make(chan struct{})
	destroyed := make(chan struct{})
	go func() {
		defer close(destroyed)
		// Only the thread that creates a window can destroy it.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		time.Sleep(100 * time.Millisecond)
		instance, _, err := procGetModuleHandleW.Call(0)
		if instance == 0 {
			t.Errorf("GetModuleHandleW: %v", err)
			created <- 0
			return
		}
		class := wndClassEx{
			LpfnWndProc:   windows.NewCallback(defWindowProcCallback),
			HInstance:     windows.Handle(instance),
			LpszClassName: utf16(className),
		}
		class.CbSize = uint32(unsafe.Sizeof(class))
		if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
			t.Errorf("RegisterClassExW: %v", err)
			created <- 0
			return
		}
		hwnd, _, err := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(utf16(className))),
			uintptr(unsafe.Pointer(utf16("instance activation test"))),
			wsOverlappedWindow,
			0, 0, 320, 240,
			0, 0, 0, 0,
		)
		if hwnd == 0 {
			t.Errorf("CreateWindowExW: %v", err)
		}
		created <- hwnd
		<-done
		procDestroyWindow.Call(hwnd)
	}()
	if !activateExistingInstance(className, 5*time.Second) {
		t.Fatal("activation gave up while the existing instance was still starting")
	}
	if hwnd := <-created; hwnd == 0 {
		t.Fatal("test window was not created")
	}
	close(done)
	<-destroyed
}

func defWindowProcCallback(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

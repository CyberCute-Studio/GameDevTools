//go:build windows

package app

import (
	"fmt"
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

// TestActivateExistingInstanceRestoresWindow covers the APP-005 activation:
// a second launch restores the running instance's minimized window. A
// missing window reports false instead of failing.
func TestActivateExistingInstanceRestoresWindow(t *testing.T) {
	if activateExistingInstance("GameDevToolsNoSuchWindowClass", 0) {
		t.Fatal("activation reported a window that does not exist")
	}

	className := fmt.Sprintf("GameDevToolsInstanceTest%d", time.Now().UnixNano())
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
	hwnd, _, err := procCreateWindowExW.Call(
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

	const swMinimize = 6
	procShowWindow.Call(hwnd, swMinimize)
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

func defWindowProcCallback(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
	return r
}

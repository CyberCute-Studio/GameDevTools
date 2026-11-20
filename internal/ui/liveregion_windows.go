//go:build windows

package ui

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// eventObjectLiveRegionChanged is EVENT_OBJECT_LIVEREGIONCHANGED, which
	// tells UI Automation clients that a live region's content changed.
	eventObjectLiveRegionChanged = 0x8019
	// objidClient and childidSelf name a window's client object itself, as
	// NotifyWinEvent and the Dynamic Annotation calls take them.
	objidClient = 0xfffffffc
	childidSelf = 0

	clsctxInprocServer = 0x1
	vtI4               = 3
	// liveSettingPolite is the UI Automation LiveSetting value Polite: the
	// change is announced once the screen reader finishes what it is
	// reading, rather than interrupting it.
	liveSettingPolite = 1

	// accPropServicesSetHwndProp is the vtable slot of
	// IAccPropServices::SetHwndProp, after IUnknown's three methods and
	// SetPropValue, SetPropServer, and ClearProps.
	accPropServicesSetHwndProp = 6
	unknownRelease             = 2
)

// guid is a COM GUID in its in-memory layout.
type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

// variant is a COM VARIANT on 64-bit Windows: the type tag, three reserved
// words, and a 16-byte value, 24 bytes in all.
type variant struct {
	vt       uint16
	reserved [3]uint16
	value    [2]uint64
}

// comObject is a COM interface pointer's target, whose first word points at
// the interface's table of methods.
type comObject struct {
	vtable *[16]uintptr
}

var (
	// oleacc is the DLL that keeps Dynamic Annotation properties, as markLive
	// describes.
	oleacc = windows.NewLazySystemDLL("oleacc.dll")

	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidAccPropServices = guid{0xb5f8350b, 0x0548, 0x48b1, [8]byte{0xa6, 0xee, 0x88, 0xbd, 0x00, 0xb4, 0xa5, 0xe7}}
	iidAccPropServices   = guid{0x6e26e776, 0x04f0, 0x495d, [8]byte{0x80, 0xe4, 0x33, 0x30, 0x35, 0x2e, 0x31, 0x69}}
	// liveSettingProperty is LiveSetting_Property_GUID, the UI Automation
	// LiveSetting property as Dynamic Annotation sets it.
	liveSettingProperty = guid{0xc12bcd8e, 0x2a8e, 0x4950, [8]byte{0x8a, 0xe7, 0x36, 0x25, 0x11, 0x1d, 0x58, 0xeb}}
)

// markLive makes the window a polite UI Automation live region through
// Dynamic Annotation (IAccPropServices::SetHwndProp), the documented way to
// give a standard Win32 control a UI Automation property it does not expose
// itself; the system's proxy for the control then reports it to every screen
// reader that reads the control. The annotation is kept in oleacc.dll, which
// implements the service: COM would unload the DLL with the apartment
// initialized for the call, and the annotation with it, so the DLL is held
// loaded for the life of the process first.
func markLive(hwnd windows.Handle) error {
	if err := oleacc.Load(); err != nil {
		return err
	}
	// The apartment belongs to the thread, so the calls stay on one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer initializeCOM()()
	var services *comObject
	result, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidAccPropServices)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidAccPropServices)), uintptr(unsafe.Pointer(&services)),
	)
	if int32(result) < 0 || services == nil {
		return fmt.Errorf("CoCreateInstance(AccPropServices): HRESULT %#x", uint32(result))
	}
	defer syscall.SyscallN(services.vtable[unknownRelease], uintptr(unsafe.Pointer(services)))
	value := variant{vt: vtI4}
	value.value[0] = liveSettingPolite
	if result := setHwndProp(services, uintptr(hwnd), &liveSettingProperty, &value); int32(result) < 0 {
		return fmt.Errorf("IAccPropServices.SetHwndProp(LiveSetting): HRESULT %#x", uint32(result))
	}
	return nil
}

// announceLiveRegion raises the live-region change event for the window, so
// a screen reader reads its new content.
func announceLiveRegion(hwnd windows.Handle) {
	procNotifyWinEvent.Call(eventObjectLiveRegionChanged, uintptr(hwnd), objidClient, childidSelf)
}

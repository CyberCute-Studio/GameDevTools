//go:build windows

package uiatest

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	namePropertyID        = 30005 // UIA_NamePropertyId
	liveSettingPropertyID = 30135 // UIA_LiveSettingPropertyId

	vtEmpty = 0
	vtI4    = 3
	vtBSTR  = 8

	coinitMultithreaded = 0x0
	clsctxInprocServer  = 0x1

	unknownRelease              = 2
	automationElementFromHandle = 6
	elementCurrentPropertyValue = 10

	eventObjectLiveRegionChanged = 0x8019
	wineventOutOfContext         = 0x0000
	pmRemove                     = 0x0001

	queryTimeout = 10 * time.Second
)

type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

// variant is a COM VARIANT on 64-bit Windows; the value's first word is
// read as a BSTR for VT_BSTR and as a 32-bit integer for VT_I4. It is held
// as an integer, not a pointer: a VT_I4 such as Polite (1) in a pointer
// field is an invalid pointer to the Go runtime, which throws when it copies
// a stack that holds one.
type variant struct {
	vt       uint16
	reserved [3]uint16
	value    uintptr
	extra    uintptr
}

type comObject struct {
	vtable *[16]uintptr
}

type message struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
	private uint32
}

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	oleaut32 = windows.NewLazySystemDLL("oleaut32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procVariantClear     = oleaut32.NewProc("VariantClear")
	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procSetWinEventHook  = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent   = user32.NewProc("UnhookWinEvent")

	clsidCUIAutomation = guid{0xff48dba4, 0x60ef, 0x4201, [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidIUIAutomation   = guid{0x30cbe57d, 0xd9d0, 0x452a, [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
)

// Names returns the UI Automation name of each window's element, as a
// screen reader announces the control when it gains the focus. Call it on
// the thread that owns the windows: the query runs on a thread of its own,
// as UI Automation clients must, while this one keeps dispatching its
// messages so the windows can answer.
func Names(hwnds ...windows.Handle) ([]string, error) {
	names := make([]string, len(hwnds))
	err := query(func(automation *comObject) error {
		for i, hwnd := range hwnds {
			value, err := property(automation, hwnd, namePropertyID)
			if err != nil {
				return err
			}
			switch value.vt {
			case vtBSTR:
				names[i] = windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&value.value)))
			case vtEmpty:
			default:
				vt := value.vt
				clearVariant(&value)
				return fmt.Errorf("the name of %#x has VARIANT type %d", hwnd, vt)
			}
			clearVariant(&value)
		}
		return nil
	})
	if err != nil {
		// A query that timed out may still be writing the names.
		return nil, err
	}
	return names, nil
}

// LiveSetting returns the UI Automation LiveSetting of the window's element:
// 0 for Off, 1 for Polite, 2 for Assertive. Call it as Names describes.
func LiveSetting(hwnd windows.Handle) (int32, error) {
	var setting int32
	err := query(func(automation *comObject) error {
		value, err := property(automation, hwnd, liveSettingPropertyID)
		if err != nil {
			return err
		}
		defer clearVariant(&value)
		if value.vt != vtI4 {
			return fmt.Errorf("the live setting of %#x has VARIANT type %d", hwnd, value.vt)
		}
		setting = *(*int32)(unsafe.Pointer(&value.value))
		return nil
	})
	if err != nil {
		// A query that timed out may still be writing the setting.
		return 0, err
	}
	return setting, nil
}

// query runs work with a UI Automation client on a thread of its own and
// dispatches the calling thread's messages until it returns. Once it
// returns an error, work may still be running, so a caller reads what work
// writes only after a nil error, which work's own completion sends.
func query(work func(*comObject) error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if result, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(result) < 0 {
			done <- fmt.Errorf("CoInitializeEx: HRESULT %#x", uint32(result))
			return
		}
		defer procCoUninitialize.Call()
		var automation *comObject
		result, _, _ := procCoCreateInstance.Call(
			uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, clsctxInprocServer,
			uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&automation)),
		)
		if int32(result) < 0 || automation == nil {
			done <- fmt.Errorf("CoCreateInstance(CUIAutomation): HRESULT %#x", uint32(result))
			return
		}
		defer release(automation)
		done <- work(automation)
	}()
	deadline := time.Now().Add(queryTimeout)
	for {
		select {
		case err := <-done:
			return err
		default:
		}
		if time.Now().After(deadline) {
			return errors.New("the UI Automation query did not finish")
		}
		pump()
		time.Sleep(time.Millisecond)
	}
}

func property(automation *comObject, hwnd windows.Handle, id int32) (variant, error) {
	var element *comObject
	result, _, _ := syscall.SyscallN(automation.vtable[automationElementFromHandle],
		uintptr(unsafe.Pointer(automation)), uintptr(hwnd), uintptr(unsafe.Pointer(&element)))
	if int32(result) < 0 || element == nil {
		return variant{}, fmt.Errorf("IUIAutomation.ElementFromHandle(%#x): HRESULT %#x", hwnd, uint32(result))
	}
	defer release(element)
	var value variant
	result, _, _ = syscall.SyscallN(element.vtable[elementCurrentPropertyValue],
		uintptr(unsafe.Pointer(element)), uintptr(id), uintptr(unsafe.Pointer(&value)))
	if int32(result) < 0 {
		return variant{}, fmt.Errorf("IUIAutomationElement.GetCurrentPropertyValue(%d) of %#x: HRESULT %#x", id, hwnd, uint32(result))
	}
	return value, nil
}

func release(object *comObject) {
	syscall.SyscallN(object.vtable[unknownRelease], uintptr(unsafe.Pointer(object)))
}

func clearVariant(value *variant) {
	procVariantClear.Call(uintptr(unsafe.Pointer(value)))
}

// pump dispatches every message waiting for the calling thread, which also
// delivers the messages other threads send it and its out-of-context
// accessibility events.
func pump() {
	var m message
	for {
		available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if available == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// Announcements counts the live-region announcements windows raise.
type Announcements struct {
	hook uintptr
}

var (
	announcementsMu sync.Mutex
	announced       = map[windows.Handle]int{}
	// announcementCallback is created once: the runtime never releases a
	// callback.
	announcementCallback = windows.NewCallback(func(_ uintptr, event uint32, hwnd windows.Handle, _, _ int32, _, _ uint32) uintptr {
		if event == eventObjectLiveRegionChanged {
			announcementsMu.Lock()
			announced[hwnd]++
			announcementsMu.Unlock()
		}
		return 0
	})
)

// WatchAnnouncements starts counting the live-region announcements
// (EVENT_OBJECT_LIVEREGIONCHANGED) the process raises. The events arrive
// through the calling thread's messages, so call it, Count, and Stop on the
// thread that owns the windows.
func WatchAnnouncements() (*Announcements, error) {
	hook, _, err := procSetWinEventHook.Call(eventObjectLiveRegionChanged, eventObjectLiveRegionChanged,
		0, announcementCallback, uintptr(windows.GetCurrentProcessId()), 0, wineventOutOfContext)
	if hook == 0 {
		return nil, fmt.Errorf("SetWinEventHook: %w", err)
	}
	announcementsMu.Lock()
	clearAnnounced()
	announcementsMu.Unlock()
	return &Announcements{hook: hook}, nil
}

// Count returns how many announcements the window raised since the watch
// started, after dispatching the events still on their way.
func (a *Announcements) Count(hwnd windows.Handle) int {
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		pump()
		time.Sleep(time.Millisecond)
	}
	announcementsMu.Lock()
	defer announcementsMu.Unlock()
	return announced[hwnd]
}

// Stop ends the watch.
func (a *Announcements) Stop() {
	procUnhookWinEvent.Call(a.hook)
	announcementsMu.Lock()
	clearAnnounced()
	announcementsMu.Unlock()
}

func clearAnnounced() {
	for hwnd := range announced {
		delete(announced, hwnd)
	}
}

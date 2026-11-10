package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type display struct {
	Name    string
	Bounds  rect
	Work    rect
	Primary bool
	DPIX    uint32
	DPIY    uint32
}

func enumerateDisplays() ([]display, error) {
	var displays []display
	callback := windows.NewCallback(func(monitor windows.Handle, _ windows.Handle, bounds *rect, _ uintptr) uintptr {
		info := monitorInfoEx{CbSize: uint32(unsafe.Sizeof(monitorInfoEx{}))}
		r, _, _ := procGetMonitorInfoW.Call(uintptr(monitor), uintptr(unsafe.Pointer(&info)))
		if r == 0 {
			return 1
		}
		item := display{
			Name:    windows.UTF16ToString(info.Device[:]),
			Bounds:  info.Monitor,
			Work:    info.Work,
			Primary: info.Flags&1 != 0,
		}
		hr, _, _ := procGetDpiForMonitor.Call(uintptr(monitor), mdtEffectiveDPI, uintptr(unsafe.Pointer(&item.DPIX)), uintptr(unsafe.Pointer(&item.DPIY)))
		if int32(hr) < 0 {
			item.DPIX = 96
			item.DPIY = 96
		}
		displays = append(displays, item)
		return 1
	})
	r, _, err := procEnumDisplayMonitors.Call(0, 0, callback, 0)
	if r == 0 {
		return nil, callError("EnumDisplayMonitors", err)
	}
	if len(displays) == 0 {
		return nil, fmt.Errorf("no connected displays found")
	}
	return displays, nil
}

func primaryDisplay(displays []display) display {
	for _, item := range displays {
		if item.Primary {
			return item
		}
	}
	return displays[0]
}

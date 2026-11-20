package ui

import (
	"syscall"
	"unsafe"
)

// setHwndProp calls IAccPropServices::SetHwndProp for the window's client
// object. The method takes the property GUID and the VARIANT by value; the
// ARM64 calling convention passes a composite of up to sixteen bytes, such
// as the GUID, in two consecutive general registers, and a larger one, such
// as the 24-byte VARIANT, as a pointer to a copy.
func setHwndProp(services *comObject, hwnd uintptr, property *guid, value *variant) uintptr {
	halves := (*[2]uintptr)(unsafe.Pointer(property))
	result, _, _ := syscall.SyscallN(services.vtable[accPropServicesSetHwndProp],
		uintptr(unsafe.Pointer(services)), hwnd, objidClient, childidSelf,
		halves[0], halves[1], uintptr(unsafe.Pointer(value)))
	return result
}

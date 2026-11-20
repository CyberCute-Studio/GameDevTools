package ui

import (
	"syscall"
	"unsafe"
)

// setHwndProp calls IAccPropServices::SetHwndProp for the window's client
// object. The method takes the property GUID and the VARIANT by value; the
// x64 calling convention passes an argument larger than eight bytes as a
// pointer to a copy, so both go as pointers.
func setHwndProp(services *comObject, hwnd uintptr, property *guid, value *variant) uintptr {
	result, _, _ := syscall.SyscallN(services.vtable[accPropServicesSetHwndProp],
		uintptr(unsafe.Pointer(services)), hwnd, objidClient, childidSelf,
		uintptr(unsafe.Pointer(property)), uintptr(unsafe.Pointer(value)))
	return result
}

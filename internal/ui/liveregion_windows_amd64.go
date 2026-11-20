package ui

import (
	"syscall"
	"unsafe"
)

// setHwndProp calls IAccPropServices::SetHwndProp for the window's client
// object. The method takes the property GUID and the VARIANT by value; the
// x64 calling convention passes an argument larger than eight bytes as a
// pointer to a copy the caller makes, 16-byte aligned, which the callee owns
// and may change, so both go as pointers to such copies rather than to the
// caller's values.
func setHwndProp(services *comObject, hwnd uintptr, property *guid, value *variant) uintptr {
	var scratch [64]byte
	base := unsafe.Pointer(&scratch[0])
	aligned := unsafe.Add(base, (16-uintptr(base)%16)%16)
	propertyCopy := (*guid)(aligned)
	valueCopy := (*variant)(unsafe.Add(aligned, 16))
	*propertyCopy, *valueCopy = *property, *value
	result, _, _ := syscall.SyscallN(services.vtable[accPropServicesSetHwndProp],
		uintptr(unsafe.Pointer(services)), hwnd, objidClient, childidSelf,
		uintptr(unsafe.Pointer(propertyCopy)), uintptr(unsafe.Pointer(valueCopy)))
	return result
}

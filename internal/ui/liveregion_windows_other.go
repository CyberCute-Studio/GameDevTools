//go:build windows && !amd64 && !arm64

package ui

// eNotImpl is E_NOTIMPL.
const eNotImpl = 0x80004001

// setHwndProp reports E_NOTIMPL on the 32-bit architectures, which the
// application does not support (NFR-004) and whose calling conventions pass
// the GUID and VARIANT differently, so labels there are not live regions.
func setHwndProp(*comObject, uintptr, *guid, *variant) uintptr {
	return eNotImpl
}

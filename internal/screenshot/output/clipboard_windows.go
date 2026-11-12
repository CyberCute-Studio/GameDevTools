//go:build windows

package output

import (
	"errors"
	"fmt"
	"image"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfDIB        = 8
	gmemMoveable = 0x0002

	clipboardOpenAttempts  = 5
	clipboardOpenRetryWait = 50 * time.Millisecond
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// setClipboardImage replaces the clipboard contents with a CF_DIB image,
// the device-independent bitmap format common Windows image destinations
// accept. The image is encoded as a bottom-up 32-bit BGRA bitmap with
// opaque alpha. The owner window may be zero; the system takes ownership
// of the clipboard data once SetClipboardData succeeds.
//
// OpenClipboard fails while another application holds the clipboard open,
// so the open is retried briefly before the attempt is reported as a
// KindClipboard failure. Retrying forever is not an option: SCR-060
// requires the save destination to proceed even when the clipboard stays
// unavailable.
func setClipboardImage(owner uintptr, img *image.RGBA) error {
	bounds := img.Bounds()
	width, height := int32(bounds.Dx()), int32(bounds.Dy())
	header := bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     width,
		Height:    height, // positive height stores rows bottom-up
		Planes:    1,
		BitCount:  32,
		SizeImage: uint32(width * height * 4),
	}
	allocationSize := unsafe.Sizeof(header) + uintptr(width*height*4)
	allocation, _, err := procGlobalAlloc.Call(gmemMoveable, allocationSize)
	if allocation == 0 {
		return kindError(KindClipboard, "GlobalAlloc", callError(err))
	}
	// SetClipboardData transfers ownership of the allocation to the
	// system; until then, this function must free it.
	ownedByClipboard := false
	defer func() {
		if !ownedByClipboard {
			procGlobalFree.Call(allocation)
		}
	}()
	destination, err := lockGlobalMemory(allocation)
	if err != nil {
		return err
	}
	procRtlMoveMemory.Call(destination, uintptr(unsafe.Pointer(&header)), unsafe.Sizeof(header))
	pixels := make([]byte, int(width*height*4))
	for y := int32(0); y < height; y++ {
		sourceRow := img.PixOffset(bounds.Min.X, bounds.Min.Y+int(height-1-y))
		destinationRow := int(y*width) * 4
		for x := 0; x < int(width); x++ {
			source := sourceRow + x*4
			target := destinationRow + x*4
			pixels[target+0] = img.Pix[source+2]
			pixels[target+1] = img.Pix[source+1]
			pixels[target+2] = img.Pix[source+0]
			pixels[target+3] = 255
		}
	}
	procRtlMoveMemory.Call(destination+unsafe.Sizeof(header), uintptr(unsafe.Pointer(&pixels[0])), uintptr(len(pixels)))
	procGlobalUnlock.Call(allocation)
	var opened bool
	for attempt := 0; attempt < clipboardOpenAttempts; attempt++ {
		r, _, _ := procOpenClipboard.Call(owner)
		if r != 0 {
			opened = true
			break
		}
		time.Sleep(clipboardOpenRetryWait)
	}
	if !opened {
		return kindError(KindClipboard, "OpenClipboard",
			errors.New("the clipboard remained in use by another application"))
	}
	defer procCloseClipboard.Call()
	r, _, err := procEmptyClipboard.Call()
	if r == 0 {
		return kindError(KindClipboard, "EmptyClipboard", callError(err))
	}
	r, _, err = procSetClipboardData.Call(cfDIB, allocation)
	if r == 0 {
		return kindError(KindClipboard, "SetClipboardData(CF_DIB)", callError(err))
	}
	ownedByClipboard = true
	return nil
}

func lockGlobalMemory(handle uintptr) (uintptr, error) {
	address, _, err := procGlobalLock.Call(handle)
	if address == 0 {
		return 0, kindError(KindClipboard, "GlobalLock", callError(err))
	}
	return address, nil
}

// clipboardImageSize reads back the dimensions of the CF_DIB image on the
// clipboard. It exists for tests that verify the round trip into a common
// Windows destination format.
func clipboardImageSize() (int32, int32, error) {
	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		return 0, 0, fmt.Errorf("OpenClipboard: %w", callError(err))
	}
	defer procCloseClipboard.Call()
	allocation, _, err := procGetClipboardData.Call(cfDIB, 0)
	if allocation == 0 {
		return 0, 0, fmt.Errorf("GetClipboardData(CF_DIB): %w", callError(err))
	}
	data, err := lockGlobalMemory(allocation)
	if err != nil {
		return 0, 0, err
	}
	defer procGlobalUnlock.Call(allocation)
	var header bitmapInfoHeader
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&header)), data, unsafe.Sizeof(header))
	if header.Size != uint32(unsafe.Sizeof(bitmapInfoHeader{})) || header.BitCount != 32 {
		return 0, 0, errors.New("unexpected clipboard DIB header")
	}
	return header.Width, header.Height, nil
}

// clipboardPixel reads back one pixel of the CF_DIB image on the clipboard
// as RGBA. It exists for tests that verify pixel content survives the
// round trip without JPEG degradation.
func clipboardPixel(x, y int) (r, g, b, a uint8, err error) {
	res, _, callErr := procOpenClipboard.Call(0)
	if res == 0 {
		return 0, 0, 0, 0, fmt.Errorf("OpenClipboard: %w", callError(callErr))
	}
	defer procCloseClipboard.Call()
	allocation, _, callErr := procGetClipboardData.Call(cfDIB, 0)
	if allocation == 0 {
		return 0, 0, 0, 0, fmt.Errorf("GetClipboardData(CF_DIB): %w", callError(callErr))
	}
	data, err := lockGlobalMemory(allocation)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer procGlobalUnlock.Call(allocation)
	var header bitmapInfoHeader
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&header)), data, unsafe.Sizeof(header))
	// Positive header height stores rows bottom-up.
	row := int(header.Height) - 1 - y
	address := data + unsafe.Sizeof(header) + uintptr(row*int(header.Width)+x)*4
	var value [4]byte
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&value[0])), address, 4)
	return value[2], value[1], value[0], value[3], nil
}

func callError(err error) error {
	if err == windows.ERROR_SUCCESS {
		return errors.New("the operation failed without an extended error")
	}
	return err
}

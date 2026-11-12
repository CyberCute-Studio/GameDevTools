//go:build windows

package output

import (
	"errors"
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
	width, height := bounds.Dx(), bounds.Dy()
	header := bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     int32(width),
		Height:    int32(height), // positive height stores rows bottom-up
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
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		sourceRow := img.PixOffset(bounds.Min.X, bounds.Min.Y+height-1-y)
		destinationRow := y * width * 4
		for x := 0; x < width; x++ {
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
		if attempt+1 < clipboardOpenAttempts {
			time.Sleep(clipboardOpenRetryWait)
		}
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

func callError(err error) error {
	if err == windows.ERROR_SUCCESS {
		return errors.New("the operation failed without an extended error")
	}
	return err
}

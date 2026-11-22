//go:build windows

package output

import (
	"errors"
	"image"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfDIB        = 8
	gmemMoveable = 0x0002
	gmemZeroInit = 0x0040

	// cloudUploadFormat is the registered clipboard format through which an
	// application tells Windows whether the clipboard's contents may be
	// synced to the user's other devices through the cloud: a DWORD, zero
	// for no. Leaving it off allows the upload when the user has turned
	// clipboard sync on.
	cloudUploadFormat = "CanUploadToCloudClipboard"

	clipboardOpenAttempts  = 5
	clipboardOpenRetryWait = 50 * time.Millisecond

	// hwndMessage is HWND_MESSAGE, -3, the parent of a message-only window.
	hwndMessage = ^uintptr(2)
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGlobalAlloc              = kernel32.NewProc("GlobalAlloc")
	procGlobalFree               = kernel32.NewProc("GlobalFree")
	procGlobalLock               = kernel32.NewProc("GlobalLock")
	procGlobalUnlock             = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory            = kernel32.NewProc("RtlMoveMemory")
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
// opaque alpha. The system takes ownership of the clipboard data once
// SetClipboardData succeeds.
//
// The clipboard is opened for a hidden message-only window that lives only
// for this call, because Windows documents that EmptyClipboard on a clipboard
// opened with no window leaves it with no owner, which makes SetClipboardData
// fail. The data stays on the clipboard after the window is destroyed.
//
// The image is marked as not for cloud upload, so Windows clipboard sync
// does not send it to the user's other devices (NFR-031), and stays in the
// local clipboard history (Win+V). An image that cannot be marked is not
// placed at all: the failure is reported as a clipboard failure, and the
// saved file is unaffected (SCR-060). The marker, the image, and the window
// are all prepared before the clipboard is opened, so a failure to prepare
// any of them leaves what the user had copied in place. Placing them has to
// follow EmptyClipboard, so a failure to place either leaves the clipboard
// empty, and what the user had copied is gone.
//
// OpenClipboard fails while another application holds the clipboard open,
// so the open is retried briefly before the attempt is reported as a
// KindClipboard failure. Retrying forever is not an option: SCR-060
// requires the save destination to proceed even when the clipboard stays
// unavailable.
//
// An open clipboard belongs to the OS thread that opened it: only that thread
// can change or close it, and until it does, no other application can copy or
// paste. The goroutine therefore stays on one thread from the open through
// the close, and the window it opens the clipboard for is created and
// destroyed on that thread.
func setClipboardImage(img *image.RGBA) error {
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
	marker, err := prepareUploadMarker()
	if err != nil {
		return err
	}
	// Like the image's allocation, the marker's belongs to the system once
	// SetClipboardData accepts it.
	markerOwnedByClipboard := false
	defer func() {
		if !markerOwnedByClipboard {
			procGlobalFree.Call(marker.handle)
		}
	}()
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
	// Deferred before CloseClipboard, so they run after it: the clipboard is
	// closed, then its owner window destroyed, then the thread released.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owner, err := newClipboardOwner()
	if err != nil {
		return err
	}
	defer procDestroyWindow.Call(owner)
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
	// The marker goes first, so the clipboard never holds the image without it.
	if r, _, err := procSetClipboardData.Call(marker.format, marker.handle); r == 0 {
		return kindError(KindClipboard, "SetClipboardData("+cloudUploadFormat+")", callError(err))
	}
	markerOwnedByClipboard = true
	r, _, err = procSetClipboardData.Call(cfDIB, allocation)
	if r == 0 {
		// The marker is not left on the clipboard without its image.
		procEmptyClipboard.Call()
		return kindError(KindClipboard, "SetClipboardData(CF_DIB)", callError(err))
	}
	ownedByClipboard = true
	return nil
}

// newClipboardOwner creates the hidden message-only window the clipboard is
// opened for, of the system's STATIC class, so no class is registered for it.
// The calling thread must destroy it.
func newClipboardOwner() (uintptr, error) {
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		return 0, kindError(KindClipboard, "CreateWindowEx", err)
	}
	window, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if window == 0 {
		return 0, kindError(KindClipboard, "CreateWindowEx", callError(err))
	}
	return window, nil
}

// uploadMarker is the cloudUploadFormat marker, a zero DWORD, ready to place
// on the clipboard.
type uploadMarker struct {
	format uintptr
	handle uintptr
}

// prepareUploadMarker is newUploadMarker, replaceable by a test that needs
// the marker to fail.
var prepareUploadMarker = newUploadMarker

// newUploadMarker registers the marker's format and allocates its value,
// which the caller frees unless the clipboard takes it.
func newUploadMarker() (uploadMarker, error) {
	name, err := windows.UTF16PtrFromString(cloudUploadFormat)
	if err != nil {
		return uploadMarker{}, kindError(KindClipboard, "RegisterClipboardFormat", err)
	}
	format, _, err := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if format == 0 {
		return uploadMarker{}, kindError(KindClipboard, "RegisterClipboardFormat", callError(err))
	}
	handle, _, err := procGlobalAlloc.Call(gmemMoveable|gmemZeroInit, unsafe.Sizeof(uint32(0)))
	if handle == 0 {
		return uploadMarker{}, kindError(KindClipboard, "GlobalAlloc", callError(err))
	}
	return uploadMarker{format: format, handle: handle}, nil
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

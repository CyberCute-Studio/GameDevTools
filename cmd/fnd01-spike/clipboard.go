package main

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func lockGlobalMemory(handle uintptr) (uintptr, error) {
	address, _, err := procGlobalLock.Call(handle)
	if address == 0 {
		return 0, callError("GlobalLock", err)
	}
	return address, nil
}

func setClipboardImage(owner windows.Handle, img *image.RGBA) error {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	header := bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     int32(width),
		Height:    int32(height),
		Planes:    1,
		BitCount:  32,
		SizeImage: uint32(width * height * 4),
	}
	allocationSize := unsafe.Sizeof(header) + uintptr(width*height*4)
	allocation, _, err := procGlobalAlloc.Call(gmemMoveable, allocationSize)
	if allocation == 0 {
		return callError("GlobalAlloc", err)
	}
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
		for x := 0; x < width; x++ {
			sourceOffset := img.PixOffset(bounds.Min.X+x, bounds.Min.Y+(height-1-y))
			destinationOffset := (y*width + x) * 4
			pixels[destinationOffset+0] = img.Pix[sourceOffset+2]
			pixels[destinationOffset+1] = img.Pix[sourceOffset+1]
			pixels[destinationOffset+2] = img.Pix[sourceOffset+0]
			pixels[destinationOffset+3] = 255
		}
	}
	procRtlMoveMemory.Call(destination+unsafe.Sizeof(header), uintptr(unsafe.Pointer(&pixels[0])), uintptr(len(pixels)))
	procGlobalUnlock.Call(allocation)
	var opened bool
	for attempt := 0; attempt < 5; attempt++ {
		r, _, _ := procOpenClipboard.Call(uintptr(owner))
		if r != 0 {
			opened = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("OpenClipboard: clipboard remained busy")
	}
	defer procCloseClipboard.Call()
	r, _, err := procEmptyClipboard.Call()
	if r == 0 {
		return callError("EmptyClipboard", err)
	}
	r, _, err = procSetClipboardData.Call(cfDIB, allocation)
	if r == 0 {
		return callError("SetClipboardData(CF_DIB)", err)
	}
	ownedByClipboard = true
	return nil
}

func clipboardImageSize() (int32, int32, error) {
	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		return 0, 0, callError("OpenClipboard", err)
	}
	defer procCloseClipboard.Call()
	allocation, _, err := procGetClipboardData.Call(cfDIB, 0)
	if allocation == 0 {
		return 0, 0, callError("GetClipboardData(CF_DIB)", err)
	}
	data, err := lockGlobalMemory(allocation)
	if err != nil {
		return 0, 0, err
	}
	defer procGlobalUnlock.Call(allocation)
	var header bitmapInfoHeader
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&header)), data, unsafe.Sizeof(header))
	if header.Size != uint32(unsafe.Sizeof(bitmapInfoHeader{})) || header.BitCount != 32 {
		return 0, 0, fmt.Errorf("unexpected clipboard DIB header")
	}
	return header.Width, header.Height, nil
}

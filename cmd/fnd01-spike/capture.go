package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"unsafe"
)

func captureScreen(bounds rect) (*image.RGBA, error) {
	width, height := bounds.width(), bounds.height()
	if width <= 0 || height <= 0 || int64(width)*int64(height) > 1<<28 {
		return nil, fmt.Errorf("invalid capture dimensions %d × %d", width, height)
	}
	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return nil, callError("GetDC", err)
	}
	defer procReleaseDC.Call(0, screen)
	memory, _, err := procCreateCompatibleDC.Call(screen)
	if memory == 0 {
		return nil, callError("CreateCompatibleDC", err)
	}
	defer procDeleteDC.Call(memory)
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     width,
		Height:    -height,
		Planes:    1,
		BitCount:  32,
		SizeImage: uint32(width * height * 4),
	}}
	var pixels unsafe.Pointer
	bitmap, _, err := procCreateDIBSection.Call(screen, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 || pixels == nil {
		return nil, callError("CreateDIBSection", err)
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, err := procSelectObject.Call(memory, bitmap)
	if previous == 0 {
		return nil, callError("SelectObject", err)
	}
	defer procSelectObject.Call(memory, previous)
	r, _, err := procBitBlt.Call(
		memory, 0, 0, uintptr(width), uintptr(height),
		screen, uintptr(bounds.Left), uintptr(bounds.Top), srcCopy|captureBLT,
	)
	if r == 0 {
		return nil, callError("BitBlt", err)
	}
	source := unsafe.Slice((*byte)(pixels), int(width*height*4))
	result := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		for x := 0; x < int(width); x++ {
			sourceOffset := (y*int(width) + x) * 4
			destinationOffset := result.PixOffset(x, y)
			result.Pix[destinationOffset+0] = source[sourceOffset+2]
			result.Pix[destinationOffset+1] = source[sourceOffset+1]
			result.Pix[destinationOffset+2] = source[sourceOffset+0]
			result.Pix[destinationOffset+3] = 255
		}
	}
	return result, nil
}

func savePNG(path string, img *image.RGBA) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create PNG: %w", err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		return fmt.Errorf("encode PNG: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close PNG: %w", err)
	}
	return nil
}

func verifyPNG(path string, width, height int) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open verification PNG: %w", err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		return fmt.Errorf("decode verification PNG: %w", err)
	}
	if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		return fmt.Errorf("decoded PNG dimensions: got %v, want %d × %d", decoded.Bounds(), width, height)
	}
	return nil
}

func captureOverlay(window *nativeWindow) (*image.RGBA, error) {
	bounds, err := getWindowRect(window.hwnd)
	if err != nil {
		return nil, err
	}
	showWindow(window.hwnd, swHide)
	defer showWindow(window.hwnd, swShow)
	if hr, _, _ := procDwmFlush.Call(); int32(hr) < 0 {
		return nil, fmt.Errorf("DwmFlush returned HRESULT %#x", uint32(hr))
	}
	return captureScreen(bounds)
}

//go:build windows

package ui

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/pathform"
)

const (
	ofnHideReadOnly      = 0x00000004
	ofnNoChangeDir       = 0x00000008
	ofnAllowMultiSelect  = 0x00000200
	ofnPathMustExist     = 0x00000800
	ofnFileMustExist     = 0x00001000
	ofnExplorer          = 0x00080000
	ofnDontAddToRecent   = 0x02000000
	fnerrBufferTooSmall  = 0x3003
	pickerBufferCapacity = 65536
)

var (
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")

	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	procCommDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
)

type openFileName struct {
	lStructSize       uint32
	hwndOwner         windows.Handle
	hInstance         windows.Handle
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

// PickImageFiles opens the common file picker filtered to the supported PNG
// and JPEG extensions (RSZ-003) and returns every selected absolute path. It
// reports false without an error when the user cancels, or when HoldDialogs
// keeps it from opening.
func PickImageFiles(parent windows.Handle, title string) ([]string, bool, error) {
	if dialogRefused() {
		return nil, false, nil
	}
	buffer := make([]uint16, pickerBufferCapacity)
	filter, err := imagePickerFilter()
	if err != nil {
		return nil, false, err
	}
	dialog := openFileName{
		lStructSize: uint32(unsafe.Sizeof(openFileName{})),
		hwndOwner:   parent,
		lpstrFilter: &filter[0],
		lpstrFile:   &buffer[0],
		nMaxFile:    uint32(len(buffer)),
		lpstrTitle:  utf16(title),
		// OFN_NOCHANGEDIR keeps the dialog from silently moving the process
		// working directory to the browsed folder.
		flags: ofnExplorer | ofnAllowMultiSelect | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly | ofnNoChangeDir | ofnDontAddToRecent,
	}
	result, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if result == 0 {
		code, _, _ := procCommDlgExtendedError.Call()
		if code == 0 {
			return nil, false, nil
		}
		if code == fnerrBufferTooSmall {
			return nil, false, errors.New(ImagePickerTooManyFiles)
		}
		return nil, false, fmt.Errorf("GetOpenFileNameW failed with code %#x", code)
	}
	return parsePickedPaths(buffer), true, nil
}

// imagePickerFilter builds the picker's filter: the label the user reads and
// the semicolon-separated pattern, each ending in a NUL, and a last NUL that
// ends the list. They are converted separately because UTF16FromString refuses
// a string that holds a NUL.
func imagePickerFilter() ([]uint16, error) {
	label, err := windows.UTF16FromString(ImageFileFilterLabel + " (*.png; *.jpg; *.jpeg)")
	if err != nil {
		return nil, err
	}
	pattern, err := windows.UTF16FromString("*.png;*.jpg;*.jpeg")
	if err != nil {
		return nil, err
	}
	return append(append(label, pattern...), 0), nil
}

// parsePickedPaths reads the picker's multi-select buffer: with several files
// the first string is the folder and the rest are file names relative to it;
// with one file the first string is already the full path. The sequence ends
// with an empty string.
func parsePickedPaths(buffer []uint16) []string {
	read := func(offset int) (string, int) {
		end := offset
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		return windows.UTF16ToString(buffer[offset:end]), end + 1
	}
	first, next := read(0)
	if first == "" {
		return nil
	}
	if next >= len(buffer) || buffer[next] == 0 {
		return []string{first}
	}
	paths := []string{}
	for {
		name, after := read(next)
		if name == "" {
			return paths
		}
		paths = append(paths, pathform.Join(first, name))
		next = after
	}
}

package ui

import (
	"fmt"
	"strconv"
	"strings"
)

type ErrorKind uint8

const (
	ErrorValidation ErrorKind = iota
	ErrorUnsupportedFormat
	ErrorReadDecode
	ErrorWriteEncode
	ErrorClipboard
	ErrorShortcutRegistration
	ErrorCapture
)

func (k ErrorKind) Label() string {
	switch k {
	case ErrorValidation:
		return "Validation error"
	case ErrorUnsupportedFormat:
		return "Unsupported format"
	case ErrorReadDecode:
		return "Read/decode error"
	case ErrorWriteEncode:
		return "Write/encode error"
	case ErrorClipboard:
		return "Clipboard error"
	case ErrorShortcutRegistration:
		return "Shortcut registration error"
	case ErrorCapture:
		return "Capture error"
	default:
		return "Error"
	}
}

func FormatError(kind ErrorKind, err error) string {
	if err == nil {
		return ""
	}
	return kind.Label() + ": " + err.Error()
}

func ParseDimension(field, input string) (int, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return 0, fmt.Errorf("%s is required. Enter a whole number of pixels greater than 0.", field)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("%s must be a whole number of pixels greater than 0.", field)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil {
		maximum := uint64(^uint(0) >> 1)
		return 0, fmt.Errorf("%s is too large. Enter a whole number no greater than %d pixels.", field, maximum)
	}
	if parsed == 0 {
		return 0, fmt.Errorf("%s must be greater than 0 pixels.", field)
	}
	return int(parsed), nil
}

func FormatDimensions(width, height int) string {
	return fmt.Sprintf("%d × %d px", width, height)
}

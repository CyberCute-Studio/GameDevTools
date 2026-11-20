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
	// ErrorUnexpected is a failure no step reported as one of the kinds
	// above, such as a panic a resize job contained (NFR-021).
	ErrorUnexpected
)

func (k ErrorKind) Label() string {
	switch k {
	case ErrorValidation:
		return ErrorValidationLabel
	case ErrorUnsupportedFormat:
		return ErrorUnsupportedFormatLabel
	case ErrorReadDecode:
		return ErrorReadDecodeLabel
	case ErrorWriteEncode:
		return ErrorWriteEncodeLabel
	case ErrorClipboard:
		return ErrorClipboardLabel
	case ErrorShortcutRegistration:
		return ErrorShortcutRegistrationLabel
	case ErrorCapture:
		return ErrorCaptureLabel
	case ErrorUnexpected:
		return ErrorUnexpectedLabel
	default:
		return ErrorOtherLabel
	}
}

func FormatError(kind ErrorKind, err error) string {
	if err == nil {
		return ""
	}
	return kind.Label() + LabelSeparator + err.Error()
}

func ParseDimension(field, input string) (int, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return 0, fmt.Errorf(DimensionRequired, field)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf(DimensionNotWhole, field)
		}
	}
	parsed, err := strconv.ParseInt(value, 10, strconv.IntSize)
	if err != nil {
		maximum := int64(^uint(0) >> 1)
		return 0, fmt.Errorf(DimensionTooLarge, field, maximum)
	}
	if parsed == 0 {
		return 0, fmt.Errorf(DimensionZero, field)
	}
	return int(parsed), nil
}

func FormatDimensions(width, height int) string {
	return fmt.Sprintf(DimensionsFormat, width, height)
}

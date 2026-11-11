package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestParseDimension(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      int
		wantError string
	}{
		{name: "integer", input: "1280", want: 1280},
		{name: "surrounding whitespace", input: " \t720\r\n", want: 720},
		{name: "one", input: "1", want: 1},
		{name: "empty", input: "", wantError: "Width is required"},
		{name: "whitespace", input: "  ", wantError: "Width is required"},
		{name: "zero", input: "0", wantError: "greater than 0 pixels"},
		{name: "negative", input: "-1", wantError: "whole number"},
		{name: "fraction", input: "1.5", wantError: "whole number"},
		{name: "text", input: "wide", wantError: "whole number"},
		{name: "embedded whitespace", input: "10 20", wantError: "whole number"},
		{name: "explicit sign", input: "+1", wantError: "whole number"},
		{name: "overflow", input: "999999999999999999999999999999999999", wantError: "too large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseDimension("Width", test.input)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("ParseDimension() error = %v, want text %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDimension() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ParseDimension() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseDimensionMaximumInt(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	got, err := ParseDimension("Height", strconv.FormatInt(int64(maximum), 10))
	if err != nil || got != maximum {
		t.Fatalf("ParseDimension(maximum) = %d, %v; want %d", got, err, maximum)
	}
}

func TestFormatDimensions(t *testing.T) {
	if got := FormatDimensions(1280, 720); got != "1280 × 720 px" {
		t.Fatalf("FormatDimensions() = %q", got)
	}
}

func TestErrorKindsHaveDistinctPresentation(t *testing.T) {
	kinds := []ErrorKind{
		ErrorValidation,
		ErrorUnsupportedFormat,
		ErrorReadDecode,
		ErrorWriteEncode,
		ErrorClipboard,
		ErrorShortcutRegistration,
		ErrorCapture,
	}
	seen := make(map[string]ErrorKind)
	for _, kind := range kinds {
		got := FormatError(kind, errors.New("details"))
		if !strings.HasSuffix(got, ": details") {
			t.Fatalf("FormatError(%v) = %q", kind, got)
		}
		if previous, exists := seen[got]; exists {
			t.Fatalf("error kinds %v and %v share presentation %q", previous, kind, got)
		}
		seen[got] = kind
	}
	if got := FormatError(ErrorValidation, nil); got != "" {
		t.Fatalf("FormatError(nil) = %q", got)
	}
}

package screenshot

import (
	"strings"
	"testing"
)

func TestParseShortcutRoundTrips(t *testing.T) {
	tests := []struct {
		input string
		want  Shortcut
	}{
		{input: "Ctrl+Alt+S", want: Shortcut{Ctrl: true, Alt: true, Key: 'S', Name: "S"}},
		{input: "ctrl+alt+s", want: Shortcut{Ctrl: true, Alt: true, Key: 'S', Name: "S"}},
		{input: " Alt + Ctrl + s ", want: Shortcut{Ctrl: true, Alt: true, Key: 'S', Name: "S"}},
		{input: "s+ctrl+alt", want: Shortcut{Ctrl: true, Alt: true, Key: 'S', Name: "S"}},
		{input: "Control+Shift+F5", want: Shortcut{Ctrl: true, Shift: true, Key: 0x74, Name: "F5"}},
		{input: "Win+Alt+7", want: Shortcut{Win: true, Alt: true, Key: '7', Name: "7"}},
		{input: "Windows+Shift+P", want: Shortcut{Win: true, Shift: true, Key: 'P', Name: "P"}},
		{input: "Shift+F1", want: Shortcut{Shift: true, Key: 0x70, Name: "F1"}},
		{input: "Ctrl+F24", want: Shortcut{Ctrl: true, Key: 0x87, Name: "F24"}},
		{input: "Alt+0", want: Shortcut{Alt: true, Key: '0', Name: "0"}},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			shortcut, err := ParseShortcut(test.input)
			if err != nil {
				t.Fatalf("ParseShortcut(%q) error = %v", test.input, err)
			}
			if shortcut != test.want {
				t.Fatalf("ParseShortcut(%q) = %+v, want %+v", test.input, shortcut, test.want)
			}
			reparsed, err := ParseShortcut(shortcut.String())
			if err != nil || reparsed != shortcut {
				t.Fatalf("String round-trip = %+v, %v; want %+v", reparsed, err, shortcut)
			}
		})
	}
}

func TestDefaultShortcut(t *testing.T) {
	shortcut := DefaultShortcut()
	got := shortcut.String()
	if got != "Ctrl+Alt+S" {
		t.Fatalf("default shortcut = %q, want Ctrl+Alt+S (SCR-003)", got)
	}
	reparsed, err := ParseShortcut(got)
	if err != nil || reparsed != shortcut {
		t.Fatalf("default shortcut does not parse back: %+v, %v", reparsed, err)
	}
}

func TestShortcutStringOrdersModifiersCanonically(t *testing.T) {
	shortcut := Shortcut{Win: true, Shift: true, Alt: true, Ctrl: true, Key: 'Q', Name: "Q"}
	if got := shortcut.String(); got != "Ctrl+Alt+Shift+Win+Q" {
		t.Fatalf("canonical order = %q, want Ctrl+Alt+Shift+Win+Q", got)
	}
	if got := (Shortcut{}).String(); got != "" {
		t.Fatalf("zero shortcut formats as %q, want empty", got)
	}
}

func TestParseShortcutRejectsInvalidCombinations(t *testing.T) {
	inputs := []string{
		"",
		"   ",
		"S",
		"Ctrl",
		"Ctrl+",
		"+S",
		"Ctrl++S",
		"Ctrl+Ctrl+S",
		"Alt+Alt+S",
		"Ctrl+S+D",
		"Ctrl+Alt",
		"Ctrl+F25",
		"Ctrl+F0",
		"Ctrl+Escape",
		"Ctrl+Hyper",
		"Ctrl+SS",
		"Ctrl+ä",
	}
	for _, input := range inputs {
		t.Run("input "+input, func(t *testing.T) {
			_, err := ParseShortcut(input)
			if err == nil || !strings.Contains(err.Error(), "Ctrl+Alt+S") {
				t.Fatalf("ParseShortcut(%q) error = %v, want guidance naming an example", input, err)
			}
		})
	}
}

package screenshot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// ErrShortcutConflict marks a global shortcut registration that failed
// because another application already owns the combination (SCR-003). The
// previously registered shortcut is untouched by a conflicted attempt.
var ErrShortcutConflict = errors.New("the shortcut is already registered by another application")

// Shortcut is one global capture-invocation key combination: at least one
// modifier plus a letter, digit, or function key. Key holds the Windows
// virtual-key code and Name the canonical display name of the key, so the
// type stays comparable and its String form round-trips through
// ParseShortcut.
type Shortcut struct {
	Ctrl  bool
	Alt   bool
	Shift bool
	Win   bool
	Key   uint32
	Name  string
}

// DefaultShortcut is the SCR-003 default global start shortcut, Ctrl+Alt+S.
func DefaultShortcut() Shortcut {
	return Shortcut{Ctrl: true, Alt: true, Key: 'S', Name: "S"}
}

// Valid reports whether the shortcut can be registered: a key plus at least
// one modifier, matching what ParseShortcut accepts and what is stored.
func (s Shortcut) Valid() bool {
	return s.Key != 0 && (s.Ctrl || s.Alt || s.Shift || s.Win)
}

// String formats the shortcut in its canonical modifier order, for example
// "Ctrl+Alt+S" or "Ctrl+Shift+F5".
func (s Shortcut) String() string {
	if s.Key == 0 {
		return ""
	}
	parts := make([]string, 0, 5)
	if s.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if s.Alt {
		parts = append(parts, "Alt")
	}
	if s.Shift {
		parts = append(parts, "Shift")
	}
	if s.Win {
		parts = append(parts, "Win")
	}
	return strings.Join(append(parts, s.Name), "+")
}

// ParseShortcut converts typed shortcut text into a Shortcut. Tokens are
// separated by +, trimmed, and matched case-insensitively; the result always
// carries the canonical String form. A valid shortcut needs at least one
// modifier and exactly one letter (A–Z), digit (0–9), or function key
// (F1–F24).
func ParseShortcut(input string) (Shortcut, error) {
	shortcut := Shortcut{}
	keySeen := false
	for _, token := range strings.Split(input, "+") {
		token = strings.TrimSpace(token)
		if token == "" {
			return Shortcut{}, errors.New(ui.ShortcutInvalid)
		}
		switch strings.ToLower(token) {
		case "ctrl", "control":
			if shortcut.Ctrl {
				return Shortcut{}, errors.New(ui.ShortcutInvalid)
			}
			shortcut.Ctrl = true
		case "alt":
			if shortcut.Alt {
				return Shortcut{}, errors.New(ui.ShortcutInvalid)
			}
			shortcut.Alt = true
		case "shift":
			if shortcut.Shift {
				return Shortcut{}, errors.New(ui.ShortcutInvalid)
			}
			shortcut.Shift = true
		case "win", "windows":
			if shortcut.Win {
				return Shortcut{}, errors.New(ui.ShortcutInvalid)
			}
			shortcut.Win = true
		default:
			if keySeen {
				return Shortcut{}, errors.New(ui.ShortcutInvalid)
			}
			key, name, ok := parseShortcutKey(token)
			if !ok {
				return Shortcut{}, errors.New(ui.ShortcutInvalid)
			}
			shortcut.Key = key
			shortcut.Name = name
			keySeen = true
		}
	}
	if !keySeen || !shortcut.Valid() {
		return Shortcut{}, errors.New(ui.ShortcutInvalid)
	}
	return shortcut, nil
}

// parseShortcutKey resolves one non-modifier token to its virtual-key code
// and canonical name. Letters and digits use their ASCII codes, which match
// the Windows virtual-key codes; F1–F24 use VK_F1 through VK_F24.
func parseShortcutKey(token string) (uint32, string, bool) {
	upper := strings.ToUpper(token)
	if len(upper) == 1 {
		character := upper[0]
		if character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			return uint32(character), upper, true
		}
		return 0, "", false
	}
	if len(upper) >= 2 && upper[0] == 'F' {
		number, err := strconv.Atoi(upper[1:])
		if err == nil && number >= 1 && number <= 24 {
			return 0x6F + uint32(number), "F" + strconv.Itoa(number), true
		}
	}
	return 0, "", false
}

// shortcutConflictError describes a registration conflict with the offending
// combination for the user.
func shortcutConflictError(shortcut Shortcut) error {
	return fmt.Errorf("%w: %s. "+ui.ShortcutConflictAction, ErrShortcutConflict, shortcut)
}

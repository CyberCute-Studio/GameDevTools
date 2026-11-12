//go:build !windows

package display

import "errors"

// Display enumeration is a Windows-only capability; the production
// application only runs on Windows.
func rawDisplays() ([]Display, error) {
	return nil, errors.New("display enumeration is available on Windows only")
}

func cursorPosition() (Point, error) {
	return Point{}, errors.New("the cursor position is available on Windows only")
}

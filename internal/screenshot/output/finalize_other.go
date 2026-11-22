//go:build !windows

package output

import (
	"errors"
	"os"
)

// moveNoReplace is a Windows-only capability; the production application
// only runs on Windows.
func moveNoReplace(from, to string) error {
	return &os.LinkError{Op: "move", Old: from, New: to, Err: errors.ErrUnsupported}
}

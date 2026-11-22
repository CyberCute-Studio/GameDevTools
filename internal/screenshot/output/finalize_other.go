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

// retryWhileHeld runs change once: only Windows refuses to change a file
// another program holds open.
func retryWhileHeld(change func() error) error {
	return change()
}

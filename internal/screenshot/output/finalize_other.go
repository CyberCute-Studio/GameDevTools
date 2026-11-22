//go:build !windows

package output

import (
	"errors"
	"os"
)

// heldWait has nothing to wait for: only Windows refuses to change a file
// another program holds open.
type heldWait struct{}

// retry runs change once.
func (*heldWait) retry(change func() error) error {
	return change()
}

// moveNoReplace is a Windows-only capability; the production application
// only runs on Windows.
func moveNoReplace(from, to string, _ *heldWait) error {
	return &os.LinkError{Op: "move", Old: from, New: to, Err: errors.ErrUnsupported}
}

//go:build windows

package update

import (
	"errors"

	"golang.org/x/sys/windows"
)

// heldByAnother reports whether a rename failed as it does while another
// program has the file open without letting it be renamed or replaced, which
// may pass: a sharing violation, or the access denied that replacing a file
// so held, or one being deleted, gives.
func heldByAnother(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

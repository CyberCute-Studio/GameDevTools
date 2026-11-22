package settings

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// networkPathMissing reports whether err is Windows failing to find a network
// path, which Go counts as a file that does not exist though it says nothing
// about the file: the server or share did not answer.
func networkPathMissing(err error) bool {
	return errors.Is(err, windows.ERROR_BAD_NETPATH)
}

// renameNoReplace moves from to to in one step, failing with an error matching
// os.ErrExist if to exists. MoveFileEx without MOVEFILE_REPLACE_EXISTING needs
// no hard links, which a redirected settings folder's volume may not have.
func renameNoReplace(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("rename %s: %w", from, err)
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("rename to %s: %w", to, err)
	}
	if err := windows.MoveFileEx(source, target, 0); err != nil {
		return fmt.Errorf("rename to %s: %w", to, err)
	}
	return nil
}

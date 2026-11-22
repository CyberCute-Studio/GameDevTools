package settings

import (
	"fmt"

	"golang.org/x/sys/windows"
)

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

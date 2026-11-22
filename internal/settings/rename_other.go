//go:build !windows

package settings

import "os"

// renameNoReplace moves from to to, failing with an error matching os.ErrExist
// if to exists: a hard link, unlike a rename, does not replace its target.
func renameNoReplace(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	// The link is the commit, so a temporary name that stays is not a failure.
	_ = os.Remove(from)
	return nil
}

//go:build !windows

package update

import "errors"

// Relaunch is available on Windows only, the one platform the application
// runs on.
func Relaunch(executable string) error {
	return errors.New("relaunching is available on Windows only")
}

//go:build !windows

package update

import "errors"

// Relaunch is available on Windows only, the one platform the application
// runs on.
func Relaunch(executable string) (uint32, error) {
	return 0, errors.New("relaunching is available on Windows only")
}

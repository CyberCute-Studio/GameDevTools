//go:build !windows

package overlay

import "errors"

// Rectangle selection overlays are a Windows-only capability; the production
// application only runs on Windows.
func createNative(CreateRequest) (NativeSession, error) {
	return nil, errors.New("rectangle selection overlay is available on Windows only")
}

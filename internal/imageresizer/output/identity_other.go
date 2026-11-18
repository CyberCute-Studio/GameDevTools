//go:build !windows

package output

import "errors"

// fileID is empty outside Windows, where the production application does
// not run.
type fileID struct{}

var errIdentityUnsupported = errors.New("file identity is available on Windows only")

// identify always fails outside Windows, so planning there never proves an
// existing file is not a source and an Overwrite job renames instead of
// replacing.
func identify(string) (fileID, error) {
	return fileID{}, errIdentityUnsupported
}

//go:build windows

package output

import (
	"errors"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// moveAttempts and moveRetryWait bound how long moveNoReplace waits for a
// program that has just opened the finished file, as a virus scanner or the
// search indexer does, to close it: the attempts span about 375 milliseconds.
const (
	moveAttempts  = 5
	moveRetryWait = 25 * time.Millisecond
)

// moveNoReplace renames a file within its folder, failing with an "already
// exists" error rather than replacing a file that has the new name, as
// MoveFileEx does without MOVEFILE_REPLACE_EXISTING. The rename is written
// through to the drive, since a FAT32 or exFAT volume is often removable.
func moveNoReplace(from, to string) error {
	failure := func(err error) error {
		return &os.LinkError{Op: "move", Old: from, New: to, Err: err}
	}
	source, err := windows.UTF16PtrFromString(extendedLengthPath(from))
	if err != nil {
		return failure(err)
	}
	target, err := windows.UTF16PtrFromString(extendedLengthPath(to))
	if err != nil {
		return failure(err)
	}
	wait := moveRetryWait
	for attempt := 1; ; attempt++ {
		err := windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH)
		if err == nil {
			return nil
		}
		if attempt == moveAttempts || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return failure(err)
		}
		time.Sleep(wait)
		wait *= 2
	}
}

// extendedLengthPath marks a full path as one Windows takes verbatim, so a
// path past the 260-character limit still works, as it does through package
// os. A save passes only paths pathform.NamesOneFolder accepted, which
// Windows reads as written, so the mark does not change what they name.
func extendedLengthPath(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\`):
		return path
	case strings.HasPrefix(path, `\??\`), strings.HasPrefix(path, `\\.\`):
		return `\\?\` + path[4:]
	case strings.HasPrefix(path, `\\`):
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}

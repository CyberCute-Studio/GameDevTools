//go:build windows

package output

import (
	"errors"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// heldRetries and heldRetryWait bound how long one save waits for a program
// that has just opened the finished file, as a virus scanner or the search
// indexer does, to close it: the retries span about 775 milliseconds.
const (
	heldRetries   = 5
	heldRetryWait = 25 * time.Millisecond
)

// heldWait is the one bounded wait a save has for such a program. The rename
// and every removal of the temporary name draw on the same wait, so a failed
// save's cleanup never waits out a hold the rename gave up on: that wait could
// only end by deleting a finished image the rename would then have saved.
type heldWait struct {
	retries int
}

// retry runs change until it succeeds, fails in a way waiting cannot help, or
// the wait is spent, and returns its last error. A scanner's hold on a file
// surfaces as a sharing violation or, through its filter driver, as access
// denied, so a denial that lasts is returned only after the wait.
func (w *heldWait) retry(change func() error) error {
	for {
		err := change()
		if err == nil || w.retries == heldRetries ||
			!(errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)) {
			return err
		}
		time.Sleep(heldRetryWait << w.retries)
		w.retries++
	}
}

// moveNoReplace renames a file within its folder, failing with an "already
// exists" error rather than replacing a file that has the new name, as
// MoveFileEx does without MOVEFILE_REPLACE_EXISTING. It asks for the rename to
// be written through, but Microsoft spells out that flag's guarantee only for
// a move done as a copy and delete, which this is not, so nothing here relies
// on the new name having reached a removable drive when it returns.
func moveNoReplace(from, to string, wait *heldWait) error {
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
	err = wait.retry(func() error {
		return windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH)
	})
	if err != nil {
		return failure(err)
	}
	return nil
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

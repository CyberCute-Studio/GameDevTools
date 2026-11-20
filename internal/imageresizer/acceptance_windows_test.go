//go:build windows

package imageresizer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// fileAddFile is the FILE_ADD_FILE access right: creating a file in a
// folder, or giving a file a name in it.
const fileAddFile = 0x2

// folderCreation prepares to take from everyone, and give back, the right to
// create files in folder, so a job's output folder can become unwritable
// partway through it, as when its permissions change. deny adds an access
// control entry denying FILE_ADD_FILE to the folder alone, which every later
// create in it meets at once while the folder stays listable and its files
// readable; restore puts back the folder's own access control list, and runs
// at the end of the test too. Both may run on another goroutine, such as a
// job's observer, so they report failures rather than failing the test.
func folderCreation(t *testing.T, folder string) (deny, restore func() error) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read the security of %s: %v", folder, err)
	}
	original, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if original == nil {
		// A NULL access control list grants everyone everything; one holding
		// only the denying entry would grant nothing, so the folder could not
		// even be read, rather than refuse only new files.
		t.Fatalf("%s has no access control list, which AC-R14 changes: run it with an NTFS temporary folder", folder)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	denied, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: fileAddFile,
		AccessMode:        windows.DENY_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, original)
	if err != nil {
		t.Fatal(err)
	}
	protection := windows.SECURITY_INFORMATION(windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		protection = windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	set := func(dacl *windows.ACL) error {
		return windows.SetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|protection, nil, nil, dacl, nil)
	}
	deny = func() error { return set(denied) }
	restore = func() error { return set(original) }
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Errorf("restore the security of %s: %v", folder, err)
		}
	})
	return deny, restore
}

// TestAcceptanceR14WriteFailure pins AC-R14 on a real folder: once the
// output folder stops letting the user create files partway through a job,
// each item that cannot then save fails on its own with a write error that
// names its destination and says how to recover, no partial final file and
// no temporary file is left, and the remaining items continue — once the
// folder accepts files again, every item that starts is saved.
func TestAcceptanceR14WriteFailure(t *testing.T) {
	sources := t.TempDir()
	content := fixtureJPEG(t, fixtureQuadrants(320, 240))
	var paths []string
	for index := range 12 {
		paths = append(paths, writeFixture(t, sources, fmt.Sprintf("photo-%02d.jpg", index), content))
	}
	out := t.TempDir()
	deny, restore := folderCreation(t, out)
	var (
		denied, restored     bool
		denyErr, restoreErr  error
		savedBefore          string
		startedAfterRestored = make(map[int]bool)
	)
	// The job reports events one at a time, and no item starts or ends
	// while one is reported, so the folder changes between two items'
	// steps: items in flight at either change may go either way, and every
	// item that starts after the folder accepts files again must be saved.
	observe := func(event job.Event) {
		switch event.Status.State {
		case job.StateProcessing:
			if restored {
				startedAfterRestored[event.Index] = true
			}
		case job.StateCompleted:
			if !denied {
				savedBefore = event.Status.Destination
				denied, denyErr = true, deny()
			}
		case job.StateFailed:
			if denied && !restored {
				restored, restoreErr = true, restore()
			}
		}
	}
	result := runAcceptance(t, context.Background(), paths, acceptanceOptions(out, FormatKeepSource, CollisionRename), target(16, 16, preset.ResizeFit), observe)
	if denyErr != nil || restoreErr != nil {
		t.Fatalf("changing the folder's security failed: %v, %v", denyErr, restoreErr)
	}
	if !denied || !restored {
		t.Fatalf("the folder was denied %v and restored %v: no item failed to write", denied, restored)
	}

	failed := 0
	var saved []string
	for index, status := range result.summary.Items {
		item := result.item(index)
		switch status.State {
		case job.StateCompleted:
			requireSize(t, status.Destination, decodeOutput(t, status.Destination), 16, 12)
			saved = append(saved, filepath.Base(status.Destination))
		case job.StateFailed:
			failed++
			var outputErr *output.Error
			if !errors.As(status.Err, &outputErr) || outputErr.Kind != output.KindWrite || !errors.Is(status.Err, fs.ErrPermission) || !strings.Contains(status.Err.Error(), status.Destination) {
				t.Fatalf("%s failed with %v, want a denied write naming %s", item.Name, status.Err, status.Destination)
			}
			if _, err := os.Lstat(status.Destination); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s failed but %s exists (%v)", item.Name, status.Destination, err)
			}
			detail := item.DetailText()
			if !strings.HasPrefix(detail, "Failed: "+ui.ErrorWriteEncode.Label()+": ") || !strings.HasSuffix(detail, ui.ResizeHintPermission) {
				t.Fatalf("failed detail = %q, want the write category and the permission hint", detail)
			}
		default:
			t.Fatalf("%s ended %v (%v)", item.Name, status.State, status.Err)
		}
		if startedAfterRestored[index] && status.State != job.StateCompleted {
			t.Fatalf("%s started once the folder accepted files again, but ended %v (%v)", item.Name, status.State, status.Err)
		}
	}
	t.Logf("%d of %d items failed to write; %d started once the folder accepted files again", failed, len(paths), len(startedAfterRestored))
	if failed == 0 || len(startedAfterRestored) == 0 {
		t.Fatalf("%d items failed and %d started after the folder accepted files again, want both", failed, len(startedAfterRestored))
	}
	if got := result.summary.Progress; got.Failed != failed || got.Completed != len(paths)-failed {
		t.Fatalf("summary counts = %+v, want %d failed", got, failed)
	}
	// The folder holds the saved results alone: no partial final file under
	// another name, and no temporary or moved-aside file of a write.
	requireNames(t, out, saved...)
	requireSize(t, savedBefore, decodeOutput(t, savedBefore), 16, 12)
}

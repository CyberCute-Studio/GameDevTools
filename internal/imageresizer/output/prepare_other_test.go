//go:build !windows

package output

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

// vanishingFolder removes the output folder before every destination is
// inspected, as when its drive is removed while the plan is made.
type vanishingFolder struct {
	osFiles
	folder string
}

func (v vanishingFolder) Lstat(path string) (fs.FileInfo, error) {
	os.Remove(v.folder)
	return os.Lstat(path)
}

// TestPrepareFailsWhenTheFolderGoes: outside Windows a path whose folder is
// gone fails with the same ENOENT as a name missing from a folder that
// exists, yet only the latter is free. An output folder removed after
// Prepare has opened it fails the job as a folder failure, as on Windows,
// rather than every destination being planned for creation in it.
func TestPrepareFailsWhenTheFolderGoes(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	source := Source{Path: filepath.Join(t.TempDir(), "x.png"), Format: decode.FormatPNG}
	_, err := prepare(context.Background(), Request{
		Sources:   []Source{source},
		Folder:    out,
		Format:    FormatKeepSource,
		Collision: CollisionRename,
	}, vanishingFolder{folder: out})
	var planErr *Error
	if !errors.As(err, &planErr) || planErr.Kind != KindFolder || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("prepare = %v, want a KindFolder error for the missing folder", err)
	}
}

// TestPrepareNeverReplacesWithoutIdentity pins the fallback outside
// Windows: no file identity can be read, so a confirmed Overwrite never
// proves an existing file is not a source and renames the result instead.
func TestPrepareNeverReplacesWithoutIdentity(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "x.png"), []byte("earlier"), 0o644); err != nil {
		t.Fatalf("write existing destination: %v", err)
	}
	source := Source{Path: filepath.Join(t.TempDir(), "x.png"), Format: decode.FormatPNG}
	want := map[Collision]struct {
		name   string
		action Action
		reason Reason
	}{
		CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
		CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
		CollisionOverwrite: {"x_2.png", ActionCreate, ReasonUnverified},
	}
	for policy, expected := range want {
		plan, err := Prepare(context.Background(), Request{
			Sources:          []Source{source},
			Folder:           out,
			Format:           FormatKeepSource,
			Collision:        policy,
			ConfirmOverwrite: policy == CollisionOverwrite,
		})
		if err != nil {
			t.Fatalf("%s: Prepare: %v", policy, err)
		}
		item := plan.Items[0]
		if filepath.Base(item.Destination) != expected.name || item.Action != expected.action || item.Reason != expected.reason {
			t.Errorf("%s: item = %s %v %v, want %+v", policy, filepath.Base(item.Destination), item.Action, item.Reason, expected)
		}
	}
}

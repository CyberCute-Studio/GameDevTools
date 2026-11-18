//go:build !windows

package output

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

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

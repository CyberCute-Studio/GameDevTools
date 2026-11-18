//go:build windows

package output

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
)

func mustIdentify(t *testing.T, path string) fileID {
	t.Helper()
	id, err := identify(path)
	if err != nil {
		t.Fatalf("identify %s: %v", path, err)
	}
	return id
}

// junction links path to the target folder with a directory junction, which
// needs no privilege, or skips the test when the volume cannot hold one.
func junction(t *testing.T, path, target string) {
	t.Helper()
	output, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		t.Skipf("create junction: %v: %s", err, output)
	}
	// Remove the junction itself before the temporary folders, so their
	// cleanup never walks into the target through it.
	t.Cleanup(func() { os.Remove(path) })
}

func TestIdentifyRecognizesEveryPathToAFile(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "Original.png")
	writeFile(t, path, "pixels")
	want := mustIdentify(t, path)

	aliases := []string{strings.ToUpper(path), strings.ToLower(path)}
	link := filepath.Join(folder, "hard-link.png")
	if err := os.Link(path, link); err != nil {
		t.Fatalf("hard-link: %v", err)
	}
	aliases = append(aliases, link)
	linked := filepath.Join(t.TempDir(), "junction")
	junction(t, linked, folder)
	aliases = append(aliases, filepath.Join(linked, "Original.png"))
	if short, err := shortName(path); err == nil && !strings.EqualFold(short, path) {
		aliases = append(aliases, short)
	}
	for _, alias := range aliases {
		if got := mustIdentify(t, alias); got != want {
			t.Errorf("identify(%s) = %+v, want %+v", alias, got, want)
		}
	}

	other := filepath.Join(folder, "other.png")
	writeFile(t, other, "pixels")
	if mustIdentify(t, other) == want {
		t.Fatal("two distinct files report the same identity")
	}
	if _, err := identify(filepath.Join(folder, "missing.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("identify(missing) = %v, want a not-exist error", err)
	}
	if _, err := identify(filepath.Join(folder, "missing", "x.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("identify(missing folder) = %v, want a not-exist error", err)
	}
}

func shortName(path string) (string, error) {
	long, err := windows.UTF16FromString(path)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	length, err := windows.GetShortPathName(&long[0], &buffer[0], uint32(len(buffer)))
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:length]), nil
}

// TestIdentifyReadsLongPaths: a destination deeper than MAX_PATH is still
// compared by identity, as os.Lstat can still inspect it.
func TestIdentifyReadsLongPaths(t *testing.T) {
	folder := t.TempDir()
	for len(folder) < 300 {
		folder = filepath.Join(folder, strings.Repeat("d", 40))
	}
	path := filepath.Join(folder, "deep.png")
	writeFile(t, path, "pixels")
	link := filepath.Join(t.TempDir(), "short.png")
	if err := os.Link(path, link); err != nil {
		t.Fatalf("hard-link: %v", err)
	}
	if mustIdentify(t, path) != mustIdentify(t, link) {
		t.Fatal("a long path reports a different identity than its short hard link")
	}
}

func TestExtendedPath(t *testing.T) {
	long := `C:\` + strings.Repeat(`folder\`, 40) + "x.png"
	share := `\\server\share\` + strings.Repeat(`folder\`, 40) + "x.png"
	tests := []struct{ path, want string }{
		{`C:\short\x.png`, `C:\short\x.png`},
		{long, `\\?\` + long},
		{share, `\\?\UNC\server\share\` + strings.Repeat(`folder\`, 40) + "x.png"},
		{`\\?\` + long, `\\?\` + long},
		{strings.Replace(long, `\x.png`, `\.\x.png`, 1), `\\?\` + long},
	}
	for _, test := range tests {
		if got := extendedPath(test.path); got != test.want {
			t.Errorf("extendedPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

// TestPrepareComparesPathsCaseInsensitively: Windows paths differing only in
// letter case name one file, so they collide and are protected alike.
func TestPrepareComparesPathsCaseInsensitively(t *testing.T) {
	folder := t.TempDir()
	source := filepath.Join(folder, "Hero.png")
	writeFile(t, source, "original")
	existing := t.TempDir()
	writeFile(t, filepath.Join(existing, "SKY.PNG"), "earlier")
	for _, policy := range policies {
		// The output folder is the source folder spelled in upper case.
		plan := mustPrepare(t, jobRequest(strings.ToUpper(folder), policy, png(source)))
		if policy == imageresizer.CollisionSkip {
			assertItems(t, plan, []expect{{"Hero.png", ActionSkip, ReasonSource}})
		} else {
			assertItems(t, plan, []expect{{"Hero_2.png", ActionCreate, ReasonSource}})
		}

		plan = mustPrepare(t, jobRequest(existing, policy, png(filepath.Join(t.TempDir(), "sky.png"))))
		switch policy {
		case imageresizer.CollisionRename:
			assertItems(t, plan, []expect{{"sky_2.png", ActionCreate, ReasonExists}})
		case imageresizer.CollisionSkip:
			assertItems(t, plan, []expect{{"sky.png", ActionSkip, ReasonExists}})
		case imageresizer.CollisionOverwrite:
			assertItems(t, plan, []expect{{"sky.png", ActionReplace, ReasonNone}})
		}
	}
}

// TestPrepareRecognizesSourcesUnderOtherNames pins RSZ-026 for aliases a
// path comparison cannot see: a destination that is a hard link to a source,
// or that reaches a source through a junction, is that source.
func TestPrepareRecognizesSourcesUnderOtherNames(t *testing.T) {
	want := map[imageresizer.CollisionPolicy]expect{
		imageresizer.CollisionRename:    {"x_2.png", ActionCreate, ReasonSource},
		imageresizer.CollisionSkip:      {"x.png", ActionSkip, ReasonSource},
		imageresizer.CollisionOverwrite: {"x_2.png", ActionCreate, ReasonSource},
	}

	t.Run("hard link", func(t *testing.T) {
		for _, policy := range policies {
			in, out := t.TempDir(), t.TempDir()
			sourceA := filepath.Join(in, "x.png")
			sourceB := filepath.Join(in, "other.png")
			writeFile(t, sourceA, "source x")
			writeFile(t, sourceB, "source other")
			// out\x.png is the second source under another name.
			if err := os.Link(sourceB, filepath.Join(out, "x.png")); err != nil {
				t.Fatalf("hard-link: %v", err)
			}
			plan := mustPrepare(t, jobRequest(out, policy, png(sourceA), png(sourceB)))
			assertItems(t, plan, []expect{want[policy], {"other.png", ActionCreate, ReasonNone}})
			materialize(t, plan, "result")
			if readFile(t, sourceB) != "source other" {
				t.Fatalf("%s: the hard-linked source was overwritten", policy)
			}
		}
	})

	t.Run("junction", func(t *testing.T) {
		for _, policy := range policies {
			in := t.TempDir()
			source := filepath.Join(in, "x.png")
			writeFile(t, source, "source x")
			out := filepath.Join(t.TempDir(), "out")
			junction(t, out, in)
			plan := mustPrepare(t, jobRequest(out, policy, png(source)))
			assertItems(t, plan, []expect{want[policy]})
			materialize(t, plan, "result")
			if readFile(t, source) != "source x" {
				t.Fatalf("%s: the source reached through a junction was overwritten", policy)
			}
		}
	})
}

// TestPrepareRenamesWhenIdentityIsUnreadable: Overwrite replaces only a
// file it can prove is not a source. When the identity of the destination
// or of a source cannot be read, the result is renamed instead; the other
// policies never replace and are unaffected.
func TestPrepareRenamesWhenIdentityIsUnreadable(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	source := filepath.Join(in, "x.png")
	writeFile(t, source, "source")
	destination := filepath.Join(out, "x.png")
	writeFile(t, destination, "earlier")
	for _, unreadable := range []string{source, destination} {
		files := faultyFiles{identity: func(path string) (fileID, error) {
			if strings.EqualFold(path, unreadable) {
				return fileID{}, &fs.PathError{Op: "open", Path: path, Err: windows.ERROR_ACCESS_DENIED}
			}
			return identify(path)
		}}
		want := map[imageresizer.CollisionPolicy]expect{
			imageresizer.CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
			imageresizer.CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
			imageresizer.CollisionOverwrite: {"x_2.png", ActionCreate, ReasonUnverified},
		}
		for _, policy := range policies {
			plan, err := prepare(context.Background(), jobRequest(out, policy, png(source)), files)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			assertItems(t, plan, []expect{want[policy]})
		}
	}
}

// TestPrepareReportsDeniedFolderCreation pins RSZ-030 with a real access
// failure: an output folder whose parent refuses new folders is reported as
// a permission failure of the folder.
func TestPrepareReportsDeniedFolderCreation(t *testing.T) {
	parent := t.TempDir()
	setDACL(t, parent, "D:P(D;;0x4;;;WD)(A;OICI;FA;;;WD)") // deny FILE_ADD_SUBDIRECTORY
	t.Cleanup(func() { setDACL(t, parent, "D:P(A;OICI;FA;;;WD)") })
	_, err := Prepare(context.Background(), jobRequest(filepath.Join(parent, "Resized"), imageresizer.CollisionRename,
		png(filepath.Join(t.TempDir(), "x.png"))))
	assertKind(t, err, KindFolder)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error = %v, want a permission failure", err)
	}
}

func setDACL(t *testing.T, path, sddl string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("parse %s: %v", sddl, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("read DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatalf("set DACL on %s: %v", path, err)
	}
}

// TestPrepareReportsInvalidFolderPaths pins RSZ-030 for paths Windows
// refuses: reserved characters and a drive that does not exist.
func TestPrepareReportsInvalidFolderPaths(t *testing.T) {
	source := png(filepath.Join(t.TempDir(), "x.png"))
	folders := []string{filepath.Join(t.TempDir(), `bad<name>`)}
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatalf("list drives: %v", err)
	}
	for letter := 'Z'; letter >= 'D'; letter-- {
		if drives&(1<<(letter-'A')) == 0 {
			folders = append(folders, string(letter)+`:\GameDevTools\Resized`)
			break
		}
	}
	for _, folder := range folders {
		_, err := Prepare(context.Background(), jobRequest(folder, imageresizer.CollisionRename, source))
		assertKind(t, err, KindFolder)
	}
}

//go:build windows

package output

import (
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func mustIdentify(t *testing.T, path string) fileID {
	t.Helper()
	id, _, err := identify(path)
	if err != nil {
		t.Fatalf("identify %s: %v", path, err)
	}
	return id
}

// junction links path to the target folder with a directory junction, which
// needs no privilege. It reports false when the volume cannot hold one.
func junction(t *testing.T, path, target string) bool {
	t.Helper()
	output, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		t.Logf("create junction: %v: %s", err, output)
		return false
	}
	// Remove the junction itself before the temporary folders, so their
	// cleanup never walks into the target through it.
	t.Cleanup(func() { os.Remove(path) })
	return true
}

// mustJunction is junction for a test that needs one, which it skips when
// the volume cannot hold one.
func mustJunction(t *testing.T, path, target string) {
	t.Helper()
	if !junction(t, path, target) {
		t.Skip("the volume cannot hold a junction")
	}
}

// fsctlSetReparsePoint is FSCTL_SET_REPARSE_POINT.
const fsctlSetReparsePoint = 0x000900A4

// dataReparsePoint gives the file at path a reparse point that is no link,
// with a tag no filter handles, the way a sync client marks the files it
// keeps. Since Go 1.23 os.Lstat reports such a file as irregular. Files the
// user owns accept such a reparse point without any privilege.
func dataReparsePoint(t *testing.T, path string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer windows.CloseHandle(handle)
	// REPARSE_GUID_DATA_BUFFER: tag, data length, reserved, GUID, data.
	const data = "test"
	buffer := make([]byte, 24+len(data))
	binary.LittleEndian.PutUint32(buffer[0:], 0x00000099) // a third-party tag, no name surrogate
	binary.LittleEndian.PutUint16(buffer[4:], uint16(len(data)))
	copy(buffer[8:24], "GameDevToolsTest")
	copy(buffer[24:], data)
	var returned uint32
	if err := windows.DeviceIoControl(handle, fsctlSetReparsePoint, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
		t.Skipf("set a reparse point on %s: %v", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Type() != fs.ModeIrregular {
		t.Skipf("os.Lstat reports the reparse point as %v (%v), not as irregular", info, err)
	}
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
	// A volume that cannot hold a junction leaves that alias out; the other
	// checks still run.
	if linked := filepath.Join(t.TempDir(), "junction"); junction(t, linked, folder) {
		aliases = append(aliases, filepath.Join(linked, "Original.png"))
	}
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
	if _, _, err := identify(filepath.Join(folder, "missing.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("identify(missing) = %v, want a not-exist error", err)
	}
	if _, _, err := identify(filepath.Join(folder, "missing", "x.png")); !errors.Is(err, fs.ErrNotExist) {
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

// TestIdentifyResolvesPathsAsTheOSPackageDoes: a short or long path reaches
// the file os.Lstat and the decoder reach, even when Windows drops a
// trailing space or dot from its last name, the path uses forward slashes,
// or it is marked extended with forward slashes. A source listed that way is
// therefore still recognized by its identity, and a confirmed Overwrite
// never replaces it.
func TestIdentifyResolvesPathsAsTheOSPackageDoes(t *testing.T) {
	short := filepath.Join(t.TempDir(), "shallow.png")
	writeFile(t, short, "pixels")
	folder := t.TempDir()
	for len(folder) < 300 {
		folder = filepath.Join(folder, strings.Repeat("d", 40))
	}
	path := filepath.Join(folder, "deep.png")
	writeFile(t, path, "pixels")
	for _, file := range []string{short, path} {
		want := mustIdentify(t, file)
		for _, alias := range []string{file + " ", file + ".", filepath.ToSlash(file), `//?/` + filepath.ToSlash(file)} {
			if _, err := os.Lstat(alias); err != nil {
				t.Fatalf("os.Lstat(%q): %v", alias, err)
			}
			if got := mustIdentify(t, alias); got != want {
				t.Errorf("identify(%q) = %+v, want %+v", alias, got, want)
			}
		}
	}

	// The listed path's key differs from the natural destination's, so only
	// the identity shows that deep.png is the source.
	plan := mustPrepare(t, jobRequest(folder, CollisionOverwrite, png(path+" ")))
	assertItems(t, plan, []expect{{"deep_2.png", ActionCreate, ReasonSource}})
	materialize(t, plan, "result")
	if readFile(t, path) != "pixels" {
		t.Fatal("the source listed with a trailing space was overwritten")
	}
}

func TestExtendedPath(t *testing.T) {
	long := `C:\` + strings.Repeat(`folder\`, 40) + "x.png"
	share := `\\server\share\` + strings.Repeat(`folder\`, 40) + "x.png"
	tests := []struct{ path, want string }{
		// Every absolute path is resolved and marked, whatever its length,
		// so no threshold of the os package's decides how it is read.
		{`C:\short\x.png`, `\\?\C:\short\x.png`},
		{`C:\short\x.png .`, `\\?\C:\short\x.png`},
		{`C:/short/./x.png`, `\\?\C:\short\x.png`},
		{`C:\`, `\\?\C:\`},
		{`\\server\share\x.png`, `\\?\UNC\server\share\x.png`},
		{`\\.\C:\x.png`, `\\.\C:\x.png`},
		{`short\x.png`, `short\x.png`},
		{long, `\\?\` + long},
		{share, `\\?\UNC\server\share\` + strings.Repeat(`folder\`, 40) + "x.png"},
		{`\\?\` + long, `\\?\` + long},
		{`\??\` + long, `\??\` + long},
		{`//?/` + filepath.ToSlash(long), `//?/` + filepath.ToSlash(long)},
		{filepath.ToSlash(long), `\\?\` + long},
		{strings.Replace(long, `\x.png`, `\.\x.png`, 1), `\\?\` + long},
		// GetFullPathName drops trailing spaces and dots from the last name,
		// as Windows does for every path it is not told to take verbatim.
		{long + " ", `\\?\` + long},
		{long + ". .", `\\?\` + long},
	}
	for _, test := range tests {
		if got := extendedPath(test.path); got != test.want {
			t.Errorf("extendedPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

// TestFindEntry: the folder lists a plain file, and a file whose reparse
// point is no link, as files, and a folder or a junction as neither, each
// under its own name, even when the path reaches it through its 8.3 short
// name or a long path.
func TestFindEntry(t *testing.T) {
	folder := t.TempDir()
	assertEntry := func(path string, want entry) {
		t.Helper()
		if got, err := findEntry(path); err != nil || got != want {
			t.Fatalf("findEntry(%s) = %+v, %v, want %+v", path, got, err, want)
		}
	}
	plain := filepath.Join(folder, "Plain photograph.png")
	writeFile(t, plain, "pixels")
	assertEntry(plain, entry{name: "Plain photograph.png", file: true})
	assertEntry(strings.ToUpper(plain), entry{name: "Plain photograph.png", file: true})
	if short, err := shortName(plain); err == nil && !strings.EqualFold(filepath.Base(short), filepath.Base(plain)) {
		assertEntry(short, entry{name: "Plain photograph.png", file: true})
	}
	assertEntry(folder, entry{name: filepath.Base(folder)})
	if linked := filepath.Join(t.TempDir(), "junction"); junction(t, linked, folder) {
		assertEntry(linked, entry{name: "junction"})
	}
	if _, err := findEntry(filepath.Join(folder, "missing.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("findEntry(missing) = %v, want a not-exist error", err)
	}

	deep := folder
	for len(deep) < 300 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	long := filepath.Join(deep, "deep.png")
	writeFile(t, long, "pixels")
	assertEntry(long, entry{name: "deep.png", file: true})

	marked := filepath.Join(folder, "marked.png")
	writeFile(t, marked, "pixels")
	dataReparsePoint(t, marked)
	assertEntry(marked, entry{name: "marked.png", file: true})
}

// TestPrepareClassifiesReparsePointsAtDestinations: a junction with a
// destination's name is never replaced, while a file with a reparse point
// that is no link is a file: a confirmed Overwrite considers replacing it,
// and renames here only because the file cannot be opened to read its
// identity, as no filter handles the test's reparse tag.
func TestPrepareClassifiesReparsePointsAtDestinations(t *testing.T) {
	t.Run("junction", func(t *testing.T) {
		out := t.TempDir()
		mustJunction(t, filepath.Join(out, "x.png"), t.TempDir())
		want := map[Collision]expect{
			CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
			CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
			CollisionOverwrite: {"x_2.png", ActionCreate, ReasonExists},
		}
		for _, policy := range policies {
			plan := mustPrepare(t, jobRequest(out, policy, png(filepath.Join(t.TempDir(), "x.png"))))
			assertItems(t, plan, []expect{want[policy]})
		}
	})

	t.Run("data reparse point", func(t *testing.T) {
		out := t.TempDir()
		destination := filepath.Join(out, "x.png")
		writeFile(t, destination, "earlier")
		dataReparsePoint(t, destination)
		want := map[Collision]expect{
			CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
			CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
			CollisionOverwrite: {"x_2.png", ActionCreate, ReasonUnverified},
		}
		for _, policy := range policies {
			plan := mustPrepare(t, jobRequest(out, policy, png(filepath.Join(t.TempDir(), "x.png"))))
			assertItems(t, plan, []expect{want[policy]})
		}
	})
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
		if policy == CollisionSkip {
			assertItems(t, plan, []expect{{"Hero.png", ActionSkip, ReasonSource}})
		} else {
			assertItems(t, plan, []expect{{"Hero_2.png", ActionCreate, ReasonSource}})
		}

		plan = mustPrepare(t, jobRequest(existing, policy, png(filepath.Join(t.TempDir(), "sky.png"))))
		switch policy {
		case CollisionRename:
			assertItems(t, plan, []expect{{"sky_2.png", ActionCreate, ReasonExists}})
		case CollisionSkip:
			assertItems(t, plan, []expect{{"sky.png", ActionSkip, ReasonExists}})
		case CollisionOverwrite:
			assertItems(t, plan, []expect{{"sky.png", ActionReplace, ReasonNone}})
		}
	}
}

// TestPrepareRecognizesSourcesUnderOtherNames pins RSZ-026 for aliases a
// path comparison cannot see: under Overwrite, a destination that is a hard
// link to a source, or that reaches a source through a junction, is that
// source and is never replaced. Rename and Skip, which never replace, treat
// it as any existing file.
func TestPrepareRecognizesSourcesUnderOtherNames(t *testing.T) {
	want := map[Collision]expect{
		CollisionRename:    {"x_2.png", ActionCreate, ReasonExists},
		CollisionSkip:      {"x.png", ActionSkip, ReasonExists},
		CollisionOverwrite: {"x_2.png", ActionCreate, ReasonSource},
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
			mustJunction(t, out, in)
			plan := mustPrepare(t, jobRequest(out, policy, png(source)))
			assertItems(t, plan, []expect{want[policy]})
			materialize(t, plan, "result")
			if readFile(t, source) != "source x" {
				t.Fatalf("%s: the source reached through a junction was overwritten", policy)
			}
		}
	})

	// A listed file the job does not process is protected by identity too.
	t.Run("protected hard link", func(t *testing.T) {
		in, out := t.TempDir(), t.TempDir()
		unprocessed := filepath.Join(in, "too-large.png")
		writeFile(t, unprocessed, "too large to resize")
		if err := os.Link(unprocessed, filepath.Join(out, "x.png")); err != nil {
			t.Fatalf("hard-link: %v", err)
		}
		request := jobRequest(out, CollisionOverwrite, png(filepath.Join(in, "x.png")))
		request.Protected = []string{unprocessed}
		plan := mustPrepare(t, request)
		assertItems(t, plan, []expect{{"x_2.png", ActionCreate, ReasonSource}})
		materialize(t, plan, "result")
		if readFile(t, unprocessed) != "too large to resize" {
			t.Fatal("the hard-linked protected file was overwritten")
		}
	})

	// Overwrite replaces an existing file under a numbered name only when
	// it is no source either.
	t.Run("numbered hard link", func(t *testing.T) {
		in, out := t.TempDir(), t.TempDir()
		source := filepath.Join(in, "other.png")
		writeFile(t, source, "source other")
		if err := os.Link(source, filepath.Join(out, "x_2.png")); err != nil {
			t.Fatalf("hard-link: %v", err)
		}
		plan := mustPrepare(t, jobRequest(out, CollisionOverwrite,
			png(filepath.Join(t.TempDir(), "x.png")),
			png(filepath.Join(t.TempDir(), "x.png")),
			png(source),
		))
		assertItems(t, plan, []expect{
			{"x.png", ActionCreate, ReasonNone},
			{"x_3.png", ActionCreate, ReasonDuplicate},
			{"other.png", ActionCreate, ReasonNone},
		})
		materialize(t, plan, "result")
		if readFile(t, source) != "source other" {
			t.Fatal("the hard-linked source under a numbered name was overwritten")
		}
	})
}

// TestPrepareNeverReplacesAFileThroughItsShortName pins AC-R10 for 8.3
// names: a natural destination that is only the short name of an existing
// file with another long name, such as PHOTOG~1.png for photograph.png,
// does not name that file, though Windows opens it through the short name,
// so a confirmed Overwrite renames the result rather than replace a file the
// job was never asked to replace. Rename and Skip treat it as any existing
// file.
func TestPrepareNeverReplacesAFileThroughItsShortName(t *testing.T) {
	for _, policy := range policies {
		out := t.TempDir()
		existing := filepath.Join(out, "photograph.png")
		writeFile(t, existing, "unlisted original")
		short, err := shortName(existing)
		if err != nil || strings.EqualFold(filepath.Base(short), filepath.Base(existing)) {
			t.Skipf("the volume gives %s no short name (%q, %v)", existing, short, err)
		}
		alias := strings.TrimSuffix(filepath.Base(short), filepath.Ext(short))
		want := map[Collision]expect{
			CollisionRename:    {alias + "_2.png", ActionCreate, ReasonExists},
			CollisionSkip:      {alias + ".png", ActionSkip, ReasonExists},
			CollisionOverwrite: {alias + "_2.png", ActionCreate, ReasonExists},
		}
		plan := mustPrepare(t, jobRequest(out, policy, png(filepath.Join(t.TempDir(), alias+".png"))))
		assertItems(t, plan, []expect{want[policy]})
		materialize(t, plan, "result")
		if readFile(t, existing) != "unlisted original" {
			t.Fatalf("%s replaced %s through its short name %s", policy, existing, alias+".png")
		}
	}
}

// TestPrepareRenamesWhenIdentityIsUnreadable: Overwrite replaces only a
// file it can prove is not a source. When the destination's identity cannot
// be read, the result is renamed instead, and the destination is read once
// however many items it is the natural destination of; the other policies
// never replace and are unaffected.
func TestPrepareRenamesWhenIdentityIsUnreadable(t *testing.T) {
	out := t.TempDir()
	destination := filepath.Join(out, "x.png")
	writeFile(t, destination, "earlier")
	sources := []Source{png(filepath.Join(t.TempDir(), "x.png")), png(filepath.Join(t.TempDir(), "x.png"))}
	want := map[Collision][]expect{
		CollisionRename: {{"x_2.png", ActionCreate, ReasonExists}, {"x_3.png", ActionCreate, ReasonExists}},
		CollisionSkip:   {{"x.png", ActionSkip, ReasonExists}, {"x.png", ActionSkip, ReasonExists}},
		CollisionOverwrite: {
			{"x_2.png", ActionCreate, ReasonUnverified},
			{"x_3.png", ActionCreate, ReasonUnverified},
		},
	}
	for _, policy := range policies {
		reads := 0
		files := faultyFiles{identity: func(path string) (fileID, uint32, error) {
			if strings.EqualFold(path, destination) {
				reads++
				return fileID{}, 0, &fs.PathError{Op: "open", Path: path, Err: windows.ERROR_ACCESS_DENIED}
			}
			return identify(path)
		}}
		plan, err := prepare(context.Background(), jobRequest(out, policy, sources...), files)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		assertItems(t, plan, want[policy])
		if wantReads := map[bool]int{true: 1}[policy == CollisionOverwrite]; reads != wantReads {
			t.Fatalf("%s read the destination's identity %d times, want %d", policy, reads, wantReads)
		}
	}
}

// deny gives the file a protected, empty DACL, as an original copied from
// another account can carry, so the user may not even read its attributes
// and its identity cannot be read. Its folder still lists it.
func deny(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, "inaccessible original")
	setDACL(t, path, "D:P")
	t.Cleanup(func() { setDACL(t, path, "D:P(A;;FA;;;WD)") })
	if _, _, err := identify(path); err == nil {
		t.Fatalf("the identity of %s can be read despite its empty DACL", path)
	}
}

// missingDrive returns the root of a drive letter no volume is mounted at,
// or false when every letter is in use.
func missingDrive(t *testing.T) (string, bool) {
	t.Helper()
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatalf("list drives: %v", err)
	}
	for letter := 'Z'; letter >= 'D'; letter-- {
		if drives&(1<<(letter-'A')) == 0 {
			return string(letter) + `:\`, true
		}
	}
	return "", false
}

// TestPrepareRenamesOnlyWhereAnUnreadableListedFileMayBe pins RSZ-026 and
// AC-R10 for listed files whose identity cannot be read. Such a file, when
// it is a plain file in a folder that can be read, has its name in that
// folder, so a confirmed Overwrite still replaces an existing file with a
// single name elsewhere; it renames only a file that may be the listed one:
// one in the same folder, or one with more names. A listed file that cannot
// be located — its folder, drive, or share cannot be reached, as a removed
// SUBST drive or link leaves while the file remains under another path — may
// be any file, so it is never taken for a missing one and nothing is
// replaced; only a file missing from a folder that exists is gone.
func TestPrepareRenamesOnlyWhereAnUnreadableListedFileMayBe(t *testing.T) {
	plan := func(t *testing.T, out string, files fileSystem, protected string) Plan {
		t.Helper()
		request := jobRequest(out, CollisionOverwrite, png(filepath.Join(t.TempDir(), "x.png")))
		request.Protected = []string{protected}
		plan, err := prepare(context.Background(), request, files)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		materialize(t, plan, "result")
		return plan
	}
	outputFolder := func(t *testing.T) string {
		out := t.TempDir()
		writeFile(t, filepath.Join(out, "x.png"), "earlier")
		return out
	}
	replaced := []expect{{"x.png", ActionReplace, ReasonNone}}
	renamed := []expect{{"x_2.png", ActionCreate, ReasonUnverified}}

	t.Run("inaccessible file in another folder", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "denied.png")
		deny(t, protected)
		assertItems(t, plan(t, outputFolder(t), osFiles{}, protected), replaced)
	})

	t.Run("inaccessible file in the output folder", func(t *testing.T) {
		out := outputFolder(t)
		protected := filepath.Join(out, "denied.png")
		deny(t, protected)
		assertItems(t, plan(t, out, osFiles{}, protected), renamed)
	})

	t.Run("inaccessible file and a destination with two names", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "denied.png")
		deny(t, protected)
		out := outputFolder(t)
		if err := os.Link(filepath.Join(out, "x.png"), filepath.Join(t.TempDir(), "elsewhere.png")); err != nil {
			t.Fatalf("hard-link: %v", err)
		}
		assertItems(t, plan(t, out, osFiles{}, protected), renamed)
	})

	t.Run("folder that cannot be found", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "gone", "x.png")
		assertItems(t, plan(t, outputFolder(t), osFiles{}, protected), renamed)
	})

	t.Run("drive that cannot be found", func(t *testing.T) {
		root, ok := missingDrive(t)
		if !ok {
			t.Skip("every drive letter is in use")
		}
		protected := filepath.Join(root, "Pictures", "x.png")
		assertItems(t, plan(t, outputFolder(t), osFiles{}, protected), renamed)
	})

	t.Run("share that cannot be found", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "share", "x.png")
		unreachable := func(path string) error {
			return &fs.PathError{Op: "open", Path: path, Err: windows.ERROR_BAD_NETPATH}
		}
		files := faultyFiles{
			identity: func(path string) (fileID, uint32, error) {
				if strings.EqualFold(path, protected) {
					return fileID{}, 0, unreachable(path)
				}
				return identify(path)
			},
			lstat: func(path string) (fs.FileInfo, error) {
				if strings.EqualFold(path, protected) {
					return nil, unreachable(path)
				}
				return os.Lstat(path)
			},
		}
		assertItems(t, plan(t, outputFolder(t), files, protected), renamed)
	})

	t.Run("inaccessible link", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "link.png")
		writeFile(t, protected, "stands in for a link")
		files := faultyFiles{
			identity: func(path string) (fileID, uint32, error) {
				if strings.EqualFold(path, protected) {
					return fileID{}, 0, &fs.PathError{Op: "open", Path: path, Err: windows.ERROR_ACCESS_DENIED}
				}
				return identify(path)
			},
			lstat: func(path string) (fs.FileInfo, error) {
				if strings.EqualFold(path, protected) {
					return fakeInfo{name: filepath.Base(path), mode: fs.ModeSymlink}, nil
				}
				return os.Lstat(path)
			},
		}
		assertItems(t, plan(t, outputFolder(t), files, protected), renamed)
	})

	// A file whose reparse point only says how its data is stored, such as
	// one OneDrive keeps in sync, has its name in its folder like any other
	// file. No filter handles the test's reparse tag, so its identity cannot
	// be read, while its reparse point can.
	storedOriginal := func(t *testing.T, path string) {
		t.Helper()
		writeFile(t, path, "synced original")
		dataReparsePoint(t, path)
		if _, _, err := identify(path); err == nil {
			t.Skipf("the identity of %s can be read", path)
		}
	}

	t.Run("inaccessible stored file in another folder", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "synced.png")
		storedOriginal(t, protected)
		assertItems(t, plan(t, outputFolder(t), osFiles{}, protected), replaced)
	})

	t.Run("inaccessible stored file in the output folder", func(t *testing.T) {
		out := outputFolder(t)
		protected := filepath.Join(out, "synced.png")
		storedOriginal(t, protected)
		assertItems(t, plan(t, out, osFiles{}, protected), renamed)
	})

	t.Run("file missing from its folder", func(t *testing.T) {
		protected := filepath.Join(t.TempDir(), "gone.png")
		assertItems(t, plan(t, outputFolder(t), osFiles{}, protected), replaced)
	})
}

// TestPrepareReadsEachIdentityOnce: a confirmed Overwrite job reads each
// path's identity once. A destination is opened once for both its identity
// and its number of names, and the folder holding several listed files
// whose identities cannot be read is read once for all of them.
func TestPrepareReadsEachIdentityOnce(t *testing.T) {
	in := t.TempDir()
	var protected []string
	denied := map[string]bool{}
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		path := filepath.Join(in, name)
		writeFile(t, path, "inaccessible original")
		protected = append(protected, path)
		denied[PathKey(path)] = true
	}
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "x.png"), "earlier")
	reads := map[string]int{}
	files := faultyFiles{identity: func(path string) (fileID, uint32, error) {
		reads[PathKey(path)]++
		if denied[PathKey(path)] {
			return fileID{}, 0, &fs.PathError{Op: "open", Path: path, Err: windows.ERROR_ACCESS_DENIED}
		}
		return identify(path)
	}}
	request := jobRequest(out, CollisionOverwrite, png(filepath.Join(t.TempDir(), "x.png")))
	request.Protected = protected
	plan, err := prepare(context.Background(), request, files)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertItems(t, plan, []expect{{"x.png", ActionReplace, ReasonNone}})
	if reads[PathKey(in)] != 1 || reads[PathKey(filepath.Join(out, "x.png"))] != 1 || reads[PathKey(out)] != 1 {
		t.Fatalf("identity reads = %v, want one of each path", reads)
	}
}

// TestPrepareStopsReadingIdentitiesOnceNothingCanBeReplaced: a listed file
// that cannot be located may be any existing file, so a confirmed Overwrite
// renames whatever it finds, and the identities of the listed files after it
// are not read: they could only change why a result is renamed.
func TestPrepareStopsReadingIdentitiesOnceNothingCanBeReplaced(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(out, "x.png"), "earlier")
	request := jobRequest(out, CollisionOverwrite, png(filepath.Join(in, "gone", "x.png")))
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		path := filepath.Join(in, name)
		writeFile(t, path, "listed")
		request.Protected = append(request.Protected, path)
	}
	reads := 0
	files := faultyFiles{identity: func(path string) (fileID, uint32, error) {
		if strings.EqualFold(filepath.Dir(path), in) {
			reads++
		}
		return identify(path)
	}}
	plan, err := prepare(context.Background(), request, files)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertItems(t, plan, []expect{{"x_2.png", ActionCreate, ReasonUnverified}})
	if reads != 0 {
		t.Fatalf("read %d identities of listed files after one that cannot be located, want none", reads)
	}
}

// TestPrepareReportsDeniedFolderCreation pins RSZ-030 with a real access
// failure: an output folder whose parent refuses new folders is reported as
// a permission failure of the folder.
func TestPrepareReportsDeniedFolderCreation(t *testing.T) {
	parent := t.TempDir()
	setDACL(t, parent, "D:P(D;;0x4;;;WD)(A;OICI;FA;;;WD)") // deny FILE_ADD_SUBDIRECTORY
	t.Cleanup(func() { setDACL(t, parent, "D:P(A;OICI;FA;;;WD)") })
	_, err := Prepare(context.Background(), jobRequest(filepath.Join(parent, "Resized"), CollisionRename,
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
// refuses: reserved characters and a drive that does not exist. Neither is a
// file in the way, though Windows reports a missing drive with the error
// code os.MkdirAll also returns for one.
func TestPrepareReportsInvalidFolderPaths(t *testing.T) {
	source := png(filepath.Join(t.TempDir(), "x.png"))
	folders := []string{filepath.Join(t.TempDir(), `bad<name>`)}
	if root, ok := missingDrive(t); ok {
		folders = append(folders, filepath.Join(root, "GameDevTools", "Resized"))
	}
	for _, folder := range folders {
		_, err := Prepare(context.Background(), jobRequest(folder, CollisionRename, source))
		assertKind(t, err, KindFolder)
		if errors.Is(err, ErrNotFolder) {
			t.Errorf("%s: error = %v, reported as a file in the way", folder, err)
		}
	}
}

// TestPrepareRefusesFolderNamesWindowsChanges: Windows drops a trailing
// space or period from a folder it creates, but not always from a path that
// leads through it, so a missing folder with such a name on the way to the
// output folder is refused before anything is created, rather than left
// behind as a stray folder under the shortened name.
func TestPrepareRefusesFolderNamesWindowsChanges(t *testing.T) {
	source := png(filepath.Join(t.TempDir(), "x.png"))
	for _, name := range []string{"sub ", "sub.", "sub..", "sub. ", "sub ."} {
		parent := t.TempDir()
		_, err := Prepare(context.Background(), jobRequest(filepath.Join(parent, name, "Resized"), CollisionRename, source))
		assertKind(t, err, KindValidation)
		if !errors.Is(err, ErrFolderName) {
			t.Fatalf("%q: error = %v, want ErrFolderName", name, err)
		}
		if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
			t.Fatalf("%q: the parent holds %v (%v) after a refused request", name, entries, err)
		}
	}
}

// TestPrepareTakesExtendedLengthFoldersVerbatim: Windows changes no name in
// an extended-length path, so an output folder whose name ends in a period
// is created and planned into under exactly that name.
func TestPrepareTakesExtendedLengthFoldersVerbatim(t *testing.T) {
	parent := t.TempDir()
	out := `\\?\` + filepath.Join(parent, "out.")
	// os.RemoveAll cannot remove a folder named out. through the temporary
	// folder's ordinary path, so the test removes it, after its files.
	t.Cleanup(func() {
		entries, _ := os.ReadDir(out)
		for _, file := range entries {
			os.Remove(filepath.Join(out, file.Name()))
		}
		os.Remove(out)
	})
	plan := mustPrepare(t, jobRequest(out, CollisionRename, png(filepath.Join(t.TempDir(), "x.png"))))
	if plan.Folder != out {
		t.Fatalf("Folder = %q, want %q", plan.Folder, out)
	}
	assertItems(t, plan, []expect{{"x.png", ActionCreate, ReasonNone}})
	materialize(t, plan, "result")
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "out." || !entries[0].IsDir() {
		t.Fatalf("the parent holds %v (%v), want only the folder out.", entries, err)
	}
}

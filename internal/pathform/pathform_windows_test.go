//go:build windows

package pathform

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// TestReadsAsOneFolder pins which output folders Windows reads as one place
// whether a save names the folder itself, a file after it, or a file through
// Join.
func TestReadsAsOneFolder(t *testing.T) {
	for _, test := range []struct {
		folder string
		want   bool
	}{
		{`C:\Art\Shots`, true},
		{`C:\Art\Shots\`, true},
		{`C:/Art/Shots`, true},
		{`C:\`, true},
		{`C:\Art\Shots.`, true},
		{`C:\Art\Photos \Shots`, true},
		{`C:\Art\v2..\Shots`, true},
		{`\\server\share`, true},
		{`\\server\share\Shots`, true},
		{`\\?\C:\Art\Shots`, true},
		{`\\?\C:\Art\Shots `, true},
		{`\??\C:\Art\Shots`, true},
		{`\\?\UNC\server\share\Shots`, true},
		{`\\.\UNC\server\share\Shots`, true},
		{`C:\Art\Shots `, false},
		{`C:\Art\Shots..`, false},
		{`\\.\UNC\server\share\..\x`, false},
		{`\\?\C:\Art\..\Shots`, false},
	} {
		if got := ReadsAsOneFolder(test.folder); got != test.want {
			t.Errorf("ReadsAsOneFolder(%q) = %v, want %v", test.folder, got, test.want)
		}
	}
}

// TestCreationPathKeepsNamesWindowsWouldShorten pins that a folder through a
// name ending in a space or a period is created by its extended-length path,
// where os.MkdirAll cannot lose those characters, and any other folder by its
// path as written, so a failure names it as the user knows it.
func TestCreationPathKeepsNamesWindowsWouldShorten(t *testing.T) {
	for _, test := range []struct{ folder, want string }{
		{`C:\Art\Shots`, `C:\Art\Shots`},
		{`C:\Art\Shots\`, `C:\Art\Shots\`},
		{`C:\Art\Shots.`, `C:\Art\Shots.`},
		{`\\server\share\Shots`, `\\server\share\Shots`},
		{`\\?\C:\Art\Photos \Shots`, `\\?\C:\Art\Photos \Shots`},
		{`C:\Art\Photos \Shots`, `\\?\C:\Art\Photos \Shots`},
		{`C:\Art\v2..\Shots`, `\\?\C:\Art\v2..\Shots`},
		{`\\server\share\Photos \Shots`, `\\?\UNC\server\share\Photos \Shots`},
		{`\\.\C:\Art\Photos \Shots`, `\\?\C:\Art\Photos \Shots`},
		{`\\.\C:\Art\Shots`, `\\.\C:\Art\Shots`},
		{`\\server\..\x`, `\\server\..\x`},
	} {
		if got := CreationPath(test.folder); got != test.want {
			t.Errorf("CreationPath(%q) = %q, want %q", test.folder, got, test.want)
		}
	}
}

// TestSaveFolderReachesARealFolderByItsOwnName pins that a folder whose own
// name ends in a space or a second period is used through a spelling every
// step reads whole while it exists, and that a path read two ways with no
// such folder has no spelling.
func TestSaveFolderReachesARealFolderByItsOwnName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Shots ", "v2.."} {
		if err := os.Mkdir(`\\?\`+root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
		folder := root + `\` + name
		if got, ok := SaveFolder(folder); !ok || got != folder+`\` {
			t.Errorf("SaveFolder(%q) = %q, %v, want %q", folder, got, ok, folder+`\`)
		}
	}
	if err := os.WriteFile(`\\?\`+root+`\File `, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{root, root + `\`, root + `\Missing\Shots`} {
		if got, ok := SaveFolder(folder); !ok || got != folder {
			t.Errorf("SaveFolder(%q) = %q, %v, want it as written", folder, got, ok)
		}
	}
	for _, folder := range []string{
		"",
		root + `\Missing `,
		root + `\File `,
		`\\.\UNC\server\share\..\x`,
		`\\?\C:\Art\..\Shots`,
	} {
		if got, ok := SaveFolder(folder); ok {
			t.Errorf("SaveFolder(%q) = %q, want no spelling", folder, got)
		}
	}
}

// TestSaveFolderRefusesANameItsSpellingWouldShorten pins that a name ending
// in a space and then a period gets no spelling, even beside a folder without
// the period, which is where a separator after that name leads.
func TestSaveFolderRefusesANameItsSpellingWouldShorten(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"x .", "x "} {
		if err := os.Mkdir(`\\?\`+root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := SaveFolder(root + `\x .`); ok {
		t.Errorf("SaveFolder = %q, which leads to the folder without the period", got)
	}
	if got, ok := SaveFolder(root + `\x `); !ok || got != root+`\x \` {
		t.Errorf("SaveFolder of the folder ending in a space = %q, %v", got, ok)
	}
	// The name loses its period wherever it falls on the path.
	for _, rest := range []string{
		`\x .\`, `\x .\Shots`, `/x ./Shots`, `\x. .\Shots`, `\x  .\Shots`, `\ .\Shots`,
		`\x . .\Shots`, `\Deep\x .\In\Shots`, `\x .\In\..\Shots`,
	} {
		if got, ok := SaveFolder(root + rest); ok {
			t.Errorf("SaveFolder(%q) = %q, which leads through the name without the period", root+rest, got)
		}
	}
	// A name a ".." removes, one Windows keeps whole, and a verbatim path
	// are not that name.
	for _, folder := range []string{
		root + `\x .\..\Shots`,
		root + `\x ..\Shots`,
		root + `\x .  \Shots`,
		`\\?\` + root + `\x .\Shots`,
	} {
		if got, ok := SaveFolder(folder); !ok || got != folder {
			t.Errorf("SaveFolder(%q) = %q, %v, want it as written", folder, got, ok)
		}
	}
}

// TestSaveFolderTellsABrokenLinkFromAMissingName pins that a folder behind a
// junction whose target is gone, which may come back, keeps its spelling so
// the save reports the failure, while a name missing under a live junction or
// blocked by a file has none.
func TestSaveFolderTellsABrokenLinkFromAMissingName(t *testing.T) {
	root := t.TempDir()
	link := func(path, target string) {
		t.Helper()
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
			t.Skipf("the volume cannot hold a junction: %v: %s", err, output)
		}
		t.Cleanup(func() { os.Remove(path) })
	}
	for _, name := range []string{"gone", "live"} {
		if err := os.Mkdir(root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link(root+`\broken`, root+`\gone`)
	link(root+`\working`, root+`\live`)
	// mklink drops a trailing space, so that name is given by a rename.
	link(root+`\spaced`, root+`\gone`)
	if err := os.Rename(root+`\spaced`, `\\?\`+root+`\broken `); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(`\\?\` + root + `\broken `) })
	if err := os.Remove(root + `\gone`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+`\Plain`, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{root + `\broken\Shots `, root + `\broken\In\Deep `, root + `\broken `} {
		if got, ok := SaveFolder(folder); !ok || got != folder+`\` {
			t.Errorf("SaveFolder(%q) = %q, %v, want %q", folder, got, ok, folder+`\`)
		}
	}
	for _, folder := range []string{root + `\working\Shots `, root + `\working\In\Deep `, root + `\Plain\Shots `} {
		if got, ok := SaveFolder(folder); ok {
			t.Errorf("SaveFolder(%q) = %q, want no spelling", folder, got)
		}
	}
}

// TestNoSuchFolderTellsMissingFromUnreachable pins that only a folder missing
// from a volume that answers counts as missing, so an offline share or a
// removed drive is reported as what it is.
func TestNoSuchFolderTellsMissingFromUnreachable(t *testing.T) {
	root := t.TempDir()
	for _, code := range []windows.Errno{windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND, windows.ERROR_DIRECTORY} {
		if !noSuchFolder(root+`\Missing \`, &os.PathError{Op: "stat", Path: root, Err: code}) {
			t.Errorf("error %d on a drive that answers does not count as missing", code)
		}
	}
	for _, code := range []windows.Errno{
		windows.ERROR_BAD_NETPATH, windows.ERROR_BAD_NET_NAME, windows.ERROR_ACCESS_DENIED, windows.ERROR_NOT_READY,
	} {
		if noSuchFolder(root+`\Missing \`, &os.PathError{Op: "stat", Path: root, Err: code}) {
			t.Errorf("error %d counts as a missing folder", code)
		}
	}
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatal(err)
	}
	for letter := 25; letter >= 2; letter-- {
		if drives&(1<<letter) == 0 {
			folder := string(rune('A'+letter)) + `:\Art\Shots \`
			if noSuchFolder(folder, &os.PathError{Op: "stat", Path: folder, Err: windows.ERROR_PATH_NOT_FOUND}) {
				t.Errorf("a folder on the absent drive %s counts as missing", folder[:2])
			}
			return
		}
	}
}

// TestPathsReadAlikeOnEveryGoRelease pins how a Windows path is read. Go
// 1.27's package filepath takes a \\?\UNC or \??\UNC path's server and share
// into its volume name, which would make a share's root look like a bare
// volume, keep a trailing separator, and append a period to a \??\UNC share's
// root, and it gives a path with ".." in its volume name no volume at all,
// which would make it relative and let Join move it to the current drive. The
// expected values are Go 1.26's, and hold whichever Go release builds the
// test.
func TestPathsReadAlikeOnEveryGoRelease(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{`\\?\UNC`, `\\?\UNC`},
		{`\\?\UNC\server\share`, `\\?\UNC\server\share`},
		{`\\?\UNC\server\share\`, `\\?\UNC\server\share`},
		{`\??\UNC\server\share`, `\??\UNC\server\share`},
		{`\??\unc\server\share\`, `\??\unc\server\share`},
		{`//?/UNC/server/share/a/../b`, `\\?\UNC\server\share\b`},
		{`\\?\UNC\server\share\..\x`, `\\?\UNC\server\x`},
		{`\\?\UNC\\server\share`, `\\?\UNC\server\share`},
		{`\\?\UNC\??\x`, `\\?\UNC\??\x`},
		{`\\?\UNCX\y\`, `\\?\UNCX\y`},
		{`C:\a\..\b`, `C:\b`},
		{`\\server\share\x\`, `\\server\share\x`},
		{`\\server\..`, `\\server\..`},
		{`\\server\..\`, `\\server\..\`},
		{`//server/../x/./y`, `\\server\..\x\y`},
		{`\\..\share\x\..\y`, `\\..\share\y`},
		{`\\.\..\x`, `\\.\..\x`},
		{`\\?\..\x\..`, `\\?\..\`},
		{`\??\..\x`, `\??\..\x`},
		{`\??\..`, `\??\...`},
		{`\\.\UNC\..\share\x`, `\\.\UNC\..\share\x`},
		{`\\.\UNC\server\..\x`, `\\.\UNC\server\..\x`},
	} {
		if got := Clean(test.path); got != test.want {
			t.Errorf("Clean(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	for _, test := range []struct{ path, want string }{
		{`\\?\UNC\server\share\x`, `\\?\UNC`},
		{`\??\unc\server`, `\??\unc`},
		{`//?/UNC/server/share`, `\\?\UNC`},
		{`\\server\share\x`, `\\server\share`},
		{`\\.\UNC\server\share\x`, `\\.\UNC\server\share`},
		{`\\?\C:\x`, `\\?\C:`},
		{`C:\x`, `C:`},
		{`x`, ``},
		{`\x`, ``},
		{`//server/../x`, `\\server\..`},
		{`\\.\..\x`, `\\.\..`},
		{`\??\..\x`, `\??\..`},
		{`\\.\UNC\server\..\x`, `\\.\UNC\server\..`},
	} {
		if got := VolumeName(test.path); got != test.want {
			t.Errorf("VolumeName(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	for _, test := range []struct {
		path string
		want bool
	}{
		{`\\?\UNC`, true},
		{`\\?\UNC\server\share`, true},
		{`\??\UNC\server\share`, true},
		{`\??\UNC\server`, true},
		{`\??\UNC`, false},
		{`C:\x`, true},
		{`C:x`, false},
		{`relative\x`, false},
		{`\rooted\x`, false},
		{`\\server\..\x`, true},
		{`//server/../x`, true},
		{`\\..\share\x`, true},
		{`\\.\..\x`, true},
		{`//?/../x`, true},
		{`\??\..\x`, true},
		{`\??\..`, false},
		{`\\.\UNC\server\..\x`, true},
	} {
		if got := IsAbs(test.path); got != test.want {
			t.Errorf("IsAbs(%q) = %v, want %v", test.path, got, test.want)
		}
	}
	for _, test := range []struct{ path, want string }{
		{`\\?\UNC\server\share\x.png`, `\\?\UNC\server\share`},
		{`\\?\UNC\server\share`, `\\?\UNC\server`},
		{`\\?\UNC\server`, `\\?\UNC\`},
		{`\\?\UNC\`, `\\?\UNC\`},
		{`\\?\UNC`, `\\?\UNC`},
		{`C:\a\b.png`, `C:\a`},
		{`\\server\..\x\y.png`, `\\server\..\x`},
		{`\\server\..\y.png`, `\\server\..\`},
		{`\\server\..`, `\\server\..`},
		{`\??\..\x\y.png`, `\??\..\x`},
		{`//x/x//../`, `\\x\x\\..\`},
		{`\\x\y\\z\w`, `\\x\y\\z\`},
		{`C:\\x\y`, `C:\\x\`},
	} {
		if got := Dir(test.path); got != test.want {
			t.Errorf("Dir(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	for _, test := range []struct{ path, want string }{
		{`\\?\UNC\server\share\x.png`, `x.png`},
		{`\\?\UNC\server\share`, `share`},
		{`\??\UNC\server\share`, `share`},
		{`//?/UNC/server/share`, `share`},
		{`C:\a\b.png`, `b.png`},
		{`\\server\..\x\y.png`, `y.png`},
		{`\\server\..\x\`, `x`},
		{`\\server\..`, `\`},
		{`\\server\..\`, `\`},
	} {
		if got := Base(test.path); got != test.want {
			t.Errorf("Base(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	for _, test := range []struct {
		folder string
		names  []string
		want   string
	}{
		{`C:\Art`, []string{`x.png`}, `C:\Art\x.png`},
		{`C:\`, []string{`x.png`}, `C:\x.png`},
		{`\\server\share`, []string{`x.png`}, `\\server\share\x.png`},
		{`\\?\UNC\server\share`, []string{`x.png`}, `\\?\UNC\server\share\x.png`},
		{`\??\UNC\nas\shots`, []string{`x.png`}, `\??\UNC\nas\shots\x.png`},
		{`\\server\..`, []string{`x.png`}, `\\server\..\x.png`},
		{`\\server\..\x`, []string{`y.png`}, `\\server\..\x\y.png`},
		{`\\server\..\`, []string{`a`, `b.png`}, `\\server\..\a\b.png`},
		{`//server/../x`, []string{`y.png`}, `\\server\..\x\y.png`},
		{`\\.\UNC\server\..\x`, []string{`y.png`}, `\\.\UNC\server\..\x\y.png`},
	} {
		if got := Join(test.folder, test.names...); got != test.want {
			t.Errorf("Join(%q, %q) = %q, want %q", test.folder, test.names, got, test.want)
		}
	}
}

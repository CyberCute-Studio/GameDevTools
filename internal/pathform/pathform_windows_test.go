//go:build windows

package pathform

import "testing"

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

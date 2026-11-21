//go:build windows

package output

import "testing"

// TestExtendedUNCPathsReadAlikeOnEveryGoRelease pins how the folder rules
// read a \\?\UNC or \??\UNC path. Go 1.27's package filepath takes such a
// path's server and share into its volume name, which would make a share's
// root look like a bare volume, keep a trailing separator, and append a
// period to a \??\UNC share's root. The expected values are what the rules
// were written for, and hold whichever Go release builds the test.
func TestExtendedUNCPathsReadAlikeOnEveryGoRelease(t *testing.T) {
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
	} {
		if got := cleanPath(test.path); got != test.want {
			t.Errorf("cleanPath(%q) = %q, want %q", test.path, got, test.want)
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
	} {
		if got := volumeName(test.path); got != test.want {
			t.Errorf("volumeName(%q) = %q, want %q", test.path, got, test.want)
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
	} {
		if got := isAbsolute(test.path); got != test.want {
			t.Errorf("isAbsolute(%q) = %v, want %v", test.path, got, test.want)
		}
	}
	for _, test := range []struct{ path, want string }{
		{`\\?\UNC\server\share\x.png`, `\\?\UNC\server\share`},
		{`\\?\UNC\server\share`, `\\?\UNC\server`},
		{`\\?\UNC\server`, `\\?\UNC\`},
		{`\\?\UNC\`, `\\?\UNC\`},
		{`C:\a\b.png`, `C:\a`},
	} {
		if got := parentPath(test.path); got != test.want {
			t.Errorf("parentPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	for _, test := range []struct{ path, want string }{
		{`\\?\UNC\server\share\x.png`, `x.png`},
		{`\\?\UNC\server\share`, `share`},
		{`\??\UNC\server\share`, `share`},
		{`C:\a\b.png`, `b.png`},
	} {
		if got := baseName(test.path); got != test.want {
			t.Errorf("baseName(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

//go:build windows

package pathform

import "testing"

// TestNamesNoFolder pins which output folders name a drive, volume, device,
// or server rather than a folder on it, in every spelling, and that such a
// path never counts as naming one folder, so a save never creates it.
func TestNamesNoFolder(t *testing.T) {
	for _, test := range []struct {
		folder string
		want   bool
	}{
		{`\\.\pipe\x`, true},
		{`//./pipe/x`, true},
		{`\\?\pipe\x`, true},
		{`\??\pipe\x`, true},
		{`\\.\COM1\x`, true},
		{`\\?\GLOBALROOT\Device\HarddiskVolume1\Shots`, true},
		{`\\?\C:`, true},
		{`\\.\C:`, true},
		{`\\server`, true},
		{`\\?\UNC\server`, true},
		{`\??\UNC\server`, true},
		{`\\.\UNC\server`, true},

		{`C:\`, false},
		{`C:\Art\Shots`, false},
		{`\\?\C:\`, false},
		{`\\?\C:\Art`, false},
		{`\\.\C:\Art`, false},
		{`\??\C:\Art`, false},
		{`\\server\share`, false},
		{`\\server\share\Shots`, false},
		{`\\?\UNC\server\share`, false},
		{`\\?\UNC\server\share\Shots`, false},
		{`\\.\UNC\server\share\Shots`, false},
		{`\\?\Volume{00000000-0000-0000-0000-000000000000}\Shots`, false},
		{`\\.\volume{00000000-0000-0000-0000-000000000000}\Shots`, false},
	} {
		if got := NamesNoFolder(test.folder); got != test.want {
			t.Errorf("NamesNoFolder(%q) = %v, want %v", test.folder, got, test.want)
		}
		if test.want && NamesOneFolder(test.folder) {
			t.Errorf("NamesOneFolder(%q) = true for a path that names no folder", test.folder)
		}
	}
}

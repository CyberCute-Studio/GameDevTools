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
		{`\\localhost\pipe\x`, true},
		{`\\127.0.0.1\pipe\x`, true},
		{`//localhost/pipe/x`, true},
		{`\\server\PIPE`, true},
		{`\\?\UNC\localhost\pipe\x`, true},
		{`\??\UNC\localhost\pipe\x`, true},
		{`\\.\UNC\localhost\pipe\x`, true},
		{`\\server\mailslot\x`, true},
		{`\\?\UNC\server\MailSlot\x`, true},
		{`\\server\IPC$`, true},
		{`\\server\ipc$\x`, true},
		{`\\\localhost\pipe\x`, true},
		{`///localhost/pipe/x`, true},
		{`\/\localhost\pipe\x`, true},
		{`\\\server\IPC$`, true},
		{`\\\\server\share\Shots`, true},
		{`\\?\UNC\server\pipe \x`, true},
		{`\\?\UNC\server\pipe.\x`, true},
		{`\??\UNC\server\MailSlot. \x`, true},
		{`\\?\UNC\server\IPC$..\x`, true},
		{`\\server\pipe.\x`, true},
		{`\\..\pipe\x`, true},
		{`//../IPC$/x`, true},
		{`\\localhost\..\pipe\x`, true},
		{`\\localhost\.\mailslot\x`, true},
		{`\\\pipe\x`, true},
		{`\\;LanmanRedirector\localhost\pipe\x`, true},
		{`\\?\UNC\;LanmanRedirector\server\IPC$\x`, true},

		{`\\server\..\x`, false},
		{`\\server\..\share\pipe`, false},
		{`\\;LanmanRedirector\nas\photos\pipe`, false},
		{`\\nas\;x\pipe`, false},
		{`\\?\UNC\server\pipe.d\x`, false},
		{`\\server\share\pipe`, false},
		{`\\server\share\pipe\x`, false},
		{`\\server\pipes\x`, false},
		{`\\pipe\share\x`, false},
		{`\\?\UNC\server\share\IPC$`, false},
		{`C:\pipe\x`, false},
		{`\\?\C:\pipe\x`, false},
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

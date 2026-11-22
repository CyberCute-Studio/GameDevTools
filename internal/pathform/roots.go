package pathform

import (
	"os"
	"path/filepath"
	"strings"
)

// RootDepth is how many names an absolute path, cleaned or resolved, holds
// below its root folder: 0 for a drive, volume, or share root, and -1 for a
// path that names only a drive, volume, device, or server, without even its
// root folder, such as \\?\C:, \\.\NUL, \\server, or \\?\UNC\server. A share
// is its own root folder however it is spelled. For the extended-length UNC
// prefixes, \\?\UNC and \??\UNC, VolumeName stops before the server and
// share, which it takes into the volume name of \\server\share and
// \\.\UNC\server\share, so for those the path's first two names are the
// server and the share.
func RootDepth(path string) int {
	volume := VolumeName(path)
	rest := path[len(volume):]
	names := len(strings.FieldsFunc(rest, func(character rune) bool {
		return character == '\\' || character == '/'
	}))
	switch {
	case strings.EqualFold(volume, `\\?\UNC`), strings.EqualFold(volume, `\??\UNC`):
		return max(names-2, -1)
	case rest == "" && !ShareRoot(volume):
		return -1
	}
	return names
}

// NamesDevice reports whether a resolved path, with any device prefix written
// \\?\, leads to no drive, volume, or share: its first element is neither a
// drive letter, nor a volume's GUID name, nor UNC.
func NamesDevice(resolved string) bool {
	device, extended := strings.CutPrefix(VolumeName(resolved), `\\?\`)
	if !extended {
		return false
	}
	const guid = "Volume{"
	switch {
	case len(device) == 2 && device[1] == ':' && 'a' <= device[0]|0x20 && device[0]|0x20 <= 'z':
		return false
	case strings.EqualFold(device, "UNC"):
		return false
	case len(device) > len(guid)+1 && strings.EqualFold(device[:len(guid)], guid) && strings.HasSuffix(device, "}"):
		return false
	}
	return true
}

// ShareRoot reports whether a volume name, as VolumeName gives it for a
// cleaned path, names a share, \\server\share or \\.\UNC\server\share, which
// is a folder, rather than a drive (C:), a server without a share (\\server,
// \\.\UNC\server), or the first element of a device path (\\?\C:, \\.\NUL),
// which are not.
func ShareRoot(volume string) bool {
	if len(volume) < 2 || !os.IsPathSeparator(volume[0]) || !os.IsPathSeparator(volume[1]) {
		return false
	}
	unc := volume[2:]
	const device = `.\UNC`
	if len(unc) >= len(device) && strings.EqualFold(unc[:len(device)], device) &&
		(len(unc) == len(device) || os.IsPathSeparator(unc[len(device)])) {
		unc = unc[min(len(unc), len(device)+1):]
	}
	server, share, found := strings.Cut(unc, string(filepath.Separator))
	return found && server != "" && share != "" && server != "." && server != "?"
}

// NamesNoFolder reports whether an output folder's path names only a drive,
// volume, device, or server rather than a folder on it, or leads through a
// device other than a drive, a volume, or a share, such as the named pipe
// \\.\pipe\x, which creating the folder would open, or the same pipe on a
// server, \\localhost\pipe\x (leadsThroughIPCShare). A path with more than two
// leading separators is refused too: Windows keeps the extra ones in the UNC
// path it builds, so its server and share cannot be judged from the names.
func NamesNoFolder(folder string) bool {
	clean := SaveFolder(folder)
	if strings.HasPrefix(clean, `\\\`) || RootDepth(clean) < 0 || leadsThroughIPCShare(clean) {
		return true
	}
	for _, prefix := range []string{`\\.\`, `\??\`} {
		if strings.HasPrefix(clean, prefix) {
			clean = `\\?\` + clean[len(prefix):]
			break
		}
	}
	return NamesDevice(clean)
}

// leadsThroughIPCShare reports whether a cleaned path's share, in any UNC
// spelling and letter case, is a server's pipe, mailslot, or IPC$. Windows
// reads those as its named pipes and mailslots rather than as folders, so
// creating \\localhost\pipe\x connects to the program serving that pipe. Only
// the share is judged: a folder named pipe on an ordinary share is a folder.
// Trailing spaces and periods are ignored, since a verbatim path keeps them
// and what a server makes of such a share name cannot be judged from here.
func leadsThroughIPCShare(clean string) bool {
	unc, found := "", false
	for _, prefix := range []string{`\\?\UNC\`, `\??\UNC\`, `\\.\UNC\`} {
		if len(clean) >= len(prefix) && strings.EqualFold(clean[:len(prefix)], prefix) {
			unc, found = clean[len(prefix):], true
			break
		}
	}
	if !found {
		if !strings.HasPrefix(clean, `\\`) || strings.HasPrefix(clean, `\\?\`) || strings.HasPrefix(clean, `\\.\`) {
			return false
		}
		unc = clean[len(`\\`):]
	}
	names := strings.Split(unc, `\`)
	if len(names) < 2 {
		return false
	}
	named := strings.TrimRight(names[1], " .")
	for _, share := range []string{"pipe", "mailslot", "IPC$"} {
		if strings.EqualFold(named, share) {
			return true
		}
	}
	return false
}

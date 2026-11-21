//go:build windows

// Package pathform reads an output folder's path the same way on every Go
// release. Go 1.27 changed two things in how package filepath reads a Windows
// path. It takes the server and share of a \\?\UNC or \??\UNC path into the
// volume name, as it always has for \\server\share and \\.\UNC\server\share.
// And it gives a path whose volume name would hold a ".." name, such as
// \\server\..\x or \\.\..\x, no volume at all, so it calls the path relative,
// and Clean and Join rewrite it as a path on the current drive.
//
// A folder is stored in the settings file, so one that an earlier build
// accepted must keep its meaning: read as relative it would fail the whole
// file's validation, and joined with a file name it would lead somewhere
// else. VolumeName, IsAbs, Clean, Dir, Base, and Join therefore keep the
// reading of Go 1.26 and earlier. Clean and Base are package filepath's own
// for every path the two readings agree on.
package pathform

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// standIn is the volume a path's rest is put on for package filepath to read
// it as names under a root, whatever the volume it really has.
const standIn = `C:`

// hasPrefixFold reports whether path starts with prefix, in any letter case
// and with either separator, and ends or has a separator there.
func hasPrefixFold(path, prefix string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for index := range len(prefix) {
		if os.IsPathSeparator(prefix[index]) {
			if !os.IsPathSeparator(path[index]) {
				return false
			}
		} else if !strings.EqualFold(path[index:index+1], prefix[index:index+1]) {
			return false
		}
	}
	return len(path) == len(prefix) || os.IsPathSeparator(path[len(prefix)])
}

// serverShareEnd is where the server and share that start at the given
// offset end: at the separator after the share, or at the end of the path.
func serverShareEnd(path string, start int) int {
	separators := 0
	for index := start; index < len(path); index++ {
		if os.IsPathSeparator(path[index]) {
			separators++
			if separators == 2 {
				return index
			}
		}
	}
	return len(path)
}

// volumeLen is the length of the path's volume name as Go 1.26 reads it: a
// drive letter; \\.\UNC with its server and share; a device prefix, \\.\,
// \\?\, or \??\, with the one name after it, which for \\?\UNC and \??\UNC
// is UNC; or the server and share of \\server\share.
func volumeLen(path string) int {
	switch {
	case len(path) >= 2 && path[1] == ':':
		return 2
	case path == "" || !os.IsPathSeparator(path[0]):
		return 0
	case hasPrefixFold(path, `\\.\UNC`):
		return serverShareEnd(path, len(`\\.\UNC\`))
	case hasPrefixFold(path, `\\.`), hasPrefixFold(path, `\\?`), hasPrefixFold(path, `\??`):
		if len(path) == 3 {
			return 3
		}
		for index := 4; index < len(path); index++ {
			if os.IsPathSeparator(path[index]) {
				return index
			}
		}
		return len(path)
	case len(path) >= 2 && os.IsPathSeparator(path[1]):
		return serverShareEnd(path, 2)
	}
	return 0
}

// split is the path's volume name and what follows it, and whether package
// filepath reads the volume name differently, in which case it cannot be
// handed the path as it is.
func split(path string) (volume, rest string, differs bool) {
	size := volumeLen(path)
	return filepath.FromSlash(path[:size]), path[size:], size != len(filepath.VolumeName(path))
}

// VolumeName is filepath.VolumeName.
func VolumeName(path string) string {
	return filepath.FromSlash(path[:volumeLen(path)])
}

// IsAbs is filepath.IsAbs: a path that starts with two separators is
// absolute, and any other is when a separator follows its volume name.
func IsAbs(path string) bool {
	size := volumeLen(path)
	if size == 0 {
		return false
	}
	if os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) {
		return true
	}
	return size < len(path) && os.IsPathSeparator(path[size])
}

// Clean is filepath.Clean, cleaning what follows the volume name as a rooted
// path, so ".." never leaves the volume name.
func Clean(path string) string {
	volume, rest, differs := split(path)
	switch {
	case !differs:
		return filepath.Clean(path)
	case rest != "":
		return volume + filepath.Clean(standIn + rest)[len(standIn):]
	case os.IsPathSeparator(path[1]):
		return volume
	}
	return path + "."
}

// Dir is Go 1.26's filepath.Dir: the volume name and the cleaned part of the
// rest up to its last separator. A share's parent under \\?\UNC is its
// server, and the parent of a path that is only a volume name is that name.
// It is written out rather than handed to package filepath for a path both
// read alike, because the part it cleans can be read differently even then:
// after \\x\y, a rest of //../ is itself a path with a volume name to Go 1.26.
func Dir(path string) string {
	size := volumeLen(path)
	volume := filepath.FromSlash(path[:size])
	index := len(path) - 1
	for index >= size && !os.IsPathSeparator(path[index]) {
		index--
	}
	dir := Clean(path[size : index+1])
	if dir == "." && size > 2 {
		return volume
	}
	return volume + dir
}

// Base is filepath.Base: the last name after the volume name, which under
// \\?\UNC may be a share's own name.
func Base(path string) string {
	_, rest, differs := split(strings.TrimRight(path, `\/`))
	switch {
	case !differs:
		return filepath.Base(path)
	case rest == "":
		return `\`
	}
	return filepath.Base(standIn + rest)
}

// Join is filepath.Join for a folder and the names under it, keeping the
// folder's volume name. As in filepath.Join, a folder that ends in a
// separator gets no second one, and one that ends in a colon gets none. The
// joined path is cleaned here, as adding a name can change how package
// filepath reads even a folder it reads alike. A folder too short to hold a
// volume name is left to filepath.Join.
func Join(folder string, names ...string) string {
	if len(folder) < 2 {
		return filepath.Join(append([]string{folder}, names...)...)
	}
	joined := folder
	for _, name := range names {
		switch last := joined[len(joined)-1]; {
		case os.IsPathSeparator(last):
			name = strings.TrimLeft(name, `\/`)
		case last != ':':
			joined += `\`
		}
		joined += name
	}
	return Clean(joined)
}

// deviceNames are the names Windows reserves for devices, as the shell's
// parser refuses them. A name with an extension, such as con.d, is not one
// here: only Windows 10 reads it as a device, and only as a path's last name,
// which NamesOneFolder compares with Windows' own resolution.
var deviceNames = []string{
	"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "COM¹", "COM²", "COM³",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "LPT¹", "LPT²", "LPT³",
}

// HasDeviceName reports whether any name after the path's volume name is one
// Windows reserves for a device, in any letter case. Windows can keep such a
// name for a folder a longer path leads through, but File Explorer's parser
// refuses a path through one wherever it falls.
func HasDeviceName(path string) bool {
	separator := func(character rune) bool { return character == '\\' || character == '/' }
	for name := range strings.FieldsFuncSeq(path[volumeLen(path):], separator) {
		if slices.ContainsFunc(deviceNames, func(device string) bool { return strings.EqualFold(name, device) }) {
			return true
		}
	}
	return false
}

// holdsStream reports whether a cleaned path has a colon after its volume
// name, which NTFS reads as naming a stream of the file or folder before it,
// verbatim paths included, or in the server or share name of a share's volume
// name, which neither name may hold.
func holdsStream(clean string) bool {
	volume := VolumeName(clean)
	if strings.Contains(clean[len(volume):], ":") {
		return true
	}
	switch {
	case hasPrefixFold(volume, `\\.\UNC`):
	case strings.HasPrefix(volume, `\\.\`), strings.HasPrefix(volume, `\\?\`), !strings.HasPrefix(volume, `\\`):
		return false
	}
	return strings.Contains(volume, ":")
}

// NamesOneFolder reports whether Windows reads a folder's path as exactly the
// folder its cleaned form names, so that a save can create it, write into it,
// and name a file in it without any of those reaching another place. Windows
// drops trailing spaces and periods from some names by where they fall in a
// path, reads names such as NUL as devices, reads a colon after the volume
// name as a file stream, and resolves a ".." after \\.\UNC\server\share
// differently from Clean, so a path that depends on any of these is refused.
//
// This is the Image Resizer's output folder rule made stricter in one way:
// that rule keeps a name Windows keeps where it falls, such as "Photos " or
// COM1 before another name, and this one refuses every name that ends in a
// space or a period or is reserved for a device, wherever it falls. A path
// marked \\?\ or \??\ is one Windows takes verbatim, so it names one folder
// unless it holds a stream or Clean would rewrite it. A path through a device
// other than a drive, such as \\.\pipe\x, is not judged here.
func NamesOneFolder(folder string) bool {
	clean := Clean(folder)
	if holdsStream(clean) {
		return false
	}
	if strings.HasPrefix(folder, `\\?\`) || strings.HasPrefix(folder, `\??\`) {
		return strings.TrimRight(folder, `\`) == strings.TrimRight(clean, `\`)
	}
	for name := range strings.SplitSeq(clean[len(VolumeName(clean)):], `\`) {
		if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
			return false
		}
	}
	if HasDeviceName(clean) {
		return false
	}
	written, err := windows.FullPath(folder)
	if err != nil {
		return false
	}
	full, err := windows.FullPath(clean)
	return err == nil && full == clean && Clean(written) == clean
}

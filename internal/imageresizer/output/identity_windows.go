//go:build windows

package output

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// fileID identifies a file on its volume, the way os.SameFile compares
// Windows files: the volume serial number and the file index. Every path to
// one file — a different letter case, a hard link, a junction or symbolic
// link, a short 8.3 name, or a share path — reports the same identity. On
// ReFS the 64-bit index may be shared by two distinct files, which can only
// make planning treat a file as a source when it is not. FILE_ID_INFO's
// 128-bit identifier would tell such files apart, but it is not documented
// for every file system and redirector a path can lead through, and reading
// it for some paths to a file but not others would let two paths to one
// file compare unequal, the one mistake that could replace a source.
type fileID struct {
	volume uint32
	index  uint64
}

// shareAll lets every other open of a file proceed while planning holds a
// handle to it.
const shareAll = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// identify returns the identity of the file the path resolves to and its
// number of names: its hard links. It opens the path without requesting any
// access to its contents, so another process's sharing mode does not
// prevent reading them, and follows links to the file they resolve to. A
// file whose permissions deny the user even reading its attributes cannot
// be identified.
func identify(path string) (fileID, uint32, error) {
	handle, err := open(path, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return fileID{}, 0, err
	}
	defer windows.CloseHandle(handle)
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return fileID{}, 0, &fs.PathError{Op: "read file identity", Path: path, Err: err}
	}
	id := fileID{
		volume: information.VolumeSerialNumber,
		index:  uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow),
	}
	return id, information.NumberOfLinks, nil
}

// fileMissing reports whether a file could not be opened because no entry
// has its name in its folder. A path whose folder cannot be found is not
// missing: a drive, share, or link it leads through may be gone while the
// file remains, reachable by another path.
func fileMissing(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}

// reparseTagNameSurrogate is the reparse tag bit that marks a link to
// another named file or folder: a symbolic link, a junction, or a WSL
// symbolic link.
const reparseTagNameSurrogate = 0x20000000

// findEntry lists the entry the path's last element opens: its long name,
// even when the element is its 8.3 short name, and whether it is a file,
// neither a folder nor a link. A file's reparse point that is no link, such
// as a OneDrive placeholder's, only says how its data is stored. It lists
// the entry itself, not what a link leads to, and opens nothing, so it never
// makes OneDrive download a file.
func findEntry(path string) (entry, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return entry{}, &fs.PathError{Op: "find", Path: path, Err: err}
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(name, &data)
	if err != nil {
		return entry{}, &fs.PathError{Op: "find", Path: path, Err: err}
	}
	windows.FindClose(handle)
	// FindFirstFile reports the reparse tag of a reparse point in
	// dwReserved0.
	link := data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 &&
		data.Reserved0&reparseTagNameSurrogate != 0
	return entry{
		name: windows.UTF16ToString(data.FileName[:]),
		file: data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && !link,
	}, nil
}

// open opens the path without requesting access to its contents.
func open(path string, flags uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return 0, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, 0, shareAll, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return 0, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return handle, nil
}

// extendedPath resolves an absolute path with GetFullPathName, which drops
// trailing spaces and periods from the last name and resolves forward
// slashes, ".", and ".." exactly as Windows does for any path it is not told
// to take verbatim, and marks the result extended-length, so a path of any
// length names the file Windows itself opens for it. os.Lstat and the
// decoder reach that same file, whether the os package passes the path on
// as it is or resolves and marks it the same way, so identities are read
// for the files they reach. A path already marked extended, with any
// separators, is taken verbatim and left alone. The planner passes only
// absolute paths.
func extendedPath(path string) string {
	if len(path) >= 4 && (path[:4] == `\??\` ||
		os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) && path[2] == '?' && os.IsPathSeparator(path[3])) {
		return path
	}
	if !filepath.IsAbs(path) {
		return path
	}
	full, err := windows.FullPath(path)
	switch {
	case err != nil:
		return path
	case len(full) >= 2 && os.IsPathSeparator(full[0]) && os.IsPathSeparator(full[1]):
		if len(full) >= 4 && full[2] == '.' && os.IsPathSeparator(full[3]) {
			// A device path would change its meaning with the prefix.
			return full
		}
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}

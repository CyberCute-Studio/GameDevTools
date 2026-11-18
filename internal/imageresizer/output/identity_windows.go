//go:build windows

package output

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

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

// longPathLimit is the path length from which Windows path functions need
// the extended-length prefix. It matches the os package, which leaves room
// for an 8.3 name below MAX_PATH.
const longPathLimit = 248

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

// fileAttributeTagInfo is FILE_ATTRIBUTE_TAG_INFO.
type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

// storedFile reports whether the path names a file, not a folder, whose
// reparse point is no link but only says how its data is stored, such as a
// placeholder OneDrive keeps in sync. It inspects the entry itself rather
// than what its reparse point leads to, and requests no access to its
// contents, so it never makes OneDrive download a file.
func storedFile(path string) (bool, error) {
	handle, err := open(path, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	var information fileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		return false, &fs.PathError{Op: "read reparse tag", Path: path, Err: err}
	}
	return information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 &&
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 &&
		information.ReparseTag&reparseTagNameSurrogate == 0, nil
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

// extendedPath gives a long absolute path the extended-length prefix the
// way the os package does, so identities are read for the files os.Lstat
// and the decoder reach. Like os, it resolves the path with GetFullPathName
// before adding the prefix, which drops trailing spaces and dots from the
// last name and resolves forward slashes, ".", and ".." as Windows does for
// a short path, and it leaves alone a path already marked extended with any
// separators. The planner passes only absolute paths.
func extendedPath(path string) string {
	if len(path) >= 4 && (path[:4] == `\??\` ||
		os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) && path[2] == '?' && os.IsPathSeparator(path[3])) {
		return path
	}
	if len(path) < longPathLimit || !filepath.IsAbs(path) {
		return path
	}
	full, err := windows.FullPath(path)
	if err != nil {
		return path
	}
	if os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) {
		if path[2] == '.' && os.IsPathSeparator(path[3]) {
			// A device path would change its meaning with the prefix.
			return full
		}
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}

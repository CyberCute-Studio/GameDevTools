//go:build windows

package output

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileID identifies a file on its volume, the way os.SameFile compares
// Windows files: the volume serial number and the file index. Every path to
// one file — a different letter case, a hard link, a junction or symbolic
// link, a short 8.3 name, or a share path — reports the same identity. On
// ReFS the 64-bit index may be shared by two distinct files, which can only
// make planning treat a file as a source when it is not.
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

// identify opens the path without requesting any access to its contents,
// so another process's sharing mode does not prevent reading its identity,
// and follows links to the file they resolve to. A file whose permissions
// deny the user even reading its attributes cannot be identified.
func identify(path string) (fileID, error) {
	information, err := inspect(path)
	if err != nil {
		return fileID{}, err
	}
	return fileID{
		volume: information.VolumeSerialNumber,
		index:  uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow),
	}, nil
}

// links returns the number of names of the file the path resolves to: its
// hard links.
func links(path string) (uint32, error) {
	information, err := inspect(path)
	if err != nil {
		return 0, err
	}
	return information.NumberOfLinks, nil
}

// inspect reads the file information of the file the path resolves to,
// without requesting access to its contents.
func inspect(path string) (windows.ByHandleFileInformation, error) {
	var information windows.ByHandleFileInformation
	handle, err := open(path, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return information, err
	}
	defer windows.CloseHandle(handle)
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return information, &fs.PathError{Op: "read file identity", Path: path, Err: err}
	}
	return information, nil
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

// extendedPath adds the extended-length prefix to a long absolute path, as
// the os package does, so identities are read for the same paths os.Lstat
// can inspect.
func extendedPath(path string) string {
	if len(path) < longPathLimit || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, `\\`) {
		return `\\?\UNC\` + clean[2:]
	}
	if filepath.IsAbs(clean) {
		return `\\?\` + clean
	}
	return path
}

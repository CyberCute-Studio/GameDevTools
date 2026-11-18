//go:build windows

package output

import (
	"io/fs"
	"path/filepath"
	"strings"

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

// identify opens the path without requesting any access to its contents,
// so neither its permissions nor another process's sharing mode prevent
// reading its identity, and follows links to the file they resolve to.
func identify(path string) (fileID, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return fileID{}, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fileID{}, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return fileID{}, &fs.PathError{Op: "read file identity", Path: path, Err: err}
	}
	return fileID{
		volume: information.VolumeSerialNumber,
		index:  uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow),
	}, nil
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

//go:build windows

package output

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// fileID identifies a file on its volume, the way os.SameFile compares
// Windows files: the volume serial number and the file index. Every path to
// one file through one volume or share — a different letter case, a hard
// link, a junction or symbolic link, or a short 8.3 name — reports the same
// identity. A Samba server, however, reports a volume serial number of its
// own for each share, so one file reached through two shares of the server,
// such as a NAS's home and homes shares, reports its one file index under two
// serial numbers. The comparisons that can only stop a replacement therefore
// compare the file index alone (anyVolume), which, like a 64-bit index ReFS
// may give two distinct files, can only make planning treat a file as a
// source when it is not. FILE_ID_INFO's 128-bit identifier would tell such
// files apart, but it is not documented for every file system and redirector
// a path can lead through, and reading it for some paths to a file but not
// others would let two paths to one file compare unequal, the one mistake
// that could replace a source.
type fileID struct {
	volume uint32
	index  uint64
}

// anyVolume is the identity without its volume serial number, which two
// paths to one file through two shares of a Samba server report alike.
func (id fileID) anyVolume() fileID {
	return fileID{index: id.index}
}

// shareAll lets every other open of a file proceed while planning holds a
// handle to it.
const shareAll = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// identify returns the identity of the file the path resolves to, its number
// of names, its hard links, and its stamp, all read through one handle. It
// opens the path without requesting any access to its contents, so another
// process's sharing mode does not prevent reading them, and follows links to
// the file they resolve to. A file whose permissions deny the user even
// reading its attributes cannot be identified.
func identify(path string) (identity, error) {
	handle, err := open(path, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return identity{}, err
	}
	defer windows.CloseHandle(handle)
	found, _, err := identifyHandle(handle, path)
	return found, err
}

// identifyHandle reads the identity, number of names, and stamp of the file a
// handle is open to, and the file's attributes.
func identifyHandle(handle windows.Handle, path string) (identity, uint32, error) {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return identity{}, 0, &fs.PathError{Op: "read file identity", Path: path, Err: err}
	}
	return identity{
		id: fileID{
			volume: information.VolumeSerialNumber,
			index:  uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow),
		},
		links: information.NumberOfLinks,
		stamp: stamp{
			size:    uint64(information.FileSizeHigh)<<32 | uint64(information.FileSizeLow),
			created: filetime(information.CreationTime),
			written: filetime(information.LastWriteTime),
		},
	}, information.FileAttributes, nil
}

func filetime(time windows.Filetime) uint64 {
	return uint64(time.HighDateTime)<<32 | uint64(time.LowDateTime)
}

// nameNotFound reports whether an error says that no entry has the path's
// last name, ERROR_FILE_NOT_FOUND, rather than that a folder it leads
// through could not be found, ERROR_PATH_NOT_FOUND, which fileMissing never
// takes for a missing file.
func nameNotFound(err error) bool {
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
	name, err := windowsPath("find", path)
	if err != nil {
		return entry{}, err
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
	name, err := windowsPath("open", path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(name, 0, shareAll, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return 0, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return handle, nil
}

// windowsPath is the path as every Windows call the planner makes takes it:
// resolved and marked extended-length by extendedPath, so the file found or
// opened is the one os.Lstat and the decoder reach. A path that cannot be
// converted fails as op's failure.
func windowsPath(op, path string) (*uint16, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return nil, &fs.PathError{Op: op, Path: path, Err: err}
	}
	return name, nil
}

// resolvedPath is the form of a path PathKey compares: resolved and marked
// by extendedPath as every Windows call the planner makes takes it, then
// cleaned, so spellings Windows resolves to one name — with or without the
// extended-length prefix, as a \??\ or \\.\ device path, or with trailing
// spaces and periods that Windows drops from the last name — compare alike.
// The path is resolved before it is cleaned because filepath.Clean writes a
// //?/ device path, which Windows resolves, as the \\?\ path it takes
// verbatim.
func resolvedPath(path string) string {
	return cleanPath(extendedPath(path))
}

// extendedUNCPrefix returns the length of the \\?\UNC or \??\UNC prefix a
// path starts with, in either slash and any letter case, or 0 if it has
// neither. Go 1.27 changed how package filepath reads such a path, taking
// its server and share into the volume name, as it always has for
// \\server\share and \\.\UNC\server\share. The folder rules are written for
// a volume name that stops at the prefix, where ".." can leave a share, as it
// can when Windows reads the path, so volumeName, isAbsolute, cleanPath,
// parentPath, and baseName keep that reading on every Go release.
func extendedUNCPrefix(path string) int {
	const size = len(`\\?\UNC`)
	if len(path) < size || !os.IsPathSeparator(path[0]) || path[2] != '?' || !os.IsPathSeparator(path[3]) ||
		!(os.IsPathSeparator(path[1]) || path[1] == '?') || !strings.EqualFold(path[4:size], "UNC") {
		return 0
	}
	if len(path) > size && !os.IsPathSeparator(path[size]) {
		return 0
	}
	return size
}

// rootedRest is what follows an extended-length UNC path's prefix, of the
// given size, as a path on a drive root. The drive root stands in for the
// prefix so package filepath reads the rest as names under a root, and does
// not read a first name such as ?? as a device prefix of its own.
func rootedRest(path string, size int) string {
	return uncStandIn + `\` + strings.TrimLeft(path[size:], `\/`)
}

// uncStandIn is the volume rootedRest puts in place of the prefix.
const uncStandIn = `C:`

// volumeName is filepath.VolumeName, ending an extended-length UNC path's
// volume name at its prefix on every Go release.
func volumeName(path string) string {
	if size := extendedUNCPrefix(path); size > 0 {
		return filepath.FromSlash(path[:size])
	}
	return filepath.VolumeName(path)
}

// isAbsolute is filepath.IsAbs, reading an extended-length UNC path by its
// prefix on every Go release: a path that starts with two separators is
// absolute, and \??\UNC is when a separator follows it.
func isAbsolute(path string) bool {
	if size := extendedUNCPrefix(path); size > 0 {
		return path[1] != '?' || len(path) > size
	}
	return filepath.IsAbs(path)
}

// cleanPath is filepath.Clean, cleaning what follows an extended-length UNC
// path's prefix as a rooted path on every Go release, so ".." can leave a
// share and a share's root gains no trailing separator or period.
func cleanPath(path string) string {
	size := extendedUNCPrefix(path)
	if size == 0 || len(path) == size {
		return filepath.Clean(path)
	}
	return filepath.FromSlash(path[:size]) + filepath.Clean(rootedRest(path, size))[len(uncStandIn):]
}

// parentPath is filepath.Dir, whose parent of an extended-length UNC path is
// the next name up on every Go release: a share's is its server's, and the
// prefix's root is its own.
func parentPath(path string) string {
	size := extendedUNCPrefix(path)
	if size == 0 {
		return filepath.Dir(path)
	}
	return filepath.FromSlash(path[:size]) + filepath.Dir(rootedRest(path, size))[len(uncStandIn):]
}

// baseName is filepath.Base, whose last element of an extended-length UNC
// path is its last name on every Go release, a share's own name included.
func baseName(path string) string {
	size := extendedUNCPrefix(path)
	if size == 0 {
		return filepath.Base(path)
	}
	return filepath.Base(rootedRest(path, size))
}

// resolvesAsWritten reports whether Windows resolves a cleaned absolute path,
// one that was not written as an extended-length path, to exactly that path,
// keeping every name in it as written: GetFullPathName, which Windows
// applies to such a path before it opens or creates anything, neither drops
// a trailing space or period from a name nor reads a name as a device. A
// //?/ path, which filepath.Clean writes as \\?\, resolves as Windows
// resolves it too, as GetFullPathName resolves the \\?\ spelling alike.
func resolvesAsWritten(clean string) bool {
	full, err := windows.FullPath(clean)
	return err == nil && full == clean
}

// namesStream reports whether a cleaned absolute path holds a colon after
// its volume name, which NTFS reads as naming a stream of the file or folder
// before it, whether or not the path is extended-length, or in the server or
// share name of a share's volume name, \\server\share or
// \\.\UNC\server\share, which neither name may hold. The \\?\UNC spelling of
// a share puts both names after its volume name, so every spelling of one
// path gets one answer.
func namesStream(clean string) bool {
	volume := volumeName(clean)
	if strings.ContainsRune(clean[len(volume):], ':') {
		return true
	}
	return shareRoot(volume) && strings.ContainsRune(volume, ':')
}

// mkdirAll creates a folder, and every missing folder that holds it, through
// the folder's extended-length path. os.MkdirAll inspects and creates each
// folder on the way by a path that ends at that folder's name, and Windows
// drops trailing spaces and periods from the last name of a path, though it
// keeps some in a name a longer path leads through, such as the space of
// "Photos " or the periods of "v2..". By the ordinary path, os.MkdirAll
// would create a stray folder without them, or take an existing one for the
// folder named. The planner passes only folders ValidateFolder accepts,
// which Windows resolves as written, so the extended-length path names the
// folder that its destinations lie in. A failure names the folder that could
// not be created as the path spells it, without the prefix.
func mkdirAll(path string) error {
	extended := extendedPath(path)
	err := os.MkdirAll(extended, 0o755)
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && strings.HasPrefix(extended, pathErr.Path) {
		if rest := extended[len(pathErr.Path):]; strings.HasSuffix(path, rest) {
			pathErr.Path = path[:len(path)-len(rest)]
		}
	}
	return err
}

// extendedPath resolves an absolute path with GetFullPathName, which drops
// trailing spaces and periods from the last name and resolves forward
// slashes, ".", and ".." exactly as Windows does for any path it is not told
// to take verbatim, and marks the result extended-length, so a path of any
// length names the file Windows itself opens for it. os.Lstat and the
// decoder reach that same file, whether the os package passes the path on
// as it is or resolves and marks it the same way, so identities are read
// for the files they reach. A path marked extended with backslashes only,
// \\?\, is taken verbatim, as Windows takes it: \??\, the NT spelling of the
// prefix, which Windows takes exactly as \\?\, is written \\?\, so both
// spellings of a path share a key. The same prefix written with a forward
// slash, such as //?/, is one Windows resolves like a device path, dropping
// trailing spaces and periods, as os.Lstat, which passes it on as it is,
// finds; GetFullPathName resolves it to the \\?\ path Windows opens. A
// device path, \\.\ followed by a name, is resolved like any other path,
// and once resolved it names what \\?\ followed by the same name does, so it
// is marked the same way. The planner passes only absolute paths.
func extendedPath(path string) string {
	if strings.HasPrefix(path, `\??\`) {
		return `\\?\` + path[4:]
	}
	if strings.HasPrefix(path, `\\?\`) || !isAbsolute(path) {
		return path
	}
	full, err := windows.FullPath(path)
	switch {
	case err != nil:
		return path
	case strings.HasPrefix(full, `\\?\`):
		return full
	case strings.HasPrefix(full, `\\.\`):
		return `\\?\` + full[4:]
	case strings.HasPrefix(full, `\\`):
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}

//go:build windows

package output

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tempFile is the temporary file a result is written to. Write holds it open
// from its creation until it is renamed to its destination or deleted, and
// renames and deletes it through that handle, so the file it renames is the
// one it wrote, whatever another program does at its name. The handle denies
// other programs write and delete access, so none can change, rename, or
// delete the file meanwhile.
type tempFile struct {
	handle windows.Handle
	path   string
}

// maxWrite bounds one WriteFile call, whose length is 32 bits.
const maxWrite = 1 << 30

// createTemp creates a new temporary file in the folder, through its
// extended-length path as the planner inspects it, never opening an existing
// file.
func createTemp(folder string) (*tempFile, error) {
	var handle windows.Handle
	path, err := unusedName(folder, tempName, func(path string) error {
		name, err := windowsPath("create", path)
		if err != nil {
			return err
		}
		handle, err = windows.CreateFile(name, windows.GENERIC_WRITE|windows.DELETE, windows.FILE_SHARE_READ,
			nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			return &fs.PathError{Op: "create", Path: path, Err: err}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &tempFile{handle: handle, path: path}, nil
}

func (t *tempFile) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		chunk := p[written:]
		if len(chunk) > maxWrite {
			chunk = chunk[:maxWrite]
		}
		var n uint32
		err := windows.WriteFile(t.handle, chunk, &n, nil)
		written += int(n)
		if err != nil {
			return written, &fs.PathError{Op: "write", Path: t.path, Err: err}
		}
		if n == 0 {
			return written, &fs.PathError{Op: "write", Path: t.path, Err: io.ErrShortWrite}
		}
	}
	return written, nil
}

// sync flushes the file's data to the drive, so a result renamed to its
// destination is complete there even if the system stops right afterwards.
func (t *tempFile) sync() error {
	if err := windows.FlushFileBuffers(t.handle); err != nil {
		return &fs.PathError{Op: "flush", Path: t.path, Err: err}
	}
	return nil
}

// claim renames the file to the destination without replacing anything
// there.
func (t *tempFile) claim(destination string) error {
	if err := renameHandle(t.handle, destination); err != nil {
		return &fs.PathError{Op: "rename", Path: destination, Err: err}
	}
	return nil
}

// finish closes the file once it has its destination's name.
func (t *tempFile) finish() error {
	windows.CloseHandle(t.handle)
	return nil
}

// discard deletes the file through its handle and closes it. Should deleting
// through the handle fail, it is deleted by its path once closed.
func (t *tempFile) discard() error {
	deleteErr := deleteHandle(t.handle)
	windows.CloseHandle(t.handle)
	if deleteErr == nil {
		return nil
	}
	name, err := windowsPath("delete", t.path)
	if err == nil {
		err = windows.DeleteFile(name)
	}
	if err != nil {
		return &fs.PathError{Op: "delete", Path: t.path, Err: err}
	}
	return nil
}

// replace replaces the file at an ActionReplace item's destination with the
// complete result, as Write describes. It reports whether the result has the
// destination's name.
func replace(temp *tempFile, item Item, steps writeSteps) (bool, error) {
	destination := item.Destination
	held, err := hold(destination)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		// The planned file is gone and nothing has its name: the result is
		// created there, without replacing anything that takes the name
		// meanwhile.
		return create(temp, destination, ErrDestinationChanged, steps)
	}
	if err != nil {
		return false, writeFailure("open existing file", err)
	}
	// Closing the handle once the file is marked for deletion deletes it.
	defer windows.CloseHandle(held)
	if err := checkHeld(&heldFile{fileSystem: osFiles{}, handle: held}, item); err != nil {
		return false, err
	}
	if steps.pinned != nil {
		steps.pinned()
	}
	aside, err := moveAside(held, destination)
	if err != nil {
		return false, writeFailure("move existing file aside", err)
	}
	if steps.movedAside != nil {
		steps.movedAside()
	}
	if err := claim(temp, destination, steps); err != nil {
		if restoreErr := renameHandle(held, destination); restoreErr != nil {
			if nameTaken(err) {
				// Another program took the name while it was free: the
				// destination changed, and the file stays where it was moved.
				return false, kindError(KindChanged, "save result",
					fmt.Errorf("%s: %w: another file took the name, so the existing file the result was to replace remains as %s (%v)",
						destination, ErrDestinationChanged, aside, restoreErr))
			}
			return false, kindError(KindWrite, "restore existing file",
				fmt.Errorf("the result could not take the name %s (%w), and the existing file it was to replace remains as %s: %w",
					destination, err, aside, restoreErr))
		}
		if nameTaken(err) {
			return false, kindError(KindChanged, "save result", fmt.Errorf("%s: %w", destination, ErrDestinationChanged))
		}
		return false, writeFailure("save result", err)
	}
	if err := deleteHandle(held); err != nil {
		return true, kindError(KindWrite, "remove replaced file", &fs.PathError{Op: "delete", Path: aside, Err: err})
	}
	return true, nil
}

// hold opens the file at the destination for deletion while denying every
// other program write and delete access, so that, until the handle is
// closed, no program can change the file's content, move, rename, delete, or
// replace it, nor give its name to another file: the file checkHeld checks is
// the one Write deletes, with the content it checked. Sharing does not
// govern a file's attributes, times, or other names, so a program can still
// mark the held file read-only, change its times, or link another name to
// it, none of which changes the content Write deletes. A program that
// already has the file open for writing, or without allowing deletion, makes
// it fail with a sharing violation. It opens a link or a folder at the
// destination as itself, which checkHeld then refuses.
func hold(destination string) (windows.Handle, error) {
	name, err := windowsPath("open", destination)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, entryFlags, 0)
	if err != nil {
		return 0, &fs.PathError{Op: "open", Path: destination, Err: err}
	}
	return handle, nil
}

// entryFlags open the entry at a path as itself, a link or a folder included,
// rather than what a link leads to.
const entryFlags = windows.FILE_FLAG_OPEN_REPARSE_POINT | windows.FILE_FLAG_BACKUP_SEMANTICS

// checkReplace is the look Item.CheckWrite takes at the file an
// ActionReplace item replaces, before any work is done for the item:
// checkHeld's check, through a handle that requests no access to the file
// and so holds nothing. A file that is no longer the planned one, unchanged,
// or is read-only fails the item as Write would; a planned file deleted since
// does not, as Write creates the result in its place, and neither does a file
// another program has open, which it may have closed by the time Write holds
// the file.
func checkReplace(item Item) error {
	handle, err := open(item.Destination, entryFlags)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return writeFailure("open existing file", err)
	}
	defer windows.CloseHandle(handle)
	return checkHeld(&heldFile{fileSystem: osFiles{}, handle: handle}, item)
}

// heldFile is the filesystem as checkHeld inspects it: the folder lists the
// entry at the destination as planning lists it, through the fileSystem, and
// the identity is read through the handle that holds the file, so the file
// checked is the one Write moves aside and deletes.
type heldFile struct {
	fileSystem
	handle windows.Handle
}

func (h *heldFile) Identity(path string) (identity, error) {
	found, _, err := identifyHandle(h.handle, path)
	return found, err
}

// checkHeld checks that the file the handle is open to is the one the plan
// proved is none of the sources, unchanged, with Item.StillReplaceable's own
// check, and that it is not read-only, reading its attributes through the
// handle once that check has passed. The handle Write holds the file with
// keeps the file at the destination's name, with its content, while the
// checks run, so their answer for the content still holds when it is
// replaced. A failure to list the entry or read the file's identity or
// attributes is a KindWrite error, except that a folder no longer listing
// the held file, which keeps its name, means the path no longer leads to the
// held file's folder: a junction, link, or drive mapping in it now leads
// elsewhere, as a folder that holds an open file can be neither renamed nor
// removed.
func checkHeld(files *heldFile, item Item) error {
	switch err := item.verifyReplaceable(files); {
	case errors.Is(err, ErrDestinationChanged):
		return kindError(KindChanged, "check existing file", fmt.Errorf("%s: %w", item.Destination, err))
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return kindError(KindChanged, "check existing file", fmt.Errorf("%w: %w", ErrDestinationChanged, err))
	case err != nil:
		return writeFailure("check existing file", err)
	}
	_, attributes, err := identifyHandle(files.handle, item.Destination)
	if err != nil {
		return writeFailure("check existing file", err)
	}
	if attributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		return kindError(KindWrite, "replace existing file", fmt.Errorf("%s: %w", item.Destination, ErrReadOnly))
	}
	return nil
}

// moveAside renames the held file to a new name in the destination's folder,
// one that starts with asidePrefix, without replacing anything there, and
// returns that name.
func moveAside(held windows.Handle, destination string) (string, error) {
	name := func() string { return asideName(destination) }
	return unusedName(filepath.Dir(destination), name, func(aside string) error {
		if err := renameHandle(held, aside); err != nil {
			return &fs.PathError{Op: "rename", Path: aside, Err: err}
		}
		return nil
	})
}

// fileRenameInformation is FILE_RENAME_INFORMATION, followed in its buffer by
// the rest of the new name.
type fileRenameInformation struct {
	// ReplaceIfExists is a BOOLEAN padded to the handle's alignment. Write
	// always leaves it false.
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

// renameHandle renames the file a handle is open to, which needs delete
// access, to the target path without replacing anything there: a name that
// an entry already has fails with ERROR_ALREADY_EXISTS. NtSetInformationFile
// takes the new name in its NT form, the target's extended-length path with
// \??\ in place of \\?\.
func renameHandle(handle windows.Handle, target string) error {
	extended := extendedPath(target)
	if !strings.HasPrefix(extended, `\\?\`) {
		return fmt.Errorf("%s is not an absolute path Windows can resolve", target)
	}
	name, err := windows.UTF16FromString(`\??\` + extended[len(`\\?\`):])
	if err != nil {
		return err
	}
	name = name[:len(name)-1]
	offset := unsafe.Offsetof(fileRenameInformation{}.FileName)
	size := offset + uintptr(len(name))*2
	// The buffer is allocated as 64-bit words, so the structure at its start
	// is aligned as the handle field requires.
	words := make([]uint64, (max(size, unsafe.Sizeof(fileRenameInformation{}))+7)/8)
	info := (*fileRenameInformation)(unsafe.Pointer(&words[0]))
	info.FileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(info)), uint32(size), windows.FileRenameInformation)
	if ntStatus, ok := err.(windows.NTStatus); ok {
		return ntStatus.Errno()
	}
	return err
}

// deleteHandle marks the file a handle is open to, which needs delete
// access, for deletion once the handle is closed. It asks for POSIX
// semantics, so the file's name is gone as soon as the handle is closed even
// while another program that allowed deletion, such as a picture viewer, the
// search indexer, or a virus scanner, still has the file open; that program
// keeps reading the file until it closes it. Marked the classic way, the name
// would stay until the last such handle closed, naming a file no program can
// open, rename, or delete meanwhile. A file system without POSIX deletion,
// such as FAT32 or exFAT, marks the file the classic way. A read-only file is
// refused either way.
func deleteHandle(handle windows.Handle) error {
	posix := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	if windows.SetFileInformationByHandle(handle, windows.FileDispositionInfoEx, (*byte)(unsafe.Pointer(&posix)), uint32(unsafe.Sizeof(posix))) == nil {
		return nil
	}
	disposition := byte(1)
	return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &disposition, 1)
}

// nameTaken reports whether creating or renaming a file failed because an
// entry already has the name.
func nameTaken(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

// diskFull reports whether an operation failed because the drive is full.
func diskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}

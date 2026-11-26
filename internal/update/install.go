package update

import (
	"archive/zip"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/release"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// stagingPrefix begins the name of the folder an update stages its files in,
// inside the folder it updates, so that moving them into place is a rename
// on one volume. The folder also keeps the executable the update replaced,
// which the running process still uses, until a later start removes it
// (Cleanup).
const stagingPrefix = ".cybercute-update-"

// stagedFiles is the subfolder of the staging folder that the ZIP's files are
// extracted to, and previousExecutable the name the replaced executable is
// given there.
const (
	stagedFiles        = "files"
	previousExecutable = "previous.exe"
)

// fileName is a name an update may give a file: letters, digits, dots,
// hyphens, and underscores, beginning and ending with a letter or digit, so it
// cannot name another folder, a stream, or a file Windows would name
// differently, as it drops a trailing dot.
var fileName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,98}[A-Za-z0-9])?$`)

// reservedNames are the device names Windows reserves, with any extension.
var reservedNames = []string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}

// Folder returns the folder of the running executable when it is the folder a
// release ZIP extracts to — the executable has the release's name, and the
// documents the ZIP carries lie beside it — or false otherwise, as for a
// developer's or a test's build, which an update must not change.
func Folder(executable string) (string, bool) {
	if !strings.EqualFold(filepath.Base(executable), release.ExecutableName()) {
		return "", false
	}
	folder := filepath.Dir(executable)
	for _, name := range release.PackagedDocuments {
		info, err := os.Lstat(filepath.Join(folder, name))
		if err != nil || !info.Mode().IsRegular() {
			return "", false
		}
	}
	return folder, true
}

// NewStaging creates the folder an update stages its files in, inside folder.
// An error here is the first sign that the folder cannot be written.
func NewStaging(folder string) (string, error) {
	return os.MkdirTemp(folder, stagingPrefix+"*")
}

// Discard removes a staging folder whose update did not install, with what it
// holds.
func Discard(staging string) error {
	return os.RemoveAll(staging)
}

// Cleanup removes from folder what earlier updates left there: their staging
// folders, each with the executable it replaced. It removes what it can and
// returns the first error, as a staging folder whose executable is still
// running, which the next start removes.
func Cleanup(folder string) error {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), stagingPrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(folder, entry.Name())); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Stage extracts the verified ZIP at archive into the staging folder, checking
// first that it holds only plain files in the folder the release ZIP is built
// with (internal/release.BuildArchive), the executable among them, within the
// limits an update may be, and then that the executable is built for the
// machine type. It returns the names of the files, the executable's first.
func Stage(archive, staging string, machine uint16) ([]string, error) {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("the update is not a ZIP: %w", err)
	}
	defer reader.Close()
	entries, names, err := checkEntries(reader.File)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(staging, stagedFiles)
	if err := os.Mkdir(target, 0o755); err != nil {
		return nil, err
	}
	for i, entry := range entries {
		if err := extract(entry, filepath.Join(target, names[i])); err != nil {
			return nil, fmt.Errorf("extract %s: %w", names[i], err)
		}
	}
	if err := checkMachine(filepath.Join(target, release.ExecutableName()), machine); err != nil {
		return nil, err
	}
	executable := slices.Index(names, release.ExecutableName())
	names[0], names[executable] = names[executable], names[0]
	return names, nil
}

// checkEntries returns the ZIP's files and the name each is installed as.
func checkEntries(files []*zip.File) ([]*zip.File, []string, error) {
	if len(files) > maxEntries {
		return nil, nil, fmt.Errorf("the update holds %d files, more than an update may", len(files))
	}
	prefix := ui.AppName + "/"
	var entries []*zip.File
	var names []string
	seen := map[string]bool{}
	var total uint64
	for _, entry := range files {
		if entry.Name == prefix && entry.Mode().IsDir() {
			continue
		}
		name, inFolder := strings.CutPrefix(entry.Name, prefix)
		if !inFolder || !entry.Mode().IsRegular() || !safeName(name) {
			return nil, nil, fmt.Errorf("the update holds %q, which is not a file an update installs", entry.Name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, nil, fmt.Errorf("the update holds %s twice", name)
		}
		seen[key] = true
		if total += entry.UncompressedSize64; total > maxExtractedBytes {
			return nil, nil, errors.New("the update extracts to more than an update may")
		}
		entries = append(entries, entry)
		names = append(names, name)
	}
	if !slices.Contains(names, release.ExecutableName()) {
		return nil, nil, fmt.Errorf("the update holds no %s", release.ExecutableName())
	}
	return entries, names, nil
}

// safeName reports whether name is one an update may give a file in the
// application's folder.
func safeName(name string) bool {
	if !fileName.MatchString(name) || strings.HasPrefix(name, stagingPrefix) {
		return false
	}
	device, _, _ := strings.Cut(strings.ToUpper(name), ".")
	return !slices.Contains(reservedNames, device)
}

// extract writes the ZIP entry to a new file at path, which must hold exactly
// the size the entry declares; the ZIP reader checks its CRC-32 as it ends.
func extract(entry *zip.File, path string) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	written, err := io.Copy(file, io.LimitReader(source, int64(entry.UncompressedSize64)+1))
	if err == nil && uint64(written) != entry.UncompressedSize64 {
		err = fmt.Errorf("it holds %d bytes, not the %d its entry declares", written, entry.UncompressedSize64)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// checkMachine reports an executable that is not a PE image for the machine
// type, so an update cannot install one that would not run here.
func checkMachine(path string, machine uint16) error {
	image, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("the update's %s is not a Windows executable: %w", release.ExecutableName(), err)
	}
	defer image.Close()
	if image.FileHeader.Machine != machine {
		return fmt.Errorf("the update's %s is built for machine type 0x%04X, not 0x%04X", release.ExecutableName(), image.FileHeader.Machine, machine)
	}
	return nil
}

// InstallError is an update that could not replace the running executable.
// The folder is as it was, but for a document it added and could not take
// back (withdraw), unless Restored is false: the running executable was set
// aside at Previous and could not be put back.
type InstallError struct {
	Err      error
	Previous string
	Restored bool
}

func (e *InstallError) Error() string {
	if !e.Restored {
		return fmt.Sprintf("%v, and the running version could not be put back from %s", e.Err, e.Previous)
	}
	return e.Err.Error()
}

func (e *InstallError) Unwrap() error { return e.Err }

// Install moves the staged files of names, the executable's first, into the
// running executable's folder. A file the folder does not hold yet, as a
// document a newer release adds, is moved first, since the new version takes
// the folder for a release only when it holds every document that version
// ships (Folder): if one cannot be moved, those already moved are taken back
// and an InstallError returned. The running executable is then renamed into
// the staging folder, which Windows allows while it runs, and the new one
// takes its name; if that fails, the running one is renamed back, the files
// added are taken back, and an InstallError returned. Each other file then
// replaces the one of its name. A document that could not be replaced leaves
// the old one, and is returned in skipped: the new version is installed, and
// the next update replaces it. Every rename is tried again for a moment while
// another program holds the file (rename).
func Install(executable, staging string, names []string) (skipped []string, err error) {
	folder := filepath.Dir(executable)
	staged := filepath.Join(staging, stagedFiles)
	previous := filepath.Join(staging, previousExecutable)
	var added, replaced []string
	for _, name := range names {
		if name == release.ExecutableName() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(folder, name)); !errors.Is(err, fs.ErrNotExist) {
			replaced = append(replaced, name)
			continue
		}
		if err := rename(filepath.Join(staged, name), filepath.Join(folder, name)); err != nil {
			withdraw(folder, staged, added)
			return nil, &InstallError{Err: fmt.Errorf("add %s: %w", name, err), Restored: true}
		}
		added = append(added, name)
	}
	if err := rename(executable, previous); err != nil {
		withdraw(folder, staged, added)
		return nil, &InstallError{Err: fmt.Errorf("set the running version aside: %w", err), Restored: true}
	}
	if err := rename(filepath.Join(staged, release.ExecutableName()), executable); err != nil {
		failure := &InstallError{Err: fmt.Errorf("put the new version in place: %w", err), Previous: previous, Restored: true}
		if restoreErr := rename(previous, executable); restoreErr != nil {
			failure.Restored = false
		}
		withdraw(folder, staged, added)
		return nil, failure
	}
	for _, name := range replaced {
		if err := rename(filepath.Join(staged, name), filepath.Join(folder, name)); err != nil {
			skipped = append(skipped, name)
		}
	}
	return skipped, nil
}

// withdraw moves the files of names that an install added to folder back to
// the staging folder's files, staged, when the install fails, as far as it
// can: a file it cannot move is a document the folder did not have, which
// changes nothing the running version reads.
func withdraw(folder, staged string, names []string) {
	for _, name := range names {
		_ = rename(filepath.Join(folder, name), filepath.Join(staged, name))
	}
}

// renameWait bounds how long rename tries again while another program holds
// the file. A virus scanner commonly opens a file that has just been written,
// as every staged file has, and an executable that has just been run, and
// may hold it for a moment without letting it be renamed.
const renameWait = 2 * time.Second

// rename is os.Rename, tried again every 50 ms for up to renameWait while it
// fails as it does when another program holds the file (heldByAnother).
func rename(from, to string) error {
	deadline := time.Now().Add(renameWait)
	for {
		err := os.Rename(from, to)
		if err == nil || !heldByAnother(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

//go:build windows

package update

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/release"
)

// runningMachine is the machine type of the executables this test runs.
func runningMachine(t *testing.T) uint16 {
	t.Helper()
	machine, ok := Machine()
	if !ok {
		t.Skip("no release is built for this architecture")
	}
	return machine
}

// TestInstallReplacesARunningExecutable pins what an update relies on: Windows
// lets the executable of a running process be renamed, so the new version can
// take its name, and the staging folder that keeps it cannot be removed until
// the process exits, when Cleanup removes it. A copy of ping.exe stands in for
// the running application.
func TestInstallReplacesARunningExecutable(t *testing.T) {
	machine := runningMachine(t)
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skipf("no ping.exe to run: %v", err)
	}
	files := releaseFiles(machine, "old")
	files[release.ExecutableName()] = ping
	executable := installation(t, files)
	folder := filepath.Dir(executable)

	command := exec.Command(executable, "-n", "30", "127.0.0.1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := false
	t.Cleanup(func() {
		if !exited {
			command.Process.Kill()
			command.Wait()
		}
	})

	staging, err := NewStaging(folder)
	if err != nil {
		t.Fatal(err)
	}
	newFiles := releaseFiles(machine, "new")
	names, err := Stage(stagedRelease(t, newFiles), staging, machine)
	if err != nil {
		t.Fatal(err)
	}
	if skipped, err := Install(executable, staging, names); err != nil || len(skipped) != 0 {
		t.Fatalf("Install over a running executable = %v, %v", skipped, err)
	}
	if data, _ := os.ReadFile(executable); !bytes.Equal(data, newFiles[release.ExecutableName()]) {
		t.Error("the new executable did not take the running one's name")
	}
	if err := Cleanup(folder); err == nil {
		t.Error("Cleanup removed the executable of a running process")
	}

	command.Process.Kill()
	command.Wait()
	exited = true
	// A virus scanner can hold an executable that has just exited for a moment.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if err = Cleanup(folder); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("Cleanup after the process exited: %v", err)
	}
	if entries, _ := filepath.Glob(filepath.Join(folder, stagingPrefix+"*")); len(entries) != 0 {
		t.Errorf("Cleanup left %v", entries)
	}
}

// TestRelaunchStartsTheExecutable starts a program that exits at once, as
// Relaunch starts the updated application, and reports one that is missing.
func TestRelaunchStartsTheExecutable(t *testing.T) {
	if pid, err := Relaunch(filepath.Join(os.Getenv("SystemRoot"), "System32", "HOSTNAME.EXE")); err != nil || pid == 0 {
		t.Errorf("Relaunch = %d, %v", pid, err)
	}
	if _, err := Relaunch(filepath.Join(t.TempDir(), release.ExecutableName())); err == nil {
		t.Error("Relaunch of a missing executable reported no error")
	}
}

// TestRelaunchTellsTheNewVersionItFollowsAnUpdate pins the argument the
// installed version is started with, which Relaunched finds, so that its start
// does not look for a newer release again.
func TestRelaunchTellsTheNewVersionItFollowsAnUpdate(t *testing.T) {
	executable := `C:\Users\Someone\CyberCute Dev Tools\cybercute-dev-tools.exe`
	if got, want := relaunchCommandLine(executable), `"`+executable+`" `+RelaunchArgument; got != want {
		t.Errorf("command line = %s, want %s", got, want)
	}
	arguments := os.Args
	t.Cleanup(func() { os.Args = arguments })
	os.Args = []string{executable}
	if Relaunched() {
		t.Error("a start without the argument was taken for one after an update")
	}
	os.Args = []string{executable, RelaunchArgument}
	if !Relaunched() {
		t.Error("the start after an update was not recognized")
	}
}

// TestInstallWaitsForAProgramHoldingAStagedFile stands in for a virus scanner
// that opens a file just written, without letting it be renamed: the install
// waits for it rather than fail, as it would at every start the scanner did.
func TestInstallWaitsForAProgramHoldingAStagedFile(t *testing.T) {
	machine := runningMachine(t)
	executable := installation(t, releaseFiles(machine, "old"))
	staging, err := NewStaging(filepath.Dir(executable))
	if err != nil {
		t.Fatal(err)
	}
	newFiles := releaseFiles(machine, "new")
	names, err := Stage(stagedRelease(t, newFiles), staging, machine)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filepath.Join(staging, stagedFiles, release.ExecutableName()))
	if err != nil {
		t.Fatal(err)
	}
	scanner, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		windows.CloseHandle(scanner)
		close(released)
	}()
	t.Cleanup(func() { <-released })
	if skipped, err := Install(executable, staging, names); err != nil || len(skipped) != 0 {
		t.Fatalf("Install while a staged file was held = %v, %v", skipped, err)
	}
	if data, _ := os.ReadFile(executable); !bytes.Equal(data, newFiles[release.ExecutableName()]) {
		t.Error("the new executable was not installed once the file was let go")
	}
}

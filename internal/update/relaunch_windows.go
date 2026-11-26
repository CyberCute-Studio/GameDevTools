//go:build windows

package update

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Relaunch starts the executable at path, the version just installed, with
// this process's environment and working folder and none of its handles. The
// new process waits for this one to release the single-instance mutex as it
// exits.
func Relaunch(executable string) error {
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	commandLine, err := windows.UTF16PtrFromString(windows.EscapeArg(executable))
	if err != nil {
		return err
	}
	startup := windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	if err := windows.CreateProcess(application, commandLine, nil, nil, false, 0, nil, nil, &startup, &process); err != nil {
		return err
	}
	windows.CloseHandle(process.Thread)
	windows.CloseHandle(process.Process)
	return nil
}

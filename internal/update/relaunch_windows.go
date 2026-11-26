//go:build windows

package update

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Relaunch starts the executable at path, the version just installed, with
// RelaunchArgument, this process's environment and working folder, and none
// of its handles, and returns its process ID. It is asked to show its window
// as this process was, as by a shortcut set to run minimized, since this
// process's main window, which that was meant for, never appears. The new
// process waits for this one to release the single-instance mutex as it
// exits.
func Relaunch(executable string) (uint32, error) {
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return 0, err
	}
	commandLine, err := windows.UTF16PtrFromString(relaunchCommandLine(executable))
	if err != nil {
		return 0, err
	}
	startup := windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var own windows.StartupInfo
	own.Cb = uint32(unsafe.Sizeof(own))
	if windows.GetStartupInfo(&own) == nil && own.Flags&windows.STARTF_USESHOWWINDOW != 0 {
		startup.Flags = windows.STARTF_USESHOWWINDOW
		startup.ShowWindow = own.ShowWindow
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(application, commandLine, nil, nil, false, 0, nil, nil, &startup, &process); err != nil {
		return 0, err
	}
	windows.CloseHandle(process.Thread)
	windows.CloseHandle(process.Process)
	return process.ProcessId, nil
}

// relaunchCommandLine is the command line Relaunch starts executable with.
func relaunchCommandLine(executable string) string {
	return windows.EscapeArg(executable) + " " + RelaunchArgument
}

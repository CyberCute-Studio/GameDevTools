//go:build windows

package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/execute"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	// offlineWorkspaceEnv and offlinePortEnv hand the sandboxed test its
	// workspace and the loopback port it must fail to reach.
	offlineWorkspaceEnv = "GAMEDEVTOOLS_OFFLINE_WORKSPACE"
	offlinePortEnv      = "GAMEDEVTOOLS_OFFLINE_PORT"
	// offlineContainerName names one AppContainer profile, so a run that was
	// killed before removing it leaves one profile, which the next run
	// reuses and removes, rather than one per run.
	offlineContainerName = "GameDevTools.OfflineTest"
	// offlineDone is printed by the sandboxed test as its last act, so a run
	// that skipped or stopped early cannot pass for one that finished.
	offlineDone = "offline workflows completed"
	// offlineSentinel is in every source's file name, and is looked for in
	// what the application persists.
	offlineSentinel = "offline-sentinel-private-name"

	procThreadAttributeSecurityCapabilities = 0x00020009
	hresultAlreadyExists                    = 0x800700B7
	tokenIsAppContainer                     = 29
	tokenCapabilities                       = 30
)

var (
	userenv                        = windows.NewLazySystemDLL("userenv.dll")
	procCreateAppContainerProfile  = userenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile  = userenv.NewProc("DeleteAppContainerProfile")
	procDeriveAppContainerSidNamed = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

// securityCapabilities is SECURITY_CAPABILITIES with no capabilities, which
// is what denies the process the network.
type securityCapabilities struct {
	appContainerSid *windows.SID
	capabilities    uintptr
	capabilityCount uint32
	reserved        uint32
}

// offlineContainer creates the AppContainer profile the test runs in, and
// removes it when the test ends.
func offlineContainer(t *testing.T) *windows.SID {
	t.Helper()
	name, err := windows.UTF16PtrFromString(offlineContainerName)
	if err != nil {
		t.Fatal(err)
	}
	var sid *windows.SID
	result, _, _ := procCreateAppContainerProfile.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if uint32(result) == hresultAlreadyExists {
		result, _, _ = procDeriveAppContainerSidNamed.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&sid)))
	}
	if result != 0 {
		t.Fatalf("create the AppContainer profile: HRESULT %#x", uint32(result))
	}
	t.Cleanup(func() {
		windows.FreeSid(sid)
		procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name)))
	})
	return sid
}

// grantContainer gives the container full access to folder and everything
// created in it. It is the only part of the file system the container can
// write to.
func grantContainer(t *testing.T, folder string, sid *windows.SID) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, current)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

// offlineEnvironment is this process's environment with the per-user
// folders inside the workspace, as a UTF-16 environment block.
func offlineEnvironment(t *testing.T, workspace string, port int) *uint16 {
	t.Helper()
	replaced := map[string]string{
		"APPDATA":           filepath.Join(workspace, "Roaming"),
		"LOCALAPPDATA":      filepath.Join(workspace, "Local"),
		"TEMP":              filepath.Join(workspace, "Temp"),
		"TMP":               filepath.Join(workspace, "Temp"),
		offlineWorkspaceEnv: workspace,
		offlinePortEnv:      strconv.Itoa(port),
	}
	var block []uint16
	add := func(entry string) {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			t.Fatal(err)
		}
		block = append(block, encoded...)
	}
	for _, entry := range os.Environ() {
		// Windows keeps per-drive folders in entries that start with "=".
		name, _, found := strings.Cut(strings.TrimPrefix(entry, "="), "=")
		if _, replacing := replaced[strings.ToUpper(name)]; found && replacing {
			continue
		}
		add(entry)
	}
	for name, value := range replaced {
		add(name + "=" + value)
	}
	block = append(block, 0)
	return &block[0]
}

// runInContainer runs this test binary's named test in the container, in
// the workspace, returning its exit code and output.
func runInContainer(t *testing.T, sid *windows.SID, workspace, executable, test string, port int) (uint32, string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	output := windows.Handle(writer.Fd())
	if err := windows.SetHandleInformation(output, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		t.Fatal(err)
	}
	defer attributes.Delete()
	capabilities := securityCapabilities{appContainerSid: sid}
	if err := attributes.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&capabilities), unsafe.Sizeof(capabilities)); err != nil {
		t.Fatal(err)
	}
	inherited := []windows.Handle{output}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), unsafe.Sizeof(inherited[0])); err != nil {
		t.Fatal(err)
	}
	var startup windows.StartupInfoEx
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdOutput, startup.StdErr = output, output
	startup.ProcThreadAttributeList = attributes.List()
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{executable, "-test.run=^" + test + "$", "-test.v", "-test.timeout=3m"}))
	if err != nil {
		t.Fatal(err)
	}
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := windows.UTF16PtrFromString(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var process windows.ProcessInformation
	err = windows.CreateProcess(application, command, nil, nil, true,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		offlineEnvironment(t, workspace, port), directory, &startup.StartupInfo, &process)
	if err != nil {
		t.Fatalf("start the test in the AppContainer: %v", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	writer.Close()
	collected := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		collected <- data
	}()
	const limit = 200 * time.Second
	event, err := windows.WaitForSingleObject(process.Process, uint32(limit/time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if event != windows.WAIT_OBJECT_0 {
		windows.TerminateProcess(process.Process, 1)
		windows.WaitForSingleObject(process.Process, windows.INFINITE)
		t.Fatalf("the sandboxed workflows did not finish within %v:\n%s", limit, <-collected)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process.Process, &code); err != nil {
		t.Fatal(err)
	}
	return code, string(<-collected)
}

// TestCoreWorkflowsRunOffline pins NFR-030 and AC-A02: both core workflows
// complete with no network available. The test runs them on the real main
// window in a child process inside an AppContainer holding no capabilities,
// which Windows denies every network connection, loopback included, and
// every write outside the workspace this test grants it. That takes neither
// administrator rights nor switching off the machine's network adapters.
func TestCoreWorkflowsRunOffline(t *testing.T) {
	sid := offlineContainer(t)
	workspace := t.TempDir()
	grantContainer(t, workspace, sid)
	for _, folder := range []string{"Roaming", "Local", "Temp", "Pictures", "Sources"} {
		if err := os.MkdirAll(filepath.Join(workspace, folder), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(workspace, "offline.test.exe")
	if err := os.WriteFile(executable, data, 0o700); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var accepted atomic.Int32
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			connection.Close()
		}
	}()

	code, output := runInContainer(t, sid, workspace, executable, "TestOfflineWorkflowsInContainer", listener.Addr().(*net.TCPAddr).Port)
	if code != 0 || !strings.Contains(output, offlineDone) {
		t.Fatalf("the sandboxed workflows exited with %d:\n%s", code, output)
	}
	if count := accepted.Load(); count != 0 {
		t.Fatalf("the sandboxed process reached this machine's loopback listener %d time(s)", count)
	}
}

// TestOfflineWorkflowsInContainer is TestCoreWorkflowsRunOffline's child:
// inside the container it proves the network is out of reach, then runs a
// Full Display capture and an Image Resizer job through the real main
// window, exits, and checks what the application left behind.
func TestOfflineWorkflowsInContainer(t *testing.T) {
	workspace := os.Getenv(offlineWorkspaceEnv)
	if workspace == "" {
		t.Skip("runs only inside TestCoreWorkflowsRunOffline's AppContainer")
	}
	checkNetworkDenied(t)
	shell, paths := openOfflineShell(t, workspace)

	saved := runOfflineCapture(t, shell, workspace)
	if file, err := os.Open(saved); err != nil {
		t.Fatal(err)
	} else {
		picture, err := png.Decode(file)
		file.Close()
		if err != nil {
			t.Fatalf("the saved capture is not a complete PNG: %v", err)
		}
		if bounds := picture.Bounds(); bounds.Dx() <= 0 || bounds.Dy() <= 0 {
			t.Fatalf("the saved capture is %v", bounds)
		}
	}
	runOfflineResize(t, shell, workspace)

	exitShell(t, shell, true)
	checkOfflineLog(t, paths.LogFile, workspace)
	settingsData, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(settingsData, []byte(offlineSentinel)) {
		t.Fatal("the settings file records a source image's name")
	}
	checkOfflineWrites(t, workspace, paths)
	fmt.Println(offlineDone)
}

// checkNetworkDenied proves the process runs in a capability-less
// AppContainer and that connections fail by policy. 192.0.2.1 is reserved
// for documentation (RFC 5737) and routes nowhere, so the probe can reach
// nothing even if the sandbox were to let it through, and only the access
// error shows that the sandbox refused it.
func checkNetworkDenied(t *testing.T) {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	var isContainer, returned uint32
	if err := windows.GetTokenInformation(token, tokenIsAppContainer, (*byte)(unsafe.Pointer(&isContainer)), uint32(unsafe.Sizeof(isContainer)), &returned); err != nil {
		t.Fatal(err)
	}
	if isContainer == 0 {
		t.Fatal("the test is not running in an AppContainer")
	}
	buffer := make([]byte, 4096)
	if err := windows.GetTokenInformation(token, tokenCapabilities, &buffer[0], uint32(len(buffer)), &returned); err != nil {
		t.Fatal(err)
	}
	if groups := (*windows.Tokengroups)(unsafe.Pointer(&buffer[0])); groups.GroupCount != 0 {
		t.Fatalf("the AppContainer holds %d capabilities, want none", groups.GroupCount)
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.Dial("tcp4", "192.0.2.1:9")
	if err == nil {
		connection.Close()
		t.Fatal("a connection to a remote address succeeded")
	}
	if !errors.Is(err, windows.WSAEACCES) {
		t.Fatalf("a remote connection failed with %v, want the sandbox's access error", err)
	}
	connection, err = dialer.Dial("tcp4", net.JoinHostPort("127.0.0.1", os.Getenv(offlinePortEnv)))
	if err == nil {
		connection.Close()
		t.Fatal("a connection to the loopback listener succeeded")
	}
}

// openOfflineShell builds the real main window as runNative does: the
// per-user paths from the environment, the session, and the diagnostic log.
func openOfflineShell(t *testing.T, workspace string) (*auditShell, settings.Paths) {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	paths, err := settings.LocalPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.ConfigFile, paths.LogFile} {
		if !strings.HasPrefix(path, workspace+string(filepath.Separator)) {
			t.Fatalf("per-user path %s is outside the workspace", path)
		}
	}
	state, err := openSession(paths, nil, defaultFolders{
		screenshot: filepath.Join(workspace, "Pictures", "GameDevTools", "Screenshots"),
		resizer:    filepath.Join(workspace, "Pictures", "GameDevTools", "Resized"),
	})
	if err != nil {
		t.Fatal(err)
	}
	logger, err := diagnostics.Open(paths.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	shortcut, err := screenshot.ParseShortcut("Ctrl+Alt+Shift+F24")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.capture.SetShortcut(shortcut); err != nil {
		t.Fatal(err)
	}
	monitor := display.NewMonitor()
	executor := execute.NewService(monitor)
	navigator, err := newNavigator(state, monitor, executor.FullDisplay, executor.Rectangle, overlay.NewController(monitor))
	if err != nil {
		t.Fatal(err)
	}
	shell, err := newNativeShell(navigator, state.store, state.value, logger, monitor, state.startupWarnings())
	if err != nil {
		t.Fatal(err)
	}
	procShowWindow.Call(uintptr(shell.hwnd), swShowNoActivate)
	return &auditShell{nativeShell: shell, capture: state.capture, resize: state.resize, path: paths.ConfigFile}, paths
}

// runOfflineCapture runs Start Capture in Full Display mode until a capture
// is both saved and on the clipboard, and returns the saved file. A test in
// another package holding the shared clipboard makes one attempt report a
// clipboard failure, so a failure of that kind is tried again.
func runOfflineCapture(t *testing.T, shell *auditShell, workspace string) string {
	t.Helper()
	if err := shell.capture.SetMode(screenshot.ModeFullDisplay); err != nil {
		t.Fatal(err)
	}
	shell.selectModule(t, core.ModuleScreenshot)
	folder := filepath.Join(workspace, "Pictures", "GameDevTools", "Screenshots")
	seen := map[string]bool{}
	var last string
	for range 5 {
		shell.nativeViews[core.ModuleScreenshot].(nativeActionHandler).NativeAction()
		var saved string
		shell.pump(t, 60*time.Second, func() bool {
			entries, err := os.ReadDir(folder)
			if err != nil {
				return false
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".png") && !seen[entry.Name()] {
					saved = filepath.Join(folder, entry.Name())
				}
			}
			if saved == "" {
				return false
			}
			for _, child := range shell.children() {
				if text := windowText(child); strings.Contains(text, saved) {
					last = text
					return true
				}
			}
			return false
		})
		seen[filepath.Base(saved)] = true
		if strings.HasPrefix(last, ui.NoticeSuccessLabel+":") {
			return saved
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no capture reached both destinations; the last outcome was %q", last)
	return ""
}

// runOfflineResize runs a real Image Resizer job and checks its results and
// that its sources are unchanged.
func runOfflineResize(t *testing.T, shell *auditShell, workspace string) {
	t.Helper()
	sources := make([]string, 0, 3)
	originals := map[string][]byte{}
	for index := range 3 {
		source := writePNG(t, filepath.Join(workspace, "Sources", fmt.Sprintf("%s-%02d.png", offlineSentinel, index)), 64, 48)
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		sources, originals[source] = append(sources, source), data
	}
	addSources(t, shell, sources...)
	resizer := shell.nativeViews[core.ModuleImageResizer]
	resizer.(nativeActionHandler).NativeAction()
	shell.pump(t, 60*time.Second, func() bool {
		_, message := resizer.(nativeCloseGuard).NativeCloseConfirmation()
		return message == ""
	})
	output := filepath.Join(workspace, "Pictures", "GameDevTools", "Resized")
	if results := checkResults(t, output); results != len(sources) {
		t.Fatalf("the job saved %d result(s), want %d", results, len(sources))
	}
	for source, data := range originals {
		if current, err := os.ReadFile(source); err != nil || !bytes.Equal(current, data) {
			t.Fatalf("source %s changed: err = %v", filepath.Base(source), err)
		}
	}
}

// checkOfflineLog pins SET-007 on a real run's log: every line is one
// event of exactly the four fixed fields, and none of it names a file, a
// folder, or the workspace.
func checkOfflineLog(t *testing.T, path, workspace string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{offlineSentinel, filepath.Base(workspace), "Screenshot_", ".png", ".jpg", `:\\`} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("the log contains %q:\n%s", forbidden, data)
		}
	}
	events := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var event struct {
			Time      time.Time `json:"time"`
			Level     string    `json:"level"`
			Operation string    `json:"operation"`
			Code      string    `json:"code"`
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("log line %q: %v", scanner.Text(), err)
		}
		if event.Time.IsZero() || event.Level == "" || event.Operation == "" || event.Code == "" {
			t.Fatalf("log line %q leaves a field empty", scanner.Text())
		}
		events++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Fatal("the run logged nothing, so the log's contents were not exercised")
	}
}

// checkOfflineWrites pins NFR-032 on a real run: every file in the
// workspace, the only place the container can write, is in the settings or
// log folder, an output folder, or the test's own sources. The settings and
// log folders are taken from the paths the application resolved, as Windows
// points a container's per-user folders into its own profile.
func checkOfflineWrites(t *testing.T, workspace string, paths settings.Paths) {
	t.Helper()
	approved := []string{
		filepath.Dir(paths.ConfigFile),
		filepath.Dir(paths.LogFile),
		filepath.Join(workspace, "Pictures", "GameDevTools", "Screenshots"),
		filepath.Join(workspace, "Pictures", "GameDevTools", "Resized"),
		filepath.Join(workspace, "Sources"),
	}
	err := filepath.WalkDir(workspace, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == filepath.Join(workspace, "offline.test.exe") {
			return nil
		}
		for _, root := range approved {
			if strings.HasPrefix(path, root+string(filepath.Separator)) {
				if strings.HasPrefix(entry.Name(), ".gamedevtools-") || strings.HasSuffix(entry.Name(), ".tmp") {
					t.Errorf("temporary file %s remains", path)
				}
				return nil
			}
		}
		t.Errorf("the run wrote %s, outside the approved folders", path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

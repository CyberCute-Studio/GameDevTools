//go:build windows

package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

const (
	// validationEnv turns on the tests that launch the real release
	// executable. scripts/validate.ps1 sets it and runs them alone, because a
	// window another process shows can take the foreground from the tests of
	// a package `go test ./...` runs at the same time.
	validationEnv = "GAMEDEVTOOLS_VALIDATION"

	launchLimit        = 30 * time.Second
	exitLimit          = 20 * time.Second
	launchCleanupLimit = 10 * time.Second

	launchWMNull        = 0x0000
	launchSMTOAbortHung = 0x0002
	launchProbeMillis   = 1000

	restrictedDisableMaxPrivilege = 0x1
	restrictedLUAToken            = 0x4
)

var (
	launchAdvapi32                = windows.NewLazySystemDLL("advapi32.dll")
	procLaunchCreateRestricted    = launchAdvapi32.NewProc("CreateRestrictedToken")
	procLaunchFindWindowExW       = user32.NewProc("FindWindowExW")
	procLaunchSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

// requireValidationRun skips a test that launches the real executable unless
// the validation script asked for it, or when an instance of the application
// is already running for this user: a launch would then activate that window
// and exit rather than start.
func requireValidationRun(tb testing.TB) {
	tb.Helper()
	if os.Getenv(validationEnv) == "" {
		tb.Skipf("set %s to launch the release executable; scripts/validate.ps1 does", validationEnv)
	}
	if instanceRunning(tb) {
		tb.Skip(ui.AppName + " is running; close it to validate the release executable")
	}
}

// instanceRunning reports whether the single-instance mutex exists.
func instanceRunning(tb testing.TB) bool {
	tb.Helper()
	name, err := windows.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		tb.Fatal(err)
	}
	handle, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err == nil {
		windows.CloseHandle(handle)
		return true
	}
	return !errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}

// launchTempDir is a temporary folder for an executable that has run and the
// files it wrote. Another program, such as a virus scanner or the x64 emulation
// cache, can hold a program that has just exited for a moment, which fails
// testing.TB.TempDir's cleanup, so removal waits that out. A folder still held
// after the wait is logged rather than failing a run whose checks all passed.
func launchTempDir(tb testing.TB) string {
	tb.Helper()
	folder, err := os.MkdirTemp("", "gamedevtools-validation-*")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		var err error
		for deadline := time.Now().Add(launchCleanupLimit); ; time.Sleep(100 * time.Millisecond) {
			if err = os.RemoveAll(folder); err == nil {
				return
			}
			if time.Now().After(deadline) {
				tb.Logf("left a temporary folder behind: %v", err)
				return
			}
		}
	})
	return folder
}

// buildReleaseExecutable builds the application with the release flags
// scripts/build.ps1 uses, for the architecture this test binary runs as.
func buildReleaseExecutable(tb testing.TB) string {
	tb.Helper()
	executable := filepath.Join(launchTempDir(tb), ui.AppFileName+".exe")
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -H=windowsgui -buildid=", "-o", executable,
		"github.com/CyberCute-Studio/GameDevTools/cmd/gamedevtools")
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		tb.Fatalf("build the release executable: %v\n%s", err, output)
	}
	return executable
}

// launchWorkspace is a folder for the launched application's per-user data,
// so a validation run neither reads nor changes the user's own settings.
func launchWorkspace(tb testing.TB) string {
	tb.Helper()
	workspace := launchTempDir(tb)
	for _, folder := range []string{"Roaming", "Local", "Temp"} {
		if err := os.MkdirAll(filepath.Join(workspace, folder), 0o755); err != nil {
			tb.Fatal(err)
		}
	}
	return workspace
}

// standardUserToken returns a token without administrator rights when this
// process is elevated, as on GitHub's hosted runners, or 0 when it already
// runs without them. The caller closes a token it receives.
func standardUserToken(tb testing.TB) windows.Token {
	tb.Helper()
	var current windows.Token
	access := uint32(windows.TOKEN_DUPLICATE | windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_QUERY | windows.TOKEN_ADJUST_DEFAULT)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access, &current); err != nil {
		tb.Fatal(err)
	}
	defer current.Close()
	if !current.IsElevated() {
		return 0
	}
	var restricted windows.Token
	result, _, err := procLaunchCreateRestricted.Call(uintptr(current), restrictedDisableMaxPrivilege|restrictedLUAToken,
		0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if result == 0 {
		tb.Fatalf("CreateRestrictedToken: %v", err)
	}
	medium, err := windows.CreateWellKnownSid(windows.WinMediumLabelSid)
	if err != nil {
		restricted.Close()
		tb.Fatal(err)
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: medium, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		restricted.Close()
		tb.Fatalf("lower the token's integrity level: %v", err)
	}
	if err := grantUserDefaultDacl(restricted); err != nil {
		restricted.Close()
		tb.Fatalf("set the token's default DACL: %v", err)
	}
	return restricted
}

// grantUserDefaultDacl makes the objects a token's process creates open to that
// token's user and to SYSTEM. An elevated token's default DACL grants
// Administrators, which a restricted token holds only as deny-only, so a second
// process could not open the single-instance mutex the first one created.
func grantUserDefaultDacl(token windows.Token) error {
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	grant := func(sid *windows.SID) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{grant(user.User.Sid), grant(system)}, nil)
	if err != nil {
		return err
	}
	// TOKEN_DEFAULT_DACL, whose only member is the pointer to the ACL.
	info := struct{ defaultDacl *windows.ACL }{acl}
	return windows.SetTokenInformation(token, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
}

// launchedApp is one running process of the release executable.
type launchedApp struct {
	command *exec.Cmd
	done    chan error
	hwnd    uintptr
	// elapsed is the time from just before the process was created until its
	// main window was visible and its message loop answered.
	elapsed time.Duration
}

// startRelease creates the process, without administrator rights, with its
// per-user folders in workspace.
func startRelease(tb testing.TB, executable, workspace string) *launchedApp {
	tb.Helper()
	command := exec.Command(executable)
	command.Dir = workspace
	command.Env = append(os.Environ(),
		"APPDATA="+filepath.Join(workspace, "Roaming"),
		"LOCALAPPDATA="+filepath.Join(workspace, "Local"),
		"TEMP="+filepath.Join(workspace, "Temp"),
		"TMP="+filepath.Join(workspace, "Temp"),
	)
	if token := standardUserToken(tb); token != 0 {
		defer token.Close()
		command.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token)}
	}
	if err := command.Start(); err != nil {
		tb.Fatalf("start the release executable: %v", err)
	}
	app := &launchedApp{command: command, done: make(chan error, 1)}
	go func() { app.done <- command.Wait() }()
	tb.Cleanup(func() {
		select {
		case <-app.done:
			app.done <- nil
		default:
			command.Process.Kill()
			<-app.done
			app.done <- nil
		}
	})
	return app
}

// launchRelease starts the executable and waits until its main window is
// visible and its message loop answers, which is what NFR-010 calls launched.
func launchRelease(tb testing.TB, executable, workspace string) *launchedApp {
	tb.Helper()
	started := time.Now()
	app := startRelease(tb, executable, workspace)
	pid := uint32(app.command.Process.Pid)
	for deadline := started.Add(launchLimit); ; time.Sleep(2 * time.Millisecond) {
		select {
		case err := <-app.done:
			app.done <- err
			tb.Fatalf("the application exited before showing its window: %v", err)
		default:
		}
		if hwnd := findMainWindow(pid); hwnd != 0 && windows.IsWindowVisible(windows.HWND(hwnd)) && windowAnswers(hwnd) {
			app.hwnd = hwnd
			app.elapsed = time.Since(started)
			return app
		}
		if time.Now().After(deadline) {
			tb.Fatalf("no responsive main window within %v", launchLimit)
		}
	}
}

// findMainWindow returns process pid's main window, or 0.
func findMainWindow(pid uint32) uintptr {
	class := utf16(shellClassName)
	var after uintptr
	for {
		hwnd, _, _ := procLaunchFindWindowExW.Call(0, after, uintptr(unsafe.Pointer(class)), 0)
		if hwnd == 0 {
			return 0
		}
		var owner uint32
		windows.GetWindowThreadProcessId(windows.HWND(hwnd), &owner)
		if owner == pid {
			return hwnd
		}
		after = hwnd
	}
}

// windowAnswers reports whether the window's message loop handles a message.
func windowAnswers(hwnd uintptr) bool {
	var result uintptr
	answered, _, _ := procLaunchSendMessageTimeoutW.Call(hwnd, launchWMNull, 0, 0, launchSMTOAbortHung, launchProbeMillis, uintptr(unsafe.Pointer(&result)))
	return answered != 0
}

// holdsAdministratorRights reports whether the process has the Administrators
// group enabled in its token.
func (a *launchedApp) holdsAdministratorRights(tb testing.TB) bool {
	tb.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(a.command.Process.Pid))
	if err != nil {
		tb.Fatal(err)
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		tb.Fatal(err)
	}
	defer token.Close()
	groups, err := token.GetTokenGroups()
	if err != nil {
		tb.Fatal(err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		tb.Fatal(err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.Equals(administrators) && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			return true
		}
	}
	return false
}

// close asks the main window to close, as its close button does, and waits
// for the process to exit successfully.
func (a *launchedApp) close(tb testing.TB) {
	tb.Helper()
	if posted, _, err := procPostMessageW.Call(a.hwnd, wmClose, 0, 0); posted == 0 {
		tb.Fatalf("PostMessage(WM_CLOSE): %v", err)
	}
	a.awaitExit(tb)
}

// awaitExit waits for the process to exit with code 0.
func (a *launchedApp) awaitExit(tb testing.TB) {
	tb.Helper()
	select {
	case err := <-a.done:
		a.done <- err
		if err != nil {
			tb.Fatalf("the application exited with an error: %v", err)
		}
	case <-time.After(exitLimit):
		tb.Fatalf("the application did not exit within %v", exitLimit)
	}
}

// restartPresetID is the custom preset changeSavedSettings adds.
const restartPresetID = "custom.0123456789abcdef0123456789abcdef"

// readSavedSettings decodes the settings file a run of the application wrote.
func readSavedSettings(tb testing.TB, path string) settings.Settings {
	tb.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	var saved settings.Settings
	if err := json.Unmarshal(encoded, &saved); err != nil {
		tb.Fatalf("decode the settings file: %v", err)
	}
	return saved
}

// changeSavedSettings makes the file hold what no default supplies, the Image
// Resizer as the current module and a selected custom Screenshot preset, so
// that only loading the file can bring them back after a restart (AC-A01).
func changeSavedSettings(tb testing.TB, path string) {
	tb.Helper()
	saved := readSavedSettings(tb, path)
	saved.Shell.SelectedModule = string(core.ModuleImageResizer)
	section := map[string]json.RawMessage{}
	if existing, ok := saved.Modules["screenshot"]; ok {
		if err := json.Unmarshal(existing, &section); err != nil {
			tb.Fatal(err)
		}
	}
	section["presets"] = json.RawMessage(`{"version":1,"custom":[{"id":"` + restartPresetID +
		`","name":"Kept across a restart","width":320,"height":240}],"selectedPresetId":"` + restartPresetID + `"}`)
	encodedSection, err := json.Marshal(section)
	if err != nil {
		tb.Fatal(err)
	}
	if saved.Modules == nil {
		saved.Modules = make(map[string]json.RawMessage)
	}
	saved.Modules["screenshot"] = encodedSection
	encoded, err := json.Marshal(saved)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		tb.Fatal(err)
	}
}

// requireSavedSettingsKept fails unless the file still holds what
// changeSavedSettings put there. The application writes the file as it exits,
// so one that started from the defaults instead would have replaced them.
func requireSavedSettingsKept(tb testing.TB, path string) {
	tb.Helper()
	saved := readSavedSettings(tb, path)
	if saved.Shell.SelectedModule != string(core.ModuleImageResizer) {
		tb.Fatalf("the selected module after a restart is %q, want %q", saved.Shell.SelectedModule, core.ModuleImageResizer)
	}
	var section struct {
		Presets struct {
			Custom []struct {
				ID string `json:"id"`
			} `json:"custom"`
			SelectedPresetID string `json:"selectedPresetId"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(saved.Modules["screenshot"], &section); err != nil {
		tb.Fatalf("decode the Screenshot settings: %v", err)
	}
	presets := section.Presets
	if len(presets.Custom) != 1 || presets.Custom[0].ID != restartPresetID || presets.SelectedPresetID != restartPresetID {
		tb.Fatalf("the custom preset did not survive a restart: %+v", presets)
	}
}

// TestReleaseExecutableRunsAsStandardUserAndExitsCleanly runs the executable
// that ships, end to end, without administrator rights (NFR-002): it starts,
// answers, refuses to run twice (APP-005), persists its settings, exits with
// nothing left holding the instance (AC-A05), and, given a settings file
// changed to hold another module and a custom preset, starts again, keeps
// both, and needs no recovery (AC-A01).
func TestReleaseExecutableRunsAsStandardUserAndExitsCleanly(t *testing.T) {
	requireValidationRun(t)
	executable := buildReleaseExecutable(t)
	workspace := launchWorkspace(t)

	first := launchRelease(t, executable, workspace)
	if first.holdsAdministratorRights(t) {
		t.Fatal("the application ran with the Administrators group enabled")
	}

	second := startRelease(t, executable, workspace)
	second.awaitExit(t)
	if hwnd := findMainWindow(uint32(first.command.Process.Pid)); hwnd != first.hwnd {
		t.Fatalf("the running instance's main window changed from %#x to %#x", first.hwnd, hwnd)
	}

	first.close(t)
	if instanceRunning(t) {
		t.Fatal("the single-instance mutex outlived the process")
	}
	configFile := filepath.Join(workspace, "Roaming", ui.AppName, "settings.json")
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("the settings file was not written: %v", err)
	}
	changeSavedSettings(t, configFile)

	restarted := launchRelease(t, executable, workspace)
	restarted.close(t)
	requireSavedSettingsKept(t, configFile)
	// Named as the settings store names its backups and its temporary files.
	entries, err := os.ReadDir(filepath.Dir(configFile))
	if err != nil {
		t.Fatal(err)
	}
	var leftovers []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "settings.json.") || (strings.HasPrefix(name, ".settings-") && strings.HasSuffix(name, ".tmp")) {
			leftovers = append(leftovers, name)
		}
	}
	if len(leftovers) != 0 {
		t.Fatalf("a restart left recovery or temporary files: %v", leftovers)
	}
}

// BenchmarkWarmLaunchToResponsiveMainWindow measures NFR-010: one op is the
// release executable from process creation to a visible main window whose
// message loop answers. A first launch, which fills the file cache and writes
// the settings file, is not timed, so every timed launch is a warm start.
func BenchmarkWarmLaunchToResponsiveMainWindow(b *testing.B) {
	requireValidationRun(b)
	executable := buildReleaseExecutable(b)
	workspace := launchWorkspace(b)
	launchRelease(b, executable, workspace).close(b)

	var slowest time.Duration
	b.ResetTimer()
	b.StopTimer()
	for range b.N {
		b.StartTimer()
		app := launchRelease(b, executable, workspace)
		b.StopTimer()
		slowest = max(slowest, app.elapsed)
		app.close(b)
	}
	b.ReportMetric(float64(slowest)/float64(time.Millisecond), "max-ms")
}

//go:build windows

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	lbGetText    = 0x0189
	lbGetTextLen = 0x018a
)

// restoreProbe stands in for the Screenshot view's restore
// (nativeDefaultsRestorer), counting the shell's requests. The real view
// registers the default global shortcut, Ctrl+Alt+S, which is machine-wide
// and must not be taken from the developer while the tests run;
// TestNativeViewRestoreDefaultsShowsAndRegistersTheDefaults covers the view
// with a fake registrar. Every other hook is the view's own, as the probe
// embeds it, so a test that also closes the shell, sends the global
// shortcut, or announces a notice reaches the real view.
type restoreProbe struct {
	*screenshot.NativeView
	calls *int
}

func (p restoreProbe) NativeRestoreDefaults() string {
	*p.calls++
	return ""
}

// The probe offers every hook the Screenshot view does but the restore.
var (
	_ nativeView            = restoreProbe{}
	_ nativeActionHandler   = restoreProbe{}
	_ nativeHotkeyHandler   = restoreProbe{}
	_ nativeShutdown        = restoreProbe{}
	_ nativeMessageFilter   = restoreProbe{}
	_ nativeNoticeAnnouncer = restoreProbe{}
)

// probeScreenshotRestore puts a restoreProbe in place of the Screenshot view
// for the test.
func probeScreenshotRestore(t *testing.T, shell *auditShell) *int {
	t.Helper()
	calls := 0
	screenshotView := shell.nativeViews[core.ModuleScreenshot]
	shell.nativeViews[core.ModuleScreenshot] = restoreProbe{NativeView: screenshotView.(*screenshot.NativeView), calls: &calls}
	t.Cleanup(func() { shell.nativeViews[core.ModuleScreenshot] = screenshotView })
	return &calls
}

// listRows returns the rows of a list box.
func listRows(list windows.Handle) []string {
	count, _, _ := procSendMessageW.Call(uintptr(list), lbGetCount, 0, 0)
	rows := make([]string, 0, int(count))
	for index := uintptr(0); index < count; index++ {
		length, _, _ := procSendMessageW.Call(uintptr(list), lbGetTextLen, index, 0)
		buffer := make([]uint16, length+1)
		procSendMessageW.Call(uintptr(list), lbGetText, index, uintptr(unsafe.Pointer(&buffer[0])))
		rows = append(rows, windows.UTF16ToString(buffer))
	}
	return rows
}

// writePNG writes a width × height PNG with a gradient, so a resize has
// work to do, and returns its path.
func writePNG(t *testing.T, path string, width, height int) string {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			offset := picture.PixOffset(x, y)
			picture.Pix[offset], picture.Pix[offset+1], picture.Pix[offset+2], picture.Pix[offset+3] = uint8(x), uint8(y), uint8(x+y), 255
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// addSources drops the paths on the Image Resizer view, as Explorer does,
// and waits until every row has been checked.
func addSources(t *testing.T, shell *auditShell, paths ...string) windows.Handle {
	t.Helper()
	shell.selectModule(t, core.ModuleImageResizer)
	shell.nativeViews[core.ModuleImageResizer].(nativeDropTarget).NativeDropFiles(paths)
	list := shell.control(t, "ListBox Source images")
	shell.pump(t, 30*time.Second, func() bool {
		rows := listRows(list)
		if len(rows) != len(paths) {
			return false
		}
		for _, row := range rows {
			if !strings.HasSuffix(row, " px — "+ui.SourceStatusReady) {
				return false
			}
		}
		return true
	})
	return list
}

// TestRestoreDefaultsKeepsPresetsImagesAndTheSession pins Restore Default
// Settings on the real main window (SET-005, APP-004): the settings file is
// backed up byte for byte and replaced by the defaults, which keep both
// preset libraries with their selected presets and the window placement and
// current module, and a restart loads them; the session takes the default
// options, while the unsaved edits in both preset forms, the images in the
// resize list, and every saved and source image are kept.
func TestRestoreDefaultsKeepsPresetsImagesAndTheSession(t *testing.T) {
	shell := newAuditShellOn(t, readFixture(t, "rsz-01"))
	calls := probeScreenshotRestore(t, shell)
	directory := filepath.Dir(shell.path)
	images := map[string][]byte{}
	for _, path := range []string{
		writePNG(t, filepath.Join(directory, "Sources", "photo.png"), 32, 24),
		writePNG(t, filepath.Join(directory, "Screenshots", "Screenshot_20261120_120000000.png"), 8, 8),
		writePNG(t, filepath.Join(directory, "Resized", "photo.png"), 8, 6),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		images[path] = data
	}
	list := addSources(t, shell, filepath.Join(directory, "Sources", "photo.png"))
	setWindowText(shell.control(t, "Edit Name"), "Unsaved resize preset")
	shell.selectModule(t, core.ModuleScreenshot)
	setWindowText(shell.control(t, "Edit Name"), "Unsaved capture preset")
	shell.selectModule(t, core.ModuleSettings)
	window := shell.settings.Shell
	before := reopenSession(t, shell.path)
	captureBefore, resizeBefore := libraries(t, before)
	original, err := os.ReadFile(shell.path)
	if err != nil {
		t.Fatal(err)
	}

	notice := shell.restoreDefaults()
	backups, err := filepath.Glob(shell.path + ".reset-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v, want one", backups, err)
	}
	if want := (ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(ui.SettingsRestored, backups[0])}); notice != want {
		t.Fatalf("outcome = %+v, want %+v", notice, want)
	}
	if copied, err := os.ReadFile(backups[0]); err != nil || !bytes.Equal(copied, original) {
		t.Fatalf("backup differs from the replaced file: err = %v", err)
	}
	if *calls != 1 {
		t.Fatalf("the Screenshot view was asked %d time(s) to show the defaults, want 1", *calls)
	}
	if shell.settings.Shell != window {
		t.Fatalf("shell settings after the restore = %+v, want %+v kept", shell.settings.Shell, window)
	}

	restarted := reopenSession(t, shell.path)
	if len(restarted.warnings()) != 0 {
		t.Fatalf("restart warnings = %v", restarted.warnings())
	}
	if restarted.value.Shell != window {
		t.Fatalf("shell settings after a restart = %+v, want %+v", restarted.value.Shell, window)
	}
	var stored map[string]map[string]json.RawMessage
	if err := json.Unmarshal(mustJSON(t, restarted.value.Modules), &stored); err != nil {
		t.Fatal(err)
	}
	for module, section := range stored {
		if len(section) != 1 || section["presets"] == nil {
			t.Fatalf("%s section after the restore = %s, want only its presets", module, mustJSON(t, section))
		}
	}
	captureAfter, resizeAfter := libraries(t, restarted)
	for name, pair := range map[string][2][]string{
		"Screenshot":    {presetSummary(captureBefore.Custom(), captureBefore.SelectedID()), presetSummary(captureAfter.Custom(), captureAfter.SelectedID())},
		"Image Resizer": {presetSummary(resizeBefore.Custom(), resizeBefore.SelectedID()), presetSummary(resizeAfter.Custom(), resizeAfter.SelectedID())},
	} {
		if strings.Join(pair[0], "\n") != strings.Join(pair[1], "\n") {
			t.Fatalf("%s presets after the restore = %q, want %q", name, pair[1], pair[0])
		}
	}
	if got, want := restarted.resize.Options(), imageresizer.DefaultOptions(filepath.Join(directory, "Resized")); got != want {
		t.Fatalf("resize options after a restart = %+v, want the defaults", got)
	}
	if got, want := shell.resize.Options(), imageresizer.DefaultOptions(filepath.Join(directory, "Resized")); got != want {
		t.Fatalf("resize options in the session = %+v, want the defaults", got)
	}

	shell.selectModule(t, core.ModuleImageResizer)
	if got := windowText(shell.control(t, "Edit Output folder")); got != filepath.Join(directory, "Resized") {
		t.Fatalf("resize folder shown = %q, want the default", got)
	}
	if got := windowText(shell.control(t, "Edit Name")); got != "Unsaved resize preset" {
		t.Fatalf("Image Resizer preset name after the restore = %q, want the unsaved edit kept", got)
	}
	if rows := listRows(list); len(rows) != 1 || !strings.HasPrefix(rows[0], "photo.png") {
		t.Fatalf("resize list after the restore = %q, want the image kept", rows)
	}
	shell.selectModule(t, core.ModuleScreenshot)
	if got := windowText(shell.control(t, "Edit Name")); got != "Unsaved capture preset" {
		t.Fatalf("Screenshot preset name after the restore = %q, want the unsaved edit kept", got)
	}
	for path, data := range images {
		if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, data) {
			t.Fatalf("image %s changed: err = %v", path, err)
		}
	}
}

// TestRestoreDefaultsChangesNothingWithoutABackup pins that a settings file
// the restore cannot back up is left as it is and so is the session: the
// outcome says that nothing was changed.
func TestRestoreDefaultsChangesNothingWithoutABackup(t *testing.T) {
	shell := newAuditShellOn(t, readFixture(t, "rsz-01"))
	calls := probeScreenshotRestore(t, shell)
	before := string(mustJSON(t, *shell.settings))
	options := shell.resize.Options()
	if err := os.Remove(shell.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shell.path, 0o700); err != nil {
		t.Fatal(err)
	}

	notice := shell.restoreDefaults()
	if notice.Kind != ui.NoticeError || !strings.HasPrefix(notice.Message, "Default settings were not restored: read the current settings") {
		t.Fatalf("outcome = %+v, want the restore refused", notice)
	}
	if got := string(mustJSON(t, *shell.settings)); got != before {
		t.Fatalf("session settings changed:\n%s\nwant:\n%s", got, before)
	}
	if got := shell.resize.Options(); got != options || *calls != 0 {
		t.Fatalf("resize options = %+v and %d view restore(s), want them unchanged", got, *calls)
	}
}

// TestRestoreDefaultsKeepsAnotherBuildsModuleAsStored pins that a settings
// file a newer build wrote, with a module this build does not know whose
// section holds a preset library — which loading the settings accepts — can
// still be restored: that library is kept exactly as stored, as every
// module's is, the rest of its section is reset, and a restart loads the
// file without a warning.
func TestRestoreDefaultsKeepsAnotherBuildsModuleAsStored(t *testing.T) {
	var stored settings.Settings
	if err := json.Unmarshal(readFixture(t, "rsz-01"), &stored); err != nil {
		t.Fatal(err)
	}
	library := `{"version":2,"custom":[{"id":"custom.0123456789abcdef0123456789abcdef","name":"Sprite","width":64,"height":64,"layer":"top"}]}`
	stored.Modules["editor"] = json.RawMessage(`{"canvas":{"zoom":2},"presets":` + library + `}`)
	shell := newAuditShellOn(t, mustJSON(t, stored))
	probeScreenshotRestore(t, shell)
	if got := shell.note.Text(); got != "" {
		t.Fatalf("note at startup = %q, want the file loaded without a warning", got)
	}

	if notice := shell.restoreDefaults(); notice.Kind != ui.NoticeSuccess {
		t.Fatalf("outcome = %+v, want the restore written", notice)
	}
	restarted := reopenSession(t, shell.path)
	if len(restarted.warnings()) != 0 {
		t.Fatalf("restart warnings = %v", restarted.warnings())
	}
	var section bytes.Buffer
	if err := json.Compact(&section, restarted.value.Modules["editor"]); err != nil {
		t.Fatal(err)
	}
	if got, want := section.String(), `{"presets":`+library+`}`; got != want {
		t.Fatalf("editor section after the restore = %s, want only its library as stored %s", got, want)
	}
}

// TestRestoreDefaultsKeepsTheRestoredSessionWhenTheWriteFails pins SET-004
// for the restore: once the file is backed up, a write that fails leaves
// the file as it was and the session restored, with a warning that says so
// and names the backup, as saving works again and the next save that
// succeeds replaces the file.
func TestRestoreDefaultsKeepsTheRestoredSessionWhenTheWriteFails(t *testing.T) {
	shell := newAuditShellOn(t, readFixture(t, "rsz-01"))
	calls := probeScreenshotRestore(t, shell)
	original, err := os.ReadFile(shell.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shell.path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(shell.path, 0o600) })

	notice := shell.restoreDefaults()
	backups, _ := filepath.Glob(shell.path + ".reset-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one", backups)
	}
	if notice.Kind != ui.NoticeWarning || !strings.HasPrefix(notice.Message, "Default settings are in use for this session, but they could not be saved") || !strings.HasSuffix(notice.Message, "backed up to "+backups[0]+".") {
		t.Fatalf("outcome = %+v, want the write failure naming the backup %s", notice, backups[0])
	}
	if current, err := os.ReadFile(shell.path); err != nil || !bytes.Equal(current, original) {
		t.Fatalf("settings file changed: err = %v", err)
	}
	if got, want := shell.resize.Options(), imageresizer.DefaultOptions(filepath.Join(filepath.Dir(shell.path), "Resized")); got != want || *calls != 1 {
		t.Fatalf("resize options = %+v and %d view restore(s), want the defaults and one", got, *calls)
	}
}

// TestRestoreDefaultsWithoutASettingsFileWhenTheWriteFails pins the outcome
// of a restore that finds no settings file and cannot write one: the
// defaults are in use for the session, and the outcome says that nothing
// was backed up, and that a file on an unavailable drive was not changed,
// rather than that a settings file still holds the previous settings, as none
// was found.
func TestRestoreDefaultsWithoutASettingsFileWhenTheWriteFails(t *testing.T) {
	shell, _ := newExitTestShell(t, &exitRecordingView{})
	blocker := filepath.Join(t.TempDir(), "not a folder")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	shell.store = settings.NewStore(settings.Paths{ConfigFile: filepath.Join(blocker, "settings.json")})

	notice := shell.restoreDefaults()
	if notice.Kind != ui.NoticeWarning || !strings.HasPrefix(notice.Message, "Default settings are in use for this session, but they could not be saved") || !strings.HasSuffix(notice.Message, "No settings file was found to back up; a file on a drive or network share that is unavailable has not been changed.") {
		t.Fatalf("outcome = %+v, want the write failure without a settings file", notice)
	}
	if strings.Contains(notice.Message, "still holds") {
		t.Fatalf("outcome = %q names a settings file there is not", notice.Message)
	}
	if got := shell.note.Text(); !strings.Contains(got, "Settings could not be saved") {
		t.Fatalf("note after the restore = %q, want the failed write's warning", got)
	}
}

// holdOpen holds the settings file at path open without sharing it, as a
// sync client or a virus scanner can while the application starts, so
// reading it fails, and returns the function that lets go of it.
func holdOpen(t *testing.T, path string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("hold the settings file open: %v", err)
	}
	return func() { _ = windows.CloseHandle(handle) }
}

// TestRestoreDefaultsKeepsThePresetsOfAFileTheSessionCouldNotRead pins the
// restore as the way out of a session that started from the defaults, its
// saving disabled, because the settings file could not be read: once the
// file can be read, the restore backs it up and keeps the preset libraries
// it stores, with their selected presets, not the defaults the session
// started from, and with them the presets the session created, which could
// not be saved, as a custom preset is deleted only by its Delete; the
// session's modules show them; the shell's note no longer warns that saving
// is disabled; and a restart loads the presets.
func TestRestoreDefaultsKeepsThePresetsOfAFileTheSessionCouldNotRead(t *testing.T) {
	data := readFixture(t, "rsz-01")
	shell := newAuditShellWhile(t, data, holdOpen)
	probeScreenshotRestore(t, shell)
	if got := shell.note.Text(); !strings.Contains(got, "saving is disabled") {
		t.Fatalf("note at startup = %q, want the unread file's warning", got)
	}
	stored := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(stored, data, 0o600); err != nil {
		t.Fatal(err)
	}
	captureWant, resizeWant := libraries(t, reopenSession(t, stored))

	// A preset the user creates in the session, which cannot be saved.
	shell.selectModule(t, core.ModuleImageResizer)
	setWindowText(shell.control(t, "Edit Name"), "Session banner")
	setWindowText(shell.control(t, "Edit Width (px)"), "300")
	setWindowText(shell.control(t, "Edit Height (px)"), "200")
	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleImageResizer].Handle()))
	shell.tabTo(t, "Button Create Preset")
	shell.press(wmKeyDown, vkReturn)
	repository, err := preset.NewRepository(core.ModuleImageResizer, settings.NewStore(settings.Paths{}))
	if err != nil {
		t.Fatal(err)
	}
	sessionLibrary, err := repository.Load(*shell.settings)
	if err != nil {
		t.Fatal(err)
	}
	created := sessionLibrary.Custom()
	if len(created) != 1 || created[0].Name != "Session banner" || created[0].Width != 300 || created[0].Height != 200 {
		t.Fatalf("session presets after Create Preset = %+v, want the one created", created)
	}
	shell.selectModule(t, core.ModuleSettings)

	notice := shell.restoreDefaults()
	backups, err := filepath.Glob(shell.path + ".reset-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v, want one", backups, err)
	}
	if want := (ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(ui.SettingsRestored, backups[0])}); notice != want {
		t.Fatalf("outcome = %+v, want %+v", notice, want)
	}
	if copied, err := os.ReadFile(backups[0]); err != nil || !bytes.Equal(copied, data) {
		t.Fatalf("backup differs from the replaced file: err = %v", err)
	}
	if got := shell.note.Text(); got != "" {
		t.Fatalf("note after the restore = %q, want the startup warning gone", got)
	}
	shell.selectModule(t, core.ModuleImageResizer)
	if got := windowText(shell.control(t, "Edit Name")); got != "Icon" {
		t.Fatalf("Image Resizer preset shown after the restore = %q, want the file's selected preset", got)
	}

	restarted := reopenSession(t, shell.path)
	if len(restarted.warnings()) != 0 {
		t.Fatalf("restart warnings = %v", restarted.warnings())
	}
	captureAfter, resizeAfter := libraries(t, restarted)
	for name, pair := range map[string][2][]string{
		"Screenshot":    {presetSummary(captureWant.Custom(), captureWant.SelectedID()), presetSummary(captureAfter.Custom(), captureAfter.SelectedID())},
		"Image Resizer": {presetSummary(append(resizeWant.Custom(), created...), resizeWant.SelectedID()), presetSummary(resizeAfter.Custom(), resizeAfter.SelectedID())},
	} {
		if strings.Join(pair[0], "\n") != strings.Join(pair[1], "\n") {
			t.Fatalf("%s presets after the restore = %q, want the file's and the session's %q", name, pair[1], pair[0])
		}
	}
}

// TestRestoreDefaultsClearsAFailedSaveWarning pins that a restore whose
// write succeeds clears the shell's warning that an earlier save failed, as
// any successful save does.
func TestRestoreDefaultsClearsAFailedSaveWarning(t *testing.T) {
	shell, path := newExitTestShell(t, &exitRecordingView{})
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := shell.saveSettings(false); err == nil {
		t.Fatal("saving over a folder succeeded")
	}
	if got := shell.note.Text(); !strings.Contains(got, "Settings could not be saved") {
		t.Fatalf("note after the failed save = %q, want the warning", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if notice := shell.restoreDefaults(); notice.Kind != ui.NoticeSuccess {
		t.Fatalf("outcome = %+v, want the restore written", notice)
	}
	if got := shell.note.Text(); got != "" {
		t.Fatalf("note after the restore = %q, want the failed save's warning gone", got)
	}
}

// TestStartupWarningsAboutARecoveredFileFollowItsSaves pins which startup
// warnings about a malformed settings file the shell's note keeps, as the
// application starts on a file it can read but not replace: the warning
// that the defaults taking its place could not be written leaves the note
// once a save succeeds, as they then are, while the warning naming the
// backup, which alone holds the malformed file's settings, stays through
// that save and through Restore Default Settings, which backs up only the
// file the defaults were then written to.
func TestStartupWarningsAboutARecoveredFileFollowItsSaves(t *testing.T) {
	readOnly := func(t *testing.T, path string) func() {
		t.Helper()
		if err := os.Chmod(path, 0o400); err != nil {
			t.Fatal(err)
		}
		return func() { _ = os.Chmod(path, 0o600) }
	}
	shell := newAuditShellWhile(t, []byte(`{"version":1`), readOnly)
	probeScreenshotRestore(t, shell)
	backups, err := filepath.Glob(shell.path + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v, want one", backups, err)
	}
	const unwritten = "Default settings could not be written"
	if got := shell.note.Text(); !strings.Contains(got, backups[0]) || !strings.Contains(got, unwritten) {
		t.Fatalf("note at startup = %q, want the recovery naming %s and the failed write", got, backups[0])
	}

	shell.selectModule(t, core.ModuleImageResizer)
	if got := shell.note.Text(); !strings.Contains(got, backups[0]) || strings.Contains(got, unwritten) {
		t.Fatalf("note once a save succeeded = %q, want only the recovery naming %s", got, backups[0])
	}

	shell.selectModule(t, core.ModuleSettings)
	if notice := shell.restoreDefaults(); notice.Kind != ui.NoticeSuccess {
		t.Fatalf("outcome = %+v, want the restore written", notice)
	}
	if got := shell.note.Text(); !strings.Contains(got, backups[0]) || strings.Contains(got, unwritten) {
		t.Fatalf("note after the restore = %q, want only the recovery naming %s", got, backups[0])
	}
}

// TestRestoreDefaultsWithoutASettingsPath pins the outcome of a restore in a
// session without a settings path: the defaults are in use in memory, and
// the outcome says so without naming a settings file there is not; the
// shell's note keeps the startup warning, which still applies.
func TestRestoreDefaultsWithoutASettingsPath(t *testing.T) {
	shell, _ := newExitTestShell(t, &exitRecordingView{})
	shell.store = settings.NewStore(settings.Paths{})
	startup := fmt.Sprintf(ui.LocalSettingsUnavailable, "no per-user configuration directory")
	shell.startup = startupWarnings{settingsFile: []string{startup}}
	shell.notice = shell.startup.text()
	shell.settings.Shell.Window.Maximized = true

	notice := shell.restoreDefaults()
	if want := (ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.SettingsRestoreInMemory, settings.ErrPathUnavailable)}); notice != want {
		t.Fatalf("outcome = %+v, want %+v", notice, want)
	}
	if !shell.settings.Shell.Window.Maximized {
		t.Fatal("the restore in memory dropped the running window's placement")
	}
	if shell.notice != startup {
		t.Fatalf("note after the restore = %q, want the startup warning %q kept", shell.notice, startup)
	}
}

// TestRestoreDefaultsReportsAFolderWithoutADefaultOnce pins how a restore
// reports an output folder that has no default, as when Windows cannot
// resolve the Pictures folder: the restored options select no folder, and
// the shell's note says so with the reason startup found, as at startup,
// while the outcome does not say it again with another reason. Once the
// user chooses a folder, the next save drops the warning from the note.
func TestRestoreDefaultsReportsAFolderWithoutADefaultOnce(t *testing.T) {
	reason := errors.New("the Pictures folder cannot be found")
	shell := newAuditShellFrom(t, readFixture(t, "rsz-01"), nil, func(dir string) defaultFolders {
		return defaultFolders{screenshot: filepath.Join(dir, "Screenshots"), resizerErr: reason}
	})
	probeScreenshotRestore(t, shell)
	if got := shell.note.Text(); got != "" {
		t.Fatalf("note at startup = %q, want none, as the file stores a resize folder", got)
	}

	notice := shell.restoreDefaults()
	backups, _ := filepath.Glob(shell.path + ".reset-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one", backups)
	}
	if want := (ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(ui.SettingsRestored, backups[0])}); notice != want {
		t.Fatalf("outcome = %+v, want %+v, the folder left to the note", notice, want)
	}
	if got := shell.resize.Options().OutputFolder; got != "" {
		t.Fatalf("resize folder after the restore = %q, want none", got)
	}
	want := ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.ResizeFolderUnavailable, reason)}.Text()
	if got := shell.note.Text(); got != want {
		t.Fatalf("note after the restore = %q, want %q", got, want)
	}

	if err := shell.resize.SetOutputFolder(filepath.Join(filepath.Dir(shell.path), "Chosen")); err != nil {
		t.Fatal(err)
	}
	shell.selectModule(t, core.ModuleScreenshot)
	if got := shell.note.Text(); got != "" {
		t.Fatalf("note once a folder was chosen and the settings saved = %q, want the warning gone", got)
	}
}

// folderChoosingView is a module view whose next command chooses an output
// folder, as Browse does once the picker returns.
type folderChoosingView struct {
	nativeView
	choose func() error
}

func (v folderChoosingView) NativeCommand(int32, uint16) bool {
	if err := v.choose(); err != nil {
		panic(err)
	}
	return true
}

// TestChoosingAFolderEndsItsWarningAtOnce pins that the warning about a stored
// Screenshot folder leaves the shell's note with the command that chooses
// another, so it does not go on saying captures are not saved, and that a
// note showing something else, such as a failed save, stays.
func TestChoosingAFolderEndsItsWarningAtOnce(t *testing.T) {
	stored := bytes.Replace(readFixture(t, "rsz-01"), []byte(`D:\\Captures`), []byte(`D:\\Captures.`), 1)
	// Each case has a subtest so its window is gone before the next opens: the
	// audit shell's cleanup drains the thread's messages, and another open
	// window's pending WM_PAINT would never leave the queue.
	for name, failedSave := range map[string]string{"warning": "", "failed save": "The settings could not be saved."} {
		t.Run(name, func(t *testing.T) {
			shell := newAuditShellOn(t, stored)
			shell.selectModule(t, core.ModuleScreenshot)
			warning := ui.Notice{Kind: ui.NoticeWarning, Message: shell.capture.FolderWarning()}.Text()
			if got := shell.note.Text(); got != warning {
				t.Fatalf("note at startup = %q, want %q", got, warning)
			}
			if failedSave != "" {
				shell.notice = failedSave
				shell.showNote()
			}
			real := shell.nativeViews[core.ModuleScreenshot]
			shell.nativeViews[core.ModuleScreenshot] = folderChoosingView{nativeView: real, choose: func() error {
				return shell.capture.SetOutputFolder(filepath.Join(filepath.Dir(shell.path), "Chosen"))
			}}
			shellWindowProc(shell.hwnd, wmCommand, uintptr(9999), 0)
			shell.nativeViews[core.ModuleScreenshot] = real
			if shell.capture.FolderWarning() != "" {
				t.Fatal("the chosen folder still has a warning")
			}
			want := ""
			if failedSave != "" {
				want = ui.Notice{Kind: ui.NoticeWarning, Message: failedSave}.Text()
			}
			if got := shell.note.Text(); got != want {
				t.Fatalf("note after choosing a folder = %q, want %q", got, want)
			}
		})
	}
}

// presetSummary lists a library's selected preset and its custom presets,
// for comparing two libraries.
func presetSummary(custom []preset.Preset, selected string) []string {
	summary := []string{"selected " + selected}
	for _, candidate := range custom {
		summary = append(summary, fmt.Sprintf("%s %s %dx%d %s", candidate.ID, candidate.Name, candidate.Width, candidate.Height, candidate.ResizeMode))
	}
	return summary
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

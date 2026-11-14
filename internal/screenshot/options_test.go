package screenshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

func testFolder(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestDefaultOptions(t *testing.T) {
	folder := testFolder(t)
	options := DefaultOptions(folder)
	if options.Mode != ModeFreeRectangle {
		t.Fatalf("default mode = %q, want %q", options.Mode, ModeFreeRectangle)
	}
	if options.Format != FormatPNG {
		t.Fatalf("default format = %q, want %q", options.Format, FormatPNG)
	}
	if options.JPEGQuality != DefaultJPEGQuality {
		t.Fatalf("default JPEG quality = %d, want %d", options.JPEGQuality, DefaultJPEGQuality)
	}
	if options.OutputFolder != folder {
		t.Fatalf("default output folder = %q, want %q", options.OutputFolder, folder)
	}
	if options.Shortcut != DefaultShortcut() {
		t.Fatalf("default shortcut = %q, want %q", options.Shortcut, DefaultShortcut())
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("default options are invalid: %v", err)
	}
}

func TestCaptureOptionsValidateBoundaries(t *testing.T) {
	folder := testFolder(t)
	valid := DefaultOptions(folder)
	tests := []struct {
		name    string
		mutate  func(*CaptureOptions)
		wantErr string
	}{
		{name: "preset rectangle mode", mutate: func(o *CaptureOptions) { o.Mode = ModePresetRectangle }},
		{name: "full display mode", mutate: func(o *CaptureOptions) { o.Mode = ModeFullDisplay }},
		{name: "jpeg format", mutate: func(o *CaptureOptions) { o.Format = FormatJPEG }},
		{name: "minimum quality", mutate: func(o *CaptureOptions) { o.JPEGQuality = 1 }},
		{name: "maximum quality", mutate: func(o *CaptureOptions) { o.JPEGQuality = 100 }},
		{name: "unknown mode", mutate: func(o *CaptureOptions) { o.Mode = "window" }, wantErr: "Capture mode"},
		{name: "empty mode", mutate: func(o *CaptureOptions) { o.Mode = "" }, wantErr: "Capture mode"},
		{name: "unknown format", mutate: func(o *CaptureOptions) { o.Format = "gif" }, wantErr: "PNG or JPEG"},
		{name: "zero quality", mutate: func(o *CaptureOptions) { o.JPEGQuality = 0 }, wantErr: "between 1 and 100"},
		{name: "quality above range", mutate: func(o *CaptureOptions) { o.JPEGQuality = 101 }, wantErr: "between 1 and 100"},
		{name: "negative quality", mutate: func(o *CaptureOptions) { o.JPEGQuality = -5 }, wantErr: "between 1 and 100"},
		{name: "empty folder", mutate: func(o *CaptureOptions) { o.OutputFolder = "" }, wantErr: "required"},
		{name: "unset shortcut", mutate: func(o *CaptureOptions) { o.Shortcut = Shortcut{} }, wantErr: "Ctrl+Alt+S"},
		{name: "modifier-less shortcut", mutate: func(o *CaptureOptions) { o.Shortcut = Shortcut{Key: 'S', Name: "S"} }, wantErr: "Ctrl+Alt+S"},
		{name: "relative folder", mutate: func(o *CaptureOptions) { o.OutputFolder = filepath.Join("Screenshots", "out") }, wantErr: "absolute"},
		{name: "folder with null character", mutate: func(o *CaptureOptions) { o.OutputFolder = folder + "\x00" }, wantErr: "not valid text"},
		{name: "folder with replacement character", mutate: func(o *CaptureOptions) { o.OutputFolder = folder + "�" }, wantErr: "not valid text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.mutate(&options)
			err := options.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want text containing %q", err, test.wantErr)
			}
		})
	}
}

func TestImageFormatExtensionAndFilenameExample(t *testing.T) {
	png := DefaultOptions(testFolder(t))
	if got := png.Format.Extension(); got != ".png" {
		t.Fatalf("PNG extension = %q", got)
	}
	if got := png.FilenameExample(); got != "Screenshot_yyyyMMdd_HHmmssfff.png" {
		t.Fatalf("PNG filename example = %q", got)
	}
	jpeg := png
	jpeg.Format = FormatJPEG
	if got := jpeg.Format.Extension(); got != ".jpg" {
		t.Fatalf("JPEG extension = %q", got)
	}
	if got := jpeg.FilenameExample(); got != "Screenshot_yyyyMMdd_HHmmssfff.jpg" {
		t.Fatalf("JPEG filename example = %q", got)
	}
}

func TestParseJPEGQuality(t *testing.T) {
	tests := []struct {
		input string
		want  int
		valid bool
	}{
		{input: "1", want: 1, valid: true},
		{input: "90", want: 90, valid: true},
		{input: "100", want: 100, valid: true},
		{input: " 75 ", want: 75, valid: true},
		{input: "0"},
		{input: "101"},
		{input: "-1"},
		{input: "4.5"},
		{input: "high"},
		{input: ""},
		{input: "99999999999999999999"},
	}
	for _, test := range tests {
		t.Run("input "+test.input, func(t *testing.T) {
			quality, err := ParseJPEGQuality(test.input)
			if test.valid {
				if err != nil || quality != test.want {
					t.Fatalf("ParseJPEGQuality(%q) = %d, %v; want %d", test.input, quality, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "between 1 and 100") {
				t.Fatalf("ParseJPEGQuality(%q) error = %v, want range guidance", test.input, err)
			}
		})
	}
}

func TestOptionsManagerUpdatesAndPersistenceFailure(t *testing.T) {
	folder := testFolder(t)
	manager, _ := newTestOptionsManager(t, filepath.Join(t.TempDir(), "settings.json"), folder)

	if err := manager.SetMode(ModePresetRectangle); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetFormat(FormatJPEG); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetJPEGQuality(70); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetOutputFolder(folder); err != nil {
		t.Fatal(err)
	}
	shortcut, err := ParseShortcut("Ctrl+Shift+F5")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetShortcut(shortcut); err != nil {
		t.Fatal(err)
	}
	options := manager.Options()
	if options.Mode != ModePresetRectangle || options.Format != FormatJPEG || options.JPEGQuality != 70 || options.OutputFolder != folder || options.Shortcut != shortcut {
		t.Fatalf("updated options = %+v", options)
	}

	if err := manager.SetMode("window"); err == nil {
		t.Fatal("SetMode accepted an unknown mode")
	}
	if err := manager.SetJPEGQuality(0); err == nil {
		t.Fatal("SetJPEGQuality accepted zero")
	}
	if err := manager.SetOutputFolder("relative\\Screenshots"); err == nil {
		t.Fatal("SetOutputFolder accepted a relative path")
	}
	if err := manager.SetShortcut(Shortcut{}); err == nil {
		t.Fatal("SetShortcut accepted an empty shortcut")
	}
	if err := manager.SetShortcut(Shortcut{Key: 'S', Name: "S"}); err == nil {
		t.Fatal("SetShortcut accepted a modifier-less shortcut")
	}
	if manager.Options() != options {
		t.Fatalf("rejected updates changed options to %+v", manager.Options())
	}
}

func TestOptionsManagerKeepsMemoryChangeWhenSaveFails(t *testing.T) {
	folder := testFolder(t)
	directory := t.TempDir()
	blocker := filepath.Join(directory, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, _ := newTestOptionsManager(t, filepath.Join(blocker, "settings.json"), folder)

	err := manager.SetFormat(FormatJPEG)
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("SetFormat() error = %v, want ErrPersistence", err)
	}
	if manager.Options().Format != FormatJPEG {
		t.Fatalf("failed save discarded the in-memory format %q", manager.Options().Format)
	}
}

func TestOptionsManagerStartsWithoutDefaultFolder(t *testing.T) {
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	optionsStore, err := NewOptionsStore(store, "")
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	manager, err := NewOptionsManager(optionsStore, &value)
	if err != nil {
		t.Fatalf("NewOptionsManager() with an unavailable default folder: %v", err)
	}
	if manager.Options().OutputFolder != "" {
		t.Fatalf("default folder = %q, want empty", manager.Options().OutputFolder)
	}
	if err := manager.SetMode(ModePresetRectangle); err == nil {
		t.Fatal("SetMode persisted options without an output folder")
	}
	folder := testFolder(t)
	if err := manager.SetOutputFolder(folder); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetMode(ModePresetRectangle); err != nil {
		t.Fatal(err)
	}
}

func newTestOptionsManager(t *testing.T, configPath, folder string) (*OptionsManager, *settings.Store) {
	t.Helper()
	store := settings.NewStore(settings.Paths{ConfigFile: configPath})
	optionsStore, err := NewOptionsStore(store, folder)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	manager, err := NewOptionsManager(optionsStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	return manager, store
}

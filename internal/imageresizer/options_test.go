package imageresizer

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

func testFolder(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestDefaultOptions(t *testing.T) {
	folder := testFolder(t)
	options := DefaultOptions(folder)
	if options.Format != FormatKeepSource {
		t.Fatalf("default format = %q, want %q", options.Format, FormatKeepSource)
	}
	if options.Collision != CollisionRename {
		t.Fatalf("default collision policy = %q, want %q", options.Collision, CollisionRename)
	}
	if options.JPEGQuality != DefaultJPEGQuality {
		t.Fatalf("default JPEG quality = %d, want %d", options.JPEGQuality, DefaultJPEGQuality)
	}
	if options.Background != DefaultBackground {
		t.Fatalf("default background = %q, want %q", options.Background, DefaultBackground)
	}
	if options.OutputFolder != folder {
		t.Fatalf("default output folder = %q, want %q", options.OutputFolder, folder)
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("default options are invalid: %v", err)
	}
}

// TestDefaultFolderIn: the default output folder lies under the Pictures
// folder Windows opens for the Pictures folder's path, and a Pictures folder
// that gives one the output folder rules refuse leaves no default, as one
// that cannot be located does, rather than a default every job would refuse.
func TestDefaultFolderIn(t *testing.T) {
	pictures := t.TempDir()
	want := filepath.Join(pictures, "GameDevTools", "Resized")
	spellings := []string{pictures}
	refused := map[string]error{"Pictures": output.ErrFolderNotAbsolute}
	if runtime.GOOS == "windows" {
		// Windows drops a trailing period or space from the Pictures
		// folder's own name, and the output folder lies in the folder it
		// opens, rather than in one with the name only a longer path keeps.
		spellings = append(spellings, pictures+".", pictures+" ", pictures+". ")
		// A colon names a stream however the path is resolved, and a device
		// path that leads to no drive, volume, or share holds no folder.
		refused[pictures+`\x:y`] = output.ErrFolderName
		refused[`\\.\pipe\Pictures`] = output.ErrFolderRoot
		// Go 1.27's filepath.Join would read this path as relative and
		// report the default as unavailable.
		const share = `\\server\..\me\Pictures`
		if folder, err := defaultFolderIn(share); err != nil || folder != share+`\GameDevTools\Resized` {
			t.Errorf("defaultFolderIn(%q) = %q, %v", share, folder, err)
		}
	}
	for _, spelling := range spellings {
		if folder, err := defaultFolderIn(spelling); err != nil || folder != want {
			t.Errorf("defaultFolderIn(%q) = %q, %v, want %q", spelling, folder, err, want)
		}
	}
	for pictures, want := range refused {
		folder, err := defaultFolderIn(pictures)
		if folder != "" || !errors.Is(err, want) || !strings.Contains(err.Error(), pictures) {
			t.Errorf("defaultFolderIn(%q) = %q, %v, want an error naming the folder with %v", pictures, folder, err, want)
		}
	}
}

// TestDefaultFolder: the default output folder lies under the known Pictures
// folder, or under the profile's when the known one cannot be located or
// gives a folder the rules refuse, and when neither gives one the error says
// why, preferring the known folder's refusal.
func TestDefaultFolder(t *testing.T) {
	known, profile := t.TempDir(), t.TempDir()
	under := func(pictures string) string { return filepath.Join(pictures, "GameDevTools", "Resized") }
	home := func() (string, error) { return profile, nil }
	relativeHome := func() (string, error) { return "profile", nil }
	lookupErr, homeErr := errors.New("element not found"), errors.New("no profile")
	noHome := func() (string, error) { return "", homeErr }
	knownRefusal, profileRefusal := under("Pictures"), under(filepath.Join("profile", "Pictures"))
	tests := []struct {
		name     string
		known    string
		knownErr error
		home     func() (string, error)
		want     string
		wantErr  error
		// refusal is the refused folder the error names.
		refusal string
	}{
		{name: "known folder", known: known, home: home, want: under(known)},
		{name: "known folder not located", knownErr: lookupErr, home: home, want: under(filepath.Join(profile, "Pictures"))},
		{name: "known folder empty", home: home, want: under(filepath.Join(profile, "Pictures"))},
		{name: "known folder refused", known: "Pictures", home: home, want: under(filepath.Join(profile, "Pictures"))},
		{name: "known folder refused without a profile", known: "Pictures", home: noHome, wantErr: output.ErrFolderNotAbsolute, refusal: knownRefusal},
		{name: "both refused", known: "Pictures", home: relativeHome, wantErr: output.ErrFolderNotAbsolute, refusal: knownRefusal},
		{name: "profile refused", knownErr: lookupErr, home: relativeHome, wantErr: output.ErrFolderNotAbsolute, refusal: profileRefusal},
		{name: "nothing located", knownErr: lookupErr, home: noHome, wantErr: lookupErr},
		{name: "no profile", home: noHome, wantErr: homeErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			folder, err := defaultFolder(test.known, test.knownErr, test.home)
			if folder != test.want || !errors.Is(err, test.wantErr) || (err == nil) != (test.wantErr == nil) {
				t.Fatalf("defaultFolder = %q, %v, want %q, %v", folder, err, test.want, test.wantErr)
			}
			if test.refusal != "" && !strings.HasPrefix(err.Error(), test.refusal+":") {
				t.Fatalf("error %v does not name the refused folder %s", err, test.refusal)
			}
		})
	}
}

func TestResizeOptionsValidateBoundaries(t *testing.T) {
	folder := testFolder(t)
	valid := DefaultOptions(folder)
	// A path that names only a volume says so, rather than that it is not
	// absolute; outside Windows paths have no volume name, and \\?\ is a
	// relative name.
	volumeErr := "rather than a folder"
	// Windows resolves a path before it opens or creates anything, which may
	// change a name in it; elsewhere every name is kept as written.
	nameErr := "Windows would change a name"
	if filepath.VolumeName(folder) == "" {
		volumeErr = "absolute"
		nameErr = ""
	}
	tests := []struct {
		name    string
		mutate  func(*ResizeOptions)
		wantErr string
	}{
		{name: "png format", mutate: func(o *ResizeOptions) { o.Format = FormatPNG }},
		{name: "jpeg format", mutate: func(o *ResizeOptions) { o.Format = FormatJPEG }},
		{name: "skip policy", mutate: func(o *ResizeOptions) { o.Collision = CollisionSkip }},
		{name: "overwrite policy", mutate: func(o *ResizeOptions) { o.Collision = CollisionOverwrite }},
		{name: "minimum quality", mutate: func(o *ResizeOptions) { o.JPEGQuality = 1 }},
		{name: "maximum quality", mutate: func(o *ResizeOptions) { o.JPEGQuality = 100 }},
		{name: "black background", mutate: func(o *ResizeOptions) { o.Background = "#000000" }},
		{name: "unknown format", mutate: func(o *ResizeOptions) { o.Format = "gif" }, wantErr: "Keep Source Format, PNG, or JPEG"},
		{name: "empty format", mutate: func(o *ResizeOptions) { o.Format = "" }, wantErr: "Keep Source Format, PNG, or JPEG"},
		{name: "unknown collision policy", mutate: func(o *ResizeOptions) { o.Collision = "delete" }, wantErr: "Rename, Skip, or Overwrite"},
		{name: "empty collision policy", mutate: func(o *ResizeOptions) { o.Collision = "" }, wantErr: "Rename, Skip, or Overwrite"},
		{name: "zero quality", mutate: func(o *ResizeOptions) { o.JPEGQuality = 0 }, wantErr: "between 1 and 100"},
		{name: "quality above range", mutate: func(o *ResizeOptions) { o.JPEGQuality = 101 }, wantErr: "between 1 and 100"},
		{name: "negative quality", mutate: func(o *ResizeOptions) { o.JPEGQuality = -5 }, wantErr: "between 1 and 100"},
		{name: "background without hash", mutate: func(o *ResizeOptions) { o.Background = "FFFFFF" }, wantErr: "# followed by six hexadecimal digits"},
		{name: "short background", mutate: func(o *ResizeOptions) { o.Background = "#FFF" }, wantErr: "# followed by six hexadecimal digits"},
		{name: "non-hex background", mutate: func(o *ResizeOptions) { o.Background = "#GGGGGG" }, wantErr: "# followed by six hexadecimal digits"},
		{name: "empty background", mutate: func(o *ResizeOptions) { o.Background = "" }, wantErr: "# followed by six hexadecimal digits"},
		{name: "empty folder", mutate: func(o *ResizeOptions) { o.OutputFolder = "" }, wantErr: "required"},
		{name: "relative folder", mutate: func(o *ResizeOptions) { o.OutputFolder = filepath.Join("Resized", "out") }, wantErr: "absolute"},
		{name: "folder with null character", mutate: func(o *ResizeOptions) { o.OutputFolder = folder + "\x00" }, wantErr: "not valid text"},
		{name: "folder with replacement character", mutate: func(o *ResizeOptions) { o.OutputFolder = folder + "�" }, wantErr: "not valid text"},
		{name: "folder ending in a space", mutate: func(o *ResizeOptions) { o.OutputFolder = folder + " " }, wantErr: nameErr},
		{name: "folder ending in a period", mutate: func(o *ResizeOptions) { o.OutputFolder = folder + "." }, wantErr: nameErr},
		{name: "parent ending in a period", mutate: func(o *ResizeOptions) { o.OutputFolder = filepath.Join(folder+".", "Resized") }, wantErr: nameErr},
		{name: "folder named as a device", mutate: func(o *ResizeOptions) { o.OutputFolder = filepath.Join(folder, "NUL") }, wantErr: nameErr},
		{name: "folder named as a stream", mutate: func(o *ResizeOptions) { o.OutputFolder = folder + "::$INDEX_ALLOCATION" }, wantErr: nameErr},
		// Windows keeps a trailing space, or two periods, on a name a path
		// leads through.
		{name: "parent ending in a space", mutate: func(o *ResizeOptions) { o.OutputFolder = filepath.Join(folder+" ", "Resized") }},
		{name: "parent ending in two periods", mutate: func(o *ResizeOptions) { o.OutputFolder = filepath.Join(folder+"..", "Resized") }},
		{name: "volume without a folder", mutate: func(o *ResizeOptions) { o.OutputFolder = `\\?\` + filepath.VolumeName(folder) }, wantErr: volumeErr},
		{name: "folder on a named pipe", mutate: func(o *ResizeOptions) { o.OutputFolder = `\\.\pipe\Resized` }, wantErr: volumeErr},
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

func TestParseBackground(t *testing.T) {
	tests := []struct {
		input string
		want  string
		valid bool
	}{
		{input: "#FFFFFF", want: "#FFFFFF", valid: true},
		{input: "#000000", want: "#000000", valid: true},
		{input: "#ff8000", want: "#FF8000", valid: true},
		{input: " #1a2B3c ", want: "#1A2B3C", valid: true},
		{input: "FFFFFF"},
		{input: "#FFF"},
		{input: "#FFFFFFF"},
		{input: "#12345G"},
		{input: "white"},
		{input: ""},
		{input: "#"},
	}
	for _, test := range tests {
		t.Run("input "+test.input, func(t *testing.T) {
			background, err := ParseBackground(test.input)
			if test.valid {
				if err != nil || background != test.want {
					t.Fatalf("ParseBackground(%q) = %q, %v; want %q", test.input, background, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "six hexadecimal digits") {
				t.Fatalf("ParseBackground(%q) error = %v, want format guidance", test.input, err)
			}
		})
	}
}

func TestOptionsManagerUpdatesAndPersistenceFailure(t *testing.T) {
	folder := testFolder(t)
	manager, _ := newTestOptionsManager(t, filepath.Join(t.TempDir(), "settings.json"), folder)

	if err := manager.SetFormat(FormatJPEG); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetCollision(CollisionOverwrite); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetJPEGQuality(70); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetBackground("#102030"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetOutputFolder(folder); err != nil {
		t.Fatal(err)
	}
	options := manager.Options()
	if options.Format != FormatJPEG || options.Collision != CollisionOverwrite || options.JPEGQuality != 70 || options.Background != "#102030" || options.OutputFolder != folder {
		t.Fatalf("updated options = %+v", options)
	}

	if err := manager.SetFormat("gif"); err == nil {
		t.Fatal("SetFormat accepted an unknown format")
	}
	if err := manager.SetCollision("delete"); err == nil {
		t.Fatal("SetCollision accepted an unknown policy")
	}
	if err := manager.SetJPEGQuality(0); err == nil {
		t.Fatal("SetJPEGQuality accepted zero")
	}
	if err := manager.SetBackground("white"); err == nil {
		t.Fatal("SetBackground accepted a color name")
	}
	if err := manager.SetOutputFolder("relative\\Resized"); err == nil {
		t.Fatal("SetOutputFolder accepted a relative path")
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

	err := manager.SetCollision(CollisionSkip)
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("SetCollision() error = %v, want ErrPersistence", err)
	}
	if manager.Options().Collision != CollisionSkip {
		t.Fatalf("failed save discarded the in-memory policy %q", manager.Options().Collision)
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
	if err := manager.SetFormat(FormatJPEG); err == nil {
		t.Fatal("SetFormat persisted options without an output folder")
	}
	folder := testFolder(t)
	if err := manager.SetOutputFolder(folder); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetFormat(FormatJPEG); err != nil {
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

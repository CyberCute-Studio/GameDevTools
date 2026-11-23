package release

import (
	"archive/zip"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

func repositoryFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCommittedResourcesAreCurrent fails when the icon or the version changed
// without running go generate ./cmd/gamedevtools.
func TestCommittedResourcesAreCurrent(t *testing.T) {
	icon := repositoryFile(t, "assets", "icon.ico")
	for _, architecture := range []string{"amd64", "arm64"} {
		want, err := BuildResources(icon, architecture)
		if err != nil {
			t.Fatal(err)
		}
		got := repositoryFile(t, "cmd", "gamedevtools", "resource_windows_"+architecture+".syso")
		if !bytes.Equal(got, want) {
			t.Errorf("resource_windows_%s.syso is stale; run go generate ./cmd/gamedevtools", architecture)
		}
	}
}

func TestBuildResourcesIsACOFFObjectForEachArchitecture(t *testing.T) {
	icon := repositoryFile(t, "assets", "icon.ico")
	for architecture, machine := range map[string]uint16{"amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64} {
		object, err := BuildResources(icon, architecture)
		if err != nil {
			t.Fatal(err)
		}
		file, err := pe.NewFile(bytes.NewReader(object))
		if err != nil {
			t.Fatalf("%s: %v", architecture, err)
		}
		defer file.Close()
		if file.Machine != machine {
			t.Errorf("%s: machine = %#x, want %#x", architecture, file.Machine, machine)
		}
		section := file.Section(".rsrc")
		if section == nil {
			t.Fatalf("%s: no .rsrc section", architecture)
		}
		// One relocation per resource: each icon image, the group, the version.
		images := int(binary.LittleEndian.Uint16(icon[4:]))
		if int(section.NumberOfRelocations) != images+2 {
			t.Errorf("%s: %d relocations, want %d", architecture, section.NumberOfRelocations, images+2)
		}
	}
}

func TestBuildResourcesRefusesBadInput(t *testing.T) {
	icon := repositoryFile(t, "assets", "icon.ico")
	if _, err := BuildResources(icon, "386"); err == nil {
		t.Error("an unsupported architecture was accepted")
	}
	outside := append([]byte(nil), icon...)
	binary.LittleEndian.PutUint32(outside[6+12:], uint32(len(icon)))
	for name, data := range map[string][]byte{
		"empty":           nil,
		"not an icon":     []byte("PNG image data"),
		"no images":       {0, 0, 1, 0, 0, 0},
		"short directory": {0, 0, 1, 0, 2, 0, 1, 2, 3},
		"image past end":  outside,
	} {
		if _, err := BuildResources(data, "amd64"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVersionIsMajorMinorPatch(t *testing.T) {
	if _, err := versionNumbers(Version); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "1.0", "1.0.0.0", "1.x.0", "70000.0.0"} {
		if _, err := versionNumbers(bad); err == nil {
			t.Errorf("versionNumbers(%q) was accepted", bad)
		}
	}
}

func TestBuildArchiveIsDeterministicAndFoldered(t *testing.T) {
	files := map[string][]byte{"b.txt": []byte("second"), "a.exe": bytes.Repeat([]byte("first"), 1000)}
	first, err := BuildArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildArchive(map[string][]byte{"a.exe": files["a.exe"], "b.txt": files["b.txt"]})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same files gave different archives")
	}
	reader, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 || reader.File[0].Name != ui.AppName+"/a.exe" || reader.File[1].Name != ui.AppName+"/b.txt" {
		t.Fatalf("entries = %v", reader.File)
	}
	for _, entry := range reader.File {
		if !entry.Modified.Equal(Date) {
			t.Errorf("%s is stamped %v, want %v", entry.Name, entry.Modified, Date)
		}
	}
}

func TestChecksumIsSha256sumFormat(t *testing.T) {
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad  a.zip\n"
	if got := Checksum([]byte("abc"), "a.zip"); got != want {
		t.Fatalf("Checksum = %q, want %q", got, want)
	}
}

// TestNoticesCarryTheLinkedLicenses holds the BSD-3-Clause condition that a
// binary distribution reproduces the notice of what it links.
func TestNoticesCarryTheLinkedLicenses(t *testing.T) {
	notices := strings.ReplaceAll(string(repositoryFile(t, "packaging", "THIRD-PARTY-NOTICES.txt")), "\r\n", "\n")
	license, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "LICENSE"))
	if err != nil {
		t.Skipf("the Go license is not available: %v", err)
	}
	if !strings.Contains(notices, strings.ReplaceAll(string(license), "\r\n", "\n")) {
		t.Error("THIRD-PARTY-NOTICES.txt does not hold the Go license text")
	}
	for _, name := range []string{"golang.org/x/sys", "Go standard library"} {
		if !strings.Contains(notices, name) {
			t.Errorf("THIRD-PARTY-NOTICES.txt does not name %s", name)
		}
	}
}

// TestLicenseShipsInThePackage holds the repository's MIT license and the copy
// in the ZIP to one text, under the copyright line the executable carries.
func TestLicenseShipsInThePackage(t *testing.T) {
	license := repositoryFile(t, "LICENSE")
	if !bytes.Equal(license, repositoryFile(t, "packaging", "LICENSE.txt")) {
		t.Error("LICENSE and packaging/LICENSE.txt differ")
	}
	for _, want := range []string{"MIT License", Copyright, "Permission is hereby granted, free of charge"} {
		if !bytes.Contains(license, []byte(want)) {
			t.Errorf("LICENSE does not state %q", want)
		}
	}
	if !slices.Contains(PackagedDocuments, "LICENSE.txt") {
		t.Error("the package does not carry LICENSE.txt")
	}
}

// TestNoticesNameTheLinkedVersions fails when go.mod moves the toolchain or
// x/sys and the notices still state the old version.
func TestNoticesNameTheLinkedVersions(t *testing.T) {
	notices := strings.Join(strings.Fields(string(repositoryFile(t, "packaging", "THIRD-PARTY-NOTICES.txt"))), " ")
	goMod := string(repositoryFile(t, "go.mod"))
	for name, test := range map[string]struct {
		pattern string
		format  string
	}{
		"toolchain": {`(?m)^toolchain (go\S+)\s*$`, "runtime (%s)"},
		"x/sys":     {`(?m)golang\.org/x/sys (v\S+)`, "golang.org/x/sys (%s)"},
	} {
		match := regexp.MustCompile(test.pattern).FindStringSubmatch(goMod)
		if match == nil {
			t.Fatalf("go.mod names no %s version", name)
		}
		if want := fmt.Sprintf(test.format, match[1]); !strings.Contains(notices, want) {
			t.Errorf("THIRD-PARTY-NOTICES.txt does not state %q", want)
		}
	}
}

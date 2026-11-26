package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/release"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// fakeExecutable is the smallest PE image debug/pe reads: a DOS header that
// points to the PE signature and a COFF header naming the machine type.
func fakeExecutable(machine uint16) []byte {
	image := make([]byte, 128)
	image[0], image[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(image[0x3c:], 0x40)
	copy(image[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(image[0x44:], machine)
	return image
}

// releaseFiles are the files a release ZIP of the machine type holds, by name.
func releaseFiles(machine uint16, marker string) map[string][]byte {
	files := map[string][]byte{release.ExecutableName(): append(fakeExecutable(machine), marker...)}
	for _, name := range release.PackagedDocuments {
		files[name] = []byte(name + " of " + marker)
	}
	return files
}

func buildArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	archive, err := release.BuildArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

// githubServer serves a release list and the assets of each release under
// /download/<tag>/<name>, as GitHub does, and records what it was sent.
type githubServer struct {
	*httptest.Server
	releases []githubRelease
	assets   map[string][]byte
	requests []*http.Request
	// handle, when set, answers a download in place of the assets.
	handle func(w http.ResponseWriter, r *http.Request) bool
}

func newGitHubServer(t *testing.T) *githubServer {
	t.Helper()
	server := &githubServer{assets: map[string][]byte{}}
	server.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.requests = append(server.requests, r)
		if r.URL.Path == "/releases" {
			json.NewEncoder(w).Encode(server.releases)
			return
		}
		if server.handle != nil && server.handle(w, r) {
			return
		}
		data, ok := server.assets[strings.TrimPrefix(r.URL.Path, "/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *githubServer) client() *Client {
	httpClient := s.Server.Client()
	httpClient.CheckRedirect = httpsOnly
	return &Client{HTTP: httpClient, ReleasesURL: s.URL + "/releases", DownloadURL: s.URL + "/download/", UserAgent: "cybercute-dev-tools/1.0.0"}
}

// publish adds a release of the version with its ZIP and checksum file for
// the architecture, and returns the release Latest should report.
func (s *githubServer) publish(t *testing.T, version, architecture string, archive []byte) Release {
	t.Helper()
	tag := "v" + version
	name := release.ArchiveName(version, architecture)
	checksum := release.Checksum(archive, name)
	s.assets[tag+"/"+name] = archive
	s.assets[tag+"/"+name+".sha256"] = []byte(checksum)
	s.releases = append(s.releases, githubRelease{TagName: tag, Assets: []githubAsset{
		{Name: name, State: "uploaded", Size: int64(len(archive))},
		{Name: name + ".sha256", State: "uploaded", Size: int64(len(checksum))},
	}})
	parsed, _ := ParseVersion(version)
	return Release{Version: parsed, Tag: tag, Archive: Asset{Name: name, Size: int64(len(archive))}, Checksum: Asset{Name: name + ".sha256", Size: int64(len(checksum))}}
}

func version(t *testing.T, text string) Version {
	t.Helper()
	parsed, ok := ParseVersion(text)
	if !ok {
		t.Fatalf("%q is not a version", text)
	}
	return parsed
}

func TestParseVersionReadsMajorMinorPatch(t *testing.T) {
	for text, want := range map[string]Version{"1.0.0": {1, 0, 0}, "1.10.2": {1, 10, 2}, "0.0.1": {0, 0, 1}, "65535.0.0": {65535, 0, 0}} {
		if got, ok := ParseVersion(text); !ok || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v, want %v", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "1", "1.0", "1.0.0.0", "1.0.x", "1..0", "-1.0.0", "+1.0.0", "1.0.0-rc1", " 1.0.0", "65536.0.0", "v1.0.0"} {
		if got, ok := ParseVersion(text); ok {
			t.Errorf("ParseVersion(%q) = %v, want no version", text, got)
		}
	}
	if !version(t, "1.9.0").Less(version(t, "1.10.0")) || version(t, "1.10.0").Less(version(t, "1.9.0")) || version(t, "1.0.0").Less(version(t, "1.0.0")) {
		t.Error("Less does not order versions by their numbers")
	}
	if current, ok := Current(); !ok || current.String() != release.Version {
		t.Errorf("Current() = %v, %v, want %s", current, ok, release.Version)
	}
}

func TestLatestPicksTheNewestReleaseForThisArchitecture(t *testing.T) {
	server := newGitHubServer(t)
	archive := buildArchive(t, releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "new"))
	server.publish(t, "1.1.0", "arm64", archive)
	want := server.publish(t, "1.3.0", "arm64", archive)
	server.publish(t, "1.2.0", "arm64", archive)
	// Newer, but each lacks what an update needs.
	server.publish(t, "1.4.0", "x64", archive)
	draft := server.publish(t, "1.5.0", "arm64", archive)
	server.releases[len(server.releases)-1].Draft = true
	server.publish(t, "1.6.0", "arm64", archive)
	server.releases[len(server.releases)-1].Assets = server.releases[len(server.releases)-1].Assets[:1]
	server.publish(t, "1.7.0", "arm64", archive)
	server.releases[len(server.releases)-1].Assets[0].State = "starter"
	server.releases = append(server.releases, githubRelease{TagName: "nightly"}, githubRelease{TagName: "v2.0.0-rc1"})

	found, ok, err := server.client().Latest(context.Background(), version(t, "1.0.0"), pe.IMAGE_FILE_MACHINE_ARM64)
	if err != nil || !ok {
		t.Fatalf("Latest = %v, %v, %v", found, ok, err)
	}
	if found != want {
		t.Errorf("Latest = %+v, want %+v (not the draft %s)", found, want, draft.Tag)
	}
	request := server.requests[0]
	if got := request.Header.Get("User-Agent"); got != "cybercute-dev-tools/1.0.0" {
		t.Errorf("User-Agent = %q", got)
	}
	if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("X-GitHub-Api-Version") == "" {
		t.Errorf("the release list was asked for with %v", request.Header)
	}
	if len(server.requests) != 1 {
		t.Errorf("the check made %d requests, want 1", len(server.requests))
	}
}

func TestLatestFindsNoUpdateAtOrAfterTheNewestRelease(t *testing.T) {
	server := newGitHubServer(t)
	archive := buildArchive(t, releaseFiles(pe.IMAGE_FILE_MACHINE_AMD64, "x"))
	server.publish(t, "1.0.0", "x64", archive)
	server.publish(t, "1.2.0", "x64", archive)
	for _, current := range []string{"1.2.0", "1.3.0"} {
		if found, ok, err := server.client().Latest(context.Background(), version(t, current), pe.IMAGE_FILE_MACHINE_AMD64); err != nil || ok {
			t.Errorf("Latest from %s = %v, %v, %v, want no update", current, found, ok, err)
		}
	}
	if found, ok, err := server.client().Latest(context.Background(), version(t, "1.1.0"), pe.IMAGE_FILE_MACHINE_ARM64); err != nil || ok {
		t.Errorf("Latest for arm64 with only x64 ZIPs = %v, %v, %v, want no update", found, ok, err)
	}
}

func TestLatestFailsWhenGitHubCannotAnswer(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"rate limited": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "rate limited", http.StatusForbidden) },
		"not a list":   func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"message":"Not Found"}`)) },
		"too long": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("["))
			w.Write(bytes.Repeat([]byte(" "), maxListBytes))
			w.Write([]byte("]"))
		},
	} {
		server := httptest.NewTLSServer(handler)
		client := &Client{HTTP: server.Client(), ReleasesURL: server.URL, UserAgent: "test"}
		if _, ok, err := client.Latest(context.Background(), version(t, "1.0.0"), pe.IMAGE_FILE_MACHINE_ARM64); err == nil || ok {
			t.Errorf("%s: Latest = %v, %v, want an error", name, ok, err)
		}
		server.Close()
	}
}

// TestLatestGivesUpPromptlyWithoutANetwork pins that the check the start
// waits for ends at once when the address cannot be reached, as when the
// computer is offline, and within CheckTimeout when it does not answer.
func TestLatestGivesUpPromptlyWithoutANetwork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	for name, address := range map[string]string{"refused": closed, "silent": silent.Addr().String()} {
		client := NewClient()
		client.ReleasesURL = "https://" + address + "/releases"
		ctx, cancel := context.WithTimeout(context.Background(), CheckTimeout)
		started := time.Now()
		_, ok, err := client.Latest(ctx, version(t, "1.0.0"), pe.IMAGE_FILE_MACHINE_ARM64)
		cancel()
		if err == nil || ok {
			t.Errorf("%s: Latest = %v, %v, want an error", name, ok, err)
		}
		if elapsed := time.Since(started); elapsed > CheckTimeout+time.Second {
			t.Errorf("%s: the check took %v, longer than %v", name, elapsed, CheckTimeout)
		}
	}
}

func TestNewClientAsksGitHubForThisRepository(t *testing.T) {
	client := NewClient()
	if client.ReleasesURL != "https://api.github.com/repos/CyberCute-Studio/GameDevTools/releases?per_page=30" {
		t.Errorf("ReleasesURL = %q", client.ReleasesURL)
	}
	found := Release{Tag: "v1.2.3", Archive: Asset{Name: release.ArchiveName("1.2.3", "arm64")}}
	if got := client.assetURL(found, found.Archive); got != "https://github.com/CyberCute-Studio/GameDevTools/releases/download/v1.2.3/cybercute-dev-tools-1.2.3-windows-arm64.zip" {
		t.Errorf("assetURL = %q", got)
	}
	if client.UserAgent != ui.AppFileName+"/"+release.Version {
		t.Errorf("UserAgent = %q", client.UserAgent)
	}
	if client.HTTP.CheckRedirect == nil {
		t.Error("the client follows redirects to any address")
	}
}

func TestDownloadChecksTheZIPAgainstItsChecksum(t *testing.T) {
	server := newGitHubServer(t)
	archive := buildArchive(t, releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "new"))
	found := server.publish(t, "1.1.0", "arm64", archive)
	folder := t.TempDir()
	var reports [][2]int64
	path, err := server.client().Download(context.Background(), found, folder, func(received, total int64) {
		reports = append(reports, [2]int64{received, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(folder, found.Archive.Name) {
		t.Errorf("Download wrote %s", path)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, archive) {
		t.Errorf("the downloaded ZIP differs: %v", err)
	}
	if len(reports) == 0 || reports[len(reports)-1] != [2]int64{int64(len(archive)), int64(len(archive))} {
		t.Errorf("progress reports = %v, want the last to be the whole ZIP", reports)
	}
}

func TestDownloadRefusesAZIPThatFailsItsCheck(t *testing.T) {
	archive := buildArchive(t, releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "new"))
	for name, tamper := range map[string]func(s *githubServer, found *Release){
		"changed": func(s *githubServer, found *Release) {
			changed := bytes.Clone(archive)
			changed[len(changed)/2] ^= 0xff
			s.assets[found.Tag+"/"+found.Archive.Name] = changed
		},
		"checksum of another file": func(s *githubServer, found *Release) {
			s.assets[found.Tag+"/"+found.Checksum.Name] = []byte(release.Checksum(archive, "other.zip"))
		},
		"checksum not a checksum": func(s *githubServer, found *Release) {
			s.assets[found.Tag+"/"+found.Checksum.Name] = []byte("not a checksum\n")
		},
		"shorter than listed": func(s *githubServer, found *Release) { found.Archive.Size++ },
		"longer than listed":  func(s *githubServer, found *Release) { found.Archive.Size-- },
		"larger than allowed": func(s *githubServer, found *Release) { found.Archive.Size = maxArchiveBytes + 1 },
		"missing":             func(s *githubServer, found *Release) { delete(s.assets, found.Tag+"/"+found.Archive.Name) },
	} {
		server := newGitHubServer(t)
		found := server.publish(t, "1.1.0", "arm64", archive)
		tamper(server, &found)
		folder := t.TempDir()
		if path, err := server.client().Download(context.Background(), found, folder, nil); err == nil {
			t.Errorf("%s: Download = %s, want an error", name, path)
		}
		if entries, _ := os.ReadDir(folder); len(entries) != 0 {
			t.Errorf("%s: a refused download left %v", name, entries)
		}
	}
}

func TestDownloadFollowsRedirectsToHTTPSOnly(t *testing.T) {
	archive := buildArchive(t, releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "new"))
	storage := newGitHubServer(t)
	for target, wantErr := range map[string]bool{"https": false, "http": true} {
		server := newGitHubServer(t)
		found := server.publish(t, "1.1.0", "arm64", archive)
		storage.assets["stored"] = archive
		location := storage.URL + "/download/stored"
		if target == "http" {
			location = "http" + strings.TrimPrefix(location, "https")
		}
		server.handle = func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, ".zip") {
				return false
			}
			http.Redirect(w, r, location, http.StatusFound)
			return true
		}
		client := server.client()
		// Both test servers share one certificate authority.
		client.HTTP = storage.Server.Client()
		client.HTTP.CheckRedirect = httpsOnly
		_, err := client.Download(context.Background(), found, t.TempDir(), nil)
		if (err != nil) != wantErr {
			t.Errorf("a redirect to %s gave %v", target, err)
		}
	}
}

func TestDownloadStopsWhenCancelled(t *testing.T) {
	archive := buildArchive(t, releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "new"))
	server := newGitHubServer(t)
	found := server.publish(t, "1.1.0", "arm64", archive)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	server.handle = func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, ".zip") {
			return false
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
		w.Write(archive[:10])
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-hold:
		}
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	folder := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := server.client().Download(ctx, found, folder, func(received, total int64) { cancel() })
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled download gave %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled download did not stop")
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Errorf("a cancelled download left %v", entries)
	}
}

// stagedRelease writes the ZIP of files and returns its path.
func stagedRelease(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update.zip")
	if err := os.WriteFile(path, buildArchive(t, files), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStageExtractsTheReleaseFiles(t *testing.T) {
	files := releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "new")
	staging := t.TempDir()
	names, err := Stage(stagedRelease(t, files), staging, pe.IMAGE_FILE_MACHINE_ARM64)
	if err != nil {
		t.Fatal(err)
	}
	if names[0] != release.ExecutableName() || len(names) != len(files) {
		t.Errorf("Stage = %v, want the executable first and every file", names)
	}
	for name, content := range files {
		if data, err := os.ReadFile(filepath.Join(staging, stagedFiles, name)); err != nil || !bytes.Equal(data, content) {
			t.Errorf("%s was not staged as it is in the ZIP: %v", name, err)
		}
	}
}

// rawArchive builds a ZIP of entries as given, folder and all.
func rawArchive(t *testing.T, entries []zip.FileHeader, content []byte) string {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, header := range entries {
		entry, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(header.Name, "/") {
			entry.Write(content)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "update.zip")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStageRefusesWhatAnUpdateDoesNotInstall(t *testing.T) {
	arm := uint16(pe.IMAGE_FILE_MACHINE_ARM64)
	folder := ui.AppName + "/"
	executable := zip.FileHeader{Name: folder + release.ExecutableName()}
	with := func(extra ...zip.FileHeader) []zip.FileHeader { return append([]zip.FileHeader{executable}, extra...) }
	symlink := zip.FileHeader{Name: folder + "link.txt"}
	symlink.SetMode(fs.ModeSymlink | 0o777)
	many := with()
	for i := range maxEntries {
		many = append(many, zip.FileHeader{Name: fmt.Sprintf("%sfile%d.txt", folder, i)})
	}
	for name, entries := range map[string][]zip.FileHeader{
		"outside the folder":      with(zip.FileHeader{Name: "README.txt"}),
		"another folder":          with(zip.FileHeader{Name: "Other/README.txt"}),
		"a subfolder":             with(zip.FileHeader{Name: folder + "docs/README.txt"}),
		"a parent reference":      with(zip.FileHeader{Name: folder + "../evil.exe"}),
		"a backslash":             with(zip.FileHeader{Name: folder + `..\evil.exe`}),
		"a drive":                 with(zip.FileHeader{Name: folder + "C:evil.exe"}),
		"a stream":                with(zip.FileHeader{Name: folder + "README.txt:hidden"}),
		"a device name":           with(zip.FileHeader{Name: folder + "NUL.txt"}),
		"a trailing dot":          with(zip.FileHeader{Name: folder + "README.txt."}),
		"a leading dot":           with(zip.FileHeader{Name: folder + ".cybercute-update-x"}),
		"a space":                 with(zip.FileHeader{Name: folder + "READ ME.txt"}),
		"one name twice":          with(zip.FileHeader{Name: folder + "README.txt"}, zip.FileHeader{Name: folder + "readme.TXT"}),
		"a symbolic link":         with(symlink),
		"no executable":           {{Name: folder + "README.txt"}},
		"the executable renamed":  {{Name: folder + "Cybercute-Dev-Tools.exe"}},
		"more files than allowed": many,
		"an empty folder entry":   with(zip.FileHeader{Name: folder + "empty/"}),
	} {
		staging := t.TempDir()
		if names, err := Stage(rawArchive(t, entries, fakeExecutable(arm)), staging, arm); err == nil {
			t.Errorf("%s: Stage = %v, want an error", name, names)
		}
		if entries, _ := os.ReadDir(staging); len(entries) != 0 {
			t.Errorf("%s: a refused update staged %v", name, entries)
		}
	}
	// The folder's own entry, which other ZIP tools add, is allowed.
	if _, err := Stage(rawArchive(t, with(zip.FileHeader{Name: folder}), fakeExecutable(arm)), t.TempDir(), arm); err != nil {
		t.Errorf("a ZIP with its folder's entry was refused: %v", err)
	}
}

func TestStageRefusesAnExecutableForAnotherMachine(t *testing.T) {
	files := releaseFiles(pe.IMAGE_FILE_MACHINE_AMD64, "x64")
	if _, err := Stage(stagedRelease(t, files), t.TempDir(), pe.IMAGE_FILE_MACHINE_ARM64); err == nil {
		t.Error("an x64 executable was staged for arm64")
	}
	files[release.ExecutableName()] = []byte("not an executable")
	if _, err := Stage(stagedRelease(t, files), t.TempDir(), pe.IMAGE_FILE_MACHINE_ARM64); err == nil {
		t.Error("a file that is not an executable was staged")
	}
}

// installation writes an extracted release folder of files and returns its
// executable's path.
func installation(t *testing.T, files map[string][]byte) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), ui.AppName)
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(folder, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(folder, release.ExecutableName())
}

func TestInstallReplacesTheFilesAndKeepsThePreviousExecutable(t *testing.T) {
	arm := uint16(pe.IMAGE_FILE_MACHINE_ARM64)
	old := releaseFiles(arm, "old")
	executable := installation(t, old)
	folder := filepath.Dir(executable)
	if err := os.WriteFile(filepath.Join(folder, "settings-note.txt"), []byte("the user's"), 0o644); err != nil {
		t.Fatal(err)
	}
	staging, err := NewStaging(folder)
	if err != nil {
		t.Fatal(err)
	}
	newFiles := releaseFiles(arm, "new")
	newFiles["CHANGES.txt"] = []byte("a document the new version adds")
	names, err := Stage(stagedRelease(t, newFiles), staging, arm)
	if err != nil {
		t.Fatal(err)
	}
	skipped, err := Install(executable, staging, names)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("Install = %v, %v", skipped, err)
	}
	for name, content := range newFiles {
		if data, err := os.ReadFile(filepath.Join(folder, name)); err != nil || !bytes.Equal(data, content) {
			t.Errorf("%s was not replaced: %v", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(staging, previousExecutable)); err != nil || !bytes.Equal(data, old[release.ExecutableName()]) {
		t.Errorf("the previous executable was not kept in the staging folder: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(folder, "settings-note.txt")); string(data) != "the user's" {
		t.Error("a file the update does not ship was changed")
	}
	if err := Cleanup(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Cleanup left the staging folder: %v", err)
	}
}

func TestInstallPutsTheRunningVersionBackWhenTheNewOneCannotBePlaced(t *testing.T) {
	arm := uint16(pe.IMAGE_FILE_MACHINE_ARM64)
	old := releaseFiles(arm, "old")
	executable := installation(t, old)
	staging, err := NewStaging(filepath.Dir(executable))
	if err != nil {
		t.Fatal(err)
	}
	names, err := Stage(stagedRelease(t, releaseFiles(arm, "new")), staging, arm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(staging, stagedFiles, release.ExecutableName())); err != nil {
		t.Fatal(err)
	}
	_, err = Install(executable, staging, names)
	var failure *InstallError
	if !errors.As(err, &failure) || !failure.Restored {
		t.Fatalf("Install = %v, want a restored InstallError", err)
	}
	for name, content := range old {
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(executable), name)); err != nil || !bytes.Equal(data, content) {
			t.Errorf("%s is not as it was: %v", name, err)
		}
	}
}

func TestInstallKeepsADocumentItCannotReplace(t *testing.T) {
	arm := uint16(pe.IMAGE_FILE_MACHINE_ARM64)
	old := releaseFiles(arm, "old")
	delete(old, "USER-GUIDE.txt")
	executable := installation(t, old)
	folder := filepath.Dir(executable)
	// A folder of the document's name cannot be replaced by a file.
	if err := os.Mkdir(filepath.Join(folder, "USER-GUIDE.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	staging, err := NewStaging(folder)
	if err != nil {
		t.Fatal(err)
	}
	newFiles := releaseFiles(arm, "new")
	names, err := Stage(stagedRelease(t, newFiles), staging, arm)
	if err != nil {
		t.Fatal(err)
	}
	skipped, err := Install(executable, staging, names)
	if err != nil || !slices.Equal(skipped, []string{"USER-GUIDE.txt"}) {
		t.Fatalf("Install = %v, %v, want USER-GUIDE.txt skipped", skipped, err)
	}
	if data, _ := os.ReadFile(executable); !bytes.Equal(data, newFiles[release.ExecutableName()]) {
		t.Error("the new executable was not installed")
	}
}

func TestCleanupRemovesOnlyStagingFolders(t *testing.T) {
	folder := t.TempDir()
	staging, err := NewStaging(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, previousExecutable), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := []string{stagingPrefix + "file", "Screenshots", "notes.txt"}
	if err := os.WriteFile(filepath.Join(folder, keep[0]), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, keep[1]), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, keep[2]), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Cleanup(folder); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(folder)
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	slices.Sort(keep)
	if !slices.Equal(left, keep) {
		t.Errorf("Cleanup left %v, want %v", left, keep)
	}
}

func TestFolderRecognizesOnlyAnExtractedRelease(t *testing.T) {
	executable := installation(t, releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "x"))
	if folder, ok := Folder(executable); !ok || folder != filepath.Dir(executable) {
		t.Errorf("Folder(%s) = %s, %v", executable, folder, ok)
	}
	upper := filepath.Join(filepath.Dir(executable), strings.ToUpper(release.ExecutableName()))
	if _, ok := Folder(upper); !ok {
		t.Error("the executable's name was compared with its case")
	}
	if _, ok := Folder(filepath.Join(filepath.Dir(executable), "gamedevtools.exe")); ok {
		t.Error("an executable of another name was taken for a release")
	}
	for _, name := range release.PackagedDocuments {
		files := releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "x")
		delete(files, name)
		if _, ok := Folder(installation(t, files)); ok {
			t.Errorf("a folder without %s was taken for a release", name)
		}
	}
	files := releaseFiles(pe.IMAGE_FILE_MACHINE_ARM64, "x")
	delete(files, "README.txt")
	executable = installation(t, files)
	if err := os.Mkdir(filepath.Join(filepath.Dir(executable), "README.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Folder(executable); ok {
		t.Error("a folder named as a document was taken for the document")
	}
}

func TestChecksumFileIsTheOneReleaseWrites(t *testing.T) {
	data := []byte("zip")
	sum, err := parseChecksum(release.Checksum(data, "a.zip"), "a.zip")
	want := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(sum) != hex.EncodeToString(want[:]) {
		t.Errorf("parseChecksum = %x, %v", sum, err)
	}
}

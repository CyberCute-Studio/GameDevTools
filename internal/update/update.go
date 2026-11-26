// Package update keeps a portable installation of the application current
// with the newest release published on GitHub (APP-006 through APP-008). It
// is the application's only network access: it asks GitHub's API for the
// repository's releases, downloads the ZIP of a newer one with its checksum
// file, and replaces the files of the folder the running executable was
// extracted to. Its requests carry nothing but the running version, in their
// User-Agent (NFR-031), and it acts only on a folder that holds what a
// release ZIP extracts to (Folder), so a build run from anywhere else, such
// as a developer's or a test's, never changes.
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/release"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// The two addresses the application reaches, both GitHub's: the API that
// lists the repository's releases, newest first, and the prefix of a release
// asset's download, to which the release's tag and the asset's name are
// added. A download redirects to GitHub's storage for release assets.
const (
	releasesEndpoint = "https://api.github.com/repos/CyberCute-Studio/GameDevTools/releases?per_page=30"
	downloadEndpoint = "https://github.com/CyberCute-Studio/GameDevTools/releases/download/"
)

const (
	// CheckTimeout bounds the check for a newer release, which the
	// application's start waits for, so that a network that answers slowly
	// or not at all delays the start by no more than this.
	CheckTimeout = 5 * time.Second
	// stallTimeout ends a download that has received nothing for this long.
	stallTimeout = 30 * time.Second
	// The limits on what GitHub may send: the release list, a checksum file,
	// and a ZIP, which is about 2 MB, and what a ZIP may extract to.
	maxListBytes      = 4 << 20
	maxChecksumBytes  = 4 << 10
	maxArchiveBytes   = 128 << 20
	maxExtractedBytes = 256 << 20
	maxEntries        = 64
)

// Version is a release's major, minor, and patch numbers.
type Version [3]uint16

// ParseVersion reads a major.minor.patch version, as internal/release's
// Version and a release's tag after its "v" are written.
func ParseVersion(text string) (Version, bool) {
	var version Version
	parts := strings.Split(text, ".")
	if len(parts) != len(version) {
		return version, false
	}
	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return version, false
		}
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return version, false
		}
		version[i] = uint16(number)
	}
	return version, true
}

// Less reports whether v is an earlier version than other.
func (v Version) Less(other Version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// RelaunchArgument is the command-line argument Relaunch starts the version
// an update has installed with. That start does not look for a newer release,
// which the process it replaces has just done, so that a release whose
// executable is not the version its tag names cannot install itself again at
// every start, in a loop, and it waits for that process to exit rather than
// take its update window for a running instance's.
const RelaunchArgument = "--updated"

// Relaunched reports whether this process is the version an update has just
// installed, started by Relaunch.
func Relaunched() bool {
	return slices.Contains(os.Args[1:], RelaunchArgument)
}

// Current is the running application's version, or false if internal/release
// gives it in another form, which release.TestVersionIsMajorMinorPatch rules
// out.
func Current() (Version, bool) {
	return ParseVersion(release.Version)
}

// Asset is a file a release publishes.
type Asset struct {
	Name string
	Size int64
}

// Release is a newer release that publishes a ZIP for the running
// architecture, and the checksum file that verifies it.
type Release struct {
	Version  Version
	Tag      string
	Archive  Asset
	Checksum Asset
}

// Machine is the PE machine type of the running executable's architecture,
// which an update's executable must have, or false for an architecture no
// release ships.
func Machine() (uint16, bool) {
	switch runtime.GOARCH {
	case "amd64":
		return pe.IMAGE_FILE_MACHINE_AMD64, true
	case "arm64":
		return pe.IMAGE_FILE_MACHINE_ARM64, true
	}
	return 0, false
}

// Client reaches GitHub for releases. Tests point it at their own server.
type Client struct {
	HTTP *http.Client
	// ReleasesURL lists the releases, and DownloadURL is the prefix of an
	// asset's address, to which the tag, "/", and the asset's name are added.
	ReleasesURL, DownloadURL string
	// UserAgent names the application and its version, as GitHub's API
	// requires every request to name its client.
	UserAgent string
}

// NewClient returns the client of GitHub's releases of this repository.
func NewClient() *Client {
	return &Client{
		HTTP:        newHTTPClient(),
		ReleasesURL: releasesEndpoint,
		DownloadURL: downloadEndpoint,
		UserAgent:   ui.AppFileName + "/" + release.Version,
	}
}

// newHTTPClient returns an HTTP client whose connections give up on a network
// that does not answer, and that follows redirects to HTTPS addresses only. A
// proxy named by the HTTPS_PROXY environment variable is used, as Go's HTTP
// client does by default.
func newHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: CheckTimeout}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   CheckTimeout,
		ResponseHeaderTimeout: stallTimeout,
		IdleConnTimeout:       stallTimeout,
		MaxIdleConns:          2,
	}
	return &http.Client{Transport: transport, CheckRedirect: httpsOnly}
}

// httpsOnly refuses a redirect to an address that is not HTTPS, and a chain
// of more than ten, as Go's HTTP client does by default.
func httpsOnly(request *http.Request, via []*http.Request) error {
	if request.URL.Scheme != "https" {
		return fmt.Errorf("GitHub redirected the download to a %s address", request.URL.Scheme)
	}
	if len(via) >= 10 {
		return errors.New("GitHub redirected the download too many times")
	}
	return nil
}

// githubRelease and githubAsset are the parts of GitHub's release list that
// the check reads.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Draft   bool          `json:"draft"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Size  int64  `json:"size"`
}

// Latest returns the newest release later than current that publishes, for
// the architecture with the machine type, its ZIP and the ZIP's checksum
// file, or false when there is none. A draft does not count; a pre-release
// does, as every release so far is one (PRD section 20). Only the 30 most
// recent releases are read.
func (c *Client) Latest(ctx context.Context, current Version, machine uint16) (Release, bool, error) {
	architecture, err := release.ArchitectureLabel(machine)
	if err != nil {
		return Release{}, false, err
	}
	data, err := c.get(ctx, c.ReleasesURL, "application/vnd.github+json", maxListBytes)
	if err != nil {
		return Release{}, false, err
	}
	var releases []githubRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return Release{}, false, fmt.Errorf("GitHub's list of releases could not be read: %w", err)
	}
	found, ok := newest(releases, current, architecture)
	return found, ok, nil
}

// newest picks the newest release later than current with a ZIP and checksum
// file for the architecture.
func newest(releases []githubRelease, current Version, architecture string) (Release, bool) {
	var best Release
	found := false
	for _, candidate := range releases {
		number, tagged := strings.CutPrefix(candidate.TagName, "v")
		if candidate.Draft || !tagged {
			continue
		}
		version, ok := ParseVersion(number)
		if !ok || !current.Less(version) || (found && !best.Version.Less(version)) {
			continue
		}
		name := release.ArchiveName(version.String(), architecture)
		archive, hasArchive := uploaded(candidate.Assets, name)
		checksum, hasChecksum := uploaded(candidate.Assets, name+".sha256")
		if !hasArchive || !hasChecksum {
			continue
		}
		best = Release{Version: version, Tag: candidate.TagName, Archive: archive, Checksum: checksum}
		found = true
	}
	return best, found
}

// uploaded returns the asset of the name, when its upload finished.
func uploaded(assets []githubAsset, name string) (Asset, bool) {
	for _, asset := range assets {
		if asset.Name == name && asset.State == "uploaded" && asset.Size > 0 {
			return Asset{Name: asset.Name, Size: asset.Size}, true
		}
	}
	return Asset{}, false
}

// get returns the body of a successful GET of address, refusing one longer
// than limit.
func (c *Client) get(ctx context.Context, address, accept string, limit int64) ([]byte, error) {
	response, err := c.open(ctx, address, accept)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GitHub sent more than %d bytes", limit)
	}
	return data, nil
}

// open sends a GET of address and returns its response when the status is
// 200 OK. The caller closes the body.
func (c *Client) open(ctx context.Context, address, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", c.UserAgent)
	if accept == "application/vnd.github+json" {
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GitHub answered %s", response.Status)
	}
	return response, nil
}

// assetURL is the download address of a release's asset.
func (c *Client) assetURL(found Release, asset Asset) string {
	return c.DownloadURL + found.Tag + "/" + asset.Name
}

// Download fetches the release's ZIP into folder and checks it against the
// SHA-256 its checksum file publishes, calling progress with the bytes
// received so far and the ZIP's size as they arrive. It returns the ZIP's
// path. A ZIP that is cut short or fails the check is removed, and a download
// that receives nothing for 30 seconds fails.
func (c *Client) Download(ctx context.Context, found Release, folder string, progress func(received, total int64)) (string, error) {
	if found.Archive.Size > maxArchiveBytes {
		return "", fmt.Errorf("the update is %d bytes, more than an update may be", found.Archive.Size)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(stallTimeout, func() {
		stalled.Store(true)
		cancel()
	})
	defer watchdog.Stop()
	stopped := func(err error) error {
		if stalled.Load() {
			return errors.New("the download stopped receiving data")
		}
		return err
	}

	listing, err := c.get(ctx, c.assetURL(found, found.Checksum), "application/octet-stream", maxChecksumBytes)
	if err != nil {
		return "", stopped(fmt.Errorf("download the checksum: %w", err))
	}
	want, err := parseChecksum(string(listing), found.Archive.Name)
	if err != nil {
		return "", err
	}
	watchdog.Reset(stallTimeout)
	path := filepath.Join(folder, found.Archive.Name)
	err = c.receive(ctx, c.assetURL(found, found.Archive), path, found.Archive.Size, want, func(received int64) {
		watchdog.Reset(stallTimeout)
		if progress != nil {
			progress(received, found.Archive.Size)
		}
	})
	if err != nil {
		os.Remove(path)
		return "", stopped(err)
	}
	return path, nil
}

// receive writes the body of address to a new file at path and checks that it
// is size bytes with the SHA-256 want, calling received after each read.
func (c *Client) receive(ctx context.Context, address, path string, size int64, want []byte, received func(int64)) error {
	response, err := c.open(ctx, address, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("download the update: %w", err)
	}
	defer response.Body.Close()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	hash := sha256.New()
	var total int64
	buffer := make([]byte, 64<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if total += int64(n); total > size {
				file.Close()
				return fmt.Errorf("the update is larger than the %d bytes GitHub lists", size)
			}
			hash.Write(buffer[:n])
			if _, err := file.Write(buffer[:n]); err != nil {
				file.Close()
				return err
			}
			received(total)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return fmt.Errorf("download the update: %w", readErr)
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if total != size {
		return fmt.Errorf("the download ended after %d of %d bytes", total, size)
	}
	if got := hash.Sum(nil); !bytes.Equal(got, want) {
		return fmt.Errorf("the update's SHA-256 %s does not match the %s its release publishes", hex.EncodeToString(got), hex.EncodeToString(want))
	}
	return nil
}

// parseChecksum reads a checksum file's line, a SHA-256 and the file name it
// is for, as internal/release writes it and sha256sum reads it.
func parseChecksum(text, name string) ([]byte, error) {
	fields := strings.Fields(text)
	if len(fields) != 2 || len(fields[0]) != 2*sha256.Size {
		return nil, errors.New("the release's checksum file is not a SHA-256 and a file name")
	}
	if fields[1] != name {
		return nil, fmt.Errorf("the release's checksum file is for %s, not %s", fields[1], name)
	}
	sum, err := hex.DecodeString(fields[0])
	if err != nil {
		return nil, errors.New("the release's checksum file is not a SHA-256 and a file name")
	}
	return sum, nil
}

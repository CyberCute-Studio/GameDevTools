// Package release holds the product's release identity and builds what a
// release ships: the Windows resources linked into the executable and the
// portable ZIP. It needs no resource compiler or C toolchain and takes the
// product's names from internal/ui. The application links it only through
// internal/update, for the version it runs as and the names of the ZIP and the
// files it holds, and changes no file through it.
package release

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	// Version is the release's version, as major.minor.patch. The Windows
	// resources carry it, so run go generate ./cmd/gamedevtools after changing it.
	Version = "1.1.0"
	// Publisher is the company the executable's version resource names.
	Publisher = "CyberCute Studio"
	// Copyright is the executable's legal copyright line.
	Copyright = "Copyright (c) 2026 CyberCute Studio"
)

// Date is the time every file in the ZIP is stamped with, so that building
// the same source twice gives the same bytes. Update it with Version.
var Date = time.Date(2026, time.November, 26, 0, 0, 0, 0, time.UTC)

// PackagedDocuments names the files in the packaging folder that ship beside
// the executable. scripts/verify-package.ps1 checks for the same list.
var PackagedDocuments = []string{"LICENSE.txt", "README.txt", "RELEASE-NOTES.txt", "THIRD-PARTY-NOTICES.txt", "USER-GUIDE.txt"}

// ExecutableName is the released executable's file name.
func ExecutableName() string { return ui.AppFileName + ".exe" }

// ArchiveName is the file name of the portable ZIP of a version, such as
// Version, for a Windows architecture label, such as x64.
func ArchiveName(version, architecture string) string {
	return fmt.Sprintf("%s-%s-windows-%s.zip", ui.AppFileName, version, architecture)
}

// ArchitectureLabel returns the Windows architecture label a portable ZIP is
// named for, x64 or arm64, from its executable's COFF machine type, so that
// the name cannot disagree with the executable it holds.
func ArchitectureLabel(machine uint16) (string, error) {
	switch machine {
	case machineAMD64:
		return "x64", nil
	case machineARM64:
		return "arm64", nil
	}
	return "", fmt.Errorf("unsupported machine type 0x%04X", machine)
}

// versionNumbers parses a major.minor.patch version.
func versionNumbers(version string) ([3]uint16, error) {
	var numbers [3]uint16
	parts := strings.Split(version, ".")
	if len(parts) != len(numbers) {
		return numbers, fmt.Errorf("version %q is not major.minor.patch", version)
	}
	for i, part := range parts {
		value, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return numbers, fmt.Errorf("version %q: %w", version, err)
		}
		numbers[i] = uint16(value)
	}
	return numbers, nil
}

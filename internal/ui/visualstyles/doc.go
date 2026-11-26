// Package visualstyles gives a test binary that imports it the application
// manifest the executable carries (release.ApplicationManifest), so a test's
// windows use version 6 of the Common Controls, as the user's do, and are
// drawn and behave as the application's. Import it for its side effect from
// the tests of every package that creates windows:
//
//	import _ "github.com/CyberCute-Studio/GameDevTools/internal/ui/visualstyles"
//
// The manifest is a resource in manifest_windows_<arch>.syso, which the Go
// linker adds to any binary that links this package. Never import it from
// the application's own code: the executable has the manifest among its
// resources already (cmd/gamedevtools), and a binary keeps only one resource
// section, so the executable would lose its icon or its manifest.
package visualstyles

//go:generate go run ../../../cmd/release manifest -out .

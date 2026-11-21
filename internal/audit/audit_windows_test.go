//go:build windows

package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// reviewedModules is the dependency inventory (NFR-033, TECH-007): every
// module the application links, at the version whose license text was
// reviewed. A new or upgraded module fails TestDependenciesAreInventoried
// until its license is reviewed and recorded here and in
// docs/privacy-verification.md.
var reviewedModules = map[string]struct {
	version       string
	licenseSHA256 string
}{
	"golang.org/x/sys": {
		version:       "v0.47.0",
		licenseSHA256: "911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad",
	},
}

// forbiddenPackages are standard packages that would give the application a
// way to reach a network, run another program, load code, or serve its
// internals (NFR-030, NFR-031). "net" itself is linked, and is handled
// separately, as golang.org/x/sys/windows imports it for a few types.
var forbiddenPackages = regexp.MustCompile(`^(net/(http|rpc|smtp|mail|textproto)(/.*)?|crypto/tls|crypto/x509(/.*)?|os/exec|plugin|expvar|log/syslog|runtime/pprof)$`)

// networkFunctions are the functions of syscall and golang.org/x/sys/windows
// that create, connect, or accept a socket, send to an address, or resolve a
// name, with the unexported wrappers an inlined call leaves in the
// executable. The WSA functions every Go program on Windows links for its
// I/O poller are not among them: they do nothing without a socket, and the
// functions that create or connect one are.
var networkFunctions = []string{
	"Socket", "socket", "WSASocket", "Bind", "bind", "Connect", "connect", "ConnectEx", "LoadConnectEx",
	"Listen", "listen", "Accept", "AcceptEx", "Recvfrom", "recvfrom", "Sendto", "sendto",
	"WSARecvFrom", "WSASendTo", "WSASendto", "WSALookupServiceBegin", "WSALookupServiceNext",
	"GetAddrInfoW", "DnsQuery", "GetHostByName", "GetServByName", "GetProtoByName",
}

// networkSymbols match the code that opens a connection, listens, or
// resolves a name. The linker drops what nothing can call, so a match means
// the application can reach it.
var networkSymbols = regexp.MustCompile(`^(net\.(Dial|Listen|Lookup|Resolve|FileConn)|net\.\(\*(Dialer|Resolver|ListenConfig|TCPConn|UDPConn|IPConn|UnixConn|TCPListener|UnixListener|sysDialer)\)\.|(syscall|golang\.org/x/sys/windows)\.(` + strings.Join(networkFunctions, "|") + `)($|\.)|internal/poll\.\(\*FD\)\.(ConnectEx|AcceptEx|Accept|WriteTo|ReadFrom))`)

// programFunctions are the functions of os, syscall, and
// golang.org/x/sys/windows that start another program.
var programFunctions = []string{"StartProcess", "CreateProcess", "CreateProcessAsUser", "ShellExecute"}

// programProcedures are the Windows functions that start another program,
// which code could bind by name past programFunctions.
var programProcedures = set("CreateProcessA", "CreateProcessW", "CreateProcessAsUserA", "CreateProcessAsUserW",
	"CreateProcessWithLogonW", "CreateProcessWithTokenW", "WinExec", "ShellExecuteA", "ShellExecuteW",
	"ShellExecuteExA", "ShellExecuteExW")

// reviewedProgramSites is every binding of a programProcedures function.
// ShellExecuteExW is how Open Folder and Reveal in Explorer, which run only
// when the user asks, open File Explorer.
var reviewedProgramSites = []string{"internal/ui/folder_windows.go ShellExecuteExW"}

// reviewedLibraries are the Windows libraries the application's own code
// binds, none of which is a network, download, or telemetry API. The Go
// runtime and standard library load others for themselves, among them
// ws2_32.dll, which net brings in.
var reviewedLibraries = []string{
	"comdlg32.dll", "dwmapi.dll", "gdi32.dll", "kernel32.dll", "ole32.dll",
	"oleacc.dll", "shcore.dll", "shell32.dll", "user32.dll",
}

// fileMutators are the functions through which the application could
// create, change, move, or remove a file.
var fileMutators = map[string]map[string]bool{
	"os": set("Create", "CreateTemp", "WriteFile", "OpenFile", "OpenRoot", "OpenInRoot", "Rename", "Remove",
		"RemoveAll", "Mkdir", "MkdirAll", "MkdirTemp", "Chmod", "Chown", "Lchown", "Chtimes", "Symlink", "Link",
		"Truncate", "CopyFS"),
	"io/ioutil": set("WriteFile", "TempFile", "TempDir"),
	"syscall": set("CreateFile", "Open", "Write", "WriteFile", "DeleteFile", "MoveFile", "Rename", "Unlink",
		"CreateDirectory", "RemoveDirectory", "Mkdir", "Rmdir", "SetFileAttributes", "SetEndOfFile", "Ftruncate",
		"Chmod", "Fchmod", "Chown", "Lchown", "Fchown", "Utimes", "UtimesNano", "SetFileTime",
		"CreateHardLink", "CreateSymbolicLink", "Link", "Symlink"),
	"golang.org/x/sys/windows": set("CreateFile", "Open", "Write", "WriteFile", "DeleteFile", "MoveFile",
		"MoveFileEx", "Rename", "Unlink", "CreateDirectory", "RemoveDirectory", "Mkdir", "Rmdir",
		"SetFileInformationByHandle", "SetFileAttributes", "SetEndOfFile", "SetFileValidData", "Ftruncate",
		"Chmod", "Fchmod", "Chown", "Lchown", "Fchown", "Utimes", "UtimesNano", "SetFileTime",
		"CreateHardLink", "CreateSymbolicLink", "Link", "Symlink", "NtCreateFile", "NtSetInformationFile",
		"NtSetEaFile"),
}

// rawFileProcedures are Windows functions that change files, which the
// application must not bind by name past the checks on fileMutators.
var rawFileProcedures = set("CreateFileW", "CreateFileA", "WriteFile", "DeleteFileW", "DeleteFileA",
	"MoveFileW", "MoveFileA", "MoveFileExW", "MoveFileExA", "CopyFileW", "CopyFileA", "CopyFileExW",
	"ReplaceFileW", "CreateDirectoryW", "CreateDirectoryA", "RemoveDirectoryW", "CreateHardLinkW",
	"CreateSymbolicLinkW", "NtCreateFile", "NtSetInformationFile", "SHFileOperationW",
	"SHCreateDirectoryExW", "URLDownloadToFileW")

// reviewedFileSites is the filesystem access review (NFR-032): every
// function that uses a file mutator, by file, function, and mutator, with
// the only place it acts on. A new site fails
// TestFileChangesStayAtReviewedSites until it is reviewed here and in
// docs/privacy-verification.md.
var reviewedFileSites = map[string]string{
	"internal/diagnostics/logger.go Logger.open os.MkdirAll": logFolder,
	"internal/diagnostics/logger.go Logger.open os.OpenFile": logFolder,
	"internal/diagnostics/logger.go Logger.rotate os.Remove": logFolder,
	"internal/diagnostics/logger.go Logger.rotate os.Rename": logFolder,

	"internal/settings/store.go Store.backup os.MkdirAll":               settingsFolder,
	"internal/settings/store.go Store.backup os.OpenFile":               settingsFolder,
	"internal/settings/store.go Store.backup os.Remove":                 settingsFolder,
	"internal/settings/store.go Store.removeStaleTemporaries os.Remove": settingsFolder,
	"internal/settings/store.go Store.write os.CreateTemp":              settingsFolder,
	"internal/settings/store.go Store.write os.MkdirAll":                settingsFolder,
	"internal/settings/store.go Store.write os.Remove":                  settingsFolder,
	"internal/settings/store.go Store.write os.Rename":                  settingsFolder,

	"internal/screenshot/output/output.go saveWithEncoder os.CreateTemp": screenshotFolder,
	"internal/screenshot/output/output.go saveWithEncoder os.Link":       screenshotFolder,
	"internal/screenshot/output/output.go saveWithEncoder os.MkdirAll":   screenshotFolder,
	"internal/screenshot/output/output.go saveWithEncoder os.Remove":     screenshotFolder,

	"internal/imageresizer/output/identity_windows.go mkdirAll os.MkdirAll":                         resizerFolder,
	"internal/imageresizer/output/identity_windows.go open windows.CreateFile":                      inspectOnly,
	"internal/imageresizer/output/write_windows.go createTemp windows.CreateFile":                   resizerFolder,
	"internal/imageresizer/output/write_windows.go deleteHandle windows.SetFileInformationByHandle": resizerFolder,
	"internal/imageresizer/output/write_windows.go hold windows.CreateFile":                         resizerFolder,
	"internal/imageresizer/output/write_windows.go renameHandle windows.NtSetInformationFile":       resizerFolder,
	"internal/imageresizer/output/write_windows.go tempFile.Write windows.WriteFile":                resizerFolder,
	"internal/imageresizer/output/write_windows.go tempFile.discard windows.DeleteFile":             resizerFolder,
}

// The places a reviewed file change acts on.
const (
	logFolder        = "the per-user log folder"
	settingsFolder   = "the per-user settings folder"
	screenshotFolder = "the Screenshot output folder the user chose, or its default"
	resizerFolder    = "the Image Resizer output folder the user chose, or its default"
	inspectOnly      = "nothing: it opens a path with no access to its contents, to read its identity"
)

// reviewedEvents is the complete catalog of diagnostic events (SET-007):
// fixed operation and result codes, with no room for a path, a file name,
// or image or clipboard data.
var reviewedEvents = []string{
	"app.end-session/stopped",
	"settings.load/recovered-with-warnings",
	"settings.restore/not-restored",
	"settings.restore/restored",
	"settings.restore/write-failed",
	"settings.save/write-failed",
}

func set(names ...string) map[string]bool {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result
}

// goTool runs the go command for the release target, windows/amd64 without
// cgo, whatever machine the test runs on.
func goTool(t *testing.T, args ...string) []byte {
	t.Helper()
	command := exec.Command("go", args...)
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return output
}

type listedModule struct {
	Path    string
	Version string
	Main    bool
	Dir     string
}

type listedPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	GoFiles    []string
	CgoFiles   []string
	Imports    []string
	Module     *listedModule
}

var (
	listOnce sync.Once
	listed   []listedPackage
	listErr  error
)

// applicationPackages lists every package linked into the application.
func applicationPackages(t *testing.T) []listedPackage {
	t.Helper()
	listOnce.Do(func() {
		command := exec.Command("go", "list", "-deps", "-json", "github.com/CyberCute-Studio/GameDevTools/cmd/gamedevtools")
		command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			listErr = fmt.Errorf("go list: %w\n%s", err, stderr.String())
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var item listedPackage
			if err := decoder.Decode(&item); err == io.EOF {
				return
			} else if err != nil {
				listErr = err
				return
			}
			listed = append(listed, item)
		}
	})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(listed) == 0 {
		t.Fatal("go list found no packages")
	}
	return listed
}

// sourceFile is one parsed production file, named from the repository root.
type sourceFile struct {
	name string
	file *ast.File
}

// applicationSources parses every file of the application's own packages
// that the release target compiles.
func applicationSources(t *testing.T) []sourceFile {
	t.Helper()
	root := strings.TrimSpace(string(goTool(t, "list", "-m", "-f", "{{.Dir}}")))
	fileSet := token.NewFileSet()
	var sources []sourceFile
	for _, item := range applicationPackages(t) {
		if item.Standard || item.Module == nil || !item.Module.Main {
			continue
		}
		for _, name := range item.GoFiles {
			path := filepath.Join(item.Dir, name)
			file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, sourceFile{name: filepath.ToSlash(relative), file: file})
		}
	}
	if len(sources) == 0 {
		t.Fatal("no application source files were found")
	}
	return sources
}

// importNames maps the names a file refers to its imports by to their paths.
func importNames(file *ast.File) map[string]string {
	names := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = path
	}
	return names
}

// inspectFunctions calls visit for every node with the name of the function
// it is in, or "(package)" for a package-level declaration.
func inspectFunctions(file *ast.File, visit func(function string, node ast.Node)) {
	for _, declaration := range file.Decls {
		function := "(package)"
		if decl, ok := declaration.(*ast.FuncDecl); ok {
			function = decl.Name.Name
			if decl.Recv != nil && len(decl.Recv.List) == 1 {
				receiver := decl.Recv.List[0].Type
				if star, ok := receiver.(*ast.StarExpr); ok {
					receiver = star.X
				}
				if ident, ok := receiver.(*ast.Ident); ok {
					function = ident.Name + "." + function
				}
			}
		}
		ast.Inspect(declaration, func(node ast.Node) bool {
			if node != nil {
				visit(function, node)
			}
			return true
		})
	}
}

// TestDependenciesAreInventoried pins NFR-033 and TECH-007: the application
// links only the modules in the inventory, at the reviewed version and with
// the reviewed license text, and builds without cgo.
func TestDependenciesAreInventoried(t *testing.T) {
	linked := map[string]listedModule{}
	for _, item := range applicationPackages(t) {
		if len(item.CgoFiles) != 0 {
			t.Errorf("package %s uses cgo", item.ImportPath)
		}
		if item.Standard {
			continue
		}
		if item.Module == nil {
			t.Errorf("package %s belongs to no module", item.ImportPath)
			continue
		}
		if !item.Module.Main {
			linked[item.Module.Path] = *item.Module
		}
	}
	for path, module := range linked {
		review, known := reviewedModules[path]
		if !known {
			t.Errorf("module %s is linked but not in the inventory", path)
			continue
		}
		if module.Version != review.version {
			t.Errorf("module %s is at %s, but %s was the version reviewed", path, module.Version, review.version)
		}
		license, err := os.ReadFile(filepath.Join(module.Dir, "LICENSE"))
		if err != nil {
			t.Errorf("module %s: %v", path, err)
			continue
		}
		// Git may check a license out with either line ending.
		sum := sha256.Sum256(bytes.ReplaceAll(license, []byte("\r\n"), []byte("\n")))
		if got := hex.EncodeToString(sum[:]); got != review.licenseSHA256 {
			t.Errorf("module %s has license text %s, but %s was reviewed", path, got, review.licenseSHA256)
		}
	}
	for path := range reviewedModules {
		if _, used := linked[path]; !used {
			t.Errorf("module %s is in the inventory but no longer linked", path)
		}
	}
	required := strings.Fields(string(goTool(t, "list", "-m", "-f", "{{if not .Main}}{{.Path}}{{end}}", "all")))
	for _, path := range required {
		if _, known := reviewedModules[path]; !known {
			t.Errorf("go.mod requires %s, which is not in the inventory", path)
		}
	}
}

// TestApplicationLinksNoNetworkPackages pins NFR-030 and NFR-031 at the
// package level: nothing linked is an HTTP, mail, RPC, or TLS client, a way
// to run another program or load code, or a profiling endpoint, and only
// golang.org/x/sys/windows imports net.
func TestApplicationLinksNoNetworkPackages(t *testing.T) {
	for _, item := range applicationPackages(t) {
		if item.Standard && forbiddenPackages.MatchString(item.ImportPath) {
			t.Errorf("the application links %s", item.ImportPath)
		}
		if item.ImportPath != "golang.org/x/sys/windows" && !item.Standard && slices.Contains(item.Imports, "net") {
			t.Errorf("%s imports net", item.ImportPath)
		}
	}
}

// TestExecutableContainsNoNetworkCode pins NFR-030 and NFR-031 in the built
// executable: of net, which golang.org/x/sys/windows imports for its types,
// the linker keeps nothing that could connect, listen, or resolve a name.
func TestExecutableContainsNoNetworkCode(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "gamedevtools.exe")
	goTool(t, "build", "-o", executable, "github.com/CyberCute-Studio/GameDevTools/cmd/gamedevtools")
	output := goTool(t, "tool", "nm", executable)
	symbols := 0
	foundMain := false
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		symbols++
		name := fields[2]
		foundMain = foundMain || name == "main.main"
		if networkSymbols.MatchString(name) {
			t.Errorf("the executable contains network code: %s", name)
		}
	}
	if !foundMain || symbols < 1000 {
		t.Fatalf("read %d symbols, main.main found = %v; the symbol table was not read", symbols, foundMain)
	}
}

// TestApplicationCallsNoNetworkOrProgramFunction pins NFR-030 and NFR-031 in
// the source, where a call the compiler inlines still shows: no code uses a
// socket or name-resolution function, none starts a program through os,
// syscall, or golang.org/x/sys/windows, and the only binding of a Windows
// function that starts one is the reviewed Explorer call.
func TestApplicationCallsNoNetworkOrProgramFunction(t *testing.T) {
	network, program := set(networkFunctions...), set(programFunctions...)
	sites := map[string]bool{}
	for _, source := range applicationSources(t) {
		names := importNames(source.file)
		inspectFunctions(source.file, func(function string, node ast.Node) {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				pkg, ok := value.X.(*ast.Ident)
				if !ok {
					return
				}
				switch path := names[pkg.Name]; {
				case (path == "syscall" || path == "golang.org/x/sys/windows") && network[value.Sel.Name]:
					t.Errorf("%s: %s uses the network function %s.%s", source.name, function, pkg.Name, value.Sel.Name)
				case (path == "os" || path == "syscall" || path == "golang.org/x/sys/windows") && program[value.Sel.Name]:
					t.Errorf("%s: %s starts a program through %s.%s", source.name, function, pkg.Name, value.Sel.Name)
				}
			case *ast.CallExpr:
				if name, ok := boundProcedure(t, source, value); ok && programProcedures[name] {
					sites[source.name+" "+name] = true
				}
			}
		})
	}
	found := make([]string, 0, len(sites))
	for site := range sites {
		found = append(found, site)
	}
	slices.Sort(found)
	if !slices.Equal(found, reviewedProgramSites) {
		t.Errorf("the application binds %v to start a program, the review covers %v", found, reviewedProgramSites)
	}
}

// boundProcedure is the Windows function a NewProc call binds. A name that is
// not a string literal cannot be checked, so it fails the test rather than
// passing as no binding.
func boundProcedure(t *testing.T, source sourceFile, call *ast.CallExpr) (string, bool) {
	t.Helper()
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewProc" || len(call.Args) == 0 {
		return "", false
	}
	name, ok := stringLiteral(call.Args[0])
	if !ok {
		t.Errorf("%s binds a Windows function whose name is not a string literal", source.name)
	}
	return name, ok
}

// TestApplicationLoadsOnlyReviewedLibraries pins that the application's own
// code loads only the reviewed Windows libraries, each from the system
// folder: nothing that speaks HTTP, DNS, or sockets, and no library a planted
// file of the same name could stand in for. It also binds no function by
// address, which the checks on NewProc could not read.
func TestApplicationLoadsOnlyReviewedLibraries(t *testing.T) {
	loaders := set("NewLazyDLL", "LoadDLL", "MustLoadDLL", "LoadLibrary", "LoadLibraryEx")
	lookups := set("GetProcAddress", "GetProcAddressByOrdinal")
	found := map[string]bool{}
	for _, source := range applicationSources(t) {
		names := importNames(source.file)
		inspectFunctions(source.file, func(_ string, node ast.Node) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || (names[pkg.Name] != "golang.org/x/sys/windows" && names[pkg.Name] != "syscall") {
				return
			}
			if loaders[selector.Sel.Name] {
				t.Errorf("%s calls %s, which is not limited to the system folder", source.name, selector.Sel.Name)
			}
			if lookups[selector.Sel.Name] {
				t.Errorf("%s calls %s, which binds a Windows function past the NewProc checks", source.name, selector.Sel.Name)
			}
			if selector.Sel.Name != "NewLazySystemDLL" {
				return
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s loads a library whose name is not a constant", source.name)
				return
			}
			library, _ := strconv.Unquote(literal.Value)
			found[strings.ToLower(library)] = true
		})
	}
	var libraries []string
	for library := range found {
		libraries = append(libraries, library)
	}
	slices.Sort(libraries)
	if !slices.Equal(libraries, reviewedLibraries) {
		t.Errorf("the application loads %v, the review covers %v", libraries, reviewedLibraries)
	}
}

// TestApplicationNamesNoNetworkEndpoint pins NFR-031: no string in the
// application is a URL, so there is no address to upload to or fetch from.
func TestApplicationNamesNoNetworkEndpoint(t *testing.T) {
	for _, source := range applicationSources(t) {
		inspectFunctions(source.file, func(function string, node ast.Node) {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return
			}
			if value, err := strconv.Unquote(literal.Value); err == nil && strings.Contains(value, "://") {
				t.Errorf("%s: %s names an endpoint: %q", source.name, function, value)
			}
		})
	}
}

// TestFileChangesStayAtReviewedSites pins NFR-032: every function that can
// change a file is in the review, with the place it acts on, no code binds
// a file-changing Windows function around that list, and nothing uses the
// system's temporary folder.
func TestFileChangesStayAtReviewedSites(t *testing.T) {
	sites := map[string]bool{}
	for _, source := range applicationSources(t) {
		names := importNames(source.file)
		inspectFunctions(source.file, func(function string, node ast.Node) {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				pkg, ok := value.X.(*ast.Ident)
				if !ok {
					return
				}
				path := names[pkg.Name]
				if fileMutators[path][value.Sel.Name] {
					sites[source.name+" "+function+" "+pkg.Name+"."+value.Sel.Name] = true
				}
				if path == "os" && value.Sel.Name == "TempDir" {
					t.Errorf("%s: %s uses the system's temporary folder", source.name, function)
				}
			case *ast.CallExpr:
				if name, ok := boundProcedure(t, source, value); ok && rawFileProcedures[name] {
					t.Errorf("%s binds %s directly", source.name, name)
				}
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok || len(value.Args) == 0 {
					return
				}
				if pkg, ok := selector.X.(*ast.Ident); ok && names[pkg.Name] == "os" && (selector.Sel.Name == "CreateTemp" || selector.Sel.Name == "MkdirTemp") {
					if literal, ok := value.Args[0].(*ast.BasicLit); ok && literal.Value == `""` {
						t.Errorf("%s: %s creates a file in the system's temporary folder", source.name, function)
					}
				}
			}
		})
	}
	for site := range sites {
		if _, reviewed := reviewedFileSites[site]; !reviewed {
			t.Errorf("unreviewed file change: %s", site)
		}
	}
	for site := range reviewedFileSites {
		if !sites[site] {
			t.Errorf("reviewed file change no longer exists: %s", site)
		}
	}
}

// TestDiagnosticEventsAreFixed pins SET-007: every call to the logger passes
// a diagnostics.Event literal with a constant operation and result code from
// the catalog, directly or through the shell's logEvent, so no path, file
// name, or image or clipboard data can reach the log, and the catalog lists
// no event that is gone. An event built any other way, or the logger's method
// taken as a value, fails the test.
func TestDiagnosticEventsAreFixed(t *testing.T) {
	found := map[string]bool{}
	record := func(source sourceFile, function string, operation, code ast.Expr) {
		operationText, ok := stringLiteral(operation)
		codeText, codeOK := stringLiteral(code)
		if !ok || !codeOK {
			t.Errorf("%s: %s logs an event whose operation or code is not a constant", source.name, function)
			return
		}
		found[operationText+"/"+codeText] = true
	}
	for _, source := range applicationSources(t) {
		if strings.HasPrefix(source.name, "internal/diagnostics/") {
			continue
		}
		names := importNames(source.file)
		// A call is visited before the selector it calls, so a selector not
		// marked here is the method taken as a value.
		called := map[*ast.SelectorExpr]bool{}
		inspectFunctions(source.file, func(function string, node ast.Node) {
			switch value := node.(type) {
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok {
					return
				}
				switch selector.Sel.Name {
				case "Record":
					called[selector] = true
					// logEvent is the shell's wrapper, whose callers are checked below.
					if function == "nativeShell.logEvent" {
						return
					}
					if len(value.Args) != 1 {
						t.Errorf("%s: %s calls Record with %d arguments", source.name, function, len(value.Args))
						return
					}
					operation, code, ok := eventLiteral(value.Args[0], names)
					if !ok {
						t.Errorf("%s: %s logs an event that is not a diagnostics.Event literal", source.name, function)
						return
					}
					record(source, function, operation, code)
				case "logEvent":
					called[selector] = true
					if len(value.Args) != 3 {
						t.Errorf("%s: %s calls logEvent with %d arguments", source.name, function, len(value.Args))
						return
					}
					record(source, function, value.Args[1], value.Args[2])
				}
			case *ast.SelectorExpr:
				if (value.Sel.Name == "Record" || value.Sel.Name == "logEvent") && !called[value] {
					t.Errorf("%s: %s uses %s other than by calling it", source.name, function, value.Sel.Name)
				}
			}
		})
	}
	var events []string
	for event := range found {
		events = append(events, event)
	}
	slices.Sort(events)
	if !slices.Equal(events, reviewedEvents) {
		t.Errorf("the application logs %v, the catalog lists %v", events, reviewedEvents)
	}
}

// eventLiteral is the Operation and Code of an expression that is a
// diagnostics.Event literal.
func eventLiteral(expression ast.Expr, names map[string]string) (operation, code ast.Expr, ok bool) {
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		return nil, nil, false
	}
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Event" {
		return nil, nil, false
	}
	if pkg, ok := selector.X.(*ast.Ident); !ok || !strings.HasSuffix(names[pkg.Name], "/internal/diagnostics") {
		return nil, nil, false
	}
	for _, element := range literal.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			if key, ok := pair.Key.(*ast.Ident); ok {
				switch key.Name {
				case "Operation":
					operation = pair.Value
				case "Code":
					code = pair.Value
				}
			}
		}
	}
	return operation, code, true
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

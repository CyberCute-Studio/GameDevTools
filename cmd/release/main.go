// Command release generates the executable's Windows resources and packages a
// built executable as the portable ZIP. scripts/package.ps1 runs it.
//
//	release resources -icon assets/icon.ico -out cmd/gamedevtools
//	release manifest -out internal/ui/visualstyles
//	release package -exe <executable> -packaging packaging -out <folder>
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CyberCute-Studio/GameDevTools/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release resources|package [flags]")
	}
	switch args[0] {
	case "resources":
		return resources(args[1:])
	case "manifest":
		return manifest(args[1:])
	case "package":
		return archive(args[1:])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

// resources writes the resource object for every supported architecture.
func resources(args []string) error {
	flags := flag.NewFlagSet("resources", flag.ContinueOnError)
	icon := flags.String("icon", "", "ICO file")
	out := flags.String("out", "", "main package folder")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *icon == "" || *out == "" {
		return errors.New("resources needs -icon and -out")
	}
	data, err := os.ReadFile(*icon)
	if err != nil {
		return err
	}
	for _, architecture := range []string{"amd64", "arm64"} {
		object, err := release.BuildResources(data, architecture)
		if err != nil {
			return err
		}
		path := filepath.Join(*out, "resource_windows_"+architecture+".syso")
		if err := os.WriteFile(path, object, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// manifest writes the manifest-only resource object, which test binaries take
// from internal/ui/visualstyles, for every supported architecture.
func manifest(args []string) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	out := flags.String("out", "", "package folder")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("manifest needs -out")
	}
	for _, architecture := range []string{"amd64", "arm64"} {
		object, err := release.BuildManifestResources(architecture)
		if err != nil {
			return err
		}
		path := filepath.Join(*out, "manifest_windows_"+architecture+".syso")
		if err := os.WriteFile(path, object, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// archive writes the x64 ZIP and its checksum file, and prints the ZIP's path.
func archive(args []string) error {
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	executable := flags.String("exe", "", "release executable")
	packaging := flags.String("packaging", "", "folder holding the files shipped beside it")
	out := flags.String("out", "", "output folder")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *executable == "" || *packaging == "" || *out == "" {
		return errors.New("package needs -exe, -packaging, and -out")
	}
	files := map[string][]byte{}
	sources := map[string]string{
		release.ExecutableName(): *executable,
	}
	for _, name := range release.PackagedDocuments {
		sources[name] = filepath.Join(*packaging, name)
	}
	for name, source := range sources {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		files[name] = data
	}
	data, err := release.BuildArchive(files)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	name := release.ArchiveName("x64")
	path := filepath.Join(*out, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(path+".sha256", []byte(release.Checksum(data, name)), 0o644); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

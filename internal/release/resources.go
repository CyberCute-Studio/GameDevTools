package release

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// The Windows resource types, IDs, and language the executable carries.
const (
	resourceIcon      = 3
	resourceGroupIcon = 14
	resourceVersion   = 16
	resourceManifest  = 24

	// groupIconID is the ID the application loads its icon by.
	groupIconID = 1
	versionID   = 1
	// manifestID is CREATEPROCESS_MANIFEST_RESOURCE_ID, the manifest the
	// loader applies to the process an executable starts.
	manifestID = 1
	// languageEnUS is U.S. English, and codePageUnicode the code page of the
	// version strings. translationKey is the two together, as the version
	// resource spells them.
	languageEnUS    = 0x0409
	codePageUnicode = 1200
	translationKey  = "040904B0"
)

// COFF values for the object file the Go linker takes resources from.
const (
	machineAMD64 = 0x8664
	machineARM64 = 0xAA64
	// The ADDR32NB relocations, which hold an address relative to the image
	// base, as the resource directory's data entries need.
	relocAMD64Addr32NB = 3
	relocARM64Addr32NB = 2
	sectionCharacters  = 0x40000040 // initialized data, readable
	symbolClassStatic  = 3
)

// resource is one leaf of the resource tree.
type resource struct {
	kind, id uint16
	data     []byte
}

// ApplicationManifest is the application manifest every binary that shows the
// application's windows carries. It asks for version 6 of the Common
// Controls, which draw the visual styles and send the custom-draw
// notifications the dark theme paints buttons through (internal/ui); without
// it Windows gives version 5, whose controls are drawn the Windows 2000 way.
// It declares nothing else, so the process keeps the DPI awareness and the
// other behavior the application sets for itself at startup.
const ApplicationManifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>
`

// BuildResources returns a COFF object for a Go architecture, amd64 or arm64,
// holding the application icon from an ICO file's bytes, the version
// resource, and the application manifest. Named resource_windows_<arch>.syso
// in the main package, the Go linker adds it to the executable without a C
// toolchain. The same input gives the same bytes.
func BuildResources(icon []byte, architecture string) ([]byte, error) {
	machine, relocation, err := coffMachine(architecture)
	if err != nil {
		return nil, err
	}
	resources, err := iconResources(icon)
	if err != nil {
		return nil, err
	}
	version, err := versionResource()
	if err != nil {
		return nil, err
	}
	resources = append(resources,
		resource{kind: resourceVersion, id: versionID, data: version},
		manifestResource(),
	)
	section, relocations := resourceSection(resources)
	return coffObject(machine, relocation, section, relocations), nil
}

// BuildManifestResources returns a COFF object for a Go architecture holding
// the application manifest alone. Named manifest_windows_<arch>.syso in
// internal/ui/visualstyles, it gives a test binary that imports that package
// the Common Controls the executable has, so the tests drive the controls the
// user sees. It must not be linked into the executable, whose resources hold
// the manifest already: a binary keeps one resource section.
func BuildManifestResources(architecture string) ([]byte, error) {
	machine, relocation, err := coffMachine(architecture)
	if err != nil {
		return nil, err
	}
	section, relocations := resourceSection([]resource{manifestResource()})
	return coffObject(machine, relocation, section, relocations), nil
}

func manifestResource() resource {
	return resource{kind: resourceManifest, id: manifestID, data: []byte(ApplicationManifest)}
}

// coffMachine returns the COFF machine and ADDR32NB relocation type of a Go
// architecture.
func coffMachine(architecture string) (machine, relocation uint16, err error) {
	switch architecture {
	case "amd64":
		return machineAMD64, relocAMD64Addr32NB, nil
	case "arm64":
		return machineARM64, relocARM64Addr32NB, nil
	}
	return 0, 0, fmt.Errorf("unsupported architecture %q", architecture)
}

// iconResources splits an ICO file into one icon resource per image and the
// group resource that lists them.
func iconResources(file []byte) ([]resource, error) {
	const headerSize, entrySize = 6, 16
	if len(file) < headerSize {
		return nil, errors.New("icon file is too short")
	}
	if binary.LittleEndian.Uint16(file) != 0 || binary.LittleEndian.Uint16(file[2:]) != 1 {
		return nil, errors.New("not an icon file")
	}
	count := int(binary.LittleEndian.Uint16(file[4:]))
	if count == 0 {
		return nil, errors.New("icon file holds no images")
	}
	if len(file) < headerSize+count*entrySize {
		return nil, errors.New("icon file's directory is truncated")
	}
	group := bytes.NewBuffer(append([]byte(nil), file[:headerSize]...))
	resources := make([]resource, 0, count+1)
	for i := range count {
		entry := file[headerSize+i*entrySize:][:entrySize]
		size := uint64(binary.LittleEndian.Uint32(entry[8:]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:]))
		if size == 0 || offset+size > uint64(len(file)) {
			return nil, fmt.Errorf("icon image %d lies outside the file", i)
		}
		id := uint16(i + 1)
		group.Write(entry[:12])
		binary.Write(group, binary.LittleEndian, id)
		resources = append(resources, resource{kind: resourceIcon, id: id, data: file[offset : offset+size]})
	}
	return append(resources, resource{kind: resourceGroupIcon, id: groupIconID, data: group.Bytes()}), nil
}

// versionNode is one block of a VS_VERSIONINFO resource.
type versionNode struct {
	key string
	// value is the block's value, and valueLength its length in the unit
	// its type counts: UTF-16 code units for text, bytes for binary.
	value       []byte
	valueLength uint16
	text        bool
	children    []versionNode
}

func (n versionNode) encode() []byte {
	var out bytes.Buffer
	out.Write(make([]byte, 6))
	out.Write(utf16z(n.key))
	align(&out)
	out.Write(n.value)
	for _, child := range n.children {
		align(&out)
		out.Write(child.encode())
	}
	data := out.Bytes()
	binary.LittleEndian.PutUint16(data, uint16(len(data)))
	binary.LittleEndian.PutUint16(data[2:], n.valueLength)
	if n.text {
		binary.LittleEndian.PutUint16(data[4:], 1)
	}
	return data
}

func versionString(key, value string) versionNode {
	encoded := utf16z(value)
	return versionNode{key: key, value: encoded, valueLength: uint16(len(encoded) / 2), text: true}
}

// versionResource builds the VS_VERSIONINFO File Properties shows.
func versionResource() ([]byte, error) {
	numbers, err := versionNumbers(Version)
	if err != nil {
		return nil, err
	}
	high := uint32(numbers[0])<<16 | uint32(numbers[1])
	low := uint32(numbers[2]) << 16
	var fixed bytes.Buffer
	for _, field := range []uint32{
		0xFEEF04BD, 0x00010000, // signature, structure version
		high, low, high, low, // file version, product version
		0x3F, 0, // flags mask, flags
		0x00040004, 1, 0, // Windows NT, application, no subtype
		0, 0, // file date
	} {
		binary.Write(&fixed, binary.LittleEndian, field)
	}
	fileVersion := fmt.Sprintf("%d.%d.%d.0", numbers[0], numbers[1], numbers[2])
	root := versionNode{
		key:         "VS_VERSION_INFO",
		value:       fixed.Bytes(),
		valueLength: uint16(fixed.Len()),
		children: []versionNode{
			{key: "StringFileInfo", text: true, children: []versionNode{{
				key: translationKey, text: true, children: []versionNode{
					versionString("CompanyName", Publisher),
					versionString("FileDescription", ui.AppName),
					versionString("FileVersion", fileVersion),
					versionString("InternalName", ui.AppFileName),
					versionString("LegalCopyright", Copyright),
					versionString("OriginalFilename", ExecutableName()),
					versionString("ProductName", ui.AppName),
					versionString("ProductVersion", Version),
				},
			}}},
			{key: "VarFileInfo", text: true, children: []versionNode{{
				key:         "Translation",
				value:       binary.LittleEndian.AppendUint32(nil, uint32(codePageUnicode)<<16|languageEnUS),
				valueLength: 4,
			}}},
		},
	}
	return root.encode(), nil
}

func utf16z(text string) []byte {
	var out []byte
	for _, unit := range utf16.Encode([]rune(text)) {
		out = binary.LittleEndian.AppendUint16(out, unit)
	}
	return append(out, 0, 0)
}

func align(out *bytes.Buffer) {
	for out.Len()%4 != 0 {
		out.WriteByte(0)
	}
}

// resourceSection lays resources out as a .rsrc section: the three-level
// directory (type, ID, language), the data entries, then the data. It also
// returns the offsets of the data entries' address fields, which the linker
// must relocate.
func resourceSection(resources []resource) (section []byte, relocations []uint32) {
	const directorySize, entrySize, dataEntrySize = 16, 8, 16
	sorted := append([]resource(nil), resources...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].kind != sorted[j].kind {
			return sorted[i].kind < sorted[j].kind
		}
		return sorted[i].id < sorted[j].id
	})
	var kinds []uint16
	counts := map[uint16]int{}
	for _, item := range sorted {
		if counts[item.kind] == 0 {
			kinds = append(kinds, item.kind)
		}
		counts[item.kind]++
	}

	// The directories come first, then the data entries, then the data.
	typeDirectories := directorySize + len(kinds)*entrySize
	nameDirectories := typeDirectories
	for _, kind := range kinds {
		nameDirectories += directorySize + counts[kind]*entrySize
	}
	dataEntries := nameDirectories + len(sorted)*(directorySize+entrySize)
	dataStart := dataEntries + len(sorted)*dataEntrySize

	out := make([]byte, dataStart)
	writeDirectory := func(at, count int) int {
		binary.LittleEndian.PutUint16(out[at+14:], uint16(count))
		return at + directorySize
	}
	writeEntry := func(at int, id uint16, target uint32) {
		binary.LittleEndian.PutUint32(out[at:], uint32(id))
		binary.LittleEndian.PutUint32(out[at+4:], target)
	}
	const subdirectory = 0x80000000

	rootEntry := writeDirectory(0, len(kinds))
	typeAt, nameAt, index := typeDirectories, nameDirectories, 0
	for k, kind := range kinds {
		writeEntry(rootEntry+k*entrySize, kind, uint32(typeAt)|subdirectory)
		nameEntry := writeDirectory(typeAt, counts[kind])
		typeAt = nameEntry + counts[kind]*entrySize
		for n := range counts[kind] {
			item := sorted[index]
			writeEntry(nameEntry+n*entrySize, item.id, uint32(nameAt)|subdirectory)
			languageEntry := writeDirectory(nameAt, 1)
			dataEntry := dataEntries + index*dataEntrySize
			writeEntry(languageEntry, languageEnUS, uint32(dataEntry))
			nameAt = languageEntry + entrySize

			for len(out)%8 != 0 {
				out = append(out, 0)
			}
			binary.LittleEndian.PutUint32(out[dataEntry:], uint32(len(out)))
			binary.LittleEndian.PutUint32(out[dataEntry+4:], uint32(len(item.data)))
			relocations = append(relocations, uint32(dataEntry))
			out = append(out, item.data...)
			index++
		}
	}
	return out, relocations
}

// coffObject wraps a .rsrc section in a COFF object with one section symbol,
// which every relocation refers to.
func coffObject(machine, relocationType uint16, section []byte, relocations []uint32) []byte {
	const headerSize, sectionHeaderSize, relocationSize = 20, 40, 10
	sectionAt := uint32(headerSize + sectionHeaderSize)
	relocationsAt := sectionAt + uint32(len(section))
	symbolsAt := relocationsAt + uint32(len(relocations)*relocationSize)
	name := [8]byte{'.', 'r', 's', 'r', 'c'}

	var out bytes.Buffer
	write := func(values ...any) {
		for _, value := range values {
			binary.Write(&out, binary.LittleEndian, value)
		}
	}
	// File header: machine, one section, no timestamp, the symbol table,
	// one symbol, no optional header, no characteristics.
	write(machine, uint16(1), uint32(0), symbolsAt, uint32(1), uint16(0), uint16(0))
	// Section header.
	write(name, uint32(0), uint32(0), uint32(len(section)), sectionAt, relocationsAt,
		uint32(0), uint16(len(relocations)), uint16(0), uint32(sectionCharacters))
	out.Write(section)
	for _, offset := range relocations {
		write(offset, uint32(0), relocationType)
	}
	// The section's symbol, then an empty string table.
	write(name, uint32(0), int16(1), uint16(0), uint8(symbolClassStatic), uint8(0))
	write(uint32(4))
	return out.Bytes()
}

package release

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// BuildArchive returns the portable ZIP of files, which maps each file's name
// to its content, all under a folder named for the product. Entries are sorted
// and stamped with Date, so the same files give the same bytes.
func BuildArchive(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, name := range names {
		entry, err := writer.CreateHeader(&zip.FileHeader{
			Name:     ui.AppName + "/" + name,
			Method:   zip.Deflate,
			Modified: Date,
		})
		if err != nil {
			return nil, fmt.Errorf("add %s: %w", name, err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish the archive: %w", err)
	}
	return out.Bytes(), nil
}

// Checksum returns a checksum file's line for data: its SHA-256 and name, in
// the format sha256sum reads.
func Checksum(data []byte, name string) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

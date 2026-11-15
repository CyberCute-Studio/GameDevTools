package decode

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
)

// JPEG markers the orientation scan distinguishes.
const (
	markerSOI  = 0xd8
	markerEOI  = 0xd9
	markerSOS  = 0xda
	markerTEM  = 0x01
	markerRST0 = 0xd0
	markerRST7 = 0xd7
	markerAPP1 = 0xe1
)

// exifHeader opens the APP1 payload of an EXIF segment. XMP and other APP1
// payloads carry different identifiers and are skipped.
var exifHeader = []byte("Exif\x00\x00")

// TIFF structure used to locate the orientation tag in IFD0.
const (
	tiffMagic          = 42
	tagOrientation     = 0x0112
	tiffTypeShort      = 3
	tiffTypeLong       = 4
	tiffEntrySize      = 12
	tiffHeaderSize     = 8
	tiffEntryCountSize = 2
)

// readOrientation scans the JPEG marker segments that precede the image
// data for the first EXIF APP1 segment and returns its IFD0 orientation.
// Orientation is advisory metadata: a missing, malformed, or out-of-range
// value yields OrientationNormal rather than rejecting a source whose image
// data still decodes, matching how image viewers treat such files.
func readOrientation(reader *bufio.Reader) Orientation {
	var soi [2]byte
	if _, err := io.ReadFull(reader, soi[:]); err != nil || soi[0] != 0xff || soi[1] != markerSOI {
		return OrientationNormal
	}
	for {
		prefix, err := reader.ReadByte()
		if err != nil || prefix != 0xff {
			// Segments before the scan are contiguous; anything else means
			// the header is damaged, which the decoder reports itself.
			return OrientationNormal
		}
		marker := byte(0xff)
		for marker == 0xff {
			// A marker may be preceded by any number of 0xFF fill bytes.
			if marker, err = reader.ReadByte(); err != nil {
				return OrientationNormal
			}
		}
		switch {
		case marker == markerEOI || marker == markerSOS:
			// EXIF metadata precedes the image data.
			return OrientationNormal
		case marker == markerSOI || marker == markerTEM || (marker >= markerRST0 && marker <= markerRST7):
			// Standalone markers carry no length.
			continue
		}
		var length [2]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return OrientationNormal
		}
		payload := int(binary.BigEndian.Uint16(length[:])) - 2
		if payload < 0 {
			return OrientationNormal
		}
		if marker != markerAPP1 || payload < len(exifHeader) {
			if _, err := reader.Discard(payload); err != nil {
				return OrientationNormal
			}
			continue
		}
		// The 16-bit segment length bounds this allocation at 64 KiB.
		segment := make([]byte, payload)
		if _, err := io.ReadFull(reader, segment); err != nil {
			return OrientationNormal
		}
		if !bytes.HasPrefix(segment, exifHeader) {
			continue
		}
		if orientation, ok := parseTIFFOrientation(segment[len(exifHeader):]); ok {
			return orientation
		}
		return OrientationNormal
	}
}

// parseTIFFOrientation reads the orientation tag from IFD0 of a TIFF
// structure in either byte order. Every offset is bounds-checked against
// the segment, so a malformed structure reports ok == false instead of
// reading outside it.
func parseTIFFOrientation(tiff []byte) (Orientation, bool) {
	if len(tiff) < tiffHeaderSize {
		return 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false
	}
	if order.Uint16(tiff[2:4]) != tiffMagic {
		return 0, false
	}
	size := uint64(len(tiff))
	ifd := uint64(order.Uint32(tiff[4:8]))
	if ifd < tiffHeaderSize || ifd+tiffEntryCountSize > size {
		return 0, false
	}
	count := uint64(order.Uint16(tiff[ifd : ifd+tiffEntryCountSize]))
	for index := uint64(0); index < count; index++ {
		entry := ifd + tiffEntryCountSize + index*tiffEntrySize
		if entry+tiffEntrySize > size {
			return 0, false
		}
		field := tiff[entry : entry+tiffEntrySize]
		if order.Uint16(field[0:2]) != tagOrientation {
			continue
		}
		if order.Uint32(field[4:8]) != 1 {
			return 0, false
		}
		var value uint32
		switch order.Uint16(field[2:4]) {
		case tiffTypeShort:
			value = uint32(order.Uint16(field[8:10]))
		case tiffTypeLong:
			value = order.Uint32(field[8:12])
		default:
			return 0, false
		}
		orientation := Orientation(value)
		if value > 0xff || !orientation.Valid() {
			return 0, false
		}
		return orientation, true
	}
	return 0, false
}

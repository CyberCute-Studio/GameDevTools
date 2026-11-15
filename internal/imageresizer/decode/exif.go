package decode

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
)

// JPEG markers the header scan distinguishes.
const (
	markerSOI  = 0xd8
	markerEOI  = 0xd9
	markerSOS  = 0xda
	markerSOF0 = 0xc0
	markerSOF2 = 0xc2
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

// jpegFrame is the first SOF0, SOF1, or SOF2 frame header — the frames the
// standard library decoder implements — as far as it sets the decoder's
// pixel memory.
type jpegFrame struct {
	valid       bool
	progressive bool
	// mcuWidth and mcuHeight are the pixel size of one minimum coded unit,
	// set by the first component's sampling factors; the decoder allocates
	// every plane in whole units. samples sums each component's horizontal ×
	// vertical sampling factors: the 8 × 8 blocks one unit holds.
	mcuWidth, mcuHeight int
	samples             int
}

// scanJPEG reads the JPEG marker segments that precede the first scan. It
// returns the IFD0 orientation of the first EXIF APP1 segment and the first
// SOF0, SOF1, or SOF2 frame header. Stray bytes between segments are skipped
// exactly as the standard library decoder skips them, so a file the decoder
// accepts never loses its orientation here. Orientation is advisory
// metadata: a missing, malformed, or out-of-range value yields
// OrientationNormal rather than rejecting a source whose image data still
// decodes, matching how image viewers treat such files. A damaged header
// ends the scan early; the decoder reports the damage itself.
func scanJPEG(reader *bufio.Reader) (Orientation, jpegFrame) {
	orientation := OrientationNormal
	var frame jpegFrame
	var soi [2]byte
	if _, err := io.ReadFull(reader, soi[:]); err != nil || soi[0] != 0xff || soi[1] != markerSOI {
		return orientation, frame
	}
	sawEXIF := false
	for !sawEXIF || !frame.valid {
		marker, ok := nextMarker(reader)
		if !ok || marker == markerEOI || marker == markerSOS {
			// EXIF metadata and the frame header precede the image data.
			return orientation, frame
		}
		if marker >= markerRST0 && marker <= markerRST7 {
			// Restart markers carry no length.
			continue
		}
		var length [2]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return orientation, frame
		}
		payload := int(binary.BigEndian.Uint16(length[:])) - 2
		if payload < 0 {
			return orientation, frame
		}
		wanted := (marker >= markerSOF0 && marker <= markerSOF2 && !frame.valid) ||
			(marker == markerAPP1 && !sawEXIF && payload >= len(exifHeader))
		if !wanted {
			if _, err := reader.Discard(payload); err != nil {
				return orientation, frame
			}
			continue
		}
		// The 16-bit segment length bounds this allocation at 64 KiB.
		segment := make([]byte, payload)
		if _, err := io.ReadFull(reader, segment); err != nil {
			return orientation, frame
		}
		if marker != markerAPP1 {
			frame = parseFrame(marker, segment)
			continue
		}
		if !bytes.HasPrefix(segment, exifHeader) {
			continue
		}
		// The first EXIF segment decides, even when it is malformed.
		sawEXIF = true
		if parsed, ok := parseTIFFOrientation(segment[len(exifHeader):]); ok {
			orientation = parsed
		}
	}
	return orientation, frame
}

// nextMarker reads through the next marker code the way the standard library
// decoder does: bytes that do not start a marker, and 0xFF 0x00 pairs, are
// extraneous data and skipped, and 0xFF fill bytes before a marker code are
// ignored.
func nextMarker(reader *bufio.Reader) (byte, bool) {
	var pair [2]byte
	for {
		if _, err := io.ReadFull(reader, pair[:]); err != nil {
			return 0, false
		}
		for pair[0] != 0xff {
			next, err := reader.ReadByte()
			if err != nil {
				return 0, false
			}
			pair[0], pair[1] = pair[1], next
		}
		marker := pair[1]
		if marker == 0 {
			continue
		}
		for marker == 0xff {
			next, err := reader.ReadByte()
			if err != nil {
				return 0, false
			}
			marker = next
		}
		return marker, true
	}
}

// parseFrame reads a frame header: precision (1 byte), height and width (2
// each), the component count (1), then three bytes per component —
// identifier, sampling factors, and quantization table. A frame the decoder
// would reject reports valid == false; the decoder then fails on it itself.
func parseFrame(marker byte, segment []byte) jpegFrame {
	if len(segment) < 6 {
		return jpegFrame{}
	}
	components := int(segment[5])
	if (components != 1 && components != 3 && components != 4) || len(segment) < 6+3*components {
		return jpegFrame{}
	}
	frame := jpegFrame{valid: true, progressive: marker == markerSOF2}
	if components == 1 {
		// The decoder treats a single component as 1 × 1 sampled, whatever
		// factors it declares.
		frame.mcuWidth, frame.mcuHeight, frame.samples = 8, 8, 1
		return frame
	}
	for index := 0; index < components; index++ {
		factors := segment[7+3*index]
		horizontal, vertical := int(factors>>4), int(factors&0x0f)
		if horizontal < 1 || horizontal > 4 || vertical < 1 || vertical > 4 {
			return jpegFrame{}
		}
		if index == 0 {
			frame.mcuWidth, frame.mcuHeight = 8*horizontal, 8*vertical
		}
		frame.samples += horizontal * vertical
	}
	return frame
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

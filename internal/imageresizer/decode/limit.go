package decode

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

// PNG color types the memory estimate distinguishes; truecolor and gray with
// alpha decode like truecolor with alpha, to 4 or 8 bytes per pixel.
const (
	pngColorGray      = 0
	pngColorPaletted  = 3
	pngColorTrueAlpha = 6
)

// maxPNGChunkLength is the largest chunk length the PNG decoder accepts.
const maxPNGChunkLength = 0x7fffffff

// pngLayout is what the chunks before the image data declare, as far as they
// set the decoder's pixel memory.
type pngLayout struct {
	valid         bool
	width, height int
	depth         byte
	colorType     byte
	interlaced    bool
	// transparent reports a tRNS chunk, which makes the decoder allocate a
	// gray or truecolor source as an image with alpha. It is also set when
	// the scan ended before the image data, so a chunk it did not reach is
	// costed as one.
	transparent bool
}

// scanPNG walks the chunks before the first IDAT the way the decoder reads
// them, each by its declared length. It reads the IHDR layout wherever the
// decoder finds it — the decoder skips unknown chunks before IHDR — and
// notes a tRNS chunk, which the decoder accepts only before the image data.
// A missing or malformed IHDR reports valid == false; the decoder then
// reports the damage itself.
func scanPNG(reader *bufio.Reader) pngLayout {
	var layout pngLayout
	if _, err := reader.Discard(len(pngSignature)); err != nil {
		return layout
	}
	for {
		// Chunk length (4) and type (4).
		var chunk [8]byte
		if _, err := io.ReadFull(reader, chunk[:]); err != nil {
			break
		}
		length := binary.BigEndian.Uint32(chunk[0:4])
		switch string(chunk[4:8]) {
		case "IDAT":
			return layout
		case "IHDR":
			if layout.valid || length != 13 {
				// The decoder rejects a second or malformed IHDR.
				layout.transparent = true
				return layout
			}
			var data [13]byte
			if _, err := io.ReadFull(reader, data[:]); err != nil {
				return pngLayout{}
			}
			width, height := binary.BigEndian.Uint32(data[0:4]), binary.BigEndian.Uint32(data[4:8])
			// The decoder rejects dimensions that are not positive 32-bit
			// integers as damaged.
			if width == 0 || height == 0 || width > math.MaxInt32 || height > math.MaxInt32 {
				return pngLayout{}
			}
			layout = pngLayout{valid: true, width: int(width), height: int(height), depth: data[8], colorType: data[9], interlaced: data[12] == 1}
			// The chunk's CRC.
			length = 0
		case "tRNS":
			layout.transparent = true
		}
		if length > maxPNGChunkLength {
			break
		}
		if _, err := reader.Discard(int(length)); err != nil {
			break
		}
		if _, err := reader.Discard(4); err != nil {
			break
		}
	}
	// The scan ended before the image data, so a transparency chunk it did
	// not reach is assumed.
	layout.transparent = true
	return layout
}

// pixelBytes is the size of one pixel of the image the PNG decoder allocates:
// one byte for a palette and for gray of up to 8 bits, two for 16-bit gray,
// and otherwise — truecolor, gray with alpha, or gray made transparent by a
// tRNS chunk — 4 bytes, or 8 at 16 bits per channel.
func (l pngLayout) pixelBytes() uint64 {
	switch {
	case l.colorType == pngColorPaletted:
		return 1
	case l.colorType == pngColorGray && !l.transparent && l.depth == 16:
		return 2
	case l.colorType == pngColorGray && !l.transparent:
		return 1
	case l.depth == 16:
		return 8
	}
	return 4
}

// pngDecodeBytes estimates the pixel memory the PNG decoder allocates for the
// layout: the image at the decoder's pixel size for its color type, and for
// an Adam7-interlaced source the seven pass images as well, which together
// hold as many pixels as the image again. The estimate saturates instead of
// overflowing.
func pngDecodeBytes(layout pngLayout) uint64 {
	perPixel := layout.pixelBytes()
	if layout.interlaced {
		perPixel *= 2
	}
	pixels := uint64(layout.width) * uint64(layout.height)
	if pixels > math.MaxUint64/perPixel {
		return math.MaxUint64
	}
	return pixels * perPixel
}

// jpegDecodeBytes estimates the pixel memory the JPEG decoder allocates. The
// decoder stores each component as a plane of one byte per sample, padded to
// whole minimum coded units; a progressive frame also keeps 64 four-byte
// coefficients per 8 × 8 block of every plane until its last scan; and an
// RGB or CMYK frame is converted into a new image of 4 bytes per pixel. The
// color model reflects only the segments before the first scan, while the
// decoder decides on the RGB conversion from every segment it reads, so
// decodeSource settles that conversion for a source it could push over the
// limit.
func jpegDecodeBytes(config image.Config, frame jpegFrame) uint64 {
	if !frame.valid {
		// The decoder parsed a frame the scan did not reach; assume the
		// costliest layout: progressive, every component at full resolution.
		frame = jpegFrame{valid: true, progressive: true, mcuWidth: 8, mcuHeight: 8, samples: componentCount(config.ColorModel)}
	}
	width, height := uint64(config.Width), uint64(config.Height)
	columns := (width + uint64(frame.mcuWidth) - 1) / uint64(frame.mcuWidth)
	rows := (height + uint64(frame.mcuHeight) - 1) / uint64(frame.mcuHeight)
	planes := columns * rows * 64 * uint64(frame.samples)
	total := planes
	if frame.progressive {
		total += 4 * planes
	}
	if config.ColorModel != color.YCbCrModel && config.ColorModel != color.GrayModel {
		total += rgbaBytes(config)
	}
	return total
}

// rgbaBytes is the size of a 4-byte-per-pixel image of the configured
// dimensions: a JPEG frame's RGB or CMYK conversion, or the upright copy of a
// reoriented source.
func rgbaBytes(config image.Config) uint64 {
	return 4 * uint64(config.Width) * uint64(config.Height)
}

// uprightCopyBytes is the image Decode copies a reoriented source into while
// the decoded one is still alive.
func uprightCopyBytes(config image.Config, orientation Orientation) uint64 {
	if !orientation.Reorients() {
		return 0
	}
	return rgbaBytes(config)
}

// componentCount is the number of components the JPEG decoder reports
// through its color model.
func componentCount(model color.Model) int {
	switch model {
	case color.GrayModel:
		return 1
	case color.CMYKModel:
		return 4
	}
	return 3
}

// decodeBytes estimates the pixel memory the decoder needs from the header
// it parsed and what the scan learned before it. A PNG whose IHDR the scan
// could not read — which the decoder then rejects too — is costed at the
// costliest layout: interlaced, 16 bits per channel, with alpha.
func decodeBytes(format Format, config image.Config, scanned header) uint64 {
	if format == FormatJPEG {
		return jpegDecodeBytes(config, scanned.jpeg)
	}
	layout := scanned.png
	if !layout.valid {
		layout = pngLayout{valid: true, width: config.Width, height: config.Height, depth: 16, colorType: pngColorTrueAlpha, interlaced: true}
	}
	return pngDecodeBytes(layout)
}

// checkLimit rejects a source whose decoding would need more than
// MaxDecodedBytes of pixel memory. Width and height are the upright
// dimensions the error reports.
func checkLimit(format Format, width, height int, need uint64) error {
	if need <= MaxDecodedBytes {
		return nil
	}
	return &Error{
		Kind:   KindTooLarge,
		Format: format,
		Width:  width,
		Height: height,
		Err:    fmt.Errorf("%d × %d pixels need more than the %d MiB decoding limit", width, height, MaxDecodedBytes>>20),
	}
}

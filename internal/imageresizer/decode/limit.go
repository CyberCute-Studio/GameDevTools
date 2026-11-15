package decode

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

// pngLayout is the image layout an IHDR chunk declares.
type pngLayout struct {
	valid         bool
	width, height int
	// deep reports 16 bits per channel.
	deep       bool
	interlaced bool
}

// scanPNG reads the IHDR chunk directly after the signature, where the
// format requires it. A missing or malformed chunk reports valid == false;
// the decoder then reports the damage itself.
func scanPNG(reader io.Reader) pngLayout {
	// Signature (8), chunk length (4), chunk type (4), IHDR data (13).
	var head [8 + 8 + 13]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return pngLayout{}
	}
	chunk := head[8:]
	if binary.BigEndian.Uint32(chunk[0:4]) != 13 || string(chunk[4:8]) != "IHDR" {
		return pngLayout{}
	}
	data := chunk[8:]
	width, height := binary.BigEndian.Uint32(data[0:4]), binary.BigEndian.Uint32(data[4:8])
	// The decoder rejects dimensions that are not positive 32-bit integers
	// as damaged.
	if width == 0 || height == 0 || width > math.MaxInt32 || height > math.MaxInt32 {
		return pngLayout{}
	}
	return pngLayout{valid: true, width: int(width), height: int(height), deep: data[8] == 16, interlaced: data[12] == 1}
}

// pngDecodeBytes estimates the pixel memory the PNG decoder allocates: the
// image at 4 bytes per pixel, or 8 at 16 bits per channel, and for an
// Adam7-interlaced source the seven pass images as well, which together hold
// as many pixels as the image again. The estimate saturates instead of
// overflowing.
func pngDecodeBytes(width, height int, deep, interlaced bool) uint64 {
	perPixel := uint64(4)
	if deep {
		perPixel = 8
	}
	if interlaced {
		perPixel *= 2
	}
	pixels := uint64(width) * uint64(height)
	if pixels > math.MaxUint64/perPixel {
		return math.MaxUint64
	}
	return pixels * perPixel
}

// jpegDecodeBytes estimates the pixel memory the JPEG decoder allocates, plus
// the upright copy a reoriented source needs. The decoder stores each
// component as a plane of one byte per sample, padded to whole minimum coded
// units; a progressive frame also keeps 64 four-byte coefficients per 8 × 8
// block of every plane until its last scan; and an RGB or CMYK frame is
// converted into a new image of 4 bytes per pixel. A reoriented source is
// copied into another 4-byte-per-pixel image while the decoded one is alive.
// That copy is counted whenever the orientation calls for it — by Inspect
// too — so a source Inspect accepts is never rejected by Decode.
func jpegDecodeBytes(config image.Config, frame jpegFrame, orientation Orientation) uint64 {
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
	pixels := width * height
	if config.ColorModel != color.YCbCrModel && config.ColorModel != color.GrayModel {
		total += 4 * pixels
	}
	if orientation.Valid() && orientation != OrientationNormal {
		total += 4 * pixels
	}
	return total
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

// decodeBytes estimates the pixel memory decoding needs from the header the
// decoder parsed and what the scan learned before it. A PNG whose IHDR the
// scan could not read is assumed to be interlaced.
func decodeBytes(format Format, config image.Config, scanned header) uint64 {
	if format == FormatJPEG {
		return jpegDecodeBytes(config, scanned.jpeg, scanned.orientation)
	}
	deep := false
	switch config.ColorModel {
	case color.RGBA64Model, color.NRGBA64Model, color.Gray16Model, color.Alpha16Model:
		deep = true
	}
	interlaced := !scanned.png.valid || scanned.png.interlaced
	return pngDecodeBytes(config.Width, config.Height, deep, interlaced)
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

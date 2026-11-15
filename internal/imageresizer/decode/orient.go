package decode

import (
	"image"
	"image/color"
)

// Orientation is an EXIF orientation value: how the stored pixel rows and
// columns map onto the upright picture.
type Orientation uint8

const (
	// OrientationNormal stores the picture upright.
	OrientationNormal Orientation = 1
	// OrientationMirrorHorizontal stores the picture mirrored left to right.
	OrientationMirrorHorizontal Orientation = 2
	// OrientationRotate180 stores the picture upside down.
	OrientationRotate180 Orientation = 3
	// OrientationMirrorVertical stores the picture mirrored top to bottom.
	OrientationMirrorVertical Orientation = 4
	// OrientationTranspose stores the picture mirrored across its main
	// diagonal: upright after a 90° clockwise rotation and a horizontal
	// mirror.
	OrientationTranspose Orientation = 5
	// OrientationRotate90 needs a 90° clockwise rotation to be upright.
	OrientationRotate90 Orientation = 6
	// OrientationTransverse stores the picture mirrored across its
	// anti-diagonal: upright after a 90° counterclockwise rotation and a
	// horizontal mirror.
	OrientationTransverse Orientation = 7
	// OrientationRotate270 needs a 90° counterclockwise rotation to be
	// upright.
	OrientationRotate270 Orientation = 8
)

// Valid reports whether o is one of the eight standard EXIF orientations.
func (o Orientation) Valid() bool {
	return o >= OrientationNormal && o <= OrientationRotate270
}

// SwapsAxes reports whether making the picture upright exchanges its width
// and height.
func (o Orientation) SwapsAxes() bool {
	return o >= OrientationTranspose && o <= OrientationRotate270
}

// upright returns the upright dimensions of a stored width × height image.
func (o Orientation) upright(width, height int) (int, int) {
	if o.SwapsAxes() {
		return height, width
	}
	return width, height
}

// target maps the stored pixel (x, y) of a width × height image to its
// position in the upright picture. EXIF defines each value by where the
// stored 0th row and 0th column land: value 6, for example, puts the 0th
// row on the visual right-hand side and the 0th column at the visual top.
func (o Orientation) target(x, y, width, height int) (int, int) {
	switch o {
	case OrientationMirrorHorizontal:
		return width - 1 - x, y
	case OrientationRotate180:
		return width - 1 - x, height - 1 - y
	case OrientationMirrorVertical:
		return x, height - 1 - y
	case OrientationTranspose:
		return y, x
	case OrientationRotate90:
		return height - 1 - y, x
	case OrientationTransverse:
		return height - 1 - y, width - 1 - x
	case OrientationRotate270:
		return y, width - 1 - x
	}
	return x, y
}

// orient returns src made upright. OrientationNormal returns src unchanged;
// every other orientation produces a new *image.RGBA in one pass — opaque,
// since only JPEG sources are reoriented — so
// a reoriented source needs one extra 4-byte-per-pixel buffer rather than
// an intermediate conversion as well. The JPEG decoder produces YCbCr,
// Gray, CMYK, or RGBA images, each of which has a direct pixel reader; any
// other type falls back to the generic color conversion.
func orient(src image.Image, o Orientation) image.Image {
	if !o.Valid() || o == OrientationNormal {
		return src
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	uprightWidth, uprightHeight := o.upright(width, height)
	dst := image.NewRGBA(image.Rect(0, 0, uprightWidth, uprightHeight))
	read := pixelReader(src)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, a := read(bounds.Min.X+x, bounds.Min.Y+y)
			tx, ty := o.target(x, y, width, height)
			offset := ty*dst.Stride + tx*4
			pixel := dst.Pix[offset : offset+4 : offset+4]
			pixel[0], pixel[1], pixel[2], pixel[3] = r, g, b, a
		}
	}
	return dst
}

// pixelReader returns a function that reads one pixel of src as
// premultiplied 8-bit RGBA, avoiding the per-pixel interface allocation of
// image.Image.At for the decoder's concrete types.
func pixelReader(src image.Image) func(x, y int) (r, g, b, a uint8) {
	switch typed := src.(type) {
	case *image.YCbCr:
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			yi := typed.YOffset(x, y)
			ci := typed.COffset(x, y)
			r, g, b := color.YCbCrToRGB(typed.Y[yi], typed.Cb[ci], typed.Cr[ci])
			return r, g, b, 0xff
		}
	case *image.Gray:
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			value := typed.Pix[typed.PixOffset(x, y)]
			return value, value, value, 0xff
		}
	case *image.CMYK:
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			offset := typed.PixOffset(x, y)
			pixel := typed.Pix[offset : offset+4 : offset+4]
			r, g, b := color.CMYKToRGB(pixel[0], pixel[1], pixel[2], pixel[3])
			return r, g, b, 0xff
		}
	case *image.RGBA:
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			offset := typed.PixOffset(x, y)
			pixel := typed.Pix[offset : offset+4 : offset+4]
			return pixel[0], pixel[1], pixel[2], pixel[3]
		}
	}
	return func(x, y int) (uint8, uint8, uint8, uint8) {
		pixel := color.RGBAModel.Convert(src.At(x, y)).(color.RGBA)
		return pixel.R, pixel.G, pixel.B, pixel.A
	}
}

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

// Reorients reports whether making the picture upright moves its pixels:
// o is one of the standard orientations other than OrientationNormal. The
// zero value and out-of-range values leave the picture as stored.
func (o Orientation) Reorients() bool {
	return o.Valid() && o != OrientationNormal
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

// steps returns where the stored pixel (0, 0) of a width × height image
// lands in an upright RGBA buffer with the given stride, and how far the
// destination moves for each stored column (across) and each stored row
// (down). Every orientation's target is linear in x and y, so these three
// offsets place every pixel.
func (o Orientation) steps(width, height, stride int) (start, across, down int) {
	offset := func(x, y int) int {
		tx, ty := o.target(x, y, width, height)
		return ty*stride + 4*tx
	}
	start = offset(0, 0)
	return start, offset(1, 0) - start, offset(0, 1) - start
}

// orient returns src made upright. A source that needs no reorientation is
// returned unchanged; every other orientation produces a new *image.RGBA in
// one pass — opaque, since only JPEG sources are reoriented — so a
// reoriented source needs one extra 4-byte-per-pixel buffer rather than an
// intermediate conversion as well. Each stored row is converted once into a
// small row buffer and then written out with the orientation's fixed
// destination steps, so the per-pixel work is a 4-byte copy.
func orient(src image.Image, o Orientation) image.Image {
	if !o.Reorients() {
		return src
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	uprightWidth, uprightHeight := o.upright(width, height)
	dst := image.NewRGBA(image.Rect(0, 0, uprightWidth, uprightHeight))
	start, across, down := o.steps(width, height, dst.Stride)
	row := make([]uint8, 4*width)
	for y := 0; y < height; y++ {
		readRow(src, bounds.Min.X, bounds.Min.Y+y, row)
		offset := start + y*down
		for x := 0; x < len(row); x += 4 {
			pixel := dst.Pix[offset : offset+4 : offset+4]
			pixel[0], pixel[1], pixel[2], pixel[3] = row[x], row[x+1], row[x+2], row[x+3]
			offset += across
		}
	}
	return dst
}

// readRow converts the row of src at y, starting at column minX, into
// premultiplied 8-bit RGBA, filling row. The JPEG decoder produces YCbCr,
// Gray, CMYK, or RGBA images, each of which is read directly rather than
// through the per-pixel interface allocation of image.Image.At; any other
// type falls back to the generic color conversion.
func readRow(src image.Image, minX, y int, row []uint8) {
	width := len(row) / 4
	switch typed := src.(type) {
	case *image.YCbCr:
		for index := range width {
			yi := typed.YOffset(minX+index, y)
			ci := typed.COffset(minX+index, y)
			r, g, b := color.YCbCrToRGB(typed.Y[yi], typed.Cb[ci], typed.Cr[ci])
			pixel := row[4*index : 4*index+4 : 4*index+4]
			pixel[0], pixel[1], pixel[2], pixel[3] = r, g, b, 0xff
		}
	case *image.Gray:
		values := typed.Pix[typed.PixOffset(minX, y):]
		for index, value := range values[:width] {
			pixel := row[4*index : 4*index+4 : 4*index+4]
			pixel[0], pixel[1], pixel[2], pixel[3] = value, value, value, 0xff
		}
	case *image.CMYK:
		values := typed.Pix[typed.PixOffset(minX, y):]
		for index := range width {
			ink := values[4*index : 4*index+4 : 4*index+4]
			r, g, b := color.CMYKToRGB(ink[0], ink[1], ink[2], ink[3])
			pixel := row[4*index : 4*index+4 : 4*index+4]
			pixel[0], pixel[1], pixel[2], pixel[3] = r, g, b, 0xff
		}
	case *image.RGBA:
		offset := typed.PixOffset(minX, y)
		copy(row, typed.Pix[offset:offset+len(row)])
	default:
		for index := range width {
			value := color.RGBAModel.Convert(src.At(minX+index, y)).(color.RGBA)
			pixel := row[4*index : 4*index+4 : 4*index+4]
			pixel[0], pixel[1], pixel[2], pixel[3] = value.R, value.G, value.B, value.A
		}
	}
}

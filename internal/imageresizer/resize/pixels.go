package resize

import (
	"image"
	"image/color"
)

// layout is the channels the filter carries per pixel, which is also the
// number of int32 samples per pixel in every working row. Samples are
// 16-bit, 0 to 0xffff, and color is premultiplied by alpha, as in Go's
// color.Color.RGBA.
type layout int

const (
	// layoutGray carries one gray sample of an opaque grayscale source.
	layoutGray layout = 1
	// layoutRGB carries red, green, and blue of an opaque color source.
	layoutRGB layout = 3
	// layoutRGBA carries premultiplied red, green, and blue, and alpha.
	layoutRGBA layout = 4
)

// source reads rows of the image being resized into working samples.
type source struct {
	layout layout
	// deep reports whether the source has 16 bits per channel, which the
	// output keeps.
	deep bool
	// fetch writes the samples of source pixels [x0, x1) of row y, both
	// relative to the source bounds, to dst.
	fetch func(dst []int32, y, x0, x1 int)
}

// newSource chooses the pixel layout and a row reader for the image. The
// standard library's image types are read directly; others through At.
func newSource(img image.Image) source {
	origin := img.Bounds().Min
	switch img := img.(type) {
	case *image.Gray:
		return source{layout: layoutGray, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				dst[i] = int32(row[i]) * 0x101
			}
		}}
	case *image.Gray16:
		return source{layout: layoutGray, deep: true, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				dst[i] = int32(row[2*i])<<8 | int32(row[2*i+1])
			}
		}}
	case *image.RGBA:
		if img.Opaque() {
			return source{layout: layoutRGB, fetch: func(dst []int32, y, x0, x1 int) {
				row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
				for i := range x1 - x0 {
					p, d := row[4*i:4*i+3], dst[3*i:3*i+3]
					d[0], d[1], d[2] = int32(p[0])*0x101, int32(p[1])*0x101, int32(p[2])*0x101
				}
			}}
		}
		return source{layout: layoutRGBA, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range 4 * (x1 - x0) {
				dst[i] = int32(row[i]) * 0x101
			}
		}}
	case *image.NRGBA:
		if img.Opaque() {
			return source{layout: layoutRGB, fetch: func(dst []int32, y, x0, x1 int) {
				row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
				for i := range x1 - x0 {
					p, d := row[4*i:4*i+3], dst[3*i:3*i+3]
					d[0], d[1], d[2] = int32(p[0])*0x101, int32(p[1])*0x101, int32(p[2])*0x101
				}
			}}
		}
		return source{layout: layoutRGBA, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				p, d := row[4*i:4*i+4], dst[4*i:4*i+4]
				d[0], d[1], d[2], d[3] = premultiply8(p[0], p[3]), premultiply8(p[1], p[3]), premultiply8(p[2], p[3]), int32(p[3])*0x101
			}
		}}
	case *image.RGBA64:
		if img.Opaque() {
			return source{layout: layoutRGB, deep: true, fetch: func(dst []int32, y, x0, x1 int) {
				row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
				for i := range x1 - x0 {
					p, d := row[8*i:8*i+6], dst[3*i:3*i+3]
					d[0], d[1], d[2] = sample16(p[0:2]), sample16(p[2:4]), sample16(p[4:6])
				}
			}}
		}
		return source{layout: layoutRGBA, deep: true, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range 4 * (x1 - x0) {
				dst[i] = sample16(row[2*i : 2*i+2])
			}
		}}
	case *image.NRGBA64:
		if img.Opaque() {
			return source{layout: layoutRGB, deep: true, fetch: func(dst []int32, y, x0, x1 int) {
				row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
				for i := range x1 - x0 {
					p, d := row[8*i:8*i+6], dst[3*i:3*i+3]
					d[0], d[1], d[2] = sample16(p[0:2]), sample16(p[2:4]), sample16(p[4:6])
				}
			}}
		}
		return source{layout: layoutRGBA, deep: true, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				p, d := row[8*i:8*i+8], dst[4*i:4*i+4]
				alpha := sample16(p[6:8])
				d[0], d[1], d[2], d[3] = premultiply16(sample16(p[0:2]), alpha), premultiply16(sample16(p[2:4]), alpha), premultiply16(sample16(p[4:6]), alpha), alpha
			}
		}}
	case *image.YCbCr:
		return source{layout: layoutRGB, fetch: func(dst []int32, y, x0, x1 int) {
			py := origin.Y + y
			for i := range x1 - x0 {
				px := origin.X + x0 + i
				yi, ci := img.YOffset(px, py), img.COffset(px, py)
				r, g, b, _ := color.YCbCr{Y: img.Y[yi], Cb: img.Cb[ci], Cr: img.Cr[ci]}.RGBA()
				d := dst[3*i : 3*i+3]
				d[0], d[1], d[2] = int32(r), int32(g), int32(b)
			}
		}}
	case *image.CMYK:
		return source{layout: layoutRGB, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				p := row[4*i : 4*i+4]
				r, g, b, _ := color.CMYK{C: p[0], M: p[1], Y: p[2], K: p[3]}.RGBA()
				d := dst[3*i : 3*i+3]
				d[0], d[1], d[2] = int32(r), int32(g), int32(b)
			}
		}}
	case *image.Paletted:
		// Every index reads its entry; an index past the end of the palette
		// reads transparent black rather than failing.
		var table [256][4]int32
		for index, entry := range img.Palette[:min(len(img.Palette), len(table))] {
			table[index][0], table[index][1], table[index][2], table[index][3] = premultiplied(entry)
		}
		channels := layoutRGBA
		if img.Opaque() {
			channels = layoutRGB
		}
		return source{layout: channels, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			n := int(channels)
			for i := range x1 - x0 {
				copy(dst[n*i:n*i+n], table[row[i]][:n])
			}
		}}
	}
	return genericSource(img, origin)
}

// genericSource reads an image of any type through At. Its layout follows
// the color model: a grayscale model is filtered as gray, a 16-bit model
// keeps 16 bits in the output, and an image reporting itself opaque drops
// alpha, as do the always-opaque YCbCr and CMYK models.
func genericSource(img image.Image, origin image.Point) source {
	model := img.ColorModel()
	if model == color.GrayModel || model == color.Gray16Model {
		return source{layout: layoutGray, deep: model == color.Gray16Model, fetch: func(dst []int32, y, x0, x1 int) {
			for i := range x1 - x0 {
				dst[i] = int32(color.Gray16Model.Convert(img.At(origin.X+x0+i, origin.Y+y)).(color.Gray16).Y)
			}
		}}
	}
	deep := model == color.RGBA64Model || model == color.NRGBA64Model || model == color.Alpha16Model
	opaque := model == color.YCbCrModel || model == color.CMYKModel
	if reporter, ok := img.(interface{ Opaque() bool }); ok && !opaque {
		opaque = reporter.Opaque()
	}
	channels := layoutRGBA
	if opaque {
		channels = layoutRGB
	}
	return source{layout: channels, deep: deep, fetch: func(dst []int32, y, x0, x1 int) {
		n := int(channels)
		for i := range x1 - x0 {
			var pixel [4]int32
			pixel[0], pixel[1], pixel[2], pixel[3] = premultiplied(img.At(origin.X+x0+i, origin.Y+y))
			copy(dst[n*i:n*i+n], pixel[:n])
		}
	}}
}

// premultiplied returns a color's 16-bit premultiplied channels. Straight
// 8-bit and 16-bit colors are premultiplied with rounding, as the image
// fast paths do, rather than with the truncation of their RGBA methods, so
// an 8-bit pixel survives the round trip through the filter unchanged.
func premultiplied(c color.Color) (r, g, b, a int32) {
	switch c := c.(type) {
	case color.NRGBA:
		return premultiply8(c.R, c.A), premultiply8(c.G, c.A), premultiply8(c.B, c.A), int32(c.A) * 0x101
	case color.NRGBA64:
		alpha := int32(c.A)
		return premultiply16(int32(c.R), alpha), premultiply16(int32(c.G), alpha), premultiply16(int32(c.B), alpha), alpha
	}
	r32, g32, b32, a32 := c.RGBA()
	return int32(r32), int32(g32), int32(b32), int32(a32)
}

// premultiply8 returns the 16-bit premultiplied value of an 8-bit straight
// channel under 8-bit alpha: value·0x101 · alpha·0x101 / 0xffff, rounded.
// 0xffff = 0xff·0x101 and 0xff is odd, so the division never meets a tie.
func premultiply8(value, alpha uint8) int32 {
	return (int32(value)*int32(alpha)*0x101 + 0x7f) / 0xff
}

// premultiply16 returns the premultiplied value of a 16-bit straight channel
// under 16-bit alpha, rounded.
func premultiply16(value, alpha int32) int32 {
	return int32((uint64(value)*uint64(alpha) + 0x7fff) / 0xffff)
}

// sample16 reads a big-endian 16-bit sample.
func sample16(p []uint8) int32 {
	return int32(p[0])<<8 | int32(p[1])
}

// sink writes finished output rows to the output image.
type sink struct {
	img image.Image
	// store writes the clamped, un-premultiplied pixels of output row y
	// from its 16-bit premultiplied samples.
	store func(row []int32, y int)
}

// newSink allocates the width × height output for the source's layout and
// depth.
func newSink(src source, width, height int) sink {
	bounds := image.Rect(0, 0, width, height)
	switch {
	case src.layout == layoutGray && !src.deep:
		img := image.NewGray(bounds)
		return sink{img: img, store: func(row []int32, y int) {
			pix := img.Pix[y*img.Stride : y*img.Stride+width]
			for i := range pix {
				pix[i] = to8(clamp16(row[i]))
			}
		}}
	case src.layout == layoutGray:
		img := image.NewGray16(bounds)
		return sink{img: img, store: func(row []int32, y int) {
			pix := img.Pix[y*img.Stride : y*img.Stride+2*width]
			for i := range width {
				put16(pix[2*i:2*i+2], clamp16(row[i]))
			}
		}}
	case src.layout == layoutRGB && !src.deep:
		img := image.NewRGBA(bounds)
		return sink{img: img, store: func(row []int32, y int) {
			pix := img.Pix[y*img.Stride : y*img.Stride+4*width]
			for i := range width {
				p, s := pix[4*i:4*i+4], row[3*i:3*i+3]
				p[0], p[1], p[2], p[3] = to8(clamp16(s[0])), to8(clamp16(s[1])), to8(clamp16(s[2])), 0xff
			}
		}}
	case src.layout == layoutRGB:
		img := image.NewRGBA64(bounds)
		return sink{img: img, store: func(row []int32, y int) {
			pix := img.Pix[y*img.Stride : y*img.Stride+8*width]
			for i := range width {
				p, s := pix[8*i:8*i+8], row[3*i:3*i+3]
				put16(p[0:2], clamp16(s[0]))
				put16(p[2:4], clamp16(s[1]))
				put16(p[4:6], clamp16(s[2]))
				p[6], p[7] = 0xff, 0xff
			}
		}}
	case !src.deep:
		img := image.NewNRGBA(bounds)
		return sink{img: img, store: func(row []int32, y int) {
			pix := img.Pix[y*img.Stride : y*img.Stride+4*width]
			for i := range width {
				p, s := pix[4*i:4*i+4], row[4*i:4*i+4]
				alpha := clamp16(s[3])
				if alpha == 0 {
					p[0], p[1], p[2], p[3] = 0, 0, 0, 0
					continue
				}
				// The filter's overshoot can leave a color above its alpha,
				// which no straight color can represent.
				p[0] = unpremultiply8(min(clamp16(s[0]), alpha), alpha)
				p[1] = unpremultiply8(min(clamp16(s[1]), alpha), alpha)
				p[2] = unpremultiply8(min(clamp16(s[2]), alpha), alpha)
				p[3] = to8(alpha)
			}
		}}
	}
	img := image.NewNRGBA64(bounds)
	return sink{img: img, store: func(row []int32, y int) {
		pix := img.Pix[y*img.Stride : y*img.Stride+8*width]
		for i := range width {
			p, s := pix[8*i:8*i+8], row[4*i:4*i+4]
			alpha := clamp16(s[3])
			if alpha == 0 {
				clear(p)
				continue
			}
			put16(p[0:2], unpremultiply16(min(clamp16(s[0]), alpha), alpha))
			put16(p[2:4], unpremultiply16(min(clamp16(s[1]), alpha), alpha))
			put16(p[4:6], unpremultiply16(min(clamp16(s[2]), alpha), alpha))
			put16(p[6:8], alpha)
		}
	}}
}

// clamp16 limits a filtered sample to the 16-bit range; the filter's
// negative lobes overshoot it near sharp edges.
func clamp16(v int32) int32 {
	return min(max(v, 0), 0xffff)
}

// to8 rounds a 16-bit sample to 8 bits: v·0xff / 0xffff, which never meets
// a tie because 0xffff is odd.
func to8(v int32) uint8 {
	return uint8((v*0xff + 0x7fff) / 0xffff)
}

// unpremultiply8 returns the 8-bit straight value of a 16-bit premultiplied
// sample under a nonzero alpha, rounded half up.
func unpremultiply8(value, alpha int32) uint8 {
	return uint8((value*2*0xff + alpha) / (2 * alpha))
}

// unpremultiply16 returns the 16-bit straight value of a 16-bit
// premultiplied sample under a nonzero alpha, rounded half up.
func unpremultiply16(value, alpha int32) int32 {
	return int32((int64(value)*2*0xffff + int64(alpha)) / (2 * int64(alpha)))
}

// put16 writes a big-endian 16-bit sample.
func put16(p []uint8, v int32) {
	p[0], p[1] = uint8(v>>8), uint8(v)
}

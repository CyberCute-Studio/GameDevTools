package resize

import (
	"encoding/binary"
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
	// fetchCost is the cost of reading one pixel into working samples,
	// relative to applying one filter weight to a pixel's samples, as
	// measured on windows/arm64: 1 to copy 8-bit samples or 16-bit gray; 2
	// to premultiply, read 16-bit color, look up a palette entry, or convert
	// CMYK; 3 to convert YCbCr; 5 to convert and premultiply NYCbCrA; and 20
	// to read through At. chooseOrder weighs it.
	fetchCost int64
	// fetch writes the samples of source pixels [x0, x1) of row y, both
	// relative to the source bounds, to dst.
	fetch func(dst []int32, y, x0, x1 int)
}

// newSource chooses the pixel layout and a row reader for the image. The
// image types the decoder returns, and *image.NYCbCrA, are read directly;
// any other type through At. read is the region of the source the filter
// reads, relative to its bounds: the layout carries alpha only when a pixel
// there is translucent, so translucency the filter never reads, such as in
// a margin Crop to Fill removes, adds no alpha channel.
func newSource(img image.Image, read image.Rectangle) source {
	origin := img.Bounds().Min
	opaque := func() bool { return opaqueWithin(img, read.Add(origin)) }
	switch img := img.(type) {
	case *image.Gray:
		return source{layout: layoutGray, fetchCost: 1, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				dst[i] = int32(row[i]) * 0x101
			}
		}}
	case *image.Gray16:
		return source{layout: layoutGray, deep: true, fetchCost: 1, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				dst[i] = sample16(row[2*i : 2*i+2])
			}
		}}
	case *image.RGBA:
		if opaque() {
			return opaque8(img, origin)
		}
		return source{layout: layoutRGBA, fetchCost: 1, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range 4 * (x1 - x0) {
				dst[i] = int32(row[i]) * 0x101
			}
		}}
	case *image.NRGBA:
		if opaque() {
			return opaque8(&image.RGBA{Pix: img.Pix, Stride: img.Stride, Rect: img.Rect}, origin)
		}
		return source{layout: layoutRGBA, fetchCost: 2, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				p, d := row[4*i:4*i+4], dst[4*i:4*i+4]
				d[0], d[1], d[2], d[3] = premultiply8(p[0], p[3]), premultiply8(p[1], p[3]), premultiply8(p[2], p[3]), int32(p[3])*0x101
			}
		}}
	case *image.RGBA64:
		if opaque() {
			return opaque16(img, origin)
		}
		return source{layout: layoutRGBA, deep: true, fetchCost: 2, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range 4 * (x1 - x0) {
				dst[i] = sample16(row[2*i : 2*i+2])
			}
		}}
	case *image.NRGBA64:
		if opaque() {
			return opaque16(&image.RGBA64{Pix: img.Pix, Stride: img.Stride, Rect: img.Rect}, origin)
		}
		return source{layout: layoutRGBA, deep: true, fetchCost: 2, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				p, d := row[8*i:8*i+8], dst[4*i:4*i+4]
				alpha := sample16(p[6:8])
				d[0], d[1], d[2], d[3] = premultiply16(sample16(p[0:2]), alpha), premultiply16(sample16(p[2:4]), alpha), premultiply16(sample16(p[4:6]), alpha), alpha
			}
		}}
	case *image.YCbCr:
		// The 8-bit conversion is the one the decoder applies when it
		// reorients a JPEG, so an upright and a reoriented picture of the
		// same pixels start from the same colors.
		return source{layout: layoutRGB, fetchCost: 3, fetch: func(dst []int32, y, x0, x1 int) {
			readYCbCr(dst, img, origin.X+x0, origin.Y+y, x1-x0, int(layoutRGB))
		}}
	case *image.NYCbCrA:
		// The color converts as a YCbCr image's does, and the alpha applies
		// as an *image.NRGBA's does.
		if opaque() {
			return source{layout: layoutRGB, fetchCost: 3, fetch: func(dst []int32, y, x0, x1 int) {
				readYCbCr(dst, &img.YCbCr, origin.X+x0, origin.Y+y, x1-x0, int(layoutRGB))
			}}
		}
		return source{layout: layoutRGBA, fetchCost: 5, fetch: func(dst []int32, y, x0, x1 int) {
			px, py := origin.X+x0, origin.Y+y
			readYCbCr(dst, &img.YCbCr, px, py, x1-x0, int(layoutRGBA))
			for i, a := range img.A[img.AOffset(px, py):][:x1-x0] {
				d := dst[4*i : 4*i+4]
				d[0], d[1], d[2], d[3] = premultiplySample(d[0], a), premultiplySample(d[1], a), premultiplySample(d[2], a), int32(a)*0x101
			}
		}}
	case *image.CMYK:
		// The 8-bit conversion matches the decoder's, as for YCbCr.
		return source{layout: layoutRGB, fetchCost: 2, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			for i := range x1 - x0 {
				p := row[4*i : 4*i+4]
				r, g, b := color.CMYKToRGB(p[0], p[1], p[2], p[3])
				d := dst[3*i : 3*i+3]
				d[0], d[1], d[2] = int32(r)*0x101, int32(g)*0x101, int32(b)*0x101
			}
		}}
	case *image.Paletted:
		// Every index reads its entry, and an index past the end of the
		// palette reads opaque black rather than failing: the color the
		// standard PNG decoder gives one, and consistent with Opaque, which
		// ignores such indices. (At panics on them instead.)
		var table [256][4]int32
		for index := range table {
			table[index][3] = 0xffff
		}
		for index, entry := range img.Palette[:min(len(img.Palette), len(table))] {
			table[index][0], table[index][1], table[index][2], table[index][3] = premultiplied(entry)
		}
		channels := layoutRGBA
		if opaque() {
			channels = layoutRGB
		}
		return source{layout: channels, fetchCost: 2, fetch: func(dst []int32, y, x0, x1 int) {
			row := img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):]
			n := int(channels)
			for i := range x1 - x0 {
				copy(dst[n*i:n*i+n], table[row[i]][:n])
			}
		}}
	}
	return genericSource(img, origin, read)
}

// opaqueWithin reports whether the pixels of img within region, in the
// image's own coordinates, are all opaque, as the Opaque method of the
// image's SubImage of the region reports it, or failing that the Opaque
// method of the whole image. An image with neither counts as translucent.
func opaqueWithin(img image.Image, region image.Rectangle) bool {
	if cropper, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		if reporter, ok := cropper.SubImage(region).(interface{ Opaque() bool }); ok {
			return reporter.Opaque()
		}
	}
	reporter, ok := img.(interface{ Opaque() bool })
	return ok && reporter.Opaque()
}

// readYCbCr writes the 8-bit RGB conversion of n pixels of row py of img,
// from column px, to dst as working samples, one pixel every stride
// samples. Along a row the luma samples are consecutive and the chroma
// index advances once per subsampled column, so the offsets are computed
// once per row rather than for every pixel, as YOffset and COffset would.
func readYCbCr(dst []int32, img *image.YCbCr, px, py, n, stride int) {
	luma := img.Y[img.YOffset(px, py):][:n]
	if img.Rect.Min.X < 0 {
		// COffset divides columns rounding toward zero, which a shift does
		// not reproduce left of zero.
		for i, l := range luma {
			c := img.COffset(px+i, py)
			r, g, b := color.YCbCrToRGB(l, img.Cb[c], img.Cr[c])
			d := dst[stride*i : stride*i+3]
			d[0], d[1], d[2] = int32(r)*0x101, int32(g)*0x101, int32(b)*0x101
		}
		return
	}
	// COffset's column term is x/h − Rect.Min.X/h for the horizontal
	// subsampling factor h, which is 1 << shift; on non-negative columns
	// the division is the shift.
	var shift uint
	switch img.SubsampleRatio {
	case image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420:
		shift = 1
	case image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410:
		shift = 2
	}
	row := img.COffset(img.Rect.Min.X, py) - img.Rect.Min.X>>shift
	for i, l := range luma {
		c := row + (px+i)>>shift
		r, g, b := color.YCbCrToRGB(l, img.Cb[c], img.Cr[c])
		d := dst[stride*i : stride*i+3]
		d[0], d[1], d[2] = int32(r)*0x101, int32(g)*0x101, int32(b)*0x101
	}
}

// opaque8 reads the color of an opaque *image.RGBA, skipping alpha. An
// opaque *image.NRGBA stores its pixels alike, so an *image.RGBA view of its
// pixels reads it too.
func opaque8(img *image.RGBA, origin image.Point) source {
	return source{layout: layoutRGB, fetchCost: 1, fetch: func(dst []int32, y, x0, x1 int) {
		readOpaque8(dst, img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):], x1-x0)
	}}
}

// readOpaque8 writes the color of the first n 8-bit RGBA pixels of row to
// dst.
func readOpaque8(dst []int32, row []uint8, n int) {
	for i := range n {
		p, d := row[4*i:4*i+3], dst[3*i:3*i+3]
		d[0], d[1], d[2] = int32(p[0])*0x101, int32(p[1])*0x101, int32(p[2])*0x101
	}
}

// opaque16 reads the color of an opaque *image.RGBA64, skipping alpha. An
// opaque *image.NRGBA64 stores its pixels alike, so an *image.RGBA64 view of
// its pixels reads it too.
func opaque16(img *image.RGBA64, origin image.Point) source {
	return source{layout: layoutRGB, deep: true, fetchCost: 2, fetch: func(dst []int32, y, x0, x1 int) {
		readOpaque16(dst, img.Pix[img.PixOffset(origin.X+x0, origin.Y+y):], x1-x0)
	}}
}

// readOpaque16 writes the color of the first n 16-bit RGBA pixels of row to
// dst.
func readOpaque16(dst []int32, row []uint8, n int) {
	for i := range n {
		p, d := row[8*i:8*i+6], dst[3*i:3*i+3]
		d[0], d[1], d[2] = sample16(p[0:2]), sample16(p[2:4]), sample16(p[4:6])
	}
}

// genericSource reads an image of any type through At. Its layout follows
// the color model: a grayscale model is filtered as gray, a 16-bit model
// keeps 16 bits in the output, and an image reporting the region the filter
// reads opaque, as opaqueWithin asks it, drops alpha, as do the
// always-opaque YCbCr and CMYK models.
func genericSource(img image.Image, origin image.Point, read image.Rectangle) source {
	model := img.ColorModel()
	if model == color.GrayModel || model == color.Gray16Model {
		return source{layout: layoutGray, deep: model == color.Gray16Model, fetchCost: 20, fetch: func(dst []int32, y, x0, x1 int) {
			for i := range x1 - x0 {
				dst[i] = int32(color.Gray16Model.Convert(img.At(origin.X+x0+i, origin.Y+y)).(color.Gray16).Y)
			}
		}}
	}
	deep := model == color.RGBA64Model || model == color.NRGBA64Model || model == color.Alpha16Model
	opaque := model == color.YCbCrModel || model == color.CMYKModel || opaqueWithin(img, read.Add(origin))
	channels := layoutRGBA
	if opaque {
		channels = layoutRGB
	}
	return source{layout: channels, deep: deep, fetchCost: 20, fetch: func(dst []int32, y, x0, x1 int) {
		n := int(channels)
		for i := range x1 - x0 {
			var pixel [4]int32
			pixel[0], pixel[1], pixel[2], pixel[3] = premultiplied(img.At(origin.X+x0+i, origin.Y+y))
			copy(dst[n*i:n*i+n], pixel[:n])
		}
	}}
}

// premultiplied returns a color's 16-bit premultiplied channels, computed as
// the image fast paths compute them. Straight 8-bit and 16-bit colors are
// premultiplied with rounding rather than with the truncation of their RGBA
// methods, so an 8-bit pixel survives the round trip through the filter
// unchanged, and YCbCr, NYCbCrA, and CMYK colors take the 8-bit conversion
// to RGB rather than the 16-bit one of their RGBA methods.
func premultiplied(c color.Color) (r, g, b, a int32) {
	switch c := c.(type) {
	case color.NRGBA:
		return premultiply8(c.R, c.A), premultiply8(c.G, c.A), premultiply8(c.B, c.A), int32(c.A) * 0x101
	case color.NRGBA64:
		alpha := int32(c.A)
		return premultiply16(int32(c.R), alpha), premultiply16(int32(c.G), alpha), premultiply16(int32(c.B), alpha), alpha
	case color.YCbCr:
		r, g, b := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
		return int32(r) * 0x101, int32(g) * 0x101, int32(b) * 0x101, 0xffff
	case color.NYCbCrA:
		r, g, b := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
		return premultiply8(r, c.A), premultiply8(g, c.A), premultiply8(b, c.A), int32(c.A) * 0x101
	case color.CMYK:
		r, g, b := color.CMYKToRGB(c.C, c.M, c.Y, c.K)
		return int32(r) * 0x101, int32(g) * 0x101, int32(b) * 0x101, 0xffff
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

// premultiplySample returns premultiply8(value, alpha) for an 8-bit channel
// already widened to the working sample value·0x101.
func premultiplySample(sample int32, alpha uint8) int32 {
	return (sample*int32(alpha) + 0x7f) / 0xff
}

// premultiply16 returns the premultiplied value of a 16-bit straight channel
// under 16-bit alpha, rounded.
func premultiply16(value, alpha int32) int32 {
	return int32((uint64(value)*uint64(alpha) + 0x7fff) / 0xffff)
}

// sample16 reads a big-endian 16-bit sample as a working sample.
func sample16(p []uint8) int32 {
	return int32(binary.BigEndian.Uint16(p))
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
				// A pixel whose alpha rounds to zero is stored as transparent
				// black, like one whose alpha is exactly zero, rather than
				// keeping a color nothing shows.
				opacity := to8(alpha)
				if opacity == 0 {
					p[0], p[1], p[2], p[3] = 0, 0, 0, 0
					continue
				}
				// The color is recovered at the 16-bit alpha's precision. The
				// filter's overshoot can leave it above its alpha, which no
				// straight color can represent.
				p[0] = unpremultiply8(min(clamp16(s[0]), alpha), alpha)
				p[1] = unpremultiply8(min(clamp16(s[1]), alpha), alpha)
				p[2] = unpremultiply8(min(clamp16(s[2]), alpha), alpha)
				p[3] = opacity
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

// put16 writes a clamped working sample as a big-endian 16-bit sample.
func put16(p []uint8, v int32) {
	binary.BigEndian.PutUint16(p, uint16(v))
}

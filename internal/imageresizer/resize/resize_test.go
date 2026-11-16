package resize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
)

// noise fills an image with seeded pseudo-random pixels, the same on every
// platform.
func noise(seed uint64, rect image.Rectangle, translucent bool) *image.NRGBA {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	img := image.NewNRGBA(rect)
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.Uint32())
		if i%4 == 3 && !translucent {
			img.Pix[i] = 0xff
		}
	}
	return img
}

// typed returns the same seeded picture as each standard image type the
// resizer reads directly.
func typed(seed uint64, width, height int) map[string]image.Image {
	rect := image.Rect(0, 0, width, height)
	translucent := noise(seed, rect, true)
	opaque := noise(seed, rect, false)
	rng := rand.New(rand.NewPCG(seed, 1))
	images := map[string]image.Image{"NRGBA": translucent, "opaque NRGBA": opaque}

	rgba := image.NewRGBA(rect)
	gray := image.NewGray(rect)
	gray16 := image.NewGray16(rect)
	rgba64 := image.NewRGBA64(rect)
	nrgba64 := image.NewNRGBA64(rect)
	opaque64 := image.NewNRGBA64(rect)
	cmyk := image.NewCMYK(rect)
	for y := range height {
		for x := range width {
			rgba.Set(x, y, translucent.At(x, y))
			gray.SetGray(x, y, color.Gray{Y: uint8(rng.Uint32())})
			gray16.SetGray16(x, y, color.Gray16{Y: uint16(rng.Uint32())})
			c := color.NRGBA64{R: uint16(rng.Uint32()), G: uint16(rng.Uint32()), B: uint16(rng.Uint32()), A: uint16(rng.Uint32())}
			nrgba64.SetNRGBA64(x, y, c)
			rgba64.Set(x, y, c)
			c.A = 0xffff
			opaque64.SetNRGBA64(x, y, c)
			cmyk.SetCMYK(x, y, color.CMYK{C: uint8(rng.Uint32()), M: uint8(rng.Uint32()), Y: uint8(rng.Uint32()), K: uint8(rng.Uint32())})
		}
	}
	images["RGBA"], images["Gray"], images["Gray16"] = rgba, gray, gray16
	images["RGBA64"], images["NRGBA64"], images["opaque NRGBA64"], images["CMYK"] = rgba64, nrgba64, opaque64, cmyk

	ratios := map[string]image.YCbCrSubsampleRatio{
		"4:4:4": image.YCbCrSubsampleRatio444, "4:2:2": image.YCbCrSubsampleRatio422, "4:2:0": image.YCbCrSubsampleRatio420,
		"4:4:0": image.YCbCrSubsampleRatio440, "4:1:1": image.YCbCrSubsampleRatio411, "4:1:0": image.YCbCrSubsampleRatio410,
	}
	for _, name := range []string{"4:4:4", "4:2:2", "4:2:0", "4:4:0", "4:1:1", "4:1:0"} {
		ycbcr := image.NewYCbCr(rect, ratios[name])
		for i := range ycbcr.Y {
			ycbcr.Y[i] = uint8(rng.Uint32())
		}
		for i := range ycbcr.Cb {
			ycbcr.Cb[i], ycbcr.Cr[i] = uint8(rng.Uint32()), uint8(rng.Uint32())
		}
		images["YCbCr "+name] = ycbcr
	}

	palette := color.Palette{}
	opaquePalette := color.Palette{}
	for i := range 256 {
		palette = append(palette, color.NRGBA{R: uint8(rng.Uint32()), G: uint8(rng.Uint32()), B: uint8(rng.Uint32()), A: uint8(i)})
		opaquePalette = append(opaquePalette, color.RGBA{R: uint8(rng.Uint32()), G: uint8(rng.Uint32()), B: uint8(rng.Uint32()), A: 0xff})
	}
	paletted := image.NewPaletted(rect, palette)
	opaquePaletted := image.NewPaletted(rect, opaquePalette)
	for i := range paletted.Pix {
		paletted.Pix[i] = uint8(rng.Uint32())
		opaquePaletted.Pix[i] = uint8(rng.Uint32())
	}
	images["Paletted"], images["opaque Paletted"] = paletted, opaquePaletted
	return images
}

// generic hides an image's concrete type, so the resizer reads it through
// At, while still reporting whether it is opaque.
type generic struct{ image.Image }

func (g generic) Opaque() bool { return g.Image.(interface{ Opaque() bool }).Opaque() }

func mustResize(t *testing.T, img image.Image, target Target) image.Image {
	t.Helper()
	out, err := Resize(context.Background(), img, target)
	if err != nil {
		t.Fatalf("Resize(%T %v, %+v): %v", img, img.Bounds(), target, err)
	}
	return out
}

func mustResample(t *testing.T, img image.Image, target Target, o order) image.Image {
	t.Helper()
	bounds := img.Bounds()
	out, err := resample(context.Background(), img, mustPlan(t, bounds.Dx(), bounds.Dy(), target), o)
	if err != nil {
		t.Fatalf("resample(%T, %+v, %d): %v", img, target, o, err)
	}
	return out
}

// pixels returns an output image's pixel bytes.
func pixels(img image.Image) []uint8 {
	switch img := img.(type) {
	case *image.Gray:
		return img.Pix
	case *image.Gray16:
		return img.Pix
	case *image.RGBA:
		return img.Pix
	case *image.RGBA64:
		return img.Pix
	case *image.NRGBA:
		return img.Pix
	case *image.NRGBA64:
		return img.Pix
	}
	panic(fmt.Sprintf("unexpected output type %T", img))
}

func sameImage(a, b image.Image) bool {
	return reflect.TypeOf(a) == reflect.TypeOf(b) && a.Bounds() == b.Bounds() && string(pixels(a)) == string(pixels(b))
}

var everyMode = []func(int, int) Target{fit, stretch, crop}

// TestResizeAcceptanceDimensions pins AC-R01 through AC-R05 on real pixels:
// the output image has exactly the planned dimensions, enlargement
// included.
func TestResizeAcceptanceDimensions(t *testing.T) {
	cases := []struct {
		width, height int
		target        Target
		want          image.Rectangle
	}{
		{1000, 500, stretch(512, 512), image.Rect(0, 0, 512, 512)},
		{1000, 500, fit(512, 512), image.Rect(0, 0, 512, 256)},
		{500, 1000, fit(512, 512), image.Rect(0, 0, 256, 512)},
		{1000, 500, crop(512, 512), image.Rect(0, 0, 512, 512)},
		{100, 50, stretch(512, 512), image.Rect(0, 0, 512, 512)},
		{100, 50, fit(512, 512), image.Rect(0, 0, 512, 256)},
		{100, 50, crop(512, 512), image.Rect(0, 0, 512, 512)},
		{1, 1, fit(16384, 1), image.Rect(0, 0, 1, 1)},
		{1, 1, crop(64, 32), image.Rect(0, 0, 64, 32)},
		{4096, 1, fit(16, 16), image.Rect(0, 0, 16, 1)},
		{1, 4096, crop(3, 5), image.Rect(0, 0, 3, 5)},
	}
	for _, tc := range cases {
		out := mustResize(t, noise(7, image.Rect(0, 0, tc.width, tc.height), false), tc.target)
		if out.Bounds() != tc.want {
			t.Errorf("%d × %d with %+v: bounds %v, want %v", tc.width, tc.height, tc.target, out.Bounds(), tc.want)
		}
	}
}

// TestResizeKeepsThePixelKind pins the output type contract: grayscale
// stays grayscale, 16-bit stays 16-bit, opaque sources lose no detail to an
// alpha channel, and any translucency keeps one.
func TestResizeKeepsThePixelKind(t *testing.T) {
	want := map[string]image.Image{
		"NRGBA": &image.NRGBA{}, "opaque NRGBA": &image.RGBA{}, "RGBA": &image.NRGBA{},
		"Gray": &image.Gray{}, "Gray16": &image.Gray16{},
		"RGBA64": &image.NRGBA64{}, "NRGBA64": &image.NRGBA64{}, "opaque NRGBA64": &image.RGBA64{},
		"CMYK": &image.RGBA{}, "Paletted": &image.NRGBA{}, "opaque Paletted": &image.RGBA{},
	}
	for name, img := range typed(3, 9, 7) {
		wantType := reflect.TypeOf(&image.RGBA{})
		if w, ok := want[name]; ok {
			wantType = reflect.TypeOf(w)
		}
		for _, img := range []image.Image{img, generic{img}} {
			if got := reflect.TypeOf(mustResize(t, img, fit(20, 20))); got != wantType {
				t.Errorf("%s (%T): output %v, want %v", name, img, got, wantType)
			}
		}
	}
}

// TestResizeUnchangedSizeIsExact checks that a resize to the source's own
// size, in any mode, returns the source pixels unchanged: the filter lands
// every output sample on a source sample center, where its only nonzero
// weight is one.
func TestResizeUnchangedSizeIsExact(t *testing.T) {
	for name, img := range typed(5, 13, 11) {
		b := img.Bounds()
		for _, mode := range everyMode {
			out := mustResize(t, img, mode(b.Dx(), b.Dy()))
			for y := range b.Dy() {
				for x := range b.Dx() {
					if !sameColor(img.At(x, y), out.At(x, y)) {
						t.Fatalf("%s %+v: pixel (%d, %d) is %#v, want %#v", name, mode(1, 1).Mode, x, y, out.At(x, y), img.At(x, y))
					}
				}
			}
		}
	}
}

// sameColor compares a source pixel with an unchanged-size output pixel,
// which stores straight color. Straight 8-bit and 16-bit sources must come
// back exactly, except that a translucent 16-bit color is only as precise as
// its 16-bit premultiplied value; a premultiplied source must come back as
// its straight color rounded to the nearest step; and a YCbCr or CMYK source
// as its 16-bit RGB conversion rounded to 8 bits. A fully transparent pixel
// comes back as transparent black.
func sameColor(source, output color.Color) bool {
	round := func(value, alpha uint32, steps float64) float64 {
		return math.Round(float64(value) * steps / float64(alpha))
	}
	switch s := source.(type) {
	case color.Gray, color.Gray16:
		return source == output
	case color.NRGBA:
		o := color.NRGBAModel.Convert(output).(color.NRGBA)
		if s.A == 0 {
			return o == color.NRGBA{}
		}
		return o == s
	case color.NRGBA64:
		o := color.NRGBA64Model.Convert(output).(color.NRGBA64)
		if s.A == 0 {
			return o == color.NRGBA64{}
		}
		tolerance := 0xffff/float64(s.A)/2 + 0.5
		for _, pair := range [][2]uint16{{s.R, o.R}, {s.G, o.G}, {s.B, o.B}} {
			if math.Abs(float64(pair[0])-float64(pair[1])) > tolerance {
				return false
			}
		}
		return s.A == o.A
	case color.RGBA:
		o := color.NRGBAModel.Convert(output).(color.NRGBA)
		if s.A == 0 {
			return o == color.NRGBA{}
		}
		a := uint32(s.A)
		want := color.NRGBA{R: uint8(round(uint32(s.R), a, 0xff)), G: uint8(round(uint32(s.G), a, 0xff)), B: uint8(round(uint32(s.B), a, 0xff)), A: s.A}
		return o == want
	case color.RGBA64:
		o := color.NRGBA64Model.Convert(output).(color.NRGBA64)
		if s.A == 0 {
			return o == color.NRGBA64{}
		}
		a := uint32(s.A)
		want := color.NRGBA64{R: uint16(round(uint32(s.R), a, 0xffff)), G: uint16(round(uint32(s.G), a, 0xffff)), B: uint16(round(uint32(s.B), a, 0xffff)), A: s.A}
		return o == want
	}
	r, g, b, a := source.RGBA()
	if a != 0xffff {
		panic(fmt.Sprintf("unexpected translucent %T", source))
	}
	want := color.RGBA{R: uint8(round(r, 0xffff, 0xff)), G: uint8(round(g, 0xffff, 0xff)), B: uint8(round(b, 0xffff, 0xff)), A: 0xff}
	return output == want
}

// TestResizeUniformStaysUniform checks that a single-color source resizes
// to exactly that color at every scale and in every mode, translucent
// colors included: each output sample's weights sum to exactly one.
func TestResizeUniformStaysUniform(t *testing.T) {
	colors := []color.NRGBA{{R: 200, G: 100, B: 50, A: 255}, {R: 10, G: 250, B: 128, A: 77}, {R: 255, G: 255, B: 255, A: 1}, {}}
	sizes := [][2]int{{1, 1}, {7, 3}, {64, 64}, {1000, 3}}
	targets := [][2]int{{1, 1}, {5, 9}, {100, 37}, {333, 2}}
	for _, c := range colors {
		for _, size := range sizes {
			src := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
			for i := 0; i < len(src.Pix); i += 4 {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c.R, c.G, c.B, c.A
			}
			for _, target := range targets {
				for _, mode := range everyMode {
					out := mustResize(t, src, mode(target[0], target[1]))
					want := color.Color(c)
					if c.A == 0xff {
						want = color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
					} else if c.A == 0 {
						want = color.NRGBA{}
					}
					b := out.Bounds()
					for y := range b.Dy() {
						for x := range b.Dx() {
							if got := out.At(x, y); got != want {
								t.Fatalf("%v %v into %v %s: pixel (%d, %d) = %v, want %v", c, size, target, mode(1, 1).Mode, x, y, got, want)
							}
						}
					}
				}
			}
		}
	}
}

// TestTransparentPixelsLendNoColor pins premultiplied filtering: a fully
// transparent red area beside opaque blue must not tint the blended edge
// pixels red when reducing or enlarging.
func TestTransparentPixelsLendNoColor(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			c := color.NRGBA{R: 0xff, A: 0}
			if x >= 17 {
				c = color.NRGBA{B: 0xff, A: 0xff}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	for _, target := range []Target{fit(13, 13), stretch(97, 31), crop(30, 50)} {
		out := mustResize(t, src, target).(*image.NRGBA)
		sawEdge := false
		for i := 0; i < len(out.Pix); i += 4 {
			if out.Pix[i+3] != 0 && (out.Pix[i] != 0 || out.Pix[i+1] != 0) {
				t.Fatalf("%+v: pixel %v carries color from the transparent area", target, out.Pix[i:i+4])
			}
			sawEdge = sawEdge || (out.Pix[i+3] != 0 && out.Pix[i+3] != 0xff)
		}
		if !sawEdge {
			t.Fatalf("%+v: no blended edge pixels, so the check proved nothing", target)
		}
	}
}

// TestFastPathsMatchTheGenericPath reads every standard image type both
// directly and through At, which must give identical output.
func TestFastPathsMatchTheGenericPath(t *testing.T) {
	for name, img := range typed(11, 17, 12) {
		for _, target := range []Target{fit(8, 8), stretch(40, 9), crop(23, 31)} {
			direct, viaAt := mustResize(t, img, target), mustResize(t, generic{img}, target)
			if !sameImage(direct, viaAt) {
				t.Errorf("%s %+v: the direct and generic paths differ", name, target)
			}
		}
	}
}

// TestSourceBoundsNeedNotStartAtTheOrigin resizes a sub-image, which must
// match resizing a copy of the same pixels at the origin.
func TestSourceBoundsNeedNotStartAtTheOrigin(t *testing.T) {
	for name, img := range typed(13, 30, 20) {
		sub := img.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(image.Rect(7, 3, 25, 17))
		copied := typed(13, 30, 20)[name]
		moved := moveToOrigin(t, copied, sub.Bounds())
		for _, target := range []Target{fit(9, 9), crop(31, 5)} {
			if !sameImage(mustResize(t, sub, target), mustResize(t, moved, target)) {
				t.Errorf("%s %+v: a sub-image resizes differently from its pixels at the origin", name, target)
			}
		}
	}
}

// moveToOrigin copies the region of img to a new image of the same type
// whose bounds start at (0, 0).
func moveToOrigin(t *testing.T, img image.Image, region image.Rectangle) image.Image {
	t.Helper()
	rect := image.Rect(0, 0, region.Dx(), region.Dy())
	var out interface {
		image.Image
		Set(x, y int, c color.Color)
	}
	switch img := img.(type) {
	case *image.NRGBA:
		out = image.NewNRGBA(rect)
	case *image.RGBA:
		out = image.NewRGBA(rect)
	case *image.Gray:
		out = image.NewGray(rect)
	case *image.Gray16:
		out = image.NewGray16(rect)
	case *image.RGBA64:
		out = image.NewRGBA64(rect)
	case *image.NRGBA64:
		out = image.NewNRGBA64(rect)
	case *image.CMYK:
		out = image.NewCMYK(rect)
	case *image.Paletted:
		out = image.NewPaletted(rect, img.Palette)
	case *image.YCbCr:
		// YCbCr images are not settable, so the sub-image is compared with
		// itself read through At, whose offsets the standard library owns.
		return generic{img.SubImage(region)}
	default:
		t.Fatalf("unexpected type %T", img)
	}
	for y := range region.Dy() {
		for x := range region.Dx() {
			out.Set(x, y, img.At(region.Min.X+x, region.Min.Y+y))
		}
	}
	return out
}

// reference resamples straight from the filter's definition in float64,
// without fixed point, windows computed from real-valued positions, or the
// separable streaming: premultiplied output samples in [0, 1], before
// clamping.
func reference(img image.Image, g Geometry) [][4]float64 {
	b := img.Bounds()
	weightsX := referenceWeights(b.Dx(), g.ScaledWidth, g.Crop.Min.X, g.Width)
	weightsY := referenceWeights(b.Dy(), g.ScaledHeight, g.Crop.Min.Y, g.Height)
	premultiplied := func(x, y int) [4]float64 {
		c := img.At(b.Min.X+x, b.Min.Y+y)
		if n, ok := c.(color.NRGBA); ok {
			a := float64(n.A) / 255
			return [4]float64{float64(n.R) / 255 * a, float64(n.G) / 255 * a, float64(n.B) / 255 * a, a}
		}
		r, gr, bl, a := c.RGBA()
		return [4]float64{float64(r) / 0xffff, float64(gr) / 0xffff, float64(bl) / 0xffff, float64(a) / 0xffff}
	}
	out := make([][4]float64, g.Width*g.Height)
	for y, wy := range weightsY {
		for x, wx := range weightsX {
			var sum [4]float64
			for j, vertical := range wy {
				if vertical == 0 {
					continue
				}
				for i, horizontal := range wx {
					if horizontal == 0 {
						continue
					}
					p := premultiplied(i, j)
					for c := range sum {
						sum[c] += vertical * horizontal * p[c]
					}
				}
			}
			out[y*g.Width+x] = sum
		}
	}
	return out
}

// referenceWeights returns, for each output sample, a weight per source
// sample, normalized over the source.
func referenceWeights(sourceLength, scaledLength, first, count int) [][]float64 {
	scale := float64(sourceLength) / float64(scaledLength)
	widen := math.Max(1, scale)
	weights := make([][]float64, count)
	for i := range weights {
		center := (float64(first+i) + 0.5) * scale
		row := make([]float64, sourceLength)
		var sum float64
		for j := range row {
			t := math.Abs((float64(j) + 0.5 - center) / widen)
			switch {
			case t < 1:
				row[j] = 1.5*t*t*t - 2.5*t*t + 1
			case t < 2:
				row[j] = -0.5*t*t*t + 2.5*t*t - 4*t + 2
			}
			sum += row[j]
		}
		for j := range row {
			row[j] /= sum
		}
		weights[i] = row
	}
	return weights
}

// TestResizeMatchesTheFloatReference checks the fixed-point streaming
// filter against the float64 reference: every 8-bit output channel is
// within one step of the reference rounded to 8 bits. Straight color is
// only compared where alpha is high enough for 8-bit precision to mean
// anything.
func TestResizeMatchesTheFloatReference(t *testing.T) {
	sources := []image.Image{noise(21, image.Rect(0, 0, 37, 23), true), noise(22, image.Rect(0, 0, 23, 37), false), typed(23, 31, 19)["Gray"], typed(24, 19, 31)["YCbCr 4:2:0"]}
	targets := []Target{fit(11, 11), fit(80, 80), stretch(5, 61), stretch(90, 3), crop(17, 17), crop(70, 9), crop(3, 100)}
	for _, src := range sources {
		b := src.Bounds()
		for _, target := range targets {
			g := mustPlan(t, b.Dx(), b.Dy(), target)
			want := reference(src, g)
			for _, o := range []order{orderHorizontalFirst, orderVerticalFirst} {
				out := mustResample(t, src, target, o)
				for y := range g.Height {
					for x := range g.Width {
						ref := want[y*g.Width+x]
						alpha := math.Min(math.Max(ref[3], 0), 1)
						var got, expect []float64
						switch o := out.(type) {
						case *image.NRGBA:
							p := o.Pix[o.PixOffset(x, y):]
							got = []float64{float64(p[3])}
							expect = []float64{alpha * 255}
							if p[3] >= 32 {
								for c := range 3 {
									got = append(got, float64(p[c]))
									expect = append(expect, math.Min(math.Max(ref[c], 0), alpha)/alpha*255)
								}
							}
						case *image.RGBA:
							p := o.Pix[o.PixOffset(x, y):]
							for c := range 3 {
								got = append(got, float64(p[c]))
								expect = append(expect, math.Min(math.Max(ref[c], 0), 1)*255)
							}
						case *image.Gray:
							got = []float64{float64(o.Pix[o.PixOffset(x, y)])}
							expect = []float64{math.Min(math.Max(ref[0], 0), 1) * 255}
						default:
							t.Fatalf("unexpected output %T", out)
						}
						for c := range got {
							if math.Abs(got[c]-math.Round(expect[c])) > 1 {
								t.Fatalf("%T %v %+v order %d: pixel (%d, %d) channel %d = %v, reference %.3f", src, b, target, o, x, y, c, got[c], expect[c])
							}
						}
					}
				}
			}
		}
	}
}

// TestCropToFillEqualsScalingThenCropping checks that resampling only the
// cropped region gives the same pixels as scaling the whole source to the
// scaled size and cropping afterwards.
func TestCropToFillEqualsScalingThenCropping(t *testing.T) {
	src := noise(31, image.Rect(0, 0, 61, 29), true)
	for _, target := range []Target{crop(20, 20), crop(61, 5), crop(9, 40), crop(200, 150)} {
		g := mustPlan(t, 61, 29, target)
		for _, o := range []order{orderHorizontalFirst, orderVerticalFirst} {
			cropped := mustResample(t, src, target, o).(*image.NRGBA)
			whole := mustResample(t, src, stretch(g.ScaledWidth, g.ScaledHeight), o).(*image.NRGBA)
			for y := range g.Height {
				got := cropped.Pix[cropped.PixOffset(0, y) : cropped.PixOffset(0, y)+4*g.Width]
				want := whole.Pix[whole.PixOffset(g.Crop.Min.X, g.Crop.Min.Y+y) : whole.PixOffset(g.Crop.Min.X, g.Crop.Min.Y+y)+4*g.Width]
				if string(got) != string(want) {
					t.Fatalf("%+v order %d: row %d differs from the scaled source's crop %v", target, o, y, g.Crop)
				}
			}
		}
	}
}

// TestOrdersAgree checks that both filter orders give the same picture:
// they differ only in where the intermediate rounding falls, which moves a
// 16-bit premultiplied sample by at most one step, so an 8-bit output by at
// most one step and a 16-bit output's premultiplied value by a few.
func TestOrdersAgree(t *testing.T) {
	for name, img := range typed(41, 45, 33) {
		for _, target := range []Target{fit(10, 10), stretch(100, 7), stretch(6, 90), crop(50, 50)} {
			a := mustResample(t, img, target, orderHorizontalFirst)
			b := mustResample(t, img, target, orderVerticalFirst)
			tolerance := uint32(0x101 + 1)
			switch a.(type) {
			case *image.Gray16, *image.RGBA64, *image.NRGBA64:
				tolerance = 4
			}
			bounds := a.Bounds()
			for y := range bounds.Dy() {
				for x := range bounds.Dx() {
					r1, g1, b1, a1 := a.At(x, y).RGBA()
					r2, g2, b2, a2 := b.At(x, y).RGBA()
					for _, pair := range [][2]uint32{{r1, r2}, {g1, g2}, {b1, b2}, {a1, a2}} {
						if max(pair[0], pair[1])-min(pair[0], pair[1]) > tolerance {
							t.Fatalf("%s %+v: pixel (%d, %d) is %v in one order and %v in the other", name, target, x, y, a.At(x, y), b.At(x, y))
						}
					}
				}
			}
		}
	}
}

// TestChooseOrder pins the order choice: the cheaper order for an ordinary
// reduction or enlargement, and vertical-first whenever the window of
// resampled rows would exceed its budget.
func TestChooseOrder(t *testing.T) {
	axes := func(sourceWidth, sourceHeight int, target Target) (*axis, *axis) {
		g := mustPlan(t, sourceWidth, sourceHeight, target)
		return newAxis(sourceWidth, g.ScaledWidth, g.Crop.Min.X, g.Width), newAxis(sourceHeight, g.ScaledHeight, g.Crop.Min.Y, g.Height)
	}
	cases := []struct {
		name                      string
		sourceWidth, sourceHeight int
		target                    Target
		want                      order
	}{
		{"uniform reduction", 4000, 3000, fit(1024, 1024), orderHorizontalFirst},
		{"uniform enlargement", 100, 100, fit(1000, 1000), orderHorizontalFirst},
		// Widening a few columns per row is cheap; combining thousands of
		// full-width rows first would not be.
		{"wider and much shorter", 2000, 4000, stretch(4000, 100), orderVerticalFirst},
		// The window would hold 4 × 65536 / 16 rows of 4096 pixels.
		{"window over budget", 4096, 65536, stretch(4096, 16), orderVerticalFirst},
		{"narrower and much taller", 4000, 100, stretch(40, 4000), orderHorizontalFirst},
	}
	for _, tc := range cases {
		horizontal, vertical := axes(tc.sourceWidth, tc.sourceHeight, tc.target)
		if got := chooseOrder(horizontal, vertical, layoutRGB); got != tc.want {
			t.Errorf("%s: chooseOrder = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestResizeIsDeterministic pins RSZ-017: repeated resizes of the same
// source are bit-identical, and the outputs match digests recorded once, so
// a platform whose arithmetic differed would fail here. The digests were
// verified on windows/arm64, whose compiler fuses multiply-adds, and on
// windows/amd64 at GOAMD64=v1 and v3.
func TestResizeIsDeterministic(t *testing.T) {
	images := typed(51, 211, 157)
	cases := []struct {
		image  string
		target Target
		want   string
	}{
		{"NRGBA", fit(64, 64), "fc0c12e6be1826b9540dffe6de0cc91cab2149c0ef76c27db6d1f113a7ddf9ee"},
		{"opaque NRGBA", stretch(300, 41), "37db23c8b6d9519a23ab9e7f898b17e7a0ff3286d9dec8dc4d6a537c1a12343e"},
		{"YCbCr 4:2:0", crop(97, 97), "c55361e835497e86f372aa7fb2a1efb95f7361494de485969eef24a381fd3ac7"},
		{"Gray16", fit(500, 500), "8be488b3c72048f7ac2031dfb643ef121113394697fe989ab91e6f9bdd2167ec"},
		{"NRGBA64", crop(33, 180), "daafef10b2d263160051dea295bc1e692418e3712ce39efa00192d3c9104c8f5"},
	}
	for _, tc := range cases {
		first := mustResize(t, images[tc.image], tc.target)
		second := mustResize(t, images[tc.image], tc.target)
		if !sameImage(first, second) {
			t.Fatalf("%s %+v: two resizes of the same source differ", tc.image, tc.target)
		}
		digest := sha256.Sum256(pixels(first))
		if got := hex.EncodeToString(digest[:]); got != tc.want {
			t.Errorf("%s %+v: digest %s, want %s", tc.image, tc.target, got, tc.want)
		}
	}
}

// cancelling reads a source through At and cancels a context after a
// number of reads, so cancellation arrives part-way through a resize.
type cancelling struct {
	generic
	reads  *int
	after  int
	cancel context.CancelFunc
}

func (c cancelling) At(x, y int) color.Color {
	if *c.reads++; *c.reads == c.after {
		c.cancel()
	}
	return c.generic.At(x, y)
}

// TestResizeObservesCancellation checks that a resize stops with a
// KindCanceled error when the context has ended before it starts, and when
// it ends part-way, in both orders, without reading the rest of the source.
func TestResizeObservesCancellation(t *testing.T) {
	src := noise(61, image.Rect(0, 0, 300, 200), true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := Resize(ctx, src, fit(100, 100)); out != nil || !isCanceled(err) {
		t.Fatalf("Resize with an ended context = %v, %v; want a KindCanceled error", out, err)
	}
	g := mustPlan(t, 300, 200, fit(100, 100))
	for _, o := range []order{orderHorizontalFirst, orderVerticalFirst} {
		ctx, cancel := context.WithCancel(context.Background())
		reads := 0
		img := cancelling{generic: generic{src}, reads: &reads, after: 3 * 300, cancel: cancel}
		out, err := resample(ctx, img, g, o)
		cancel()
		if out != nil || !isCanceled(err) {
			t.Fatalf("order %d: resample cancelled part-way = %v, %v; want a KindCanceled error", o, out, err)
		}
		// Cancellation is observed before the next source row is read.
		if reads != 3*300 {
			t.Fatalf("order %d: %d pixels read, want the 3 rows read before the cancellation", o, reads)
		}
	}
}

func isCanceled(err error) bool {
	var resizeErr *Error
	return errors.As(err, &resizeErr) && resizeErr.Kind == KindCanceled && errors.Is(err, context.Canceled)
}

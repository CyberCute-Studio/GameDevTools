package resize

import (
	"context"
	"image"
	"image/color"
	"math"
	"runtime"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
)

// TestCatmullRomValues pins the kernel at its defining points: one at the
// center, zero at every other integer, and the familiar half-pixel values,
// symmetric about the center.
func TestCatmullRomValues(t *testing.T) {
	cases := []struct {
		numerator, denominator int64
		want                   float64
	}{
		{0, 1, 1}, {1, 1, 0}, {2, 1, 0}, {3, 1, 0},
		{1, 2, 0.5625}, {3, 2, -0.0625}, {1, 4, 0.8671875}, {7, 4, -0.0234375},
	}
	for _, tc := range cases {
		for _, sign := range []int64{1, -1} {
			if got := catmullRom(sign*tc.numerator, tc.denominator); got != tc.want {
				t.Errorf("catmullRom(%d/%d) = %v, want %v", sign*tc.numerator, tc.denominator, got, tc.want)
			}
		}
	}
}

// axisCases spans reductions, enlargements, unchanged lengths, extreme
// ratios, and crops of a scaled axis.
var axisCases = []struct{ source, scaled, first, count int }{
	{1, 1, 0, 1}, {1, 7, 0, 7}, {7, 1, 0, 1}, {10, 10, 0, 10}, {10, 3, 0, 3},
	{3, 10, 0, 10}, {1000, 512, 0, 512}, {500, 1024, 256, 512}, {37, 161, 79, 3},
	{4000, 1024, 0, 1024}, {MaxSourceDimension, 16, 0, 16}, {2, 1 << 34, 1<<33 - 8, 16},
	{4096, 3, 0, 3}, {5, 2, 0, 2},
}

// TestAxisWeightsSumToUnity checks every output sample's weights: they sum
// to exactly one in fixed point, each lies within one unit of the float64
// reference's weight however many the output has, they start and end with
// nonzero weights, and they read only source samples inside the axis, which
// lo, hi, and widest bound.
func TestAxisWeightsSumToUnity(t *testing.T) {
	for _, tc := range axisCases {
		a := newAxis(tc.source, tc.scaled, tc.first, tc.count)
		if len(a.start) != tc.count {
			t.Fatalf("%+v: %d output samples, want %d", tc, len(a.start), tc.count)
		}
		widest, lo, hi := 0, tc.source, 0
		for i := range tc.count {
			first, weights := a.taps(i)
			if len(weights) == 0 || weights[0] == 0 || weights[len(weights)-1] == 0 {
				t.Fatalf("%+v: output %d has weights %v, want nonzero ends", tc, i, weights)
			}
			if first < 0 || first+len(weights) > tc.source {
				t.Fatalf("%+v: output %d reads source samples [%d, %d) outside [0, %d)", tc, i, first, first+len(weights), tc.source)
			}
			// The reference evaluates the kernel at real-valued positions, so
			// it differs from the exact weights by far less than the slack.
			exact := referenceWeights(tc.source, tc.scaled, tc.first+i, 1)[0]
			for j, w := range exact {
				var got int32
				if k := j - first; k >= 0 && k < len(weights) {
					got = weights[k]
				}
				if math.Abs(float64(got)-w*unitWeight) > 1.001 {
					t.Fatalf("%+v: output %d weighs source sample %d at %d, reference %.3f", tc, i, j, got, w*unitWeight)
				}
			}
			var sum int64
			for _, w := range weights {
				sum += int64(w)
			}
			if sum != unitWeight {
				t.Fatalf("%+v: output %d weights sum to %d, want %d", tc, i, sum, unitWeight)
			}
			widest, lo, hi = max(widest, len(weights)), min(lo, first), max(hi, first+len(weights))
		}
		if a.widest != widest || a.lo != lo || a.hi != hi {
			t.Fatalf("%+v: widest %d, range [%d, %d); want %d, [%d, %d)", tc, a.widest, a.lo, a.hi, widest, lo, hi)
		}
		// newAxis sizes its table for at most 4·max(S, L)/L + 2 weights per
		// output sample.
		if bound := min(tc.source, 4*max(tc.source, tc.scaled)/tc.scaled+2); widest > bound {
			t.Fatalf("%+v: an output has %d weights, beyond the preallocated %d", tc, widest, bound)
		}
	}
}

// TestUnchangedAxisCopiesSamples checks that an axis scaled to its own
// length, whether whole or cropped, maps each output sample to exactly one
// source sample.
func TestUnchangedAxisCopiesSamples(t *testing.T) {
	for _, tc := range []struct{ length, first, count int }{{1, 0, 1}, {9, 0, 9}, {1000, 0, 1000}, {1000, 333, 7}} {
		a := newAxis(tc.length, tc.length, tc.first, tc.count)
		for i := range tc.count {
			first, weights := a.taps(i)
			if first != tc.first+i || !slices.Equal(weights, []int32{unitWeight}) {
				t.Fatalf("%+v: output %d reads %v from %d, want one unit weight from %d", tc, i, weights, first, tc.first+i)
			}
		}
	}
}

// TestCroppedAxisMatchesTheWholeAxis checks that the weights of a cropped
// run of output samples are exactly those of the same samples of the whole
// scaled axis, which is why Crop to Fill can resample only its crop.
func TestCroppedAxisMatchesTheWholeAxis(t *testing.T) {
	for _, tc := range []struct{ source, scaled, first, count int }{{500, 1024, 256, 512}, {37, 161, 79, 3}, {1000, 997, 1, 995}, {61, 67, 30, 30}} {
		whole := newAxis(tc.source, tc.scaled, 0, tc.scaled)
		cropped := newAxis(tc.source, tc.scaled, tc.first, tc.count)
		for i := range tc.count {
			wantFirst, wantWeights := whole.taps(tc.first + i)
			first, weights := cropped.taps(i)
			if first != wantFirst || !slices.Equal(weights, wantWeights) {
				t.Fatalf("%+v: output %d differs from scaled sample %d", tc, i, tc.first+i)
			}
		}
	}
}

// TestReductionReadsEverySourceSample checks that a reduction's widened
// filter leaves no source sample out, so no detail is skipped.
func TestReductionReadsEverySourceSample(t *testing.T) {
	for _, tc := range []struct{ source, scaled int }{{1000, 512}, {4000, 1024}, {7, 2}, {MaxSourceDimension, 1}, {100, 99}} {
		a := newAxis(tc.source, tc.scaled, 0, tc.scaled)
		read := make([]bool, tc.source)
		for i := range tc.scaled {
			first, weights := a.taps(i)
			for k, w := range weights {
				read[first+k] = read[first+k] || w != 0
			}
		}
		if missed := slices.Index(read, false); missed >= 0 {
			t.Fatalf("%+v: source sample %d contributes to no output", tc, missed)
		}
	}
}

// TestExtremeSourcesStayBounded resizes real sources at MaxSourceDimension
// on one axis to the planned bounds, without the work growing with the
// scaled size, which Crop to Fill makes 1,572,864 rows here.
// TestWorkingMemoryStaysBounded measures the memory.
func TestExtremeSourcesStayBounded(t *testing.T) {
	wide := image.NewGray(image.Rect(0, 0, MaxSourceDimension, 2))
	tall := image.NewGray(image.Rect(0, 0, 2, MaxSourceDimension))
	for i := range wide.Pix {
		wide.Pix[i] = uint8(i)
		tall.Pix[i] = uint8(i)
	}
	if got := mustResize(t, wide, fit(64, 64)).Bounds(); got != image.Rect(0, 0, 64, 1) {
		t.Fatalf("wide source fit into 64 × 64: %v", got)
	}
	// Crop to Fill scales the tall source to 3 × 1,572,864 and keeps the
	// middle three rows, which read about two source rows.
	if got := mustResize(t, tall, crop(3, 3)).Bounds(); got != image.Rect(0, 0, 3, 3) {
		t.Fatalf("tall source cropped to 3 × 3: %v", got)
	}
	if got := mustResize(t, tall, stretch(16384, 2)).Bounds(); got != image.Rect(0, 0, 16384, 2) {
		t.Fatalf("tall source stretched to 16384 × 2: %v", got)
	}
}

// virtual is a uniform source of any size whose pixels take no memory,
// read through At, which its probe observes.
type virtual struct {
	rect  image.Rectangle
	model color.Model
	pixel color.Color
	probe *probe
}

// probe counts a virtual source's reads. At read mark it records how much
// the process has allocated so far, and at read stop it cancels the resize,
// which by then is filtering.
type probe struct {
	reads, mark, stop int
	stats             runtime.MemStats
	marked            uint64
	cancel            context.CancelFunc
}

func (v virtual) ColorModel() color.Model { return v.model }
func (v virtual) Bounds() image.Rectangle { return v.rect }
func (v virtual) At(x, y int) color.Color {
	p := v.probe
	switch p.reads++; p.reads {
	case p.mark:
		runtime.ReadMemStats(&p.stats)
		p.marked = p.stats.TotalAlloc
	case p.stop:
		p.cancel()
	}
	return v.pixel
}

// TestWorkingMemoryStaysBounded measures what Resize allocates beyond its
// output for the most demanding shapes the source bound admits — axes of
// MaxSourceDimension reduced to a sliver, to the widest output row, or to
// the tallest output column, read with four channels, and the
// horizontal-first order with its working rows just within their budget —
// which the package documentation promises stays under MaxWorkingBytes.
// Each resize is cancelled once it has filtered a few source rows, so the
// measurement covers the filtering as well as the setup, and between two
// reads two source rows apart nothing proportional to a row may be
// allocated.
func TestWorkingMemoryStaysBounded(t *testing.T) {
	huge := image.Rect(0, 0, MaxSourceDimension, MaxSourceDimension)
	// Boxed once, so reading them allocates nothing.
	translucent := color.Color(color.NRGBA{R: 10, G: 20, B: 30, A: 40})
	gray := color.Color(color.Gray16{Y: 0x1234})
	cases := []struct {
		name          string
		rect          image.Rectangle
		model         color.Model
		pixel         color.Color
		target        Target
		bytesPerPixel int
	}{
		{"square to a sliver", huge, color.NRGBAModel, translucent, stretch(4, 3), 4},
		{"square cropped to a sliver", huge, color.NRGBAModel, translucent, crop(4, 3), 4},
		{"square to the widest row", huge, color.NRGBAModel, translucent, stretch(preset.MaxDimension, 1), 4},
		{"square to the tallest column", huge, color.NRGBAModel, translucent, stretch(1, preset.MaxDimension), 4},
		{"narrow column to a sliver", image.Rect(0, 0, 3, MaxSourceDimension), color.NRGBAModel, translucent, stretch(3, 4), 4},
		{"gray column to a sliver", image.Rect(0, 0, 16, MaxSourceDimension), color.GrayModel, gray, stretch(16, 3), 1},
		// TestChooseOrder pins the horizontal-first order for this shape.
		{"working rows just within budget", image.Rect(0, 0, MaxSourceDimension, 376), color.NRGBAModel, translucent, stretch(preset.MaxDimension, 8), 4},
	}
	for _, tc := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		// A row is read in at most rect.Dx() reads, after the opacity check's
		// one read of a translucent source.
		row := tc.rect.Dx()
		p := &probe{mark: 1 + row + row/2, stop: 1 + 3*row + row/2, cancel: cancel}
		src := virtual{rect: tc.rect, model: tc.model, pixel: tc.pixel, probe: p}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := Resize(ctx, src, tc.target)
		runtime.ReadMemStats(&after)
		cancel()
		if !isCanceled(err) || p.reads < p.stop {
			t.Fatalf("%s: Resize = %v after %d reads, want it cancelled after read %d", tc.name, err, p.reads, p.stop)
		}
		output := uint64(tc.target.Width * tc.target.Height * tc.bytesPerPixel)
		working := after.TotalAlloc - before.TotalAlloc - output
		t.Logf("%s: %.1f MiB of working memory", tc.name, float64(working)/(1<<20))
		if working >= MaxWorkingBytes {
			t.Errorf("%s: %.1f MiB of working memory, want under %d MiB", tc.name, float64(working)/(1<<20), MaxWorkingBytes>>20)
		}
		// Only the error the cancellation returns is allocated after the mark.
		if filtering := after.TotalAlloc - p.marked; filtering >= 1<<10 {
			t.Errorf("%s: %d bytes allocated while filtering two source rows, want none per row", tc.name, filtering)
		}
	}
}

package resize

import (
	"context"
	"image"
)

// order is which axis the separable filter resamples first. Both give the
// same picture, within one 16-bit step of rounding, and the choice is made
// to save work and memory. It depends only on the geometry and on how the
// source's pixels are read, so a given source and target always take the
// same order and give the same bits.
type order uint8

const (
	// orderAuto chooses the cheaper order that fits the memory budget.
	orderAuto order = iota
	// orderHorizontalFirst resamples each source row the output needs once,
	// keeping a window of resampled rows to combine vertically.
	orderHorizontalFirst
	// orderVerticalFirst combines the source rows of each output row first
	// and resamples the combination horizontally. It needs only one row of
	// working memory, so it serves strong vertical reductions, whose window
	// of resampled rows would be large.
	orderVerticalFirst
)

// rowBudget bounds the working rows of the horizontal-first order, in
// bytes, as horizontalFirstBytes counts them. The vertical-first order's
// working rows take 16 bytes per sample of a source row of at most
// MaxSourceDimension four-channel pixels, 64 MiB, and one output row of at
// most 256 KiB, so either order's rows stay within 64.25 MiB. Each axis's
// weight table and the scratch that builds it add at most 24.4 MiB (see
// newAxis), which keeps the working memory under 114 MiB.
const rowBudget = 64 << 20

// resample filters the source onto the geometry's output in the given
// order. It observes the context between its preparatory steps, so a
// cancellation arriving while it builds the weights or checks the source's
// opacity stops it before it allocates the output.
func resample(ctx context.Context, img image.Image, geometry Geometry, o order) (image.Image, error) {
	canceled := func() (image.Image, error) {
		return nil, &Error{Kind: KindCanceled, Err: ctx.Err()}
	}
	bounds := img.Bounds()
	horizontal := newAxis(bounds.Dx(), geometry.ScaledWidth, geometry.Crop.Min.X, geometry.Width)
	vertical := newAxis(bounds.Dy(), geometry.ScaledHeight, geometry.Crop.Min.Y, geometry.Height)
	if ctx.Err() != nil {
		return canceled()
	}
	src := newSource(img, image.Rect(horizontal.lo, vertical.lo, horizontal.hi, vertical.hi))
	if ctx.Err() != nil {
		return canceled()
	}
	if o == orderAuto {
		o = chooseOrder(horizontal, vertical, src)
	}
	out := newSink(src, geometry.Width, geometry.Height)
	f := filter{done: ctx.Done(), src: src, out: out, horizontal: horizontal, vertical: vertical}
	var completed bool
	if o == orderHorizontalFirst {
		completed = f.horizontalFirst()
	} else {
		completed = f.verticalFirst()
	}
	if !completed {
		return canceled()
	}
	return out.img, nil
}

// chooseOrder estimates the work of each order — reading a source pixel
// costs the source's fetchCost, and applying one weight to a pixel's samples
// costs one — and picks the cheaper, unless the horizontal-first order's
// working rows would exceed rowBudget. Horizontal-first reads each source
// row once; vertical-first reads a row again for every output row that
// combines it, which a costly read, such as a JPEG's YCbCr conversion,
// makes dearer. The vertical-first order's working rows always fit, as
// rowBudget describes.
func chooseOrder(horizontal, vertical *axis, src source) order {
	width, height := int64(len(horizontal.start)), int64(len(vertical.start))
	columns, rows := int64(horizontal.hi-horizontal.lo), int64(vertical.hi-vertical.lo)
	if horizontalFirstBytes(horizontal, vertical, src.layout) > rowBudget {
		return orderVerticalFirst
	}
	horizontalFirst := rows*(columns*src.fetchCost+int64(horizontal.total())) + width*int64(vertical.total())
	verticalFirst := columns*int64(vertical.total())*(src.fetchCost+1) + height*int64(horizontal.total())
	if verticalFirst < horizontalFirst {
		return orderVerticalFirst
	}
	return orderHorizontalFirst
}

// horizontalFirstBytes is the memory of the horizontal-first order's working
// rows: the window of resampled rows with the record of which source row
// each slot holds, the source row being resampled, and an output row's sums
// and finished samples.
func horizontalFirstBytes(horizontal, vertical *axis, l layout) int64 {
	rowLength := int64(len(horizontal.start)) * int64(l)
	slots := int64(vertical.widest)
	return slots*(4*rowLength+4) + 4*int64(horizontal.hi-horizontal.lo)*int64(l) + 12*rowLength
}

// filter runs one resize.
type filter struct {
	done                 <-chan struct{}
	src                  source
	out                  sink
	horizontal, vertical *axis
}

// channels is the number of samples per pixel in every working row.
func (f *filter) channels() int {
	return int(f.src.layout)
}

// canceled reports whether the context has ended.
func (f *filter) canceled() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// horizontalFirst resamples each needed source row horizontally into a
// window of resampled rows, then combines each output row's window rows
// vertically. A resampled row lives in slot r mod widest, where it stays
// until a row widest rows further replaces it, so the rows of one output
// row never evict each other. It reports whether it finished before the
// context ended.
func (f *filter) horizontalFirst() bool {
	channels := f.channels()
	rowLength := len(f.horizontal.start) * channels
	slots := f.vertical.widest
	window := make([]int32, slots*rowLength)
	// slotRow is the source row each slot holds, or −1. Rows are below
	// MaxSourceDimension, which int32 holds.
	slotRow := make([]int32, slots)
	for i := range slotRow {
		slotRow[i] = -1
	}
	sourceRow := make([]int32, (f.horizontal.hi-f.horizontal.lo)*channels)
	sums := make([]int64, rowLength)
	finished := make([]int32, rowLength)
	for y := range len(f.vertical.start) {
		if f.canceled() {
			return false
		}
		first, weights := f.vertical.taps(y)
		clear(sums)
		for k, weight := range weights {
			r := first + k
			slot := r % slots
			resampled := window[slot*rowLength : (slot+1)*rowLength]
			if slotRow[slot] != int32(r) {
				if f.canceled() {
					return false
				}
				f.src.fetch(sourceRow, r, f.horizontal.lo, f.horizontal.hi)
				f.resampleRow(resampled, sourceRow, firstShift)
				slotRow[slot] = int32(r)
			}
			accumulate(sums, resampled, weight)
		}
		roundSums(finished, sums, secondShift)
		f.out.store(finished, y)
	}
	return true
}

// verticalFirst combines each output row's source rows vertically into one
// working row, then resamples that row horizontally. It reports whether it
// finished before the context ended.
func (f *filter) verticalFirst() bool {
	channels := f.channels()
	rowLength := (f.horizontal.hi - f.horizontal.lo) * channels
	sourceRow := make([]int32, rowLength)
	sums := make([]int64, rowLength)
	combined := make([]int32, rowLength)
	finished := make([]int32, len(f.horizontal.start)*channels)
	for y := range len(f.vertical.start) {
		if f.canceled() {
			return false
		}
		first, weights := f.vertical.taps(y)
		clear(sums)
		for k, weight := range weights {
			if f.canceled() {
				return false
			}
			f.src.fetch(sourceRow, first+k, f.horizontal.lo, f.horizontal.hi)
			accumulate(sums, sourceRow, weight)
		}
		roundSums(combined, sums, firstShift)
		f.resampleRow(finished, combined, secondShift)
		f.out.store(finished, y)
	}
	return true
}

// resampleRow applies the horizontal weights to a row of samples that
// starts at source column horizontal.lo, rounding each sum by shift.
func (f *filter) resampleRow(dst, row []int32, shift uint) {
	a, lo := f.horizontal, f.horizontal.lo
	half := int64(1) << (shift - 1)
	switch f.channels() {
	case 1:
		for x := range a.start {
			first, weights := a.taps(x)
			samples := row[first-lo : first-lo+len(weights)]
			var s int64
			for k, w := range weights {
				s += int64(w) * int64(samples[k])
			}
			dst[x] = int32((s + half) >> shift)
		}
	case 3:
		for x := range a.start {
			first, weights := a.taps(x)
			samples := row[3*(first-lo) : 3*(first-lo+len(weights))]
			var s0, s1, s2 int64
			for k, w := range weights {
				p := samples[3*k : 3*k+3]
				w := int64(w)
				s0 += w * int64(p[0])
				s1 += w * int64(p[1])
				s2 += w * int64(p[2])
			}
			d := dst[3*x : 3*x+3]
			d[0], d[1], d[2] = int32((s0+half)>>shift), int32((s1+half)>>shift), int32((s2+half)>>shift)
		}
	default:
		for x := range a.start {
			first, weights := a.taps(x)
			samples := row[4*(first-lo) : 4*(first-lo+len(weights))]
			var s0, s1, s2, s3 int64
			for k, w := range weights {
				p := samples[4*k : 4*k+4]
				w := int64(w)
				s0 += w * int64(p[0])
				s1 += w * int64(p[1])
				s2 += w * int64(p[2])
				s3 += w * int64(p[3])
			}
			d := dst[4*x : 4*x+4]
			d[0], d[1], d[2], d[3] = int32((s0+half)>>shift), int32((s1+half)>>shift), int32((s2+half)>>shift), int32((s3+half)>>shift)
		}
	}
}

// accumulate adds weight·row to sums, element by element.
func accumulate(sums []int64, row []int32, weight int32) {
	w := int64(weight)
	row = row[:len(sums)]
	for i, v := range row {
		sums[i] += w * int64(v)
	}
}

// roundSums divides each sum by 2^shift, rounding halves up, into dst.
// The arithmetic shift floors negative sums too, so the rounding is the
// same on either side of zero.
func roundSums(dst []int32, sums []int64, shift uint) {
	half := int64(1) << (shift - 1)
	dst = dst[:len(sums)]
	for i, s := range sums {
		dst[i] = int32((s + half) >> shift)
	}
}

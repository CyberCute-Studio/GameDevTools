package resize

import "math"

const (
	// precisionBits is the fixed-point precision of a filter weight: the
	// weights of one output sample sum to exactly 1 << precisionBits. It
	// leaves every weight distinguishable even when a reduction spreads one
	// output sample across MaxSourceDimension source samples.
	precisionBits = 28
	unitWeight    = 1 << precisionBits
	// fractionBits is the extra precision kept between the two filter
	// passes, so the first pass's rounding does not compound with the
	// second's.
	fractionBits = 8
	// firstShift and secondShift return each pass's sums to its output
	// scale: the first pass keeps fractionBits beyond the 16-bit sample
	// scale, and the second removes them again.
	firstShift  = precisionBits - fractionBits
	secondShift = precisionBits + fractionBits
	// filterRadius is the Catmull-Rom support radius in filter units.
	filterRadius = 2
)

// axis holds the fixed-point filter weights that resample one source axis
// onto the output samples of that axis.
type axis struct {
	// start is each output sample's first source sample. Its weights are
	// weights[offset[i]:offset[i+1]], one per consecutive source sample.
	start   []int
	offset  []int
	weights []int32
	// lo and hi bound the source samples any output reads: [lo, hi).
	lo, hi int
	// widest is the largest number of weights of one output sample.
	widest int
}

// taps returns output sample i's first source sample and weights.
func (a *axis) taps(i int) (int, []int32) {
	return a.start[i], a.weights[a.offset[i]:a.offset[i+1]]
}

// total is the number of weights across every output sample.
func (a *axis) total() int {
	return len(a.weights)
}

// newAxis computes the weights that resample sourceLength samples, scaled
// to scaledLength, onto the count output samples starting at scaled sample
// first — all of the scaled axis unless Crop to Fill crops it.
//
// Sample i covers [i, i+1) and is centered at i + 1/2, in the source and in
// the scaled axis alike, so scaled sample x is centered at source position
// c = (2x+1)·S / (2L) for source length S and scaled length L. Reducing
// widens the filter by S/L, so source sample j sits at filter argument
// (j + 1/2 − c) / max(1, S/L) = ((2j+1)·L − (2x+1)·S) / (2·max(S, L)): an
// exact ratio of integers, which keeps the positions identical on every
// platform. The filter covers arguments in (−2, 2); weights the source edge
// cuts off are dropped and the rest renormalized.
func newAxis(sourceLength, scaledLength, first, count int) *axis {
	s, l := int64(sourceLength), int64(scaledLength)
	denominator := 2 * max(s, l)
	reach := filterRadius * denominator
	// The filter covers an open interval 2·reach/(2l) source samples wide,
	// so no output has more weights than that plus two, or than the source
	// has samples. Allocating that bound up front keeps the table from
	// growing — and briefly doubling — while it fills: it comes to about
	// 4·sourceLength weights for a reduction and 6·count for an
	// enlargement.
	perOutput := min(s, 2*reach/(2*l)+2)
	a := &axis{
		start:   make([]int, count),
		offset:  make([]int, count+1),
		weights: make([]int32, 0, int64(count)*perOutput),
		lo:      sourceLength,
	}
	for i := range count {
		position := (2*int64(first+i) + 1) * s
		// |(2j+1)·l − position| < reach bounds the source samples the
		// filter covers; the clamp to the source drops those outside it.
		low := max(floorDiv(position-reach-l, 2*l)+1, 0)
		high := min(ceilDiv(position+reach-l, 2*l), s)
		var sum float64
		for j := low; j < high; j++ {
			sum += catmullRom((2*j+1)*l-position, denominator)
		}
		base := len(a.weights)
		heaviest, quantized := base, int64(0)
		for j := low; j < high; j++ {
			weight := int64(math.Round(float64(catmullRom((2*j+1)*l-position, denominator) / sum * unitWeight)))
			a.weights = append(a.weights, int32(weight))
			quantized += weight
			if int32(weight) > a.weights[heaviest] {
				heaviest = len(a.weights) - 1
			}
		}
		// Rounding each weight separately can leave the sum a few units off;
		// the heaviest weight absorbs the difference, so a uniform area stays
		// exactly uniform.
		a.weights[heaviest] += int32(unitWeight - quantized)
		// Exact zeros at either end, where a source sample center lands on a
		// zero of the filter, cost work and change nothing.
		taps := a.weights[base:]
		leading, trailing := 0, len(taps)
		for taps[leading] == 0 {
			leading++
		}
		for taps[trailing-1] == 0 {
			trailing--
		}
		copy(taps, taps[leading:trailing])
		a.weights = a.weights[:base+trailing-leading]
		a.start[i] = int(low) + leading
		a.offset[i+1] = len(a.weights)
		a.lo = min(a.lo, a.start[i])
		a.hi = max(a.hi, a.start[i]+trailing-leading)
		a.widest = max(a.widest, trailing-leading)
	}
	return a
}

// catmullRom evaluates the Catmull-Rom cubic — the Keys cubic with
// a = −1/2 — at the filter argument numerator/denominator. Both are exact
// in float64, so the argument is one correctly rounded division. Each
// product is converted explicitly so the compiler cannot fuse it with the
// following addition into a multiply-add, whose different rounding would
// make the weights differ between platforms.
func catmullRom(numerator, denominator int64) float64 {
	t := math.Abs(float64(numerator)) / float64(denominator)
	switch {
	case t < 1:
		// (3/2)t³ − (5/2)t² + 1
		p := float64(1.5*t) - 2.5
		p = float64(p * t)
		p = float64(p * t)
		return p + 1
	case t < 2:
		// −(1/2)t³ + (5/2)t² − 4t + 2
		p := float64(-0.5*t) + 2.5
		p = float64(p*t) - 4
		p = float64(p * t)
		return p + 2
	}
	return 0
}

// floorDiv returns ⌊a/b⌋ for b > 0.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// ceilDiv returns ⌈a/b⌉ for b > 0.
func ceilDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a > 0 {
		q++
	}
	return q
}

package resize

import (
	"errors"
	"fmt"
	"image"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

func fit(width, height int) Target {
	return Target{Width: width, Height: height, Mode: preset.ResizeFit}
}
func stretch(width, height int) Target {
	return Target{Width: width, Height: height, Mode: preset.ResizeStretch}
}
func crop(width, height int) Target {
	return Target{Width: width, Height: height, Mode: preset.ResizeCropToFill}
}

func mustPlan(t *testing.T, sourceWidth, sourceHeight int, target Target) Geometry {
	t.Helper()
	geometry, err := Plan(sourceWidth, sourceHeight, target)
	if err != nil {
		t.Fatalf("Plan(%d, %d, %+v): %v", sourceWidth, sourceHeight, target, err)
	}
	return geometry
}

// TestPlanAcceptanceGeometry pins AC-R01, AC-R02, AC-R04, and AC-R05 at the
// geometry level: the PRD's 1000 × 500 source against a 512 × 512 preset in
// every mode, and a source smaller than the preset, which every mode
// enlarges rather than keeping the original dimensions.
func TestPlanAcceptanceGeometry(t *testing.T) {
	cases := []struct {
		name                      string
		sourceWidth, sourceHeight int
		target                    Target
		want                      Geometry
	}{
		{"AC-R01 stretch", 1000, 500, stretch(512, 512), Geometry{Width: 512, Height: 512, ScaledWidth: 512, ScaledHeight: 512, Crop: image.Rect(0, 0, 512, 512)}},
		{"AC-R02 fit landscape", 1000, 500, fit(512, 512), Geometry{Width: 512, Height: 256, ScaledWidth: 512, ScaledHeight: 256, Crop: image.Rect(0, 0, 512, 256)}},
		{"fit portrait", 500, 1000, fit(512, 512), Geometry{Width: 256, Height: 512, ScaledWidth: 256, ScaledHeight: 512, Crop: image.Rect(0, 0, 256, 512)}},
		// Scaling 1000 × 500 by 512/500 gives 1024 × 512; the centered
		// 512 columns start 256 from the left.
		{"AC-R04 crop to fill", 1000, 500, crop(512, 512), Geometry{Width: 512, Height: 512, ScaledWidth: 1024, ScaledHeight: 512, Crop: image.Rect(256, 0, 768, 512)}},
		{"crop to fill portrait", 500, 1000, crop(512, 512), Geometry{Width: 512, Height: 512, ScaledWidth: 512, ScaledHeight: 1024, Crop: image.Rect(0, 256, 512, 768)}},
		{"AC-R05 stretch enlarges", 100, 50, stretch(512, 512), Geometry{Width: 512, Height: 512, ScaledWidth: 512, ScaledHeight: 512, Crop: image.Rect(0, 0, 512, 512)}},
		{"AC-R05 fit enlarges", 100, 50, fit(512, 512), Geometry{Width: 512, Height: 256, ScaledWidth: 512, ScaledHeight: 256, Crop: image.Rect(0, 0, 512, 256)}},
		{"AC-R05 crop to fill enlarges", 100, 50, crop(512, 512), Geometry{Width: 512, Height: 512, ScaledWidth: 1024, ScaledHeight: 512, Crop: image.Rect(256, 0, 768, 512)}},
		{"square", 300, 300, fit(512, 512), Geometry{Width: 512, Height: 512, ScaledWidth: 512, ScaledHeight: 512, Crop: image.Rect(0, 0, 512, 512)}},
		{"square crop to fill", 300, 300, crop(512, 256), Geometry{Width: 512, Height: 256, ScaledWidth: 512, ScaledHeight: 512, Crop: image.Rect(0, 128, 512, 384)}},
		{"same size", 640, 480, fit(640, 480), Geometry{Width: 640, Height: 480, ScaledWidth: 640, ScaledHeight: 480, Crop: image.Rect(0, 0, 640, 480)}},
	}
	for _, tc := range cases {
		if got := mustPlan(t, tc.sourceWidth, tc.sourceHeight, tc.target); got != tc.want {
			t.Errorf("%s: Plan = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestPlanFitRoundsToTheNearestPixel pins RSZ-012: the dimension Fit does
// not take from the target is rounded to the nearest whole pixel, halves
// up, and clamped to at least one pixel.
func TestPlanFitRoundsToTheNearestPixel(t *testing.T) {
	cases := []struct {
		name                      string
		sourceWidth, sourceHeight int
		target                    Target
		wantWidth, wantHeight     int
	}{
		{"1.33 rounds down", 3, 2, fit(2, 2), 2, 1},
		{"1.2 rounds down", 5, 3, fit(2, 2), 2, 1},
		{"1.5 rounds up", 4, 3, fit(2, 2), 2, 2},
		{"0.5 rounds up", 4, 1, fit(2, 2), 2, 1},
		{"2.5 rounds up", 5, 2, fit(10, 1), 3, 1},
		{"1.6 rounds up", 5, 4, fit(2, 10), 2, 2},
		{"clamped wide", 10000, 1, fit(16, 16), 16, 1},
		{"clamped tall", 1, 10000, fit(16, 16), 1, 16},
		{"clamped extreme", MaxSourceDimension, 1, fit(1, 1), 1, 1},
		{"height limits", 1000, 1000, fit(2000, 500), 500, 500},
		{"683 from 1024 × 1536", 1024, 1536, fit(1024, 1024), 683, 1024},
	}
	for _, tc := range cases {
		got := mustPlan(t, tc.sourceWidth, tc.sourceHeight, tc.target)
		if got.Width != tc.wantWidth || got.Height != tc.wantHeight {
			t.Errorf("%s: Plan(%d × %d, %+v) = %d × %d, want %d × %d", tc.name, tc.sourceWidth, tc.sourceHeight, tc.target, got.Width, got.Height, tc.wantWidth, tc.wantHeight)
		}
	}
}

// sourceSizes spans landscape, portrait, square, extreme, and one-pixel
// sources, both smaller and larger than the targets.
var sourceSizes = [][2]int{
	{1, 1}, {1, 2}, {2, 1}, {3, 7}, {7, 3}, {16, 16}, {100, 50}, {50, 100},
	{333, 1000}, {1000, 333}, {1000, 500}, {1024, 1536}, {1920, 1080},
	{1080, 1920}, {4000, 3000}, {12345, 7}, {7, 12345}, {16384, 1},
	{1, 16384}, {MaxSourceDimension, 3}, {3, MaxSourceDimension},
}

var targetSizes = [][2]int{
	{1, 1}, {1, 16384}, {16384, 1}, {16, 16}, {512, 512}, {640, 480},
	{1920, 1080}, {1080, 1920}, {10000, 10000}, {16384, 6103},
}

// TestPlanFitProperties pins AC-R03 across mixed portrait and landscape
// sources: every Fit output lies within the target, at least 1 × 1, with
// one dimension exactly on the target and the other within half a pixel of
// the exact aspect-preserving size unless the one-pixel clamp applies.
func TestPlanFitProperties(t *testing.T) {
	for _, source := range sourceSizes {
		for _, target := range targetSizes {
			sw, sh := int64(source[0]), int64(source[1])
			g := mustPlan(t, source[0], source[1], fit(target[0], target[1]))
			w, h := int64(g.Width), int64(g.Height)
			if w < 1 || h < 1 || g.Width > target[0] || g.Height > target[1] {
				t.Fatalf("%v into %v: %d × %d is not within the target and at least 1 × 1", source, target, w, h)
			}
			if g.ScaledWidth != g.Width || g.ScaledHeight != g.Height || g.Crop != image.Rect(0, 0, g.Width, g.Height) {
				t.Fatalf("%v into %v: Fit must not crop: %+v", source, target, g)
			}
			// |h − sh·w/sw| ≤ 1/2, or the transposed condition, in integers.
			switch {
			case g.Width == target[0] && abs(2*(h*sw-sh*w)) <= sw:
			case g.Height == target[1] && abs(2*(w*sh-sw*h)) <= sh:
			case g.Width == target[0] && h == 1 && 2*sh*w < sw:
			case g.Height == target[1] && w == 1 && 2*sw*h < sh:
			default:
				t.Fatalf("%v into %v: %d × %d does not preserve the aspect ratio within rounding", source, target, w, h)
			}
		}
	}
}

// TestPlanCropToFillProperties pins RSZ-013 and RSZ-014 across mixed
// sources: the output is exactly the target, the uniformly scaled source
// covers it with one axis exactly on the target and the other within half a
// pixel of the exact scale, and the crop removes equal amounts from opposite
// sides, the odd pixel from the right or bottom.
func TestPlanCropToFillProperties(t *testing.T) {
	for _, source := range sourceSizes {
		for _, target := range targetSizes {
			sw, sh := int64(source[0]), int64(source[1])
			g := mustPlan(t, source[0], source[1], crop(target[0], target[1]))
			if g.Width != target[0] || g.Height != target[1] || g.Crop.Dx() != g.Width || g.Crop.Dy() != g.Height {
				t.Fatalf("%v into %v: output %d × %d, crop %v, want exactly the target", source, target, g.Width, g.Height, g.Crop)
			}
			W, H := int64(g.ScaledWidth), int64(g.ScaledHeight)
			switch {
			case g.ScaledWidth == target[0] && g.ScaledHeight >= target[1] && abs(2*(H*sw-sh*W)) <= sw:
			case g.ScaledHeight == target[1] && g.ScaledWidth >= target[0] && abs(2*(W*sh-sw*H)) <= sh:
			default:
				t.Fatalf("%v into %v: scaled to %d × %d, which does not cover the target at the source's aspect ratio", source, target, W, H)
			}
			left, right := g.Crop.Min.X, g.ScaledWidth-g.Crop.Max.X
			top, bottom := g.Crop.Min.Y, g.ScaledHeight-g.Crop.Max.Y
			if right-left != (g.ScaledWidth-g.Width)%2 || bottom-top != (g.ScaledHeight-g.Height)%2 {
				t.Fatalf("%v into %v: crop removes %d left, %d right, %d top, %d bottom; want centered with the odd pixel right or bottom", source, target, left, right, top, bottom)
			}
		}
	}
}

// TestPlanCropToFillRemovesTheOddPixelRightOrBottom pins RSZ-014 with small
// hand-checked cases.
func TestPlanCropToFillRemovesTheOddPixelRightOrBottom(t *testing.T) {
	cases := []struct {
		sourceWidth, sourceHeight int
		want                      image.Rectangle
	}{
		// 3 × 2 scaled by 1 is 3 × 2: one column goes, from the right.
		{3, 2, image.Rect(0, 0, 2, 2)},
		// 5 × 2: one column from the left, two from the right.
		{5, 2, image.Rect(1, 0, 3, 2)},
		// 2 × 5: one row from the top, two from the bottom.
		{2, 5, image.Rect(0, 1, 2, 3)},
		// 4 × 2: one column from each side.
		{4, 2, image.Rect(1, 0, 3, 2)},
	}
	for _, tc := range cases {
		if got := mustPlan(t, tc.sourceWidth, tc.sourceHeight, crop(2, 2)).Crop; got != tc.want {
			t.Errorf("%d × %d into 2 × 2: crop %v, want %v", tc.sourceWidth, tc.sourceHeight, got, tc.want)
		}
	}
}

// TestPlanStretchAlwaysProducesTheTarget pins RSZ-010.
func TestPlanStretchAlwaysProducesTheTarget(t *testing.T) {
	for _, source := range sourceSizes {
		for _, target := range targetSizes {
			g := mustPlan(t, source[0], source[1], stretch(target[0], target[1]))
			want := Geometry{Width: target[0], Height: target[1], ScaledWidth: target[0], ScaledHeight: target[1], Crop: image.Rect(0, 0, target[0], target[1])}
			if g != want {
				t.Fatalf("%v into %v: %+v, want %+v", source, target, g, want)
			}
		}
	}
}

// TestPlanValidatesTheTarget pins PRESET-006 at the resize boundary: 1 to
// 16,384 pixels per axis and at most 100 megapixels, and a known fitting
// mode. The rules are checked in the preset library's order — dimensions,
// mode, then area — and the target before the source.
func TestPlanValidatesTheTarget(t *testing.T) {
	valid := []Target{fit(10000, 10000), stretch(16384, 6103), crop(6103, 16384), fit(1, 1), crop(16384, 1)}
	for _, target := range valid {
		if err := target.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", target, err)
		}
	}
	invalid := []struct {
		target Target
		want   error
	}{
		{fit(10000, 10001), ErrTargetArea},
		{stretch(16384, 6104), ErrTargetArea},
		{fit(16385, 1), ErrTargetDimension},
		{fit(1, 16385), ErrTargetDimension},
		{fit(0, 512), ErrTargetDimension},
		{crop(512, -1), ErrTargetDimension},
		{Target{Width: 512, Height: 512}, ErrMode},
		{Target{Width: 512, Height: 512, Mode: "zoom"}, ErrMode},
		{Target{Width: 0, Height: 0, Mode: "zoom"}, ErrTargetDimension},
		{Target{Width: 10000, Height: 10001, Mode: "zoom"}, ErrMode},
	}
	for _, tc := range invalid {
		for _, err := range []error{tc.target.Validate(), planErr(1000, 500, tc.target), planErr(0, 0, tc.target)} {
			var resizeErr *Error
			if !errors.As(err, &resizeErr) || resizeErr.Kind != KindInvalid || !errors.Is(err, tc.want) {
				t.Errorf("%+v: error %v, want a KindInvalid error wrapping %v", tc.target, err, tc.want)
			}
		}
	}
}

// TestValidateAgreesWithThePresetLibrary checks Target.Validate against the
// preset library's own validation of a saved Image Resizer preset over every
// combination of valid and invalid widths, heights, and modes: both accept
// the same targets, and a target breaking several rules is reported for the
// same one, so reordering either check fails here.
func TestValidateAgreesWithThePresetLibrary(t *testing.T) {
	library, err := preset.NewLibrary(shell.ModuleImageResizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]error{
		fmt.Sprintf(ui.PresetDimensionRange, "Width"):  ErrTargetDimension,
		fmt.Sprintf(ui.PresetDimensionRange, "Height"): ErrTargetDimension,
		ui.PresetResizeMode:                            ErrMode,
		ui.PresetResizePixelLimit:                      ErrTargetArea,
	}
	dimensions := []int{-1, 0, 1, 6103, 16384, 16385}
	modes := []preset.ResizeMode{preset.ResizeFit, preset.ResizeStretch, preset.ResizeCropToFill, "", "zoom"}
	for _, width := range dimensions {
		for _, height := range dimensions {
			for _, mode := range modes {
				target := Target{Width: width, Height: height, Mode: mode}
				_, saveErr := library.Create(fmt.Sprintf("%d × %d %s", width, height, mode), width, height, mode)
				err := target.Validate()
				if saveErr == nil {
					if err != nil {
						t.Errorf("%+v: the library accepts it, Validate = %v", target, err)
					}
					continue
				}
				want, ok := rules[saveErr.Error()]
				if !ok {
					t.Fatalf("%+v: unexpected library error %v", target, saveErr)
				}
				if !errors.Is(err, want) {
					t.Errorf("%+v: the library reports %q, Validate = %v, want %v", target, saveErr, err, want)
				}
			}
		}
	}
}

func planErr(sourceWidth, sourceHeight int, target Target) error {
	_, err := Plan(sourceWidth, sourceHeight, target)
	return err
}

// TestPlanValidatesTheSource rejects sources without pixels as invalid and
// sources past MaxSourceDimension as too large, and accepts the bound
// itself.
func TestPlanValidatesTheSource(t *testing.T) {
	cases := []struct {
		sourceWidth, sourceHeight int
		kind                      Kind
		want                      error
	}{
		{0, 10, KindInvalid, ErrEmptySource},
		{10, 0, KindInvalid, ErrEmptySource},
		{-1, 10, KindInvalid, ErrEmptySource},
		{MaxSourceDimension + 1, 1, KindTooLarge, ErrSourceDimension},
		{1, MaxSourceDimension + 1, KindTooLarge, ErrSourceDimension},
	}
	for _, tc := range cases {
		err := planErr(tc.sourceWidth, tc.sourceHeight, fit(512, 512))
		var resizeErr *Error
		if !errors.As(err, &resizeErr) || resizeErr.Kind != tc.kind || !errors.Is(err, tc.want) {
			t.Errorf("%d × %d: error %v, want kind %v wrapping %v", tc.sourceWidth, tc.sourceHeight, err, tc.kind, tc.want)
		}
	}
	mustPlan(t, MaxSourceDimension, MaxSourceDimension, crop(16384, 6103))
}

func TestErrorPresentation(t *testing.T) {
	invalid := &Error{Kind: KindInvalid, Err: ErrMode}
	if got, want := invalid.Error(), "resize: "+ErrMode.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := (&Error{Kind: KindCanceled, Err: errors.New("context canceled")}).Error(); got != "resize canceled: context canceled" {
		t.Errorf("canceled Error() = %q", got)
	}
	for kind, want := range map[Kind]string{KindInvalid: "invalid", KindTooLarge: "too large", KindCanceled: "canceled", KindFailed: "failed", Kind(9): "Kind(9)"} {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", kind, got, want)
		}
	}
	// The messages write their limits as the preset editor does, so they
	// name the constants' values.
	if preset.MinDimension != 1 || preset.MaxDimension != 16384 || preset.MaxResizeMegapixels != 100 || MaxSourceDimension != 1_048_576 {
		t.Errorf("limits %d, %d, %d, and %d differ from the ones the messages name", preset.MinDimension, preset.MaxDimension, preset.MaxResizeMegapixels, MaxSourceDimension)
	}
	for err, want := range map[error]string{
		ErrTargetDimension: "the target width and height must each be between 1 and 16,384 pixels",
		ErrTargetArea:      "the target must not exceed 100 megapixels",
		ErrSourceDimension: "the source is wider or taller than 1,048,576 pixels",
	} {
		if got := err.Error(); got != want {
			t.Errorf("message %q, want %q", got, want)
		}
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

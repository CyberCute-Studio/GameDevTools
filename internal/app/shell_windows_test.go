//go:build windows

package app

import (
	"math"
	"testing"
)

func TestFitStartupBounds(t *testing.T) {
	tests := []struct {
		name                  string
		x, y, width, height   int32
		work                  rect
		wantX, wantY          int32
		wantWidth, wantHeight int32
	}{
		{
			name: "negative monitor origin", x: 0, y: 0, width: 1920, height: 1080,
			work:  rect{Left: -1920, Top: -1080, Right: 0, Bottom: 0},
			wantX: -1920, wantY: -1080, wantWidth: 1920, wantHeight: 1080,
		},
		{
			name: "clamps to small work area", x: 100, y: 100, width: 1100, height: 700,
			work:  rect{Left: 0, Top: 0, Right: 800, Bottom: 600},
			wantX: 0, wantY: 0, wantWidth: 800, wantHeight: 600,
		},
		{
			name: "wide intermediate at right and bottom limits", x: math.MaxInt32 - 50, y: math.MaxInt32 - 50, width: 100, height: 100,
			work:  rect{Left: math.MaxInt32 - 1000, Top: math.MaxInt32 - 1000, Right: math.MaxInt32, Bottom: math.MaxInt32},
			wantX: math.MaxInt32 - 100, wantY: math.MaxInt32 - 100, wantWidth: 100, wantHeight: 100,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y, width, height := fitStartupBounds(test.x, test.y, test.width, test.height, test.work)
			if x != test.wantX || y != test.wantY || width != test.wantWidth || height != test.wantHeight {
				t.Fatalf("fitStartupBounds() = (%d, %d, %d, %d), want (%d, %d, %d, %d)", x, y, width, height, test.wantX, test.wantY, test.wantWidth, test.wantHeight)
			}
		})
	}
}

func TestCalculateShellLayout(t *testing.T) {
	t.Run("standard window keeps navigation beside content", func(t *testing.T) {
		plan := calculateShellLayout(rect{Right: 1100, Bottom: 700}, 96, 3, shellTextHeights{})
		if plan.compact {
			t.Fatal("standard window uses compact navigation")
		}
		if plan.virtualHeight != 700 {
			t.Fatalf("virtual height = %d, want 700", plan.virtualHeight)
		}
	})

	t.Run("high DPI uses full-width navigation and scrolling", func(t *testing.T) {
		client := rect{Right: 1920, Bottom: 970}
		plan := calculateShellLayout(client, 480, 3, shellTextHeights{})
		if !plan.compact {
			t.Fatal("high-DPI window retains clipped side navigation")
		}
		if plan.navWidth != plan.contentWidth {
			t.Fatalf("navigation width = %d, content width = %d", plan.navWidth, plan.contentWidth)
		}
		navBottom := plan.navY + 3*plan.navHeight + 2*40
		if navBottom > plan.virtualHeight-plan.margin {
			t.Fatalf("navigation bottom = %d, scrollable bottom = %d", navBottom, plan.virtualHeight-plan.margin)
		}
		if actionBottom := plan.actionY + 220; actionBottom > plan.virtualHeight-plan.margin {
			t.Fatalf("primary action bottom = %d, scrollable bottom = %d", actionBottom, plan.virtualHeight-plan.margin)
		}
		if plan.virtualHeight <= client.Bottom {
			t.Fatalf("virtual height = %d, want scrolling beyond client height %d", plan.virtualHeight, client.Bottom)
		}
	})

	t.Run("wrapped text contributes its measured height", func(t *testing.T) {
		text := shellTextHeights{title: 240, description: 900, note: 400}
		plan := calculateShellLayout(rect{Right: 1280, Bottom: 650}, 480, 3, text)
		if plan.titleHeight != text.title || plan.descriptionHeight != text.description || plan.noteHeight != text.note {
			t.Fatalf("layout text heights = %+v, want %+v", plan, text)
		}
		if noteBottom := plan.noteY + plan.noteHeight; noteBottom != plan.virtualHeight-plan.margin {
			t.Fatalf("note bottom = %d, scrollable bottom = %d", noteBottom, plan.virtualHeight-plan.margin)
		}
	})
}

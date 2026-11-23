//go:build windows

package app

import (
	"strings"
	"testing"
)

// TestMeasureTextCountsTheLinesOfALongPath pins that a token wider than the
// width is measured as the wrapped lines the status area draws, so the layout
// gives it the height to show all of it.
func TestMeasureTextCountsTheLinesOfALongPath(t *testing.T) {
	shell := newCommandTestShell(t, nil)
	const width = 120
	line := shell.measureText("C", width, 0)
	if line <= 0 {
		t.Fatalf("one line measured %d", line)
	}
	path := `C:\` + strings.Repeat("LongFolderName", 12) + `\shot.png`
	if got := shell.measureText(path, width, 0); got < 2*line {
		t.Fatalf("a path wider than %d measured %d, one line is %d", width, got, line)
	}
}

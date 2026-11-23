package ui

import (
	"slices"
	"time"
)

// cardSuccessDuration is how long the result card shows a result that needs
// nothing from the user. A warning or error stays until it is dismissed.
const cardSuccessDuration = 6 * time.Second

// CardEntry is one result on the result card: its notice, and the saved file
// Open Folder shows, if there is one. Setting marks a problem with a setting,
// which no longer applies once the default settings are restored.
type CardEntry struct {
	Notice  Notice
	Path    string
	Setting bool
}

// lasting reports whether the entry stays until the user dismisses it, as a
// problem does, rather than leaving by itself.
func (e CardEntry) lasting() bool {
	return e.Notice.Kind >= NoticeWarning
}

// CardPlacement is the work area of the display the result card appears on,
// in physical pixels, and that display's DPI. The zero value places the card
// at the primary display's top-left corner at 96 DPI.
type CardPlacement struct {
	Left, Top, Right, Bottom int32
	DPI                      uint32
}

// position returns where a card of the given size goes: the bottom-right
// corner of the work area, margin from its edges. In a work area too small
// for it, the top-left of the card stays on screen, where its message starts.
func (p CardPlacement) position(width, height, margin int32) (x, y int32) {
	return max(p.Right-width-margin, p.Left+margin), max(p.Bottom-height-margin, p.Top+margin)
}

// cardQueue is what the result card has to show. Problems wait, oldest first,
// until each is dismissed, so a later result never hides an earlier failure.
// A success is shown over them and leaves by itself, and the problems then
// show again.
type cardQueue struct {
	problems []CardEntry
	passing  *CardEntry
}

// push adds a result. A problem identical to the newest one, as the same
// refused start repeated, is not listed twice.
func (q *cardQueue) push(entry CardEntry) {
	if !entry.lasting() {
		q.passing = &entry
		return
	}
	q.passing = nil
	if last := len(q.problems) - 1; last >= 0 && q.problems[last] == entry {
		return
	}
	q.problems = append(q.problems, entry)
}

// current returns the result to show: a passing success, or else the newest
// problem.
func (q *cardQueue) current() (CardEntry, bool) {
	if q.passing != nil {
		return *q.passing, true
	}
	if last := len(q.problems) - 1; last >= 0 {
		return q.problems[last], true
	}
	return CardEntry{}, false
}

// waiting returns how many problems stand behind the result being shown.
func (q *cardQueue) waiting() int {
	if q.passing != nil {
		return len(q.problems)
	}
	return max(len(q.problems)-1, 0)
}

// dismiss removes the result being shown.
func (q *cardQueue) dismiss() {
	if q.passing != nil {
		q.passing = nil
		return
	}
	if last := len(q.problems) - 1; last >= 0 {
		q.problems = q.problems[:last]
	}
}

// expire removes a passing success and reports whether there was one.
func (q *cardQueue) expire() bool {
	expired := q.passing != nil
	q.passing = nil
	return expired
}

// withdrawSettings removes the problems with a setting and reports whether
// there were any.
func (q *cardQueue) withdrawSettings() bool {
	before := len(q.problems)
	q.problems = slices.DeleteFunc(q.problems, func(entry CardEntry) bool { return entry.Setting })
	return len(q.problems) != before
}

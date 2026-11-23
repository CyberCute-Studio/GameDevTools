package ui

import "testing"

func cardEntry(kind NoticeKind, message string) CardEntry {
	return CardEntry{Notice: Notice{Kind: kind, Message: message}}
}

func TestCardQueueKeepsProblemsBehindALaterSuccess(t *testing.T) {
	var queue cardQueue
	failed := cardEntry(NoticeError, "first failed")
	queue.push(failed)
	queue.push(cardEntry(NoticeSuccess, "second saved"))

	got, ok := queue.current()
	if !ok || got.Notice.Message != "second saved" {
		t.Fatalf("current = %+v, %v, want the success", got, ok)
	}
	if queue.waiting() != 1 {
		t.Fatalf("waiting = %d, want the failure behind the success", queue.waiting())
	}
	if !queue.expire() {
		t.Fatal("the success did not expire")
	}
	if got, ok := queue.current(); !ok || got != failed {
		t.Fatalf("after the success left, current = %+v, %v, want the failure", got, ok)
	}
	if queue.expire() {
		t.Fatal("a failure expired by itself")
	}
}

func TestCardQueueDismissesProblemsNewestFirst(t *testing.T) {
	var queue cardQueue
	queue.push(cardEntry(NoticeError, "one"))
	queue.push(cardEntry(NoticeWarning, "two"))
	if got, _ := queue.current(); got.Notice.Message != "two" || queue.waiting() != 1 {
		t.Fatalf("current = %q with %d waiting", got.Notice.Message, queue.waiting())
	}
	queue.dismiss()
	if got, _ := queue.current(); got.Notice.Message != "one" || queue.waiting() != 0 {
		t.Fatalf("after dismiss, current = %q with %d waiting", got.Notice.Message, queue.waiting())
	}
	queue.dismiss()
	if _, ok := queue.current(); ok {
		t.Fatal("the queue still has a result")
	}
	queue.dismiss()
}

func TestCardQueueListsARepeatedProblemOnce(t *testing.T) {
	var queue cardQueue
	queue.push(cardEntry(NoticeError, "refused"))
	queue.push(cardEntry(NoticeError, "refused"))
	if len(queue.problems) != 1 {
		t.Fatalf("problems = %d, want a repeat listed once", len(queue.problems))
	}
	queue.push(cardEntry(NoticeError, "other"))
	queue.push(cardEntry(NoticeError, "refused"))
	if len(queue.problems) != 3 {
		t.Fatalf("problems = %d, want a non-adjacent repeat kept", len(queue.problems))
	}
}

func TestCardQueueTakeReturnsProblemsOldestFirst(t *testing.T) {
	var queue cardQueue
	queue.push(cardEntry(NoticeError, "one"))
	queue.push(cardEntry(NoticeWarning, "two"))
	queue.push(cardEntry(NoticeSuccess, "saved"))
	taken := queue.take()
	if len(taken) != 2 || taken[0].Notice.Message != "one" || taken[1].Notice.Message != "two" {
		t.Fatalf("take = %+v", taken)
	}
	if _, ok := queue.current(); ok {
		t.Fatal("take left a result in the queue")
	}
}

func TestCardPlacementKeepsTheCardOnTheWorkArea(t *testing.T) {
	tests := []struct {
		name  string
		area  CardPlacement
		wantX int32
		wantY int32
	}{
		{"bottom right", CardPlacement{Right: 1920, Bottom: 1040}, 1544, 904},
		{"negative coordinates", CardPlacement{Left: -1920, Top: -200, Right: 0, Bottom: 880}, -376, 744},
		{"too small keeps the top left", CardPlacement{Left: 100, Top: 100, Right: 300, Bottom: 200}, 116, 116},
		{"unknown display", CardPlacement{}, 16, 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y := test.area.position(360, 120, 16)
			if x != test.wantX || y != test.wantY {
				t.Fatalf("position = (%d, %d), want (%d, %d)", x, y, test.wantX, test.wantY)
			}
		})
	}
}

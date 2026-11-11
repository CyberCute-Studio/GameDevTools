package ui

import "testing"

func TestNoticeTextUsesExplicitStatus(t *testing.T) {
	tests := []struct {
		kind NoticeKind
		want string
	}{
		{kind: NoticeInfo, want: "Information: Saved"},
		{kind: NoticeSuccess, want: "Success: Saved"},
		{kind: NoticeWarning, want: "Warning: Saved"},
		{kind: NoticeError, want: "Error: Saved"},
	}
	for _, test := range tests {
		notice := Notice{Kind: test.kind, Message: "Saved"}
		if got := notice.Text(); got != test.want {
			t.Fatalf("Notice{%v}.Text() = %q, want %q", test.kind, got, test.want)
		}
	}
}

func TestEmptyNoticeIsHidden(t *testing.T) {
	if got := (Notice{Kind: NoticeSuccess}).Text(); got != "" {
		t.Fatalf("empty Notice.Text() = %q", got)
	}
}

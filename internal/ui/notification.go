package ui

type NoticeKind uint8

const (
	NoticeInfo NoticeKind = iota
	NoticeSuccess
	NoticeWarning
	NoticeError
)

type Notice struct {
	Kind    NoticeKind
	Message string
}

// Text returns the notice with its severity spelled out, so a result never
// depends on color alone (UX-005).
func (n Notice) Text() string {
	if n.Message == "" {
		return ""
	}
	label := NoticeInfoLabel
	switch n.Kind {
	case NoticeSuccess:
		label = NoticeSuccessLabel
	case NoticeWarning:
		label = NoticeWarningLabel
	case NoticeError:
		label = NoticeErrorLabel
	}
	return label + LabelSeparator + n.Message
}

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

func (n Notice) Text() string {
	if n.Message == "" {
		return ""
	}
	label := "Information"
	switch n.Kind {
	case NoticeSuccess:
		label = "Success"
	case NoticeWarning:
		label = "Warning"
	case NoticeError:
		label = "Error"
	}
	return label + ": " + n.Message
}

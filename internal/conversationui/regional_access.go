package conversationui

import (
	"slices"
	"strings"

	"shioji/internal/domain"
)

const (
	regionalAccessWarning = "⚠ FAILED_PRECONDITION (code 400): User location is not supported for the API use."
	regionalAccessTitle   = "当前地区不支持使用此 API"
	regionalAccessCaption = "FAILED_PRECONDITION · HTTP 400"
)

func latestAntigravityReply(detail domain.SessionDetail, harnessKind string) (domain.Turn, domain.Item) {
	if harnessKind != "antigravity" || detail.Session.Archived ||
		detail.History != nil && (detail.History.Newer || detail.History.Invalidated) {
		return domain.Turn{}, domain.Item{}
	}
	var latest domain.Turn
	for _, turn := range detail.Turns {
		if turn.Presentation != "compaction" && turn.Order >= latest.Order {
			latest = turn
		}
	}
	if latest.ID == "" || (latest.Status != domain.ExecutionCompleted && latest.Status != domain.ExecutionFailed) {
		return domain.Turn{}, domain.Item{}
	}
	for _, item := range slices.Backward(detail.Items) {
		if item.TurnID == latest.ID && item.WorkID == "" && item.Kind == "assistant" {
			return latest, item
		}
	}
	return domain.Turn{}, domain.Item{}
}

// SplitRegionalAccessWarning recognizes the standalone warning that Antigravity
// appends to a reply. It only changes display so saved history and copying still
// use the original text.
func SplitRegionalAccessWarning(item domain.Item) (body string, warning bool) {
	if item.Final {
		text := strings.TrimRight(item.Text, "\r\n")
		start := strings.LastIndexByte(text, '\n') + 1
		if text[start:] == regionalAccessWarning {
			return strings.TrimRight(text[:start], "\r\n"), true
		}
	}
	return item.Text, false
}

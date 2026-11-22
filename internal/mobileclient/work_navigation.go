package mobileclient

import (
	"slices"
	"strings"

	"shioji/internal/domain"
)

// WorkSourceAnchor maps the hidden work event to the closest visible context
// in its parent turn. It prefers preceding context and never crosses turns.
func WorkSourceAnchor(detail domain.SessionDetail, work domain.WorkActivity) string {
	if work.SourceItemID == "" {
		return ""
	}
	at := slices.IndexFunc(detail.Items, func(item domain.Item) bool { return item.ID == work.SourceItemID })
	if at < 0 {
		return ""
	}
	source := detail.Items[at]
	turn := source.TurnID
	if turn == "" {
		turn = work.TurnID
	}
	visible := make(map[string]bool)
	for _, item := range ConversationItems(detail) {
		if item.WorkID == "" && (item.Kind == "user" || item.Kind == "assistant" || item.Kind == "tool" || item.Kind == "activity") {
			visible[item.ID] = true
		}
	}
	if turn == "" {
		return ""
	}
	for _, item := range slices.Backward(detail.Items[:at]) {
		if item.TurnID == turn && visible[item.ID] {
			return item.ID
		}
	}
	for _, item := range detail.Items[at+1:] {
		if item.TurnID == turn && visible[item.ID] {
			return item.ID
		}
	}
	return ""
}

// BackgroundWorkOutput is the terminal body and clipboard payload. The
// command header is presentation metadata and is not part of the output.
func BackgroundWorkOutput(items []domain.Item, latest string) string {
	var lines []string
	for _, item := range items {
		if item.Text != "" {
			lines = append(lines, item.Text)
		}
	}
	if len(lines) == 0 {
		return latest
	}
	return strings.Join(lines, "\n")
}

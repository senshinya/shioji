package conversationui

import (
	"strings"

	"shioji/internal/domain"
)

// ReasoningForDisplay drops trailing line breaks from each rendered summary
// block while leaving the recorded text and its interior line breaks intact.
func ReasoningForDisplay(item domain.Item) domain.Item {
	if item.Presentation != "reasoning" {
		return item
	}
	item.Text = trimTrailingLineBreaks(item.Text)
	if len(item.Parts) == 0 {
		return item
	}
	parts := make([]domain.ContentPart, 0, len(item.Parts))
	for _, part := range item.Parts {
		if part.Attachment == nil && (part.Kind == "text" || part.Kind == "") {
			part.Text = trimTrailingLineBreaks(part.Text)
			if part.Text == "" && part.Label == "" {
				continue
			}
		}
		parts = append(parts, part)
	}
	item.Parts = parts
	return item
}

func trimTrailingLineBreaks(text string) string {
	content := strings.TrimRight(text, " \t\r\n")
	lineBreak := strings.IndexAny(text[len(content):], "\r\n")
	if lineBreak < 0 {
		return text
	}
	if content == "" {
		return ""
	}
	return text[:len(content)+lineBreak]
}

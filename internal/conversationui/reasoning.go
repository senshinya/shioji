package conversationui

import (
	"strings"

	"shioji/internal/domain"
)

// ThinkingIconPath is shared by the native tool timelines.
const ThinkingIconPath = "M10 6.7C9.4 5.4 8.1 4.5 6.7 4.5C4.9 4.5 3.8 5.7 3.9 7.4C2.2 8.1 2 10.8 3.6 11.8C3.4 13.8 5 15.5 7 15.5C8.3 15.5 9.3 15 10 13.9C10.7 15 11.7 15.5 13 15.5C15 15.5 16.6 13.8 16.4 11.8C18 10.8 17.8 8.1 16.1 7.4C16.2 5.7 15.1 4.5 13.3 4.5C11.9 4.5 10.6 5.4 10 6.7V13.9M3.9 7.4c1.3-.1 2.4.7 2.6 1.8M16.1 7.4c-1.3-.1-2.4.7-2.6 1.8M3.6 11.8c1.3.4 2.4-.1 2.9-1M16.4 11.8c-1.3.4-2.4-.1-2.9-1"

// ReasoningSummaryText reads both streamed summary text and structured
// summary paragraphs. It leaves the journal text available for search.
func ReasoningSummaryText(item domain.Item) string {
	paragraphs := make([]string, 0, 1+len(item.Parts))
	if text := trimTrailingLineBreaks(item.Text); strings.TrimSpace(text) != "" {
		paragraphs = append(paragraphs, text)
	}
	for _, part := range item.Parts {
		if part.Attachment == nil {
			if text := trimTrailingLineBreaks(part.Text); strings.TrimSpace(text) != "" {
				paragraphs = append(paragraphs, text)
			}
		}
	}
	if item.Truncated {
		paragraphs = append(paragraphs, "内容过长，已截断显示")
	}
	return strings.Join(paragraphs, "\n\n")
}

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

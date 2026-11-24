package conversationui

import (
	"strings"
	"time"

	"shioji/internal/domain"
)

// SplitAntigravityQuotaWarning recognizes the footer agy emitted as reply text.
// The original item remains available to history and copy actions.
func SplitAntigravityQuotaWarning(harnessID string, item domain.Item) (body string, retryAfter *time.Duration, found bool) {
	if harnessID != "antigravity" || item.Kind != "assistant" || !item.Final {
		return item.Text, nil, false
	}
	text := strings.TrimRight(item.Text, "\r\n")
	start := strings.LastIndexByte(text, '\n') + 1
	suffix, ok := strings.CutPrefix(text[start:], "⚠ Individual quota reached.")
	if !ok {
		return item.Text, nil, false
	}
	suffix = strings.TrimPrefix(suffix, " Please upgrade your subscription to increase your limits.")
	if suffix != "" {
		delay, ok := strings.CutPrefix(suffix, " Resets in ")
		if !ok {
			return item.Text, nil, false
		}
		if parsed, err := time.ParseDuration(strings.TrimSuffix(delay, ".")); err == nil && parsed >= 0 {
			retryAfter = &parsed
		}
	}
	return strings.TrimRight(text[:start], "\r\n"), retryAfter, true
}

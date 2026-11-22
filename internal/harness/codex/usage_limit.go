package codex

import (
	"regexp"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

var (
	usageRetrySuffix = regexp.MustCompile(`[Tt]ry again at (.+)$`)
	usageRetryDate   = regexp.MustCompile(`^([A-Z][a-z]{2} [0-9]{1,2})(?:st|nd|rd|th)(, [0-9]{4} [0-9]{1,2}:[0-9]{2} [AP]M)$`)
)

func isUsageLimit(err *wire.TurnError) bool {
	return err != nil && string(err.CodexErrorInfo) == `"usageLimitExceeded"`
}

func applyTurnError(turn *domain.Turn, err *wire.TurnError) {
	turn.Error = boundedJSONText(err.Message, turnErrorJSONLimit)
	turn.UsageLimit = nil
	if isUsageLimit(err) {
		turn.UsageLimit = &domain.UsageLimit{ResetAt: usageResetTime(err.Message, turn.CompletedAt)}
	}
}

// Codex renders the retry suffix in the execution device's local time. A
// time-only suffix refers to the local date of that failure, never today
// on the controlling client or the next matching clock time.
func usageResetTime(message string, completed *time.Time) *time.Time {
	match := usageRetrySuffix.FindStringSubmatch(strings.TrimSpace(message))
	if match == nil {
		return nil
	}
	value := strings.TrimSuffix(match[1], ".")
	layout := "Jan 2, 2006 3:04 PM"
	if parts := usageRetryDate.FindStringSubmatch(value); parts != nil {
		value = parts[1] + parts[2]
	} else {
		if completed == nil || completed.IsZero() {
			return nil
		}
		layout = "2006-01-02 3:04 PM"
		value = completed.In(time.Local).Format("2006-01-02") + " " + value
	}
	reset, err := time.ParseInLocation(layout, value, time.Local)
	if err != nil || completed != nil && !reset.After(*completed) {
		return nil
	}
	utc := reset.UTC()
	return &utc
}

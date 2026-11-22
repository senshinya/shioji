package app

import (
	"time"

	"shioji/internal/domain"
)

// UsageLimitForSession projects only the latest conversation turn. Historical
// failures and other sessions on the same device cannot show a notice here.
func (m *Model) UsageLimitForSession(sid string, now time.Time) domain.Turn {
	if sid == "" {
		return domain.Turn{}
	}
	detail := m.Details[sid]
	status := detail.Session.Status
	if detail.Session.Archived || (status != domain.ExecutionFailed && status != domain.ExecutionStarting) ||
		detail.History != nil && (detail.History.Newer || detail.History.Invalidated) {
		return domain.Turn{}
	}
	var latest domain.Turn
	for _, turn := range detail.Turns {
		if turn.Presentation != "compaction" && turn.Order >= latest.Order {
			latest = turn
		}
	}
	if latest.Status != domain.ExecutionFailed || latest.UsageLimit == nil {
		return domain.Turn{}
	}
	if reset := latest.UsageLimit.ResetAt; reset != nil && !now.Before(*reset) {
		return domain.Turn{}
	}
	return latest
}

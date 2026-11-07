package domain

import "time"

// ContextUsage is the latest harness-reported context footprint, independent
// of cumulative billing counters. Missing counts remain unknown.
type ContextUsage struct {
	TurnID      string    `json:"turnId"`
	UsedTokens  *int64    `json:"usedTokens,omitempty"`
	LimitTokens *int64    `json:"limitTokens,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

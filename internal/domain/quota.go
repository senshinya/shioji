package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	QuotaKindCodex       = "codex"
	QuotaKindAntigravity = "antigravity"
	QuotaKindDeepSeek    = "deepseek"
)

const (
	QuotaSourcePoll = "poll"
	QuotaSourcePush = "push"
)

// QuotaWindow represents one dynamically returned rate-limit window.
// Labels and window durations are never hardcoded to fixed 5h/7d buckets.
type QuotaWindow struct {
	ID               string     `json:"id,omitempty"`
	Label            string     `json:"label,omitempty"`
	WindowMinutes    *int64     `json:"windowMinutes,omitempty"`
	UsedPercent      *float64   `json:"usedPercent,omitempty"`
	RemainingPercent *float64   `json:"remainingPercent,omitempty"`
	ResetAt          *time.Time `json:"resetAt,omitempty"`
}

// DisplayLabel formats the window label strictly from reported metadata.
func (w QuotaWindow) DisplayLabel() string {
	if label := strings.TrimSpace(w.Label); label != "" {
		return label
	}
	if w.WindowMinutes == nil || *w.WindowMinutes <= 0 {
		return "额度窗口"
	}
	return FormatQuotaWindowMinutes(*w.WindowMinutes)
}

// EffectiveRemainingPercent returns a clamped 0..100 remaining percentage when available.
func (w QuotaWindow) EffectiveRemainingPercent() (float64, bool) {
	if w.RemainingPercent != nil && !math.IsNaN(*w.RemainingPercent) && !math.IsInf(*w.RemainingPercent, 0) {
		return clampQuotaPercent(*w.RemainingPercent), true
	}
	if w.UsedPercent != nil && !math.IsNaN(*w.UsedPercent) && !math.IsInf(*w.UsedPercent, 0) {
		return clampQuotaPercent(100 - *w.UsedPercent), true
	}
	return 0, false
}

func FormatQuotaWindowMinutes(mins int64) string {
	if mins <= 0 {
		return "额度窗口"
	}
	if mins%(24*60) == 0 {
		return fmt.Sprintf("%dd", mins/(24*60))
	}
	if mins%60 == 0 {
		return fmt.Sprintf("%dh", mins/60)
	}
	return fmt.Sprintf("%dm", mins)
}

func clampQuotaPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// QuotaWindowGroup holds a named subset of windows (e.g., Antigravity model groups
// or Codex rateLimitsByLimitId additional limits).
type QuotaWindowGroup struct {
	ID      string        `json:"id,omitempty"`
	Name    string        `json:"name,omitempty"`
	LimitID string        `json:"limitId,omitempty"`
	Windows []QuotaWindow `json:"windows,omitempty"`
}

// QuotaCredits represents Codex account credits when returned by the API.
type QuotaCredits struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited,omitempty"`
	Balance    string `json:"balance,omitempty"`
}

// QuotaSpendLimit represents Codex individual spend control limits when returned.
type QuotaSpendLimit struct {
	Used             string     `json:"used,omitempty"`
	Limit            string     `json:"limit,omitempty"`
	RemainingPercent *int       `json:"remainingPercent,omitempty"`
	ResetAt          *time.Time `json:"resetAt,omitempty"`
}

// QuotaBalanceEntry represents one currency entry from DeepSeek /user/balance.
type QuotaBalanceEntry struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"totalBalance"`
	GrantedBalance  string `json:"grantedBalance,omitempty"`
	ToppedUpBalance string `json:"toppedUpBalance,omitempty"`
}

// QuotaBalance holds balance-only provider data (no progress bar, no reset time).
type QuotaBalance struct {
	Available bool                `json:"available"`
	Entries   []QuotaBalanceEntry `json:"entries,omitempty"`
}

// QuotaSnapshot captures the actual fields reported by one supported Agent Profile.
type QuotaSnapshot struct {
	HarnessID            string             `json:"harnessId"`
	Kind                 string             `json:"kind"`
	Source               string             `json:"source,omitempty"`
	Version              int64              `json:"version,omitempty"`
	FetchedAt            time.Time          `json:"fetchedAt"`
	PlanType             string             `json:"planType,omitempty"`
	OrdinaryUsageAllowed *bool              `json:"ordinaryUsageAllowed,omitempty"`
	SpendControlReached  bool               `json:"spendControlReached,omitempty"`
	Windows              []QuotaWindow      `json:"windows,omitempty"`
	Groups               []QuotaWindowGroup `json:"groups,omitempty"`
	Credits              *QuotaCredits      `json:"credits,omitempty"`
	SpendLimit           *QuotaSpendLimit   `json:"spendLimit,omitempty"`
	Balance              *QuotaBalance      `json:"balance,omitempty"`
}

// Clone returns a deep copy safe for concurrent readers.
func (s QuotaSnapshot) Clone() QuotaSnapshot {
	out := s
	if s.OrdinaryUsageAllowed != nil {
		v := *s.OrdinaryUsageAllowed
		out.OrdinaryUsageAllowed = &v
	}
	if len(s.Windows) > 0 {
		out.Windows = make([]QuotaWindow, len(s.Windows))
		for i, w := range s.Windows {
			out.Windows[i] = cloneQuotaWindow(w)
		}
	}
	if len(s.Groups) > 0 {
		out.Groups = make([]QuotaWindowGroup, len(s.Groups))
		for i, g := range s.Groups {
			out.Groups[i] = QuotaWindowGroup{ID: g.ID, Name: g.Name, LimitID: g.LimitID}
			if len(g.Windows) > 0 {
				out.Groups[i].Windows = make([]QuotaWindow, len(g.Windows))
				for j, w := range g.Windows {
					out.Groups[i].Windows[j] = cloneQuotaWindow(w)
				}
			}
		}
	}
	if s.Credits != nil {
		c := *s.Credits
		out.Credits = &c
	}
	if s.SpendLimit != nil {
		l := *s.SpendLimit
		if s.SpendLimit.RemainingPercent != nil {
			p := *s.SpendLimit.RemainingPercent
			l.RemainingPercent = &p
		}
		if s.SpendLimit.ResetAt != nil {
			t := *s.SpendLimit.ResetAt
			l.ResetAt = &t
		}
		out.SpendLimit = &l
	}
	if s.Balance != nil {
		b := QuotaBalance{Available: s.Balance.Available}
		if len(s.Balance.Entries) > 0 {
			b.Entries = append([]QuotaBalanceEntry(nil), s.Balance.Entries...)
		}
		out.Balance = &b
	}
	return out
}

func cloneQuotaWindow(w QuotaWindow) QuotaWindow {
	out := w
	if w.WindowMinutes != nil {
		v := *w.WindowMinutes
		out.WindowMinutes = &v
	}
	if w.UsedPercent != nil {
		v := *w.UsedPercent
		out.UsedPercent = &v
	}
	if w.RemainingPercent != nil {
		v := *w.RemainingPercent
		out.RemainingPercent = &v
	}
	if w.ResetAt != nil {
		v := *w.ResetAt
		out.ResetAt = &v
	}
	return out
}

// QuotaSupportKind classifies whether a HarnessInfo supports quota querying:
//   - Codex: subscription account mode only (system or custom account profile; skip API Key mode)
//   - Antigravity: system default profile only (custom profiles excluded)
//   - DeepSeek: all DeepSeek profiles (balance only)
func QuotaSupportKind(h HarnessInfo) string {
	if h.ID == "" || h.IsCustomProfileProvider() {
		return ""
	}
	if h.Profile != nil {
		if h.Profile.Disabled {
			return ""
		}
		switch h.Profile.HarnessID {
		case QuotaKindCodex:
			if h.Profile.Mode == AgentProfileAccount && (h.Account == nil || h.Account.Mode != "api") {
				return QuotaKindCodex
			}
		case QuotaKindDeepSeek:
			return QuotaKindDeepSeek
		}
		// Antigravity custom profiles and all other custom profiles are excluded.
		return ""
	}
	switch h.ID {
	case QuotaKindCodex:
		if !h.HasSystemProfile() {
			return ""
		}
		if h.Account != nil && h.Account.Mode == "api" {
			return ""
		}
		return QuotaKindCodex
	case QuotaKindAntigravity:
		if !h.HasSystemProfile() {
			return ""
		}
		return QuotaKindAntigravity
	case QuotaKindDeepSeek:
		if !h.HasSystemProfile() && !h.Configured {
			return ""
		}
		return QuotaKindDeepSeek
	}
	return ""
}

// SupportsQuota reports whether h is eligible for the left-sidebar quota panel.
func SupportsQuota(h HarnessInfo) bool {
	return QuotaSupportKind(h) != ""
}

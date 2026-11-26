package domain

import (
	"testing"
	"time"
)

func TestQuotaSupportKind(t *testing.T) {
	cases := []struct {
		name string
		info HarnessInfo
		want string
	}{
		{
			name: "codex system subscription default",
			info: HarnessInfo{ID: "codex", Name: "Codex", State: RuntimeReady, Account: &RuntimeAccount{Mode: "chatgpt", State: "authenticated"}},
			want: QuotaKindCodex,
		},
		{
			name: "codex system ready without account probe yet",
			info: HarnessInfo{ID: "codex", Name: "Codex", State: RuntimeReady},
			want: QuotaKindCodex,
		},
		{
			name: "codex system uninstalled placeholder excluded",
			info: HarnessInfo{ID: "codex", Name: "Codex", State: RuntimeUnavailable},
			want: "",
		},
		{
			name: "codex system api key excluded",
			info: HarnessInfo{ID: "codex", Name: "Codex", State: RuntimeReady, Account: &RuntimeAccount{Mode: "api", State: "configured"}},
			want: "",
		},
		{
			name: "codex custom account profile supported",
			info: HarnessInfo{
				ID:      "codex-work",
				Name:    "Codex Work",
				Profile: &AgentProfile{ID: "codex-work", HarnessID: "codex", Mode: AgentProfileAccount},
				Account: &RuntimeAccount{Mode: "chatgpt", State: "authenticated"},
			},
			want: QuotaKindCodex,
		},
		{
			name: "codex custom api profile excluded",
			info: HarnessInfo{
				ID:      "codex-api",
				Name:    "Codex API",
				Profile: &AgentProfile{ID: "codex-api", HarnessID: "codex", Mode: AgentProfileAPI},
			},
			want: "",
		},
		{
			name: "codex custom disabled profile excluded",
			info: HarnessInfo{
				ID:      "codex-off",
				Name:    "Codex Off",
				Profile: &AgentProfile{ID: "codex-off", HarnessID: "codex", Mode: AgentProfileAccount, Disabled: true},
			},
			want: "",
		},
		{
			name: "antigravity system default supported",
			info: HarnessInfo{ID: "antigravity", Name: "Antigravity", State: RuntimeReady},
			want: QuotaKindAntigravity,
		},
		{
			name: "antigravity custom profile excluded",
			info: HarnessInfo{
				ID:      "agy-custom",
				Name:    "AGY Custom",
				Profile: &AgentProfile{ID: "agy-custom", HarnessID: "antigravity", Mode: AgentProfileAccount},
			},
			want: "",
		},
		{
			name: "deepseek system supported",
			info: HarnessInfo{ID: "deepseek", Name: "DeepSeek", Configured: true},
			want: QuotaKindDeepSeek,
		},
		{
			name: "deepseek custom api profile supported",
			info: HarnessInfo{
				ID:      "ds-prod",
				Name:    "DeepSeek Prod",
				Profile: &AgentProfile{ID: "ds-prod", HarnessID: "deepseek", Mode: AgentProfileAPI},
			},
			want: QuotaKindDeepSeek,
		},
		{
			name: "claude excluded",
			info: HarnessInfo{ID: "claude", Name: "Claude Code"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := QuotaSupportKind(tc.info); got != tc.want {
				t.Fatalf("QuotaSupportKind(%+v) = %q, want %q", tc.info, got, tc.want)
			}
			if SupportsQuota(tc.info) != (tc.want != "") {
				t.Fatalf("SupportsQuota mismatch for %s", tc.name)
			}
		})
	}
}

func TestQuotaWindowDisplayAndEffectivePercent(t *testing.T) {
	m300 := int64(300)
	m10080 := int64(10080)
	m90 := int64(90)
	used35 := 35.0
	rem120 := 120.0

	if got := (QuotaWindow{WindowMinutes: &m300}).DisplayLabel(); got != "5h" {
		t.Fatalf("got %q, want 5h", got)
	}
	if got := (QuotaWindow{WindowMinutes: &m10080}).DisplayLabel(); got != "7d" {
		t.Fatalf("got %q, want 7d", got)
	}
	if got := (QuotaWindow{WindowMinutes: &m90}).DisplayLabel(); got != "90m" {
		t.Fatalf("got %q, want 90m", got)
	}
	if got := (QuotaWindow{Label: "Gemini Pro", WindowMinutes: &m300}).DisplayLabel(); got != "Gemini Pro" {
		t.Fatalf("got %q, want Gemini Pro", got)
	}

	if rem, ok := (QuotaWindow{UsedPercent: &used35}).EffectiveRemainingPercent(); !ok || rem != 65 {
		t.Fatalf("got (%v, %v), want (65, true)", rem, ok)
	}
	if rem, ok := (QuotaWindow{RemainingPercent: &rem120}).EffectiveRemainingPercent(); !ok || rem != 100 {
		t.Fatalf("clamping failed: got (%v, %v)", rem, ok)
	}
	if _, ok := (QuotaWindow{}).EffectiveRemainingPercent(); ok {
		t.Fatal("empty window should not report percentage")
	}
}

func TestQuotaSnapshotCloneIsDeep(t *testing.T) {
	now := time.Now().UTC()
	allowed := true
	rem := 50.0
	spendRem := 40
	orig := QuotaSnapshot{
		HarnessID:            "codex",
		Kind:                 QuotaKindCodex,
		OrdinaryUsageAllowed: &allowed,
		Windows:              []QuotaWindow{{ID: "secondary", RemainingPercent: &rem, ResetAt: &now}},
		Groups:               []QuotaWindowGroup{{ID: "g1", Name: "Extra", Windows: []QuotaWindow{{ID: "w1", RemainingPercent: &rem}}}},
		Credits:              &QuotaCredits{HasCredits: true, Balance: "$10"},
		SpendLimit:           &QuotaSpendLimit{Used: "$6", Limit: "$10", RemainingPercent: &spendRem, ResetAt: &now},
		Balance:              &QuotaBalance{Available: true, Entries: []QuotaBalanceEntry{{Currency: "CNY", TotalBalance: "88.00"}}},
	}
	cloned := orig.Clone()
	*cloned.OrdinaryUsageAllowed = false
	*cloned.Windows[0].RemainingPercent = 5
	*cloned.Groups[0].Windows[0].RemainingPercent = 1
	cloned.Credits.Balance = "$0"
	*cloned.SpendLimit.RemainingPercent = 0
	cloned.Balance.Entries[0].TotalBalance = "0"

	if !*orig.OrdinaryUsageAllowed || *orig.Windows[0].RemainingPercent != 50 || *orig.Groups[0].Windows[0].RemainingPercent != 50 ||
		orig.Credits.Balance != "$10" || *orig.SpendLimit.RemainingPercent != 40 || orig.Balance.Entries[0].TotalBalance != "88.00" {
		t.Fatal("mutating clone affected original snapshot")
	}
}

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestConvertCodexRateLimitsStrictlyDynamic(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	used := 32.0
	mins7d := int64(10080)
	resetAt := int64(1_700_100_000)
	plan := "pro"
	bal := "$15.00"
	spendRem := 75
	allowed := true
	extraID := "codex_other"
	extraName := "Codex Spark"
	extraUsed := 10.0
	mins60 := int64(60)

	// No primary (5h) window returned — only secondary (7d), credits, spendLimit, and one extra group.
	resp := codexGetRateLimitsResponse{
		OrdinaryUsageAllowed: &allowed,
		RateLimits: &codexRateLimitSnapshot{
			PlanType: &plan,
			Secondary: &codexRateLimitWindow{
				UsedPercent:        &used,
				WindowDurationMins: &mins7d,
				ResetsAt:           &resetAt,
			},
			Credits: &codexCreditsSnapshot{
				HasCredits: true,
				Balance:    &bal,
			},
			IndividualLimit: &codexSpendControlLimitSnapshot{
				Used:             "$25",
				Limit:            "$100",
				RemainingPercent: &spendRem,
				ResetsAt:         &resetAt,
			},
		},
		RateLimitsByLimitID: map[string]*codexRateLimitSnapshot{
			extraID: {
				LimitID:   &extraID,
				LimitName: &extraName,
				Primary: &codexRateLimitWindow{
					UsedPercent:        &extraUsed,
					WindowDurationMins: &mins60,
				},
			},
		},
	}

	snap := convertCodexRateLimits("codex", resp, "", domain.QuotaSourcePoll, now)
	if snap.Kind != domain.QuotaKindCodex || snap.PlanType != "pro" {
		t.Fatalf("unexpected header: %+v", snap)
	}
	if len(snap.Windows) != 1 {
		t.Fatalf("must strictly emit only returned windows (got %d: %+v)", len(snap.Windows), snap.Windows)
	}
	if snap.Windows[0].DisplayLabel() != "7d" {
		t.Fatalf("window label = %q, want 7d", snap.Windows[0].DisplayLabel())
	}
	if rem, ok := snap.Windows[0].EffectiveRemainingPercent(); !ok || rem != 68 {
		t.Fatalf("remaining = (%v, %v), want (68, true)", rem, ok)
	}
	if len(snap.Groups) != 1 || snap.Groups[0].Name != "Codex Spark" || len(snap.Groups[0].Windows) != 1 {
		t.Fatalf("unexpected groups: %+v", snap.Groups)
	}
	if snap.Credits == nil || snap.Credits.Balance != "$15.00" {
		t.Fatalf("unexpected credits: %+v", snap.Credits)
	}
	if snap.SpendLimit == nil || snap.SpendLimit.Limit != "$100" || *snap.SpendLimit.RemainingPercent != 75 {
		t.Fatalf("unexpected spendLimit: %+v", snap.SpendLimit)
	}
}

func TestMergeCodexRateLimitSnapshotPreservesPreviousFields(t *testing.T) {
	mins300 := int64(300)
	mins10080 := int64(10080)
	u10 := 10.0
	u50 := 50.0
	plan := "plus"
	base := &codexRateLimitSnapshot{
		PlanType:  &plan,
		Primary:   &codexRateLimitWindow{UsedPercent: &u10, WindowDurationMins: &mins300},
		Secondary: &codexRateLimitWindow{UsedPercent: &u10, WindowDurationMins: &mins10080},
	}
	// Sparse push updates only Primary.
	delta := &codexRateLimitSnapshot{
		Primary: &codexRateLimitWindow{UsedPercent: &u50, WindowDurationMins: &mins300},
	}
	merged := mergeCodexRateLimitSnapshot(base, delta)
	if merged.PlanType == nil || *merged.PlanType != "plus" {
		t.Fatalf("lost planType: %+v", merged)
	}
	if merged.Primary == nil || *merged.Primary.UsedPercent != 50 {
		t.Fatalf("primary not updated: %+v", merged.Primary)
	}
	if merged.Secondary == nil || *merged.Secondary.UsedPercent != 10 {
		t.Fatalf("secondary lost in sparse merge: %+v", merged.Secondary)
	}
}

func TestDeepSeekQuotaBalanceMockHTTP(t *testing.T) {
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test-mock" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch mode {
		case "ok":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"is_available": true,
				"balance_infos": []map[string]any{
					{
						"currency":          "CNY",
						"total_balance":     "128.40",
						"granted_balance":   "28.40",
						"topped_up_balance": "100.00",
					},
				},
			})
		case "429":
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	cfg := "model_provider = \"deepseek\"\n\n[model_providers.deepseek]\nbase_url = \"" + srv.URL + "\"\nexperimental_bearer_token = \"sk-test-mock\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := Adapter{
		Home:      home,
		apiPreset: &apiPreset{id: domain.QuotaKindDeepSeek, endpoint: srv.URL},
	}

	mode = "ok"
	snap, err := adapter.Quota(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Kind != domain.QuotaKindDeepSeek || snap.Balance == nil || !snap.Balance.Available {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Windows) != 0 || len(snap.Groups) != 0 {
		t.Fatalf("DeepSeek must be balance-only without windows: %+v", snap)
	}
	if len(snap.Balance.Entries) != 1 || snap.Balance.Entries[0].TotalBalance != "128.40" || snap.Balance.Entries[0].Currency != "CNY" {
		t.Fatalf("unexpected balance entries: %+v", snap.Balance.Entries)
	}

	mode = "429"
	_, err = adapter.Quota(context.Background(), nil)
	var qe *harness.QuotaError
	if !errors.As(err, &qe) || qe.RetryAfter != 45*time.Second {
		t.Fatalf("expected QuotaError with RetryAfter=45s, got %#v (%v)", qe, err)
	}

	mode = "401"
	_, err = adapter.Quota(context.Background(), nil)
	qe = nil
	if !errors.As(err, &qe) || !qe.AuthUnavailable {
		t.Fatalf("expected AuthUnavailable QuotaError, got %#v (%v)", qe, err)
	}
}

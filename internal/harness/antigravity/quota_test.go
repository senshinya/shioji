package antigravity

import (
	"context"
	"errors"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestAntigravityQuotaRejectsCustomProfile(t *testing.T) {
	a := Adapter{profileID: "agy-custom-1"}
	_, err := a.Quota(context.Background(), nil)
	var qe *harness.QuotaError
	if !errors.As(err, &qe) || !qe.Unsupported {
		t.Fatalf("expected Unsupported QuotaError for custom profile, got %#v (%v)", qe, err)
	}
}

func TestParseAntigravityUsageJSONDynamicGroups(t *testing.T) {
	raw := []byte("some noisy log line\n" + `{
		"status": "SUCCESS",
		"command": {
			"name": "/usage",
			"data": {
				"groups": [
					{
						"id": "gemini",
						"name": "Gemini 3 Pro",
						"buckets": [
							{
								"id": "bucket-1",
								"window": "5h",
								"remaining_fraction": 0.72,
								"reset_time": "2026-11-26T15:00:00Z"
							}
						]
					},
					{
						"id": "claude",
						"name": "Claude Sonnet",
						"buckets": [
							{
								"id": "bucket-2",
								"label": "Weekly",
								"remaining_percent": 18
							}
						]
					}
				]
			}
		}
	}`)

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	snap, err := parseAntigravityUsageJSON(raw, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Kind != domain.QuotaKindAntigravity || len(snap.Groups) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	g0 := snap.Groups[0]
	if g0.Name != "Gemini 3 Pro" || len(g0.Windows) != 1 {
		t.Fatalf("unexpected group 0: %+v", g0)
	}
	if rem, ok := g0.Windows[0].EffectiveRemainingPercent(); !ok || rem != 72 {
		t.Fatalf("group 0 remaining = (%v, %v), want (72, true)", rem, ok)
	}
	if g0.Windows[0].ResetAt == nil || !g0.Windows[0].ResetAt.Equal(time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected resetAt: %+v", g0.Windows[0].ResetAt)
	}
	g1 := snap.Groups[1]
	if g1.Name != "Claude Sonnet" || g1.Windows[0].DisplayLabel() != "Weekly" {
		t.Fatalf("unexpected group 1: %+v", g1)
	}
	if rem, ok := g1.Windows[0].EffectiveRemainingPercent(); !ok || rem != 18 {
		t.Fatalf("group 1 remaining = (%v, %v), want (18, true)", rem, ok)
	}
}

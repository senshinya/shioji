package antigravity

import (
	"context"
	"os"
	"shioji/internal/harness"
	"testing"
	"time"
)

func TestNativeQuotaUsesOnlyStructuredZeroTurnWindows(t *testing.T) {
	raw := `{"status":"SUCCESS","num_turns":0,"command":{"name":"usage","data":{"groups":[{"name":"Pro","buckets":[{"id":"a","window":"5h","remaining_fraction":0.75,"reset_time":"2026-11-23T12:00:00Z"},{"id":"b","window":"weekly","remaining_fraction":0.5,"reset_time":"2026-11-23 12:00:00"}]}]}}}`
	got, err := decodeNativeQuota([]byte(raw))
	if err != nil || len(got) != 2 || got[0].UsedPercent != 25 || got[0].DurationSeconds != 18000 || got[0].ResetsAt == nil ||
		!got[0].ResetsAt.Equal(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)) || got[1].ResetsAt != nil {
		t.Fatalf("unexpected windows: %+v %v", got, err)
	}
	if _, err := decodeNativeQuota([]byte(`{"status":"SUCCESS","num_turns":1,"result":"remaining 50%"}`)); err == nil {
		t.Fatal("accepted usage inferred from an ordinary model turn")
	}
}

func TestNativeQuotaWithInstalledCLI(t *testing.T) {
	if os.Getenv("SHIOJI_TEST_AGY_ACCOUNT_QUOTA") != "1" {
		t.Skip("requires an authorized device account with quota windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	adapter := Adapter{}
	catalog, err := adapter.Start(ctx, harness.StartOptions{CatalogOnly: true, RefreshCatalog: true})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close(context.Background())
	account := catalog.Readiness().Account
	if account == nil || len(account.QuotaWindows) == 0 {
		t.Fatal("the real CLI returned no visible quota windows")
	}
	t.Logf("loaded %d native account quota windows", len(account.QuotaWindows))
}

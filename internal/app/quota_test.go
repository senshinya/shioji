package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestQuotaBackoffScheduleAndRetryAfter(t *testing.T) {
	want := []time.Duration{
		15 * time.Second,
		30 * time.Second,
		60 * time.Second,
		120 * time.Second,
		300 * time.Second,
		15 * time.Minute,
		15 * time.Minute,
	}
	for i, exp := range want {
		if got := QuotaBackoffDelay(i+1, 0); got != exp {
			t.Fatalf("failure %d: got %v, want %v", i+1, got, exp)
		}
	}
	if got := QuotaBackoffDelay(1, 90*time.Second); got != 90*time.Second {
		t.Fatalf("larger Retry-After should win: got %v", got)
	}
	if got := QuotaBackoffDelay(4, 30*time.Second); got != 120*time.Second {
		t.Fatalf("larger schedule delay should win: got %v", got)
	}
}

func TestQuotaPanelLocalPersistenceRoundTrip(t *testing.T) {
	m := NewModel()
	t1 := QuotaTarget{ConnectionID: "local", ProfileID: "codex"}
	t2 := QuotaTarget{ConnectionID: "dev-1", ProfileID: "deepseek"}
	if !m.ToggleQuotaTarget(t1) {
		t.Fatal("expected add t1")
	}
	if !m.ToggleQuotaTarget(t2) {
		t.Fatal("expected add t2")
	}
	m.SetQuotaActiveTab(t2.Key())

	raw, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := WriteState(statePath, raw); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(statePath); err != nil {
		t.Fatal(err)
	}
	prefs := restored.QuotaPanel.Normalized()
	if len(prefs.Targets) != 2 || prefs.Targets[0] != t1 || prefs.Targets[1] != t2 {
		t.Fatalf("targets did not round-trip: %+v", prefs.Targets)
	}
	if prefs.ActiveTab != t2.Key() {
		t.Fatalf("activeTab = %q, want %q", prefs.ActiveTab, t2.Key())
	}
}

func TestQuotaResultPreservesCacheOnFailureAndRespectsPushVersion(t *testing.T) {
	m := NewModel()
	target := QuotaTarget{ConnectionID: "local", ProfileID: "codex"}
	now := time.Now().UTC()
	rem75 := 75.0
	rem40 := 40.0

	m.ApplyQuotaResult(target, domain.QuotaKindCodex, protocol.QuotaResult{
		Snapshot: &domain.QuotaSnapshot{
			HarnessID: "codex",
			Kind:      domain.QuotaKindCodex,
			Version:   2,
			Windows:   []domain.QuotaWindow{{ID: "primary", RemainingPercent: &rem75}},
		},
	}, nil, now)

	// Push update with higher version overwrites.
	m.ReceiveQuotaPush("local", domain.QuotaSnapshot{
		HarnessID: "codex",
		Kind:      domain.QuotaKindCodex,
		Source:    domain.QuotaSourcePush,
		Version:   5,
		Windows:   []domain.QuotaWindow{{ID: "primary", RemainingPercent: &rem40}},
	}, now.Add(time.Second))

	// Older poll (version 3) must NOT overwrite push version 5.
	m.ApplyQuotaResult(target, domain.QuotaKindCodex, protocol.QuotaResult{
		Snapshot: &domain.QuotaSnapshot{
			HarnessID: "codex",
			Kind:      domain.QuotaKindCodex,
			Version:   3,
			Windows:   []domain.QuotaWindow{{ID: "primary", RemainingPercent: &rem75}},
		},
	}, nil, now.Add(2*time.Second))

	st := m.QuotaState(target)
	if st == nil || st.Snapshot == nil || *st.Snapshot.Windows[0].RemainingPercent != 40 {
		t.Fatalf("stale poll overwrote newer push: %+v", st)
	}

	// Failure preserves cached snapshot and sets exponential backoff.
	m.ApplyQuotaResult(target, domain.QuotaKindCodex, protocol.QuotaResult{}, errors.New("network down"), now.Add(3*time.Second))
	if st.Snapshot == nil || *st.Snapshot.Windows[0].RemainingPercent != 40 {
		t.Fatal("cached snapshot was dropped on failure")
	}
	if st.ConsecutiveFailures != 1 || st.Error != "network down" {
		t.Fatalf("unexpected error state: %+v", st)
	}
	if got := st.BackoffUntil.Sub(now.Add(3 * time.Second)); got != 15*time.Second {
		t.Fatalf("backoff delay = %v, want 15s", got)
	}
}

func TestQuotaCatalogTabDisambiguationAndSeverity(t *testing.T) {
	m := NewModel()
	m.LocalDeviceName = "Work Mac"
	m.Connections = []DeviceConnection{{ID: "srv", Name: "Build Linux"}}
	m.DeviceStates = map[string]*DeviceState{
		"local": {
			Connected:   true,
			WorkerMinor: protocol.QuotaMinor,
			Readiness: domain.Readiness{Harnesses: []domain.HarnessInfo{
				{ID: "codex", Name: "Codex", State: domain.RuntimeReady, Account: &domain.RuntimeAccount{Mode: "chatgpt", State: "authenticated"}},
				{ID: "antigravity", Name: "Antigravity", State: domain.RuntimeReady},
				{ID: "agy-custom", Name: "AGY Work", Profile: &domain.AgentProfile{ID: "agy-custom", HarnessID: "antigravity", Mode: domain.AgentProfileAccount}},
				{ID: "codex-api", Name: "Codex API", Profile: &domain.AgentProfile{ID: "codex-api", HarnessID: "codex", Mode: domain.AgentProfileAPI}},
			}},
		},
		"srv": {
			Connected:   true,
			WorkerMinor: protocol.QuotaMinor,
			Readiness: domain.Readiness{Harnesses: []domain.HarnessInfo{
				{ID: "codex", Name: "Codex", State: domain.RuntimeReady, Account: &domain.RuntimeAccount{Mode: "chatgpt", State: "authenticated"}},
				{ID: "deepseek", Name: "DeepSeek", State: domain.RuntimeReady, Configured: true},
			}},
		},
	}

	cat := m.QuotaCatalog()
	if len(cat) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(cat))
	}
	if len(cat[0].Profiles) != 2 {
		t.Fatalf("local should have exactly 2 eligible profiles (codex + system antigravity), got %+v", cat[0].Profiles)
	}
	if len(cat[1].Profiles) != 2 {
		t.Fatalf("srv should have 2 eligible profiles, got %+v", cat[1].Profiles)
	}

	selected := []QuotaProfileDescriptor{cat[0].Profiles[0], cat[1].Profiles[0], cat[1].Profiles[1]}
	labels := QuotaTabLabels(selected)
	if labels[0] == labels[1] || labels[0] != "Codex·Mac" || labels[1] != "Codex·Linux" || labels[2] != "DeepSeek" {
		t.Fatalf("unexpected disambiguated labels: %+v", labels)
	}

	m.ToggleQuotaTarget(cat[0].Profiles[0].Target)
	rem4 := 4.0
	m.ApplyQuotaResult(cat[0].Profiles[0].Target, domain.QuotaKindCodex, protocol.QuotaResult{
		Snapshot: &domain.QuotaSnapshot{
			HarnessID: "codex",
			Kind:      domain.QuotaKindCodex,
			Windows:   []domain.QuotaWindow{{ID: "primary", RemainingPercent: &rem4}},
		},
	}, nil, time.Now())
	if got := m.OverallQuotaSeverity(); got != QuotaSeverityCritical {
		t.Fatalf("severity = %v, want critical", got)
	}
}

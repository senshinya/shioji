package worker

import (
	"context"
	"slices"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type explicitCatalogHarness struct {
	memoryHarness
	probes chan harness.StartOptions
}

func (a explicitCatalogHarness) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	a.probes <- opts
	return a.memoryHarness.Start(ctx, opts)
}

func TestDeviceCatalogMarksOnlyUserRequestedModelDiscovery(t *testing.T) {
	probe := explicitCatalogHarness{memoryHarness: memoryHarness{id: "explicit"}, probes: make(chan harness.StartOptions, 4)}
	s, _ := memoryWorker(t, probe)
	background := <-probe.probes
	if background.RefreshCatalog || background.CatalogDirectory != s.catalogDirectory("explicit") {
		t.Fatalf("startup discovery: %+v", background)
	}
	if _, err := s.recheckRuntime("explicit"); err != nil {
		t.Fatal(err)
	}
	select {
	case refreshed := <-probe.probes:
		if !refreshed.RefreshCatalog || refreshed.CatalogDirectory != background.CatalogDirectory {
			t.Fatalf("explicit discovery: %+v", refreshed)
		}
	default:
		t.Fatal("device catalog was not refreshed")
	}
}

func TestWorkerCatalogSeparatesAgentsFromProfileProviders(t *testing.T) {
	var ids, providers []string
	for _, a := range SystemAdapters("") {
		d := a.Descriptor()
		if d.ProfileOnly {
			providers = append(providers, d.ID)
		} else {
			ids = append(ids, d.ID)
		}
	}
	if !slices.Equal(ids, []string{"codex", "antigravity"}) ||
		!slices.Equal(providers, []string{"deepseek", domain.AgentResponses, domain.AgentAnthropic}) {
		t.Fatalf("worker Agents = %v, Profile providers = %v", ids, providers)
	}
}

package codex

import (
	"context"
	"os"
	"shioji/internal/domain"
	"testing"
)

func TestFastModeUsesTheAdvertisedNativeTier(t *testing.T) {
	for _, tt := range []struct{ mode, tier string }{{"models-priority", "priority"}, {"models-fast", "fast"}, {"models-legacy", "fast"}} {
		t.Run(tt.mode, func(t *testing.T) {
			r := loginPipe(t, tt.mode)
			r.protocol.serviceTier = true
			models, err := r.nativeModels(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 1 || !models[0].Fast {
				t.Fatal("advertised Fast mode was hidden", models)
			}
			r.ready = domain.Readiness{State: "ready", Models: models, Permissions: []string{"read-only"}}
			s := domain.Session{ID: "s", ThreadID: "thread", Config: domain.RunConfig{Model: "model", Fast: true, Permission: "read-only"}}
			got, err := r.Submit(context.Background(), s, "fast-op", "fixture")
			if err != nil || got != tt.tier {
				t.Fatal("Fast was not mapped to the advertised native tier", got, err)
			}
			s.Config.Fast = false
			got, err = r.Submit(context.Background(), s, "standard-op", "fixture")
			if err != nil || got != "default" {
				t.Fatal("disabling Fast retained its service tier", got, err)
			}
		})
	}
}

func TestFastModeRequiresTheActualProtocolAndModelCapability(t *testing.T) {
	for _, mode := range []string{"models-no-fast", "models-priority"} {
		t.Run(mode, func(t *testing.T) {
			r := loginPipe(t, mode)
			r.protocol.serviceTier = mode == "models-no-fast"
			models, err := r.nativeModels(context.Background())
			if err != nil || len(models) != 1 || models[0].Fast {
				t.Fatal("unsupported Fast mode was enabled", models, err)
			}
		})
	}
}

func TestFastToggleUsesPerTurnSpeedWithoutChangingTheThreadTier(t *testing.T) {
	r := loginPipe(t, "models-priority-per-turn")
	schema, err := os.ReadFile("testdata/request-shapes-0167.json")
	if err != nil {
		t.Fatal(err)
	}
	r.protocol, err = protocolFromSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	models, err := r.nativeModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.ready = domain.Readiness{State: "ready", Models: models, Permissions: []string{"read-only"}}
	for _, fast := range []bool{true, false, true} {
		s := domain.Session{ID: "s", ThreadID: "thread", Config: domain.RunConfig{Model: "model", Fast: fast, Permission: "read-only"}}
		tier := "default"
		if fast {
			tier = "priority"
		}
		got, err := r.Submit(context.Background(), s, "op", "fixture")
		if err != nil || got != "per-turn:"+tier {
			t.Fatal("speed toggle inherited or mutated the thread tier", fast, got, err)
		}
	}
}

func TestExplicitEmptyServiceTierCatalogDoesNotUseLegacyFast(t *testing.T) {
	r := loginPipe(t, "models-empty-modern")
	r.protocol.serviceTier = true
	models, err := r.nativeModels(context.Background())
	if err != nil || len(models) != 1 || models[0].Fast {
		t.Fatal("an explicitly empty modern catalog fell back to deprecated Fast", models, err)
	}
}

package modelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"shioji/internal/domain"
)

func TestProbeAnthropicReadsGatewayEffortEvenWhenFastIsInconclusive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "fixture" {
			t.Error("gateway request is unauthenticated")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/models/claude-opus-4-6":
			fmt.Fprint(w, `{"id":"claude-opus-4-6","supported_reasoning_levels":[{"effort":"low"},{"effort":"max"}]}`)
		case "POST /v1/messages":
			var req map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&req) != nil || req["output_config"] != nil {
				t.Error("effort discovery must not send generation probes")
			}
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	got := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: server.URL, Model: "claude-opus-4-6", Credential: "fixture",
	})
	if got.Probed || got.Fast || !slices.Equal(got.Efforts, []string{"low", "max"}) {
		t.Fatalf("gateway declaration was lost: %+v", got)
	}
}

func TestProbeAnthropicRejectsGatewaysThatSilentlyIgnoreParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"msg_naive","type":"message","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	got := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: server.URL, Model: "custom-model", Credential: "fixture",
	})
	if !got.Probed || got.Fast || len(got.Efforts) != 0 {
		t.Fatalf("naive gateway should not advertise effort or fast mode, got %+v", got)
	}
}

func TestProbeAnthropicUsesSentinelWhenGatewayOmitsFastUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Speed string `json:"speed"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Speed == "fast" {
			fmt.Fprint(w, `{"id":"msg_fast","type":"message","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		http.Error(w, "invalid speed", http.StatusBadRequest)
	}))
	defer server.Close()

	got := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: server.URL, Model: "claude-sonnet-4-6", Credential: "fixture",
	})
	if !got.Probed || !got.Fast || got.Efforts != nil {
		t.Fatalf("unexpected probe result: %+v", got)
	}
}

func TestProbeAnthropicRejectsDowngradedFastAndMarksTransientErrorsInconclusive(t *testing.T) {
	downgraded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Speed string `json:"speed"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Speed != "" {
			fmt.Fprint(w, `{"id":"msg_std","type":"message","usage":{"speed":"standard"}}`)
			return
		}
		http.Error(w, "unsupported", http.StatusBadRequest)
	}))
	defer downgraded.Close()

	got := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: downgraded.URL, Model: "claude-sonnet-4-6", Credential: "fixture",
	})
	if !got.Probed || got.Fast || len(got.Efforts) != 0 {
		t.Fatalf("downgraded fast mode should not be enabled, got %+v", got)
	}

	rateLimited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer rateLimited.Close()

	transient := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: rateLimited.URL, Model: "claude-sonnet-4-6", Credential: "fixture",
	})
	if transient.Probed || transient.Fast || len(transient.Efforts) != 0 {
		t.Fatalf("rate-limited probe must stay inconclusive, got %+v", transient)
	}
}

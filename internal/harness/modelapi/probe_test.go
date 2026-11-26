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

func TestProbeAnthropicDetectsValidatedEffortsAndFastMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "fixture" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			OutputConfig *struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
			Speed string `json:"speed"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.OutputConfig != nil {
			switch req.OutputConfig.Effort {
			case "low", "medium", "high", "max":
				fmt.Fprint(w, `{"id":"msg_1","type":"message","usage":{"input_tokens":1,"output_tokens":1}}`)
			default:
				http.Error(w, `{"type":"error","error":{"type":"invalid_request_error"}}`, http.StatusBadRequest)
			}
			return
		}
		if req.Speed != "" {
			if r.Header.Get("anthropic-beta") == anthropicFastModeBeta && req.Speed == "fast" {
				fmt.Fprint(w, `{"id":"msg_2","type":"message","usage":{"input_tokens":1,"output_tokens":1,"speed":"fast"}}`)
				return
			}
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error"}}`, http.StatusBadRequest)
			return
		}
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
	defer server.Close()

	got := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: server.URL, Model: "claude-opus-4-6", Credential: "fixture",
	})
	wantEfforts := []string{"low", "medium", "high", "max"}
	if !got.Probed || !got.Fast || !slices.Equal(got.Efforts, wantEfforts) {
		t.Fatalf("ProbeAnthropic() = %+v, want probed=true fast=true efforts=%v", got, wantEfforts)
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

func TestProbeAnthropicSupportsBetaEffortAndSentinelValidatedFast(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OutputConfig *struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
			Speed string `json:"speed"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.OutputConfig != nil {
			if r.Header.Get("anthropic-beta") != anthropicEffortBeta {
				http.Error(w, "beta required", http.StatusBadRequest)
				return
			}
			if req.OutputConfig.Effort == "low" || req.OutputConfig.Effort == "high" {
				fmt.Fprint(w, `{"id":"msg_beta","type":"message"}`)
				return
			}
			http.Error(w, "invalid effort", http.StatusBadRequest)
			return
		}
		if req.Speed != "" {
			if req.Speed == "fast" {
				// Proxy strips usage.speed, so sentinel must verify validation.
				fmt.Fprint(w, `{"id":"msg_fast","type":"message","usage":{"input_tokens":1,"output_tokens":1}}`)
				return
			}
			http.Error(w, "invalid speed", http.StatusBadRequest)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	got := ProbeAnthropic(context.Background(), domain.APIConfiguration{
		Endpoint: server.URL, Model: "claude-sonnet-4-6", Credential: "fixture",
	})
	if !got.Probed || !got.Fast || !slices.Equal(got.Efforts, []string{"low", "high"}) {
		t.Fatalf("ProbeAnthropic() = %+v, want probed=true fast=true efforts=[low high]", got)
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

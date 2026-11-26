package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"shioji/internal/domain"
)

const (
	anthropicProbeSentinel = "__shioji_probe_invalid__"
	anthropicFastModeBeta  = "fast-mode-2026-02-01"
)

// AnthropicCapabilities carries declared effort independently of the Fast probe.
type AnthropicCapabilities struct {
	Probed        bool     `json:"probed"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	Fast          bool     `json:"fast,omitempty"`
}

func ProbeAnthropic(ctx context.Context, in domain.APIConfiguration) AnthropicCapabilities {
	in, err := Normalize(in)
	if err != nil || in.Model == "" {
		return AnthropicCapabilities{}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var fast, fastConclusive bool
	var wg sync.WaitGroup
	wg.Go(func() {
		fast, fastConclusive = probeAnthropicFast(ctx, in)
	})
	gateway := findModelEfforts(ctx, domain.AgentAnthropic, in)
	wg.Wait()
	return AnthropicCapabilities{
		Probed: fastConclusive, Fast: fast,
		Efforts: gateway.Efforts, DefaultEffort: gateway.DefaultEffort,
	}
}

func probeAnthropicFast(ctx context.Context, in domain.APIConfiguration) (bool, bool) {
	status, msgType, usageSpeed, err := probeAnthropicMessage(ctx, in, anthropicFastModeBeta, anthropicFastPayload(in.Model, "fast"))
	if err != nil || !isConclusiveProbeStatus(status) {
		return false, false
	}
	if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity ||
		status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return false, true
	}
	if status != http.StatusOK || msgType != "message" {
		return false, false
	}
	if usageSpeed == "fast" {
		return true, true
	}
	if usageSpeed != "" {
		return false, true
	}

	sentinelStatus, _, _, err := probeAnthropicMessage(ctx, in, anthropicFastModeBeta, anthropicFastPayload(in.Model, anthropicProbeSentinel))
	if err != nil || !isConclusiveProbeStatus(sentinelStatus) {
		return false, false
	}
	return sentinelStatus == http.StatusBadRequest || sentinelStatus == http.StatusUnprocessableEntity, true
}

func isConclusiveProbeStatus(status int) bool {
	return status == http.StatusOK || status == http.StatusBadRequest ||
		status == http.StatusUnprocessableEntity || status == http.StatusNotFound ||
		status == http.StatusMethodNotAllowed
}

func anthropicFastPayload(model, speed string) map[string]any {
	return map[string]any{
		"model":      model,
		"max_tokens": 16,
		"messages":   []map[string]string{{"role": "user", "content": "Reply OK."}},
		"speed":      speed,
	}
}

func probeAnthropicMessage(ctx context.Context, in domain.APIConfiguration, beta string, body map[string]any) (int, string, string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(domain.AgentAnthropic, in.Endpoint, "messages"), bytes.NewReader(data))
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if in.Credential != "" {
		req.Header.Set("x-api-key", in.Credential)
	}
	if beta != "" {
		req.Header.Set("anthropic-beta", beta)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", "", nil
	}
	if err != nil || len(raw) > 256<<10 {
		return resp.StatusCode, "", "", nil
	}
	var reply struct {
		Type  string `json:"type"`
		Usage struct {
			Speed string `json:"speed"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &reply) != nil {
		return resp.StatusCode, "", "", nil
	}
	return resp.StatusCode, reply.Type, reply.Usage.Speed, nil
}

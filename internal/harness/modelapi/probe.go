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
	anthropicEffortBeta    = "effort-2025-11-24"
	anthropicFastModeBeta  = "fast-mode-2026-02-01"
)

var anthropicEffortCandidates = []string{"low", "medium", "high", "xhigh", "max"}

// AnthropicCapabilities captures model-level features verified against a live
// Anthropic Messages API endpoint rather than inferred from model names.
type AnthropicCapabilities struct {
	Probed  bool     `json:"probed"`
	Efforts []string `json:"efforts,omitempty"`
	Fast    bool     `json:"fast,omitempty"`
}

// ProbeAnthropic tests whether the configured Anthropic Messages API endpoint
// and model validate and accept thinking effort levels and fast mode.
// Sentinel probes reject gateways that silently ignore unknown fields.
func ProbeAnthropic(ctx context.Context, in domain.APIConfiguration) AnthropicCapabilities {
	in, err := Normalize(in)
	if err != nil || in.Model == "" {
		return AnthropicCapabilities{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var (
		efforts           []string
		effortsConclusive bool
		fast              bool
		fastConclusive    bool
		wg                sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		efforts, effortsConclusive = probeAnthropicEfforts(probeCtx, in)
	}()
	go func() {
		defer wg.Done()
		fast, fastConclusive = probeAnthropicFast(probeCtx, in)
	}()
	wg.Wait()

	return AnthropicCapabilities{
		Probed:  effortsConclusive && fastConclusive,
		Efforts: efforts,
		Fast:    fast,
	}
}

func probeAnthropicEfforts(ctx context.Context, in domain.APIConfiguration) ([]string, bool) {
	status, _, _, err := probeAnthropicMessage(ctx, in, "", anthropicEffortPayload(in.Model, anthropicProbeSentinel))
	if err != nil || !isConclusiveProbeStatus(status) {
		return nil, false
	}
	if status == http.StatusOK || status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return nil, true
	}

	efforts, conclusive := probeEffortLevels(ctx, in, "", anthropicEffortCandidates)
	if len(efforts) > 0 || !conclusive {
		return efforts, conclusive
	}

	// Some gateways gate output_config.effort behind the Anthropic beta header.
	status, msgType, _, err := probeAnthropicMessage(ctx, in, anthropicEffortBeta, anthropicEffortPayload(in.Model, anthropicEffortCandidates[0]))
	if err != nil || !isConclusiveProbeStatus(status) {
		return nil, false
	}
	if status != http.StatusOK || msgType != "message" {
		return nil, true
	}
	sentinelStatus, _, _, err := probeAnthropicMessage(ctx, in, anthropicEffortBeta, anthropicEffortPayload(in.Model, anthropicProbeSentinel))
	if err != nil || !isConclusiveProbeStatus(sentinelStatus) {
		return nil, false
	}
	if sentinelStatus != http.StatusBadRequest && sentinelStatus != http.StatusUnprocessableEntity {
		return nil, true
	}
	rest, restConclusive := probeEffortLevels(ctx, in, anthropicEffortBeta, anthropicEffortCandidates[1:])
	return append([]string{anthropicEffortCandidates[0]}, rest...), restConclusive
}

func probeEffortLevels(ctx context.Context, in domain.APIConfiguration, beta string, levels []string) ([]string, bool) {
	accepted := make([]bool, len(levels))
	conclusive := make([]bool, len(levels))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, level := range levels {
		wg.Add(1)
		go func(index int, effort string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			status, msgType, _, err := probeAnthropicMessage(ctx, in, beta, anthropicEffortPayload(in.Model, effort))
			if err != nil {
				return
			}
			switch status {
			case http.StatusOK:
				if msgType == "message" {
					accepted[index] = true
					conclusive[index] = true
				}
			case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusMethodNotAllowed:
				conclusive[index] = true
			}
		}(i, level)
	}
	wg.Wait()

	var supported []string
	allConclusive := true
	for i, level := range levels {
		if accepted[i] {
			supported = append(supported, level)
		}
		if !conclusive[i] {
			allConclusive = false
		}
	}
	return supported, allConclusive
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

func anthropicEffortPayload(model, effort string) map[string]any {
	return map[string]any{
		"model":         model,
		"max_tokens":    16,
		"messages":      []map[string]string{{"role": "user", "content": "Reply OK."}},
		"output_config": map[string]any{"effort": effort},
	}
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

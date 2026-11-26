package modelapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"shioji/internal/domain"
)

// EffortMetadata accepts the effort fields used by Anthropic-compatible and
// Codex-compatible model catalogs. A missing field differs from an empty list.
type EffortMetadata struct {
	Levels       json.RawMessage `json:"supported_reasoning_levels"`
	Efforts      json.RawMessage `json:"supported_reasoning_efforts"`
	Native       json.RawMessage `json:"supportedReasoningEfforts"`
	Capabilities struct {
		Effort json.RawMessage `json:"effort"`
	} `json:"capabilities"`
	Reasoning struct {
		Levels  json.RawMessage `json:"supported_efforts"`
		Default string          `json:"default_effort"`
	} `json:"reasoning"`
	DefaultLevel  string `json:"default_reasoning_level"`
	DefaultEffort string `json:"default_reasoning_effort"`
	NativeDefault string `json:"defaultReasoningEffort"`
}

func (m EffortMetadata) Apply(model domain.Model) domain.Model {
	var levels []string
	for _, data := range []json.RawMessage{m.Levels, m.Efforts, m.Native, m.Reasoning.Levels} {
		if levels = declaredEffortLevels(data); levels != nil {
			break
		}
	}
	if levels == nil {
		levels = anthropicCapabilityLevels(m.Capabilities.Effort)
	}
	if levels == nil {
		return model
	}
	for _, value := range []string{m.DefaultLevel, m.DefaultEffort, m.NativeDefault, m.Reasoning.Default} {
		if slices.Contains(levels, value) {
			model.DefaultEffort = value
			break
		}
	}
	return model.WithEfforts(levels)
}

func declaredEffortLevels(data json.RawMessage) []string {
	var entries []json.RawMessage
	if json.Unmarshal(data, &entries) != nil || entries == nil {
		return nil
	}
	levels := []string{}
	for _, entry := range entries {
		var level string
		if json.Unmarshal(entry, &level) != nil {
			var row struct {
				Effort string `json:"effort"`
				Snake  string `json:"reasoning_effort"`
				Camel  string `json:"reasoningEffort"`
			}
			if json.Unmarshal(entry, &row) != nil {
				continue
			}
			level = row.Effort
			if level == "" {
				level = row.Snake
			}
			if level == "" {
				level = row.Camel
			}
		}
		levels = appendEffortLevel(levels, level)
	}
	if len(entries) > 0 && len(levels) == 0 {
		return nil
	}
	return levels
}

func anthropicCapabilityLevels(data json.RawMessage) []string {
	var flags map[string]json.RawMessage
	if json.Unmarshal(data, &flags) != nil {
		return nil
	}
	var supported bool
	if json.Unmarshal(flags["supported"], &supported) != nil || string(flags["supported"]) == "null" {
		return nil
	}
	levels := []string{}
	if supported {
		for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
			var capability struct{ Supported bool }
			if json.Unmarshal(flags[level], &capability) == nil && capability.Supported {
				levels = append(levels, level)
			}
		}
	}
	return levels
}

func appendEffortLevel(levels []string, level string) []string {
	if domain.ValidEffortLevel(level) && !slices.Contains(levels, level) {
		return append(levels, level)
	}
	return levels
}

// findModelEfforts keeps the configured model ID as an exact match. Some
// gateways expose capability metadata only on the individual model resource.
func findModelEfforts(ctx context.Context, id string, in domain.APIConfiguration) domain.Model {
	fallback := Model(in)
	if in.Model != "." && in.Model != ".." {
		detail := endpoint(id, in.Endpoint, "models") + "/" + url.PathEscape(in.Model)
		var row struct {
			ID string `json:"id"`
			EffortMetadata
		}
		if request(ctx, id, in.Credential, http.MethodGet, detail, nil, &row) == nil && row.ID == in.Model {
			if model := row.Apply(fallback); model.Efforts != nil {
				return model
			}
		}
	}
	models, err := Models(ctx, id, in)
	if err == nil {
		for _, model := range models {
			if model.ID == in.Model {
				return model
			}
		}
	}
	return fallback
}

func NormalizeEfforts(levels []string) []string {
	if levels == nil {
		return nil
	}
	out := []string{}
	for _, level := range levels {
		out = appendEffortLevel(out, strings.TrimSpace(level))
	}
	return out
}

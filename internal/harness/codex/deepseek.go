package codex

import (
	"errors"
	"slices"
	"strings"

	"shioji/internal/domain"
)

// The preset belongs to a separate device Agent. The app-server, tool
// permissions and history machinery can use the same engine as Codex.
type apiPreset struct {
	id, endpoint, defaultModel, documentation string
	reasoningModels, efforts                  []string
	defaultEffort                             string
}

func NewDeepSeekAdapter(executable string) Adapter {
	return Adapter{Executable: executable, apiPreset: &apiPreset{
		id: "deepseek", endpoint: "https://api.deepseek.com", defaultModel: "deepseek-flash",
		documentation: "https://api-docs.deepseek.com/zh-cn/guides/responses_api/",
		// /models advertises availability. The Responses API documents the
		// independent reasoning options for these model IDs.
		reasoningModels: []string{"deepseek-flash", "deepseek-v4-pro"},
		efforts:         []string{"none", "low", "high", "max"},
		defaultEffort:   "high",
	}}
}

func (p *apiPreset) applyReasoningOptions(models []domain.Model) {
	for i := range models {
		if slices.Contains(p.reasoningModels, models[i].ID) {
			models[i].Efforts = slices.Clone(p.efforts)
			models[i].DefaultEffort = p.defaultEffort
		}
	}
}

func (a Adapter) apiConfiguration(in domain.APIConfiguration) (domain.APIConfiguration, error) {
	if preset := a.apiPreset; preset != nil {
		if strings.TrimSpace(in.Endpoint) != "" {
			return in, errors.New("此 Agent 使用内置的 API 地址")
		}
		if strings.TrimSpace(in.Credential) == "" {
			return in, errors.New("请输入 API Key")
		}
		in.Endpoint = preset.endpoint
	}
	return validateAPIConfiguration(in)
}

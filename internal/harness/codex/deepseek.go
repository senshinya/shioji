package codex

import (
	"errors"
	"strings"

	"shioji/internal/domain"
)

// The preset belongs to a separate device Agent. The app-server, tool
// permissions and history machinery can use the same engine as Codex.
type apiPreset struct {
	id, endpoint, defaultModel, documentation string
}

func NewDeepSeekAdapter(executable string) Adapter {
	return Adapter{Executable: executable, apiPreset: &apiPreset{
		id: "deepseek", endpoint: "https://api.deepseek.com", defaultModel: "deepseek-flash",
		documentation: "https://api-docs.deepseek.com/zh-cn/guides/responses_api/",
	}}
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

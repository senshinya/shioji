package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"shioji/internal/harness"
	"strconv"
	"strings"

	"shioji/internal/domain"
)

// Only the adapter sees native layers and provider settings. The UI receives a
// whitelist projection; neither raw config nor any credential crosses it.
type nativeConfiguration struct {
	Config struct {
		ToolServices map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"mcp_servers"`
		CredentialsStore string                    `json:"cli_auth_credentials_store"`
		ForcedLogin      string                    `json:"forced_login_method"`
		Provider         string                    `json:"model_provider"`
		Model            string                    `json:"model"`
		Endpoint         string                    `json:"openai_base_url"`
		Providers        map[string]nativeProvider `json:"model_providers"`
	} `json:"config"`
	Layers []struct {
		Config json.RawMessage `json:"config"`
		Name   struct {
			Type    string `json:"type"`
			File    string `json:"file"`
			Profile string `json:"profile"`
		} `json:"name"`
		Version        string  `json:"version"`
		DisabledReason *string `json:"disabledReason"`
	} `json:"layers"`
}
type nativeProvider struct {
	CatalogURL   string            `json:"model_catalog_url"`
	Query        map[string]string `json:"query_params"`
	Name         string            `json:"name"`
	Endpoint     string            `json:"base_url"`
	EnvKey       string            `json:"env_key"`
	Token        string            `json:"experimental_bearer_token"`
	Auth         json.RawMessage   `json:"auth"`
	Gateway      json.RawMessage   `json:"gateway_oauth"`
	RequiresAuth bool              `json:"requires_openai_auth"`
	Headers      map[string]string `json:"http_headers"`
	EnvHeaders   map[string]string `json:"env_http_headers"`
}

func environmentValue(env []string, name string) string {
	for _, value := range env {
		if strings.HasPrefix(value, name+"=") {
			return strings.TrimPrefix(value, name+"=")
		}
	}
	return ""
}
func publicEndpoint(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func (c nativeConfiguration) summary(account accountStatus, env []string) *domain.RuntimeAccount {
	result := account.summary()
	if result == nil {
		result = &domain.RuntimeAccount{State: "required", Label: "未配置"}
	}
	result.Model = c.Config.Model
	for _, layer := range c.Layers {
		if layer.Name.Type == "user" && layer.DisabledReason == nil {
			result.ConfigurationFile = layer.Name.File
			result.Profile = layer.Name.Profile
			break
		}
	}
	id := c.Config.Provider
	if id == "" {
		id = "openai"
	}
	provider := c.provider(env)
	endpoint := provider.Endpoint
	if id == "openai" {
		endpoint = c.Config.Endpoint
		if endpoint == "" {
			endpoint = environmentValue(env, "OPENAI_BASE_URL")
		}
	}
	api := id != "openai" || endpoint != "" || result.Label == "API Key" || result.Label != "ChatGPT" && environmentValue(env, "OPENAI_API_KEY") != ""
	if !api || result.Label == "ChatGPT" && (provider.RequiresAuth || id == "openai") {
		result.Mode = "account"
		return result
	}
	result.Mode = "api"
	result.Endpoint = publicEndpoint(provider.Endpoint)
	result.Subscription = ""
	result.Identity = ""
	result.Label = provider.Name
	if result.Label == "" {
		result.Label = id
	}
	if id == "openai" {
		result.Label = "OpenAI API"
	}
	result.Credential = "notRequired"
	result.State = "notRequired"
	switch {
	case provider.EnvKey != "":
		result.Credential = "missing"
		result.State = "missingCredential"
		if environmentValue(env, provider.EnvKey) != "" {
			result.Credential = "configured"
			result.State = "authenticated"
		}
	case provider.Token != "", len(provider.Headers) > 0:
		result.Credential = "configured"
		result.State = "authenticated"
	case len(provider.EnvHeaders) > 0:
		result.Credential = "configured"
		result.State = "authenticated"
		for _, name := range provider.EnvHeaders {
			if environmentValue(env, name) == "" {
				result.Credential = "missing"
				result.State = "missingCredential"
			}
		}
	case string(provider.Auth) != "" && string(provider.Auth) != "null", string(provider.Gateway) != "" && string(provider.Gateway) != "null":
		result.Credential = "managed"
		result.State = "configured"
	case provider.RequiresAuth || id == "openai":
		result.Credential = "missing"
		result.State = "missingCredential"
		if account.issue() == "" && account.summary() != nil && account.summary().State == "authenticated" || id == "openai" && environmentValue(env, "OPENAI_API_KEY") != "" {
			result.Credential = "configured"
			result.State = "authenticated"
		}
	}
	return result
}
func (c nativeConfiguration) canInitialize(account accountStatus) bool {
	return (c.Config.Provider == "" || c.Config.Provider == "openai") && c.Config.Endpoint == "" && len(c.Config.Providers) == 0 && c.Config.ForcedLogin != "chatgpt" && account.issue() == domain.RuntimeSignInRequired
}
func (r *Runtime) systemConfiguration(ctx context.Context) (nativeConfiguration, error) {
	var config nativeConfiguration
	err := r.call(ctx, "config/read", map[string]any{"includeLayers": true}, &config)
	return config, err
}

func validateAPIConfiguration(in domain.APIConfiguration) (domain.APIConfiguration, error) {
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Model = strings.TrimSpace(in.Model)
	in.Credential = strings.TrimSpace(in.Credential)
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(in.Endpoint) > 4096 {
		return in, errors.New("请输入不含凭据的 API 地址")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return in, errors.New("API 地址须使用 HTTPS，本机服务可使用 HTTP")
	}
	if len(in.Model) > 256 || strings.ContainsAny(in.Model, "\x00\r\n") || len(in.Credential) > 16<<10 || strings.ContainsAny(in.Credential, "\x00\r\n") {
		return in, errors.New("请检查模型名称和 API Key")
	}
	return in, nil
}

func (a Adapter) ConfigureAPI(ctx context.Context, in domain.APIConfiguration) (domain.Readiness, error) {
	in, err := validateAPIConfiguration(in)
	if err != nil {
		return domain.Readiness{}, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: err.Error()}
	}
	r, err := Start(ctx, Options{Executable: a.Executable, Home: a.Home, Arguments: a.runtimeOptions.Arguments, Environment: a.runtimeOptions.Environment, authentication: true})
	if err != nil {
		return domain.Readiness{}, apiSetupError("无法读取 Agent 配置，请重新检测")
	}
	defer r.Close()
	if !r.supports("config/batchWrite") {
		return domain.Readiness{}, harness.Unsupported("api-configuration")
	}
	config, err := r.systemConfiguration(ctx)
	if err != nil {
		return domain.Readiness{}, apiSetupError("无法读取系统配置，请重新检测")
	}
	var account accountStatus
	if err = r.call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return domain.Readiness{}, apiSetupError("无法读取授权状态，请重新检测")
	}
	if !config.canInitialize(account) || environmentValue(r.cmd.Env, "OPENAI_API_KEY") != "" || environmentValue(r.cmd.Env, "OPENAI_BASE_URL") != "" {
		return domain.Readiness{}, apiSetupError("该设备已存在接入配置，请重新检测")
	}
	file, version := "", ""
	for _, layer := range config.Layers {
		if layer.DisabledReason != nil {
			continue
		}
		if layer.Name.Type == "sessionFlags" {
			var flags map[string]json.RawMessage
			json.Unmarshal(layer.Config, &flags)
			for _, key := range []string{"model", "model_provider", "model_providers", "openai_base_url", "profile"} {
				if _, exists := flags[key]; exists {
					return domain.Readiness{}, apiSetupError("当前启动参数覆盖了接入配置，请先移除覆盖")
				}
			}
		}
		if layer.Name.Type == "user" && file == "" {
			file, version = layer.Name.File, layer.Version
		}
	}
	if file == "" || version == "" {
		return domain.Readiness{}, apiSetupError("未找到可写的系统配置，请重新检测")
	}
	models, err := a.APIModels(ctx, in)
	if err != nil {
		return domain.Readiness{}, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: err.Error()}
	}
	if in.Model == "" {
		in.Model = models[0].ID
		for _, model := range models {
			if model.ID == config.Config.Model || model.Default {
				in.Model = model.ID
				break
			}
		}
	}
	selected := false
	for _, model := range models {
		selected = selected || model.ID == in.Model
	}
	if !selected {
		return domain.Readiness{}, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: "所选模型已不可用，请重新获取模型"}
	}
	// Discovery performs network I/O. Another native client may authorize an
	// account meanwhile; the config version alone does not cover auth storage.
	guard, err := Start(ctx, Options{Executable: a.Executable, Home: a.Home, Arguments: a.runtimeOptions.Arguments, Environment: a.runtimeOptions.Environment, authentication: true})
	if err != nil {
		return domain.Readiness{}, apiSetupError("无法确认当前授权状态，请重新检测")
	}
	defer guard.Close()
	current, err := guard.systemConfiguration(ctx)
	if err != nil {
		return domain.Readiness{}, apiSetupError("无法确认当前配置，请重新检测")
	}
	var currentAccount accountStatus
	if err = guard.call(ctx, "account/read", map[string]any{"refreshToken": false}, &currentAccount); err != nil || !current.canInitialize(currentAccount) {
		return domain.Readiness{}, apiSetupError("该设备的授权或接入配置已变化，请重新检测")
	}
	type edit struct {
		Key      string `json:"keyPath"`
		Value    any    `json:"value"`
		Strategy string `json:"mergeStrategy"`
	}
	provider := map[string]any{"name": "Custom API", "base_url": in.Endpoint, "wire_api": "responses", "requires_openai_auth": false}
	if in.Credential != "" {
		provider["experimental_bearer_token"] = in.Credential
	}
	// One native atomic, version-checked write. Other providers, preferences and
	// profile contents remain intact; loaded sessions are never hot-reloaded.
	var written struct {
		Status     string          `json:"status"`
		Overridden json.RawMessage `json:"overriddenMetadata"`
	}
	err = r.call(ctx, "config/batchWrite", map[string]any{"filePath": file, "expectedVersion": version, "reloadUserConfig": false, "edits": []edit{{"model_providers.custom", provider, "replace"}, {"model_provider", "custom", "replace"}, {"model", in.Model, "replace"}}}, &written)
	if err != nil {
		return domain.Readiness{}, apiSetupError("配置未确认保存；请重新检测后再试")
	}
	if written.Status != "ok" || len(written.Overridden) > 0 && string(written.Overridden) != "null" {
		return domain.Readiness{}, apiSetupError("配置受到系统策略限制，请重新检测")
	}
	// Read from a fresh process, matching what a terminal Codex launch will see.
	fresh, err := Start(ctx, Options{Executable: a.Executable, Home: a.Home, Arguments: a.runtimeOptions.Arguments, Environment: a.runtimeOptions.Environment})
	if err != nil {
		var setup *harness.SetupError
		if errors.As(err, &setup) && setup.Account != nil {
			return domain.Readiness{}, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Account: setup.Account, Message: "配置已保存，模型读取失败，请重试"}
		}
		return domain.Readiness{}, apiSetupError("已写入系统配置，重新检测后确认可用性")
	}
	defer fresh.Close()
	ready := fresh.Readiness()
	if ready.Account == nil || ready.Account.Model != in.Model || ready.Account.Endpoint != in.Endpoint {
		return domain.Readiness{}, apiSetupError("系统配置已变化，请重新检测")
	}
	return ready, nil
}

func (c nativeConfiguration) provider(env []string) nativeProvider {
	id := c.Config.Provider
	if id == "" {
		id = "openai"
	}
	provider := c.Config.Providers[id]
	if id == "openai" {
		provider.Endpoint = c.Config.Endpoint
		if provider.Endpoint == "" {
			provider.Endpoint = environmentValue(env, "OPENAI_BASE_URL")
		}
		if provider.Endpoint == "" {
			provider.Endpoint = "https://api.openai.com/v1"
		}
	}
	if (id == "ollama" || id == "lmstudio") && provider.Endpoint == "" {
		port := 11434
		if id == "lmstudio" {
			port = 1234
		}
		if value, err := strconv.ParseUint(environmentValue(env, "CODEX_OSS_PORT"), 10, 16); err == nil {
			port = int(value)
		}
		provider.Endpoint = environmentValue(env, "CODEX_OSS_BASE_URL")
		if strings.TrimSpace(provider.Endpoint) == "" {
			provider.Endpoint = fmt.Sprintf("http://localhost:%d/v1", port)
		}
	}
	return provider
}

func apiSetupError(message string) error {
	return &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: message}
}

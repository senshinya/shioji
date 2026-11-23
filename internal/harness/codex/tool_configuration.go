package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type nativeToolService struct {
	Enabled          *bool             `json:"enabled"`
	Command          string            `json:"command"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env"`
	EnvVars          []string          `json:"env_vars"`
	Directory        string            `json:"cwd"`
	URL              string            `json:"url"`
	TokenEnvironment string            `json:"bearer_token_env_var"`
	Headers          map[string]string `json:"http_headers"`
	StartupSeconds   float64           `json:"startup_timeout_sec"`
}

func toolConfigurationRevision(c nativeConfiguration) string {
	b, _ := json.Marshal(c.Layers)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func toolUserLayer(c nativeConfiguration) (string, string, map[string]json.RawMessage) {
	for _, layer := range c.Layers {
		if layer.Name.Type != "user" || layer.Name.Profile != "" || layer.DisabledReason != nil {
			continue
		}
		var content struct {
			Services map[string]json.RawMessage `json:"mcp_servers"`
		}
		if json.Unmarshal(layer.Config, &content) != nil {
			return "", "", nil
		}
		return layer.Name.File, layer.Version, content.Services
	}
	return "", "", nil
}
func (r *Runtime) canWriteToolConfiguration(c nativeConfiguration) bool {
	file, version, _ := toolUserLayer(c)
	return file != "" && version != "" && r.supports("config/batchWrite") && r.supports("config/mcpServer/reload")
}
func toolReadOnlyReason(c nativeConfiguration, id string) string {
	for _, layer := range c.Layers {
		if layer.DisabledReason != nil {
			continue
		}
		var data struct {
			Services map[string]json.RawMessage `json:"mcp_servers"`
		}
		json.Unmarshal(layer.Config, &data)
		if _, ok := data.Services[id]; ok && (layer.Name.Type != "user" || layer.Name.Profile != "") {
			if layer.Name.Type == "project" {
				return "项目配置，只读"
			}
			return "由其他配置层管理，只读"
		}
	}
	return ""
}
func (r *Runtime) projectToolConfiguration(c nativeConfiguration, s *domain.ToolService) {
	raw, ok := c.Config.ToolServices[s.ID]
	if !ok {
		return
	}
	var native nativeToolService
	if json.Unmarshal(raw, &native) != nil {
		s.ReadOnlyReason = "配置无法编辑"
		return
	}
	config := domain.ToolServiceConfiguration{Name: s.ID, Enabled: native.Enabled == nil || *native.Enabled, Command: native.Command, Arguments: native.Args, Environment: harness.RetainedToolValues(native.Env), InheritedEnvironment: native.EnvVars, Directory: native.Directory, URL: publicEndpoint(native.URL), TokenEnvironment: native.TokenEnvironment, Headers: harness.RetainedToolValues(native.Headers), StartupSeconds: native.StartupSeconds}
	if native.Command != "" {
		config.Transport = "command"
	} else if native.URL != "" {
		config.Transport = "http"
	}
	s.Transport = config.Transport
	s.Configuration = &config
	s.ReadOnlyReason = toolReadOnlyReason(c, s.ID)
	if config.Transport == "" {
		s.ReadOnlyReason = "此连接类型暂不支持编辑"
	}
	if native.URL != "" && config.URL != native.URL {
		s.ReadOnlyReason = "地址含私有参数，请在原配置中编辑"
	}
	s.Editable = r.canWriteToolConfiguration(c) && s.ReadOnlyReason == ""
}

func (r *Runtime) ChangeToolService(ctx context.Context, in domain.ToolServiceChange) (domain.ToolServiceChangeResult, error) {
	c, err := r.systemConfiguration(ctx)
	if err != nil {
		return domain.ToolServiceChangeResult{}, errors.New("无法读取当前配置")
	}
	fail := func(message string) (domain.ToolServiceChangeResult, error) {
		return domain.ToolServiceChangeResult{}, errors.New(message)
	}
	if !r.canWriteToolConfiguration(c) {
		return fail("当前 Agent 不支持修改工具配置")
	}
	if in.Revision == "" || in.Revision != toolConfigurationRevision(c) {
		return fail("配置已变化，请刷新后重新编辑")
	}
	file, version, user := toolUserLayer(c)
	var table map[string]any
	var previous nativeToolService
	if in.ServiceID != "" {
		raw, ok := user[in.ServiceID]
		if !ok || toolReadOnlyReason(c, in.ServiceID) != "" {
			return fail("此服务由其他配置管理，无法修改")
		}
		if json.Unmarshal(raw, &table) != nil || json.Unmarshal(raw, &previous) != nil {
			return fail("原配置无法读取")
		}
	} else {
		table = map[string]any{}
	}
	id := in.ServiceID
	switch in.Action {
	case "save":
		if in.Configuration == nil {
			return fail("缺少工具服务配置")
		}
		config := *in.Configuration
		if err = harness.ValidateToolConfiguration(config); err != nil {
			return fail(err.Error())
		}
		id = config.Name
		if _, exists := c.Config.ToolServices[id]; exists && id != in.ServiceID {
			return fail("此服务名称已存在")
		}
		env, err := harness.MergeToolValues(config.Environment, previous.Env, false)
		if err != nil {
			return fail(err.Error())
		}
		headers, err := harness.MergeToolValues(config.Headers, previous.Headers, true)
		if err != nil {
			return fail(err.Error())
		}
		// Retain unexposed native options. Only an explicit transport change
		// removes fields that belong to the previous connection type.
		for _, key := range []string{"command", "args", "env", "env_vars", "cwd", "url", "bearer_token_env_var", "http_headers"} {
			delete(table, key)
		}
		table["enabled"] = config.Enabled
		if config.StartupSeconds > 0 {
			table["startup_timeout_sec"] = config.StartupSeconds
		} else {
			delete(table, "startup_timeout_sec")
		}
		if config.Transport == "command" {
			delete(table, "env_http_headers")
			table["command"] = config.Command
			if len(config.Arguments) > 0 {
				table["args"] = config.Arguments
			}
			if len(env) > 0 {
				table["env"] = env
			}
			if len(config.InheritedEnvironment) > 0 {
				table["env_vars"] = config.InheritedEnvironment
			}
			if config.Directory != "" {
				table["cwd"] = config.Directory
			}
		} else {
			table["url"] = config.URL
			if config.TokenEnvironment != "" {
				table["bearer_token_env_var"] = config.TokenEnvironment
			}
			if len(headers) > 0 {
				table["http_headers"] = headers
			}
		}
	case "setEnabled":
		if id == "" {
			return fail("请选择要启停的服务")
		}
		table["enabled"] = in.Enabled
	case "delete":
		if id == "" {
			return fail("请选择要删除的服务")
		}
	default:
		return fail("工具配置操作无效")
	}
	type edit struct {
		Key      string `json:"keyPath"`
		Value    any    `json:"value"`
		Strategy string `json:"mergeStrategy"`
	}
	key := func(name string) string { return "mcp_servers." + strconv.Quote(name) }
	edits := []edit{}
	if in.Action == "delete" || in.ServiceID != "" && id != in.ServiceID {
		edits = append(edits, edit{key(in.ServiceID), nil, "replace"})
	}
	if in.Action != "delete" {
		edits = append(edits, edit{key(id), table, "replace"})
	}
	var result struct {
		Status     string          `json:"status"`
		Overridden json.RawMessage `json:"overriddenMetadata"`
	}
	err = r.call(ctx, "config/batchWrite", map[string]any{"filePath": file, "expectedVersion": version, "reloadUserConfig": false, "edits": edits}, &result)
	if err != nil {
		return fail("保存结果未确认，请刷新配置后核对")
	}
	if result.Status != "ok" || len(result.Overridden) > 0 && string(result.Overridden) != "null" {
		return fail("配置受到其他配置层限制，请刷新核对")
	}
	return domain.ToolServiceChangeResult{ServiceID: id, Saved: true}, nil
}

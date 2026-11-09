package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"shioji/internal/domain"
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
func retainedToolValues(values map[string]string) []domain.ToolServiceValue {
	out := []domain.ToolServiceValue{}
	for name := range values {
		out = append(out, domain.ToolServiceValue{Name: name, Retained: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
	config := domain.ToolServiceConfiguration{Name: s.ID, Enabled: native.Enabled == nil || *native.Enabled, Command: native.Command, Arguments: native.Args, Environment: retainedToolValues(native.Env), InheritedEnvironment: native.EnvVars, Directory: native.Directory, URL: publicEndpoint(native.URL), TokenEnvironment: native.TokenEnvironment, Headers: retainedToolValues(native.Headers), StartupSeconds: native.StartupSeconds}
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

func validateToolConfiguration(in domain.ToolServiceConfiguration) error {
	if strings.TrimSpace(in.Name) != in.Name || in.Name == "" || len(in.Name) > 128 || strings.ContainsAny(in.Name, "\x00\r\n") {
		return errors.New("请输入有效的服务名称")
	}
	if in.Transport != "command" && in.Transport != "http" {
		return errors.New("请选择连接方式")
	}
	if in.Transport == "command" {
		if strings.TrimSpace(in.Command) == "" || len(in.Command) > 4096 || strings.ContainsRune(in.Command, 0) {
			return errors.New("请输入执行命令")
		}
		if in.Directory != "" && !strings.HasPrefix(in.Directory, "/") {
			return errors.New("工作目录须为所属设备的绝对路径")
		}
	} else {
		u, e := url.Parse(in.URL)
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || len(in.URL) > 8192 {
			return errors.New("请输入不含凭据的 HTTP 或 HTTPS 地址")
		}
	}
	if in.StartupSeconds < 0 || in.StartupSeconds > 600 {
		return errors.New("启动超时须在 0 到 600 秒之间")
	}
	if len(in.Arguments) > 256 || len(in.Environment) > 128 || len(in.Headers) > 128 || len(in.InheritedEnvironment) > 128 {
		return errors.New("配置项过多")
	}
	for _, arg := range in.Arguments {
		if len(arg) > 16<<10 || strings.ContainsRune(arg, 0) {
			return errors.New("命令参数无效")
		}
	}
	for _, name := range append(append([]string{}, in.InheritedEnvironment...), in.TokenEnvironment) {
		if name != "" && !toolEnvironmentName(name) {
			return errors.New("环境变量名无效")
		}
	}
	return nil
}
func toolEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
func mergeToolValues(rows []domain.ToolServiceValue, previous map[string]string, headers bool) (map[string]string, error) {
	values := map[string]string{}
	seen := map[string]bool{}
	for _, entry := range rows {
		if entry.Name == "" || len(entry.Name) > 256 || strings.ContainsAny(entry.Name, "\x00\r\n:") || !headers && !toolEnvironmentName(entry.Name) {
			return nil, errors.New("请检查变量或请求头名称")
		}
		identity := entry.Name
		if headers {
			identity = strings.ToLower(identity)
			for _, r := range entry.Name {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
					return nil, errors.New("请求头名称无效")
				}
			}
		}
		if seen[identity] {
			return nil, errors.New("变量或请求头名称不能重复")
		}
		seen[identity] = true
		if len(entry.Value) > 64<<10 || strings.ContainsRune(entry.Value, 0) || headers && strings.ContainsAny(entry.Value, "\r\n") {
			return nil, errors.New("变量或请求头值无效")
		}
		value := entry.Value
		if entry.Retained {
			var ok bool
			value, ok = previous[entry.Name]
			if !ok || entry.Value != "" {
				return nil, errors.New("原有私密值已变化，请重新打开编辑")
			}
		}
		values[entry.Name] = value
	}
	return values, nil
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
		if err = validateToolConfiguration(config); err != nil {
			return fail(err.Error())
		}
		id = config.Name
		if _, exists := c.Config.ToolServices[id]; exists && id != in.ServiceID {
			return fail("此服务名称已存在")
		}
		env, err := mergeToolValues(config.Environment, previous.Env, false)
		if err != nil {
			return fail(err.Error())
		}
		headers, err := mergeToolValues(config.Headers, previous.Headers, true)
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

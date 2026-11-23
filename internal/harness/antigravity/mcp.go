package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const userMCPFile = "config/mcp_config.json"
const projectMCPName = "mcp_config.json"

type mcpDocument struct {
	nativeConfigDocument
	servers map[string]json.RawMessage
}

type nativeMCPService struct {
	Command   string
	Args      []string
	Env       map[string]string
	CWD       string
	ServerURL string
	URL       string
	Headers   map[string]string
	Disabled  bool
}

func (s nativeMCPService) endpoint() string {
	if s.ServerURL != "" {
		return s.ServerURL
	}
	return s.URL
}

func readMCP(base, name string) (mcpDocument, error) {
	return parseMCP(readNativeConfig(base, name))
}

func parseMCP(native nativeConfigDocument, err error) (mcpDocument, error) {
	doc := mcpDocument{nativeConfigDocument: native}
	if err != nil {
		return doc, err
	}
	if servers := doc.fields["mcpServers"]; servers != nil && json.Unmarshal(servers, &doc.servers) != nil {
		return doc, errors.New("无法解析 Antigravity MCP 服务列表")
	}
	if doc.servers == nil {
		doc.servers = map[string]json.RawMessage{}
	}
	return doc, nil
}

func (doc mcpDocument) names() []string {
	names := make([]string, 0, len(doc.servers))
	for name := range doc.servers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func projectMCPService(name string, raw json.RawMessage) (domain.ToolService, nativeMCPService) {
	service := domain.ToolService{ID: name, Name: name, State: domain.ToolServiceUnknown}
	var native nativeMCPService
	if json.Unmarshal(raw, &native) != nil {
		service.ReadOnlyReason = "无法解析此服务"
		return service, native
	}
	if native.Disabled {
		service.State = domain.ToolServiceDisabled
	}
	cfg := domain.ToolServiceConfiguration{Name: name, Enabled: !native.Disabled}
	endpoint := native.endpoint()
	switch {
	case native.Command != "" && endpoint == "":
		cfg.Transport, cfg.Command, cfg.Arguments, cfg.Directory = "command", native.Command, native.Args, native.CWD
		cfg.Environment = harness.RetainedToolValues(native.Env)
	case native.Command == "" && endpoint != "":
		cfg.Transport = "http"
		cfg.Headers = harness.RetainedToolValues(native.Headers)
		if u, err := url.Parse(endpoint); err == nil && u.Hostname() != "" && (u.Scheme == "https" || u.Scheme == "http") {
			u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
			cfg.URL = u.String()
			service.Origin = u.Scheme + "://" + u.Host
		}
		if cfg.URL == "" || cfg.URL != endpoint {
			service.ReadOnlyReason = "服务地址包含私有参数或格式不受支持，请在原配置中编辑"
		}
	default:
		service.ReadOnlyReason = "此连接类型由 Antigravity 原生配置管理"
	}
	service.Transport, service.Configuration = cfg.Transport, &cfg
	service.Editable = service.ReadOnlyReason == ""
	return service, native
}

func (r *runtime) mcpRevision(doc mcpDocument) string {
	digest := sha256.Sum256([]byte(filepath.Join(r.skillProfile, userMCPFile) + "\x00" + doc.revision))
	return hex.EncodeToString(digest[:])
}

func (r *runtime) ToolServices(ctx context.Context) (domain.ToolServiceCatalog, error) {
	if !filepath.IsAbs(r.skillProfile) {
		return domain.ToolServiceCatalog{}, harness.Unsupported("tool-services")
	}
	if err := ctx.Err(); err != nil {
		return domain.ToolServiceCatalog{}, err
	}
	doc, err := readMCP(r.skillProfile, userMCPFile)
	if err != nil {
		return domain.ToolServiceCatalog{}, err
	}
	catalog := domain.ToolServiceCatalog{CanAdd: true, Revision: r.mcpRevision(doc), BasicEditing: true, Services: []domain.ToolService{}}
	for _, name := range doc.names() {
		service, _ := projectMCPService(name, doc.servers[name])
		catalog.Services = append(catalog.Services, service)
	}
	return catalog, nil
}

func (*runtime) RefreshToolServices(context.Context) error {
	return harness.Unsupported("tool-service-live-refresh")
}

func (*runtime) BeginToolServiceAuthorization(context.Context, string) (harness.ToolServiceAuthorization, domain.SignInChallenge, error) {
	return nil, domain.SignInChallenge{}, harness.Unsupported("tool-service-authorization")
}

func (r *runtime) InspectToolServices(ctx context.Context, launch domain.LaunchContext, source string) (domain.ToolServiceInspection, error) {
	result := domain.ToolServiceInspection{Directory: launch.CWD, Entries: []domain.InspectedToolService{}}
	if source != "" {
		r.mu.Lock()
		matches := r.session.ThreadID == source && r.session.Context.CWD == launch.CWD
		r.mu.Unlock()
		if !matches {
			return result, errors.New("会话上下文已变化，请重新核对")
		}
		result.Warnings = append(result.Warnings, "原生配置的当前会话生效状态未知")
	}
	read := func(base, name string, scope domain.ToolServiceScope) error {
		if !filepath.IsAbs(base) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		doc, err := readMCP(base, name)
		if err != nil {
			return err
		}
		path := filepath.Join(base, name)
		for _, id := range doc.names() {
			service, native := projectMCPService(id, doc.servers[id])
			digest := sha256.Sum256([]byte(path + "\x00" + id))
			if scope == domain.ToolServiceProject {
				service.Editable = false
				if service.ReadOnlyReason == "" {
					service.ReadOnlyReason = "项目配置，只读"
				}
			}
			result.Entries = append(result.Entries, domain.InspectedToolService{
				ToolService: service, EnabledConfigured: !native.Disabled, ArgumentCount: len(native.Args),
				EntryID: hex.EncodeToString(digest[:]), Scope: scope, ConfigurationPath: path,
			})
		}
		return nil
	}
	for _, cwd := range projectNativeConfigBases(launch) {
		for _, configDir := range projectNativeConfigDirectories {
			if err := read(cwd, filepath.Join(configDir, projectMCPName), domain.ToolServiceProject); err != nil {
				return result, err
			}
		}
	}
	if err := read(r.skillProfile, userMCPFile, domain.ToolServiceUser); err != nil {
		return result, err
	}
	counts := map[string]int{}
	for _, entry := range result.Entries {
		counts[entry.ID]++
	}
	for i := range result.Entries {
		entry := &result.Entries[i]
		entry.PrimarySource = counts[entry.ID] == 1 && entry.EnabledConfigured
	}
	return result, nil
}

func (r *runtime) ChangeToolService(ctx context.Context, change domain.ToolServiceChange) (domain.ToolServiceChangeResult, error) {
	var none domain.ToolServiceChangeResult
	if !filepath.IsAbs(r.skillProfile) {
		return none, harness.Unsupported("tool-service-configuration")
	}
	if err := ctx.Err(); err != nil {
		return none, err
	}
	if err := os.MkdirAll(r.skillProfile, 0700); err != nil {
		return none, errors.New("无法创建 Antigravity 配置目录")
	}
	root, err := os.OpenRoot(r.skillProfile)
	if err != nil {
		return none, errors.New("无法打开 Antigravity 配置目录")
	}
	defer root.Close()
	doc, err := parseMCP(readNativeConfigFile(root, userMCPFile))
	if err != nil {
		return none, err
	}
	if change.Revision == "" || change.Revision != r.mcpRevision(doc) {
		return none, errors.New("配置已变化，请刷新后重新编辑")
	}
	id := change.ServiceID
	old := doc.servers[id]
	service, native := projectMCPService(id, old)
	if id != "" && (old == nil || !service.Editable) {
		return none, errors.New("该服务的设备配置不可编辑")
	}
	var entry map[string]json.RawMessage
	if old != nil && json.Unmarshal(old, &entry) != nil {
		return none, errors.New("无法读取原服务配置")
	}
	switch change.Action {
	case "save":
		if change.Configuration == nil {
			return none, errors.New("缺少 MCP 配置")
		}
		cfg := *change.Configuration
		if cfg.Name != id && doc.servers[cfg.Name] != nil {
			return none, errors.New("已有同名服务")
		}
		entry, err = editMCPEntry(cfg, native, entry)
		if err != nil {
			return none, err
		}
		delete(doc.servers, id)
		id = cfg.Name
	case "setEnabled":
		if old == nil {
			return none, errors.New("找不到设备服务")
		}
		entry["disabled"], _ = json.Marshal(!change.Enabled)
	case "delete":
		if old == nil {
			return none, errors.New("找不到设备服务")
		}
		delete(doc.servers, id)
		entry = nil
	default:
		return none, errors.New("不支持的 MCP 配置操作")
	}
	if entry != nil {
		doc.servers[id], err = json.Marshal(entry)
		if err != nil {
			return none, err
		}
	}
	doc.fields["mcpServers"], _ = json.Marshal(doc.servers)
	if err := writeNativeConfig(ctx, root, userMCPFile, doc.nativeConfigDocument, 0600); err != nil {
		return none, err
	}
	return domain.ToolServiceChangeResult{ServiceID: id, Saved: true}, nil
}

func editMCPEntry(cfg domain.ToolServiceConfiguration, old nativeMCPService, entry map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if err := harness.ValidateToolConfiguration(cfg); err != nil {
		return nil, err
	}
	// Existing clients supply their form's default startup value. The native
	// tool execution timeout is a different field and keeps its stored value.
	if len(cfg.InheritedEnvironment) != 0 || cfg.TokenEnvironment != "" || cfg.StartupSeconds != 0 && cfg.StartupSeconds != 10 {
		return nil, errors.New("该 Agent 暂不支持此高级连接设置")
	}
	previousURL, _ := url.Parse(old.endpoint())
	nextURL, _ := url.Parse(cfg.URL)
	sameOrigin := previousURL != nil && nextURL != nil && previousURL.Scheme == nextURL.Scheme && strings.EqualFold(previousURL.Host, nextURL.Host)
	nativeAuth := entry["oauth"] != nil || entry["authProviderType"] != nil
	if nativeAuth && (cfg.Transport != "http" || !sameOrigin) ||
		cfg.Transport == "http" && !sameOrigin && slices.ContainsFunc(cfg.Headers, func(h domain.ToolServiceValue) bool { return h.Retained }) {
		return nil, errors.New("更改服务来源需要重新配置请求头或原生授权")
	}
	if entry == nil {
		entry = map[string]json.RawMessage{}
	}
	set := func(name string, value any, present bool) {
		if present {
			entry[name], _ = json.Marshal(value)
		} else {
			delete(entry, name)
		}
	}
	set("disabled", true, !cfg.Enabled)
	if cfg.Transport == "command" {
		env, err := harness.MergeToolValues(cfg.Environment, old.Env, false)
		if err != nil {
			return nil, err
		}
		set("command", cfg.Command, true)
		set("args", cfg.Arguments, len(cfg.Arguments) != 0)
		set("env", env, len(env) != 0)
		set("cwd", cfg.Directory, cfg.Directory != "")
		for _, key := range []string{"serverUrl", "url", "headers"} {
			delete(entry, key)
		}
	} else {
		headers, err := harness.MergeToolValues(cfg.Headers, old.Headers, true)
		if err != nil {
			return nil, err
		}
		set("serverUrl", cfg.URL, true)
		set("headers", headers, len(headers) != 0)
		for _, key := range []string{"command", "args", "env", "cwd", "url"} {
			delete(entry, key)
		}
	}
	return entry, nil
}

package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const mcpFile = ".mcp.json"
const goalService = "shioji_goal"

type mcpDocument struct {
	fields, servers map[string]json.RawMessage
	revision        string
}

type nativeMCP struct {
	Type, Command, URL string
	Args               []string
	Env, Headers       map[string]string
	Disabled           bool
}

type mcpObservation struct {
	Name, Status string
	Tools        []domain.ToolServiceDefinition
}

func (m mcpObservation) state() domain.ToolServiceState {
	switch m.Status {
	case "connected":
		return domain.ToolServiceReady
	case "needs-auth":
		return domain.ToolServiceAuthorizationRequired
	case "failed":
		return domain.ToolServiceFailed
	case "pending", "connecting":
		return domain.ToolServiceConnecting
	case "disabled":
		return domain.ToolServiceDisabled
	default:
		return domain.ToolServiceUnknown
	}
}

func readMCP(path string) (mcpDocument, error) {
	doc := mcpDocument{fields: map[string]json.RawMessage{}, servers: map[string]json.RawMessage{}}
	info, err := os.Lstat(path)
	var raw []byte
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return doc, errors.New("Claude Code MCP 配置文件不可读")
		}
		raw, err = os.ReadFile(path)
		if err != nil {
			return doc, err
		}
		if json.Unmarshal(raw, &doc.fields) != nil || doc.fields == nil {
			return doc, errors.New("Claude Code MCP 配置无效")
		}
		if servers := doc.fields["mcpServers"]; servers != nil && json.Unmarshal(servers, &doc.servers) != nil {
			return doc, errors.New("Claude Code MCP 服务列表无效")
		}
		if doc.servers == nil {
			doc.servers = map[string]json.RawMessage{}
		}
	} else if !os.IsNotExist(err) {
		return doc, err
	}
	hash := sha256.Sum256(append([]byte(path+"\x00"), raw...))
	doc.revision = hex.EncodeToString(hash[:])
	return doc, nil
}

func (r *runtime) mcpConfiguration() (mcpDocument, error) {
	if !filepath.IsAbs(r.home) {
		return mcpDocument{}, errors.New("Claude Code API Profile 不可用")
	}
	return readMCP(filepath.Join(r.home, mcpFile))
}

func projectMCP(id string, raw json.RawMessage) (domain.ToolService, nativeMCP) {
	state := domain.ToolService{ID: id, Name: id, State: domain.ToolServiceNotLoaded}
	var source nativeMCP
	if json.Unmarshal(raw, &source) != nil {
		state.ReadOnlyReason = "无法读取此服务配置"
		return state, source
	}
	if source.Disabled {
		state.State = domain.ToolServiceDisabled
	}
	cfg := &domain.ToolServiceConfiguration{Name: id, Enabled: !source.Disabled}
	switch source.Type {
	case "", "stdio":
		if source.Command != "" {
			cfg.Transport, cfg.Command, cfg.Arguments = "command", source.Command, source.Args
			cfg.Environment = harness.RetainedToolValues(source.Env)
		}
	case "http":
		if u, err := url.Parse(source.URL); err == nil && domain.ValidAuthorizationURL(source.URL) && u.RawQuery == "" && u.Fragment == "" {
			cfg.Transport, cfg.URL = "http", source.URL
			cfg.Headers = harness.RetainedToolValues(source.Headers)
			state.Origin = u.Scheme + "://" + u.Host
		}
	}
	if cfg.Transport == "" {
		state.ReadOnlyReason = "此连接类型或私有地址由 Claude Code 原生配置管理"
		return state, source
	}
	state.Editable, state.Configuration, state.Transport = true, cfg, cfg.Transport
	return state, source
}

func (r *runtime) ToolServices(ctx context.Context) (domain.ToolServiceCatalog, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolServiceCatalog{}, err
	}
	doc, err := r.mcpConfiguration()
	if err != nil {
		return domain.ToolServiceCatalog{}, err
	}
	out := domain.ToolServiceCatalog{Revision: doc.revision, CanAdd: true, BasicEditing: true, Services: []domain.ToolService{}}
	for _, id := range slices.Sorted(maps.Keys(doc.servers)) {
		service, _ := projectMCP(id, doc.servers[id])
		out.Services = append(out.Services, service)
	}
	return out, nil
}

func (*runtime) RefreshToolServices(context.Context) error {
	return harness.Unsupported("tool-service-live-refresh")
}

func (*runtime) BeginToolServiceAuthorization(context.Context, string) (harness.ToolServiceAuthorization, domain.SignInChallenge, error) {
	return nil, domain.SignInChallenge{}, harness.Unsupported("tool-service-authorization")
}

func (r *runtime) InspectToolServices(ctx context.Context, launch domain.LaunchContext, source string) (domain.ToolServiceInspection, error) {
	out := domain.ToolServiceInspection{Directory: launch.CWD}
	var observed map[string]mcpObservation
	var observedRevision string
	if source != "" {
		r.mu.Lock()
		matches := r.session.ThreadID == source && r.session.Context.CWD == launch.CWD
		observed = maps.Clone(r.mcp)
		observedRevision = r.mcpRevision
		r.mu.Unlock()
		if !matches {
			return out, errors.New("Claude Code 会话上下文已改变")
		}
	}
	catalogPath := filepath.Join(r.home, mcpFile)
	projectPaths := append([]string{launch.CWD}, launch.AdditionalDirectories...)
	paths := append([]string{catalogPath}, projectPaths...)
	for i, path := range paths {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if i > 0 {
			if !filepath.IsAbs(path) {
				continue
			}
			path = filepath.Join(path, mcpFile)
		}
		doc, err := readMCP(path)
		if err != nil {
			return out, err
		}
		if i == 0 && source != "" {
			out.RuntimeKnown = observed != nil && observedRevision == doc.revision
			if !out.RuntimeKnown && observed != nil {
				out.Warnings = append(out.Warnings, "会话尚未加载当前 MCP 配置")
			}
		}
		for _, id := range slices.Sorted(maps.Keys(doc.servers)) {
			service, raw := projectMCP(id, doc.servers[id])
			entry := domain.InspectedToolService{ToolService: service, EntryID: doc.revision + ":" + id, ConfigurationPath: path,
				Scope: domain.ToolServiceUser, EnabledConfigured: !raw.Disabled, PrimarySource: i == 0 && !raw.Disabled,
				ArgumentCount: len(raw.Args)}
			if i != 0 {
				entry.Scope, entry.Editable, entry.Configuration = domain.ToolServiceProject, false, nil
				entry.IgnoredReason = "独立 API Profile 不加载项目 MCP 配置"
			} else if obs, ok := observed[id]; out.RuntimeKnown && ok {
				entry.State = obs.state()
				entry.Tools = len(obs.Tools)
				for _, def := range obs.Tools {
					entry.Definitions = append(entry.Definitions, domain.ToolServiceDefinition{
						Name: r.redact(def.Name), Description: r.redact(def.Description),
					})
				}
			}
			out.Entries = append(out.Entries, entry)
		}
	}
	return out, nil
}

func (r *runtime) ChangeToolService(ctx context.Context, change domain.ToolServiceChange) (domain.ToolServiceChangeResult, error) {
	var out domain.ToolServiceChangeResult
	doc, err := r.mcpConfiguration()
	if err != nil {
		return out, err
	}
	if change.Revision == "" || change.Revision != doc.revision {
		return out, errors.New("Claude Code MCP 配置已变化，请刷新后重试")
	}
	id := change.ServiceID
	entry := map[string]json.RawMessage{}
	var old nativeMCP
	if id != "" {
		service, native := projectMCP(id, doc.servers[id])
		if !service.Editable || json.Unmarshal(doc.servers[id], &entry) != nil {
			return out, errors.New("此 Claude Code 服务不可编辑")
		}
		old = native
	}
	switch change.Action {
	case "save":
		if change.Configuration == nil {
			return out, errors.New("缺少 Claude Code MCP 配置")
		}
		cfg := *change.Configuration
		if cfg.Name == goalService {
			return out, errors.New("此 MCP 名称由 Shioji 的目标进度工具使用")
		}
		if cfg.Name != id && doc.servers[cfg.Name] != nil {
			return out, errors.New("存在同名 Claude Code MCP 服务")
		}
		if err := editMCP(entry, cfg, old); err != nil {
			return out, err
		}
		delete(doc.servers, id)
		id = cfg.Name
	case "delete":
		if id == "" {
			return out, errors.New("缺少 Claude Code MCP 服务标识")
		}
		delete(doc.servers, id)
		entry = nil
	case "setEnabled":
		if id == "" {
			return out, errors.New("缺少 Claude Code MCP 服务标识")
		}
		entry["disabled"], _ = json.Marshal(!change.Enabled)
	default:
		return out, errors.New("不支持的 Claude Code MCP 配置操作")
	}
	if entry != nil {
		doc.servers[id], _ = json.Marshal(entry)
	}
	doc.fields["mcpServers"], _ = json.Marshal(doc.servers)
	body, _ := json.MarshalIndent(doc.fields, "", "  ")
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := savePrivate(filepath.Join(r.home, mcpFile), body); err != nil {
		return out, err
	}
	return domain.ToolServiceChangeResult{ServiceID: id, Saved: true}, nil
}

func editMCP(entry map[string]json.RawMessage, cfg domain.ToolServiceConfiguration, old nativeMCP) error {
	if err := harness.ValidateToolConfiguration(cfg); err != nil {
		return err
	}
	if cfg.Directory != "" || cfg.TokenEnvironment != "" || len(cfg.InheritedEnvironment) != 0 || cfg.StartupSeconds != 0 && cfg.StartupSeconds != 10 {
		return errors.New("Claude Code SDK 不支持所选 MCP 高级设置")
	}
	set := func(k string, v any) { entry[k], _ = json.Marshal(v) }
	set("disabled", !cfg.Enabled)
	if cfg.Transport == "command" {
		changed := cfg.Command != old.Command || !slices.Equal(cfg.Arguments, old.Args)
		if changed && slices.ContainsFunc(cfg.Environment, func(v domain.ToolServiceValue) bool { return v.Retained }) {
			return errors.New("更改服务命令或参数时须重新填写私密环境变量")
		}
		env, err := harness.MergeToolValues(cfg.Environment, old.Env, false)
		if err != nil {
			return err
		}
		set("type", "stdio")
		set("command", cfg.Command)
		set("args", append([]string{}, cfg.Arguments...))
		set("env", env)
		delete(entry, "url")
		delete(entry, "headers")
		delete(entry, "oauth")
		return nil
	}
	prev, _ := url.Parse(old.URL)
	next, _ := url.Parse(cfg.URL)
	sameOrigin := prev != nil && next != nil && prev.Scheme == next.Scheme && strings.EqualFold(prev.Host, next.Host)
	if !sameOrigin && (entry["oauth"] != nil || slices.ContainsFunc(cfg.Headers, func(h domain.ToolServiceValue) bool { return h.Retained })) {
		return errors.New("更改服务来源时须重新填写私密请求头或原生授权")
	}
	headers, err := harness.MergeToolValues(cfg.Headers, old.Headers, true)
	if err != nil {
		return err
	}
	set("type", "http")
	set("url", cfg.URL)
	set("headers", headers)
	for _, k := range []string{"command", "args", "env"} {
		delete(entry, k)
	}
	return nil
}

func (r *runtime) executionMCP() (map[string]json.RawMessage, string, error) {
	if r.title || r.home == "" {
		return nil, "", nil
	}
	doc, err := r.mcpConfiguration()
	if err != nil {
		return nil, "", err
	}
	enabled := make(map[string]json.RawMessage)
	for id, raw := range doc.servers {
		if id == goalService {
			continue
		}
		var native nativeMCP
		if json.Unmarshal(raw, &native) != nil || native.Disabled {
			continue
		}
		var entry map[string]json.RawMessage
		if json.Unmarshal(raw, &entry) != nil || entry == nil {
			continue
		}
		delete(entry, "disabled")
		if native.Type == "" || native.Type == "stdio" {
			if native.Env == nil {
				native.Env = map[string]string{}
			}
			native.Env["ANTHROPIC_API_KEY"], native.Env["ANTHROPIC_AUTH_TOKEN"], native.Env["ANTHROPIC_BASE_URL"] = "", "", ""
			entry["env"], _ = json.Marshal(native.Env)
		}
		enabled[id], _ = json.Marshal(entry)
	}
	return enabled, doc.revision, nil
}

var _ harness.ToolServiceInspector = (*runtime)(nil)
var _ harness.ToolServiceRuntime = (*runtime)(nil)
var _ harness.ToolServiceEditor = (*runtime)(nil)

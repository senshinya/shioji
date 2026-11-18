package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type nativeHook struct {
	Key, CurrentHash, EventName, HandlerType, Source, SourcePath, TrustStatus string
	Command, Server, Tool, PluginID, Matcher, StatusMessage                   string
	Enabled, IsManaged, Async                                                 bool
	TimeoutSec                                                                uint64
}

func hookScope(h nativeHook) domain.HookScope {
	if h.Source == "project" {
		return domain.HookProject
	}
	if h.IsManaged {
		return domain.HookManaged
	}
	switch h.Source {
	case "user":
		return domain.HookUser
	case "plugin":
		return domain.HookPlugin
	}
	return domain.HookUnknown
}

func hookIdentity(h nativeHook) string {
	sum := sha256.Sum256([]byte(h.Source + "\x00" + h.SourcePath + "\x00" + h.Key))
	return hex.EncodeToString(sum[:])
}

func hookEventLabel(event string) string {
	first, size := utf8.DecodeRuneInString(event)
	if size == 0 {
		return "Hook"
	}
	return string(unicode.ToUpper(first)) + event[size:]
}

func (r *Runtime) nativeHooks(ctx context.Context, cwd, source string) ([]nativeHook, []string, error) {
	if !r.supports("hooks/list") {
		return nil, nil, harness.Unsupported("hook-management")
	}
	params := map[string]any{"cwds": []string{cwd}}
	if source != "" {
		if r.protocol == nil || !r.protocol.scopedHooks {
			return nil, nil, harness.Unsupported("loaded-hooks")
		}
		params = map[string]any{"threadId": source}
	}
	var response struct {
		Data []struct {
			CWD      string
			Hooks    []nativeHook
			Warnings []string
			Errors   []struct{ Path, Message string }
		}
	}
	if err := r.call(ctx, "hooks/list", params, &response); err != nil {
		return nil, nil, errors.New("无法读取 Hook，请刷新重试")
	}
	var hooks []nativeHook
	var warnings []string
	matched := false
	for _, entry := range response.Data {
		if sourceExecutionPath(entry.CWD) != sourceExecutionPath(cwd) {
			return nil, nil, errors.New("Hook 列表与所属目录不一致")
		}
		matched = true
		hooks = append(hooks, entry.Hooks...)
		for _, warning := range entry.Warnings {
			warnings = append(warnings, bounded(warning))
		}
		for _, problem := range entry.Errors {
			warnings = append(warnings, bounded(problem.Path+": "+problem.Message))
		}
	}
	if !matched {
		return nil, nil, errors.New("运行服务没有返回此目录的 Hook")
	}
	return hooks, warnings, nil
}

// A cwd-scoped inventory cannot establish global provenance if it includes
// an active project layer. In particular HOME may itself be a trusted project.
func (r *Runtime) globalHookDirectory(ctx context.Context) (string, error) {
	cwd := environmentValue(r.processEnvironment(), "HOME")
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", errors.New("设备未提供用户目录")
	}
	config, err := r.skillConfiguration(ctx, cwd)
	if err != nil {
		return "", errors.New("无法确认全局 Hook 的配置范围")
	}
	for _, layer := range config.Layers {
		if layer.Name.Type == "project" && layer.DisabledReason == nil {
			return "", errors.New("用户目录包含项目配置，无法独立确认全局 Hooks")
		}
	}
	return cwd, nil
}

func (r *Runtime) managedHooks(ctx context.Context, launch domain.LaunchContext, source string) (domain.HookCatalog, nativeConfiguration, map[string]nativeHook, error) {
	out := domain.HookCatalog{Directory: launch.CWD, RuntimeKnown: source != "", Entries: []domain.HookEntry{}}
	globalCWD := environmentValue(r.processEnvironment(), "HOME")
	if globalCWD == "" || !filepath.IsAbs(globalCWD) {
		return out, nativeConfiguration{}, nil, errors.New("设备未提供用户目录")
	}
	if launch.CWD == "" {
		verified, err := r.globalHookDirectory(ctx)
		if err != nil {
			return out, nativeConfiguration{}, nil, err
		}
		globalCWD = verified
	}
	cwd := launch.CWD
	if cwd == "" {
		if source != "" {
			return out, nativeConfiguration{}, nil, errors.New("缺少会话所属目录")
		}
		cwd = globalCWD
	}
	hooks, warnings, err := r.nativeHooks(ctx, cwd, source)
	if err != nil {
		return out, nativeConfiguration{}, nil, err
	}
	config, err := r.skillConfiguration(ctx, launch.CWD)
	if err != nil {
		return out, config, nil, errors.New("无法读取 Hook 配置，请刷新重试")
	}
	file, version, _ := toolUserLayer(config)
	writable := file != "" && version != "" && r.supports("config/batchWrite")
	// A plugin offered only by a project cannot be relabelled global merely
	// because its handler lives in a shared plugin cache.
	globalPlugins := map[string]bool{}
	if launch.CWD != "" {
		for _, h := range hooks {
			if h.Source != "plugin" {
				continue
			}
			verified, e := r.globalHookDirectory(ctx)
			if e != nil {
				warnings = append(warnings, "无法独立确认插件的全局来源，相关项目条目只读")
				break
			}
			globals, _, e := r.nativeHooks(ctx, verified, "")
			if e == nil {
				for _, g := range globals {
					if g.Source == "plugin" {
						globalPlugins[hookIdentity(g)] = true
					}
				}
			}
			break
		}
	}
	out.Warnings = warnings
	byID := map[string]nativeHook{}
	for _, h := range hooks {
		scope := hookScope(h)
		if launch.CWD == "" && (scope == domain.HookProject || scope == domain.HookUnknown) {
			continue
		}
		id := hookIdentity(h)
		if _, seen := byID[id]; seen || h.Key == "" || h.CurrentHash == "" {
			continue
		}
		if launch.CWD != "" && scope == domain.HookPlugin && !globalPlugins[id] {
			scope = domain.HookUnknown
		}
		kind := "unknown"
		switch h.HandlerType {
		case "command":
			kind = "command"
		case "mcpTool":
			kind = "tool"
		}
		entry := domain.HookEntry{ID: id, DefinitionRevision: h.CurrentHash, Event: hookEventLabel(h.EventName), Kind: kind, Command: h.Command, Service: h.Server, Tool: h.Tool, Scope: scope, SourcePath: h.SourcePath, Plugin: h.PluginID, Matcher: h.Matcher, StatusMessage: h.StatusMessage, Fingerprint: h.CurrentHash, TimeoutSeconds: h.TimeoutSec, Async: h.Async, Enabled: h.Enabled}
		switch scope {
		case domain.HookProject:
			entry.Source = "项目"
		case domain.HookUser:
			entry.Source = "用户"
		case domain.HookPlugin:
			entry.Source = "插件"
		case domain.HookManaged:
			entry.Source = "策略"
		default:
			entry.Source = "其他来源"
		}
		switch h.TrustStatus {
		case "untrusted":
			entry.Trust = domain.HookUntrusted
		case "modified":
			entry.Trust = domain.HookModified
		case "trusted":
			entry.Trust = domain.HookTrusted
		case "managed":
			entry.Trust = domain.HookPolicy
		}
		// The native list omits configured MCP input arguments, so server/tool
		// alone are insufficient for a version-bound review.
		knownHandler := h.HandlerType == "command" && strings.TrimSpace(h.Command) != ""
		owned := launch.CWD == "" && scope != domain.HookProject || launch.CWD != "" && scope == domain.HookProject
		entry.CanEnable = writable && knownHandler && owned && scope != domain.HookUnknown && !h.IsManaged && entry.Trust != "" && entry.Trust != domain.HookPolicy
		entry.CanTrust = entry.CanEnable && (entry.Trust == domain.HookUntrusted || entry.Trust == domain.HookModified)
		if !entry.CanEnable {
			entry.ReadOnlyReason = "当前来源只读"
			if launch.CWD != "" && scope != domain.HookProject && scope != domain.HookUnknown && !h.IsManaged {
				entry.ReadOnlyReason = "在设备设置中管理"
			}
			if !knownHandler {
				entry.ReadOnlyReason = "此类型未提供完整审核内容"
			}
		}
		byID[id] = h
		out.Entries = append(out.Entries, entry)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].ID < out.Entries[j].ID })
	b, _ := json.Marshal(struct {
		Configuration string
		Entries       []domain.HookEntry
	}{toolConfigurationRevision(config), out.Entries})
	sum := sha256.Sum256(b)
	out.Revision = hex.EncodeToString(sum[:])
	return out, config, byID, nil
}

func (r *Runtime) Hooks(ctx context.Context, launch domain.LaunchContext, source string) (domain.HookCatalog, error) {
	out, _, _, err := r.managedHooks(ctx, launch, source)
	return out, err
}

func (r *Runtime) ChangeHook(ctx context.Context, launch domain.LaunchContext, in domain.HookChange) (domain.HookChangeResult, error) {
	catalog, config, native, err := r.managedHooks(ctx, launch, "")
	if err != nil {
		return domain.HookChangeResult{}, err
	}
	if !in.Valid() || in.Revision != catalog.Revision {
		return domain.HookChangeResult{}, errors.New("配置已变化，请刷新后重新审核")
	}
	targets := in.MatchingEntries(catalog)
	if len(targets) == 0 {
		return domain.HookChangeResult{}, errors.New("定义已变化，请刷新后重新审核")
	}
	edits := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if !in.Allowed(target) {
			return domain.HookChangeResult{}, errors.New("当前入口不能修改此 Hook")
		}
		h := native[target.ID]
		field, value := "enabled", any(in.Enabled)
		if in.Action != domain.HookSetEnabled {
			field, value = "trusted_hash", h.CurrentHash
		}
		edits = append(edits, map[string]any{"keyPath": "hooks.state." + strconv.Quote(h.Key) + "." + field, "value": value, "mergeStrategy": "replace"})
	}
	file, version, _ := toolUserLayer(config)
	var result struct {
		Status             string
		OverriddenMetadata json.RawMessage
	}
	err = r.call(ctx, "config/batchWrite", map[string]any{"filePath": file, "expectedVersion": version, "reloadUserConfig": true, "edits": edits}, &result)
	if err != nil {
		return domain.HookChangeResult{}, &harness.DeliveryError{Uncertain: harness.IsUncertain(err), Message: "保存未确认，请刷新后核对配置"}
	}
	if result.Status != "ok" || len(result.OverriddenMetadata) > 0 && string(result.OverriddenMetadata) != "null" {
		return domain.HookChangeResult{}, errors.New("配置受到策略限制，请刷新核对")
	}
	updated, err := r.Hooks(ctx, launch, "")
	if err == nil && in.Observed(updated) {
		return domain.HookChangeResult{Saved: true, Catalog: updated}, nil
	}
	return domain.HookChangeResult{}, &harness.DeliveryError{Uncertain: true, Message: "保存后定义或状态已变化，请刷新核对"}
}

func (r *Runtime) RefreshHooks(ctx context.Context) error {
	if !r.supports("hooks/list") || !r.supports("config/batchWrite") {
		return harness.Unsupported("hook-configuration-refresh")
	}
	var result struct{ Status string }
	if err := r.call(ctx, "config/batchWrite", map[string]any{"edits": []any{}, "reloadUserConfig": true}, &result); err != nil {
		return err
	}
	if result.Status != "ok" {
		return errors.New("当前会话尚未确认 Hook 配置刷新")
	}
	return nil
}

func (r *Runtime) refreshHooksBeforeInput(ctx context.Context) error {
	if r.supports("hooks/list") && r.supports("config/batchWrite") {
		if err := r.RefreshHooks(ctx); err != nil {
			// No turn or user input has been sent; uncertainty belongs to the
			// configuration reload, never to the not-yet-submitted prompt.
			return errors.New("Hook 配置尚未确认，本轮输入未发送")
		}
	}
	return nil
}

var _ harness.HookProvider = (*Runtime)(nil)

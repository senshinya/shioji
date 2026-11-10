package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sort"
)

func toolInspectionSources(c nativeConfiguration, cwd string) domain.ToolServiceInspection {
	out := domain.ToolServiceInspection{Directory: cwd, Entries: []domain.InspectedToolService{}}
	seen := map[string]bool{}
	appendEntry := func(id, path string, scope domain.ToolServiceScope, raw json.RawMessage, ignored string) {
		projected := c
		projected.Config.ToolServices = map[string]json.RawMessage{id: raw}
		service := domain.ToolService{ID: id, Name: id, State: domain.ToolServiceUnknown}
		(&Runtime{}).projectToolConfiguration(projected, &service)
		service.Editable = false
		service.CanAuthorize = false
		service.ReadOnlyReason = "只读配置来源"
		if service.Configuration != nil && !service.Configuration.Enabled {
			service.State = domain.ToolServiceDisabled
		}
		sum := sha256.Sum256([]byte(path + "\x00" + id + "\x00" + ignored))
		out.Entries = append(out.Entries, domain.InspectedToolService{ToolService: service, EntryID: hex.EncodeToString(sum[:]), Scope: scope, ConfigurationPath: path, IgnoredReason: ignored})
	}
	for _, layer := range c.Layers {
		var values struct {
			Services map[string]json.RawMessage `json:"mcp_servers"`
		}
		if json.Unmarshal(layer.Config, &values) != nil {
			continue
		}
		path, scope := layer.Name.File, domain.ToolServiceOther
		switch layer.Name.Type {
		case "project":
			scope = domain.ToolServiceProject
			path = filepath.Join(layer.Name.DotCodexFolder, "config.toml")
		case "user":
			scope = domain.ToolServiceUser
		case "system", "mdm":
			scope = domain.ToolServiceManaged
		}
		ignored := ""
		if layer.DisabledReason != nil {
			ignored = bounded(*layer.DisabledReason)
			if len(values.Services) > 0 {
				out.Warnings = append(out.Warnings, path+": "+ignored)
			}
		}
		for id, raw := range values.Services {
			if ignored != "" {
				appendEntry(id, path, scope, raw, ignored)
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			if effective, ok := c.Config.ToolServices[id]; ok {
				raw = effective
			}
			appendEntry(id, path, scope, raw, "")
		}
	}
	for id, raw := range c.Config.ToolServices {
		if !seen[id] {
			appendEntry(id, "", domain.ToolServiceOther, raw, "")
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.ConfigurationPath != b.ConfigurationPath {
			return a.ConfigurationPath < b.ConfigurationPath
		}
		return a.ID < b.ID
	})
	return out
}

func (r *Runtime) InspectToolServices(ctx context.Context, launch domain.LaunchContext, source string) (domain.ToolServiceInspection, error) {
	c, err := r.skillConfiguration(ctx, launch.CWD)
	if err != nil {
		// Read outside the failed project, without opening or rewriting its file.
		// Keep only independently owned sources; root-local project config is not
		// part of this workspace and must never be offered as an inherited source.
		global, e := r.skillConfiguration(ctx, string(filepath.Separator))
		if e != nil {
			return domain.ToolServiceInspection{}, errors.New("无法读取此目录的 MCP 配置，请检查项目配置后重试")
		}
		fallback := toolInspectionSources(global, launch.CWD)
		entries := fallback.Entries[:0]
		for _, entry := range fallback.Entries {
			if entry.Scope == domain.ToolServiceUser || entry.Scope == domain.ToolServiceManaged {
				entries = append(entries, entry)
			}
		}
		fallback.Entries = entries
		fallback.Warnings = append(fallback.Warnings, "项目配置读取失败，显示可读取的继承配置；会话加载状态尚未核对")
		return fallback, nil
	}
	out := toolInspectionSources(c, launch.CWD)
	if source == "" {
		return out, nil
	}
	if r.protocol == nil || !r.protocol.scopedToolStatus {
		out.Warnings = append(out.Warnings, "当前 Agent 不支持核对单个会话的 MCP 状态")
		return out, nil
	}
	type status struct {
		Name, RuntimeStatus, AuthStatus string
		Tools                           map[string]struct{ Name, Description string }
		ToolsError                      *string
	}
	observed := map[string]status{}
	var cursor *string
	seen := map[string]bool{}
	for {
		var response struct {
			Data []status
			Next *string `json:"nextCursor"`
		}
		if err := r.call(ctx, "mcpServerStatus/list", map[string]any{"threadId": source, "limit": 100, "cursor": cursor}, &response); err != nil {
			return out, errors.New("会话 MCP 状态未确认，请重试")
		}
		for _, s := range response.Data {
			observed[s.Name] = s
		}
		if response.Next == nil {
			break
		}
		if seen[*response.Next] {
			return out, errors.New("会话 MCP 状态分页未前进")
		}
		seen[*response.Next] = true
		cursor = response.Next
	}
	// Include dynamically supplied services without inventing a configuration source.
	for id := range observed {
		found := false
		for _, e := range out.Entries {
			if e.ID == id && e.IgnoredReason == "" {
				found = true
				break
			}
		}
		if !found {
			out.Entries = append(out.Entries, domain.InspectedToolService{ToolService: domain.ToolService{ID: id, Name: id}, EntryID: skillID("runtime\x00" + id), Scope: domain.ToolServiceOther})
		}
	}
	out.RuntimeKnown = true
	for i := range out.Entries {
		e := &out.Entries[i]
		if e.IgnoredReason != "" {
			continue
		}
		s, ok := observed[e.ID]
		if !ok {
			e.State = domain.ToolServiceNotLoaded
			continue
		}
		switch {
		case s.RuntimeStatus == "disabled":
			e.State = domain.ToolServiceDisabled
		case s.RuntimeStatus == "authenticationRequired" || s.AuthStatus == "notLoggedIn":
			e.State = domain.ToolServiceAuthorizationRequired
		case s.RuntimeStatus == "starting":
			e.State = domain.ToolServiceConnecting
		case s.RuntimeStatus == "failed" || s.ToolsError != nil:
			e.State = domain.ToolServiceFailed
		case s.RuntimeStatus == "connected" || len(s.Tools) > 0:
			e.State = domain.ToolServiceReady
		default:
			e.State = domain.ToolServiceUnknown
		}
		e.Tools = len(s.Tools)
		names := make([]string, 0, len(s.Tools))
		for name := range s.Tools {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if len(e.Definitions) == 100 {
				break
			}
			e.Definitions = append(e.Definitions, domain.ToolServiceDefinition{Name: boundedTo(name, 256), Description: boundedTo(s.Tools[name].Description, 512)})
		}
	}
	return out, nil
}

var _ harness.ToolServiceInspector = (*adapterRuntime)(nil)

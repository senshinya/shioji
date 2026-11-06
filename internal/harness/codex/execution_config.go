package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

func (r *Runtime) executionEnvironmentID() string { return "shioji-" + r.scope.Session }
func (r *Runtime) executionEnvironment(s domain.Session) []wire.ExecutionEnvironment {
	if r.executor == nil {
		return nil
	}
	return []wire.ExecutionEnvironment{{EnvironmentID: r.executionEnvironmentID(), CWD: s.Context.CWD, RuntimeWorkspaceRoots: append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)}}
}
func (r *Runtime) executionConfig(ctx context.Context, s domain.Session) (map[string]any, error) {
	if r.executor == nil {
		return nil, nil
	}
	if r.scope == nil || r.scope.Session != s.ID {
		return nil, errors.New("execution environment belongs to another session")
	}
	var result struct{ Config map[string]json.RawMessage }
	if err := r.call(ctx, "config/read", map[string]any{"cwd": s.Context.CWD, "includeLayers": false}, &result); err != nil {
		return nil, err
	}
	var servers map[string]map[string]json.RawMessage
	if raw := result.Config["mcp_servers"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, err
		}
	}
	coverage := true
	overrides := map[string]any{}
	for name, config := range servers {
		var command string
		json.Unmarshal(config["command"], &command)
		if command == "" {
			continue
		}
		var enabled *bool
		json.Unmarshal(config["enabled"], &enabled)
		if enabled != nil && !*enabled {
			continue
		}
		var environment string
		json.Unmarshal(config["environment_id"], &environment)
		if environment != "" && environment != "local" && environment != r.executionEnvironmentID() {
			coverage = false
			continue
		}
		server := map[string]any{"environment_id": r.executionEnvironmentID()}
		var cwd string
		json.Unmarshal(config["cwd"], &cwd)
		if cwd == "" {
			server["cwd"] = s.Context.CWD
		}
		overrides[name] = server
	}
	// Arbitrary local hooks/plugins may start processes outside this executor.
	// Keep their execution available, but do not certify unobserved cleanup.
	for _, key := range []string{"hooks", "plugins"} {
		raw := strings.TrimSpace(string(result.Config[key]))
		if raw != "" && raw != "null" && raw != "{}" && raw != "[]" {
			coverage = false
		}
	}
	r.managedCoverage = coverage
	if len(overrides) == 0 {
		return nil, nil
	}
	return map[string]any{"mcp_servers": overrides}, nil
}
func (r *Runtime) ManagedCoverage() bool { return r.executor != nil && r.managedCoverage }
func sourceExecutionPath(value string) string {
	if !strings.HasPrefix(value, "file:") {
		return value
	}
	u, err := url.Parse(value)
	if err != nil || (u.Host != "" && u.Host != "localhost") {
		return value
	}
	return u.Path
}

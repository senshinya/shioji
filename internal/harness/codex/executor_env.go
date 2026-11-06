package codex

import (
	"encoding/json"
	"errors"
	"strings"
)

// Mirrors the pinned executor's child_env for an in-memory ACK. Bootstrap gets
// a clean environment; the original policy is applied only to the target image.
func executorTargetEnv(base map[string]string, params map[string]json.RawMessage) ([]string, error) {
	env := map[string]string{}
	var overlay map[string]string
	if err := json.Unmarshal(params["env"], &overlay); err != nil {
		return nil, err
	}
	if raw := params["envPolicy"]; len(raw) > 0 && string(raw) != "null" {
		var policy struct {
			Inherit               string
			IgnoreDefaultExcludes bool
			Exclude               []string
			Set                   map[string]string
			IncludeOnly           []string
		}
		if err := json.Unmarshal(raw, &policy); err != nil {
			return nil, err
		}
		switch policy.Inherit {
		case "all":
			for k, v := range base {
				env[k] = v
			}
		case "core":
			for k, v := range base {
				if matchesEnv(k, []string{"PATH", "SHELL", "TMPDIR", "TEMP", "TMP", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "LOGNAME", "USER"}) {
					env[k] = v
				}
			}
		case "none":
		default:
			return nil, errors.New("unsupported executor environment policy")
		}
		for k := range env {
			if !policy.IgnoreDefaultExcludes && matchesEnv(k, []string{"*KEY*", "*SECRET*", "*TOKEN*"}) || matchesEnv(k, policy.Exclude) {
				delete(env, k)
			}
		}
		for k, v := range policy.Set {
			env[k] = v
		}
		if len(policy.IncludeOnly) > 0 {
			for k := range env {
				if !matchesEnv(k, policy.IncludeOnly) {
					delete(env, k)
				}
			}
		}
	}
	for k, v := range overlay {
		env[k] = v
	}
	for k := range env {
		if matchesEnv(k, []string{"CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN", "NODE_REPL_AUTH_TOKEN", "CODEX_GUARDIAN_DECISIONS_API_KEY", "OPENAI_FEDERATION_RULE_ID", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_WORKLOAD_IDENTITY_CONTEXT"}) || k == "CODEX_EXEC_SERVER_EXIT_ON_STDIN_CLOSE" {
			delete(env, k)
		}
	}
	if raw := params["metadata"]; len(raw) > 0 && string(raw) != "null" {
		var metadata struct {
			ThreadID   *string
			ToolCallID *string
		}
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return nil, err
		}
		delete(env, "CODEX_THREAD_ID")
		if metadata.ThreadID != nil {
			env["CODEX_THREAD_ID"] = *metadata.ThreadID
		}
		delete(env, "CODEX_TOOL_CALL_ID")
		if metadata.ToolCallID != nil && !strings.ContainsRune(*metadata.ToolCallID, 0) {
			env["CODEX_TOOL_CALL_ID"] = *metadata.ToolCallID
		}
	} else {
		delete(env, "CODEX_TOOL_CALL_ID")
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out, nil
}
func matchesEnv(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if wildcard(strings.ToUpper(name), strings.ToUpper(pattern)) {
			return true
		}
	}
	return false
}
func wildcard(value, pattern string) bool {
	// The pinned EnvironmentVariablePattern supports * and ?; other bytes are literal.
	vi, pi, star, back := 0, 0, -1, 0
	for vi < len(value) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == value[vi]) {
			vi++
			pi++
			continue
		}
		if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			pi++
			back = vi
			continue
		}
		if star >= 0 {
			back++
			vi = back
			pi = star + 1
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

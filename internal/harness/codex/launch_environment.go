package codex

import (
	"context"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/systemexec"
)

const shellEnvironmentTimeout = systemexec.ShellEnvironmentTimeout

func codexEnvironment(ctx context.Context, home string, entries []domain.EnvironmentVariable) ([]string, error) {
	env, err := systemexec.Environment(ctx, codexManagedEnvironment)
	return runtimeEnvironment(env, home, entries), err
}

func codexManagedEnvironment(entry string) bool {
	name, _, _ := strings.Cut(entry, "=")
	return name == "CODEX_MCP_NODE_PATH" || strings.HasPrefix(name, "CODEX_THREAD_") || strings.HasPrefix(name, "CODEX_TOOL_") || strings.HasPrefix(name, "CODEX_APP_TOOLS_")
}

func lookPathEnvironment(name string, env []string) (string, error) {
	return systemexec.LookPath(name, env)
}

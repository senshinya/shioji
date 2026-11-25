package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (a Adapter) ProfileModes() []domain.AgentProfileMode {
	if a.profileMode != "" {
		return nil
	}
	if a.apiPreset != nil || a.responses {
		return []domain.AgentProfileMode{domain.AgentProfileAPI}
	}
	return []domain.AgentProfileMode{domain.AgentProfileAccount, domain.AgentProfileAPI}
}

func (a Adapter) WithAgentProfile(id, name, directory string, mode domain.AgentProfileMode) (harness.Adapter, error) {
	if !slices.Contains(a.ProfileModes(), mode) {
		return nil, harness.Unsupported("agent-profile")
	}
	return Adapter{Executable: a.Executable, Home: directory, profileID: id, profileName: name, profileMode: mode, apiPreset: a.apiPreset, responses: a.responses, managedState: a.managedState}, nil
}

func (a Adapter) ensureProfileConfig() error {
	// Native revision-checked API setup needs an explicit user layer.
	path := filepath.Join(a.Home, "config.toml")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(path)
		if statErr == nil && info.Mode().IsRegular() {
			return nil
		}
		return errors.New("Profile 配置路径不可用")
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func (a Adapter) launchArguments() []string {
	if a.profileMode == "" {
		return a.runtimeOptions.Arguments
	}
	args := []string{"-c", `cli_auth_credentials_store="file"`}
	if a.profileMode == domain.AgentProfileAccount {
		args = append(args, "-c", `forced_login_method="chatgpt"`, "-c", `model_provider="openai"`)
	}
	return args
}

func (a Adapter) accountAlreadyBound() bool {
	_, err := os.Lstat(filepath.Join(a.Home, "auth.json"))
	return !errors.Is(err, os.ErrNotExist)
}

func stripProfileEnvironment(env []string) []string {
	return slices.DeleteFunc(env, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		for _, prefix := range []string{"OPENAI_", "CODEX_", "CHATGPT_", "AZURE_OPENAI_"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return name == "MODEL_PROVIDER"
	})
}

func (a Adapter) startAuthentication(ctx context.Context, authentication bool) (*Runtime, error) {
	env, _ := a.environment(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return Start(ctx, Options{launchEnv: env, apiPreset: a.apiPreset, Executable: a.Executable, Home: a.Home, Arguments: a.launchArguments(), authentication: authentication})
}

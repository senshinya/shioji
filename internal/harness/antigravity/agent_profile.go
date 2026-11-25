package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

func (a Adapter) ProfileModes() []domain.AgentProfileMode {
	if a.profileID != "" {
		return nil
	}
	return []domain.AgentProfileMode{domain.AgentProfileAccount}
}

func (a Adapter) WithAgentProfile(id, name, directory string, mode domain.AgentProfileMode) (harness.Adapter, error) {
	if !slices.Contains(a.ProfileModes(), mode) {
		return nil, harness.Unsupported("agent-profile")
	}
	return Adapter{Executable: a.Executable, profileID: id, profileName: name, profileDirectory: directory}, nil
}

func (a Adapter) profileTokenPath() string {
	return filepath.Join(a.profileDirectory, "antigravity-cli", "acp_token.json")
}

func (a Adapter) accountAlreadyBound() bool {
	_, err := os.Lstat(a.profileTokenPath())
	return !errors.Is(err, os.ErrNotExist)
}

func (a Adapter) profileAuthReady() bool {
	info, err := os.Lstat(a.profileTokenPath())
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func (a Adapter) environment(ctx context.Context) ([]string, error) {
	if a.profileID == "" {
		return systemexec.Environment(ctx, nil)
	}
	env, err := systemexec.Environment(ctx, nil)
	env = slices.DeleteFunc(env, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GEMINI_HOME", "SSH_CONNECTION", "GCLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_API_KEY":
			return true
		}
		for _, prefix := range []string{"GEMINI_", "GOOGLE_CLOUD_", "GOOGLE_GENAI_", "GOOGLE_GEMINI_", "AGY_ACP_"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	})
	// Current agy can otherwise reuse one macOS keyring entry across homes.
	return append(env, "GEMINI_HOME="+a.profileDirectory, "AGY_ACP_FORCE_FILE_STORAGE=1", "SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0"), err
}

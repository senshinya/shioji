package claudecode

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"shioji/internal/domain"
)

//go:embed model-sdk.mjs
var modelSDK string

type nativeModel struct {
	Value, ResolvedModel             string
	SupportsFastMode, SupportsEffort bool
	SupportedEffortLevels            []string
}

func officialEndpoint(value string) bool {
	endpoint, err := url.Parse(value)
	return err == nil && endpoint.Scheme == "https" && strings.EqualFold(endpoint.Hostname(), "api.anthropic.com") &&
		(endpoint.Port() == "" || endpoint.Port() == "443") &&
		(endpoint.Path == "" || endpoint.Path == "/" || endpoint.Path == "/v1") &&
		endpoint.User == nil && endpoint.RawQuery == "" && endpoint.Fragment == ""
}

func mergeModelOptions(model domain.Model, options []nativeModel) domain.Model {
	at := slices.IndexFunc(options, func(native nativeModel) bool { return model.ID == native.Value })
	if at < 0 {
		at = slices.IndexFunc(options, func(native nativeModel) bool { return model.ID == native.ResolvedModel })
	}
	if at < 0 {
		return model
	}
	native := options[at]
	if native.SupportsEffort {
		for _, effort := range native.SupportedEffortLevels {
			if slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, effort) && !slices.Contains(model.Efforts, effort) {
				model.Efforts = append(model.Efforts, effort)
			}
		}
	}
	model.Fast = native.SupportsFastMode
	return model
}

func modelOptions(ctx context.Context, node, sdk, binary string) []nativeModel {
	dir, err := os.MkdirTemp("", "shioji-claude-models-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", modelSDK, "--", binary, sdk)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, "profile"),
		"ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ANTHROPIC_API_KEY=shioji-model-catalog",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.Output()
	var values []nativeModel
	if err != nil || json.Unmarshal(out, &values) != nil {
		return nil
	}
	return values
}

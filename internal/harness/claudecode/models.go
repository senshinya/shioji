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
	"shioji/internal/harness/modelapi"
)

//go:embed model-sdk.mjs
var modelSDK string

const (
	permissionReadOnly    = "read-only"
	permissionDefault     = "default"
	permissionAutoApprove = "auto-approve"
	permissionYOLO        = "yolo"

	capabilitiesCacheFile    = "capabilities.json"
	capabilitiesCacheVersion = 2
)

var allowedEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

type nativeModel struct {
	Value, ResolvedModel             string
	SupportsFastMode, SupportsEffort bool
	SupportedEffortLevels            []string
}

type sdkPermissionCapabilities struct {
	ReadOnly          bool `json:"readOnly"`
	Default           bool `json:"default"`
	AutoApprove       bool `json:"autoApprove"`
	AcceptEdits       bool `json:"acceptEdits"`
	YOLO              bool `json:"yolo"`
	BypassPermissions bool `json:"bypassPermissions"`
	BypassRestricted  bool `json:"bypassRestricted"`
}

type sdkCapabilities struct {
	Probed      bool                      `json:"probed"`
	Models      []nativeModel             `json:"models,omitempty"`
	Permissions sdkPermissionCapabilities `json:"permissions"`
}

type cachedCapabilities struct {
	Schema         int                            `json:"schema"`
	Endpoint       string                         `json:"endpoint"`
	Model          string                         `json:"model"`
	RuntimeVersion string                         `json:"runtimeVersion"`
	API            modelapi.AnthropicCapabilities `json:"api"`
	SDK            sdkCapabilities                `json:"sdk"`
	ProbedAt       time.Time                      `json:"probedAt"`
}

func normalizePermission(value string) string {
	if value == "" {
		return permissionDefault
	}
	return value
}

func permissionChoices(caps sdkPermissionCapabilities) ([]string, []domain.OptionChoice) {
	var permissions []string
	var choices []domain.OptionChoice
	if caps.ReadOnly {
		permissions = append(permissions, permissionReadOnly)
		choices = append(choices, domain.OptionChoice{ID: permissionReadOnly, Name: "只读", Description: "仅查看"})
	}
	permissions = append(permissions, permissionDefault)
	choices = append(choices, domain.OptionChoice{ID: permissionDefault, Name: "默认权限", Description: "按需请求批准"})
	if caps.AutoApprove {
		permissions = append(permissions, permissionAutoApprove)
		choices = append(choices, domain.OptionChoice{ID: permissionAutoApprove, Name: "自动批准", Description: "沙盒内自动批准"})
	}
	if caps.YOLO && caps.BypassPermissions {
		permissions = append(permissions, permissionYOLO)
		choices = append(choices, domain.OptionChoice{ID: permissionYOLO, Name: "YOLO", Description: "不限制沙盒或批准"})
	}
	return permissions, choices
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
		model = model.WithEfforts(claudeEffortLevels(native.SupportedEffortLevels))
	}
	model.Fast = native.SupportsFastMode
	return model
}

func claudeEffortLevels(levels []string) []string {
	return slices.DeleteFunc(modelapi.NormalizeEfforts(levels), func(level string) bool {
		return !slices.Contains(allowedEffortLevels, level)
	})
}

func probeSDKCapabilities(ctx context.Context, node, sdk, binary, model string) sdkCapabilities {
	if node == "" || sdk == "" {
		return sdkCapabilities{}
	}
	dir, err := os.MkdirTemp("", "shioji-claude-models-")
	if err != nil {
		return sdkCapabilities{}
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", modelSDK, "--", binary, sdk, model)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, "profile"),
		"ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ANTHROPIC_API_KEY=shioji-model-catalog",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.Output()
	if err != nil {
		return sdkCapabilities{}
	}
	var probed sdkCapabilities
	if json.Unmarshal(out, &probed) == nil && probed.Probed {
		return probed
	}
	var legacy []nativeModel
	if json.Unmarshal(out, &legacy) == nil {
		return sdkCapabilities{Models: legacy}
	}
	return sdkCapabilities{}
}

func loadCapabilitiesCache(home, endpoint, model string) (cachedCapabilities, bool) {
	var cache cachedCapabilities
	if home == "" {
		return cache, false
	}
	p := filepath.Join(home, capabilitiesCacheFile)
	stat, err := os.Lstat(p)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > 64<<10 {
		return cache, false
	}
	raw, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(raw, &cache) != nil {
		return cache, false
	}
	if cache.Schema != capabilitiesCacheVersion || cache.Endpoint != endpoint || cache.Model != model {
		return cachedCapabilities{}, false
	}
	return cache, true
}

func saveCapabilitiesCache(home string, cache cachedCapabilities) {
	if home == "" {
		return
	}
	cache.Schema = capabilitiesCacheVersion
	cache.ProbedAt = time.Now().UTC()
	if raw, err := json.Marshal(cache); err == nil {
		_ = savePrivate(filepath.Join(home, capabilitiesCacheFile), raw)
	}
}

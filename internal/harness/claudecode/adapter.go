// Package claudecode runs an isolated Claude Code API profile on the owning device.
package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/managedagent"
	"shioji/internal/harness/modelapi"
	"shioji/internal/systemexec"
)

type Adapter struct {
	State, Executable string
	id, name, home    string
}

func (a Adapter) Descriptor() domain.HarnessDescriptor {
	if a.id != "" {
		return domain.HarnessDescriptor{ID: a.id, Name: a.name, Icon: "claude"}
	}
	return domain.HarnessDescriptor{ID: domain.AgentAnthropic, Name: "Anthropic API", Icon: "claude", ProfileOnly: true}
}
func (a Adapter) ProfileModes() []domain.AgentProfileMode {
	if a.id == "" {
		return []domain.AgentProfileMode{domain.AgentProfileAPI}
	}
	return nil
}
func (a Adapter) WithAgentProfile(id, name, home string, mode domain.AgentProfileMode) (harness.Adapter, error) {
	if !slices.Contains(a.ProfileModes(), mode) {
		return nil, harness.Unsupported("agent-profile")
	}
	a.id, a.name, a.home = id, name, home
	return a, nil
}
func (a Adapter) WithExecutable(path string) (harness.Adapter, error) {
	if a.id != "" && path != "" {
		return nil, harness.Unsupported("profile-executable")
	}
	a.Executable = path
	return a, nil
}
func (a Adapter) Installation() domain.RuntimeInstallation {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	binary, _, err := a.executable(ctx, false)
	i := domain.RuntimeInstallation{APISetup: true, Executable: binary, Documentation: "https://code.claude.com/docs/en/setup"}
	if err != nil {
		i.Issue = domain.RuntimeMissing
	}
	return i
}

func (a Adapter) executable(ctx context.Context, install bool, updates ...func(int64, int64)) (string, []string, error) {
	env, _ := systemexec.Environment(ctx, nil)
	name := a.Executable
	if name == "" {
		name = "claude"
	}
	candidates := []string{name}
	if a.Executable == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".local/bin/claude"))
		}
		candidates = append(candidates, "/opt/homebrew/bin/claude")
	}
	for _, candidate := range candidates {
		path, err := systemexec.LookPath(candidate, env)
		if err == nil && compatible(ctx, path, env) {
			return path, env, nil
		}
	}
	if path := managedagent.Claude.Find(a.State); path != "" {
		return path, env, nil
	}
	if install {
		path, err := managedagent.Claude.Ensure(ctx, a.State, updates...)
		return path, env, err
	}
	return "", nil, &harness.SetupError{Issue: domain.RuntimeMissing, Message: "需要安装兼容的 Claude Code 运行环境"}
}

func compatible(ctx context.Context, binary string, env []string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--help")
	cmd.Env = env
	out, err := cmd.Output()
	return err == nil && strings.Contains(string(out), "--restricted") && strings.Contains(string(out), "--permission-prompts") && strings.Contains(string(out), "--replay-user-messages")
}

func (a Adapter) APIModels(ctx context.Context, in domain.APIConfiguration) ([]domain.Model, error) {
	return modelapi.Models(ctx, domain.AgentAnthropic, in)
}

type configuration struct {
	Endpoint string `json:"baseUrl"`
	Model    string `json:"model"`
	Key      string `json:"apiKey"`
}

func (a Adapter) config() (configuration, error) {
	var out configuration
	p := filepath.Join(a.home, "settings-api.json")
	stat, err := os.Lstat(p)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > 32<<10 {
		return out, setupError("请连接此 Profile 的 Anthropic API")
	}
	raw, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Model == "" || out.Key == "" {
		return out, setupError("无法读取此 Profile 的 API 配置")
	}
	if _, err := modelapi.Normalize(out.input()); err != nil {
		return out, setupError("此 Profile 的 API 配置无效")
	}
	return out, nil
}
func (c configuration) input() domain.APIConfiguration {
	return domain.APIConfiguration{Endpoint: c.Endpoint, Model: c.Model, Credential: c.Key}
}
func setupError(message string) error {
	return &harness.SetupError{Issue: domain.RuntimeSignInRequired, Message: message}
}
func (a Adapter) ConfigureAPI(ctx context.Context, in domain.APIConfiguration) (domain.Readiness, error) {
	return a.ConfigureAPIWithProgress(ctx, in, nil)
}
func (a Adapter) ConfigureAPIWithProgress(ctx context.Context, in domain.APIConfiguration, progress func(domain.RuntimeSetupProgress)) (domain.Readiness, error) {
	if a.id == "" {
		return domain.Readiness{}, harness.Unsupported("api-configuration-without-profile")
	}
	in, err := modelapi.Normalize(in)
	if err != nil {
		return domain.Readiness{}, setupError(err.Error())
	}
	if in.Model == "" || in.Credential == "" {
		return domain.Readiness{}, setupError("请输入模型 ID 和 API Key")
	}
	if _, _, err = a.executable(ctx, in.InstallRuntime, func(done, total int64) {
		if progress != nil {
			progress(domain.RuntimeSetupProgress{Phase: "downloading", Downloaded: done, Total: total})
		}
	}); err != nil {
		return domain.Readiness{}, setupError(err.Error())
	}
	if progress != nil {
		progress(domain.RuntimeSetupProgress{Phase: "verifying"})
	}
	if err = modelapi.Verify(ctx, domain.AgentAnthropic, in); err != nil {
		return domain.Readiness{}, setupError(err.Error())
	}
	if ctx.Err() != nil {
		return domain.Readiness{}, ctx.Err()
	}
	config := configuration{Endpoint: in.Endpoint, Model: in.Model, Key: in.Credential}
	temp, err := os.CreateTemp(a.home, ".api-config-*")
	if err != nil {
		return domain.Readiness{}, setupError("无法保存设备上的 API 配置")
	}
	defer os.Remove(temp.Name())
	err = errors.Join(json.NewEncoder(temp).Encode(config), temp.Close())
	if err != nil || os.Rename(temp.Name(), filepath.Join(a.home, "settings-api.json")) != nil {
		return domain.Readiness{}, setupError("无法保存设备上的 API 配置")
	}
	return a.readiness(config), nil
}

func (a Adapter) readiness(config configuration) domain.Readiness {
	return domain.Readiness{
		HarnessID: a.id, State: domain.RuntimeReady, Models: []domain.Model{modelapi.Model(config.input())},
		Account:           &domain.RuntimeAccount{State: "configured", Mode: "api", Label: "Anthropic API", Endpoint: config.Endpoint, Model: config.Model, Credential: "configured"},
		Permissions:       []string{"default"},
		PermissionChoices: []domain.OptionChoice{{ID: "default", Name: "按需批准"}},
		Capabilities:      domain.HarnessCapabilities{Interrupt: true, Resume: true},
	}
}

func (a Adapter) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	if a.id == "" {
		return nil, harness.Unsupported("execution-without-profile")
	}
	bin, env, err := a.executable(ctx, false)
	if err != nil {
		return nil, err
	}
	config, err := a.config()
	if err != nil {
		return nil, err
	}
	return &runtime{ready: a.readiness(config), config: config, opts: opts, binary: bin, env: env, home: a.home, done: make(chan struct{}), approvals: map[string]approval{}}, nil
}

// Claude receives a per-process loopback capability. The user's API Key stays
// in the device adapter, outside Claude's tool environment and transcript.
func (c configuration) connect() (string, string, func(), error) {
	target, err := url.Parse(c.Endpoint)
	if err != nil {
		return "", "", nil, err
	}
	target.Path = strings.TrimSuffix(target.Path, "/v1")
	target.RawPath = ""
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	token := domain.NewID() + domain.NewID()
	proxy := &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
		req.SetURL(target)
		req.Out.Header.Del("Authorization")
		req.Out.Header.Set("x-api-key", c.Key)
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Model service unavailable", http.StatusBadGateway)
	}}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != token && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		valid := r.Method == http.MethodPost && (r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens") ||
			r.Method == http.MethodGet && r.URL.Path == "/v1/models"
		if !valid {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	go server.Serve(ln)
	return "http://" + ln.Addr().String(), token, func() { _ = server.Close() }, nil
}

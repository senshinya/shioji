// Package claudecode runs an isolated Claude Code API profile on the owning device.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	binary, _, err := a.executable(ctx)
	i := domain.RuntimeInstallation{APISetup: true, Executable: binary, Documentation: "https://code.claude.com/docs/en/setup"}
	if err != nil || managedagent.ClaudeNode.Find(a.State) == "" || managedagent.ClaudeSDK.Find(a.State) == "" {
		i.Issue = domain.RuntimeMissing
		if a.id != "" {
			_, configErr := a.config()
			i.CanRepair = configErr == nil
		}
	}
	return i
}

func (a Adapter) executable(ctx context.Context) (string, []string, error) {
	env, _ := systemexec.Environment(ctx, nil)
	for _, path := range a.localExecutables(env) {
		if compatible(ctx, path, env) {
			return path, env, nil
		}
	}
	if path := managedagent.Claude.Find(a.State); path != "" {
		return path, env, nil
	}
	return "", nil, &harness.SetupError{Issue: domain.RuntimeMissing, Message: "需要安装兼容的 Claude Code 运行环境"}
}

func (a Adapter) localExecutables(env []string) []string {
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
	var paths []string
	for _, candidate := range candidates {
		if path, err := systemexec.LookPath(candidate, env); err == nil && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths
}

func (a Adapter) CatalogVersion() (string, error) {
	if a.home == "" {
		return "", nil
	}
	policy, err := profileVersion(a.home)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	env, _ := systemexec.Environment(ctx, nil)
	return a.runtimeVersion(policy, env), nil
}

func (a Adapter) runtimeVersion(policy string, env []string) string {
	inputs := []string{policy}
	paths := append(a.localExecutables(env), managedagent.Claude.Find(a.State),
		managedagent.ClaudeNode.Find(a.State), managedagent.ClaudeSDK.Find(a.State))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			inputs = append(inputs, path)
			continue
		}
		var identity uint64
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			identity = stat.Ino
		}
		inputs = append(inputs, fmt.Sprintf("%s:%d:%d:%d", path, info.Size(), info.ModTime().UnixNano(), identity))
	}
	return digestText(inputs...)
}

func (a Adapter) ensureToolchain(ctx context.Context, progress func(domain.RuntimeSetupProgress)) error {
	_, _, missing := a.executable(ctx)
	err := managedagent.EnsureClaudeToolchain(ctx, a.State, missing != nil, func(done, total int64) {
		if progress != nil {
			progress(domain.RuntimeSetupProgress{Phase: "downloading", Downloaded: done, Total: total})
		}
	})
	if err != nil {
		return setupError(err.Error())
	}
	return nil
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
	if in.InstallRuntime {
		if err := a.ensureToolchain(ctx, progress); err != nil {
			return domain.Readiness{}, err
		}
	}
	bin, env, err := a.executable(ctx)
	if err != nil {
		return domain.Readiness{}, err
	}
	node, sdk := managedagent.ClaudeNode.Find(a.State), managedagent.ClaudeSDK.Find(a.State)
	if node == "" || sdk == "" {
		return domain.Readiness{}, missingSDK()
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
	raw, err := json.Marshal(config)
	if err != nil || savePrivate(filepath.Join(a.home, "settings-api.json"), raw) != nil {
		return domain.Readiness{}, setupError("无法保存设备上的 API 配置")
	}
	version, _ := profileVersion(a.home)
	apiCaps, sdkCaps := a.resolveCapabilities(ctx, config, node, sdk, bin, a.runtimeVersion(version, env), true, true)
	return applyCapabilities(a.readiness(config), config, apiCaps, sdkCaps), nil
}

func missingSDK() error {
	return &harness.SetupError{Issue: domain.RuntimeMissing, Message: "需要在所属设备上安装 Claude Agent SDK"}
}

func (a Adapter) RepairAPIRuntime(ctx context.Context, progress func(domain.RuntimeSetupProgress)) (domain.Readiness, error) {
	config, err := a.config()
	if err != nil {
		return domain.Readiness{}, err
	}
	if err := a.ensureToolchain(ctx, progress); err != nil {
		return domain.Readiness{}, err
	}
	bin, env, err := a.executable(ctx)
	if err != nil {
		return domain.Readiness{}, err
	}
	node, sdk := managedagent.ClaudeNode.Find(a.State), managedagent.ClaudeSDK.Find(a.State)
	version, _ := profileVersion(a.home)
	apiCaps, sdkCaps := a.resolveCapabilities(ctx, config, node, sdk, bin, a.runtimeVersion(version, env), true, false)
	return applyCapabilities(a.readiness(config), config, apiCaps, sdkCaps), nil
}

func (a Adapter) readiness(config configuration) domain.Readiness {
	permissions, choices := permissionChoices(sdkPermissionCapabilities{})
	return domain.Readiness{
		HarnessID: a.id, State: domain.RuntimeReady, Models: []domain.Model{modelapi.Model(config.input())}, Plan: true,
		Account:           &domain.RuntimeAccount{State: "configured", Mode: "api", Label: "Anthropic API", Endpoint: config.Endpoint, Model: config.Model, Credential: "configured"},
		Permissions:       permissions,
		PermissionChoices: choices,
		Capabilities: domain.HarnessCapabilities{Steer: true, Interrupt: true, Recovery: true, Resume: true, Continue: true,
			Fork: true, Restart: true, CompactNeedsCheck: true,
			ToolServices: true, ToolServiceInspection: true, Skills: true, SkillManagement: true, Goals: true, WorkerGoals: true,
			PathAttachments: true, HookManagement: true, HookInspection: true},
	}
}

func applyCapabilities(ready domain.Readiness, config configuration, apiCaps modelapi.AnthropicCapabilities, sdkCaps sdkCapabilities) domain.Readiness {
	ready.Permissions, ready.PermissionChoices = permissionChoices(sdkCaps.Permissions)
	if len(ready.Models) > 0 {
		model := mergeModelOptions(ready.Models[0], sdkCaps.Models)
		model.Fast = apiCaps.Fast || !apiCaps.Probed && officialEndpoint(config.Endpoint) && model.Fast
		if apiCaps.Efforts != nil {
			if apiCaps.DefaultEffort != "" {
				model.DefaultEffort = apiCaps.DefaultEffort
			}
			model = model.WithEfforts(claudeEffortLevels(apiCaps.Efforts))
		}
		ready.Models[0] = model
	}
	return ready
}

func (a Adapter) resolveCapabilities(ctx context.Context, config configuration, node, sdk, bin, runtimeVer string, probeAPI, forceRefresh bool) (modelapi.AnthropicCapabilities, sdkCapabilities) {
	cache, hasCache := loadCapabilitiesCache(a.home, config.Endpoint, config.Model)
	apiCaps, sdkCaps := cache.API, cache.SDK
	needSDK := forceRefresh || !hasCache || !sdkCaps.Probed || cache.RuntimeVersion != runtimeVer
	needAPI := probeAPI && (forceRefresh || !hasCache || !apiCaps.Probed)
	if !needSDK && !needAPI {
		return apiCaps, sdkCaps
	}
	var wg sync.WaitGroup
	if needSDK {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sdkCaps = probeSDKCapabilities(ctx, node, sdk, bin, config.Model)
		}()
	}
	if needAPI {
		wg.Add(1)
		go func() {
			defer wg.Done()
			apiCaps = modelapi.ProbeAnthropic(ctx, config.input())
		}()
	}
	wg.Wait()
	if sdkCaps.Probed || apiCaps.Probed || apiCaps.Efforts != nil {
		saveCapabilitiesCache(a.home, cachedCapabilities{
			Endpoint:       config.Endpoint,
			Model:          config.Model,
			RuntimeVersion: runtimeVer,
			API:            apiCaps,
			SDK:            sdkCaps,
		})
	}
	return apiCaps, sdkCaps
}

func (a Adapter) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	if a.id == "" {
		return nil, harness.Unsupported("execution-without-profile")
	}
	bin, env, err := a.executable(ctx)
	if err != nil {
		return nil, err
	}
	config, err := a.config()
	if err != nil {
		return nil, err
	}
	node, sdk := managedagent.ClaudeNode.Find(a.State), managedagent.ClaudeSDK.Find(a.State)
	if node == "" || sdk == "" {
		return nil, missingSDK()
	}
	version, err := profileVersion(a.home)
	if err != nil {
		return nil, err
	}
	userHome, _ := os.UserHomeDir()
	privateProfiles := a.home
	if root := filepath.Join(a.State, "agent-profiles"); filepath.IsAbs(root) && filepath.Dir(a.home) == root {
		privateProfiles = root
	}
	catalogVersion := a.runtimeVersion(version, env)
	apiCaps, sdkCaps := a.resolveCapabilities(ctx, config, node, sdk, bin, catalogVersion, opts.CatalogOnly, opts.RefreshCatalog)
	ready := applyCapabilities(a.readiness(config), config, apiCaps, sdkCaps)
	return &runtime{ready: ready, config: config, sdkCaps: sdkCaps, opts: opts, binary: bin, node: node, sdk: sdk, env: env,
		home: a.home, privateProfiles: privateProfiles, version: version, catalogVersion: catalogVersion,
		externalHome: filepath.Join(userHome, ".claude"), done: make(chan struct{}), approvals: map[string]approval{}}, nil
}

func (r *runtime) CatalogVersion() (string, error) { return r.catalogVersion, nil }

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

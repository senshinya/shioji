package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"slices"
	"strings"
	"time"
	"unicode"
)

func (a Adapter) Installation() domain.RuntimeInstallation {
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvironmentTimeout)
	defer cancel()
	env, _ := a.environment(ctx)
	path := a.Executable
	if image, err := installedExecutable(path, env); err == nil {
		path = image.path
	}
	command := []string{path}
	command = append(command, a.runtimeOptions.Arguments...)
	command = append(command, "login", "--device-auth")
	if a.Home != "" {
		command = append([]string{"/usr/bin/env", "CODEX_HOME=" + a.Home}, command...)
	}
	return domain.RuntimeInstallation{APISetup: a.apiSetupSupported(), SignInMethods: a.signInMethods(), Options: a.runtimeOptions, OptionsSupported: domain.RuntimeOptionSupport{Arguments: true, Directory: true, Environment: true, Instructions: true, TitleModel: true}, Executable: path, Override: a.override, Configurable: true, Guide: "在该设备的终端安装或登录，然后重新检测", Documentation: "https://learn.chatgpt.com/docs/codex/cli", SignInCommand: command}
}
func (a Adapter) WithExecutable(path string) (harness.Adapter, error) {
	if a.defaultExecutable == "" {
		a.defaultExecutable = a.Executable
		if a.defaultExecutable == "" {
			a.defaultExecutable = "codex"
		}
	}
	a.launchEnv = nil
	a.override = path
	a.Executable = path
	if path == "" {
		a.Executable = a.defaultExecutable
	}
	return a, nil
}

type accountStatus struct {
	Account            json.RawMessage `json:"account"`
	RequiresOpenAIAuth bool            `json:"requiresOpenaiAuth"`
}

var errAccountRoutingUnavailable = errors.New("Codex 账号工作区路由暂不可用")

func (a accountStatus) issue() domain.RuntimeIssue {
	if a.RequiresOpenAIAuth && (len(a.Account) == 0 || bytes.Equal(bytes.TrimSpace(a.Account), []byte("null"))) {
		return domain.RuntimeSignInRequired
	}
	return ""
}
func (r *Runtime) checkAuthentication(ctx context.Context) error {
	if !r.supports("account/read") {
		return nil
	}
	var account accountStatus
	if err := r.call(ctx, "account/read", struct {
		Refresh bool `json:"refreshToken"`
	}{false}, &account); err != nil {
		if errors.Is(err, errAccountRoutingUnavailable) {
			// Routing discovery can fail with a saved login. Leave the account
			// unconfirmed and let Codex enforce authentication on native requests.
			return nil
		}
		return err
	}
	r.account = account.summary()
	if r.supports("config/read") {
		config, err := r.systemConfiguration(ctx)
		if err != nil {
			return err
		}
		r.account = config.summary(account, r.processEnvironment())
		if r.account.Credential == "missing" {
			return &harness.SetupError{Issue: domain.RuntimeSignInRequired, Account: r.account, Message: "请检查该设备的 API 凭据"}
		}
	}
	if r.account != nil && r.account.Mode == "api" {
		return nil
	}
	if issue := account.issue(); issue != "" {
		return &harness.SetupError{Issue: issue, Account: r.account, Message: "请先在该设备登录 Codex，再重新检测"}
	}
	return nil
}

func authenticationIssue(code int, message string) domain.RuntimeIssue {
	lower := strings.ToLower(message)
	if code == 401 || strings.Contains(lower, "refresh_token_reused") || strings.Contains(lower, "refresh_token_expired") || strings.Contains(lower, "401 unauthorized") {
		return domain.RuntimeAuthenticationExpired
	}
	return ""
}

func (a Adapter) WithRuntimeOptions(options domain.RuntimeOptions) (harness.Adapter, error) {
	if len(options.Arguments) > 128 || len(options.Environment) > 128 || len(options.Instructions) > 64<<10 {
		return nil, errors.New("运行配置过大")
	}
	for i := 0; i < len(options.Arguments); i++ {
		arg := options.Arguments[i]
		flag := strings.SplitN(arg, "=", 2)[0]
		if !slices.Contains([]string{"-c", "--config", "-p", "--profile", "--enable", "--disable"}, flag) {
			return nil, errors.New("启动参数只支持配置、配置集和功能开关")
		}
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("启动参数包含无效字符")
		}
		if !strings.Contains(arg, "=") {
			i++
			if i >= len(options.Arguments) || strings.ContainsRune(options.Arguments[i], 0) {
				return nil, errors.New("启动参数缺少值")
			}
		}
	}
	seen := map[string]bool{}
	for _, entry := range options.Environment {
		if entry.Name == "" || strings.ContainsAny(entry.Name, "=\x00") || strings.ContainsRune(entry.Value, 0) || seen[entry.Name] {
			return nil, errors.New("环境变量名称无效或重复")
		}
		for i, r := range entry.Name {
			if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
				return nil, errors.New("环境变量名称无效")
			}
		}
		if entry.Name == "CODEX_HOME" || strings.HasPrefix(entry.Name, "CODEX_THREAD_") || strings.HasPrefix(entry.Name, "CODEX_TOOL_") || strings.HasPrefix(entry.Name, "SHIOJI_") || strings.HasPrefix(entry.Name, "HERDR_") {
			return nil, errors.New("该环境变量由执行服务管理")
		}
		seen[entry.Name] = true
	}
	if !a.hasDefaultHome {
		a.defaultHome = a.Home
		a.hasDefaultHome = true
	}
	directory := strings.TrimSpace(options.Directory)
	if directory == "~" || strings.HasPrefix(directory, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		directory = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(directory, "~"), "/"))
	}
	if directory != "" {
		if !filepath.IsAbs(directory) || strings.ContainsRune(directory, 0) {
			return nil, errors.New("请输入所属设备上的绝对配置目录")
		}
		st, err := os.Stat(directory)
		if err != nil || !st.IsDir() {
			return nil, errors.New("所属设备上不存在该配置目录")
		}
	}
	a.Home = a.defaultHome
	if directory != "" {
		a.Home = directory
	}
	options.Arguments = slices.Clone(options.Arguments)
	options.Environment = slices.Clone(options.Environment)
	a.launchEnv = nil
	a.runtimeOptions = options
	return a, nil
}
func runtimeEnvironment(base []string, home string, entries []domain.EnvironmentVariable) []string {
	env := slices.Clone(base)
	for _, entry := range entries {
		env = slices.DeleteFunc(env, func(value string) bool { return strings.HasPrefix(value, entry.Name+"=") })
		env = append(env, entry.Name+"="+entry.Value)
	}
	if home != "" {
		env = slices.DeleteFunc(env, func(value string) bool { return strings.HasPrefix(value, "CODEX_HOME=") })
		env = append(env, "CODEX_HOME="+home)
	}
	return env
}

func (a accountStatus) summary() *domain.RuntimeAccount {
	var public struct {
		Type  string `json:"type"`
		Email string `json:"email"`
		Plan  string `json:"planType"`
	}
	if json.Unmarshal(a.Account, &public) != nil {
		return nil
	}
	if public.Type == "" {
		if !a.RequiresOpenAIAuth {
			return &domain.RuntimeAccount{State: "notRequired", Label: "无需登录"}
		}
		return &domain.RuntimeAccount{State: "required", Label: "未登录"}
	}
	label, subscription := "已登录", ""
	switch public.Type {
	case "apiKey":
		label = "API Key"
	case "chatgpt":
		label = "ChatGPT"
		subscription = map[string]string{"free": "Free", "go": "Go", "plus": "Plus", "pro": "Pro", "prolite": "Pro Lite", "team": "Team", "business": "Business", "enterprise": "Enterprise", "edu": "Edu"}[public.Plan]
		if subscription == "" {
			subscription = public.Plan
		}
	}
	return &domain.RuntimeAccount{State: "authenticated", Label: label, Subscription: subscription, Identity: public.Email}

}

func (a Adapter) signInMethods() []domain.SignInMethod {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	env, _ := a.environment(ctx)
	_, support, err := compatibleExecutable(ctx, a.Executable, env)
	if err != nil {
		return nil
	}
	var methods []domain.SignInMethod
	if support.browserLogin {
		methods = append(methods, domain.SignInBrowser)
	}
	if support.deviceLogin {
		methods = append(methods, domain.SignInDeviceCode)
	}
	return methods
}

func (a Adapter) apiSetupSupported() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	env, _ := a.environment(ctx)
	_, support, err := compatibleExecutable(ctx, a.Executable, env)
	return err == nil && support.methods["config/batchWrite"] && support.methods["account/read"]
}

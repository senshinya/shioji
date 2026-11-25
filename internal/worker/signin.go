package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"strings"
	"time"
)

type workerSignIn struct {
	status      domain.SignInStatus
	fingerprint [32]byte
	cancel      context.CancelFunc
	login       harness.SignIn
}

func (s *Server) signInCommand(method string, in protocol.SignInInput) (domain.SignInStatus, error) {
	if in.ID == "" || len(in.ID) > 128 || s.adapter(in.HarnessID) == nil {
		return domain.SignInStatus{}, errors.New("登录请求无效")
	}
	s.signInMu.Lock()
	defer s.signInMu.Unlock()
	if s.signInClosed || s.ctx.Err() != nil {
		return domain.SignInStatus{}, errors.New("执行服务正在关闭")
	}
	current := s.signIns[in.HarnessID]
	if method != "runtime.signin.start" {
		if current == nil || current.status.ID != in.ID {
			for id, entry := range s.signIns {
				if entry.status.ID == in.ID && id != in.HarnessID {
					return domain.SignInStatus{}, errors.New("登录不属于该 Agent")
				}
			}
			return domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInExpired, Message: "登录已过期，请重新登录"}, nil
		}
		if method == "runtime.signin.code" && current.status.Phase == domain.SignInWaiting && current.status.Challenge.ManualCode {
			receiver, ok := current.login.(harness.SignInCodeReceiver)
			if !ok {
				return current.status, harness.Unsupported("sign-in-input")
			}
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			err := receiver.SubmitCode(ctx, in.AuthorizationCode)
			cancel()
			if err == nil || harness.IsUncertain(err) {
				current.status.Challenge.ManualCode = false
				current.status.Message = "已提交授权信息，正在核对账号"
			}
			if err != nil && !harness.IsUncertain(err) {
				return current.status, err
			}
		}
		if method == "runtime.signin.cancel" && current.status.Phase.Active() {
			current.status.Phase = domain.SignInCanceled
			current.status.Challenge = domain.SignInChallenge{}
			current.status.Message = ""
			// Cancel the exact source authorization before ending its waiting context.
			if current.login != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := current.login.Cancel(ctx)
				cancel()
				if err != nil {
					current.status.Message = "登录已关闭；若已在浏览器授权，请重新检测账号"
				}
			}
			current.cancel()
		}
		return current.status, nil
	}
	if in.Method != domain.SignInBrowser && in.Method != domain.SignInDeviceCode && in.Method != domain.SignInAPI {
		return domain.SignInStatus{}, errors.New("登录方式不可用")
	}
	if profile, ok := s.agentProfile(in.HarnessID); ok && (profile.Mode == domain.AgentProfileAPI) != (in.Method == domain.SignInAPI) {
		return domain.SignInStatus{}, errors.New("请选择此 Profile 的登录方式")
	}
	if in.AuthorizationCode != "" || (in.Method == domain.SignInAPI) != (in.API != nil) {
		return domain.SignInStatus{}, errors.New("接入配置无效")
	}
	encoded, _ := json.Marshal(in)
	fingerprint := sha256.Sum256(encoded)
	if current != nil && current.status.ID == in.ID {
		if current.fingerprint != fingerprint {
			return domain.SignInStatus{}, errors.New("登录配置已变化，请重新发起登录")
		}
		return current.status, nil
	}
	if current != nil && current.status.Phase.Active() {
		return domain.SignInStatus{}, errors.New("该 Agent 已有登录正在进行")
	}
	if profile, ok := s.agentProfile(in.HarnessID); ok && profile.Authorized {
		return domain.SignInStatus{}, errors.New("此 Profile 已绑定账号或服务。请为新的账号或服务创建 Profile")
	}
	// Different harnesses may not reuse another authorization's operation identity.
	for _, entry := range s.signIns {
		if entry.status.ID == in.ID {
			return domain.SignInStatus{}, errors.New("登录标识已使用")
		}
	}
	if s.signIns == nil {
		s.signIns = map[string]*workerSignIn{}
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	entry := &workerSignIn{status: domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInStarting}, fingerprint: fingerprint, cancel: cancel}
	s.signIns[in.HarnessID] = entry
	s.wg.Add(1)
	go s.runSignIn(ctx, in, entry)
	return entry.status, nil
}
func (s *Server) signInAdapter(in protocol.SignInInput) (harness.Adapter, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	adapter := s.adapter(in.HarnessID)
	if _, managed := s.agentProfile(in.HarnessID); managed {
		if err := s.checkAgentProfileHome(in.HarnessID, false); err != nil {
			return nil, err
		}
	}
	if in.Configuration == nil {
		if err := s.runtimeSettingError(in.HarnessID); err != nil {
			return nil, errors.New("请先修正 Agent 配置")
		}
		return adapter, nil
	}
	config := *in.Configuration
	if config.HarnessID != in.HarnessID {
		return nil, errors.New("登录配置不属于该 Agent")
	}
	executable := strings.TrimSpace(config.Executable)
	if executable != "" {
		st, err := os.Stat(executable)
		if !filepath.IsAbs(executable) || len(executable) > 4096 || strings.ContainsAny(executable, "\x00\r\n") || err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return nil, errors.New("请检查所属设备上的 Agent 可执行文件")
		}
	}
	provider, ok := adapter.(harness.InstallationProvider)
	if !ok {
		return nil, harness.Unsupported("configure-runtime")
	}
	next, err := provider.WithExecutable(executable)
	if err != nil {
		return nil, err
	}
	return s.applyRuntimeOptions(in.HarnessID, next, config.Options)
}
func (s *Server) runSignIn(ctx context.Context, in protocol.SignInInput, entry *workerSignIn) {
	defer s.wg.Done()
	defer entry.cancel()
	adapter, err := s.signInAdapter(in)
	var login harness.SignIn
	var challenge domain.SignInChallenge
	var ready domain.Readiness
	if err == nil && in.Method == domain.SignInAPI {
		provider, ok := adapter.(harness.APIConfigurationProvider)
		if !ok {
			err = harness.Unsupported("api-configuration")
		} else {
			if reporter, ok := adapter.(harness.APISetupProgressProvider); ok {
				ready, err = reporter.ConfigureAPIWithProgress(ctx, *in.API, func(value domain.RuntimeSetupProgress) {
					s.signInMu.Lock()
					defer s.signInMu.Unlock()
					if entry.status.Phase.Active() && ctx.Err() == nil {
						entry.status.Setup = &value
					}
				})
			} else {
				ready, err = provider.ConfigureAPI(ctx, *in.API)
			}
		}
		in.API = nil // The credential has no role in polling, refresh or saved state.
	} else if err == nil {
		provider, ok := adapter.(harness.AuthenticationProvider)
		if !ok {
			err = harness.Unsupported("sign-in")
		} else {
			startCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			login, challenge, err = provider.BeginSignIn(startCtx, in.Method)
			cancel()
		}
	}
	ready, err = confirmedSignInReadiness(ready, err)
	s.signInMu.Lock()
	if !entry.status.Phase.Active() || ctx.Err() != nil {
		if entry.status.Phase.Active() {
			entry.status.Phase = domain.SignInExpired
			entry.status.Message = "登录已过期，请重新登录"
		}
		s.signInMu.Unlock()
		if login != nil {
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			login.Cancel(stop)
			cancel()
		}
		return
	}
	if err != nil {
		entry.status.Phase = domain.SignInFailed
		entry.status.Message = "无法开始登录，请检查 Agent 配置与设备网络后重试"
		if in.Method == domain.SignInAPI {
			entry.status.Message = "API 配置未确认，请重新检测系统配置"
			var setup *harness.SetupError
			if errors.As(err, &setup) {
				entry.status.Message = setup.Message
			}
		}
		s.signInMu.Unlock()
		return
	}
	entry.login = login
	entry.status.Phase = domain.SignInWaiting
	entry.status.Challenge = challenge
	s.signInMu.Unlock()
	if login != nil {
		ready, err = login.Wait(ctx)
	}
	ready, err = confirmedSignInReadiness(ready, err)
	if _, managed := s.agentProfile(in.HarnessID); err == nil && managed {
		if !confirmedAccount(ready.Account) {
			err = errors.New("Agent 未确认此 Profile 的登录")
		} else {
			err = s.bindAgentProfile(in.HarnessID)
		}
	}
	if err == nil && ctx.Err() == nil && s.signInUsesSavedConfiguration(in) {
		_, _ = s.recheckRuntime(in.HarnessID)
	}
	s.signInMu.Lock()
	defer s.signInMu.Unlock()
	entry.login = nil
	if !entry.status.Phase.Active() {
		return
	}
	entry.status.Challenge = domain.SignInChallenge{}
	if ctx.Err() != nil {
		entry.status.Phase = domain.SignInExpired
		entry.status.Message = "登录已过期，请重新登录"
		return
	}
	if err != nil {
		entry.status.Phase = domain.SignInFailed
		entry.status.Message = "登录未完成，请重试或重新检测账号"
		return
	}
	descriptor := adapter.Descriptor()
	info := domain.HarnessInfo{ID: in.HarnessID, Name: descriptor.Name, Icon: descriptor.Icon, State: ready.State, Error: ready.Error, Account: ready.Account, Models: ready.Models, Permissions: ready.Permissions, PermissionChoices: ready.PermissionChoices, Plan: ready.Plan, Capabilities: ready.Capabilities}
	if profile, ok := s.agentProfile(in.HarnessID); ok {
		info.Profile = &profile
		if profile.Icon != "" {
			info.Icon = profile.Icon
		}
	}
	if provider, ok := adapter.(harness.InstallationProvider); ok {
		installation := provider.Installation()
		info.Installation = &installation
		applyProfileBinding(&info)
	}
	entry.status.Profile = &info
	entry.status.Phase = domain.SignInCompleted
	entry.status.Message = ""
}
func (s *Server) closeSignIns() {
	s.signInMu.Lock()
	s.signInClosed = true
	var logins []harness.SignIn
	for _, entry := range s.signIns {
		entry.cancel()
		if entry.login != nil {
			logins = append(logins, entry.login)
		}
	}
	s.signInMu.Unlock()
	for _, login := range logins {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		login.Cancel(ctx)
		cancel()
	}
}

func (s *Server) signInUsesSavedConfiguration(in protocol.SignInInput) bool {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if in.Configuration == nil {
		return true
	}
	config := in.Configuration
	options := s.runtimeOptions[in.HarnessID]
	return strings.TrimSpace(config.Executable) == s.runtimeSettings[in.HarnessID] && (config.Options == nil || reflect.DeepEqual(*config.Options, options))
}
func (s *Server) hasActiveSignIn() bool {
	if s.activeToolAuthorization("") {
		return true
	}
	s.signInMu.Lock()
	defer s.signInMu.Unlock()
	for _, entry := range s.signIns {
		if entry.status.Phase.Active() {
			return true
		}
	}
	return false
}

// Authentication and catalog readiness are independent. A verified account is
// kept even when model discovery fails, without inventing selectable models.
func confirmedSignInReadiness(ready domain.Readiness, err error) (domain.Readiness, error) {
	var setup *harness.SetupError
	if errors.As(err, &setup) && confirmedAccount(setup.Account) {
		return domain.Readiness{State: domain.RuntimeUnavailable, Account: setup.Account, Error: setup.Message}, nil
	}
	return ready, err
}

func confirmedAccount(account *domain.RuntimeAccount) bool {
	return account != nil && (account.State == "authenticated" || account.State == "configured" || account.State == "notRequired")
}

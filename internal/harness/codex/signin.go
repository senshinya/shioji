package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"strconv"
	"sync"
	"time"
)

type accountSignIn struct {
	runtime *Runtime
	id      string
	once    sync.Once
}

func (a Adapter) BeginSignIn(ctx context.Context, method domain.SignInMethod) (harness.SignIn, domain.SignInChallenge, error) {
	r, err := Start(ctx, Options{Executable: a.Executable, Home: a.Home, Arguments: a.runtimeOptions.Arguments, Environment: a.runtimeOptions.Environment, authentication: true})
	if err != nil {
		return nil, domain.SignInChallenge{}, errors.New("无法启动登录，请检查 Agent 安装与配置")
	}
	login, challenge, err := r.beginSignIn(ctx, method)
	if err != nil {
		r.Close()
		return nil, domain.SignInChallenge{}, err
	}
	return login, challenge, nil
}
func (r *Runtime) beginSignIn(ctx context.Context, method domain.SignInMethod) (*accountSignIn, domain.SignInChallenge, error) {
	kind := ""
	if method == domain.SignInBrowser && r.protocol.browserLogin {
		kind = "chatgpt"
	}
	if method == domain.SignInDeviceCode && r.protocol.deviceLogin {
		kind = "chatgptDeviceCode"
	}
	if kind == "" {
		return nil, domain.SignInChallenge{}, harness.Unsupported("sign-in-method")
	}
	var out struct{ Type, LoginID, AuthURL, VerificationURL, UserCode string }
	if err := r.call(ctx, "account/login/start", struct {
		Type string `json:"type"`
	}{kind}, &out); err != nil {
		return nil, domain.SignInChallenge{}, errors.New("无法发起登录，请重试或检查设备网络")
	}
	login := &accountSignIn{runtime: r, id: out.LoginID}
	target := out.AuthURL
	if method == domain.SignInDeviceCode {
		target = out.VerificationURL
	}
	u, err := url.Parse(target)
	if err != nil || len(target) > 16<<10 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || out.Type != kind || out.LoginID == "" || len(out.UserCode) > 128 || method == domain.SignInDeviceCode && out.UserCode == "" {
		return nil, domain.SignInChallenge{}, errors.New("Agent 返回的登录信息无效")
	}
	challenge := domain.SignInChallenge{URL: target, Code: out.UserCode}
	if method == domain.SignInBrowser {
		redirect := u.Query().Get("redirect_uri")
		if redirect == "" {
			return nil, domain.SignInChallenge{}, errors.New("Agent 未提供可核验的浏览器回调")
		}
		{
			callback, e := url.Parse(redirect)
			if e != nil || callback.Scheme != "http" || callback.User != nil {
				return nil, domain.SignInChallenge{}, errors.New("Agent 返回的登录回调无效")
			}
			port, _ := strconv.Atoi(callback.Port())
			endpoint := domain.LoopbackEndpoint{Host: callback.Hostname(), Port: port}
			if !endpoint.Valid() {
				return nil, domain.SignInChallenge{}, errors.New("Agent 返回的登录回调无效")
			}
			challenge.Callback = &endpoint
		}
	}
	return login, challenge, nil
}
func (l *accountSignIn) close() {
	l.once.Do(func() {
		l.runtime.Close()
	})
}
func (l *accountSignIn) Wait(ctx context.Context) (domain.Readiness, error) {
	defer l.close()
	for {
		select {
		case body := <-l.runtime.loginEvents:
			var result struct {
				LoginID string `json:"loginId"`
				Success bool   `json:"success"`
			}
			if json.Unmarshal(body, &result) != nil || result.LoginID != l.id {
				continue
			}
			if !result.Success {
				return domain.Readiness{}, errors.New("登录未完成，请重试")
			}
			// Completion is not proof of usable credentials; verify the effective account.
			if err := l.runtime.catalog(ctx); err != nil {
				return domain.Readiness{}, err
			}
			return l.runtime.ready, nil
		case <-ctx.Done():
			return domain.Readiness{}, ctx.Err()
		case <-l.runtime.done:
			return domain.Readiness{}, errors.New("登录进程已退出，请重试")
		}
	}
}
func (l *accountSignIn) Cancel(ctx context.Context) error {
	defer l.close()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := l.runtime.call(callCtx, "account/login/cancel", struct {
		LoginID string `json:"loginId"`
	}{l.id}, nil); err != nil {
		return errors.New("登录已关闭；若浏览器已完成授权，请重新检测账号")
	}
	return nil
}

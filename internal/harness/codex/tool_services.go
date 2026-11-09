package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sort"
	"strconv"
)

func (r *Runtime) ToolServices(ctx context.Context) (domain.ToolServiceCatalog, error) {
	if !r.supports("mcpServerStatus/list") {
		return domain.ToolServiceCatalog{}, harness.Unsupported("tool-services")
	}
	config, err := r.systemConfiguration(ctx)
	if err != nil {
		return domain.ToolServiceCatalog{}, errors.New("无法读取工具服务配置，请重试")
	}
	catalog := domain.ToolServiceCatalog{Services: []domain.ToolService{}, CanRefresh: r.supports("config/mcpServer/reload")}
	catalog.CanAdd = r.canWriteToolConfiguration(config)
	catalog.Revision = toolConfigurationRevision(config)
	listed := map[string]bool{}
	var cursor *string
	seen := map[string]bool{}
	for {
		var result struct {
			Data []struct {
				Name       string                     `json:"name"`
				HTTPOrigin string                     `json:"httpOrigin"`
				Auth       string                     `json:"authStatus"`
				Runtime    string                     `json:"runtimeStatus"`
				Tools      map[string]json.RawMessage `json:"tools"`
				ToolsError *string                    `json:"toolsError"`
				Info       *struct {
					Title string `json:"title"`
				} `json:"serverInfo"`
			} `json:"data"`
			Next *string `json:"nextCursor"`
		}
		if err := r.call(ctx, "mcpServerStatus/list", map[string]any{"limit": 100, "cursor": cursor}, &result); err != nil {
			return catalog, errors.New("无法检查工具服务，请重试")
		}
		for _, source := range result.Data {
			if source.Name == "" {
				continue
			}
			service := domain.ToolService{ID: source.Name, Name: source.Name, Origin: publicOrigin(source.HTTPOrigin), Tools: len(source.Tools), State: domain.ToolServiceUnknown}
			listed[source.Name] = true
			var configured nativeToolService
			json.Unmarshal(config.Config.ToolServices[source.Name], &configured)
			if source.Info != nil && source.Info.Title != "" {
				service.Name = source.Info.Title
			}
			service.CanAuthorize = r.supports("mcpServer/oauth/login") && (source.Auth == "notLoggedIn" || source.Auth == "oAuth")
			switch {
			case configured.Enabled != nil && !*configured.Enabled || source.Runtime == "disabled":
				service.State = domain.ToolServiceDisabled
				service.CanAuthorize = false
			case source.Runtime == "authenticationRequired":
				service.State = domain.ToolServiceAuthorizationRequired
			case source.Runtime == "starting":
				service.State = domain.ToolServiceConnecting
			case source.Runtime == "failed" || source.ToolsError != nil:
				service.State = domain.ToolServiceFailed
				if source.Auth == "notLoggedIn" {
					service.State = domain.ToolServiceAuthorizationRequired
				}
			case source.Runtime == "connected" || len(source.Tools) > 0:
				service.State = domain.ToolServiceReady
			case source.Auth == "notLoggedIn":
				service.State = domain.ToolServiceAuthorizationRequired
			}
			r.projectToolConfiguration(config, &service)
			catalog.Services = append(catalog.Services, service)
		}
		if result.Next == nil {
			for id := range config.Config.ToolServices {
				if listed[id] {
					continue
				}
				s := domain.ToolService{ID: id, Name: id, State: domain.ToolServiceUnknown}
				r.projectToolConfiguration(config, &s)
				if s.Configuration != nil && !s.Configuration.Enabled {
					s.State = domain.ToolServiceDisabled
				}
				catalog.Services = append(catalog.Services, s)
			}
			sort.SliceStable(catalog.Services, func(i, j int) bool { return catalog.Services[i].ID < catalog.Services[j].ID })
			return catalog, nil
		}
		if seen[*result.Next] {
			return catalog, errors.New("工具服务分页未前进，请重试")
		}
		seen[*result.Next] = true
		cursor = result.Next
	}
}
func publicOrigin(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
func (r *Runtime) RefreshToolServices(ctx context.Context) error {
	if !r.supports("config/mcpServer/reload") {
		return harness.Unsupported("tool-service-refresh")
	}
	if err := r.call(ctx, "config/mcpServer/reload", nil, nil); err != nil {
		return errors.New("工具服务刷新未确认，请重新检查状态")
	}
	return nil
}

type toolServiceLogin struct {
	runtime  *Runtime
	name, id string
	events   chan json.RawMessage
}

func (r *Runtime) BeginToolServiceAuthorization(ctx context.Context, id string) (harness.ToolServiceAuthorization, domain.SignInChallenge, error) {
	if !r.supports("mcpServer/oauth/login") {
		return nil, domain.SignInChallenge{}, harness.Unsupported("tool-service-authorization")
	}
	login := &toolServiceLogin{runtime: r, name: id, events: make(chan json.RawMessage, 8)}
	r.mu.Lock()
	if r.toolLogin != nil {
		r.mu.Unlock()
		return nil, domain.SignInChallenge{}, errors.New("已有工具服务正在授权")
	}
	r.toolLogin = login
	r.mu.Unlock()
	fail := func(message string) (harness.ToolServiceAuthorization, domain.SignInChallenge, error) {
		login.release()
		return nil, domain.SignInChallenge{}, errors.New(message)
	}
	var out struct {
		URL string `json:"authorizationUrl"`
		ID  string `json:"loginId"`
	}
	if err := r.call(ctx, "mcpServer/oauth/login", map[string]any{"name": id, "timeoutSecs": 180}, &out); err != nil {
		return fail("无法发起工具授权，请检查服务配置后重试")
	}
	u, err := url.Parse(out.URL)
	if err != nil || !domain.ValidAuthorizationURL(out.URL) || out.ID == "" {
		return fail("工具服务未提供可核验的授权信息")
	}
	challenge := domain.SignInChallenge{URL: out.URL}
	if redirect := u.Query().Get("redirect_uri"); redirect != "" {
		callback, e := url.Parse(redirect)
		if e != nil || callback.User != nil {
			return fail("工具服务返回的授权回调无效")
		}
		if callback.Scheme == "http" {
			port := 80
			if callback.Port() != "" {
				port, _ = strconv.Atoi(callback.Port())
			}
			endpoint := domain.LoopbackEndpoint{Host: callback.Hostname(), Port: port}
			if !endpoint.Valid() {
				return fail("工具服务返回的授权回调无效")
			}
			challenge.Callback = &endpoint
		} else if callback.Scheme != "https" {
			return fail("工具服务返回的授权回调无效")
		}
	}
	login.id = out.ID
	return login, challenge, nil
}
func (l *toolServiceLogin) release() {
	l.runtime.mu.Lock()
	if l.runtime.toolLogin == l {
		l.runtime.toolLogin = nil
	}
	l.runtime.mu.Unlock()
}
func (l *toolServiceLogin) Wait(ctx context.Context) error {
	defer l.release()
	for {
		select {
		case raw := <-l.events:
			var result struct {
				Name    string  `json:"name"`
				ID      string  `json:"loginId"`
				Thread  *string `json:"threadId"`
				Success bool    `json:"success"`
			}
			if json.Unmarshal(raw, &result) != nil || result.Name != l.name || result.ID != l.id || result.Thread != nil {
				continue
			}
			if !result.Success {
				return errors.New("工具授权未完成，请重试")
			}
			return nil
		case <-ctx.Done():
			return errors.New("工具授权已过期，请重新授权")
		case <-l.runtime.done:
			return errors.New("Agent 已重新连接，请检查工具授权状态")
		}
	}
}
func (r *Runtime) receiveToolLogin(raw json.RawMessage) {
	var event struct {
		Name   string  `json:"name"`
		Thread *string `json:"threadId"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Thread != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if login := r.toolLogin; login != nil && login.name == event.Name {
		select {
		case login.events <- append(json.RawMessage(nil), raw...):
		default:
		}
	}
}

var _ harness.ToolServiceRuntime = (*Runtime)(nil)

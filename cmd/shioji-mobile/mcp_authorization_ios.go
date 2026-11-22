package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (s *mobileToolServices) authorizationActive() bool {
	return s.catalog.Authorization != nil && s.catalog.Authorization.Phase.Active()
}

func (s *mobileToolServices) startAuthorization(a *mobileApp, w *mygo.Window, serviceID string) {
	if !s.available(a, w) || s.authRequest.busy || s.authorizationActive() || s.needsCheck || s.read.busy || s.write.busy {
		return
	}
	s.catalog.Authorization = &domain.ToolServiceAuthorization{
		ID: domain.NewID(), HarnessID: s.harness, ServiceID: serviceID, Phase: domain.SignInStarting,
	}
	s.sendAuthorization(a, w, "start")
}

func (s *mobileToolServices) pollAuthorization(a *mobileApp, c *ui.Context, w *mygo.Window) {
	if a.preview || !s.authorizationActive() || !s.loaded || s.authRequest.busy {
		return
	}
	if delay := s.nextAuth.Sub(c.Now()); delay > 0 {
		c.After(delay)
		return
	}
	s.sendAuthorization(a, w, "status")
}

func (s *mobileToolServices) sendAuthorization(a *mobileApp, w *mygo.Window, action string) {
	if !s.available(a, w) || s.authRequest.busy || !s.authorizationActive() {
		return
	}
	expected := *s.catalog.Authorization
	s.nextAuth = time.Now().Add(time.Second)
	if a.preview {
		if action == "start" {
			expected.Phase = domain.SignInWaiting
			expected.Challenge.URL = "https://127.0.0.1:9/mcp/authorize"
		} else {
			expected.Phase, expected.Challenge = domain.SignInCompleted, domain.SignInChallenge{}
			for i := range s.catalog.Services {
				if s.catalog.Services[i].ID == expected.ServiceID {
					s.catalog.Services[i].State, s.catalog.Services[i].Tools = domain.ToolServiceReady, 2
				}
			}
		}
		s.catalog.Authorization = &expected
		w.Invalidate()
		return
	}
	epoch := s.epoch
	in := protocol.ToolServiceInput{HarnessID: expected.HarnessID, ServiceID: expected.ServiceID, ID: expected.ID}
	a.pageJob(w, &s.authRequest, "runtime.tools.auth."+action, in, decodePage(func(status domain.ToolServiceAuthorization) error {
		if s.epoch != epoch || a.model.ConnectionEpoch != epoch {
			return nil
		}
		if status.ID != in.ID || status.HarnessID != in.HarnessID || status.ServiceID != in.ServiceID {
			return fmt.Errorf("MCP 授权回执与当前操作不一致")
		}
		s.catalog.Authorization = &status
		if !status.Phase.Active() {
			s.closeForward()
		}
		if status.Phase == domain.SignInCompleted {
			s.load(a, w, s.catalog.CanRefresh)
		}
		return nil
	}))
}

func (s *mobileToolServices) authorizationView(a *mobileApp, c *ui.Context, w *mygo.Window, status *domain.ToolServiceAuthorization) {
	ui.Column(c.Key("mcp-authorization")).Padding(4, 0, 6).Transition(mobilePanelTransition).Children(func() {
		if status.Phase.Active() {
			message := "正在准备 MCP 授权…"
			if status.Phase == domain.SignInWaiting {
				message = "等待浏览器授权"
			}
			mobileMessage(c, message)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				if status.Phase == domain.SignInWaiting && domain.ValidAuthorizationURL(status.Challenge.URL) {
					secondaryAction(c, "打开 MCP 授权页", func() { s.openAuthorization(a, w, *status) }).
						Disabled(s.opening || !s.available(a, w))
				}
				action(c, "检查 MCP 授权结果", func() { s.sendAuthorization(a, w, "status") }).
					Disabled(s.authRequest.busy || !s.available(a, w))
			})
		}
		if status.Message != "" {
			mobileMessage(c, status.Message)
		}
		s.authRequest.status(c)
	})
}

func (s *mobileToolServices) closeForward() {
	if s.stopForward != nil {
		s.stopForward()
	}
	s.stopForward, s.forwardRoute, s.opening = nil, "", false
}

func (s *mobileToolServices) openAuthorization(a *mobileApp, w *mygo.Window, status domain.ToolServiceAuthorization) {
	address := status.Challenge.URL
	if !s.available(a, w) || s.opening || !domain.ValidAuthorizationURL(address) {
		return
	}
	owner, epoch := a.controller, s.epoch
	endpoint := a.activeEndpoint()
	current := func() bool {
		active := s.catalog.Authorization
		return s.visible && s.available(a, w) && owner == a.controller && epoch == a.model.ConnectionEpoch &&
			active != nil && active.ID == status.ID && active.Phase == domain.SignInWaiting && active.Challenge.URL == address
	}
	parsed, _ := url.Parse(address)
	open := func() {
		if !current() {
			return
		}
		if parsed.Scheme == "http" {
			if err := mygo.Shell.OpenExternal(address); err != nil {
				s.authRequest.err = "无法打开授权页，请重试"
			}
			return
		}
		a.callUIKit("openAgentAuthorization", map[string]any{"window": w.NativeHandle(), "url": address}, func(err error) {
			if err != nil && current() {
				s.authRequest.err = "无法打开授权页，请重试"
				w.Invalidate()
			}
		})
	}
	if a.preview {
		open()
		return
	}
	var forward []domain.LoopbackEndpoint
	if status.Challenge.Callback != nil {
		if !status.Challenge.Callback.Valid() {
			s.authRequest.err = "工具服务未提供可转发的授权回调"
			return
		}
		forward = append(forward, *status.Challenge.Callback)
	}
	port := 443
	if parsed.Scheme == "http" {
		port = 80
	}
	if parsed.Port() != "" {
		port, _ = strconv.Atoi(parsed.Port())
	}
	provider := domain.LoopbackEndpoint{Host: parsed.Hostname(), Port: port}
	if provider.Valid() && !slices.Contains(forward, provider) {
		forward = append(forward, provider)
	}
	routeID := endpoint.RouteID()
	if len(forward) == 0 || s.stopForward != nil && s.forwardRoute == routeID {
		open()
		return
	}
	if endpoint.DeviceID == "" || endpoint.DeviceID != a.model.Snapshot.DeviceID {
		s.authRequest.err = "连接该设备后再打开授权页"
		return
	}
	signer, err := a.identity()
	if err != nil {
		s.authRequest.err = "无法读取设备的 SSH 密钥"
		return
	}
	s.closeForward()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	s.opening, s.stopForward, s.authRequest.err = true, cancel, ""
	route := a.routeDial()
	go func() {
		var forwardErr error
		for _, target := range forward {
			bridge, err := endpoint.ForwardAgentCallback(ctx, signer, route, target)
			if err == nil {
				err = bridge.Check(ctx)
			}
			if err != nil {
				forwardErr = err
				break
			}
		}
		mygo.RunOnMain(func() {
			if ctx.Err() != nil {
				return
			}
			if !current() || a.activeEndpoint().RouteID() != routeID {
				s.closeForward()
				return
			}
			if forwardErr != nil {
				s.closeForward()
				s.authRequest.err = "无法连接设备上的 MCP 授权，请重试"
			} else {
				s.opening, s.forwardRoute = false, routeID
				open()
			}
			w.Invalidate()
		})
	}()
}

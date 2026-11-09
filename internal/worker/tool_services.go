package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"time"
)

type workerToolAuthorization struct {
	status domain.ToolServiceAuthorization
	cancel context.CancelFunc
}

func (s *Server) toolRuntime(id string) (harness.ToolServiceRuntime, func(), error) {
	gate := s.catalogGates[id]
	if gate == nil || !s.agentEnabled(id) {
		return nil, nil, errors.New("请先启用该 Agent")
	}
	if !gate.TryRLock() {
		return nil, nil, errors.New("Agent 正在刷新，请稍后重试")
	}
	r := s.catalogRuntime(id)
	if r == nil || r.Stopped() {
		gate.RUnlock()
		return nil, nil, errors.New("请先完成 Agent 配置并重新检测")
	}
	provider, ok := r.(harness.ToolServiceRuntime)
	if !ok {
		gate.RUnlock()
		return nil, nil, harness.Unsupported("tool-services")
	}
	return provider, gate.RUnlock, nil
}
func (s *Server) listToolServices(in protocol.ToolServiceInput, refresh bool) (domain.ToolServiceCatalog, error) {
	provider, release, err := s.toolRuntime(in.HarnessID)
	if err != nil {
		return domain.ToolServiceCatalog{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
	defer cancel()
	if refresh {
		if s.activeToolAuthorization(in.HarnessID) {
			return domain.ToolServiceCatalog{}, errors.New("请先完成工具授权")
		}
		if err = provider.RefreshToolServices(ctx); err != nil {
			return domain.ToolServiceCatalog{}, err
		}
		// Session runtimes are independent processes. Queue each one's native reload
		// without stopping or resuming any turn merely to refresh configuration.
		catalog := s.catalogRuntime(in.HarnessID)
		s.runtimeMu.RLock()
		owned := make([]harness.Runtime, 0, len(s.runtimes))
		for _, r := range s.runtimes {
			owned = append(owned, r)
		}
		s.runtimeMu.RUnlock()
		seen := map[<-chan struct{}]bool{}
		if catalog != nil {
			seen[catalog.Done()] = true
		}
		for _, r := range owned {
			if r.Stopped() || r.Readiness().HarnessID != in.HarnessID || seen[r.Done()] {
				continue
			}
			seen[r.Done()] = true
			if p, ok := r.(harness.ToolServiceRuntime); ok {
				if p.RefreshToolServices(ctx) != nil {
					return domain.ToolServiceCatalog{}, errors.New("部分会话尚未确认工具刷新，请重试")
				}
			}
		}
	}
	result, err := provider.ToolServices(ctx)
	if err != nil {
		return result, err
	}
	s.toolAuthMu.Lock()
	if entry := s.toolAuth[in.HarnessID]; entry != nil {
		status := entry.status
		result.Authorization = &status
	}
	s.toolAuthMu.Unlock()
	return result, nil
}

func (s *Server) toolAuthorizationCommand(method string, in protocol.ToolServiceInput) (domain.ToolServiceAuthorization, error) {
	if in.HarnessID == "" || s.adapter(in.HarnessID) == nil || in.ServiceID == "" || len(in.ServiceID) > 512 || len(in.ID) > 128 {
		return domain.ToolServiceAuthorization{}, errors.New("工具授权请求无效")
	}
	s.toolAuthMu.Lock()
	defer s.toolAuthMu.Unlock()
	if s.ctx.Err() != nil {
		return domain.ToolServiceAuthorization{}, errors.New("执行服务正在关闭")
	}
	current := s.toolAuth[in.HarnessID]
	if method != "runtime.tools.auth.start" {
		if current != nil && current.status.ID == in.ID && current.status.ServiceID == in.ServiceID {
			return current.status, nil
		}
		return domain.ToolServiceAuthorization{ID: in.ID, HarnessID: in.HarnessID, ServiceID: in.ServiceID, Phase: domain.SignInExpired, Message: "授权记录已过期，请重新检查"}, nil
	}
	if in.ID == "" {
		return domain.ToolServiceAuthorization{}, errors.New("缺少授权操作标识")
	}
	if current != nil && current.status.ID == in.ID {
		if current.status.ServiceID != in.ServiceID {
			return domain.ToolServiceAuthorization{}, errors.New("授权不属于该服务")
		}
		return current.status, nil
	}
	if current != nil && current.status.Phase.Active() {
		return domain.ToolServiceAuthorization{}, errors.New("该 Agent 已有工具服务正在授权")
	}
	for _, entry := range s.toolAuth {
		if entry.status.ID == in.ID {
			return domain.ToolServiceAuthorization{}, errors.New("授权标识已使用")
		}
	}
	ctx, cancel := context.WithTimeout(s.ctx, 4*time.Minute)
	entry := &workerToolAuthorization{status: domain.ToolServiceAuthorization{ID: in.ID, HarnessID: in.HarnessID, ServiceID: in.ServiceID, Phase: domain.SignInStarting}, cancel: cancel}
	if s.toolAuth == nil {
		s.toolAuth = map[string]*workerToolAuthorization{}
	}
	s.toolAuth[in.HarnessID] = entry
	s.wg.Add(1)
	go s.runToolAuthorization(ctx, in, entry)
	return entry.status, nil
}
func (s *Server) runToolAuthorization(ctx context.Context, in protocol.ToolServiceInput, entry *workerToolAuthorization) {
	defer s.wg.Done()
	defer entry.cancel()
	provider, release, err := s.toolRuntime(in.HarnessID)
	if err == nil {
		defer release()
	}
	var login harness.ToolServiceAuthorization
	var challenge domain.SignInChallenge
	if err == nil {
		var catalog domain.ToolServiceCatalog
		catalog, err = provider.ToolServices(ctx)
		found := false
		if err == nil {
			for _, service := range catalog.Services {
				if service.ID == in.ServiceID && service.CanAuthorize && service.State != domain.ToolServiceDisabled {
					found = true
					break
				}
			}
			if !found {
				err = errors.New("该服务暂不支持浏览器授权")
			}
		}
	}
	if err == nil {
		login, challenge, err = provider.BeginToolServiceAuthorization(ctx, in.ServiceID)
	}
	s.toolAuthMu.Lock()
	if err != nil {
		entry.status.Phase = domain.SignInFailed
		entry.status.Message = "无法开始工具授权，请检查服务配置后重试"
		s.toolAuthMu.Unlock()
		return
	}
	if ctx.Err() == nil {
		entry.status.Phase = domain.SignInWaiting
		entry.status.Challenge = challenge
	}
	s.toolAuthMu.Unlock()
	err = login.Wait(ctx)
	s.toolAuthMu.Lock()
	defer s.toolAuthMu.Unlock()
	entry.status.Challenge = domain.SignInChallenge{}
	if ctx.Err() != nil {
		entry.status.Phase = domain.SignInExpired
		entry.status.Message = "工具授权已过期，请重新检查"
	} else if err != nil {
		entry.status.Phase = domain.SignInFailed
		entry.status.Message = "工具授权未完成，请重试"
	} else {
		entry.status.Phase = domain.SignInCompleted
	}
}
func (s *Server) activeToolAuthorization(id string) bool {
	s.toolAuthMu.Lock()
	defer s.toolAuthMu.Unlock()
	if id != "" {
		entry := s.toolAuth[id]
		return entry != nil && entry.status.Phase.Active()
	}
	for _, entry := range s.toolAuth {
		if entry.status.Phase.Active() {
			return true
		}
	}
	return false
}
func (s *Server) closeToolAuthorizations() {
	s.toolAuthMu.Lock()
	defer s.toolAuthMu.Unlock()
	for _, entry := range s.toolAuth {
		entry.cancel()
	}
}

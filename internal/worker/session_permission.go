package worker

import (
	"context"
	"errors"
	"slices"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

var errPermissionRuntimeBusy = errors.New("请等待当前会话空闲后更改权限")

func (s *Server) changeSessionPermission(in protocol.SessionPermissionInput) (domain.Session, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	var session domain.Session
	if err := s.updateAdmission(); err != nil {
		return session, err
	}
	if s.sessionTransitions[in.SessionID] != nil {
		return session, errPermissionRuntimeBusy
	}
	if err := s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return session, err
	}
	session, err := s.store.SessionRecord(in.SessionID)
	if err != nil {
		return session, err
	}
	if session.ThreadID != in.ThreadID || session.Config.Permission != in.Previous {
		return session, errors.New("会话权限已变化，请重新选择")
	}
	if session.Status == domain.ExecutionStarting || session.Status == domain.ExecutionUnknown || session.ExecutionGuard != "" {
		return session, errPermissionRuntimeBusy
	}
	workspace, err := s.store.Workspace(session.WorkspaceID)
	if err != nil {
		return session, err
	}
	if session.Archived || workspace.Archived {
		return session, errors.New("请先恢复会话")
	}
	runtime := s.currentRuntime(session.ID)
	live := runtime != nil && runtime.ExecutionReady() && runtime.Observes(session)
	catalog := s.catalogRuntime(session.HarnessID)
	if live {
		catalog = runtime
	}
	if catalog == nil {
		return session, harness.Unsupported("session-permission")
	}
	ready := catalog.Readiness()
	if !ready.LivePermissionChange(in.Previous, in.Permission) || !slices.Contains(ready.Permissions, in.Permission) {
		return session, harness.Unsupported("session-permission")
	}
	accept := func() error {
		return s.store.SetSessionPermission(session.ID, session.ThreadID, in.Previous, in.Permission, ready.Capabilities.ExclusivePlan)
	}
	if !live {
		if session.Status.Active() {
			return session, errPermissionRuntimeBusy
		}
		if err := accept(); err != nil {
			return session, err
		}
		return s.store.SessionRecord(session.ID)
	}
	controller, ok := runtime.(harness.LivePermissionRuntime)
	if !ok {
		return session, harness.Unsupported("session-permission")
	}
	release, err := s.beginSessionTransition(s.ctx, session.ID)
	if err != nil {
		return session, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(s.sessionTransitions[session.ID].ctx, 10*time.Second)
	defer cancel()
	next := session.Config
	next.Permission = in.Permission
	if ready.Capabilities.ExclusivePlan {
		next.Plan = false
	}
	if controller.PermissionRequiresRestart(next) {
		idle, err := s.store.RuntimeIdleForPermissionChange(session.ID)
		if err != nil {
			return session, err
		}
		if !idle || !ready.Capabilities.CanReopenIdle() {
			return session, errPermissionRuntimeBusy
		}
		if err := s.cleanSessionRuntime(ctx, session.ID); err != nil {
			return session, err
		}
		if err := ctx.Err(); err != nil {
			return session, err
		}
		if err := accept(); err != nil {
			return session, err
		}
	} else {
		_, err = s.runtimeCallOutsideDispatch(ctx, session.ID, runtime, func() (bool, error) {
			return false, controller.ChangePermission(ctx, session, in.Permission, accept)
		})
		if err != nil {
			return session, err
		}
	}
	return s.store.SessionRecord(session.ID)
}

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

func (s *Server) hookContext(in domain.HookRequest, mutation bool) (domain.LaunchContext, domain.Session, error) {
	if in.WorkspaceID == "" {
		if in.SessionID != "" {
			return domain.LaunchContext{}, domain.Session{}, errors.New("会话需要所属工作区")
		}
		return domain.LaunchContext{}, domain.Session{}, nil
	}
	w, err := s.store.Workspace(in.WorkspaceID)
	if err != nil {
		return domain.LaunchContext{}, domain.Session{}, err
	}
	if w.DeviceID != s.store.DeviceID() || mutation && w.Archived {
		return domain.LaunchContext{}, domain.Session{}, errors.New("工作区不属于当前设备或已归档")
	}
	roots := w.Folders
	var session domain.Session
	if in.SessionID != "" {
		detail, e := s.store.Session(in.SessionID, 0, 1)
		if e != nil {
			return domain.LaunchContext{}, session, e
		}
		session = detail.Session
		if session.WorkspaceID != w.ID || session.HarnessID != in.HarnessID || mutation && session.Archived {
			return domain.LaunchContext{}, session, errors.New("会话不属于此工作区或 Agent，或已归档")
		}
		roots = append([]string{session.Context.CWD}, session.Context.AdditionalDirectories...)
	}
	valid, err := validateFolders(roots)
	if err != nil || len(valid) == 0 || strings.Join(valid, "\x00") != strings.Join(roots, "\x00") {
		return domain.LaunchContext{}, session, errors.New("所属目录已改变，请重新核对")
	}
	launch := domain.LaunchContext{CWD: valid[0]}
	if in.SessionID != "" {
		launch.AdditionalDirectories = valid[1:]
	}
	return launch, session, nil
}

func (s *Server) listHooks(ctx context.Context, in domain.HookRequest) (domain.HookCatalog, error) {
	launch, session, err := s.hookContext(in, false)
	if err != nil {
		return domain.HookCatalog{}, err
	}
	if in.SessionID != "" && !session.Archived {
		if r := s.currentRuntime(session.ID); r != nil && !r.Stopped() && r.Observes(session) && r.Readiness().HarnessID == in.HarnessID && r.Readiness().Capabilities.HookInspection {
			if provider, ok := r.(harness.HookProvider); ok {
				out, err := provider.Hooks(ctx, launch, session.ThreadID)
				current, e := s.store.Session(session.ID, 0, 1)
				if e != nil || current.Session.ThreadID != session.ThreadID || current.Session.Context.CWD != launch.CWD || !slices.Equal(current.Session.Context.AdditionalDirectories, launch.AdditionalDirectories) || current.Session.HarnessID != in.HarnessID {
					return domain.HookCatalog{}, errors.New("会话上下文已变化，请重新核对")
				}
				return out, err
			}
		}
	}
	r, release, err := s.borrowCatalog(in.HarnessID)
	if err != nil {
		return domain.HookCatalog{}, err
	}
	defer release()
	provider, ok := r.(harness.HookProvider)
	if !ok || !r.Readiness().Capabilities.HookManagement {
		return domain.HookCatalog{}, harness.Unsupported("hook-management")
	}
	out, err := provider.Hooks(ctx, launch, "")
	if in.SessionID != "" {
		out.RuntimeKnown = false
	}
	return out, err
}

func (s *Server) refreshHookRuntimes(ctx context.Context, id string) bool {
	s.runtimeMu.RLock()
	owned := make([]harness.Runtime, 0, len(s.runtimes))
	for _, r := range s.runtimes {
		owned = append(owned, r)
	}
	s.runtimeMu.RUnlock()
	seen := map[<-chan struct{}]bool{}
	confirmed := true
	for _, r := range owned {
		if r == nil || r.Stopped() || r.Readiness().HarnessID != id || seen[r.Done()] {
			continue
		}
		seen[r.Done()] = true
		if p, ok := r.(harness.HookProvider); ok {
			if err := p.RefreshHooks(ctx); err != nil {
				confirmed = false
			}
		}
	}
	return confirmed
}

// The configuration mutex also protects receipt reconciliation: an accepted
// receipt visible here cannot be an in-flight write on this worker instance.
func (s *Server) inspectHookOperation(ctx context.Context, in domain.HookRequest) (domain.HookOperation, error) {
	op, err := s.store.HookOperation(in)
	if err != nil || op.State == domain.OperationConfirmed || op.State == domain.OperationRejected {
		return op, err
	}
	if op.State == domain.OperationAccepted {
		op, err = s.store.FinishHookOperation(in, domain.OperationIndeterminate, false, "保存结果尚未确认，请刷新核对", false)
		if err != nil {
			return op, err
		}
	}
	catalog, err := s.listHooks(ctx, in)
	if err == nil && in.Observed(catalog) {
		op, err = s.store.FinishHookOperation(in, domain.OperationConfirmed, true, "", false)
		return hookSaveCatalog(op, catalog), err
	}
	return op, nil
}

func (s *Server) manageHooks(method string, in domain.HookRequest) (any, error) {
	if in.HarnessID == "" || len(in.HarnessID) > 128 || len(in.WorkspaceID) > 256 || len(in.SessionID) > 256 || len(in.OperationID) > 128 {
		return nil, errors.New("Hook 请求无效")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	if method == "runtime.hooks.list" {
		return s.listHooks(ctx, in)
	}
	if in.OperationID == "" || !in.HookChange.Valid() {
		return nil, errors.New("Hook 操作请求无效")
	}
	s.toolConfigMu.Lock()
	defer s.toolConfigMu.Unlock()
	if method == "runtime.hooks.operation" {
		return s.inspectHookOperation(ctx, in)
	}
	if op, err := s.store.HookOperation(in); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return nil, err
		}
		if op.State == domain.OperationAccepted || op.State == domain.OperationIndeterminate {
			return s.inspectHookOperation(ctx, in)
		}
		return op, nil
	}
	launch, _, err := s.hookContext(in, true)
	if err != nil {
		return nil, err
	}
	r, release, err := s.borrowCatalog(in.HarnessID)
	if err != nil {
		return nil, err
	}
	defer release()
	provider, ok := r.(harness.HookProvider)
	if !ok || !r.Readiness().Capabilities.HookManagement {
		return nil, harness.Unsupported("hook-management")
	}
	catalog, err := provider.Hooks(ctx, launch, "")
	if err != nil {
		return nil, err
	}
	if catalog.Revision != in.Revision || in.Revision == "" {
		return nil, errors.New("配置已变化，请刷新后重新审核")
	}
	targets := in.MatchingEntries(catalog)
	if len(targets) == 0 {
		return nil, errors.New("定义已变化，请刷新后重新审核")
	}
	for _, h := range targets {
		owned := in.WorkspaceID == "" && h.Scope != domain.HookProject || in.WorkspaceID != "" && h.Scope == domain.HookProject
		if !owned || !in.Allowed(h) {
			return nil, errors.New("当前入口不能修改此 Hook，请在所属位置管理")
		}
	}
	if _, fresh, err := s.store.PrepareHookOperation(in); err != nil || !fresh {
		if err != nil {
			return nil, err
		}
		return s.inspectHookOperation(ctx, in)
	}
	result, err := provider.ChangeHook(ctx, launch, in.HookChange)
	if err != nil || !result.Saved {
		state, message := domain.OperationRejected, "保存未确认，请刷新核对"
		if err != nil {
			message = err.Error()
		}
		if err == nil || harness.IsUncertain(err) {
			state = domain.OperationIndeterminate
		}
		return s.store.FinishHookOperation(in, state, false, message, false)
	}
	adopted := s.refreshHookRuntimes(ctx, in.HarnessID)
	message := ""
	if !adopted {
		message = "已保存，部分会话尚未确认配置刷新"
	}
	op, err := s.store.FinishHookOperation(in, domain.OperationConfirmed, true, message, !adopted)
	if in.SessionID == "" {
		op = hookSaveCatalog(op, result.Catalog)
	}
	return op, err
}

func hookSaveCatalog(op domain.HookOperation, catalog domain.HookCatalog) domain.HookOperation {
	if op.Saved && catalog.Revision != "" {
		// A list can fit on its own while the same list plus the selected
		// definitions exceeds a frame. In that case return the receipt alone.
		op.Catalog = &catalog
		if b, err := json.Marshal(op); err != nil || len(b) > protocol.MaxFrame-1024 {
			op.Catalog = nil
		}
	}
	return op
}

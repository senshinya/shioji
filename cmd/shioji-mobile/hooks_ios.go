package main

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

const mobileHookMinor = 46

type mobileHookSlot struct {
	device, harness, workspace, session string
}

type mobileHookBinding struct {
	slot                         mobileHookSlot
	epoch, context, installation string
	revision                     int64
	archived                     bool
}

type mobileHookReceipts struct {
	pending     *domain.HookRequest
	unconfirmed []domain.HookRequest
}

func (r *mobileHookReceipts) remove(in domain.HookRequest) {
	if r.pending != nil && r.pending.Equal(in) {
		r.pending = nil
	}
	r.unconfirmed = slices.DeleteFunc(r.unconfirmed, func(candidate domain.HookRequest) bool { return candidate.Equal(in) })
}

type mobileHooks struct {
	workspace, session, harness            string
	binding                                mobileHookBinding
	generation                             uint64
	receipts                               *mobileHookReceipts
	request                                string
	catalog                                domain.HookCatalog
	loaded, visited                        bool
	query, detail, message                 string
	review, metadata                       bool
	selected                               map[string]bool
	listScroll, detailScroll, reviewScroll ui.ScrollState
}

func (s *mobileHooks) input() domain.HookRequest {
	return domain.HookRequest{HarnessID: s.harness, WorkspaceID: s.workspace, SessionID: s.session}
}

func (s *mobileHooks) currentBinding(a *mobileApp, agent domain.HarnessInfo) mobileHookBinding {
	if agent.ID != s.harness || agent.ID == "" {
		return mobileHookBinding{}
	}
	for _, current := range a.model.Readiness.Harnesses {
		if current.ID == agent.ID {
			agent = current
			break
		}
	}
	if !agent.Capabilities.HookManagement {
		return mobileHookBinding{}
	}
	device := a.model.Snapshot.DeviceID
	installation, _ := json.Marshal(agent.Installation)
	b := mobileHookBinding{slot: mobileHookSlot{device, s.harness, s.workspace, s.session},
		epoch: a.model.ConnectionEpoch, installation: string(installation)}
	if s.workspace == "" {
		return b
	}
	ws := a.workspace(s.workspace)
	if ws.ID == "" || ws.DeviceID != device {
		return mobileHookBinding{}
	}
	b.revision, b.archived = ws.Revision, ws.Archived
	if s.session != "" {
		session := a.sessionByID(s.session)
		if session.WorkspaceID != ws.ID || session.HarnessID != agent.ID {
			return mobileHookBinding{}
		}
		raw, _ := json.Marshal(session.Context)
		b.context, b.archived = session.ThreadID+"\x00"+string(raw), b.archived || session.Archived
	}
	return b
}

func (s *mobileHooks) bind(a *mobileApp, binding mobileHookBinding) {
	if s.binding == binding && s.receipts != nil {
		return
	}
	previous := s.binding
	previous.epoch = binding.epoch
	if s.receipts != nil && previous != binding {
		s.catalog = domain.HookCatalog{}
		if previous.slot != binding.slot {
			s.detail, s.review = "", false
			s.detailScroll, s.reviewScroll, s.listScroll = ui.ScrollState{}, ui.ScrollState{}, ui.ScrollState{}
		}
	}
	s.binding, s.request, s.message = binding, "", ""
	s.generation++
	s.loaded, s.visited, s.catalog.RuntimeKnown = false, false, false
	clear(s.selected)
	if a.hookReceipts == nil {
		a.hookReceipts = make(map[mobileHookSlot]*mobileHookReceipts)
	}
	s.receipts = a.hookReceipts[binding.slot]
	if s.receipts == nil {
		s.receipts = &mobileHookReceipts{}
		a.hookReceipts[binding.slot] = s.receipts
	}
}

func (s *mobileHooks) ready(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo) bool {
	return a.pageCurrent(w) && s.binding.slot.device != "" && a.model.Connected && a.model.WorkerMinor >= mobileHookMinor &&
		s.request == "" && s.currentBinding(a, agent) == s.binding
}

func (s *mobileHooks) call(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo, method string, in domain.HookRequest, apply func(json.RawMessage, error)) bool {
	if !s.ready(a, w, agent) {
		return false
	}
	binding, controller, generation := s.binding, a.controller, s.generation
	s.request = method
	done := func(raw json.RawMessage, err error) {
		if s.generation != generation || s.currentBinding(a, agent) != binding ||
			!a.pageCurrent(w) || controller != a.controller {
			return
		}
		s.request = ""
		apply(raw, err)
		w.Invalidate()
	}
	if a.preview {
		response, err := a.previewHookCall(method, in)
		raw, _ := json.Marshal(response)
		done(raw, err)
		return true
	}
	if controller == nil || !controller.Do(app.Job{Method: method, Input: in, ConnectionEpoch: binding.epoch,
		Apply: func(_ *app.Model, raw json.RawMessage, err error) { done(raw, err) },
	}) {
		s.request = ""
		s.message = "设备操作繁忙，请刷新重试"
		w.Invalidate()
		return false
	}
	w.Invalidate()
	return true
}

func (s *mobileHooks) acceptCatalog(catalog domain.HookCatalog) {
	if s.catalog.Revision != catalog.Revision {
		clear(s.selected)
	}
	s.catalog, s.loaded = catalog, true
}

func (s *mobileHooks) load(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo, reconcile bool) {
	if !s.ready(a, w, agent) {
		return
	}
	s.visited, s.loaded = true, false
	s.call(a, w, agent, "runtime.hooks.list", s.input(), func(raw json.RawMessage, err error) {
		var catalog domain.HookCatalog
		if err != nil || json.Unmarshal(raw, &catalog) != nil {
			s.message = "无法读取 Hooks，显示上次结果。请刷新重试"
			return
		}
		s.acceptCatalog(catalog)
		if reconcile && s.receipts.pending != nil {
			s.receipts.unconfirmed = append(s.receipts.unconfirmed, *s.receipts.pending)
			s.receipts.pending = nil
			clear(s.selected)
			s.message = "上次结果未确认，请核对当前定义"
		}
	})
}

func (s *mobileHooks) refresh(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo) {
	if s.receipts.pending != nil {
		s.operation(a, w, agent, "runtime.hooks.operation", *s.receipts.pending)
		return
	}
	s.message = ""
	s.load(a, w, agent, false)
}

func (s *mobileHooks) writable(a *mobileApp) bool {
	return !s.binding.archived && s.loaded && s.catalog.Revision != "" && s.receipts.pending == nil &&
		s.request == "" && a.model.Connected
}

func (s *mobileHooks) owned(h domain.HookEntry) bool {
	if s.workspace != "" {
		return h.Scope == domain.HookProject
	}
	return h.Scope == domain.HookUser || h.Scope == domain.HookPlugin
}

func (s *mobileHooks) inherited(h domain.HookEntry) bool {
	return s.workspace != "" && (h.Scope == domain.HookUser || h.Scope == domain.HookPlugin)
}

func (s *mobileHooks) change(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo, revision string, change domain.HookChange) {
	if !s.writable(a) || !s.ready(a, w, agent) || revision != s.catalog.Revision ||
		change.Action == domain.HookTrustDefinitions && a.model.WorkerMinor < protocol.HookBatchMinor {
		return
	}
	change.Revision, change.OperationID = revision, domain.NewID()
	if !change.Valid() {
		return
	}
	entries := change.MatchingEntries(s.catalog)
	if len(entries) == 0 || slices.ContainsFunc(entries, func(h domain.HookEntry) bool { return !s.owned(h) || !change.Allowed(h) }) {
		return
	}
	in := s.input()
	in.HookChange = change
	s.receipts.pending = &in
	s.message = ""
	if !s.operation(a, w, agent, "runtime.hooks.change", in) {
		s.receipts.remove(in)
	}
}

func (s *mobileHooks) operation(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo, method string, in domain.HookRequest) bool {
	s.visited = true
	return s.call(a, w, agent, method, in, func(raw json.RawMessage, err error) {
		var remote *protocol.RemoteError
		if errors.As(err, &remote) && (method == "runtime.hooks.change" && remote.Code == "notSent" ||
			method == "runtime.hooks.operation" && remote.Code == "notFound") {
			s.receipts.remove(in)
			// Clear a stale review when a new attempt can be prepared.
			clear(s.selected)
			s.message = "操作未保存，请重新审核"
			s.load(a, w, agent, false)
			return
		}
		var result domain.HookOperation
		if err != nil || json.Unmarshal(raw, &result) != nil || !result.Input.Equal(in) ||
			result.State != domain.OperationConfirmed && result.State != domain.OperationRejected {
			s.message = "结果未确认，可查询原回执"
			s.loaded = false
			return
		}
		s.receipts.remove(in)
		clear(s.selected)
		s.message = result.Message
		if !result.Saved || result.State == domain.OperationRejected {
			s.loaded = false
			if s.message == "" {
				s.message = "保存失败，请刷新后重新审核"
			}
			return
		}
		if result.AdoptionPending && s.message == "" {
			s.message = "已保存，部分会话尚未确认配置刷新"
		}
		if result.Catalog != nil && result.Catalog.Revision != "" && in.Observed(*result.Catalog) && s.session == "" {
			s.acceptCatalog(*result.Catalog)
		} else {
			s.load(a, w, agent, false)
		}
	})
}

func (s *mobileHooks) workspaceView(a *mobileApp, c *ui.Context, w *mygo.Window) {
	var agents []domain.HarnessInfo
	var selected domain.HarnessInfo
	for _, agent := range a.model.Readiness.Harnesses {
		if agent.Configured && agent.Capabilities.HookManagement {
			agents = append(agents, agent)
			if agent.ID == s.harness || selected.ID == "" {
				selected = agent
			}
		}
	}
	s.harness = selected.ID
	s.view(a, c, w, selected, agents, false)
}

func (a *mobileApp) openHooks(parent *mygo.Window, workspaceID, sessionID string, review bool) {
	hooks := mobileHooks{workspace: workspaceID, session: sessionID, review: review}
	title := "Hooks"
	if sessionID != "" {
		title = "会话 Hooks"
	}
	var w *mygo.Window
	w = a.page(title, func(c *ui.Context) {
		ui.Column(c).Fill().Padding(pageTopInset, 0, 0).Background(palette(c).canvas).Children(func() {
			if sessionID == "" {
				hooks.workspaceView(a, c, w)
				return
			}
			session := a.sessionByID(sessionID)
			hooks.harness = session.HarnessID
			var agent domain.HarnessInfo
			for _, candidate := range a.model.Readiness.Harnesses {
				if candidate.ID == hooks.harness {
					agent = candidate
					break
				}
			}
			hooks.view(a, c, w, agent, nil, false)
		})
	})
	a.pushNative(parent, w)
}

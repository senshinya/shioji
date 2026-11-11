package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type Update func(*Model)
type Job struct {
	ConnectionID    string
	ConnectionEpoch string
	Method          string
	Input           any
	Prepare         func() error
	Apply           func(*Model, json.RawMessage, error)
	epoch           string
}
type Controller struct {
	signInForwards    signInForwards
	WorkerSSH         *transport.SSH
	WorkerBinary      string
	WorkerAutoUpdate  bool
	fleet             *deviceFleet
	ExpectedDeviceID  string
	RestoreOperations []string
	Directory         string
	Dial              func(context.Context) (net.Conn, error)
	Jobs              chan Job
	Updates           chan Update
	mu                sync.Mutex
	epoch             string
	invalidate        func()
}

func NewController(dir string) *Controller {
	return &Controller{Directory: dir, Jobs: make(chan Job, 16), Updates: make(chan Update, 256)}
}
func (c *Controller) SetInvalidate(fn func()) { c.mu.Lock(); c.invalidate = fn; c.mu.Unlock() }
func (c *Controller) post(update Update) {
	c.Updates <- update
	c.mu.Lock()
	fn := c.invalidate
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}
func (c *Controller) Do(job Job) bool {
	c.mu.Lock()
	job.epoch = job.ConnectionEpoch
	if job.epoch == "" {
		job.epoch = c.epoch
	}
	c.mu.Unlock()
	select {
	case c.Jobs <- job:
		return true
	default:
		return false
	}
}
func (c *Controller) Select(id string) { c.Do(Job{Method: "$select", Input: id}) }
func executeJob(ctx context.Context, client *protocol.Client, job Job) (json.RawMessage, error) {
	if client == nil || job.epoch == "" || job.epoch != client.Epoch || ctx.Err() != nil {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"}
	}
	if strings.HasPrefix(job.Method, "session.work") && client.Minor < 27 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能读取子 Agent 和后台任务详情"}
	}
	if strings.HasPrefix(job.Method, "runtime.tools.") && client.Minor < 22 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能管理工具服务"}
	}
	if job.Method == "runtime.tools.inspect" && client.Minor < 26 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能查看工作区 MCP"}
	}
	if job.Method == "runtime.tools.change" && client.Minor < 24 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能修改工具服务配置"}
	}
	if job.Method == "device.directories" && client.Minor < 23 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能浏览目录"}
	}
	if strings.HasPrefix(job.Method, "storage.") && client.Minor < 31 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能管理存储"}
	}
	if strings.HasPrefix(job.Method, "runtime.signin.") && client.Minor < 19 {
		return nil, errors.New("更新设备的执行服务后才能在应用内登录")
	}
	if in, ok := job.Input.(protocol.SignInInput); ok && in.Method == domain.SignInBrowser && job.ConnectionID != "" && job.ConnectionID != "local" && client.Minor < 20 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能使用远程浏览器登录"}
	}
	if client.Minor < 21 {
		apiSetup := false
		if in, ok := job.Input.(protocol.SignInInput); ok && strings.HasPrefix(job.Method, "runtime.signin.") {
			apiSetup = apiSetup || in.Method == domain.SignInAPI
		}
		if apiSetup {
			return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能配置 API"}
		}
	}
	if job.Prepare != nil {
		if err := job.Prepare(); err != nil {
			return nil, &protocol.RemoteError{Code: "notSent", Message: "无法保存草稿，操作未发送：" + err.Error()}
		}
	}
	var out json.RawMessage
	err := client.Call(ctx, job.Method, job.Input, &out)
	return out, err
}
func reconcileOperations(ctx context.Context, client *protocol.Client, ids map[string]bool, emit func(Update)) error {
	if client == nil || ctx.Err() != nil {
		return net.ErrClosed
	}
	for id := range ids {
		var op domain.Operation
		err := client.Call(ctx, "operation", struct {
			ID string `json:"id"`
		}{id}, &op)
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && remote.Code == "notFound" {
				delete(ids, id)
				key := id
				emit(func(m *Model) {
					if in, ok := m.Pending[key]; ok {
						m.Reject(in, "未找到已接纳的提交，草稿已保留")
					}
				})
				continue
			}
			return err
		}
		emit(func(m *Model) { m.ApplyOperation(op) })
		if op.State.ReceiptObserved() {
			delete(ids, id)
		}
	}
	return nil
}

// Pages may overlap writes. Replay every event after the first page barrier before
// publishing the assembled history; a later page cannot vouch for an earlier page.
func hydrateSession(ctx context.Context, client *protocol.Client, id string, generation ...string) (domain.SessionDetail, error) {
	var all domain.SessionDetail
	var after, first int64
	turns := map[string]domain.Turn{}
	requests := map[string]domain.PendingRequest{}
	gen := ""
	if len(generation) > 0 {
		gen = generation[0]
	}
	for {
		var page domain.SessionDetail
		if err := client.Call(ctx, "session", protocol.SessionQuery{ID: id, After: after, Limit: 100}, &page); err != nil {
			return all, err
		}
		if first == 0 {
			first = page.Sequence
		}
		all.Session = page.Session
		all.Queue = page.Queue
		all.Plan = page.Plan
		all.Works = page.Works
		all.WorksTruncated = page.WorksTruncated
		all.Sequence = page.Sequence
		all.Items = append(all.Items, page.Items...)
		for _, t := range page.Turns {
			turns[t.ID] = t
		}
		for _, r := range page.Requests {
			requests[r.ID] = r
		}
		if !page.HasMore {
			break
		}
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].Order <= after {
			return all, errors.New("history page did not advance")
		}
		after = page.Items[len(page.Items)-1].Order
	}
	for _, t := range turns {
		all.Turns = append(all.Turns, t)
	}
	for _, r := range requests {
		all.Requests = append(all.Requests, r)
	}
	barrier := all.Sequence
	all.Sequence = first
	projection := NewModel()
	projection.Sequence = first
	projection.Details[id] = all
	projection.Snapshot.Sessions = []domain.Session{all.Session}
	for projection.Sequence < barrier {
		var page protocol.EventPage
		if err := client.Call(ctx, "events", protocol.EventsQuery{After: projection.Sequence, Generation: gen}, &page); err != nil {
			return all, err
		}
		if len(page.Events) == 0 {
			return all, errors.New("history barrier requires missing journal events")
		}
		for _, e := range page.Events {
			if e.Sequence > barrier {
				break
			}
			if err := projection.ApplyEvent(e); err != nil {
				return all, err
			}
		}
	}
	all = projection.Details[id]
	all.Sequence = barrier
	sort.Slice(all.Items, func(i, j int) bool { return all.Items[i].Order < all.Items[j].Order })
	sort.Slice(all.Requests, func(i, j int) bool { return all.Requests[i].ID < all.Requests[j].ID })
	sort.Slice(all.Turns, func(i, j int) bool {
		a, b := all.Turns[i].StartedAt, all.Turns[j].StartedAt
		if a != nil && b != nil && !a.Equal(*b) {
			return a.Before(*b)
		}
		return all.Turns[i].ID < all.Turns[j].ID
	})
	return all, nil
}
func (c *Controller) Run(ctx context.Context) {
	if c.fleet != nil {
		c.runDevices(ctx)
		return
	}
	var client *protocol.Client
	var generation string
	var sequence int64
	var selected string
	history := map[string]historyLoadPhase{}
	visible := []string{}
	var lastReadiness time.Time
	var eventResults <-chan eventResult
	var stopEvents context.CancelFunc
	unconfirmed := map[string]bool{}
	for _, id := range c.RestoreOperations {
		unconfirmed[id] = true
	}
	defer func() {
		if stopEvents != nil {
			stopEvents()
		}
		if client != nil {
			client.Close()
		}
	}()
	readinessQuery := func() protocol.ReadinessQuery {
		var ids []string
		for _, id := range append([]string{selected}, visible...) {
			if id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
			if len(ids) == 2 {
				break
			}
		}
		return protocol.ReadinessQuery{SessionIDs: ids}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastAttempt := time.Time{}
	var candidate *domain.WorkerBuild
	var candidateChecked bool
	var candidateError error
	var service *WorkerService
	var background *domain.BackgroundService
	var lastUpdateAttempt, updateStarted time.Time
	var lastBackgroundCheck time.Time
	updateIntent := updateNotRequested
	publishService := func(state *WorkerService) {
		state.Background = background
		service = state
		copy := *state
		c.post(func(m *Model) { m.WorkerService = &copy })
	}
	type backgroundResult struct {
		state  domain.BackgroundService
		err    error
		epoch  string
		action string
	}
	backgroundResults := make(chan backgroundResult, 1)
	backgroundBusy := false
	var pendingBackground *struct{ action, epoch string }
	checkBackground := func(action string) {
		if c.WorkerBinary == "" {
			return
		}
		epoch := ""
		if client != nil {
			epoch = client.Epoch
		}
		if backgroundBusy {
			if action != "status" {
				pendingBackground = &struct{ action, epoch string }{action, epoch}
			}
			return
		}
		backgroundBusy = true
		lastBackgroundCheck = time.Now()
		if action != "status" && service != nil {
			label, phase := "正在启用后台服务", "enabling"
			if action == "unregister" {
				label, phase = "正在关闭后台服务", "disabling"
			}
			background = &domain.BackgroundService{Format: 1, State: phase, Message: label}
			copy := *service
			publishService(&copy)
		}
		target := client
		go func() {
			callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			var state domain.BackgroundService
			var err error
			managed := target != nil && target.Worker != nil && target.Worker.Managed
			if action == "unregister" && managed {
				if target.Minor < 17 {
					err = errors.New("请先更新执行服务，再关闭后台运行")
				} else {
					err = target.Call(callCtx, "worker.background.disable", nil, &state)
					if err != nil { // Unregister closes the old IPC owner; query status, never resend it.
						cause := err
						if c.WorkerSSH != nil {
							state, err = c.WorkerSSH.RuntimeService(callCtx, c.WorkerBinary, false)
						} else {
							state, err = transport.RuntimeService(callCtx, c.Directory, c.WorkerBinary, false)
						}
						if err == nil && state.State != "notRegistered" && state.State != "notInstalled" {
							state.Message = cause.Error()
						}
					}
					if err == nil && (state.State == "notRegistered" || state.State == "notInstalled") {
						err = transport.EnsureDetachedWorker(callCtx, c.Directory, c.WorkerBinary, target.DeviceID, c.WorkerSSH)
					}
				}
			} else {
				if c.WorkerSSH != nil {
					if action == "unregister" {
						state, err = c.WorkerSSH.DisableRuntimeService(callCtx, c.WorkerBinary)
					} else {
						state, err = c.WorkerSSH.RuntimeService(callCtx, c.WorkerBinary, action == "register")
					}
				} else {
					if action == "unregister" {
						state, err = transport.DisableRuntimeService(callCtx, c.Directory, c.WorkerBinary)
					} else {
						state, err = transport.RuntimeService(callCtx, c.Directory, c.WorkerBinary, action == "register")
					}
				}
			}
			select {
			case backgroundResults <- backgroundResult{state, err, epoch, action}:
			case <-ctx.Done():
			}
		}()
	}

	stageCandidate := func(force bool) {
		if c.WorkerBinary == "" || candidateChecked && !force {
			return
		}
		candidateChecked = true
		stageCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		var info domain.WorkerBuild
		var err error
		if c.WorkerSSH != nil {
			info, err = transport.InspectWorker(stageCtx, c.WorkerBinary)
			if force && err == nil {
				info, err = c.WorkerSSH.StageWorker(stageCtx, c.WorkerBinary)
			}
		} else {
			info, err = transport.StageWorker(stageCtx, c.Directory, c.WorkerBinary)
		}
		candidateError = err
		if err == nil {
			candidate = &info
		}
	}
	tryWorkerUpdate := func(explicit bool) (bool, error) {
		if c.WorkerBinary == "" {
			return false, nil
		}
		if explicit {
			if c.WorkerSSH != nil && (client == nil || client.DeviceID != c.ExpectedDeviceID) {
				return false, errors.New("请先重新连接并确认设备身份，再更新执行服务")
			}
			updateIntent = updateWaitingForIdle
			stageCandidate(true)
		}
		if candidateError != nil {
			state := newWorkerService(nil, candidate)
			if client != nil {
				state.Build = client.Worker
			}
			state.Phase = WorkerUpdateFailed
			state.Message = candidateError.Error()
			publishService(state)
			return false, nil
		}
		if client == nil {
			if explicit && candidate != nil && c.WorkerSSH == nil && (background == nil || background.State != "enabled" && background.State != "requiresApproval") {
				path, err := transport.StagedWorkerPath(c.Directory, candidate.BuildID)
				if err == nil {
					_, err = transport.EnsureLocalWorker(ctx, c.Directory, path, "")
				}
				if err != nil {
					state := newWorkerService(nil, candidate)
					state.Phase = WorkerUpdateFailed
					state.Message = err.Error()
					publishService(state)
				}
			}
			return false, nil
		}
		state := newWorkerService(client.Worker, candidate)
		if state.Phase != WorkerAvailable {
			publishService(state)
			return false, nil
		}
		if !explicit && updateIntent == updateNotRequested && !state.Automatic() {
			return false, nil
		}
		lastUpdateAttempt = time.Now()
		updateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var result domain.WorkerUpdateResult
		updateIntent = updateAwaitingReceipt
		err := client.Call(updateCtx, "worker.update", domain.WorkerReplacement{ExpectedBuildID: state.Build.BuildID, BuildID: state.Available.BuildID}, &result)
		if err != nil {
			state.Phase = WorkerUpdateFailed
			state.Message = err.Error()
			publishService(state)
			return false, err
		}
		if !result.Ready {
			updateIntent = updateWaitingForIdle
			state.Phase = WorkerWaiting
			state.Message = result.Reason
			publishService(state)
			return false, nil
		}
		state.Phase = WorkerUpdating
		state.Message = "正在更新并重新连接，会话和草稿已保留"
		updateStarted = time.Now()
		publishService(state)
		return true, nil
	}
	disconnected := func() {
		if stopEvents != nil {
			stopEvents()
			stopEvents = nil
		}
		eventResults = nil
		if client != nil {
			client.Close()
		}
		client = nil
		for id, phase := range history {
			history[id] = phase.invalidated()
		}
		c.mu.Lock()
		c.epoch = ""
		c.mu.Unlock()
		c.post(func(m *Model) {
			m.Connected = false
			m.ConnectionEpoch = ""
		})
	}
	fetchDetail := func() error {
		ids := append([]string{selected}, visible...)
		for _, id := range ids {
			if id == "" || history[id].loaded() {
				continue
			}
			all, err := hydrateSession(ctx, client, id, generation)
			if err != nil {
				var remote *protocol.RemoteError
				if errors.As(err, &remote) {
					history[id] = history[id].failed()
					c.post(func(m *Model) { m.Error = err.Error() })
					continue
				}
				return err
			}
			collapseHistory := history[id].opening()
			history[id] = historyLoaded
			c.post(func(m *Model) {
				m.Details[id] = all
				if collapseHistory {
					m.FoldToolHistory[id] = true
				}
				for i, s := range m.Snapshot.Sessions {
					if s.ID == all.Session.ID && s.Revision <= all.Session.Revision {
						m.Snapshot.Sessions[i] = all.Session
					}
				}
			})
		}
		return nil
	}

	applyPage := func(page protocol.EventPage) bool {
		if page.Generation != generation {
			return false
		}
		if len(page.Events) == 0 {
			return true
		}
		events := page.Events
		sequence = events[len(events)-1].Sequence
		for _, e := range events {
			if e.Operation != nil && (e.Operation.State.ReceiptObserved()) {
				delete(unconfirmed, e.Operation.ID)
			}
		}
		c.post(func(m *Model) {
			for _, e := range events {
				if err := m.ApplyEvent(e); err != nil {
					m.Error, m.Connected, m.ConnectionEpoch = err.Error(), false, ""
					return
				}
			}
		})
		return true
	}
	checkBackground("status")
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-backgroundResults:
			backgroundBusy = false
			epoch := ""
			if client != nil {
				epoch = client.Epoch
			}
			if result.epoch != epoch {
				checkBackground("status")
				continue
			}
			if result.err != nil {
				result.state.Format = 1
				result.state.State = "failed"
				result.state.Message = result.err.Error()
			}
			background = &result.state
			state := newWorkerService(nil, candidate)
			if service != nil {
				copy := *service
				state = &copy
			}
			publishService(state)
			if pendingBackground != nil {
				requested := pendingBackground
				pendingBackground = nil
				if requested.epoch == epoch {
					checkBackground(requested.action)
				}
			}
		case result := <-eventResults:
			if result.err != nil || !applyPage(result.page) {
				disconnected()
			}
		case job := <-c.Jobs:
			if job.Method == "$background.status" || job.Method == "$background.enable" || job.Method == "$background.disable" {
				if job.Method != "$background.status" && (client == nil || job.epoch == "" || job.epoch != client.Epoch) {
					continue
				}
				action := "status"
				if job.Method == "$background.enable" {
					action = "register"
				}
				if job.Method == "$background.disable" {
					action = "unregister"
				}
				checkBackground(action)
				continue
			}
			if job.Method == "$worker.check" {
				if client == nil || job.epoch != client.Epoch {
					continue
				}
				if service != nil && service.Phase == WorkerUpdating {
					continue
				}
				candidateChecked = false
				stageCandidate(false)
				callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				var current domain.WorkerBuild
				err := client.Call(callCtx, "worker.info", nil, &current)
				cancel()
				next := newWorkerService(&current, candidate)
				if err != nil {
					next.Phase = WorkerUpdateFailed
					next.Message = err.Error()
				}
				if candidateError != nil {
					next.Phase = WorkerUpdateFailed
					next.Message = candidateError.Error()
				}
				publishService(next)
				continue
			}
			if job.Method == "$worker.update" {
				if service != nil && service.Phase == WorkerUpdating {
					continue
				}
				if client != nil && job.epoch != "" && job.epoch != client.Epoch {
					continue
				}
				restart, err := tryWorkerUpdate(true)
				if restart || err != nil {
					disconnected()
					lastAttempt = time.Time{}
				}
				continue
			}
			if job.Method == "$reconnect" {
				disconnected()
				lastAttempt = time.Time{}
				continue
			}
			if job.Method == "$visible" {
				previous := map[string]bool{}
				for _, id := range visible {
					previous[id] = true
				}
				visible = append([]string(nil), job.Input.([]string)...)
				lastReadiness = time.Time{}
				for _, id := range visible {
					if !previous[id] {
						history[id] = historyOpening
					}
				}
				if client != nil {
					if err := fetchDetail(); err != nil {
						disconnected()
					}
				}
				continue
			}
			if job.Method == "$select" {
				selected = job.Input.(string)
				lastReadiness = time.Time{}
				history[selected] = historyUnloaded
				if selected != "" {
					history[selected] = historyOpening
				}
				if client != nil {
					if err := fetchDetail(); err != nil {
						disconnected()
					}
				}
				continue
			}
			var opID string
			if in, ok := job.Input.(protocol.SteerInput); ok {
				opID = in.Input.OperationID
				unconfirmed[opID] = true
			}
			if in, ok := job.Input.(domain.Submission); ok {
				opID = in.OperationID
				unconfirmed[opID] = true
			}
			timeout := 10 * time.Second
			if strings.HasPrefix(job.Method, "runtime.tools.") {
				timeout = 45 * time.Second
			}
			if job.Method == "session.reconcile" {
				timeout = 25 * time.Second
			}
			if job.Method == "session.fork" || job.Method == "session.restart" {
				timeout = 45 * time.Second
			}
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			out, err := executeJob(callCtx, client, job)
			cancel()
			var remote *protocol.RemoteError
			if err != nil && errors.As(err, &remote) {
				delete(unconfirmed, opID)
			}
			if job.Apply != nil {
				c.post(func(m *Model) { job.Apply(m, out, err) })
			}
			if err != nil && !errors.As(err, &remote) {
				disconnected()
			}
		case <-ticker.C:
			if client == nil {
				if service != nil && service.Phase == WorkerUpdating && !updateStarted.IsZero() && time.Since(updateStarted) > 35*time.Second {
					state := *service
					state.Phase = WorkerUpdateFailed
					state.Message = "更新后未能重新连接，可重试启动；原数据和更新前备份已保留"
					publishService(&state)
				}
				if time.Since(lastAttempt) < time.Second {
					continue
				}
				lastAttempt = time.Now()
				var err error
				client, err = protocol.Dial(ctx, c.dial, c.ExpectedDeviceID)
				if err != nil {
					client = nil
					var network *net.OpError
					if c.Dial == nil && errors.As(err, &network) && network.Op == "dial" {
						continue
					}
					var legacy *protocol.LegacyWorkerError
					if errors.As(err, &legacy) {
						publishService(&WorkerService{Phase: WorkerLegacy, Message: err.Error(), Legacy: &domain.LegacyWorkerInfo{DeviceID: legacy.DeviceID, Major: legacy.Major, Minor: legacy.Minor}})
					}
					c.post(func(m *Model) { m.Error = err.Error() })
					continue
				}
				var snapshot domain.Snapshot
				var ready domain.Readiness
				if client.Call(ctx, "snapshot", nil, &snapshot) != nil || client.Call(ctx, "readiness", readinessQuery(), &ready) != nil {
					disconnected()
					continue
				}
				if snapshot.DeviceID != client.DeviceID {
					c.post(func(m *Model) { m.Error = "worker snapshot identity mismatch" })
					disconnected()
					continue
				}
				c.ExpectedDeviceID = client.DeviceID
				generation = snapshot.Generation
				sequence = snapshot.Sequence
				c.mu.Lock()
				c.epoch = client.Epoch
				c.mu.Unlock()
				minor := client.Minor
				stageCandidate(false)
				state := newWorkerService(client.Worker, candidate)
				if state.Phase == WorkerCurrent {
					updateIntent = updateNotRequested
				}
				if state.Phase == WorkerAvailable && updateIntent == updateWaitingForIdle {
					state.Phase = WorkerWaiting
					state.Message = "等待空闲后更新，会话和队列已保留"
				}
				if state.Phase == WorkerAvailable && updateIntent == updateAwaitingReceipt {
					state.Phase = WorkerUpdateFailed
					state.Message = "上次更新回执未确认，请重新检查后再决定是否重试"
				}
				if candidateError != nil {
					state.Phase = WorkerUpdateFailed
					state.Message = candidateError.Error()
				}
				publishService(state)
				checkBackground("status")
				updateStarted = time.Time{}
				epoch := client.Epoch
				c.post(func(m *Model) {
					m.WorkerMinor = minor
					m.Connected = true
					m.ConnectionEpoch = epoch
					m.Readiness = ready
					m.ApplySnapshot(snapshot)
				})
				for id, phase := range history {
					history[id] = phase.invalidated()
				}
				if err := reconcileOperations(ctx, client, unconfirmed, c.post); err != nil {
					disconnected()
					continue
				}
				if client.Minor >= 1 {
					eventResults, stopEvents = c.observeEvents(ctx, snapshot.DeviceID, generation, sequence)
				}
			}
			if time.Since(lastBackgroundCheck) > 30*time.Second {
				checkBackground("status")
			}
			if time.Since(lastReadiness) > 5*time.Second {
				started := time.Now()
				var ready domain.Readiness
				if err := client.Call(ctx, "readiness", readinessQuery(), &ready); err != nil {
					disconnected()
					continue
				}
				lastReadiness = time.Now()
				sample := &DeviceTiming{Milliseconds: float64(time.Since(started)) / float64(time.Millisecond), ObservedAt: time.Now()}
				c.post(func(m *Model) { m.Readiness = ready; m.ConnectionTiming = sample })
			}
			if (c.WorkerAutoUpdate && service.Automatic() || updateIntent == updateWaitingForIdle && service != nil && service.Phase == WorkerWaiting) && time.Since(lastUpdateAttempt) > 5*time.Second {
				restart, err := tryWorkerUpdate(false)
				if restart || err != nil {
					disconnected()
					lastAttempt = time.Time{}
					continue
				}
			}
			// Older workers retain their existing polling path until upgraded.
			if eventResults == nil {
				var page protocol.EventPage
				if err := client.Call(ctx, "events", protocol.EventsQuery{After: sequence, Generation: generation}, &page); err != nil || !applyPage(page) {
					disconnected()
					continue
				}
			}
			if err := fetchDetail(); err != nil {
				disconnected()
			}
		}
	}
}

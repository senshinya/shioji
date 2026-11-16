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
	"shioji/internal/portforward"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
)

type Update func(*Model)
type Job struct {
	Context         context.Context
	ConnectionID    string
	ConnectionEpoch string
	Method          string
	Input           any
	Prepare         func() error
	Apply           func(*Model, json.RawMessage, error)
	epoch           string
}
type Controller struct {
	forwardSnapshot   []portforward.Mapping
	forwardsStarted   bool
	forwards          *portforward.Manager
	paths             *devicePaths
	resolveSSH        func(transport.SSH) (transport.SSH, error)
	Tailnet           tailnet.Connection
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
	runContext        context.Context
	deviceWG          sync.WaitGroup
	networkWG         sync.WaitGroup
	epoch             string
	invalidate        func()
}

func NewController(dir string) *Controller {
	return &Controller{Directory: dir, Jobs: make(chan Job, 16), Updates: make(chan Update, 256)}
}
func (c *Controller) SetInvalidate(fn func()) { c.mu.Lock(); c.invalidate = fn; c.mu.Unlock() }
func (c *Controller) post(update Update) {
	c.mu.Lock()
	ctx := c.runContext
	fn := c.invalidate
	c.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case c.Updates <- update:
	case <-ctx.Done():
		return
	}
	if fn != nil {
		fn()
	}
}
func (c *Controller) Do(job Job) bool {
	c.mu.Lock()
	if c.runContext != nil && c.runContext.Err() != nil {
		c.mu.Unlock()
		return false
	}
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
	if job.Method == "session.completion.read" && client.Minor < 39 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能同步会话处理状态"}
	}
	if (strings.HasPrefix(job.Method, "worker.maintenance.") || job.Method == "worker.update.diagnose" || job.Method == "worker.processes.inspect") && (client.Worker == nil || (!client.Worker.Maintenance && !client.Worker.ForceRecovery)) {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "此执行服务尚不支持后台环境处理"}
	}
	if (strings.HasPrefix(job.Method, "archive.") || job.Method == "session.delete" || job.Method == "workspace.delete") && client.Minor < 40 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能管理归档保留时间和彻底删除"}
	}
	if historyReadMethod(job.Method) && client.Minor < 37 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能按需加载历史"}
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
	if (job.Method == "device.health" || job.Method == "device.keepAwake") && client.Minor < 38 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能查看负载和保持唤醒"}
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
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.runContext = ctx
	c.mu.Unlock()
	defer func() {
		cancel()
		c.networkWG.Wait()
		if c.forwards != nil {
			c.forwards.Close()
		}
	}()
	if c.fleet != nil {
		c.runDevices(ctx)
		return
	}
	var client *protocol.Client
	var generation string
	var sequence int64
	var selected string
	var logicalEpoch string
	var pathChanges <-chan struct{}
	if c.paths != nil {
		pathChanges = c.paths.changed
		c.networkWG.Add(1)
		go func() { defer c.networkWG.Done(); c.paths.run(ctx) }()
	}
	history := map[string]historyLoadPhase{}
	visible := []string{}
	var lastReadiness time.Time
	var stopHealth context.CancelFunc
	var eventResults <-chan eventResult
	var stopEvents context.CancelFunc
	unconfirmed := map[string]bool{}
	for _, id := range c.RestoreOperations {
		unconfirmed[id] = true
	}
	defer func() {
		if stopHealth != nil {
			stopHealth()
		}
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
		ssh := c.currentSSH()
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
						if ssh != nil {
							state, err = ssh.RuntimeService(callCtx, c.WorkerBinary, false)
						} else {
							state, err = transport.RuntimeService(callCtx, c.Directory, c.WorkerBinary, false)
						}
						if err == nil && state.State != "notRegistered" && state.State != "notInstalled" {
							state.Message = cause.Error()
						}
					}
					if err == nil && (state.State == "notRegistered" || state.State == "notInstalled") {
						err = transport.EnsureDetachedWorker(callCtx, c.Directory, c.WorkerBinary, target.DeviceID, ssh)
					}
				}
			} else {
				if ssh != nil {
					if action == "unregister" {
						state, err = ssh.DisableRuntimeService(callCtx, c.WorkerBinary)
					} else {
						state, err = ssh.RuntimeService(callCtx, c.WorkerBinary, action == "register")
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
		if c.currentSSH() != nil {
			info, err = transport.InspectWorker(stageCtx, c.WorkerBinary)
			if force && err == nil {
				info, err = c.currentSSH().StageWorker(stageCtx, c.WorkerBinary)
			}
		} else {
			info, err = transport.StageWorker(stageCtx, c.Directory, c.WorkerBinary)
		}
		candidateError = err
		if err == nil {
			candidate = &info
		}
	}
	tryWorkerUpdate := func(explicit bool, policy ...string) (bool, error) {
		if c.WorkerBinary == "" {
			return false, nil
		}
		if explicit {
			if c.currentSSH() != nil && (c.ExpectedDeviceID == "" || client != nil && client.DeviceID != c.ExpectedDeviceID) {
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
			if explicit && candidate != nil && candidate.Lifecycle == 1 && c.ExpectedDeviceID != "" {
				var known *domain.WorkerBuild
				if service != nil {
					known = service.Build
				}
				observer := c.upgradeObserver(c.ExpectedDeviceID, known)
				selectedPolicy := "when-idle"
				if len(policy) > 0 {
					selectedPolicy = policy[0]
				}
				state, e := observer.update(ctx, nil, candidate, true, selectedPolicy)
				if state != nil {
					publishService(state)
				}
				return false, e
			}
			if explicit && candidate != nil && c.currentSSH() == nil && (background == nil || background.State != "enabled" && background.State != "requiresApproval") {
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
		if client.Worker != nil && (client.Worker.Lifecycle == 1 || candidate != nil && candidate.Lifecycle == 1) {
			observer := c.upgradeObserver(client.DeviceID, client.Worker)
			prior, e := observer.load()
			if e != nil {
				return false, e
			}
			initial := newWorkerService(client.Worker, candidate)
			if prior == nil && initial.Phase != WorkerAvailable {
				publishService(initial)
				return false, nil
			}
			if prior == nil && !explicit && updateIntent == updateNotRequested && !initial.Automatic() {
				publishService(initial)
				return false, nil
			}
			selectedPolicy := "when-idle"
			if len(policy) > 0 {
				selectedPolicy = policy[0]
			}
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			state, e := observer.update(call, client, candidate, explicit, selectedPolicy)
			if state != nil {
				publishService(state)
			}
			lastUpdateAttempt = time.Now()
			updateIntent = updateAwaitingReceipt
			return false, e
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
		err := client.Call(updateCtx, "worker.update", domain.WorkerReplacement{ExpectedBuildID: state.Build.BuildID, BuildID: state.Available.BuildID, Explicit: explicit}, &result)
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
	observeWorkerBuild := func() {
		if client.Worker != nil && (client.Worker.Lifecycle == 1 || candidate != nil && candidate.Lifecycle == 1) {
			observer := c.upgradeObserver(client.DeviceID, client.Worker)
			if prior, e := observer.load(); e != nil {
				state := newWorkerService(client.Worker, candidate)
				state.Phase = WorkerUpdateFailed
				state.Message = e.Error()
				publishService(state)
				return
			} else if prior != nil {
				call, cancel := context.WithTimeout(ctx, 3*time.Second)
				state, e := observer.observe(call, client, candidate)
				cancel()
				if state != nil {
					if e != nil {
						state.Message = e.Error()
					}
					publishService(state)
				}
				return
			}
		}
		state := newWorkerService(client.Worker, candidate)
		if state.Phase == WorkerCurrent || state.Phase == WorkerNewer {
			updateIntent = updateNotRequested
			updateStarted = time.Time{}
		}
		if state.Phase == WorkerAvailable && updateIntent == updateWaitingForIdle {
			state.Phase = WorkerWaiting
			state.Message = "等待空闲后更新，会话和队列已保留"
			if service != nil && service.Phase == WorkerWaiting && service.Message != "" {
				state.Message = service.Message
			}
		}
		if state.Phase == WorkerAvailable && updateIntent == updateAwaitingReceipt {
			if service != nil && service.Phase == WorkerUpdating && !updateStarted.IsZero() && time.Since(updateStarted) < 35*time.Second {
				state.Phase = WorkerUpdating
				state.Message = "正在更新并重新连接，会话和草稿已保留"
			} else {
				state.Phase = WorkerUpdateFailed
				state.Message = "上次更新回执未确认，请重新检查后再决定是否重试"
			}
		}
		if candidateError != nil {
			state.Phase = WorkerUpdateFailed
			state.Message = candidateError.Error()
		}
		publishService(state)
	}
	lastDetachedService := time.Time{}
	observeDetachedService := func() {
		if time.Since(lastDetachedService) < 3*time.Second {
			return
		}
		lastDetachedService = time.Now()
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		device := c.ExpectedDeviceID
		if device == "" && c.currentSSH() == nil {
			if op, e := supervisor.Query(call, c.Directory, ""); e == nil {
				device = op.DeviceID
			}
		}
		if device == "" {
			return
		}
		var known *domain.WorkerBuild
		if service != nil {
			known = service.Build
		}
		observer := c.upgradeObserver(device, known)
		prior, e := observer.load()
		if e != nil || prior == nil {
			return
		}
		state, e := observer.observe(call, nil, candidate)
		if state != nil {
			if e != nil {
				state.Message = e.Error()
			}
			publishService(state)
		}
	}
	var recoverPath func() bool
	initialHistory := map[string]context.CancelFunc{}
	cancelHistory := func(wanted map[string]bool) {
		for id, cancel := range initialHistory {
			if !wanted[id] {
				cancel()
				delete(initialHistory, id)
			}
		}
	}
	defer cancelHistory(nil)
	disconnected := func() {
		cancelHistory(nil)
		if stopHealth != nil {
			stopHealth()
			stopHealth = nil
		}
		if c.paths != nil {
			c.paths.record(c.paths.active(), 0, time.Now(), net.ErrClosed)
			c.publishPaths()
			if recoverPath != nil && recoverPath() {
				return
			}
		}
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
		if c.paths == nil {
			logicalEpoch = ""
			c.epoch = ""
		}
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
			if client.Minor >= 37 {
				if cancel := initialHistory[id]; cancel != nil {
					cancel()
				}
				opening := history[id].opening()
				history[id] = historyLoaded
				epoch := logicalEpoch
				if epoch == "" {
					epoch = client.Epoch
				}
				initialHistory[id] = c.queueInitialHistory(ctx, id, epoch, generation, opening)
				continue
			}
			read := &HistoryRead{Pending: true}
			epoch := logicalEpoch
			if epoch == "" {
				epoch = client.Epoch
			}
			c.post(func(m *Model) {
				if m.ConnectionEpoch == epoch && m.ownsSession(id) {
					read.Uncached = len(m.Details[id].Items) == 0
					m.HistoryReads[id] = read
				}
			})
			all, err := hydrateSession(ctx, client, id, generation)
			if err != nil {
				c.post(func(m *Model) {
					if m.ConnectionEpoch == epoch && m.ownsSession(id) && m.HistoryReads[id] == read {
						read.Pending, read.Error = false, err.Error()
					}
				})
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
				if !m.ownsSession(id) || m.ConnectionEpoch != epoch || m.HistoryReads[id] != read {
					return
				}
				read.Pending = false
				delete(m.HistoryReads, id)
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
	recoverPath = func() bool {
		if c.paths == nil || client == nil || backgroundBusy {
			return false
		}
		id := c.paths.preferred(time.Now())
		if id == "" || id == c.paths.active() {
			return false
		}
		c.paths.preparing(id)
		c.publishPaths()
		next, snapshot, ready, gap, err := c.preparePath(ctx, id, generation, sequence, readinessQuery())
		// Reconcile through the candidate before discarding the old path. Buffer
		// resulting model updates until the same-generation snapshot is committed.
		queried := map[string]bool{}
		for id, pending := range unconfirmed {
			queried[id] = pending
		}
		var receipts []Update
		if err == nil {
			call, cancel := context.WithTimeout(ctx, 8*time.Second)
			err = reconcileOperations(call, next, queried, func(u Update) { receipts = append(receipts, u) })
			cancel()
		}
		if err != nil {
			if next != nil {
				next.Close()
			}
			c.paths.preparing("")
			c.paths.record(id, 0, time.Now(), err)
			c.publishPaths()
			return false
		}
		if stopEvents != nil {
			stopEvents()
			stopEvents = nil
		}
		eventResults = nil
		if len(gap) > 0 {
			applyPage(protocol.EventPage{Generation: generation, Events: gap})
		}
		old := client
		client = next
		observeWorkerBuild()
		sequence = snapshot.Sequence
		unconfirmed = queried
		c.paths.activate(id, time.Now())
		c.publishPaths()
		epoch, minor := logicalEpoch, client.Minor
		c.post(func(m *Model) {
			m.Connected = true
			m.ConnectionEpoch = epoch
			m.WorkerMinor = minor
			m.Readiness = ready
			m.ApplySnapshot(snapshot)
		})
		for _, update := range receipts {
			c.post(update)
		}
		old.Close()
		if client.Minor >= 1 {
			eventResults, stopEvents = c.observeEvents(ctx, snapshot.DeviceID, generation, sequence)
			if stopHealth != nil {
				stopHealth()
				stopHealth = nil
			}
			if client.Minor >= 38 {
				stopHealth = c.observeDeviceHealth(ctx, snapshot.DeviceID, logicalEpoch)
			}
		}
		return true
	}
	checkBackground("status")
	for {
		jobs := c.Jobs
		if c.paths != nil && client == nil {
			jobs = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-pathChanges:
			c.publishPaths()
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
		case job := <-jobs:
			if job.Method == "$worker.recovery" {
				c.runWorkerRecoveryJob(ctx, job)
				continue
			}
			if isForwardJob(job.Method) {
				c.runForwardJob(ctx, job)
				continue
			}
			if c.paths != nil && client != nil && job.epoch == logicalEpoch {
				job.epoch = client.Epoch
			}
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
			if job.Method == "$worker.maintenance.inspect" {
				var out json.RawMessage
				var err error
				if client == nil || job.epoch != client.Epoch || client.Worker == nil || !client.Worker.Maintenance {
					err = &protocol.RemoteError{Code: "notSent", Message: "请重新连接并检查执行服务能力"}
				} else {
					stageCandidate(true)
					if candidateError != nil {
						err = &protocol.RemoteError{Code: "notSent", Message: candidateError.Error()}
					} else {
						callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
						request := job
						request.Method = "worker.update.diagnose"
						out, err = executeJob(callCtx, client, request)
						cancel()
					}
				}
				if job.Apply != nil {
					c.post(func(m *Model) { job.Apply(m, out, err) })
				}
				continue
			}
			if job.Method == "$worker.check" {
				if client == nil {
					lastDetachedService = time.Time{}
					observeDetachedService()
					lastAttempt = time.Time{}
					continue
				}
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
			if job.Method == "$worker.update.cancel" {
				if service != nil && service.OperationID != "" && c.ExpectedDeviceID != "" {
					observer := c.upgradeObserver(c.ExpectedDeviceID, service.Build)
					_, err := observer.control(ctx, "cancel", service.OperationID)
					if err != nil {
						service.Message = err.Error()
						publishService(service)
					} else {
						if client != nil {
							observeWorkerBuild()
						} else {
							lastDetachedService = time.Time{}
							observeDetachedService()
						}
					}
				}
				continue
			}
			if job.Method == "$worker.update" || job.Method == "$worker.update.immediate" {
				if service != nil && service.Phase == WorkerUpdating {
					continue
				}
				if client != nil && job.epoch != "" && job.epoch != client.Epoch {
					continue
				}
				if job.Method == "$worker.update.immediate" && service != nil && service.OperationID != "" && service.OperationPhase == "waiting" {
					observer := c.upgradeObserver(c.ExpectedDeviceID, service.Build)
					if _, err := observer.control(ctx, "cancel", service.OperationID); err != nil {
						service.Message = err.Error()
						publishService(service)
						continue
					}
					observer = c.upgradeObserver(c.ExpectedDeviceID, service.Build)
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) {
						state, e := observer.observe(ctx, client, candidate)
						if e != nil {
							break
						}
						if state.OperationPhase == "failed" {
							publishService(state)
							break
						}
						time.Sleep(20 * time.Millisecond)
					}
				}
				policy := "when-idle"
				if job.Method == "$worker.update.immediate" {
					policy = "immediate"
				}
				restart, err := tryWorkerUpdate(true, policy)
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
				wanted := map[string]bool{selected: true}
				for _, id := range visible {
					wanted[id] = true
				}
				cancelHistory(wanted)
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
				wanted := map[string]bool{selected: true}
				for _, id := range visible {
					wanted[id] = true
				}
				cancelHistory(wanted)
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
			if historyReadMethod(job.Method) {
				c.startHistoryRead(ctx, client, job)
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
			if job.Method == "conversation.import" {
				timeout = 60 * time.Second
			}
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
			if err != nil && !errors.As(err, &remote) && c.paths != nil && readOnlyPathQuery(job.Method) {
				epoch := logicalEpoch
				disconnected()
				if client != nil && logicalEpoch == epoch {
					retry := job
					retry.epoch = client.Epoch
					retry.Prepare = nil
					call, done := context.WithTimeout(ctx, timeout)
					out, err = executeJob(call, client, retry)
					done()
				}
			}
			if err != nil && errors.As(err, &remote) {
				delete(unconfirmed, opID)
			}
			if job.Apply != nil {
				c.post(func(m *Model) { job.Apply(m, out, err) })
			}
			if err != nil && !errors.As(err, &remote) && client != nil {
				disconnected()
			}
		case <-ticker.C:
			if client == nil {
				observeDetachedService()
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
					if errors.Is(err, errDevicePathsWaiting) {
						continue
					}
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
				requestedAt := time.Now()
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
				if logicalEpoch == "" || generation != snapshot.Generation {
					logicalEpoch = client.Epoch
				}
				generation = snapshot.Generation
				sequence = snapshot.Sequence
				c.mu.Lock()
				c.epoch = logicalEpoch
				c.mu.Unlock()
				minor := client.Minor
				stageCandidate(false)
				observeWorkerBuild()
				checkBackground("status")
				epoch := logicalEpoch
				c.post(func(m *Model) {
					m.WorkerMinor = minor
					m.DeviceHealth = nil
					m.Connected = true
					m.ConnectionEpoch = epoch
					m.Readiness = ready
					m.ApplySnapshot(snapshot, requestedAt)
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
					if stopHealth != nil {
						stopHealth()
						stopHealth = nil
					}
					if client.Minor >= 38 {
						stopHealth = c.observeDeviceHealth(ctx, snapshot.DeviceID, logicalEpoch)
					}
				}
			}
			if c.paths != nil && !backgroundBusy {
				recoverPath()
			}
			if service != nil && service.Phase == WorkerUpdating && !updateStarted.IsZero() && time.Since(updateStarted) > 35*time.Second {
				call, cancel := context.WithTimeout(ctx, 3*time.Second)
				var build domain.WorkerBuild
				err := client.Call(call, "worker.info", nil, &build)
				cancel()
				if err != nil {
					disconnected()
					continue
				}
				client.Worker = &build
				observeWorkerBuild()
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
				c.post(func(m *Model) {
					m.Readiness = ready
					if c.paths == nil {
						m.ConnectionTiming = sample
					}
				})
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

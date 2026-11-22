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
	SearchProgress  func(*Model, SearchProgress)
	epoch           string
}
type Controller struct {
	forwardSnapshot              []portforward.Mapping
	forwardsStarted              bool
	forwards                     *portforward.Manager
	paths                        *devicePaths
	resolveSSH                   func(transport.SSH) (transport.SSH, error)
	Tailnet                      tailnet.Connection
	signInForwards               signInForwards
	WorkerSSH                    *transport.SSH
	WorkerBinary                 string
	WorkerAutoUpdate             bool
	AuthenticatedWorkerBuild     *domain.WorkerBuild
	AuthenticatedWorkerArtifact  transport.WorkerArtifact
	AuthenticatedRuntimeArtifact transport.WorkerArtifact
	fleet                        *deviceFleet
	ExpectedDeviceID             string
	RestoreOperations            []protocol.OperationQuery
	Directory                    string
	workerUpdateDirectory        string
	Dial                         func(context.Context) (net.Conn, error)
	Jobs                         chan Job
	priorityJobs                 chan Job
	workerRepairs                chan Job
	workerControlJobs            chan Job
	Updates                      chan Update
	mu                           sync.Mutex
	runContext                   context.Context
	deviceWG                     sync.WaitGroup
	networkWG                    sync.WaitGroup
	epoch                        string
	invalidate                   func()
}

func NewController(dir string) *Controller {
	return &Controller{Directory: dir, workerUpdateDirectory: dir, Jobs: make(chan Job, 16), priorityJobs: make(chan Job, 8), Updates: make(chan Update, 256)}
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
	running := c.runContext != nil
	c.mu.Unlock()
	queue := c.Jobs
	if running && urgentLane(job) {
		queue = c.priorityJobs
	}
	if c.workerRepairs != nil && job.Method == "$worker.repair" {
		queue = c.workerRepairs
	} else if c.workerControlJobs != nil && (job.Method == "$worker.check" || job.Method == "$worker.update.cancel") {
		queue = c.workerControlJobs
	}
	select {
	case queue <- job:
		return true
	default:
		return false
	}
}
func (c *Controller) commandJobs(ctx context.Context) <-chan Job {
	jobs := make(chan Job)
	c.networkWG.Add(1)
	go func() {
		defer c.networkWG.Done()
		for {
			var job Job
			select {
			case job = <-c.priorityJobs:
			default:
				select {
				case job = <-c.priorityJobs:
				case job = <-c.Jobs:
				case <-ctx.Done():
					return
				}
			}
			select {
			case jobs <- job:
			case <-ctx.Done():
				return
			}
		}
	}()
	return jobs
}

func (c *Controller) Select(id string) { c.Do(Job{Method: "$select", Input: id}) }

// SelectHistory opens the selected session around a search result instead of its latest turn.
func (c *Controller) SelectHistory(id, thread, item string, done func(error)) {
	if !c.Do(Job{Method: "$select", Input: historySelection{id: id, thread: thread, item: item, done: done}}) {
		done(errors.New("设备忙，请重试定位消息"))
	}
}

type historySelection struct {
	id, thread, item string
	done             func(error)
}

func executeJob(ctx context.Context, client *protocol.Client, job Job) (json.RawMessage, error) {
	if client == nil || job.epoch == "" || job.epoch != client.Epoch || ctx.Err() != nil {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"}
	}
	if job.Method == "session.completion.read" && client.Minor < 39 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能同步会话处理状态"}
	}
	if job.Method == "question.reply" && !protocol.SupportsQuestionReply(client.Minor) {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能提交答案"}
	}
	if (strings.HasPrefix(job.Method, "worker.maintenance.") || job.Method == "worker.update.diagnose" || job.Method == "worker.processes.inspect") && (client.Worker == nil || (!client.Worker.Maintenance && !client.Worker.ForceRecovery)) {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "此执行服务尚不支持后台环境处理"}
	}
	if (strings.HasPrefix(job.Method, "archive.") || job.Method == "session.delete" || job.Method == "workspace.delete") && client.Minor < 40 {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能管理归档保留时间和彻底删除"}
	}
	if strings.HasPrefix(job.Method, "schedule.") && client.Minor < protocol.ScheduleMinor {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能使用定时任务"}
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
	input := job.Input
	if in, ok := input.(domain.Submission); ok && job.Method == "session.submit" && in.SessionID == "" && !in.Continue && client.Minor >= protocol.SubmissionReceiptMinor {
		input = protocol.SubmissionInput{Submission: in, ReceiveBeforePrepare: true}
	}
	err := client.Call(ctx, job.Method, input, &out)
	return out, err
}
func reconcileOperations(ctx context.Context, client *protocol.Client, queries []protocol.OperationQuery) ([]domain.Operation, error) {
	if client == nil || ctx.Err() != nil {
		return nil, net.ErrClosed
	}
	var receipts []domain.Operation
	if len(queries) > 1 && client.Minor >= protocol.CatalogMinor && client.Generation != "" {
		err := client.Call(ctx, "operations", protocol.OperationBatchQuery{Operations: queries}, &receipts)
		for _, op := range receipts {
			if !slices.ContainsFunc(queries, func(q protocol.OperationQuery) bool { return q.ID == op.ID }) {
				return nil, errors.New("提交回执标识与原操作不一致")
			}
		}
		return receipts, err
	}
	for _, lookup := range queries {
		if client.Minor < protocol.RecoveryMinor {
			lookup.SealGeneration = ""
		}
		var op domain.Operation
		err := client.Call(ctx, "operation", lookup, &op)
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && remote.Code == "notFound" {
				// The original handler can still be preparing the input. A
				// read on another stream cannot infer a definitive rejection.
				continue
			}
			return receipts, err
		}
		if op.ID != lookup.ID {
			return receipts, errors.New("提交回执标识与原操作不一致")
		}
		receipts = append(receipts, op)
	}
	return receipts, nil
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
	var sequence, replayThrough int64
	type catalogResult struct {
		client      *protocol.Client
		snapshot    domain.Snapshot
		ready       domain.Readiness
		resume      *protocol.EventPage
		requestedAt time.Time
		err         error
	}
	var catalogResults <-chan catalogResult
	catalogReady := false
	var catalogKnown, requireSnapshot, catalogResumed, catalogControls bool
	catalogRetry, eventRetry := time.Second, time.Second
	connectionPhase := ConnectionOpening
	c.post(func(m *Model) { m.ConnectionPhase = ConnectionOpening })
	publishConnectionPhase := func(phase ConnectionPhase) {
		if connectionPhase != phase {
			connectionPhase = phase
			c.post(func(m *Model) { m.ConnectionPhase = phase })
		}
	}
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
	type readinessObservation struct {
		client *protocol.Client
		query  protocol.ReadinessQuery
		ready  domain.Readiness
		timing *DeviceTiming
		err    error
	}
	var readinessResults <-chan readinessObservation
	var stopHealth context.CancelFunc
	var eventResults <-chan eventResult
	var stopEvents context.CancelFunc
	var retryEventsAt time.Time
	var nextReceiptCheck time.Time
	receiptInterval := time.Second
	type receiptCheck struct {
		epoch    string
		ids      []string
		receipts []domain.Operation
	}
	var receiptChecks <-chan receiptCheck
	type commandReply struct {
		job        Job
		generation string
		retried    bool
		client     *protocol.Client
		out        json.RawMessage
		err        error
	}
	var commandReplies <-chan commandReply
	waitingJobs := make(chan Job, cap(c.Jobs))
	type laneReply struct {
		key   string
		route string
		reply commandReply
	}
	laneResults := make(chan laneReply, 32)
	activeLanes := map[string]bool{}
	var laneJobs []Job
	var receiptCursor string
	unconfirmed := map[string]domain.OperationState{}
	receiptGenerations := map[string]string{}
	pendingReceiptErrors := map[string]Update{}
	postReceiptError := func(id string) {
		if update := pendingReceiptErrors[id]; update != nil {
			delete(pendingReceiptErrors, id)
			c.post(update)
		}
	}
	for _, lookup := range c.RestoreOperations {
		unconfirmed[lookup.ID] = ""
		receiptGenerations[lookup.ID] = lookup.SealGeneration
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
	nativeWorker := c.paths != nil && c.paths.authenticated != nil
	candidate := c.AuthenticatedWorkerBuild
	type workerStaging struct {
		job    Job
		device string
		epoch  string
		err    error
	}
	workerStageResults := make(chan workerStaging, 1)
	stagingWorker := false
	finishWorkerJob := func(job Job, err error) {
		if job.Apply != nil {
			c.post(func(m *Model) { job.Apply(m, nil, err) })
		}
	}
	var candidateChecked bool
	var candidateError error
	var service *WorkerService
	var background *domain.BackgroundService
	var lastUpdateAttempt, updateStarted time.Time
	var lastUpgradeObservation time.Time
	var lastBackgroundCheck time.Time
	updateIntent := updateNotRequested
	type uploadResult struct {
		build domain.WorkerBuild
		err   error
	}
	type requestedUpload struct {
		device, sourceBuild, targetBuild, directory, policy string
		schema                                              *WorkerSchemaConfirmation
		staged                                              *uploadResult
		reconnectUntil                                      time.Time
	}
	var upload *requestedUpload
	uploadProgress := make(chan transport.WorkerUploadPhase, 1)
	uploadResults := make(chan uploadResult)
	publishService := func(state *WorkerService) {
		state.Background = background
		service = state
		copy := *state
		c.post(func(m *Model) { m.WorkerService = &copy })
	}
	type backgroundResult struct {
		state domain.BackgroundService
		err   error
		epoch string
		job   *Job
	}
	backgroundResults := make(chan backgroundResult, 1)
	backgroundBusy := false
	type backgroundRequest struct {
		action, epoch string
		job           *Job
	}
	var pendingBackground *backgroundRequest
	checkBackground := func(action string, job *Job) {
		remote := c.authenticatedWorker()
		if (remote.Run == nil && c.WorkerBinary == "") || (nativeWorker && (remote.Run == nil || client == nil)) {
			if job != nil {
				finishWorkerJob(*job, errors.New("请连接执行设备后再操作后台服务"))
			}
			return
		}
		epoch := ""
		if client != nil {
			epoch = client.Epoch
		}
		if backgroundBusy {
			if action != "status" {
				if pendingBackground != nil && pendingBackground.job != nil {
					finishWorkerJob(*pendingBackground.job, errors.New("后台服务操作已更新"))
				}
				pendingBackground = &backgroundRequest{action, epoch, job}
			} else if job != nil {
				finishWorkerJob(*job, errors.New("正在核对后台服务"))
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
		var build *domain.WorkerBuild
		if target != nil {
			build = target.Worker
		}
		ssh := c.currentSSH()
		go func() {
			timeout := 30 * time.Second
			if remote.Run != nil && action == "register" {
				timeout = 2 * time.Minute
			}
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			state, err := c.changeWorkerBackground(callCtx, target, build, remote, ssh, action)
			select {
			case backgroundResults <- backgroundResult{state, err, epoch, job}:
			case <-ctx.Done():
			}
		}()
	}

	stageCandidate := func(force bool) {
		if c.WorkerBinary == "" || candidateChecked && !force {
			return
		}
		candidateChecked = true
		timeout := 20 * time.Second
		if force && c.configuredSSH() != nil {
			timeout = time.Minute
		}
		stageCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var info domain.WorkerBuild
		var err error
		if c.configuredSSH() != nil {
			if force {
				info, err = c.currentSSH().StageWorker(stageCtx, c.WorkerBinary)
			} else {
				info, err = transport.InspectWorker(stageCtx, c.WorkerBinary)
			}
		} else {
			info, err = transport.StageWorker(stageCtx, c.Directory, c.WorkerBinary)
		}
		candidateError = err
		if err == nil {
			candidate = &info
		}
	}
	tryWorkerUpdate := func(explicit bool, schema *WorkerSchemaConfirmation, policy ...string) (bool, error) {
		if c.WorkerBinary == "" {
			return false, nil
		}
		if explicit {
			remote := c.configuredSSH() != nil
			if remote && (c.ExpectedDeviceID == "" || client != nil && client.DeviceID != c.ExpectedDeviceID) {
				return false, errors.New("请先重新连接并确认设备身份，再更新执行服务")
			}
			updateIntent = updateWaitingForIdle
			if !remote {
				stageCandidate(true)
			}
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
		if schema != nil && (client == nil || client.Worker == nil || candidate == nil ||
			client.Worker.BuildID != schema.SourceBuild || candidate.BuildID != schema.TargetBuild) {
			var current *domain.WorkerBuild
			if client != nil {
				current = client.Worker
			}
			state := newWorkerService(current, candidate)
			state.Phase, state.Message = WorkerUpdateFailed, "更新构建或连接已变化，请重新确认数据库迁移"
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
				state, e := observer.update(ctx, nil, candidate, true, selectedPolicy, false)
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
			state, e := observer.update(call, client, candidate, explicit, selectedPolicy, schema != nil)
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
		if upload != nil || stagingWorker {
			return
		}
		lastUpgradeObservation = time.Now()
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
		if upload != nil || stagingWorker {
			return
		}
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
			if state.OperationPhase == "applied" && state.Phase != WorkerUpdateFailed {
				state.Phase = WorkerUpdating
				state.Message = "更新已提交，正在重新连接"
			}
			if e != nil {
				state.Message = e.Error()
			}
			publishService(state)
		}
	}
	var recoverPath func() bool
	type pathHandoff struct {
		base, next *protocol.Client
		id, epoch  string
		snapshot   domain.Snapshot
		ready      domain.Readiness
		events     []domain.Event
		err        error
	}
	var pathHandoffs <-chan pathHandoff
	var preparedPath *pathHandoff
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
		if c.paths != nil {
			// Resetting a client is not a failed path observation. Actual dial,
			// probe and stream failures are recorded at their own boundaries.
			if recoverPath != nil && recoverPath() {
				return
			}
		}
		catalogReady, catalogResults, catalogControls = false, nil, false
		readinessResults = nil
		replayThrough = 0
		cancelHistory(nil)
		if stopHealth != nil {
			stopHealth()
			stopHealth = nil
		}
		if stopEvents != nil {
			stopEvents()
			stopEvents = nil
		}
		eventResults = nil
		retryEventsAt = time.Time{}
		if client != nil {
			client.Close()
		}
		client = nil
		for id, phase := range history {
			history[id] = phase.invalidated()
		}
		c.mu.Lock()
		logicalEpoch, c.epoch = "", ""
		c.mu.Unlock()
		connectionPhase = ConnectionReconnecting
		c.post(func(m *Model) {
			m.Connected = false
			m.ConnectionEpoch = ""
			m.ConnectionPhase = ConnectionReconnecting
		})
		connectionPhase = ConnectionReconnecting
		for id := range pendingReceiptErrors {
			postReceiptError(id)
		}
	}
	var selection historySelection
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
				if id == selection.id && selection.item != "" {
					initialHistory[id] = c.queueInitialHistoryPage(ctx, id, epoch, generation, opening, catalogResumed, selection)
					selection = historySelection{}
				} else {
					initialHistory[id] = c.queueInitialHistory(ctx, id, epoch, generation, opening, catalogResumed)
				}
				continue
			}
			if id == selection.id && selection.item != "" {
				done := selection.done
				selection = historySelection{}
				c.post(func(*Model) { done(errors.New("更新设备执行服务后才能定位历史消息")) })
			}
			read := &HistoryRead{Pending: true}
			epoch := logicalEpoch
			if epoch == "" {
				epoch = client.Epoch
			}
			c.post(func(m *Model) {
				if m.ConnectionEpoch == epoch && m.ownsSession(id) {
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
				all.HistoryLoaded = true
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

	applyPage := func(page protocol.EventPage, replay ...bool) bool {
		if page.Generation != generation {
			return false
		}
		if len(page.Events) == 0 {
			return true
		}
		events := page.Events
		for i, event := range events {
			if event.Sequence != sequence+int64(i)+1 {
				return false
			}
		}
		sequence = events[len(events)-1].Sequence
		for _, e := range events {
			if client != nil && client.Minor < 37 && e.Type == "history.reset" {
				// Legacy workers refresh through the controller, without a window
				// invalidation that the view can use to request a new page.
				history[e.SessionID] = history[e.SessionID].invalidated()
			}
			if op := e.Operation; op != nil {
				if op.State.ReceiptObserved() {
					delete(unconfirmed, op.ID)
					delete(receiptGenerations, op.ID)
				} else if _, ok := unconfirmed[op.ID]; ok {
					unconfirmed[op.ID] = op.State
				}
			}
		}
		historicalThrough := replayThrough
		if len(replay) != 0 && replay[0] {
			historicalThrough = sequence
		}
		c.post(func(m *Model) {
			for _, e := range events {
				if err := m.applyEvent(e, e.Sequence > historicalThrough); err != nil {
					m.scopeFailure(err)
					return
				}
			}
		})
		for _, e := range events {
			if e.Operation != nil {
				postReceiptError(e.Operation.ID)
			}
		}
		return true
	}
	adoptPath := func(result pathHandoff) bool {
		if result.err != nil || result.base != client || result.epoch != logicalEpoch || !catalogReady {
			if result.next != nil {
				result.next.Close()
			}
			c.paths.preparing("")
			if result.err != nil {
				c.paths.record(result.id, 0, time.Now(), result.err)
			}
			c.publishPaths()
			if connectionPhase == ConnectionSwitching {
				publishConnectionPhase(ConnectionIdle)
			}
			return false
		}
		id, next, snapshot, ready, gap := result.id, result.next, result.snapshot, result.ready, result.events
		start := sort.Search(len(gap), func(i int) bool { return gap[i].Sequence > sequence })
		if !applyPage(protocol.EventPage{Generation: generation, Events: gap[start:]}) {
			next.Close()
			c.paths.preparing("")
			c.publishPaths()
			publishConnectionPhase(ConnectionIdle)
			return false
		}
		if stopEvents != nil {
			stopEvents()
			stopEvents = nil
		}
		eventResults = nil
		retryEventsAt = time.Time{}
		old := client
		client = next
		observeWorkerBuild()
		c.paths.activate(id, time.Now())
		c.publishPaths()
		epoch, minor := logicalEpoch, client.Minor
		connectionPhase = ConnectionIdle
		c.post(func(m *Model) {
			m.Connected = true
			m.ConnectionPhase, m.ConnectionError = ConnectionIdle, ""
			m.ConnectionEpoch = epoch
			m.WorkerMinor = minor
			m.Readiness = ready
		})
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
	preferredPath := func() string {
		if c.paths == nil || client == nil || !catalogReady || backgroundBusy || pathHandoffs != nil || preparedPath != nil {
			return ""
		}
		id := c.paths.preferred(time.Now())
		if id == c.paths.active() {
			return ""
		}
		return id
	}
	recoverPath = func() bool {
		id := preferredPath()
		if id == "" {
			return false
		}
		previousPhase := connectionPhase
		publishConnectionPhase(ConnectionSwitching)
		c.paths.preparing(id)
		c.publishPaths()
		next, snapshot, ready, gap, err := c.preparePath(ctx, id, generation, sequence, readinessQuery())
		adopted := adoptPath(pathHandoff{client, next, id, logicalEpoch, snapshot, ready, gap, err})
		if adopted {
			connectionPhase = ConnectionIdle
		} else {
			publishConnectionPhase(previousPhase)
		}
		return adopted
	}
	preparePreferredPath := func() {
		id := preferredPath()
		if id == "" {
			return
		}
		publishConnectionPhase(ConnectionSwitching)
		c.paths.preparing(id)
		c.publishPaths()
		results := make(chan pathHandoff)
		pathHandoffs = results
		base, epoch, gen, cursor, query := client, logicalEpoch, generation, sequence, readinessQuery()
		c.networkWG.Add(1)
		go func() {
			defer c.networkWG.Done()
			next, snapshot, ready, gap, err := c.preparePath(ctx, id, gen, cursor, query)
			select {
			case results <- pathHandoff{base, next, id, epoch, snapshot, ready, gap, err}:
			case <-ctx.Done():
				if next != nil {
					next.Close()
				}
			}
		}()
	}
	repairCommand := func(cause error) bool {
		if client == nil {
			return false
		}
		// EOF on a routed stream can be a path failure. A deadline alone is
		// not evidence to demote the independently probed active path.
		var timeout net.Error
		if c.paths != nil && (!errors.As(cause, &timeout) || !timeout.Timeout()) {
			c.paths.record(c.paths.active(), 0, time.Now(), cause)
			c.publishPaths()
			if recoverPath() {
				return true
			}
		}
		call, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		dial := c.activeWorkerDial()
		next, err := connectWorkerGeneration(call, func(context.Context) (net.Conn, error) { return dial(ctx) }, client.DeviceID, generation)
		if err != nil {
			return false
		}
		client.Close()
		client = next
		return true
	}
	checkReceipts := func() {
		if receiptChecks != nil || client == nil || time.Now().Before(nextReceiptCheck) {
			return
		}
		ids := make([]string, 0, len(unconfirmed))
		for id := range unconfirmed {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return
		}
		sort.Strings(ids)
		start := sort.Search(len(ids), func(i int) bool { return ids[i] > receiptCursor })
		if start == len(ids) {
			start = 0
		}
		if len(ids) > 8 {
			ordered := append(slices.Clone(ids[start:]), ids[:start]...)
			ids = ordered[:8]
			receiptInterval = time.Second
		}
		receiptCursor = ids[len(ids)-1]
		queries := make([]protocol.OperationQuery, 0, len(ids))
		for _, id := range ids {
			queries = append(queries, protocol.OperationQuery{ID: id, SealGeneration: receiptGenerations[id]})
		}
		nextReceiptCheck = time.Now().Add(receiptInterval)
		receiptInterval = min(15*time.Second, 2*receiptInterval)
		dial, device, gen, epoch := c.activeWorkerDial(), client.DeviceID, generation, logicalEpoch
		result := make(chan receiptCheck, 1)
		receiptChecks = result
		c.networkWG.Add(1)
		go func() {
			defer c.networkWG.Done()
			observed := receiptCheck{epoch: epoch, ids: ids}
			defer func() { result <- observed }()
			call, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			peer, err := connectWorkerGeneration(call, dial, device, gen)
			if err != nil {
				return
			}
			defer peer.Close()
			observed.receipts, _ = reconcileOperations(call, peer, queries)
		}()
	}
	var startCommand func(Job, bool)
	finishCommand := func(reply commandReply) {
		job, out, err := reply.job, reply.out, reply.err
		var remote *protocol.RemoteError
		if err != nil && !errors.As(err, &remote) && client != nil && reply.client == client {
			if !repairCommand(err) {
				disconnected()
			} else if !reply.retried && readOnlyPathQuery(job.Method) {
				job.epoch = client.Epoch
				job.Prepare = nil
				startCommand(job, true)
				return
			}
		}
		if err != nil && !errors.As(err, &remote) {
			var opID string
			switch in := job.Input.(type) {
			case protocol.SteerInput:
				opID = in.Input.OperationID
			case domain.Submission:
				opID = in.OperationID
			}
			if opID != "" {
				unconfirmed[opID] = ""
				if job.Method == "session.submit" || job.Method == "turn.steer" || job.Method == "turn.enqueue" {
					receiptGenerations[opID] = reply.generation
				}
				nextReceiptCheck = time.Time{}
				receiptInterval = time.Second
				deferFeedback := client != nil && job.Apply != nil
				if deferFeedback {
					// Give the owning worker's journal or first receipt lookup
					// precedence over a lost command reply in the presentation.
					pendingReceiptErrors[opID] = func(m *Model) { job.Apply(m, out, err) }
				}
				checkReceipts()
				if deferFeedback {
					return
				}
			}
		}
		if job.Apply != nil {
			c.post(func(m *Model) { job.Apply(m, out, err) })
		}
	}
	startLane := func(job Job, key string) {
		current, epoch, gen := client, logicalEpoch, generation
		if current == nil || job.epoch == "" || job.epoch != epoch {
			if job.Apply != nil {
				c.post(func(m *Model) {
					job.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"})
				})
			}
			return
		}
		activeLanes[key] = true
		dial, device, route := c.activeWorkerDial(), current.DeviceID, ""
		if c.paths != nil {
			route = c.paths.active()
		}
		c.networkWG.Add(1)
		go func() {
			defer c.networkWG.Done()
			call, cancel := context.WithTimeout(ctx, commandTimeout(job.Method))
			defer cancel()
			if job.Context != nil {
				stop := context.AfterFunc(job.Context, cancel)
				defer stop()
			}
			peer, err := connectWorkerGeneration(call, dial, device, gen)
			var out json.RawMessage
			if err != nil {
				err = &protocol.RemoteError{Code: "notSent", Message: "未建立命令连接，操作未发送：" + err.Error()}
			} else {
				defer peer.Close()
				c.mu.Lock()
				stillAdmitted := c.epoch == epoch
				c.mu.Unlock()
				if !stillAdmitted {
					err = &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"}
				} else {
					wireJob := job
					wireJob.epoch = peer.Epoch
					wireJob.Prepare = func() error {
						if job.Prepare != nil {
							if err := job.Prepare(); err != nil {
								return err
							}
						}
						c.mu.Lock()
						stillAdmitted := c.epoch == epoch
						c.mu.Unlock()
						if !stillAdmitted {
							return errors.New("连接已改变，请重新提交")
						}
						return call.Err()
					}
					out, err = executeJob(call, peer, wireJob)
				}
			}
			select {
			case laneResults <- laneReply{key: key, route: route, reply: commandReply{job: job, generation: current.Generation, out: out, err: err}}:
			case <-ctx.Done():
			}
		}()
	}
	finishUpload := func(result uploadResult) {
		if ctx.Err() != nil {
			return
		}
		request := upload
		upload = nil
		var current *domain.WorkerBuild
		if client != nil {
			current = client.Worker
		}
		if result.err == nil && request.targetBuild != "" && request.targetBuild != result.build.BuildID {
			result.err = errors.New("候选执行服务已变化，请重新检查更新")
		}
		if result.err == nil {
			candidate, candidateChecked, candidateError = &result.build, true, nil
			if current != nil && current.BuildID == candidate.BuildID && request.sourceBuild != candidate.BuildID {
				observeWorkerBuild()
				return
			}
			ssh := c.configuredSSH()
			if client == nil || client.DeviceID != request.device || ssh == nil || ssh.StateDirectory != request.directory {
				result.err = errors.New("执行服务已暂存，请重新连接后重试更新")
			} else if current == nil || current.BuildID != request.sourceBuild {
				result.err = errors.New("设备执行服务已变化，请重新检查更新")
			}
		}
		if result.err != nil {
			if current == nil {
				current = service.Build
			}
			state := newWorkerService(current, candidate)
			state.Phase, state.Message = WorkerUpdateFailed, result.err.Error()
			publishService(state)
			return
		}
		if restart, err := tryWorkerUpdate(true, request.schema, request.policy); restart || err != nil {
			disconnected()
			lastAttempt = time.Time{}
		}
	}
	startCommand = func(job Job, retried bool) {
		result := make(chan commandReply, 1)
		commandReplies = result
		current := client
		c.networkWG.Add(1)
		go func() {
			defer c.networkWG.Done()
			call, cancel := context.WithTimeout(ctx, commandTimeout(job.Method))
			defer cancel()
			if job.Context != nil {
				stop := context.AfterFunc(job.Context, cancel)
				defer stop()
			}
			out, err := executeJob(call, current, job)
			result <- commandReply{job: job, retried: retried, client: current, out: out, err: err}
		}()
	}
	startCatalog := func() {
		publishConnectionPhase(ConnectionSyncing)
		result := make(chan catalogResult, 1)
		catalogResults = result
		current, query := client, readinessQuery()
		device := current.DeviceID
		gen, cursor := generation, sequence
		canResume := catalogKnown && !requireSnapshot && current.Generation == gen && current.Minor >= 1
		c.networkWG.Add(1)
		go func() {
			defer c.networkWG.Done()
			out := catalogResult{client: current, requestedAt: time.Now()}
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			if canResume {
				var page protocol.EventPage
				err := current.Call(call, "events", protocol.EventsQuery{After: cursor, Generation: gen}, &page)
				continuous := page.Generation == gen
				for i, event := range page.Events {
					continuous = continuous && event.Sequence == cursor+int64(i)+1
				}
				if err == nil && continuous {
					out.resume = &page
					out.snapshot = domain.Snapshot{DeviceID: device, Generation: gen}
				} else {
					var remote *protocol.RemoteError
					if err != nil && !errors.As(err, &remote) {
						out.err = err
					}
				}
			}
			if out.err == nil && out.resume == nil {
				out.snapshot, out.err = current.ReadSnapshot(call)
			}
			cancel()
			if out.err == nil && out.snapshot.DeviceID != device {
				out.err = &protocol.RemoteError{Code: "snapshotIdentity", Message: "worker snapshot identity mismatch"}
			} else if out.err == nil && (out.snapshot.Generation == "" || current.Generation != "" && out.snapshot.Generation != current.Generation) {
				out.err = &protocol.RemoteError{Code: "snapshotIdentity", Message: "设备目录与执行服务运行代次不一致"}
			}
			if out.err == nil {
				call, cancel = context.WithTimeout(ctx, 3*time.Second)
				if err := current.Call(call, "readiness", query, &out.ready); err != nil {
					out.ready = domain.Readiness{Error: err.Error()}
				}
				cancel()
			}
			result <- out
		}()
	}
	refreshCatalog := func() {
		requireSnapshot = true
		if client == nil || client.Generation == "" || client.Generation != generation || !catalogReady {
			disconnected()
			return
		}
		stopEvents()
		eventResults, stopEvents = nil, nil
		retryEventsAt = time.Time{}
		catalogReady, catalogControls = false, true
		startCatalog()
	}
	checkBackground("status", nil)
	commandJobs := c.commandJobs(ctx)
	for {
		if preparedPath != nil && commandReplies == nil && readinessResults == nil {
			adoptPath(*preparedPath)
			preparedPath = nil
		}
		if upload != nil && upload.staged != nil && (client != nil || time.Now().After(upload.reconnectUntil)) {
			finishUpload(*upload.staged)
		}
		jobs := commandJobs
		if catalogReady && commandReplies == nil && readinessResults == nil && len(waitingJobs) > 0 {
			jobs = waitingJobs
		}
		if client != nil && !catalogReady && !catalogControls {
			jobs = nil
		}
		select {
		case <-ctx.Done():
			if preparedPath != nil && preparedPath.next != nil {
				preparedPath.next.Close()
			}
			return
		case phase := <-uploadProgress:
			state := *service
			state.UploadPhase = phase
			publishService(&state)
		case result := <-uploadResults:
			select {
			case <-uploadProgress:
			default:
			}
			if result.err == nil && client == nil {
				upload.staged = &result
				upload.reconnectUntil = time.Now().Add(5 * time.Second)
				state := *service
				state.UploadPhase = ""
				state.Message = "执行服务已校验，正在重新连接"
				publishService(&state)
				continue
			}
			finishUpload(result)
		case <-pathChanges:
			c.publishPaths()
		case result := <-pathHandoffs:
			pathHandoffs = nil
			if commandReplies == nil && readinessResults == nil || result.err != nil {
				adoptPath(result)
			} else {
				preparedPath = &result
			}
		case result := <-backgroundResults:
			backgroundBusy = false
			epoch := ""
			if client != nil {
				epoch = client.Epoch
			}
			if result.epoch != epoch {
				if result.job != nil {
					finishWorkerJob(*result.job, result.err)
				}
				if pendingBackground != nil && pendingBackground.epoch != epoch {
					if pendingBackground.job != nil {
						finishWorkerJob(*pendingBackground.job, errors.New("连接已变化，请重新核对后台服务"))
					}
					pendingBackground = nil
				}
				checkBackground("status", nil)
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
			if result.job != nil {
				finishWorkerJob(*result.job, result.err)
			}
			if pendingBackground != nil {
				requested := pendingBackground
				pendingBackground = nil
				if requested.epoch == epoch {
					checkBackground(requested.action, requested.job)
				} else if requested.job != nil {
					finishWorkerJob(*requested.job, errors.New("连接已变化，请重新核对后台服务"))
				}
			}
		case result := <-catalogResults:
			catalogResults = nil
			if result.client != client {
				continue
			}
			if result.err != nil {
				message := result.err.Error()
				connectionPhase = ConnectionSyncFailed
				c.post(func(m *Model) {
					m.ConnectionPhase, m.ConnectionError = ConnectionSyncFailed, message
				})
				var remote *protocol.RemoteError
				if errors.As(result.err, &remote) && remote.Code == "snapshotIdentity" {
					disconnected()
					continue
				}
				if client.Generation == "" || client.Minor < protocol.RecoveryMinor {
					disconnected()
					continue
				}
				if remote != nil {
					// The worker answered on this stream. Retry its catalog
					// here without renegotiating a healthy transport.
					lastAttempt = time.Now()
					continue
				}
				// Verify liveness on a fresh stream. A catalog error must
				// not demote an otherwise healthy worker.
				call, cancel := context.WithTimeout(ctx, 3*time.Second)
				next, err := connectWorkerGeneration(call, c.activeWorkerDial(), client.DeviceID, client.Generation)
				if next != nil {
					next.Close()
				}
				cancel()
				if err != nil {
					disconnected()
					continue
				}
				client.Close()
				client = nil
				lastAttempt = time.Now()
				continue
			}
			snapshot := result.snapshot
			c.ExpectedDeviceID = client.DeviceID
			if logicalEpoch == "" || generation != snapshot.Generation {
				logicalEpoch = client.Epoch
			}
			if result.resume == nil {
				generation, sequence = snapshot.Generation, snapshot.Sequence
			}
			catalogReady, catalogKnown, requireSnapshot, catalogControls = true, true, false, false
			catalogResumed = result.resume != nil
			catalogRetry = time.Second
			nextReceiptCheck = time.Time{}
			receiptInterval = time.Second
			c.mu.Lock()
			c.epoch = logicalEpoch
			c.mu.Unlock()
			stageCandidate(false)
			observeWorkerBuild()
			checkBackground("status", nil)
			minor, epoch := client.Minor, logicalEpoch
			connectionPhase = ConnectionIdle
			c.post(func(m *Model) {
				m.WorkerMinor = minor
				m.DeviceHealth = nil
				m.Connected = true
				m.ConnectionEpoch = epoch
				m.ConnectionPhase, m.ConnectionError = ConnectionIdle, ""
				m.Readiness = result.ready
				if result.resume == nil {
					m.ApplySnapshot(snapshot, result.requestedAt)
				}
			})
			if result.resume != nil {
				replayThrough = result.resume.Sequence
				applyPage(*result.resume, true)
			} else {
				replayThrough = 0
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
			lastReadiness = time.Now()
		case result := <-eventResults:
			if result.err == nil {
				if !applyPage(result.page) {
					refreshCatalog()
				} else {
					eventRetry = time.Second
				}
				continue
			}
			// An event stream can be replaced at its last cursor without
			// invalidating the healthy command channel or the visible history.
			var remote *protocol.RemoteError
			if errors.As(result.err, &remote) && strings.HasPrefix(remote.Message, "snapshot required:") {
				refreshCatalog()
				continue
			}
			stopEvents()
			eventResults, stopEvents = nil, nil
			retryEventsAt = time.Now().Add(eventRetry)
			eventRetry = min(15*time.Second, 2*eventRetry)
		case observed := <-readinessResults:
			readinessResults = nil
			if observed.client != client {
				continue
			}
			if observed.err != nil {
				var remote *protocol.RemoteError
				if !errors.As(observed.err, &remote) && !repairCommand(observed.err) {
					disconnected()
				} else if remote != nil {
					c.post(func(m *Model) {
						if m.Readiness.State == "" && len(m.Readiness.Harnesses) == 0 {
							m.Readiness.Error = remote.Error()
						}
					})
				}
				continue
			}
			if !slices.Equal(observed.query.SessionIDs, readinessQuery().SessionIDs) {
				continue
			}
			c.post(func(m *Model) {
				m.Readiness = observed.ready
				if c.paths == nil {
					m.ConnectionTiming = observed.timing
				}
			})
		case observed := <-receiptChecks:
			receiptChecks = nil
			if client == nil || observed.epoch != logicalEpoch {
				for _, id := range observed.ids {
					postReceiptError(id)
				}
				continue
			}
			for _, op := range observed.receipts {
				last, pending := unconfirmed[op.ID]
				if !pending || last == op.State || last == domain.OperationDispatched && op.State == domain.OperationAccepted {
					continue
				}
				if op.State.ReceiptObserved() {
					delete(unconfirmed, op.ID)
					delete(receiptGenerations, op.ID)
				} else {
					unconfirmed[op.ID] = op.State
				}
				c.post(func(m *Model) { m.ApplyOperation(op) })
			}
			for _, id := range observed.ids {
				postReceiptError(id)
			}
		case received := <-laneResults:
			delete(activeLanes, received.key)
			var remote *protocol.RemoteError
			var timeout net.Error
			if received.route != "" && c.paths.active() == received.route && received.reply.err != nil &&
				!errors.As(received.reply.err, &remote) && !(errors.As(received.reply.err, &timeout) && timeout.Timeout()) {
				c.paths.record(received.route, 0, time.Now(), received.reply.err)
				c.publishPaths()
				preparePreferredPath()
			}
			finishCommand(received.reply)
			for i := 0; i < len(laneJobs); {
				job := laneJobs[i]
				key := commandLane(job)
				if activeLanes[key] || len(activeLanes) >= 12 || len(activeLanes) >= 8 && !urgentLane(job) {
					i++
					continue
				}
				laneJobs = slices.Delete(laneJobs, i, i+1)
				startLane(job, key)
			}
		case reply := <-commandReplies:
			commandReplies = nil
			finishCommand(reply)
		case job := <-c.workerRepairs:
			if client != nil || upload != nil || c.ExpectedDeviceID == "" {
				continue
			}
			operation, _ := job.Input.(string)
			var known *domain.WorkerBuild
			if service != nil {
				known = service.Build
			}
			call, cancel := context.WithTimeout(ctx, time.Minute)
			state, err := c.upgradeObserver(c.ExpectedDeviceID, known).retryDetached(call, operation, candidate)
			cancel()
			if state != nil {
				if err != nil {
					state.Message = err.Error()
				}
				publishService(state)
			}
			if job.Apply != nil {
				c.post(func(m *Model) { job.Apply(m, nil, err) })
			}
			lastDetachedService = time.Now()
			lastAttempt = time.Time{}
		case staged := <-workerStageResults:
			stagingWorker = false
			var current *domain.WorkerBuild
			if client != nil {
				current = client.Worker
			}
			input := staged.job.Input.(AuthenticatedWorkerConfirmation)
			if staged.err != nil || client == nil || client.DeviceID != staged.device || logicalEpoch != staged.epoch ||
				current == nil || current.BuildID != input.SourceBuild {
				state := newWorkerService(current, candidate)
				state.Phase, state.Message = WorkerUpdateFailed, "设备连接或执行服务版本已变化，请重新确认升级"
				if staged.err != nil {
					state.Message = staged.err.Error()
				}
				publishService(state)
				finishWorkerJob(staged.job, nil)
				continue
			}
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			observer := c.upgradeObserver(staged.device, current)
			policy := "when-idle"
			if staged.job.Method == "$worker.update.immediate" {
				policy = "immediate"
				_, staged.err = observer.control(call, "cancel", input.OperationID)
				if staged.err == nil {
					staged.err = waitForWorkerCancellation(call, observer, client, candidate, input.OperationID)
				}
			}
			if staged.err == nil {
				state, err := observer.update(call, client, candidate, true, policy, staged.job.Method == "$worker.update.migrate")
				if state != nil {
					if err != nil {
						state.Message = err.Error()
					}
					publishService(state)
				} else {
					observeWorkerBuild()
				}
				staged.err = err
			} else {
				observeWorkerBuild()
			}
			cancel()
			finishWorkerJob(staged.job, staged.err)
		case job := <-c.workerControlJobs:
			var err error
			if job.Method == "$worker.update.cancel" {
				id, _ := job.Input.(string)
				if service == nil || id == "" || service.OperationID != id || service.OperationPhase != "waiting" {
					finishWorkerJob(job, errors.New("升级状态已变化，请重新检查"))
					continue
				}
				observer := c.upgradeObserver(c.ExpectedDeviceID, service.Build)
				call, cancel := context.WithTimeout(ctx, 8*time.Second)
				_, err = observer.control(call, "cancel", id)
				cancel()
			}
			if client != nil {
				observeWorkerBuild()
			} else {
				lastDetachedService = time.Time{}
				observeDetachedService()
				lastAttempt = time.Time{}
			}
			finishWorkerJob(job, err)
		case job := <-jobs:
			if catalogControls && !urgentLane(job) && job.Method != "$select" && job.Method != "$visible" {
				if job.Apply != nil {
					c.post(func(m *Model) {
						job.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "设备目录正在同步，操作未发送"})
					})
				}
				continue
			}
			if key := commandLane(job); key != "" {
				if activeLanes[key] || len(activeLanes) >= 12 || len(activeLanes) >= 8 && !urgentLane(job) {
					if len(laneJobs) < 40 && (urgentLane(job) || len(laneJobs) < 32) {
						laneJobs = append(laneJobs, job)
					} else if job.Apply != nil {
						c.post(func(m *Model) {
							job.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "设备命令队列暂满，操作未发送"})
						})
					}
				} else {
					startLane(job, key)
				}
				continue
			}
			if (commandReplies != nil || readinessResults != nil) && job.Method != "$select" && job.Method != "$visible" && !historyReadMethod(job.Method) {
				select {
				case waitingJobs <- job:
				default:
					if job.Apply != nil {
						c.post(func(m *Model) {
							job.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "设备命令队列暂满，操作未发送"})
						})
					}
				}
				continue
			}
			if job.Method == "$worker.recovery" {
				c.runWorkerRecoveryJob(ctx, job)
				continue
			}
			if isForwardJob(job.Method) {
				c.runForwardJob(ctx, job)
				continue
			}
			if client != nil && job.epoch == logicalEpoch {
				job.epoch = client.Epoch
			}
			if job.Method == "$background.status" || job.Method == "$background.enable" || job.Method == "$background.disable" {
				if job.Method != "$background.status" && (client == nil || job.epoch == "" || job.epoch != client.Epoch) {
					finishWorkerJob(job, errors.New("连接已变化，请重新核对后台服务"))
					continue
				}
				action := "status"
				if job.Method == "$background.enable" {
					action = "register"
				}
				if job.Method == "$background.disable" {
					action = "unregister"
				}
				checkBackground(action, &job)
				continue
			}
			if job.Method == "$worker.maintenance.inspect" {
				var err error
				if client == nil || job.epoch != client.Epoch || client.Worker == nil || !client.Worker.Maintenance {
					err = &protocol.RemoteError{Code: "notSent", Message: "请重新连接并检查执行服务能力"}
				} else {
					stageCandidate(true)
					if candidateError == nil {
						job.Method = "worker.update.diagnose"
						startCommand(job, false)
						continue
					}
					err = &protocol.RemoteError{Code: "notSent", Message: candidateError.Error()}
				}
				if job.Apply != nil {
					c.post(func(m *Model) { job.Apply(m, nil, err) })
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
			if job.Method == "$worker.update" || job.Method == "$worker.update.immediate" || job.Method == "$worker.update.migrate" {
				if nativeWorker {
					input, valid := job.Input.(AuthenticatedWorkerConfirmation)
					if stagingWorker || !valid || client == nil || client.Epoch != job.epoch || client.Worker == nil ||
						client.Worker.BuildID != input.SourceBuild || candidate == nil || candidate.BuildID != input.TargetBuild ||
						c.AuthenticatedWorkerArtifact == nil {
						finishWorkerJob(job, errors.New("请重新检查已连接设备的执行服务版本"))
						continue
					}
					observer := c.upgradeObserver(client.DeviceID, client.Worker)
					prior, err := observer.load()
					if err != nil {
						finishWorkerJob(job, err)
						continue
					}
					if job.Method == "$worker.update.immediate" {
						if prior == nil || prior.Operation == nil || prior.Operation.Phase != "waiting" ||
							prior.Input.ID != input.OperationID || prior.Input.BuildID != input.TargetBuild ||
							prior.Input.ExpectedBuildID != input.SourceBuild || prior.Input.SchemaMigration {
							finishWorkerJob(job, errors.New("原升级已变化，请重新检查"))
							continue
						}
					} else if prior != nil && !prior.Rejected && (prior.Operation == nil || !prior.Operation.Terminal()) {
						observeWorkerBuild()
						finishWorkerJob(job, nil)
						continue
					}
					state := newWorkerService(client.Worker, candidate)
					migrate := job.Method == "$worker.update.migrate"
					crossSchema := client.Worker.Schema > 0 && candidate.Schema != client.Worker.Schema
					repairing := prior != nil && prior.Operation != nil && prior.Operation.Phase == "blocked"
					if (!repairing && state.Phase != WorkerAvailable) || (crossSchema && !state.RequiresSchemaMigration()) || crossSchema != migrate {
						finishWorkerJob(job, errors.New("此执行服务版本无法按所选方式升级"))
						continue
					}
					remote := c.authenticatedWorker()
					if remote.Run == nil {
						finishWorkerJob(job, errors.New("设备当前没有可用的 SSH 路径"))
						continue
					}
					stagingWorker = true
					state.Phase, state.Message = WorkerPreparing, "正在上传并验证已签名的候选版本"
					if job.Method == "$worker.update.immediate" {
						state.Message = "正在核对已暂存的版本和等待中的升级"
					}
					publishService(state)
					request := workerStaging{job: job, device: client.DeviceID, epoch: logicalEpoch}
					if job.Method == "$worker.update.immediate" {
						workerStageResults <- request
					} else {
						c.networkWG.Add(1)
						go func() {
							defer c.networkWG.Done()
							call, cancel := context.WithTimeout(ctx, 2*time.Minute)
							defer cancel()
							request.err = remote.Stage(call, input.TargetBuild, c.AuthenticatedWorkerArtifact)
							workerStageResults <- request
						}()
					}
					continue
				}
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
				var schema *WorkerSchemaConfirmation
				if job.Method == "$worker.update.migrate" {
					confirmed, valid := job.Input.(WorkerSchemaConfirmation)
					if !valid || !service.RequiresSchemaMigration() {
						continue
					}
					schema = &confirmed
				}
				if ssh := c.configuredSSH(); ssh != nil {
					if c.WorkerBinary == "" || client == nil || client.Worker == nil || client.DeviceID != c.ExpectedDeviceID {
						continue
					}
					upload = &requestedUpload{device: client.DeviceID, sourceBuild: client.Worker.BuildID, directory: ssh.StateDirectory, policy: policy, schema: schema}
					if candidate != nil {
						upload.targetBuild = candidate.BuildID
					}
					state := newWorkerService(client.Worker, candidate)
					state.Phase, state.UploadPhase = WorkerUpdating, transport.WorkerUploadSending
					updateStarted = time.Time{}
					publishService(state)
					go func(route transport.SSH) {
						resolve := func() (transport.SSH, error) {
							if c.resolveSSH != nil {
								return c.resolveSSH(route)
							}
							return route, nil
						}
						lastPhase := transport.WorkerUploadSending
						report := func(phase transport.WorkerUploadPhase) {
							if lastPhase != phase {
								lastPhase = phase
								select {
								case <-uploadProgress:
								default:
								}
								uploadProgress <- phase
							}
						}
						info, err := transport.StageRemoteWorker(ctx, c.WorkerBinary, resolve, report)
						select {
						case uploadResults <- uploadResult{info, err}:
						case <-ctx.Done():
						}
					}(*ssh)
					continue
				}
				restart, err := tryWorkerUpdate(true, schema, policy)
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
				if client != nil && catalogReady && (commandReplies == nil || client.Minor >= 37) {
					if err := fetchDetail(); err != nil {
						disconnected()
					}
				}
				continue
			}
			if job.Method == "$select" {
				switch target := job.Input.(type) {
				case historySelection:
					selection, selected = target, target.id
				case string:
					selection, selected = historySelection{}, target
				}
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
				if client != nil && catalogReady && (commandReplies == nil || client.Minor >= 37) {
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
			if job.Method == "session.delete" || job.Method == "workspace.delete" {
				c.startArchiveDelete(ctx, client, job)
				continue
			}
			startCommand(job, false)
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
						if c.paths != nil {
							states := c.paths.states(time.Now())
							if len(states) > 0 && !slices.ContainsFunc(states, func(s DevicePathStatus) bool { return s.Phase != PathUnavailable }) {
								publishConnectionPhase(ConnectionUnavailable)
							}
						}
						continue
					}
					publishConnectionPhase(ConnectionUnavailable)
					var network *net.OpError
					if c.Dial == nil && errors.As(err, &network) && network.Op == "dial" {
						continue
					}
					var legacy *protocol.LegacyWorkerError
					if errors.As(err, &legacy) {
						publishService(&WorkerService{Phase: WorkerLegacy, Message: err.Error(), Legacy: &domain.LegacyWorkerInfo{DeviceID: legacy.DeviceID, Major: legacy.Major, Minor: legacy.Minor}})
					}
					message := err.Error()
					c.post(func(m *Model) {
						m.Connected = false
						m.ConnectionEpoch = ""
						m.ConnectionError = message
					})
					continue
				}
				publishConnectionPhase(ConnectionSyncing)
				if client.Generation != "" && client.Minor >= protocol.RecoveryMinor {
					// A verified connection is shown while its catalog is
					// loading. The catalog still gates session mutations.
					minor := client.Minor
					c.post(func(m *Model) {
						m.Connected = true
						m.WorkerMinor = minor
						m.Readiness = domain.Readiness{}
					})
				}
				startCatalog()
			}
			if !catalogReady {
				if catalogKnown && client.Generation == generation {
					checkReceipts()
				}
				if catalogResults == nil && time.Since(lastAttempt) >= catalogRetry {
					lastAttempt = time.Now()
					catalogRetry = min(15*time.Second, 2*catalogRetry)
					startCatalog()
				}
				continue
			}
			checkReceipts()
			if !retryEventsAt.IsZero() && !time.Now().Before(retryEventsAt) {
				eventResults, stopEvents = c.observeEvents(ctx, client.DeviceID, generation, sequence)
				retryEventsAt = time.Time{}
			}
			if commandReplies != nil || readinessResults != nil {
				continue
			}
			preparePreferredPath()
			if service != nil && service.OperationID != "" &&
				(service.Phase == WorkerUpdating || service.Phase == WorkerWaiting) &&
				time.Since(lastUpgradeObservation) >= 3*time.Second {
				observeWorkerBuild()
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
				checkBackground("status", nil)
			}
			if readinessResults == nil && time.Since(lastReadiness) > 5*time.Second {
				lastReadiness = time.Now()
				pending := make(chan readinessObservation, 1)
				readinessResults = pending
				current, query := client, readinessQuery()
				c.networkWG.Add(1)
				go func() {
					defer c.networkWG.Done()
					started := time.Now()
					call, cancel := context.WithTimeout(ctx, 3*time.Second)
					defer cancel()
					observed := readinessObservation{client: current, query: query}
					observed.err = current.Call(call, "readiness", query, &observed.ready)
					observed.timing = &DeviceTiming{Milliseconds: float64(time.Since(started)) / float64(time.Millisecond), ObservedAt: time.Now()}
					pending <- observed
				}()
				continue
			}

			// Accepted lifecycle operations keep waiting on the device. Their
			// receipts are queried above, independently of automatic dispatch.
			legacyWaiting := service != nil && service.OperationID == "" && service.Phase == WorkerWaiting
			if (c.WorkerAutoUpdate && service.Automatic() && (service.Phase != WorkerWaiting || legacyWaiting) ||
				updateIntent == updateWaitingForIdle && legacyWaiting) && time.Since(lastUpdateAttempt) > 5*time.Second {
				restart, err := tryWorkerUpdate(false, nil)
				if restart || err != nil {
					disconnected()
					lastAttempt = time.Time{}
					continue
				}
			}
			// Older workers retain their existing polling path until upgraded.
			if client.Minor < 1 {
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

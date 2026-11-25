package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"shioji/internal/attachments"
	"shioji/internal/devicehealth"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
)

type Config struct {
	ServiceOperation                                string
	ServiceInstance                                 string
	StartPaused                                     bool
	RequestUpdate                                   func(domain.WorkerReplacement) (domain.WorkerUpdateResult, error) `json:"-"`
	legacyOwner                                     *Owner
	legacyExpected                                  *legacyMigrationRecord
	CheckStartupBuild                               func() error `json:"-"`
	BackgroundManaged                               bool
	RuntimeHosted                                   bool
	DisableBackground                               func(context.Context, string) (domain.BackgroundService, error) `json:"-"`
	ReplaceWorker                                   func(domain.WorkerReplacement) error                            `json:"-"`
	VerifyReplacement                               func(context.Context, domain.WorkerReplacement) error           `json:"-"`
	Directory, LauncherExecutable, DefaultHarnessID string
	Harnesses                                       []harness.Adapter `json:"-"`
}
type WorkspaceInput = protocol.WorkspaceInput
type SessionQuery = protocol.SessionQuery
type EventsQuery = protocol.EventsQuery
type EventPage = protocol.EventPage
type AnswerInput = protocol.AnswerInput
type Server struct {
	inputReceiptMu       sync.Mutex
	inputReceipts        map[string]int
	runtimeStarts        map[string]*runtimeStart
	drainMu              sync.Mutex
	drainOperation       string
	archiveWake          chan struct{}
	worktreeWake         chan struct{}
	worktreeCollection   *worktreeCollection
	titleMu              sync.Mutex
	titleExecutions      *managedexec.Manager
	titleActive          map[string]bool
	retirementMu         sync.Mutex
	retirements          map[string]*runtimeRetirement
	detachedRuntimes     map[string]*detachedRuntime
	health               *devicehealth.Monitor
	storageMu            sync.Mutex
	storageReport        *domain.StorageReport
	idleRuntimes         map[string]idleRuntimeObservation
	toolConfigMu         sync.RWMutex
	toolAuthMu           sync.Mutex
	toolAuth             map[string]*workerToolAuthorization
	signInMu             sync.Mutex
	signIns              map[string]*workerSignIn
	signInClosed         bool
	build                domain.WorkerBuild
	maintenance          sync.RWMutex
	updating             atomic.Bool
	backgroundStopping   atomic.Bool
	backgroundStop       *backgroundStop
	pressureSample       func() uint32
	pressureReclaim      chan struct{}
	runtimeMu            sync.RWMutex
	toolMu               sync.Mutex
	tools                map[string]recoveryToolProcess
	attachments          *attachments.Repository
	config               Config
	owner                *Owner
	listener             net.Listener
	store                *store.Store
	runtime              harness.Runtime
	runtimes             map[string]harness.Runtime
	runtimeCatalogs      map[string]harness.Runtime
	runtimeEvents        map[string]*runtimeEventGate
	adapters             map[string]harness.Adapter
	adapterOrder         []string
	agentProfiles        map[string]domain.AgentProfile
	systemAgentProfiles  map[string]systemAgentProfile
	profileProviders     map[string]harness.AgentProfileProvider
	catalogs             map[string]harness.Runtime
	catalogErrors        map[string]error
	catalogGates         map[string]*sync.RWMutex
	catalogStates        map[string]catalogState
	installations        map[string]domain.RuntimeInstallation
	catalogWake          chan struct{}
	runtimeSettingErrors map[string]error
	runtimeSettings      map[string]string
	runtimeOptions       map[string]domain.RuntimeOptions
	selectedHarnesses    []string
	rootCatalogID        string
	executions           *managedexec.Manager
	readiness            domain.Readiness
	dispatchMu           sync.Mutex
	coldStarts           chan struct{}
	steerDispatch        map[steerRuntimeKey]chan struct{}
	sessionTransitions   map[string]*sessionTransition
	worktreeTransitions  map[string]chan struct{}
	worktreeRetries      map[string]worktreeRetry
	queueWake            chan struct{}
	scheduleWake         chan struct{}
	titleWake            chan struct{}
	recoveryWake         chan struct{}
	recoveryChecked      map[string]recoveryAttempt
	restartRecovery      map[string]bool
	recoveryRefresh      atomic.Uint64
	bound                map[string]bool
	dispatchReservations map[string]string
	dispatchEpochs       map[string]uint64
	mu                   sync.Mutex
	conns                map[net.Conn]bool
	wg                   sync.WaitGroup
	once                 sync.Once
	closeErr             error
	ctx                  context.Context
	cancel               context.CancelFunc
}

func New(ctx context.Context, config Config) (*Server, error) {
	if config.LauncherExecutable == "" {
		var err error
		config.LauncherExecutable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	manager, err := managedexec.New(filepath.Join(config.Directory, "executions"), config.LauncherExecutable)
	if err != nil {
		return nil, err
	}
	owner := config.legacyOwner
	if owner == nil {
		owner, err = Acquire(config.Directory)
		if err != nil {
			return nil, err
		}
	}
	if config.ServiceInstance != "" {
		if err = archiveLegacyRecoveryFence(config.Directory); err != nil {
			owner.Close()
			return nil, err
		}
	}
	// Recheck under ownership: a managed service may already have been waiting
	// for the old process to exit when the migration was authorized.
	if pending, e := transport.PendingDeviceRecovery(config.Directory); e != nil || pending != nil {
		owner.Close()
		if e != nil {
			return nil, e
		}
		return nil, errors.New("执行服务恢复尚未完成，请查询原恢复操作")
	}
	if config.legacyExpected == nil {
		pending, e := transport.PendingWorkerMigration(config.Directory)
		if e != nil {
			owner.Close()
			return nil, e
		}
		if pending != nil {
			owner.Close()
			return nil, errors.New("设备迁移尚未完成，请继续同一次服务准备")
		}
	}
	if config.CheckStartupBuild != nil {
		if err = config.CheckStartupBuild(); err != nil {
			owner.Close()
			return nil, err
		}
	}
	db, err := store.Open(filepath.Join(config.Directory, "state.sqlite"))
	if err != nil {
		owner.Close()
		return nil, err
	}
	if config.legacyExpected != nil {
		snapshot, e := db.Snapshot()
		if e == nil {
			e = verifyLegacyCatalog(*config.legacyExpected, snapshot)
		}
		if e != nil {
			db.Close()
			owner.Close()
			return nil, e
		}
	}
	if err = db.Recover(); err != nil {
		db.Close()
		owner.Close()
		return nil, err
	}
	recoverable, err := db.RecoverableSessionIDs()
	if err != nil {
		db.Close()
		owner.Close()
		return nil, err
	}
	listener, err := owner.Listen()
	if err != nil {
		db.Close()
		owner.Close()
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &Server{attachments: attachments.New(filepath.Join(config.Directory, "attachments")), config: config, owner: owner, listener: listener, store: db, ctx: workerCtx, cancel: cancel, queueWake: make(chan struct{}, 1), recoveryWake: make(chan struct{}, 1), recoveryChecked: map[string]recoveryAttempt{}, bound: map[string]bool{}, conns: map[net.Conn]bool{}}
	s.restartRecovery = make(map[string]bool, len(recoverable))
	for _, id := range recoverable {
		s.restartRecovery[id] = true
	}
	s.build = runtimebuild.Current()
	s.build.Schema = store.SchemaVersion
	s.build.MigrationFrom = store.WorkerMigrationFrom
	if config.ServiceInstance != "" {
		s.build.PrivateControl = supervisor.PrivateControlVersion
	}
	s.updating.Store(config.StartPaused)
	s.build.SafeUpdate = config.ReplaceWorker != nil || config.RequestUpdate != nil
	if config.RequestUpdate != nil {
		s.build.Lifecycle = 1
	}
	s.build.Maintenance = config.ReplaceWorker != nil && config.VerifyReplacement != nil
	s.build.ContextUsage = true
	s.build.OutputFiles = true
	s.build.RecoveryHelper = true
	s.build.ForceRecovery = true
	s.tools = map[string]recoveryToolProcess{}
	s.titleWake = make(chan struct{}, 1)
	s.runtimes = map[string]harness.Runtime{}
	s.runtimeEvents = map[string]*runtimeEventGate{}
	s.runtimeCatalogs = map[string]harness.Runtime{}
	if err = s.configureAdapters(); err != nil {
		listener.Close()
		db.Close()
		owner.Close()
		return nil, err
	}
	s.build.Managed = config.BackgroundManaged
	s.build.RuntimeHost = domain.RuntimeHostRegistrationRefresh
	s.build.RuntimeHosted = config.RuntimeHosted
	s.executions = manager
	if err = s.recoverManualGuards(); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.recoverWorkerMaintenance(); err != nil {
		listener.Close()
		db.Close()
		owner.Close()
		return nil, err
	}
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 20*time.Second)
	cleanupErr := s.recoverManagedScopes(cleanupCtx)
	cancelCleanup()
	if cleanupErr != nil {
		s.cancel()
		listener.Close()
		db.Close()
		owner.Close()
		return nil, cleanupErr
	}
	if err := s.store.InvalidateWork(); err != nil {
		s.cancel()
		listener.Close()
		db.Close()
		owner.Close()
		return nil, err
	}
	s.wg.Add(1)
	go s.reapExecutions()
	s.wg.Add(1)
	go s.idleRuntimeLoop()
	s.wg.Add(1)
	go s.detachedRuntimeLoop()
	s.wg.Add(1)
	go s.watchRecoveryTools()
	for _, id := range s.adapterOrder {
		if !s.agentEnabled(id) {
			continue
		}
		startup, cancelStartup := context.WithTimeout(ctx, 20*time.Second)
		_, _ = s.openCatalog(startup, id, false)
		cancelStartup()
	}
	// Catalog startup already discovers enabled installations. Keep metadata
	// available for disabled/invalid configurations without probing twice.
	for _, id := range s.adapterOrder {
		s.runtimeMu.RLock()
		_, discovered := s.installations[id]
		s.runtimeMu.RUnlock()
		if !discovered {
			s.refreshInstallation(id, s.adapter(id))
		}
	}
	if !config.StartPaused {
		if err := s.migrateSystemAgentProfiles(); err != nil {
			return nil, errors.Join(err, s.Close())
		}
	}

	s.health = devicehealth.New(config.Directory)
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.health.Run(s.ctx) }()
	s.readiness = s.runtimeReadiness()
	s.catalogWake = make(chan struct{}, 1)
	s.wg.Add(1)
	go s.catalogLoop()
	s.wg.Add(1)
	go s.queueLoop()
	s.wg.Add(1)
	go s.monitorGit()
	s.wg.Add(1)
	go s.recoveryLoop()
	s.wg.Add(1)
	go s.titleLoop()
	s.wg.Add(1)
	go s.workerMaintenanceLoop()
	s.worktreeWake = make(chan struct{}, 1)
	s.wg.Add(1)
	go s.worktreeLoop()
	s.wg.Add(1)
	s.archiveWake = make(chan struct{}, 1)
	go s.archiveLoop()
	s.scheduleWake = make(chan struct{}, 1)
	s.wg.Add(1)
	go s.scheduleLoop()
	return s, nil
}
func (s *Server) Directory() string { return s.config.Directory }
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			conn.Close()
			return nil
		}
		s.conns[conn] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			defer func() { s.mu.Lock(); delete(s.conns, conn); s.mu.Unlock() }()
			s.serveConnection(conn)
		}()
	}
}
func (s *Server) serveConnection(conn net.Conn) {
	if authorizeLocal(conn) != nil {
		return
	}
	codec, epoch, err := protocol.WelcomeWorker(conn, s.store.DeviceID(), s.store.Generation(), s.build)
	if err != nil {
		return
	}
	requestedRecovery := false
	var history connectionHistory
	defer history.close()
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		var f protocol.Frame
		if err = codec.Read(&f); err != nil {
			return
		}
		if f.Type != "command" || f.Epoch != epoch || f.DeviceID != s.store.DeviceID() || f.ID == "" {
			return
		}
		if f.Method == protocol.TCPStreamMethod {
			s.serveTCP(conn, codec, f)
			return
		}
		if f.Method == "snapshot" && !requestedRecovery {
			s.recoveryRefresh.Add(1)
			s.wakeRecovery()
			requestedRecovery = true
		}
		out, callErr := history.handle(s, f.Method, f.Body)
		drain, _ := out.(domain.WorkerUpdateResult)
		stopForUpdate := callErr == nil && s.updating.Load() && (f.Method == "worker.drain" && drain.Ready || f.Method == "worker.update" && s.config.RequestUpdate == nil)
		if stopForUpdate {
			defer func() { s.cancel(); s.listener.Close() }()
		}
		result := protocol.Frame{Type: "result", ID: f.ID, Epoch: epoch, DeviceID: s.store.DeviceID()}
		if callErr != nil {
			result.Error = callErr.Error()
			var remote *protocol.RemoteError
			if errors.As(callErr, &remote) {
				result.ErrorCode = remote.Code
			}
			var unsupported *harness.UnsupportedError
			if errors.As(callErr, &unsupported) {
				result.ErrorCode = "unsupportedCapability"
			}
			if errors.Is(callErr, sql.ErrNoRows) {
				result.ErrorCode = "notFound"
			}
		} else {
			result.Body, err = json.Marshal(out)
			if err != nil {
				result.Error = "Could not encode response"
			}
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err = codec.Write(result); err != nil {
			if len(result.Body) > protocol.MaxFrame {
				result.Body = nil
				result.Error = "Response exceeds frame limit; request a smaller page"
				codec.Write(result)
			} else {
				return
			}
		}
		if stopForUpdate {
			return
		}
	}
}
func decode[T any](body []byte) (T, error) { var v T; err := json.Unmarshal(body, &v); return v, err }
func (s *Server) handle(method string, body []byte) (any, error) {
	return s.handleWithHistory(method, body, nil)
}

func (s *Server) handleWithHistory(method string, body []byte, history *store.HistorySnapshot) (any, error) {
	if strings.HasPrefix(method, "worker.local-") || method == "worker.prepare-drain" || method == "worker.drain" || method == "worker.resume" {
		return s.serviceControl(method, body)
	}
	switch method {
	case "worker.update.diagnose":
		return s.diagnoseWorkerUpdate()
	case "worker.processes.inspect":
		return InspectDeviceRecovery(s.ctx, s.config.Directory, s.store.DeviceID())
	case "worker.maintenance.apply", "worker.maintenance.cancel":
		in, err := decode[domain.WorkerMaintenanceInput](body)
		if err != nil {
			return nil, err
		}
		if method == "worker.maintenance.cancel" {
			return s.cancelWorkerMaintenance(in)
		}
		return s.applyWorkerMaintenance(in)
	case "worker.maintenance.get":
		in, err := decode[protocol.WorkerMaintenanceQuery](body)
		if err != nil {
			return nil, err
		}
		return s.workerMaintenanceStatus(in)
	}
	if method == "device.ping" {
		return struct {
			DeviceID string `json:"deviceId"`
		}{s.store.DeviceID()}, nil
	}
	if method == "worker.update.status" || method == "worker.update.cancel" {
		in, err := decode[struct {
			ID string `json:"id"`
		}](body)
		if err != nil {
			return nil, err
		}
		action := "status"
		if method == "worker.update.cancel" {
			action = "cancel"
		}
		return supervisor.Call(s.ctx, s.config.Directory, supervisor.Request{Action: action, ID: in.ID, DeviceID: s.store.DeviceID()})
	}
	if method == "worker.info" {
		return s.build, nil
	}
	if method == "worker.background.disable" {
		return nil, errors.New("请先更新客户端，再关闭后台运行")
	}
	if method == "worker.background.stop" {
		in, err := decode[protocol.BackgroundStopInput](body)
		if err != nil || in.ID == "" || filepath.Base(in.ID) != in.ID || len(in.ID) > 128 {
			return nil, errors.New("invalid background operation")
		}
		return s.disableBackground(in.ID)
	}
	if method == "worker.update" {
		in, err := decode[domain.WorkerReplacement](body)
		if err != nil {
			return nil, err
		}
		return s.prepareWorkerUpdate(in)
	}
	// Connection recovery can observe the last committed state during a drain.
	// These reads must not queue behind a mutation waiting for another session.
	switch method {
	case "events", "events.watch", "snapshot", "readiness", "operation", "operations", "device.health":
	default:
		s.maintenance.RLock()
		defer s.maintenance.RUnlock()
		if err := s.updateAdmission(); err != nil {
			return nil, err
		}
	}

	switch method {
	case "archive.policy":
		return s.store.ArchivePolicy()
	case "archive.configure":
		in, err := decode[protocol.ArchivePolicyInput](body)
		if err != nil {
			return nil, err
		}
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		return s.store.ConfigureArchive(in.Days, in.Revision)
	case "session.delete", "workspace.delete":
		in, err := decode[protocol.ArchiveDeleteInput](body)
		if err != nil {
			return nil, err
		}
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		return s.deleteArchive(method, in)
	case "device.health":
		return s.deviceHealth(), nil
	case "device.keepAwake":
		in, err := decode[domain.KeepAwakeInput](body)
		if err != nil {
			return nil, err
		}
		report, err := s.health.SetKeepAwake(in.Enabled, in.Revision)
		report.DeviceID = s.store.DeviceID()
		return report, err
	case "conversation.list", "conversation.preview":
		in, err := decode[protocol.ConversationQuery](body)
		if err != nil {
			return nil, err
		}
		if method == "conversation.list" {
			return s.listConversations(in)
		}
		return s.previewConversation(in)
	case "conversation.import":
		in, err := decode[protocol.ConversationImportInput](body)
		if err != nil {
			return nil, err
		}
		return s.importConversation(in)
	case "storage.usage", "storage.clean":
		query := struct {
			Refresh bool `json:"refresh"`
		}{Refresh: true} // Older clients have no cache option; preserve fresh reads.
		if len(body) > 0 {
			if err := json.Unmarshal(body, &query); err != nil {
				return nil, err
			}
		}
		return s.storage(method == "storage.clean", query.Refresh)
	case "runtime.tools.inspect":
		in, err := decode[protocol.ToolInspectionInput](body)
		if err != nil {
			return nil, err
		}
		return s.inspectToolServices(in)
	case "runtime.skills.list", "runtime.skills.read", "runtime.skills.change":
		in, err := decode[protocol.SkillManagementInput](body)
		if err != nil {
			return nil, err
		}
		return s.manageSkills(method, in)
	case "runtime.hooks.list", "runtime.hooks.change", "runtime.hooks.operation":
		in, err := decode[domain.HookRequest](body)
		if err != nil {
			return nil, err
		}
		return s.manageHooks(method, in)
	case "runtime.tools.change":
		in, err := decode[protocol.ToolServiceInput](body)
		if err != nil {
			return nil, err
		}
		return s.changeToolService(in)
	case "device.directories":
		in, err := decode[protocol.DirectoryQuery](body)
		if err != nil {
			return nil, err
		}
		return s.directories(in)
	case "runtime.tools.list", "runtime.tools.refresh":
		in, err := decode[protocol.ToolServiceInput](body)
		if err != nil {
			return nil, err
		}
		return s.listToolServices(in, method == "runtime.tools.refresh")
	case "runtime.tools.auth.start", "runtime.tools.auth.status":
		in, err := decode[protocol.ToolServiceInput](body)
		if err != nil {
			return nil, err
		}
		return s.toolAuthorizationCommand(method, in)
	case "runtime.catalog":
		return s.runtimeProfiles(nil, true), nil
	case "runtime.profile.create":
		in, err := decode[domain.AgentProfile](body)
		if err != nil {
			return nil, err
		}
		return s.createAgentProfile(in)
	case "runtime.agent.selection":
		in, err := decode[protocol.RuntimeAgentSelection](body)
		if err != nil {
			return nil, err
		}
		return s.selectRuntimeAgent(in)
	case "runtime.signin.start", "runtime.signin.status", "runtime.signin.cancel", "runtime.signin.code":
		in, err := decode[protocol.SignInInput](body)
		if err != nil {
			return nil, err
		}
		return s.signInCommand(method, in)
	case "runtime.agents":
		in, err := decode[protocol.DeviceAgents](body)
		if err != nil {
			return nil, err
		}
		return s.configureAgents(in)
	case "runtime.configure":
		in, err := decode[protocol.RuntimeConfiguration](body)
		if err != nil {
			return nil, err
		}
		return s.configureRuntime(in)
	case "runtime.settings", "runtime.settings.environment":
		in, err := decode[protocol.RuntimeSettingsQuery](body)
		if err != nil {
			return nil, err
		}
		return s.readRuntimeSettings(in, method == "runtime.settings.environment")
	case "runtime.settings.change":
		in, err := decode[protocol.RuntimeSettingsChange](body)
		if err != nil {
			return nil, err
		}
		return s.changeRuntimeSettings(in)
	case "runtime.check":
		in, err := decode[protocol.RuntimeConfiguration](body)
		if err != nil {
			return nil, err
		}
		return s.recheckRuntime(in.HarnessID)
	case "skills.list":
		q, err := decode[protocol.SkillQuery](body)
		if err != nil {
			return nil, err
		}
		return s.listSkills(q)
	case "file.resolve", "file.read":
		q, err := decode[protocol.FileResourceQuery](body)
		if err != nil {
			return nil, err
		}
		return s.fileResourceCommand(method, q)
	case "file.search":
		q, err := decode[protocol.FileSearchQuery](body)
		if err != nil {
			return nil, err
		}
		return s.searchFiles(q)
	case "file.snapshot":
		q, err := decode[protocol.FileSnapshotQuery](body)
		if err != nil {
			return nil, err
		}
		return s.snapshotFile(q)
	case "session.reconcile":
		in, err := decode[protocol.RecoveryInput](body)
		if err != nil {
			return nil, err
		}
		return s.reconcile(in)
	case "attachment.put", "attachment.get":
		return s.attachmentCommand(method, body)
	case "session.completion.read":
		in, err := decode[protocol.CompletionReadInput](body)
		if err != nil {
			return nil, err
		}
		return s.store.ReadCompletion(in.SessionID, in.CompletionID)
	case "session.permission":
		in, err := decode[protocol.SessionPermissionInput](body)
		if err != nil {
			return nil, err
		}
		return s.changeSessionPermission(in)
	case "session.rename", "session.reset-title", "session.archive":
		in, err := decode[protocol.SessionChangeInput](body)
		if err != nil {
			return nil, err
		}
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		if method == "session.archive" {
			gen, err := s.archiveGeneration(in.SessionID)
			if err != nil {
				return nil, err
			}
			result, err := s.store.SetSessionArchived(in.SessionID, in.Revision, in.Archived, gen)
			if err == nil && in.Archived {
				delete(s.restartRecovery, in.SessionID)
				s.wakeArchiveRetirement()
			}
			return result, err
		}
		return s.store.RenameSession(in.SessionID, in.Title, in.TitleRevision, method == "session.reset-title")
	case "workspace.archive":
		in, err := decode[protocol.WorkspaceArchiveInput](body)
		if err != nil {
			return nil, err
		}
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		generations := map[string]string{}
		snapshot, err := s.store.Snapshot()
		if err != nil {
			return nil, err
		}
		for _, session := range snapshot.Sessions {
			if session.WorkspaceID == in.WorkspaceID {
				gen, e := s.archiveGeneration(session.ID)
				if e != nil {
					return nil, e
				}
				generations[session.ID] = gen
			}
		}
		result, err := s.store.SetWorkspaceArchived(in.WorkspaceID, in.Revision, in.Archived, generations)
		if err == nil && in.Archived {
			for id := range generations {
				delete(s.restartRecovery, id)
			}
			s.wakeArchiveRetirement()
		}
		return result, err
	case "snapshot":
		s.wakeRecovery()
		return s.store.Snapshot()
	case "schedule.list", "schedule.get", "schedule.zone", "schedule.save", "schedule.delete", "schedule.run":
		return s.handleSchedule(method, body)
	case "readiness":
		var query protocol.ReadinessQuery
		if len(body) > 0 {
			if err := json.Unmarshal(body, &query); err != nil {
				return nil, err
			}
		}
		if len(query.SessionIDs) > 2 {
			return nil, errors.New("最多同时查看两个会话的运行能力")
		}
		s.wakeCatalog()
		return s.runtimeReadiness(query.SessionIDs...), nil
	case "session.work.send", "session.work.operation":
		in, err := decode[domain.WorkInput](body)
		if err != nil {
			return nil, err
		}
		if method == "session.work.send" {
			return s.sendWork(in)
		}
		return s.queryWorkInput(in)
	case "session.work.stop":
		q, err := decode[protocol.WorkQuery](body)
		if err != nil {
			return nil, err
		}
		work, err := s.store.Work(q.SessionID, q.ID)
		if err != nil {
			return nil, err
		}
		if !work.Work.Active() || !work.Work.CanStop {
			return nil, harness.Unsupported("independent-work-stop")
		}
		detail, err := s.store.Session(q.SessionID, 0, 1)
		if err != nil {
			return nil, err
		}
		r := s.currentRuntime(q.SessionID)
		if r == nil || r.Stopped() || !r.Observes(detail.Session) {
			return nil, errors.New("此任务没有可确认的运行实例")
		}
		controller, ok := r.(harness.WorkController)
		if !ok {
			return nil, harness.Unsupported("independent-work-stop")
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		return nil, controller.StopWork(ctx, detail.Session, q.ID)
	case "session.work.history":
		q, err := decode[domain.WorkHistoryQuery](body)
		if err != nil {
			return nil, err
		}
		if history != nil {
			return history.Work(q)
		}
		return s.store.WorkHistory(q)
	case "session.work":
		q, err := decode[protocol.WorkQuery](body)
		if err != nil {
			return nil, err
		}
		if q.SessionID == "" || q.ID == "" || len(q.SessionID) > 256 || len(q.ID) > 1024 {
			return nil, errors.New("无效的任务查询")
		}
		return s.store.Work(q.SessionID, q.ID)
	case "session":
		q, err := decode[SessionQuery](body)
		if err != nil {
			return nil, err
		}
		return s.store.Session(q.ID, q.After, q.Limit)
	case "session.history":
		q, err := decode[domain.HistoryQuery](body)
		if err != nil {
			return nil, err
		}
		if history != nil {
			return history.Read(q)
		}
		return s.store.SessionHistory(q)
	case "session.outline":
		q, err := decode[domain.HistoryQuery](body)
		if err != nil {
			return nil, err
		}
		return s.store.HistoryOutline(q)
	case "session.matches":
		q, err := decode[domain.HistorySearchQuery](body)
		if err != nil {
			return nil, err
		}
		return s.store.HistoryMatches(q)
	case "operation":
		q, err := decode[protocol.OperationQuery](body)
		if err != nil {
			return nil, err
		}
		return s.inputReceipt(q)
	case "operations":
		q, err := decode[protocol.OperationBatchQuery](body)
		if err != nil {
			return nil, err
		}
		if len(q.Operations) > 8 {
			return nil, errors.New("too many operation receipts")
		}
		var receipts []domain.Operation
		for _, lookup := range q.Operations {
			op, err := s.inputReceipt(lookup)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			receipts = append(receipts, op)
		}
		return receipts, nil
	case "session.reload", "session.reload.operation":
		in, err := decode[domain.SessionReloadRequest](body)
		if err != nil {
			return nil, err
		}
		return s.reloadSession(in, method == "session.reload.operation")
	case "session.goal.control":
		in, err := decode[domain.GoalRequest](body)
		if err != nil {
			return nil, err
		}
		return s.goalControl(in)
	case "session.context.compact", "session.context.operation":
		in, err := decode[domain.ContextActionRequest](body)
		if err != nil {
			return nil, err
		}
		var op domain.Operation
		if method == "session.context.operation" {
			op, err = s.queryContextOperation(in)
		} else {
			op, err = s.compactContext(in)
		}
		if err != nil {
			return nil, err
		}
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return nil, err
		}
		return domain.ContextActionResult{Operation: op, Session: d.Session}, nil
	case "events", "events.watch":
		q, err := decode[EventsQuery](body)
		if err != nil {
			return nil, err
		}
		var wait time.Duration
		if method == "events.watch" {
			wait = time.Duration(max(1, min(q.WaitMS, 15_000))) * time.Millisecond
		}
		if err := s.store.WaitEvents(s.ctx, q.After, q.Generation, wait); err != nil {
			return nil, err
		}
		if method == "events.watch" {
			// Coalesce token bursts for one native display frame. This wait uses
			// the observation connection; commands remain independently available.
			timer := time.NewTimer(12 * time.Millisecond)
			select {
			case <-s.ctx.Done():
				timer.Stop()
				return nil, s.ctx.Err()
			case <-timer.C:
			}
		}
		head, err := s.store.JournalSequence()
		if err != nil {
			return nil, err
		}
		events, err := s.store.Events(q.After, 100)
		if err != nil {
			return nil, err
		}
		page := EventPage{Generation: q.Generation, Sequence: head}
		size := 0
		for _, e := range events {
			b, _ := json.Marshal(e)
			if e.Sequence > head || size+len(b) > 768<<10 {
				break
			}
			size += len(b)
			page.Events = append(page.Events, e)
		}
		return page, nil
	case "workspace.create":
		in, err := decode[WorkspaceInput](body)
		if err != nil {
			return nil, err
		}
		folders, err := validateFolders(in.Folders)
		if err != nil {
			return nil, err
		}
		return s.store.CreateWorkspace(in.Name, folders)
	case "workspace.update":
		in, err := decode[domain.Workspace](body)
		if err != nil {
			return nil, err
		}
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		folders, err := validateFolders(in.Folders)
		if err != nil {
			return nil, err
		}
		in.Folders = folders
		return s.store.UpdateWorkspace(in)
	case "session.search":
		q, err := decode[protocol.SearchQuery](body)
		if err != nil {
			return nil, err
		}
		return s.store.Search(q.Query)
	case "git.status":
		q, err := decode[protocol.GitQuery](body)
		if err != nil {
			return nil, err
		}
		return s.gitStatus(q)
	case "git.checkout":
		in, err := decode[protocol.CheckoutInput](body)
		if err != nil {
			return nil, err
		}
		return s.checkout(in)
	case "git.confirm":
		in, err := decode[protocol.ConfirmCheckoutInput](body)
		if err != nil {
			return nil, err
		}
		return s.updateCheckout(in.CheckoutInput, &in.Head)
	case "diff.snapshot.retain":
		q, err := decode[protocol.DiffSnapshotInput](body)
		if err != nil {
			return nil, err
		}
		return nil, s.retainDiffSnapshot(q)
	case "diff.comment":
		q, err := decode[protocol.DiffCommentInput](body)
		if err != nil {
			return nil, err
		}
		return s.captureDiffComment(q)
	case "git.compare", "git.refs":
		q, err := decode[protocol.GitQuery](body)
		if err != nil {
			return nil, err
		}
		if method == "git.refs" {
			return s.comparisonRefs(q)
		}
		return s.compareGit(q)
	case "git.changes":
		q, err := decode[protocol.GitQuery](body)
		if err != nil {
			return nil, err
		}
		return s.changedFiles(q)
	case "git.file":
		q, err := decode[protocol.FileQuery](body)
		if err != nil {
			return nil, err
		}
		return s.fileVersions(q)
	case "turn.enqueue":
		in, err := decode[domain.Submission](body)
		if err != nil {
			return nil, err
		}
		if in.QuestionReply != nil {
			return nil, errors.New("请使用问题的回答入口")
		}
		return s.enqueue(in)
	case "turn.steer":
		in, err := decode[protocol.SteerInput](body)
		if err != nil {
			return nil, err
		}
		if in.Input.QuestionReply != nil {
			return nil, errors.New("请使用问题的回答入口")
		}
		return s.steer(in)
	case "queue.edit", "queue.remove", "queue.continue", "queue.send", "queue.reorder":
		in, err := decode[protocol.QueueInput](body)
		if err != nil {
			return nil, err
		}
		return s.changeQueue(method, in)
	case "session.fork", "session.restart":
		in, err := decode[domain.HistoryChange](body)
		if err != nil {
			return nil, err
		}
		if in.Restart != (method == "session.restart") {
			return nil, errors.New("历史操作类型不一致")
		}
		return s.changeHistory(in)
	case "session.cancel-history":
		in, err := decode[protocol.HistoryOperationQuery](body)
		if err != nil {
			return nil, err
		}
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		return s.store.CancelHistory(in.ID, in.SessionID)
	case "session.history-operation":
		in, err := decode[protocol.HistoryOperationQuery](body)
		if err != nil {
			return nil, err
		}
		op, err := s.store.HistoryOperation(in.ID)
		if err == nil && op.Input.SessionID != in.SessionID {
			return nil, errors.New("历史操作不属于此会话")
		}
		return op, err
	case "session.submit":
		request, err := decode[protocol.SubmissionInput](body)
		if err != nil {
			return nil, err
		}
		in := request.Submission
		if in.QuestionReply != nil {
			return nil, errors.New("请使用问题的回答入口")
		}
		if strings.HasPrefix(in.OperationID, "scheduled:") {
			return nil, errors.New("定时运行标识仅供所属设备的调度器使用")
		}
		if request.ReceiveBeforePrepare && in.SessionID == "" {
			return s.receiveHomeSubmission(in)
		}
		return s.submit(in)
	case "question.reply":
		in, err := decode[protocol.QuestionReplyInput](body)
		if err != nil {
			return nil, err
		}
		return s.replyToQuestion(in)
	case "request.respond":
		in, err := decode[AnswerInput](body)
		if err != nil {
			return nil, err
		}
		return nil, s.respondRequest(in)
	case "turn.interrupt":
		in, err := decode[struct {
			SessionID   string `json:"sessionId"`
			TurnID      string `json:"turnId"`
			OperationID string `json:"operationId,omitempty"`
		}](body)
		if err != nil {
			return nil, err
		}
		return nil, s.interruptTurn(in.SessionID, in.TurnID, in.OperationID)

	default:
		return nil, errors.New("unsupported worker command")
	}
}
func validateFolders(paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > 32 {
		return nil, errors.New("select a primary directory and at most 31 additional directories")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, errors.New("directory must be absolute on the owning device")
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return nil, errors.New("workspace directory is unavailable")
		}
		dir, err := os.Open(canonical)
		if err != nil {
			return nil, err
		}
		_, err = dir.Readdirnames(1)
		dir.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out, nil
}
func (s *Server) submit(in domain.Submission) (domain.Operation, error) {
	if strings.HasPrefix(in.OperationID, domain.GoalContinuationPrefix) {
		return domain.Operation{}, errors.New("无效的输入标识")
	}
	if in.OperationID == "" || len(in.OperationID) > 256 || in.ValidateContent() != nil {
		return domain.Operation{}, errors.New("submission requires an operation ID and nonempty bounded text")
	}
	defer s.beginInputReceipt(in.OperationID)()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.submitLocked(in, 0, false)
}
func (s *Server) submitLocked(in domain.Submission, queueRevision int64, immediate bool) (_ domain.Operation, failure error) {
	return s.prepareSubmissionLocked(in, queueRevision, immediate, nil, nil)
}

func (s *Server) prepareSubmissionLocked(in domain.Submission, queueRevision int64, immediate bool, received *domain.Workspace, goal *domain.Goal) (_ domain.Operation, failure error) {
	if err := s.waitSessionTransition(s.ctx, in.SessionID); err != nil {
		return domain.Operation{}, err
	}
	if err := s.updateAdmission(); err != nil {
		return domain.Operation{}, err
	}
	_, err := s.store.Operation(in.OperationID)
	if err == nil && received == nil {
		if queueRevision > 0 {
			return s.store.AcceptQueued(in, queueRevision)
		}
		return s.store.Accept(in)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, err
	}
	if err := store.CheckSubmissionJournal(in); err != nil {
		return domain.Operation{}, err
	}
	if in.SessionID == "" && in.Git.Branch != "" && !in.Git.NewWorktree {
		if warning, e := s.store.SubmissionGitWarning(in.OperationID); e != nil {
			return domain.Operation{}, e
		} else if warning != "" {
			return domain.Operation{}, errors.New(warning)
		}
		defer func() {
			if failure == nil {
				return
			}
			if warning, e := s.store.SubmissionGitWarning(in.OperationID); e == nil && warning != "" {
				failure = fmt.Errorf("%s %w", warning, failure)
			}
		}()
	}
	if in.SessionID != "" {
		if err = s.cleanRuntimeRetirements(in.SessionID); err != nil {
			return domain.Operation{}, err
		}
	}
	if err = s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return domain.Operation{}, err
	}
	releasePreparation := s.reservePreparation(in.SessionID)
	defer releasePreparation()
	var preparation *sessionTransition
	if in.SessionID != "" {
		release, err := s.beginSessionTransition(s.ctx, in.SessionID)
		if err != nil {
			return domain.Operation{}, err
		}
		defer release()
		preparation = s.sessionTransitions[in.SessionID]
	}
	if _, err = s.attachmentInputsOutsideDispatch(in); err != nil {
		return domain.Operation{}, err
	}
	id, err := s.inputHarness(in)
	if err != nil {
		return domain.Operation{}, err
	}
	catalog, release, err := s.borrowCatalog(id)
	if err != nil {
		return domain.Operation{}, err
	}
	defer release()
	if err = validateAvailableConfiguration(catalog, in.Config); err != nil {
		return domain.Operation{}, err
	}
	if in.GoalObjective != "" && !catalog.Readiness().Capabilities.Goals {
		return domain.Operation{}, harness.Unsupported("goal")
	}
	if in.Continue && !catalog.Readiness().Capabilities.Continue {
		return domain.Operation{}, harness.Unsupported("continue")
	}
	workspace, err := s.store.Workspace(in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, errors.New("workspace unavailable")
	}
	if err != nil {
		return domain.Operation{}, err
	}
	if workspace.Archived {
		return domain.Operation{}, errors.New("请先恢复工作区")
	}
	if received != nil && workspace.Revision != received.Revision {
		return domain.Operation{}, errors.New("工作区在准备会话期间已变化，消息未发送")
	}
	paths := workspace.Folders
	if in.Continue && in.SessionID == "" {
		return domain.Operation{}, errors.New("继续需要原会话标识")
	}
	if in.SessionID != "" {
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return domain.Operation{}, err
		}
		if d.Session.WorkspaceID != in.WorkspaceID {
			return domain.Operation{}, errors.New("session is not in this workspace")
		}
		if d.Session.Archived {
			return domain.Operation{}, errors.New("请先恢复会话")
		}
		if d.Session.Origin != nil && d.Session.Origin.ResumeRef == "" && d.Session.ThreadID == "" {
			return domain.Operation{}, errors.New("此导入历史仅可查看")
		}
		if in.GoalObjective != "" && d.Session.ThreadID != "" && in.Config != d.Session.Config {
			return domain.Operation{}, errors.New("目标使用会话已接纳的运行配置")
		}
		if !d.Session.Status.CanSubmit() || d.Session.Goal.Active() != (goal != nil) || goal != nil && *d.Session.Goal != *goal {
			return domain.Operation{}, errors.New("session is unavailable for a new turn")
		}
		if in.Continue {
			if d.Session.ThreadID == "" || d.Session.Recovery == nil || !d.Session.Recovery.CanContinue {
				return domain.Operation{}, errors.New("原线程尚不能继续，请重新核对")
			}
		}
		if queueRevision == 0 && !in.Continue && goal == nil {
			q, e := s.store.Queue(in.SessionID)
			if e != nil {
				return domain.Operation{}, e
			}
			if len(q.Items) > 0 {
				return domain.Operation{}, errors.New("session has queued messages")
			}
		}
		if queueRevision > 0 || in.Continue || goal != nil {
			for _, req := range d.Requests {
				if req.State.AwaitingResolution() {
					return domain.Operation{}, errors.New("session has pending requests")
				}
			}
			busy, e := s.store.HasUnsettledOperation(in.SessionID)
			if e != nil {
				return domain.Operation{}, e
			}
			if busy {
				return domain.Operation{}, errors.New("session has unsettled input")
			}
		}
		paths = append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
	}
	if err := s.restoreOwnedWorktrees(paths); err != nil {
		return domain.Operation{}, err
	}
	validated, err := validateFolders(paths)
	if err != nil {
		return domain.Operation{}, err
	}
	if strings.Join(validated, "\x00") != strings.Join(paths, "\x00") {
		return domain.Operation{}, errors.New("execution directory identity changed")
	}
	if err := s.admitMemoryPressure(in.SessionID); err != nil {
		return domain.Operation{}, err
	}
	launch := domain.LaunchContext{DeviceID: s.store.DeviceID(), CWD: paths[0], AdditionalDirectories: append([]string(nil), paths[1:]...)}
	launch, err = s.prepareGitLocked(in, launch)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := s.updateAdmission(); err != nil {
		return domain.Operation{}, err
	}
	if preparation != nil && !preparation.active() {
		return domain.Operation{}, errSessionTransitionRetired
	}
	if in.SessionID == "" {
		current, e := s.store.Workspace(in.WorkspaceID)
		if e != nil {
			return domain.Operation{}, e
		}
		if strings.Join(current.Folders, "\x00") != strings.Join(paths, "\x00") {
			return domain.Operation{}, errors.New("工作区执行目录已改变，输入未发送")
		}
		if received != nil && current.Revision != received.Revision {
			return domain.Operation{}, errors.New("工作区在准备会话期间已变化，消息未发送")
		}
	}
	var op domain.Operation
	if queueRevision > 0 && immediate {
		op, err = s.store.AcceptQueuedNow(in, queueRevision)
	} else if queueRevision > 0 {
		op, err = s.store.AcceptQueued(in, queueRevision)
	} else {
		id, e := s.inputHarness(in)
		if e != nil {
			return domain.Operation{}, e
		}
		if goal != nil {
			op, err = s.store.AcceptManagedGoalContinuation(in, goal, launch)
		} else if received == nil {
			op, err = s.store.AcceptForHarness(in, id, launch)
		} else {
			op, err = s.store.AcceptReceivedForHarness(in, id, launch)
		}
	}
	if err != nil {
		if in.Git.NewWorktree {
			s.removeUnacceptedWorktree(in)
		}
		return op, err
	}
	delete(s.restartRecovery, op.SessionID)
	if in.SessionID == "" {
		select {
		case s.titleWake <- struct{}{}:
		default:
		}
	}
	releaseDispatch := s.reserveDispatch(op)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer releaseDispatch()
		s.dispatchMu.Lock()
		current, err := s.store.Operation(op.ID)
		if err != nil || current.State != "accepted" || s.ctx.Err() != nil {
			s.dispatchMu.Unlock()
			return
		}
		startup := &sessionStartup{operationID: op.ID}
		runtime, startErr := s.openSessionRuntime(s.ctx, op.SessionID, startup)
		if startErr != nil && !errors.Is(startErr, errRuntimeStartCancelled) {
			s.failRuntimeLaunch(op, startErr)
		}
		s.dispatchMu.Unlock()
		if startErr == nil {
			s.dispatchWithStartup(in, op, runtime, startup)
		}
	}()
	return op, nil
}
func (s *Server) dispatch(in domain.Submission, op domain.Operation, runtime harness.Runtime) {
	s.dispatchWithStartup(in, op, runtime, nil)
}

func (s *Server) dispatchWithStartup(in domain.Submission, op domain.Operation, runtime harness.Runtime, startup *sessionStartup) {
	defer startup.finish()
	s.dispatchMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.dispatchMu.Unlock()
		}
	}()
	if !s.dispatchReserved(op, runtime) {
		return
	}
	defer s.wakeQueue()
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	detail, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		return
	}
	session := detail.Session
	s.runtimeMu.RLock()
	gate := s.runtimeEvents[session.ID]
	s.runtimeMu.RUnlock()
	if gate == nil || !gate.Load() {
		return
	}
	s.dispatchMu.Unlock()
	locked = false
	// A retired native call no longer owns the configuration boundary. Its
	// captured generation stays fenced, and late writes are rejected below.
	s.toolConfigMu.RLock()
	type result struct {
		session domain.Session
		turnID  string
		handed  bool
		err     error
		receipt <-chan error
	}
	settled := make(chan result, 1)
	go func(session domain.Session) {
		var turnID string
		var receipt <-chan error
		var err error
		handed := false
		if !gate.Load() {
			err = errRuntimeRetired
		} else if session.ThreadID == "" {
			if session.Origin != nil {
				if importer, ok := runtime.(harness.ConversationContinuation); ok {
					session.ThreadID, err = importer.PrepareConversation(ctx, session)
				} else {
					err = harness.Unsupported("imported-conversation-continuation")
				}
			} else if in.GoalObjective != "" && !runtime.Readiness().Capabilities.WorkerGoals {
				if goalRuntime, ok := runtime.(harness.GoalController); ok && runtime.Readiness().Capabilities.Goals {
					session.ThreadID, err = goalRuntime.CreateGoalThread(ctx, session)
				} else {
					err = harness.Unsupported("goal")
				}
			} else {
				session.ThreadID, err = runtime.Create(ctx, session)
			}
			if err == nil {
				err = gate.apply(func() error {
					return s.store.BindPreparedInput(op.ID, session)
				})
			}
			if err == nil && session.Origin != nil {
				err = runtime.Resume(ctx, session)
			}
		} else {
			s.mu.Lock()
			bound := s.bound[session.ID]
			s.mu.Unlock()
			if !bound {
				err = runtime.Resume(ctx, session)
				if err == nil && gate.Load() {
					if restorer, ok := runtime.(harness.WorkRestorer); ok {
						restorer.RestoreWork(session, detail.Works)
					}
				}
			}
		}
		startup.finish()
		if err == nil {
			var inputs []harness.AttachmentInput
			inputs, err = s.attachmentInputs(in)
			if err == nil {
				err = gate.apply(func() error {
					s.mu.Lock()
					s.bound[session.ID] = true
					s.mu.Unlock()
					return nil
				})
			}
			if err == nil {
				commit := func() error { return s.store.BeginInputHandoff(op.ID) }
				claim := func() error {
					err := s.claimInput(gate, runtime, op, commit)
					handed = err == nil
					return err
				}
				input := harness.TurnInput{Text: in.Text, Attachments: inputs, Skills: in.Skills, QuestionReply: in.QuestionReply}
				if session.PortableFork {
					var prefix domain.ForkContext
					prefix, err = s.store.PortableForkContext(session.ID, op.ID)
					if err != nil {
						settled <- result{session: session, err: err}
						return
					}
					if len(prefix.Entries) > 0 || prefix.Omitted {
						input.ForkContext = &prefix
					}
				}
				workerGoal := runtime.Readiness().Capabilities.WorkerGoals
				firstGoal := workerGoal && in.GoalObjective != ""
				continueGoal := workerGoal && session.Goal.Managed() && session.Goal.Active() && session.Goal.RunID == op.ID
				if strings.HasPrefix(op.ID, domain.GoalContinuationPrefix) && !continueGoal {
					settled <- result{session: session, err: errors.New("目标已暂停或改变，本次续跑未发送")}
					return
				}
				if firstGoal || continueGoal {
					goalID, objective := op.ID, in.GoalObjective
					if continueGoal {
						goalID, objective = session.Goal.ID, session.Goal.Objective
					}
					commit = func() error {
						var before *domain.Goal
						if continueGoal {
							before = session.Goal
						}
						return s.store.BeginGoalInputHandoff(op.ID, session.ThreadID, in.GoalObjective, before)
					}
					input.Goal = &domain.GoalProgress{GoalID: goalID, RunID: op.ID}
					input.Objective = objective
					input.Text = ""
				}
				if deferred, ok := runtime.(harness.DeferredInputRuntime); ok && (in.GoalObjective == "" || workerGoal) {
					turnID, receipt, err = deferred.SubmitDeferred(ctx, session, op.ID, input, claim)
				} else if delayed, ok := runtime.(harness.InputHandoffRuntime); ok && (in.GoalObjective == "" || workerGoal) {
					turnID, err = delayed.SubmitWithHandoff(ctx, session, op.ID, input, claim)
				} else {
					err = claim()
					if err == nil {
						if in.GoalObjective != "" {
							err = s.startGoal(ctx, runtime, session, in.GoalObjective, gate)
						} else {
							turnID, err = runtime.Submit(ctx, session, op.ID, input)
						}
					}
				}
			}
		}
		settled <- result{session: session, turnID: turnID, handed: handed, err: err, receipt: receipt}
	}(session)
	var turnID string
	var handed bool
	var receipt <-chan error
	select {
	case out := <-settled:
		session, turnID, handed, err, receipt = out.session, out.turnID, out.handed, out.err, out.receipt
	case <-gate.done:
		err = errRuntimeRetired
	}
	s.toolConfigMu.RUnlock()
	if receipt != nil && err == nil {
		err = awaitNativeInput(s.ctx, runtime, receipt)
		if s.ctx.Err() != nil {
			return
		}
	}

	s.dispatchMu.Lock()
	locked = true
	if errors.Is(err, errRuntimeRetired) || errors.Is(err, store.ErrInputHandoffRevoked) {
		return
	}
	if current := s.currentRuntime(session.ID); current == nil || current.Done() != runtime.Done() {
		return // The retired owner cannot settle a successor's execution.
	}
	if handed && err != nil && runtime.Stopped() && !harness.IsRejected(err) {
		err = &harness.DeliveryError{Uncertain: true, Message: "运行环境已结束，输入结果待核对；不会自动重发"}
	}
	outcome := domain.DeliveryOutcome(err != nil, handed && harness.IsUncertain(err))
	message := ""
	if err != nil {
		s.reportRuntimeSetup(session.ID, runtime, err)
		message = err.Error()
	}
	if outcome.Operation == domain.OperationIndeterminate {
		defer s.wakeRecovery()
	}
	if !handed && errors.Is(err, harness.ErrConversationSourceChanged) {
		err = s.store.RejectChangedConversationInput(op.ID, message)
	} else {
		err = s.store.CompleteInput(op.ID, outcome, message, turnID)
	}
	if err != nil && !errors.Is(err, store.ErrInputHandoffRevoked) {
		s.cancel()
	}
	if outcome.Operation == domain.OperationRejected && (in.GoalObjective != "" || strings.HasPrefix(op.ID, domain.GoalContinuationPrefix)) {
		if err := s.store.BlockManagedGoal(session.ID, op.ID); err != nil {
			s.cancel()
		}
	}
}
func (s *Server) onEvent(e domain.Event) error {
	if e.SessionID == "" {
		return nil
	}
	s.trackRecoveryTool(e)
	lost := e.Type == "runtime.lost"
	if lost && s.runtimeRetiring(e.SessionID) {
		// The exact instance ended before its invalidated work observations.
		// Keep the parent state, but publish an interrupted background outcome first.
		return s.store.InterruptCompletion(e.SessionID)
	}
	if lost {
		if before, err := s.store.SessionRecord(e.SessionID); err == nil && before.Goal.Managed() && before.Goal.RunID != "" {
			if err := s.store.BlockManagedGoal(e.SessionID, before.Goal.RunID); err != nil {
				return err
			}
		}
		if err := s.store.InterruptCompletion(e.SessionID); err != nil {
			return err
		}
		if err := s.guardManagedExecution(e.SessionID); err != nil {
			return err
		}
		detail, err := s.store.Session(e.SessionID, 0, 1)
		if err != nil {
			return err
		}
		session := detail.Session
		if !session.Status.Active() {
			return nil
		}
		for _, turn := range detail.Turns {
			if turn.Status == "running" {
				turn.Status = "unknown"
				turn.Error = "Agent process stopped; the in-flight turn needs reconciliation"
				if _, err := s.store.Apply(e.SessionID, domain.Event{Type: "turn", Turn: &turn}); err != nil {
					return err
				}
			}
		}
		session.Status = "unknown"
		session.Error = "Agent process stopped; the in-flight turn needs reconciliation"
		e.Type = "session"
		e.Session = &session
	}
	if (e.Turn != nil && e.Turn.Presentation != "compaction" && e.Turn.Status != "running" && e.Turn.Status != "completed") || lost || (e.Session != nil && e.Session.ContextChanged) {
		if _, err := s.store.PauseQueue(e.SessionID, "execution needs confirmation"); err != nil {
			return err
		}
	}
	_, err := s.store.Apply(e.SessionID, e)
	if err == nil && (e.Turn != nil || e.Type == "request.resolve" || e.Type == "goal") {
		s.wakeRecovery()
		s.wakeQueue()
	}
	return err
}
func (s *Server) Close() error {
	s.once.Do(func() { s.closeErr = s.shutdown() })
	return s.closeErr
}

func DefaultDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Shioji", "worker"), nil
}
func (s *Server) String() string { return fmt.Sprintf("Shioji worker %s", s.store.DeviceID()) }

func (s *Server) deviceHealth() domain.DeviceHealth {
	h := s.health.Snapshot()
	h.DeviceID = s.store.DeviceID()
	return h
}

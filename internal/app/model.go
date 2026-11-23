// Package app owns client drafts and projections independently of device execution.
package app

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"shioji/internal/protocol"
	"shioji/internal/tailnet"
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/portforward"
)

// InspectorLayout keeps the built-in views within one or two sidebar groups.
type InspectorGroup struct {
	Views    []string `json:"views"`
	Selected string   `json:"selected"`
}
type InspectorLayout struct {
	Groups   []InspectorGroup `json:"groups,omitempty"`
	Joined   bool             `json:"joined,omitempty"`
	First    string           `json:"first,omitempty"`
	Selected string           `json:"selected,omitempty"`
}

type Model struct {
	stateEncoding              *clientStateEncoding
	RemotePathUploads          map[string]bool
	ArchivePolicyChanges       map[string]ArchivePolicyChange
	Feedbacks                  []Feedback
	UncertainSubmissions       map[string]bool
	submissionReceipts         map[string]domain.OperationState
	PortForwards               []portforward.Mapping
	HistoryReads               map[string]*HistoryRead
	HistoryOutlines            map[string]domain.HistoryOutline
	HistoryAnchors             map[string]string
	HistoryOpenings            map[string]bool
	Tailnet                    tailnet.Snapshot
	CompletedReplies           map[string]CompletedReply
	startedAt                  time.Time
	completionCutoffs          map[string]time.Time
	completionReads            map[string]string
	Navigation                 NavigationPreferences
	WorkDetails                map[WorkKey]domain.WorkDetail
	workReads                  []WorkKey
	ServicePreparations        map[string]*ServicePreparation
	ServicePreparationDeviceID string
	WorkerMigrations           map[string]WorkerMigrationTarget
	SignInIDs                  map[string]map[string]string
	FirstRun                   bool
	ModelMenus                 map[string]map[string]ModelMenuPreferences
	WorkerRecoveryClients      map[string]*WorkerRecoveryClient
	WorkerRecoveryIDs          map[string]string
	WorkerMaintenanceClients   map[string]*WorkerMaintenanceClient
	WorkerMaintenanceIDs       map[string]string
	WorkerService              *WorkerService
	DraftSkills                map[string][]DraftSkill
	homeDefaultsPending        bool
	homeConfigContext          string
	HomeHarnesses              map[string]string
	HarnessConfigs             map[string]domain.RunConfig
	HistoryActions             map[string]domain.HistoryChange
	HistoryEdits               map[string]HistoryEdit
	Preferences                ClientPreferences
	Notifications              []SessionNotification
	notificationSeen           []string
	SecondaryWorkspaceID       string
	historyVisits              map[string]uint64
	historyClock               uint64
	SecondarySessionID         string
	FocusSecondary             bool
	Attachments                map[string][]DraftAttachment
	sourceConnection           string
	DeviceHealth               *domain.DeviceHealth
	ConnectionTiming           *DeviceTiming
	DevicePaths                []DevicePathStatus
	Connections                []DeviceConnection
	ActiveConnection           string
	LocalDeviceName            string
	LocalDeviceIcon            DeviceIcon
	LocalDeviceID              string
	DeviceStates               map[string]*DeviceState
	PendingDevices             map[string]string
	pendingGenerations         map[string]string
	WorkerMinor                int
	QueueExpanded              map[string]bool
	QueueEdits                 map[string]QueueEdit
	ConnectionEpoch            string
	ConnectionPhase            ConnectionPhase
	ConnectionError            string
	KnownDeviceID              string
	Snapshot                   domain.Snapshot
	Sequence                   int64
	Connected                  bool
	Readiness                  domain.Readiness
	WorkspaceID                string
	SessionID                  string
	Drafts                     map[string]string
	Configs                    map[string]domain.RunConfig
	sessionConfigDrafts        map[string]sessionConfigDraft
	Answers                    map[string]string
	Pager                      map[string]int
	Collapsed                  map[string]bool
	// Transient: each visit starts with its hydrated tool history collapsed.
	FoldToolHistory      map[string]bool
	Pending              map[string]domain.Submission
	Details              map[string]domain.SessionDetail
	Resolved             map[string]bool
	GitDrafts            map[string]domain.GitChoice
	GitRepositories      map[string]string
	GitStatuses          map[string]domain.GitStatus
	GitEpochs            map[string]string
	Inspectors           map[string]bool
	InspectorLayout      InspectorLayout
	InspectorSplitHeight float32
	InspectorWidth       float32
	Error                string
}

func NewModel() *Model {
	return &Model{RemotePathUploads: map[string]bool{}, startedAt: time.Now(), completionCutoffs: map[string]time.Time{}, completionReads: map[string]string{}, HistoryAnchors: map[string]string{}, HistoryOpenings: map[string]bool{}, HistoryReads: map[string]*HistoryRead{}, HistoryOutlines: map[string]domain.HistoryOutline{}, homeDefaultsPending: true, WorkDetails: map[WorkKey]domain.WorkDetail{}, DraftSkills: map[string][]DraftSkill{}, HistoryActions: map[string]domain.HistoryChange{}, HistoryEdits: map[string]HistoryEdit{}, Preferences: (ClientPreferences{CollapseLatestTools: true}).Normalized(), Attachments: map[string][]DraftAttachment{}, DeviceStates: map[string]*DeviceState{}, PendingDevices: map[string]string{}, pendingGenerations: map[string]string{}, QueueExpanded: map[string]bool{}, QueueEdits: map[string]QueueEdit{}, FoldToolHistory: map[string]bool{}, Inspectors: map[string]bool{}, GitEpochs: map[string]string{}, GitDrafts: map[string]domain.GitChoice{}, GitRepositories: map[string]string{}, GitStatuses: map[string]domain.GitStatus{}, Drafts: map[string]string{}, Configs: map[string]domain.RunConfig{}, sessionConfigDrafts: map[string]sessionConfigDraft{}, Answers: map[string]string{}, Pager: map[string]int{}, Collapsed: map[string]bool{}, Pending: map[string]domain.Submission{}, Details: map[string]domain.SessionDetail{}, Resolved: map[string]bool{}}
}
func (m *Model) DraftKey() string {
	if m.SessionID == "" {
		return "home"
	}
	return "session:" + m.SessionID
}
func (m *Model) Config() domain.RunConfig {
	if id := m.SessionID; id != "" {
		c := m.sessionConfigDrafts[id].apply(m.acceptedSessionConfig(id))
		return normalizeRunConfig(c, m.RunReadiness())
	}
	m.syncHomeConfiguration()
	ready := m.RunReadiness()
	c := normalizeRunConfig(m.Configs["home"], ready)
	if ready.State == domain.RuntimeReady || len(ready.Models) > 0 {
		m.Configs["home"] = c
	}
	return c
}

func (m *Model) SessionConfig(s domain.Session) domain.RunConfig {
	return normalizeRunConfig(m.sessionConfigDrafts[s.ID].apply(s.Config), m.SessionReadiness(s))
}

func normalizeRunConfig(c domain.RunConfig, ready domain.Readiness) domain.RunConfig {
	if ready.State != domain.RuntimeReady && len(ready.Models) == 0 {
		if c.Permission == "" {
			c.Permission = "default"
		}
		return c
	}
	var model *domain.Model
	for i := range ready.Models {
		if ready.Models[i].ID == c.Model {
			model = &ready.Models[i]
		}
	}
	if model == nil && c.Model != "" {
		return c
	}
	if model == nil {
		for i := range ready.Models {
			if ready.Models[i].Default {
				model = &ready.Models[i]
				break
			}
		}
		if model == nil && len(ready.Models) > 0 {
			model = &ready.Models[0]
		}
	}
	if model != nil {
		c.Model = model.ID
		if !slices.Contains(model.Efforts, c.Effort) {
			c.Effort = model.DefaultEffort
		}
		if !model.Fast {
			c.Fast = false
		}
	}
	if !ready.Plan {
		c.Plan = false
	} else if c.Plan && ready.Capabilities.ExclusivePlan {
		c.Permission = "default"
	}
	if model != nil && len(model.Efforts) == 0 {
		c.Effort = ""
	}
	if !slices.Contains(ready.Permissions, c.Permission) {
		c.Permission = "default"
		if !slices.Contains(ready.Permissions, c.Permission) && len(ready.Permissions) > 0 {
			c.Permission = ready.Permissions[0]
		}
	}
	return c
}
func (m *Model) BeginSubmission() (domain.Submission, error) {
	return m.beginSubmission("session.submit")
}
func (m *Model) beginSubmission(method string) (domain.Submission, error) {
	if reason := m.ReadOnlyConversationReason(); reason != "" {
		return domain.Submission{}, errors.New(reason)
	}
	if kind := m.ArchivedContext(); kind != "" {
		return domain.Submission{}, errors.New("请先恢复" + kind)
	}
	key := m.DraftKey()
	if m.HasPendingInput() {
		return domain.Submission{}, errors.New("previous submission is still awaiting confirmation")
	}
	steering := method == "turn.steer"
	ready := m.RunReadiness()
	if steering {
		if own, ok := ready.Sessions[m.SessionID]; ok && own.ID == m.HarnessID() {
			ready.State = own.State
		}
		for _, session := range m.Snapshot.Sessions {
			if session.ID == m.SessionID && session.Config.Model != "" && m.Config().Model != session.Config.Model {
				return domain.Submission{}, errors.New("更换后的模型将在下一轮使用，请加入待发送队列")
			}
		}
	}
	if !m.Connected || ready.State != "ready" {
		return domain.Submission{}, errors.New("connect to a ready execution device")
	}
	if !steering && m.ModelUnavailable() {
		return domain.Submission{}, errors.New(ModelUnavailableMessage)
	}
	if !m.HasDraftContent(key) {
		return domain.Submission{}, errors.New("enter a message")
	}
	workspace := m.WorkspaceID
	if m.SessionID != "" {
		for _, s := range m.Snapshot.Sessions {
			if s.ID == m.SessionID {
				workspace = s.WorkspaceID
				if s.ContextChanged {
					return domain.Submission{}, errors.New("执行分支已改变，请先在分支选择中确认")
				}
				if s.Status == "unknown" || (method == "session.submit" && s.Status.Active()) {
					return domain.Submission{}, errors.New("current turn is still active or needs reconciliation")
				}
			}
		}
	}
	found := false
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == workspace {
			found = true
		}
	}
	if !found {
		return domain.Submission{}, errors.New("select a workspace")
	}
	refs, err := m.SubmissionAttachments(workspace)
	if err != nil {
		return domain.Submission{}, err
	}
	skills, err := m.SubmissionSkills()
	if err != nil {
		return domain.Submission{}, err
	}
	if m.WorkerMinor < 12 && len(m.DraftReferencePositions(key)) > 0 {
		return domain.Submission{}, errors.New("请先升级执行服务以保留行内引用的位置")
	}
	in := domain.Submission{ReferencePositions: m.DraftReferencePositions(key), Skills: skills, HarnessID: m.HarnessID(), Attachments: refs, OperationID: domain.NewID(), WorkspaceID: workspace, SessionID: m.SessionID, Text: m.Drafts[key], Config: m.Config()}
	if m.SessionID == "" {
		choice := m.GitDrafts[workspace]
		if (choice.Branch != "" || choice.NewWorktree) && m.GitEpochs["workspace:"+workspace] != m.ConnectionEpoch {
			return domain.Submission{}, errors.New("正在核对设备上的分支，请稍后提交")
		}
	}
	if objective, goal := domain.GoalCommand(in.Text); goal {
		if m.WorkerMinor < protocol.GoalMinor || !ready.Capabilities.Goals {
			return domain.Submission{}, errors.New("当前设备的 Agent 不支持 Goal")
		}
		if objective == "" {
			return domain.Submission{}, errors.New("请输入 /goal 和完整目标")
		}
		in.GoalObjective = objective
		if m.SessionID != "" {
			for _, session := range m.Snapshot.Sessions {
				if session.ID == m.SessionID {
					// Goal continuation uses the session's accepted run configuration.
					in.Config = session.Config
					break
				}
			}
		}
	}
	if m.SessionID == "" && m.GitStatuses["workspace:"+workspace].Applicable {
		in.Git = m.GitDrafts[workspace]
	}
	if err := in.ValidateContent(); err != nil {
		return domain.Submission{}, err
	}
	m.Pending[in.OperationID] = in
	m.PendingDevices[in.OperationID] = m.ConnectionID()
	if (method == "session.submit" || method == "turn.steer" || method == "turn.enqueue") && m.WorkerMinor >= protocol.RecoveryMinor && m.Snapshot.Generation != "" {
		m.pendingGenerations[in.OperationID] = m.Snapshot.Generation
	}
	return in, nil
}

// Missing receipts can only be settled by the original submission journal.
// Older saved drafts carry no such proof and are still looked up read-only.
func (m *Model) SubmissionReceiptQuery(id string) protocol.OperationQuery {
	q := protocol.OperationQuery{ID: id}
	if m.WorkerMinor >= protocol.RecoveryMinor && m.pendingGenerations[id] == m.Snapshot.Generation {
		q.SealGeneration = m.pendingGenerations[id]
	}
	return q
}

func (m *Model) RestoredSubmissionQueries() map[string][]protocol.OperationQuery {
	byDevice := map[string][]protocol.OperationQuery{}
	for id, connection := range m.PendingDevices {
		if connection == "" {
			connection = "local"
		}
		byDevice[connection] = append(byDevice[connection], protocol.OperationQuery{ID: id, SealGeneration: m.pendingGenerations[id]})
	}
	for id := range m.Pending {
		if _, hasOwner := m.PendingDevices[id]; hasOwner {
			continue
		}
		byDevice["local"] = append(byDevice["local"], protocol.OperationQuery{ID: id, SealGeneration: m.pendingGenerations[id]})
	}
	return byDevice
}

func (m *Model) Accepted(in domain.Submission, op domain.Operation) {
	if m.Resolved[op.ID] {
		return
	}
	m.Pending[in.OperationID] = in
	m.ApplyOperation(op)
}
func (m *Model) ApplyOperation(op domain.Operation) {
	if op.Deleted && op.State == "rejected" {
		if _, ok := m.Pending[op.ID]; !ok {
			return
		}
		if m.sourceConnection != "" && (m.PendingDevices[op.ID] != "" && m.PendingDevices[op.ID] != m.sourceConnection || m.sessionOnOtherDevice(m.sourceConnection, op.SessionID)) {
			m.scopeFailure(errors.New("删除回执不属于原提交设备"))
			return
		}
		delete(m.Pending, op.ID)
		delete(m.PendingDevices, op.ID)
		delete(m.pendingGenerations, op.ID)
		delete(m.UncertainSubmissions, op.ID)
		delete(m.submissionReceipts, op.ID)
		m.Resolved[op.ID] = true
		return
	}
	if m.sourceConnection != "" && op.SessionID != "" && !m.ownsSession(op.SessionID) {
		// A receipt query can overtake the journal's creation of an owned
		// session. Record acceptance without selecting an unpublished session.
		if in, ok := m.Pending[op.ID]; ok && in.SessionID == "" && m.ownsWorkspace(in.WorkspaceID) && !m.sessionOnOtherDevice(m.sourceConnection, op.SessionID) {
			switch op.State {
			case domain.OperationAccepted, domain.OperationDispatched, domain.OperationConfirmed:
				m.noteSubmissionAccepted(op)
			}
			return
		}
		m.scopeFailure(errors.New("设备提交回执不属于本设备"))
		return
	}
	m.confirmHistoryOperation(op)
	in, ok := m.Pending[op.ID]
	if !ok {
		return
	}
	if m.sourceConnection != "" && (!m.ownsWorkspace(in.WorkspaceID) || in.SessionID != "" && !m.ownsSession(in.SessionID) || op.SessionID != "" && in.SessionID != "" && op.SessionID != in.SessionID) {
		m.scopeFailure(errors.New("提交回执与本设备的原始请求不一致"))
		return
	}
	if op.State == domain.OperationPreparing || op.State == domain.OperationAccepted || op.State == domain.OperationDispatched {
		for _, current := range m.Snapshot.Operations {
			if current.ID == op.ID && current.State.ReceiptObserved() {
				return
			}
		}
		m.noteSubmissionAccepted(op)
	} else if op.State == "confirmed" {
		delete(m.UncertainSubmissions, op.ID)
		key := m.submissionDraftKey(in)
		if in.SessionID == "" && key != "home" {
			m.deleteDraft(key)
		} else {
			m.clearSubmittedDraft(key, in)
		}
		// A receipt settles the submitted choices, but current session
		// configuration still comes from the owning worker's session metadata.
		if op.Kind == "submit" && !in.Continue {
			m.reconcileSessionConfig(in.SessionID, in.Config)
		}
		if key == "home" && m.SessionID == "" && m.WorkspaceID == in.WorkspaceID {
			m.SessionID = op.SessionID
		}
		delete(m.Pending, op.ID)
		delete(m.PendingDevices, op.ID)
		delete(m.pendingGenerations, op.ID)
		delete(m.submissionReceipts, op.ID)
		m.Resolved[op.ID] = true
		m.Error = ""
	} else if op.State == "rejected" || op.State == "abandoned" {
		if op.State == domain.OperationRejected {
			m.restoreRejectedDraft(in)
		}
		if op.State == domain.OperationRejected || m.UncertainSubmissions[op.ID] {
			title := "消息未发送"
			if op.State == domain.OperationAbandoned {
				title = "发送结果仍无法确认"
			} else if in.QuestionReply != nil {
				title = "答案未提交"
			} else if op.SessionID != "" {
				title = "消息未送达 Agent"
			}
			m.ReportFeedback(Feedback{ID: op.ID, ConnectionID: m.PendingDevices[op.ID], WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, Kind: FeedbackSubmissionFailed, Title: title, Details: op.Error})
		}
		delete(m.UncertainSubmissions, op.ID)
		delete(m.Pending, op.ID)
		delete(m.PendingDevices, op.ID)
		delete(m.pendingGenerations, op.ID)
		delete(m.submissionReceipts, op.ID)
		m.Resolved[op.ID] = true
		m.Error = op.Error
	} else if op.State == "indeterminate" {
		m.markSubmissionUncertain(in)
		m.Error = "提交结果待核实，草稿已保留；请勿重复发送"
	}
}
func (m *Model) Reject(in domain.Submission, message string) {
	m.ApplyOperation(domain.Operation{ID: in.OperationID, State: "rejected", Error: message})
}
func (m *Model) ApplySnapshot(snapshot domain.Snapshot, requestedAt ...time.Time) {
	if m.sourceConnection != "" {
		if err := m.validateDeviceSnapshot(m.sourceConnection, snapshot); err != nil {
			m.scopeFailure(err)
			return
		}
	}
	sampledAt := time.Now()
	if len(requestedAt) > 0 {
		sampledAt = requestedAt[0]
	}
	m.reconcileCompletedReplies(snapshot, sampledAt)
	m.pruneArchiveSnapshot(snapshot)
	if m.Snapshot.Generation != "" && m.Snapshot.Generation != snapshot.Generation {
		for id := range m.Details {
			delete(m.HistoryReads, id)
			delete(m.HistoryOutlines, id)
			delete(m.HistoryAnchors, id)
			delete(m.HistoryOpenings, id)
		}
		m.Details = map[string]domain.SessionDetail{}
	}
	for id, detail := range m.Details {
		detail.Requests = nil
		m.Details[id] = detail
	}
	for _, req := range snapshot.PendingRequests {
		detail := m.Details[req.SessionID]
		detail.Requests = append(detail.Requests, req)
		m.Details[req.SessionID] = detail
	}
	m.Snapshot = snapshot
	for _, s := range snapshot.Sessions {
		m.reconcileSessionConfig(s.ID, s.Config)
	}
	m.KnownDeviceID = snapshot.DeviceID
	m.Sequence = snapshot.Sequence
	if m.WorkspaceID == "" && len(snapshot.Workspaces) > 0 {
		m.WorkspaceID = snapshot.Workspaces[0].ID
	}
	for _, op := range snapshot.Operations {
		m.ApplyOperation(op)
	}
}
func (m *Model) ConversationGeneration() string {
	return conversationGeneration(m.Snapshot, m.ConnectionEpoch)
}

func conversationGeneration(snapshot domain.Snapshot, epoch string) string {
	if snapshot.Generation == "" {
		return epoch
	}
	return snapshot.DeviceID + ":" + snapshot.Generation
}

func (m *Model) ApplyEvent(e domain.Event) error { return m.applyEvent(e, true) }

func (m *Model) applyEvent(e domain.Event, live bool) error {
	if e.Sequence <= m.Sequence {
		return nil
	}
	if e.Sequence != m.Sequence+1 {
		return errors.New("event gap: snapshot required")
	}
	if m.sourceConnection != "" {
		if err := m.validateDeviceEvent(e); err != nil {
			return err
		}
	}
	m.Sequence = e.Sequence
	m.Snapshot.Sequence = e.Sequence
	if e.Deletion != nil {
		m.ApplyArchiveDeletion(*e.Deletion)
		return nil
	}
	if e.ArchivePolicy != nil && (m.Snapshot.ArchivePolicy == nil || e.ArchivePolicy.Revision >= m.Snapshot.ArchivePolicy.Revision) {
		m.Snapshot.ArchivePolicy = e.ArchivePolicy
	}
	if r := m.HistoryReads[e.SessionID]; r != nil {
		r.observe(e)
	}
	if e.Item != nil && e.Item.Kind == "user" {
		if outline, ok := m.HistoryOutlines[e.SessionID]; ok {
			text := []rune(e.Item.Text)
			outline.Prompts = slices.Clone(outline.Prompts)
			p := domain.HistoryPrompt{ID: e.Item.ID, TurnID: e.Item.TurnID, Order: e.Item.Order, Text: string(text[:min(120, len(text))])}
			at := slices.IndexFunc(outline.Prompts, func(x domain.HistoryPrompt) bool { return x.ID == p.ID })
			if at >= 0 {
				outline.Prompts[at] = p
			} else {
				outline.Prompts = append(outline.Prompts, p)
			}
			m.HistoryOutlines[e.SessionID] = outline
		}
	}
	var notice *SessionNotification
	if live {
		notice = m.notificationForEvent(e)
	}
	m.observeCompletedReply(e)
	if e.Workspace != nil {
		found := false
		for i, w := range m.Snapshot.Workspaces {
			if w.ID == e.Workspace.ID {
				m.Snapshot.Workspaces[i] = *e.Workspace
				found = true
				break
			}
		}
		if !found {
			m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, *e.Workspace)
		}
	}
	if e.Session != nil {
		found := false
		for i, s := range m.Snapshot.Sessions {
			if s.ID == e.Session.ID {
				if s.Revision <= e.Session.Revision {
					m.Snapshot.Sessions[i] = *e.Session
					m.reconcileSessionConfig(e.Session.ID, e.Session.Config)
				}
				found = true
				break
			}
		}
		if !found {
			m.Snapshot.Sessions = append(m.Snapshot.Sessions, *e.Session)
			m.reconcileSessionConfig(e.Session.ID, e.Session.Config)
		}
	}
	if e.Type == "submission" && e.Item != nil && e.Item.Kind == "user" {
		m.promoteNavigationSession(e.SessionID)
	}
	if item := e.Item; e.SessionID != "" && item != nil && item.Kind == "user" && item.WorkID == "" && item.SentAt != nil &&
		item.SentAt.After(m.Snapshot.SessionInputTimes[e.SessionID]) {
		if len(m.Snapshot.SessionInputTimes) == 0 {
			m.Snapshot.SessionInputTimes = make(map[string]time.Time)
		} else {
			m.Snapshot.SessionInputTimes = maps.Clone(m.Snapshot.SessionInputTimes)
		}
		m.Snapshot.SessionInputTimes[e.SessionID] = item.SentAt.UTC()
	}
	if e.Operation != nil {
		// Keep the live receipt available to presentation after confirmation
		// releases Pending. The command reply and event stream are independent.
		if i := slices.IndexFunc(m.Snapshot.Operations, func(op domain.Operation) bool { return op.ID == e.Operation.ID }); i >= 0 {
			m.Snapshot.Operations[i] = *e.Operation
		} else {
			m.Snapshot.Operations = append([]domain.Operation{*e.Operation}, m.Snapshot.Operations...)
			m.Snapshot.Operations = m.Snapshot.Operations[:min(100, len(m.Snapshot.Operations))]
		}
		m.ApplyOperation(*e.Operation)
	}
	if e.Queue != nil {
		m.SetQueue(*e.Queue)
	}
	if _, ok := m.Details[e.SessionID]; !ok && e.Request != nil {
		m.Details[e.SessionID] = domain.SessionDetail{}
	}
	m.applyWorkEvent(e)
	if detail, ok := m.Details[e.SessionID]; ok && e.Sequence > detail.Sequence {
		if detail.History != nil {
			h := *detail.History
			h.LiveSequence = e.Sequence
			h.Groups = slices.Clone(h.Groups)
			detail.History = &h
		}
		if e.Type == "history.reset" && e.HistoryTurnOrder != nil {
			detail.HistoryLoaded = false
			if detail.History != nil {
				h := *detail.History
				h.Invalidated = true
				detail.History = &h
			} else if m.WorkerMinor >= 37 {
				detail.History = &domain.HistoryWindow{Invalidated: true}
			}
			// The old read and its scroll anchor belong to the replaced history.
			delete(m.HistoryReads, e.SessionID)
			delete(m.HistoryAnchors, e.SessionID)
			delete(m.HistoryOpenings, e.SessionID)
			delete(m.HistoryOutlines, e.SessionID)
			retained := map[string]bool{}
			for _, turn := range detail.Turns {
				if turn.Order <= *e.HistoryTurnOrder {
					retained[turn.ID] = true
				}
			}
			detail.Items = slices.DeleteFunc(detail.Items, func(item domain.Item) bool { return !retained[item.TurnID] })
			detail.Turns = slices.DeleteFunc(detail.Turns, func(turn domain.Turn) bool { return !retained[turn.ID] })
			detail.Requests = slices.DeleteFunc(detail.Requests, func(req domain.PendingRequest) bool { return !retained[req.TurnID] })
			detail.Plan = nil
			detail.Works = slices.DeleteFunc(detail.Works, func(w domain.WorkActivity) bool { return !retained[w.TurnID] })
			for key, work := range m.WorkDetails {
				if key.DeviceID == m.Snapshot.DeviceID && key.SessionID == e.SessionID && !retained[work.Work.TurnID] {
					delete(m.WorkDetails, key)
				}
			}
		}
		if e.Plan != nil {
			detail.Plan = e.Plan
		}
		if e.Session != nil && detail.Session.Revision <= e.Session.Revision {
			detail.Session = *e.Session
		}
		if e.Item != nil && e.Item.Kind == "tool" && detail.History != nil && len(detail.History.Groups) > 0 && (e.Item.Order < detail.History.First || detail.History.Newer && e.Item.Order > detail.History.Last) {
			detail.History.SummaryDirty = e.Sequence
		}
		if e.Item != nil && historyAcceptsItem(detail, *e.Item) {
			found := false
			for i, item := range detail.Items {
				if item.ID == e.Item.ID {
					detail.Items[i] = *e.Item
					found = true
					break
				}
			}
			if !found {
				position := len(detail.Items)
				if e.Item.Order > 0 {
					for i, item := range detail.Items {
						if item.Order > e.Item.Order {
							position = i
							break
						}
					}
				}
				detail.Items = slices.Insert(detail.Items, position, *e.Item)
			}
		}
		if e.Request != nil {
			found := false
			for i, r := range detail.Requests {
				if r.ID == e.Request.ID {
					detail.Requests[i] = *e.Request
					found = true
					break
				}
			}
			if !found {
				detail.Requests = append(detail.Requests, *e.Request)
			}
		}
		if e.Turn != nil {
			found := false
			for i, t := range detail.Turns {
				if t.ID == e.Turn.ID {
					detail.Turns[i] = *e.Turn
					found = true
					break
				}
			}
			if !found {
				detail.Turns = append(detail.Turns, *e.Turn)
			}
		}
		m.Details[e.SessionID] = detail
	}
	if notice != nil {
		m.Notifications = append(m.Notifications, *notice)
		m.notificationSeen = append(slices.Clone(m.notificationSeen), notice.ID)
		if len(m.notificationSeen) > 256 {
			m.notificationSeen = m.notificationSeen[len(m.notificationSeen)-256:]
		}
	}
	return nil
}

type savedState struct {
	RemotePathUploads          map[string]bool                            `json:"remotePathUploads,omitempty"`
	WorkerRecoveryIDs          map[string]string                          `json:"workerRecoveryIDs,omitempty"`
	WorkerMaintenanceIDs       map[string]string                          `json:"workerMaintenanceIDs,omitempty"`
	ArchivePolicyChanges       map[string]ArchivePolicyChange             `json:"archivePolicyChanges,omitempty"`
	Navigation                 NavigationPreferences                      `json:"navigation,omitempty"`
	ServicePreparations        map[string]*ServicePreparation             `json:"servicePreparations,omitempty"`
	ServicePreparationDeviceID string                                     `json:"servicePreparationDeviceId,omitempty"`
	WorkerMigrations           map[string]WorkerMigrationTarget           `json:"workerMigrations,omitempty"`
	SignInIDs                  map[string]map[string]string               `json:"signInIds,omitempty"`
	SetupCompleted             *bool                                      `json:"setupCompleted,omitempty"`
	ModelMenus                 map[string]map[string]ModelMenuPreferences `json:"modelMenus,omitempty"`
	DraftSkills                map[string][]DraftSkill                    `json:"draftSkills,omitempty"`
	HomeConfigContext          string                                     `json:"homeConfigContext,omitempty"`
	HomeHarnesses              map[string]string                          `json:"homeHarnesses,omitempty"`
	HarnessConfigs             map[string]domain.RunConfig                `json:"harnessConfigs,omitempty"`
	HistoryActions             map[string]domain.HistoryChange            `json:"historyActions,omitempty"`
	HistoryEdits               map[string]HistoryEdit                     `json:"historyEdits,omitempty"`
	Preferences                ClientPreferences                          `json:"preferences"`
	SecondaryWorkspaceID       string                                     `json:"secondaryWorkspaceId,omitempty"`
	SecondarySessionID         string                                     `json:"secondarySessionId,omitempty"`
	FocusSecondary             bool                                       `json:"focusSecondary,omitempty"`
	Attachments                map[string][]DraftAttachment               `json:"attachments,omitempty"`
	DeviceCache                map[string]cachedDevice                    `json:"deviceCache,omitempty"`
	LocalDeviceName            string                                     `json:"localDeviceName,omitempty"`
	LocalDeviceIcon            DeviceIcon                                 `json:"localDeviceIcon,omitempty"`
	Connections                []DeviceConnection                         `json:"connections,omitempty"`
	ActiveConnection           string                                     `json:"activeConnection,omitempty"`
	PendingDevices             map[string]string                          `json:"pendingDevices,omitempty"`
	PendingGenerations         map[string]string                          `json:"pendingGenerations,omitempty"`
	QueueExpanded              map[string]bool                            `json:"queueExpanded,omitempty"`
	QueueEdits                 map[string]QueueEdit                       `json:"queueEdits,omitempty"`
	DeviceID                   string                                     `json:"deviceId"`
	WorkspaceID                string                                     `json:"workspaceId"`
	Drafts                     map[string]string                          `json:"drafts"`
	Configs                    map[string]domain.RunConfig                `json:"configs"`
	SessionConfigDrafts        map[string]sessionConfigDraft              `json:"sessionConfigDrafts,omitempty"`
	Answers                    map[string]string                          `json:"answers"`
	Pager                      map[string]int                             `json:"pager"`
	Collapsed                  map[string]bool                            `json:"collapsed,omitempty"`
	Pending                    map[string]domain.Submission               `json:"pending"`
	GitDrafts                  map[string]domain.GitChoice                `json:"gitDrafts,omitempty"`
	GitRepositories            map[string]string                          `json:"gitRepositories,omitempty"`
	Inspectors                 map[string]bool                            `json:"inspectors,omitempty"`
	InspectorLayout            InspectorLayout                            `json:"inspectorLayout,omitempty"`
	InspectorSplitHeight       float32                                    `json:"inspectorSplitHeight,omitempty"`
	InspectorWidth             float32                                    `json:"inspectorWidth,omitempty"`
}

func (m *Model) Save(path string) error {
	b, err := m.EncodeState()
	if err != nil {
		return err
	}
	return WriteState(path, b)
}
func (m *Model) Load(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		m.FirstRun = true
		return nil
	}
	if err != nil {
		return err
	}
	saved := savedState{Preferences: ClientPreferences{CollapseLatestTools: true}}
	if err = json.Unmarshal(b, &saved); err != nil {
		return err
	}
	m.FirstRun = saved.SetupCompleted != nil && !*saved.SetupCompleted
	m.ServicePreparations = saved.ServicePreparations
	m.ServicePreparationDeviceID = saved.ServicePreparationDeviceID
	m.WorkerMaintenanceIDs = saved.WorkerMaintenanceIDs
	m.WorkerRecoveryIDs = saved.WorkerRecoveryIDs
	m.WorkerMigrations = saved.WorkerMigrations
	m.SignInIDs = saved.SignInIDs
	if saved.DraftSkills != nil {
		m.DraftSkills = saved.DraftSkills
	}
	m.Preferences = saved.Preferences.Normalized()
	m.ArchivePolicyChanges = saved.ArchivePolicyChanges
	m.Navigation = saved.Navigation
	m.homeConfigContext = saved.HomeConfigContext
	m.HomeHarnesses = saved.HomeHarnesses
	m.HarnessConfigs = saved.HarnessConfigs
	if saved.HistoryActions != nil {
		m.HistoryActions = saved.HistoryActions
	}
	if saved.HistoryEdits != nil {
		m.HistoryEdits = saved.HistoryEdits
	}
	m.SecondaryWorkspaceID = saved.SecondaryWorkspaceID
	m.SecondarySessionID = saved.SecondarySessionID
	m.FocusSecondary = saved.FocusSecondary
	if saved.Attachments != nil {
		m.Attachments = saved.Attachments
	}
	if saved.RemotePathUploads != nil {
		m.RemotePathUploads = saved.RemotePathUploads
	}
	for key, files := range m.Attachments {
		for i := range files {
			if files[i].Attachment.Kind == domain.PathAttachmentResource && files[i].UploadApproved && !files[i].Ready {
				files[i].Error = "上传已中断，请重试"
			}
		}
		m.Attachments[key] = files
	}
	if saved.QueueExpanded != nil {
		m.QueueExpanded = saved.QueueExpanded
	}
	if saved.QueueEdits != nil {
		m.QueueEdits = saved.QueueEdits
	}
	m.ModelMenus = saved.ModelMenus
	m.InspectorWidth = saved.InspectorWidth
	m.InspectorSplitHeight = saved.InspectorSplitHeight
	m.InspectorLayout = saved.InspectorLayout
	m.InspectorLayout.Groups = SanitizeInspectorGroups(m.InspectorLayout.Groups)
	if m.InspectorLayout.First != "changes" && m.InspectorLayout.First != "tasks" {
		m.InspectorLayout.First = ""
	}
	if m.InspectorLayout.Selected != "changes" && m.InspectorLayout.Selected != "tasks" {
		m.InspectorLayout.Selected = ""
	}
	if saved.Inspectors != nil {
		m.Inspectors = saved.Inspectors
	}
	if saved.GitDrafts != nil {
		m.GitDrafts = saved.GitDrafts
	}
	if saved.GitRepositories != nil {
		m.GitRepositories = saved.GitRepositories
	}
	m.KnownDeviceID = saved.DeviceID
	m.LocalDeviceID = saved.DeviceID
	m.LocalDeviceName = saved.LocalDeviceName
	m.LocalDeviceIcon = saved.LocalDeviceIcon
	m.Connections = normalizedConnections(saved.Connections)
	for key, cache := range saved.DeviceCache {
		if key != "local" {
			found := false
			for _, c := range m.Connections {
				if c.ID == key {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		if m.validateDeviceSnapshot(key, cache.Snapshot) != nil {
			continue
		}
		m.DeviceStates[key] = &DeviceState{Snapshot: cache.Snapshot, Sequence: cache.Snapshot.Sequence, Readiness: cache.Readiness,
			ScheduleSummaries: cachedScheduleSummaries(cache.Snapshot, cache.Schedules)}
	}
	m.ActiveConnection = saved.ActiveConnection
	if saved.PendingDevices != nil {
		m.PendingDevices = saved.PendingDevices
	}
	if saved.PendingGenerations != nil {
		m.pendingGenerations = saved.PendingGenerations
	}
	m.WorkspaceID = saved.WorkspaceID
	if saved.Drafts != nil {
		m.Drafts = saved.Drafts
	}
	if saved.Configs != nil {
		m.Configs = saved.Configs
		// Older clients used the same key for explicit edits and cached
		// session defaults, so these entries cannot serve as draft overrides.
		for key := range m.Configs {
			if strings.HasPrefix(key, "session:") {
				delete(m.Configs, key)
			}
		}
	}
	if saved.SessionConfigDrafts != nil {
		m.sessionConfigDrafts = saved.SessionConfigDrafts
	}
	if saved.Answers != nil {
		m.Answers = saved.Answers
	}
	if saved.Pager != nil {
		m.Pager = saved.Pager
	}
	if saved.Collapsed != nil {
		m.Collapsed = saved.Collapsed
	}
	if saved.Pending != nil {
		m.Pending = saved.Pending
		m.UncertainSubmissions = map[string]bool{}
		for _, in := range saved.Pending {
			m.markSubmissionUncertain(in)
		}
	}
	m.restoreLegacyDrafts()
	return nil
}

const submissionUncertainMessage = "提交结果待核实，草稿已保留"

func (m *Model) SubmissionReceiptState(id string) domain.OperationState {
	return m.submissionReceipts[id]
}

func (m *Model) noteSubmissionAccepted(op domain.Operation) {
	id := op.ID
	delete(m.pendingGenerations, id)
	if m.submissionReceipts == nil {
		m.submissionReceipts = map[string]domain.OperationState{}
	}
	// Command receipts and journal events can arrive in either order.
	previous := m.submissionReceipts[id]
	if !previous.ReceiptObserved() && (op.State.ReceiptObserved() ||
		previous != domain.OperationDispatched && (previous == "" || op.State != domain.OperationPreparing)) {
		m.submissionReceipts[id] = op.State
	}
	if m.UncertainSubmissions[id] {
		delete(m.UncertainSubmissions, id)
		m.Feedbacks = slices.DeleteFunc(m.Feedbacks, func(f Feedback) bool { return f.ID == id && f.Kind == FeedbackSubmissionUncertain })
		if m.Error == submissionUncertainMessage {
			m.Error = ""
		}
	}
}

func (m *Model) SubmissionError(in domain.Submission, err error) bool {
	if _, pending := m.Pending[in.OperationID]; !pending || m.SubmissionReceiptState(in.OperationID) != "" {
		return false
	}
	var remote *protocol.RemoteError
	if errors.As(err, &remote) {
		m.Reject(in, err.Error())
	} else {
		m.markSubmissionUncertain(in)
		m.Error = submissionUncertainMessage
	}
	return true
}

func (m *Model) SetGitStatus(key string, status domain.GitStatus) {
	previous, known := m.GitStatuses[key]
	fresh := m.GitEpochs[key] != "" && m.GitEpochs[key] == m.ConnectionEpoch
	m.GitStatuses[key] = status
	m.GitEpochs[key] = m.ConnectionEpoch
	if !strings.HasPrefix(key, "workspace:") {
		return
	}
	id := strings.TrimPrefix(key, "workspace:")
	choice := m.GitDrafts[id]
	emptyDraft := !m.HasDraftContent("home") && !m.HasPendingInput()
	followCheckout := emptyDraft && (!fresh || known && choice.Branch == previous.Branch)
	if m.GitRepositories[id] != status.Repository || !slices.Contains(status.Branches, choice.Branch) || followCheckout {
		choice.Branch = status.Branch
	}
	m.GitDrafts[id] = choice
	m.GitRepositories[id] = status.Repository
}

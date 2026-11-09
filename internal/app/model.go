// Package app owns client drafts and projections independently of device execution.
package app

import (
	"encoding/json"
	"errors"
	"os"
	"shioji/internal/protocol"
	"slices"
	"strings"

	"shioji/internal/domain"
)

// InspectorLayout describes the two built-in views in the client's one sidebar.
type InspectorLayout struct {
	Joined   bool   `json:"joined,omitempty"`
	First    string `json:"first,omitempty"`
	Selected string `json:"selected,omitempty"`
}

type Model struct {
	ServicePreparations        map[string]*ServicePreparation
	ServicePreparationDeviceID string
	WorkerMigrations           map[string]WorkerMigrationTarget
	SignInIDs                  map[string]map[string]string
	FirstRun                   bool
	ModelMenus                 map[string]map[string]ModelMenuPreferences
	WorkerService              *WorkerService
	DraftSkills                map[string][]DraftSkill
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
	ConnectionTiming           *DeviceTiming
	Connections                []DeviceConnection
	ActiveConnection           string
	LocalDeviceName            string
	LocalDeviceID              string
	DeviceStates               map[string]*DeviceState
	PendingDevices             map[string]string
	WorkerMinor                int
	QueueExpanded              map[string]bool
	QueueEdits                 map[string]QueueEdit
	ConnectionEpoch            string
	KnownDeviceID              string
	Snapshot                   domain.Snapshot
	Sequence                   int64
	Connected                  bool
	Readiness                  domain.Readiness
	WorkspaceID                string
	SessionID                  string
	Drafts                     map[string]string
	Configs                    map[string]domain.RunConfig
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
	return &Model{DraftSkills: map[string][]DraftSkill{}, HistoryActions: map[string]domain.HistoryChange{}, HistoryEdits: map[string]HistoryEdit{}, Preferences: (ClientPreferences{}).Normalized(), Attachments: map[string][]DraftAttachment{}, DeviceStates: map[string]*DeviceState{}, PendingDevices: map[string]string{}, QueueExpanded: map[string]bool{}, QueueEdits: map[string]QueueEdit{}, FoldToolHistory: map[string]bool{}, Inspectors: map[string]bool{}, GitEpochs: map[string]string{}, GitDrafts: map[string]domain.GitChoice{}, GitRepositories: map[string]string{}, GitStatuses: map[string]domain.GitStatus{}, Drafts: map[string]string{}, Configs: map[string]domain.RunConfig{}, Answers: map[string]string{}, Pager: map[string]int{}, Collapsed: map[string]bool{}, Pending: map[string]domain.Submission{}, Details: map[string]domain.SessionDetail{}, Resolved: map[string]bool{}}
}
func (m *Model) DraftKey() string {
	if m.SessionID == "" {
		return "home"
	}
	return "session:" + m.SessionID
}
func (m *Model) Config() domain.RunConfig {
	if m.SessionID == "" {
		m.syncHomeConfiguration()
	}
	key := m.DraftKey()
	ready := m.RunReadiness()
	c, configured := m.Configs[key]
	if !configured && m.SessionID != "" {
		for _, session := range m.Snapshot.Sessions {
			if session.ID == m.SessionID {
				c = session.Config
				break
			}
		}
	}
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
	m.Configs[key] = c
	return c
}
func (m *Model) BeginSubmission() (domain.Submission, error) {
	return m.beginSubmission("session.submit")
}
func (m *Model) beginSubmission(method string) (domain.Submission, error) {
	if kind := m.ArchivedContext(); kind != "" {
		return domain.Submission{}, errors.New("请先恢复" + kind)
	}
	key := m.DraftKey()
	for _, in := range m.Pending {
		inKey := "home"
		if in.SessionID != "" {
			inKey = "session:" + in.SessionID
		}
		if inKey == key {
			return domain.Submission{}, errors.New("previous submission is still awaiting confirmation")
		}
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
	if m.SessionID == "" && m.GitStatuses["workspace:"+workspace].Applicable {
		in.Git = m.GitDrafts[workspace]
	}
	if err := in.ValidateContent(); err != nil {
		return domain.Submission{}, err
	}
	m.Pending[in.OperationID] = in
	m.PendingDevices[in.OperationID] = m.ConnectionID()
	return in, nil
}
func (m *Model) Accepted(in domain.Submission, op domain.Operation) {
	if m.Resolved[op.ID] {
		return
	}
	m.Pending[in.OperationID] = in
	m.ApplyOperation(op)
}
func (m *Model) ApplyOperation(op domain.Operation) {
	if m.sourceConnection != "" && op.SessionID != "" && !m.ownsSession(op.SessionID) {
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
	if op.State == "confirmed" {
		key := "home"
		if in.SessionID != "" {
			key = "session:" + in.SessionID
		}
		if !in.Continue && m.Drafts[key] == in.Text {
			m.Drafts[key] = ""
		}
		m.clearAcceptedAttachments(key, in.Attachments)
		m.clearAcceptedSkills(key, in)
		m.Configs["session:"+op.SessionID] = in.Config
		if in.SessionID == "" && m.SessionID == "" && m.WorkspaceID == in.WorkspaceID {
			m.SessionID = op.SessionID
		}
		delete(m.Pending, op.ID)
		delete(m.PendingDevices, op.ID)
		m.Resolved[op.ID] = true
		m.Error = ""
	} else if op.State == "rejected" || op.State == "abandoned" {
		delete(m.Pending, op.ID)
		delete(m.PendingDevices, op.ID)
		m.Resolved[op.ID] = true
		m.Error = op.Error
	} else if op.State == "indeterminate" {
		m.Error = "提交结果待核实，草稿已保留；请勿重复发送"
	}
}
func (m *Model) Reject(in domain.Submission, message string) {
	m.ApplyOperation(domain.Operation{ID: in.OperationID, State: "rejected", Error: message})
}
func (m *Model) ApplySnapshot(snapshot domain.Snapshot) {
	if m.sourceConnection != "" {
		if err := m.validateDeviceSnapshot(m.sourceConnection, snapshot); err != nil {
			m.scopeFailure(err)
			return
		}
	}
	if m.Snapshot.Generation != "" && m.Snapshot.Generation != snapshot.Generation {
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
	m.KnownDeviceID = snapshot.DeviceID
	m.Sequence = snapshot.Sequence
	if m.WorkspaceID == "" && len(snapshot.Workspaces) > 0 {
		m.WorkspaceID = snapshot.Workspaces[0].ID
	}
	for _, op := range snapshot.Operations {
		m.ApplyOperation(op)
	}
}
func (m *Model) ApplyEvent(e domain.Event) error {
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
	notice := m.notificationForEvent(e)
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
				}
				found = true
				break
			}
		}
		if !found {
			m.Snapshot.Sessions = append(m.Snapshot.Sessions, *e.Session)
		}
	}
	if e.Operation != nil {
		m.ApplyOperation(*e.Operation)
	}
	if e.Queue != nil {
		m.SetQueue(*e.Queue)
	}
	if _, ok := m.Details[e.SessionID]; !ok && e.Request != nil {
		m.Details[e.SessionID] = domain.SessionDetail{}
	}
	if detail, ok := m.Details[e.SessionID]; ok && e.Sequence > detail.Sequence {
		if e.Type == "history.reset" && e.HistoryTurnOrder != nil {
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
		}
		if e.Plan != nil {
			detail.Plan = e.Plan
		}
		if e.Session != nil {
			detail.Session = *e.Session
		}
		if e.Item != nil {
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
	Connections                []DeviceConnection                         `json:"connections,omitempty"`
	ActiveConnection           string                                     `json:"activeConnection,omitempty"`
	PendingDevices             map[string]string                          `json:"pendingDevices,omitempty"`
	QueueExpanded              map[string]bool                            `json:"queueExpanded,omitempty"`
	QueueEdits                 map[string]QueueEdit                       `json:"queueEdits,omitempty"`
	DeviceID                   string                                     `json:"deviceId"`
	WorkspaceID                string                                     `json:"workspaceId"`
	Drafts                     map[string]string                          `json:"drafts"`
	Configs                    map[string]domain.RunConfig                `json:"configs"`
	Answers                    map[string]string                          `json:"answers"`
	Pager                      map[string]int                             `json:"pager"`
	Collapsed                  map[string]bool                            `json:"collapsed"`
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
	var saved savedState
	if err = json.Unmarshal(b, &saved); err != nil {
		return err
	}
	m.FirstRun = saved.SetupCompleted != nil && !*saved.SetupCompleted
	m.ServicePreparations = saved.ServicePreparations
	m.ServicePreparationDeviceID = saved.ServicePreparationDeviceID
	m.WorkerMigrations = saved.WorkerMigrations
	m.SignInIDs = saved.SignInIDs
	if saved.DraftSkills != nil {
		m.DraftSkills = saved.DraftSkills
	}
	m.Preferences = saved.Preferences.Normalized()
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
	m.Connections = saved.Connections
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
		m.DeviceStates[key] = &DeviceState{Snapshot: cache.Snapshot, Sequence: cache.Snapshot.Sequence, Readiness: cache.Readiness}
	}
	m.ActiveConnection = saved.ActiveConnection
	if saved.PendingDevices != nil {
		m.PendingDevices = saved.PendingDevices
	}
	m.WorkspaceID = saved.WorkspaceID
	if saved.Drafts != nil {
		m.Drafts = saved.Drafts
	}
	if saved.Configs != nil {
		m.Configs = saved.Configs
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
	}
	return nil
}

func (m *Model) SubmissionError(in domain.Submission, err error) {
	var remote *protocol.RemoteError
	if errors.As(err, &remote) {
		m.Reject(in, err.Error())
	} else {
		m.Error = "提交结果待核实，草稿已保留"
	}
}

func (m *Model) SetGitStatus(key string, status domain.GitStatus) {
	m.GitStatuses[key] = status
	m.GitEpochs[key] = m.ConnectionEpoch
	if !strings.HasPrefix(key, "workspace:") {
		return
	}
	id := strings.TrimPrefix(key, "workspace:")
	choice := m.GitDrafts[id]
	if m.GitRepositories[id] != status.Repository || !slices.Contains(status.Branches, choice.Branch) {
		choice.Branch = status.DefaultBranch
	}
	m.GitDrafts[id] = choice
	m.GitRepositories[id] = status.Repository
}

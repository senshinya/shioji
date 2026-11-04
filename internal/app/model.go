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

type Model struct {
	ConnectionEpoch string
	KnownDeviceID   string
	Snapshot        domain.Snapshot
	Sequence        int64
	Connected       bool
	Readiness       domain.Readiness
	WorkspaceID     string
	SessionID       string
	Drafts          map[string]string
	Configs         map[string]domain.RunConfig
	Answers         map[string]string
	Pager           map[string]int
	Collapsed       map[string]bool
	Pending         map[string]domain.Submission
	Details         map[string]domain.SessionDetail
	Resolved        map[string]bool
	GitDrafts       map[string]domain.GitChoice
	GitRepositories map[string]string
	GitStatuses     map[string]domain.GitStatus
	GitEpochs       map[string]string
	Inspectors      map[string]bool
	Error           string
}

func NewModel() *Model {
	return &Model{Inspectors: map[string]bool{}, GitEpochs: map[string]string{}, GitDrafts: map[string]domain.GitChoice{}, GitRepositories: map[string]string{}, GitStatuses: map[string]domain.GitStatus{}, Drafts: map[string]string{}, Configs: map[string]domain.RunConfig{}, Answers: map[string]string{}, Pager: map[string]int{}, Collapsed: map[string]bool{}, Pending: map[string]domain.Submission{}, Details: map[string]domain.SessionDetail{}, Resolved: map[string]bool{}}
}
func (m *Model) DraftKey() string {
	if m.SessionID == "" {
		return "home"
	}
	return "session:" + m.SessionID
}
func (m *Model) Config() domain.RunConfig {
	key := m.DraftKey()
	c, configured := m.Configs[key]
	if !configured && m.SessionID != "" {
		for _, session := range m.Snapshot.Sessions {
			if session.ID == m.SessionID {
				c = session.Config
				break
			}
		}
	}
	var model *domain.Model
	for i := range m.Readiness.Models {
		if m.Readiness.Models[i].ID == c.Model {
			model = &m.Readiness.Models[i]
		}
	}
	if model == nil {
		for i := range m.Readiness.Models {
			if m.Readiness.Models[i].Default {
				model = &m.Readiness.Models[i]
				break
			}
		}
		if model == nil && len(m.Readiness.Models) > 0 {
			model = &m.Readiness.Models[0]
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
	if !m.Readiness.Plan {
		c.Plan = false
	}
	if !slices.Contains(m.Readiness.Permissions, c.Permission) {
		c.Permission = "default"
		if !slices.Contains(m.Readiness.Permissions, c.Permission) && len(m.Readiness.Permissions) > 0 {
			c.Permission = m.Readiness.Permissions[0]
		}
	}
	m.Configs[key] = c
	return c
}
func (m *Model) BeginSubmission() (domain.Submission, error) {
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
	if !m.Connected || m.Readiness.State != "ready" {
		return domain.Submission{}, errors.New("connect to a ready execution device")
	}
	if strings.TrimSpace(m.Drafts[key]) == "" {
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
				if s.Status == "running" || s.Status == "starting" || s.Status == "unknown" {
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
	in := domain.Submission{OperationID: domain.NewID(), WorkspaceID: workspace, SessionID: m.SessionID, Text: m.Drafts[key], Config: m.Config()}
	if m.SessionID == "" {
		choice := m.GitDrafts[workspace]
		if (choice.Branch != "" || choice.NewWorktree) && m.GitEpochs["workspace:"+workspace] != m.ConnectionEpoch {
			return domain.Submission{}, errors.New("正在核对设备上的分支，请稍后提交")
		}
	}
	if m.SessionID == "" && m.GitStatuses["workspace:"+workspace].Applicable {
		in.Git = m.GitDrafts[workspace]
	}
	m.Pending[in.OperationID] = in
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
	in, ok := m.Pending[op.ID]
	if !ok {
		return
	}
	if op.State == "confirmed" {
		key := "home"
		if in.SessionID != "" {
			key = "session:" + in.SessionID
		}
		if m.Drafts[key] == in.Text {
			m.Drafts[key] = ""
		}
		m.Configs["session:"+op.SessionID] = in.Config
		if in.SessionID == "" && m.SessionID == "" && m.WorkspaceID == in.WorkspaceID {
			m.SessionID = op.SessionID
		}
		delete(m.Pending, op.ID)
		m.Resolved[op.ID] = true
		m.Error = ""
	} else if op.State == "rejected" {
		delete(m.Pending, op.ID)
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
	m.Sequence = e.Sequence
	m.Snapshot.Sequence = e.Sequence
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
	if _, ok := m.Details[e.SessionID]; !ok && e.Request != nil {
		m.Details[e.SessionID] = domain.SessionDetail{}
	}
	if detail, ok := m.Details[e.SessionID]; ok && e.Sequence > detail.Sequence {
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
				detail.Items = append(detail.Items, *e.Item)
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
	return nil
}

type savedState struct {
	DeviceID        string                       `json:"deviceId"`
	WorkspaceID     string                       `json:"workspaceId"`
	Drafts          map[string]string            `json:"drafts"`
	Configs         map[string]domain.RunConfig  `json:"configs"`
	Answers         map[string]string            `json:"answers"`
	Pager           map[string]int               `json:"pager"`
	Collapsed       map[string]bool              `json:"collapsed"`
	Pending         map[string]domain.Submission `json:"pending"`
	GitDrafts       map[string]domain.GitChoice  `json:"gitDrafts,omitempty"`
	GitRepositories map[string]string            `json:"gitRepositories,omitempty"`
	Inspectors      map[string]bool              `json:"inspectors,omitempty"`
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
		return nil
	}
	if err != nil {
		return err
	}
	var saved savedState
	if err = json.Unmarshal(b, &saved); err != nil {
		return err
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

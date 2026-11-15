package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"sort"
	"strings"
	"time"
)

func (m *Model) validateArchiveDeletion(d domain.ArchiveDeletion) error {
	if d.DeviceID != m.Snapshot.DeviceID {
		return errors.New("删除回执不属于当前设备")
	}
	if d.WorkspaceID != "" {
		for _, s := range m.Snapshot.Sessions {
			if slices.Contains(d.SessionIDs, s.ID) && s.WorkspaceID != d.WorkspaceID {
				return errors.New("删除回执中的会话不属于目标工作区")
			}
		}
	}
	for key, state := range m.DeviceStates {
		if key == m.sourceConnection || state.Snapshot.DeviceID == d.DeviceID {
			continue
		}
		for _, w := range state.Snapshot.Workspaces {
			if d.WorkspaceID != "" && w.ID == d.WorkspaceID {
				return errors.New("删除回执引用了其他设备的工作区")
			}
		}
		for _, s := range state.Snapshot.Sessions {
			if slices.Contains(d.SessionIDs, s.ID) {
				return errors.New("删除回执引用了其他设备的会话")
			}
		}
	}
	return nil
}

type ArchivedSession struct {
	Session   domain.Session
	Workspace domain.Workspace
}

func (m *Model) ArchivedSessions(query, workspace string) []ArchivedSession {
	q := strings.ToLower(strings.TrimSpace(query))
	byID := make(map[string]domain.Workspace, len(m.Snapshot.Workspaces))
	for _, w := range m.Snapshot.Workspaces {
		byID[w.ID] = w
	}
	var out []ArchivedSession
	for _, s := range m.Snapshot.Sessions {
		w, ok := byID[s.WorkspaceID]
		if !ok || workspace != "" && w.ID != workspace || workspace == "" && !w.Archived && !s.Archived {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(s.Title+" "+w.Name), q) {
			continue
		}
		out = append(out, ArchivedSession{Session: s, Workspace: w})
	}
	slices.SortStableFunc(out, func(a, b ArchivedSession) int {
		return archiveTime(b.Session.ArchivedAt, b.Workspace.ArchivedAt).Compare(archiveTime(a.Session.ArchivedAt, a.Workspace.ArchivedAt))
	})
	return out
}

func archiveTime(primary, fallback *time.Time) time.Time {
	if primary != nil {
		return *primary
	}
	if fallback != nil {
		return *fallback
	}
	return time.Time{}
}

func (m *Model) ArchivedWorkspaces(query string) []domain.Workspace {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []domain.Workspace
	for _, w := range m.Snapshot.Workspaces {
		if w.Archived && (q == "" || strings.Contains(strings.ToLower(w.Name+" "+m.DeviceName(w.DeviceID)), q)) {
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b domain.Workspace) int {
		return archiveTime(b.ArchivedAt, nil).Compare(archiveTime(a.ArchivedAt, nil))
	})
	return out
}

type ArchivePolicyChange struct {
	ID          string `json:"id"`
	Days        int    `json:"days"`
	Revision    int64  `json:"revision"`
	Error       string `json:"error,omitempty"`
	FailedEpoch string `json:"-"`
}

func (m *Model) ArchiveDays() int { return *m.Preferences.Normalized().ArchiveRetentionDays }

func (m *Model) SetArchiveRetention(days int) error {
	if !domain.ValidArchiveDays(days) {
		return errors.New("归档保留时间只支持 7 天、14 天、30 天或永不")
	}
	m.Preferences.ArchiveRetentionDays = &days
	if m.ArchivePolicyChanges == nil {
		m.ArchivePolicyChanges = map[string]ArchivePolicyChange{}
	}
	for _, state := range m.DeviceStates {
		if id := state.Snapshot.DeviceID; id != "" {
			revision := int64(0)
			if p := state.Snapshot.ArchivePolicy; p != nil {
				revision = p.Revision
			}
			m.ArchivePolicyChanges[id] = ArchivePolicyChange{ID: domain.NewID(), Days: days, Revision: revision}
		}
	}
	for _, connection := range m.Connections {
		if connection.DeviceID != "" {
			if _, ok := m.ArchivePolicyChanges[connection.DeviceID]; !ok {
				m.ArchivePolicyChanges[connection.DeviceID] = ArchivePolicyChange{ID: domain.NewID(), Days: days}
			}
		}
	}
	if len(m.DeviceStates) == 0 && m.Snapshot.DeviceID != "" {
		p := m.Snapshot.ArchivePolicy
		revision := int64(0)
		if p != nil {
			revision = p.Revision
		}
		m.ArchivePolicyChanges[m.Snapshot.DeviceID] = ArchivePolicyChange{ID: domain.NewID(), Days: days, Revision: revision}
	}
	return nil
}

// Jobs carry only explicit, persisted setting intent. Ordinary reconnects do
// not reapply the client's old preference over a worker's newer revision.
func (m *Model) ArchivePolicyJobs() []Job {
	if m.ArchivePolicyChanges == nil {
		m.ArchivePolicyChanges = map[string]ArchivePolicyChange{}
	}
	keys := make([]string, 0, len(m.DeviceStates))
	for key := range m.DeviceStates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var jobs []Job
	for _, key := range keys {
		state := m.DeviceStates[key]
		policy := state.Snapshot.ArchivePolicy
		if policy == nil || !state.Connected || state.WorkerMinor < 40 {
			continue
		}
		id := state.Snapshot.DeviceID
		change, ok := m.ArchivePolicyChanges[id]
		// A pristine new worker can inherit the saved default. An existing
		// worker's revision is never treated as a client-owned preference.
		if !ok && policy.Revision == 1 && policy.Days == 30 && m.ArchiveDays() != 30 {
			change = ArchivePolicyChange{ID: domain.NewID(), Days: m.ArchiveDays(), Revision: 1}
			m.ArchivePolicyChanges[id] = change
			ok = true
		}
		if !ok {
			continue
		}
		if policy.Days == change.Days {
			delete(m.ArchivePolicyChanges, id)
			continue
		}
		if change.Revision != 0 && change.Revision != policy.Revision {
			change.Error = "归档保留时间已被其他客户端更改，请重新选择"
			change.FailedEpoch = state.ConnectionEpoch
			m.ArchivePolicyChanges[id] = change
			continue
		}
		if change.Error != "" && change.FailedEpoch == state.ConnectionEpoch {
			continue
		}
		if change.Revision == 0 {
			change.Revision = policy.Revision
		}
		change.Error = ""
		m.ArchivePolicyChanges[id] = change
		input := protocol.ArchivePolicyInput{Days: change.Days, Revision: change.Revision}
		epoch := state.ConnectionEpoch
		jobs = append(jobs, Job{ConnectionID: key, ConnectionEpoch: state.ConnectionEpoch, Method: "archive.configure", Input: input, Apply: func(p *Model, body json.RawMessage, err error) {
			current, exists := p.ArchivePolicyChanges[id]
			if err != nil {
				if !exists || current.ID != change.ID {
					return
				}
				current.Error = err.Error()
				current.FailedEpoch = epoch
				p.ArchivePolicyChanges[id] = current
				return
			}
			var result domain.ArchivePolicy
			if json.Unmarshal(body, &result) != nil || result.DeviceID != id || result.Days != input.Days {
				if !exists || current.ID != change.ID {
					return
				}
				current.Error = "设备的归档设置回执无效"
				current.FailedEpoch = epoch
				p.ArchivePolicyChanges[id] = current
				return
			}
			if p.Snapshot.ArchivePolicy == nil || p.Snapshot.ArchivePolicy.Revision <= result.Revision {
				p.Snapshot.ArchivePolicy = &result
			}
			if !exists {
				return
			}
			if current.ID != change.ID {
				if current.Revision == input.Revision && result.Revision == input.Revision+1 {
					current.Revision = result.Revision
					current.Error = ""
					current.FailedEpoch = ""
					p.ArchivePolicyChanges[id] = current
				}
				return
			}
			delete(p.ArchivePolicyChanges, id)
		}})
	}
	return jobs
}

func (m *Model) ArchivePolicyStatus() string {
	var messages []string
	for id, c := range m.ArchivePolicyChanges {
		name := m.DeviceName(id)
		if c.Error != "" {
			messages = append(messages, name+"："+c.Error)
		} else {
			messages = append(messages, name+"：等待同步归档保留时间")
		}
	}
	if len(messages) == 0 {
		for _, state := range m.DeviceStates {
			if p := state.Snapshot.ArchivePolicy; p != nil && p.Days != m.ArchiveDays() {
				label := fmt.Sprintf("%d 天", p.Days)
				if p.Days == 0 {
					label = "永不"
				}
				messages = append(messages, m.DeviceName(p.DeviceID)+"：设备当前保留时间为"+label)
			}
		}
	}
	sort.Strings(messages)
	return strings.Join(messages, "\n")
}

// ApplyArchiveDeletion also accepts a validated command receipt, so a delayed
// event does not keep deleted history or a confirmation dialog on screen.
func (m *Model) ApplyArchiveDeletion(d domain.ArchiveDeletion) {
	for _, id := range d.SessionIDs {
		delete(m.Details, id)
		delete(m.HistoryReads, id)
		delete(m.HistoryOutlines, id)
		delete(m.HistoryAnchors, id)
		delete(m.HistoryOpenings, id)
		delete(m.HistoryEdits, id)
		delete(m.HistoryActions, id)
		delete(m.QueueEdits, id)
		delete(m.QueueExpanded, id)
		delete(m.FoldToolHistory, id)
		delete(m.Collapsed, id)
		delete(m.Pager, id)
		delete(m.Inspectors, id)
		delete(m.CompletedReplies, id)
		delete(m.completionReads, id)
		delete(m.Configs, "session:"+id)
		delete(m.HarnessConfigs, "session:"+id)
		delete(m.GitStatuses, "session:"+id)
		delete(m.GitEpochs, "session:"+id)
		for key := range m.Drafts {
			if key == "session:"+id || strings.HasPrefix(key, "history:"+id+":") {
				delete(m.Drafts, key)
				delete(m.Attachments, key)
				delete(m.DraftSkills, key)
			}
		}
		for key := range m.Attachments {
			if key == "session:"+id || strings.HasPrefix(key, "history:"+id+":") {
				delete(m.Attachments, key)
			}
		}
		for key := range m.DraftSkills {
			if key == "session:"+id || strings.HasPrefix(key, "history:"+id+":") {
				delete(m.DraftSkills, key)
			}
		}
		for key := range m.WorkDetails {
			if key.SessionID == id {
				delete(m.WorkDetails, key)
			}
		}
		for op, in := range m.Pending {
			if in.SessionID == id {
				delete(m.Pending, op)
				delete(m.PendingDevices, op)
				delete(m.UncertainSubmissions, op)
			}
		}
		m.Navigation.Pinned = slices.DeleteFunc(m.Navigation.Pinned, func(ref SessionLocation) bool { return ref.SessionID == id && ref.DeviceID == d.DeviceID })
		for i := range m.Navigation.Workspaces {
			p := &m.Navigation.Workspaces[i]
			if p.DeviceID == d.DeviceID {
				p.Sessions = slices.DeleteFunc(p.Sessions, func(s string) bool { return s == id })
			}
		}
		if m.SessionID == id {
			m.SessionID = ""
			m.FocusSecondary = false
		}
		if m.SecondarySessionID == id {
			m.SecondarySessionID = ""
			m.SecondaryWorkspaceID = ""
			m.FocusSecondary = false
		}
	}
	m.Snapshot.Sessions = slices.DeleteFunc(m.Snapshot.Sessions, func(s domain.Session) bool { return slices.Contains(d.SessionIDs, s.ID) })
	m.Snapshot.PendingRequests = slices.DeleteFunc(m.Snapshot.PendingRequests, func(r domain.PendingRequest) bool { return slices.Contains(d.SessionIDs, r.SessionID) })
	m.Snapshot.Operations = slices.DeleteFunc(m.Snapshot.Operations, func(o domain.Operation) bool { return slices.Contains(d.SessionIDs, o.SessionID) })
	if id := d.WorkspaceID; id != "" {
		m.Snapshot.Workspaces = slices.DeleteFunc(m.Snapshot.Workspaces, func(w domain.Workspace) bool { return w.ID == id && w.DeviceID == d.DeviceID })
		m.Navigation.Workspaces = slices.DeleteFunc(m.Navigation.Workspaces, func(w WorkspaceNavigation) bool { return w.WorkspaceID == id && w.DeviceID == d.DeviceID })
		delete(m.GitDrafts, id)
		delete(m.GitRepositories, id)
		delete(m.HomeHarnesses, id)
		delete(m.GitStatuses, "workspace:"+id)
		delete(m.GitEpochs, "workspace:"+id)
		if m.WorkspaceID == id {
			m.WorkspaceID = ""
		}
		if m.SecondaryWorkspaceID == id {
			m.SecondaryWorkspaceID = ""
			m.SecondarySessionID = ""
			m.FocusSecondary = false
		}
	}
}

func (m *Model) pruneArchiveSnapshot(snapshot domain.Snapshot) {
	if m.Snapshot.DeviceID == "" || m.Snapshot.DeviceID != snapshot.DeviceID {
		return
	}
	d := domain.ArchiveDeletion{DeviceID: snapshot.DeviceID}
	for _, old := range m.Snapshot.Sessions {
		if !slices.ContainsFunc(snapshot.Sessions, func(s domain.Session) bool { return s.ID == old.ID }) {
			d.SessionIDs = append(d.SessionIDs, old.ID)
		}
	}
	m.ApplyArchiveDeletion(d)
	for _, old := range slices.Clone(m.Snapshot.Workspaces) {
		if !slices.ContainsFunc(snapshot.Workspaces, func(w domain.Workspace) bool { return w.ID == old.ID }) {
			m.ApplyArchiveDeletion(domain.ArchiveDeletion{DeviceID: snapshot.DeviceID, WorkspaceID: old.ID})
		}
	}
}

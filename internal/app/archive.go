package app

import (
	"encoding/json"
	"errors"
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

type ArchiveRetentionDevice struct {
	ConnectionID string
	DeviceID     string
	Name         string
	Policy       *domain.ArchivePolicy
	Connected    bool
	Supported    bool
}

func (d ArchiveRetentionDevice) CanSet() bool {
	return d.DeviceID != "" && (!d.Connected || d.Supported)
}

func (m *Model) ArchiveRetentionDevices() []ArchiveRetentionDevice {
	devices := []ArchiveRetentionDevice{{ConnectionID: "local", DeviceID: m.LocalDeviceID, Name: m.LocalName()}}
	for _, c := range m.Connections {
		devices = append(devices, ArchiveRetentionDevice{ConnectionID: c.ID, DeviceID: c.DeviceID, Name: c.Name})
	}
	for i := range devices {
		d := &devices[i]
		if state := m.DeviceStates[d.ConnectionID]; state != nil {
			d.Policy = state.Snapshot.ArchivePolicy
			d.Connected, d.Supported = state.Connected, state.WorkerMinor >= 40
		} else if d.ConnectionID == "local" && len(m.DeviceStates) == 0 && m.Snapshot.DeviceID != "" {
			d.DeviceID, d.Policy = m.Snapshot.DeviceID, m.Snapshot.ArchivePolicy
			d.Connected, d.Supported = m.Connected, m.WorkerMinor >= 40
		}
	}
	return devices
}

func (m *Model) SetDeviceArchiveRetention(deviceID string, days int) error {
	if !domain.ValidArchiveDays(days) {
		return errors.New("归档保留期只支持 7 天、14 天、30 天或永久保留")
	}
	for _, device := range m.ArchiveRetentionDevices() {
		if device.DeviceID != deviceID || !device.CanSet() {
			continue
		}
		change := ArchivePolicyChange{ID: domain.NewID(), Days: days}
		if device.Policy != nil {
			change.Revision = device.Policy.Revision
		}
		if m.ArchivePolicyChanges == nil {
			m.ArchivePolicyChanges = map[string]ArchivePolicyChange{}
		}
		m.ArchivePolicyChanges[deviceID] = change
		return nil
	}
	return errors.New("当前无法设置这台设备的归档保留期")
}

// Jobs carry only explicit, persisted device changes. Let an in-flight job
// settle before clearing a newer choice that matches the old snapshot.
func (m *Model) ArchivePolicyJobs(inFlight func(connection, epoch string) bool) []Job {
	keys := make([]string, 0, len(m.DeviceStates))
	for key := range m.DeviceStates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var jobs []Job
	for _, key := range keys {
		state := m.DeviceStates[key]
		policy := state.Snapshot.ArchivePolicy
		if policy == nil || !state.Connected || state.WorkerMinor < 40 || (inFlight != nil && inFlight(key, state.ConnectionEpoch)) {
			continue
		}
		id := state.Snapshot.DeviceID
		change, ok := m.ArchivePolicyChanges[id]
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

// ApplyArchiveDeletion also accepts a validated command receipt, so a delayed
// event does not keep deleted history or a confirmation dialog on screen.
func (m *Model) ApplyArchiveDeletion(d domain.ArchiveDeletion) {
	if d.WorkspaceID != "" {
		m.deleteRetainedDrafts("", d.WorkspaceID)
	}
	for op, in := range m.Pending {
		preparingInWorkspace := d.WorkspaceID != "" && in.SessionID == "" && in.WorkspaceID == d.WorkspaceID
		if preparingInWorkspace || slices.Contains(d.SessionIDs, in.SessionID) {
			delete(m.Pending, op)
			delete(m.PendingDevices, op)
			delete(m.pendingGenerations, op)
			delete(m.UncertainSubmissions, op)
			delete(m.submissionAccepted, op)
			m.Resolved[op] = true
		}
	}
	for _, id := range d.SessionIDs {
		m.deleteRetainedDrafts(id, "")
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
		delete(m.sessionConfigDrafts, id)
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

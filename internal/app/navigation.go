package app

import (
	"errors"
	"shioji/internal/domain"
	"slices"
)

// Navigation choices belong to this client, never to a harness or execution context.
type SessionLocation struct {
	DeviceID    string `json:"deviceId"`
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
}
type WorkspaceNavigation struct {
	DeviceID    string   `json:"deviceId"`
	WorkspaceID string   `json:"workspaceId"`
	Sessions    []string `json:"sessions,omitempty"`
}
type NavigationPreferences struct {
	Pinned     []SessionLocation     `json:"pinned,omitempty"`
	Workspaces []WorkspaceNavigation `json:"workspaces,omitempty"`
}

func (p NavigationPreferences) Clone() NavigationPreferences {
	p.Pinned = slices.Clone(p.Pinned)
	p.Workspaces = slices.Clone(p.Workspaces)
	for i := range p.Workspaces {
		p.Workspaces[i].Sessions = slices.Clone(p.Workspaces[i].Sessions)
	}
	return p
}

// Observe new identities, not activity. Missing catalogs do not erase saved positions.
func (m *Model) ObserveNavigation() {
	for _, w := range m.Snapshot.Workspaces {
		i := slices.IndexFunc(m.Navigation.Workspaces, func(p WorkspaceNavigation) bool { return p.DeviceID == w.DeviceID && p.WorkspaceID == w.ID })
		if i < 0 {
			m.Navigation.Workspaces = append(m.Navigation.Workspaces, WorkspaceNavigation{DeviceID: w.DeviceID, WorkspaceID: w.ID})
			i = len(m.Navigation.Workspaces) - 1
		}
		p := &m.Navigation.Workspaces[i]
		known := make(map[string]bool, len(p.Sessions))
		for _, id := range p.Sessions {
			known[id] = true
		}
		var added []string
		for _, s := range m.Snapshot.Sessions {
			if s.WorkspaceID == w.ID && !known[s.ID] {
				added = append(added, s.ID)
				known[s.ID] = true
			}
		}
		if len(added) > 0 {
			p.Sessions = append(added, p.Sessions...)
		}
	}
}

// New accepted user input overrides only this session's ordinary workspace
// position. Keep global pins, workspace order and sibling order intact.
func (m *Model) promoteNavigationSession(id string) {
	ref, ok := m.NavigationSessionLocation(id)
	if !ok {
		return
	}
	session, workspace, ok := m.navigationSession(ref)
	if !ok || session.Archived || workspace.Archived {
		return
	}
	m.ObserveNavigation()
	for i := range m.Navigation.Workspaces {
		p := &m.Navigation.Workspaces[i]
		if p.DeviceID == ref.DeviceID && p.WorkspaceID == ref.WorkspaceID {
			if len(p.Sessions) > 0 && p.Sessions[0] == id {
				return
			}
			p.Sessions = slices.DeleteFunc(p.Sessions, func(value string) bool { return value == id })
			p.Sessions = slices.Insert(p.Sessions, 0, id)
			return
		}
	}
}
func (m *Model) NavigationSessionLocation(id string) (SessionLocation, bool) {
	for _, s := range m.Snapshot.Sessions {
		if s.ID != id {
			continue
		}
		for _, w := range m.Snapshot.Workspaces {
			if w.ID == s.WorkspaceID {
				return SessionLocation{w.DeviceID, w.ID, s.ID}, true
			}
		}
	}
	return SessionLocation{}, false
}
func (m *Model) navigationSession(ref SessionLocation) (domain.Session, domain.Workspace, bool) {
	for _, w := range m.Snapshot.Workspaces {
		if w.DeviceID != ref.DeviceID || w.ID != ref.WorkspaceID {
			continue
		}
		for _, s := range m.Snapshot.Sessions {
			if s.ID == ref.SessionID && s.WorkspaceID == w.ID {
				return s, w, true
			}
		}
	}
	return domain.Session{}, domain.Workspace{}, false
}
func (m *Model) SessionPinned(ref SessionLocation) bool {
	return slices.Contains(m.Navigation.Pinned, ref)
}
func (m *Model) NavigationPins() []domain.Session {
	result := []domain.Session{}
	seen := map[SessionLocation]bool{}
	for _, ref := range m.Navigation.Pinned {
		s, w, ok := m.navigationSession(ref)
		if ok && !s.Archived && !w.Archived && !seen[ref] {
			result = append(result, s)
			seen[ref] = true
		}
	}
	return result
}
func (m *Model) NavigationWorkspaces() []domain.Workspace {
	result := []domain.Workspace{}
	seen := map[string]bool{}
	for _, p := range m.Navigation.Workspaces {
		for _, w := range m.Snapshot.Workspaces {
			if p.DeviceID == w.DeviceID && p.WorkspaceID == w.ID && !seen[w.ID] {
				result = append(result, w)
				seen[w.ID] = true
			}
		}
	}
	for _, w := range m.Snapshot.Workspaces {
		if !seen[w.ID] {
			result = append(result, w)
			seen[w.ID] = true
		}
	}
	return result
}
func (m *Model) NavigationSessions(w domain.Workspace) []domain.Session {
	all := map[string]domain.Session{}
	for _, s := range m.Snapshot.Sessions {
		if s.WorkspaceID == w.ID {
			all[s.ID] = s
		}
	}
	var rank []string
	for _, p := range m.Navigation.Workspaces {
		if p.DeviceID == w.DeviceID && p.WorkspaceID == w.ID {
			rank = p.Sessions
			break
		}
	}
	ranked := make(map[string]bool, len(rank))
	for _, id := range rank {
		ranked[id] = true
	}
	result := []domain.Session{}
	appendSession := func(id string) {
		if s, ok := all[id]; ok {
			delete(all, id)
			if s.Archived || w.Archived || !m.SessionPinned(SessionLocation{w.DeviceID, w.ID, s.ID}) {
				result = append(result, s)
			}
		}
	}
	for _, s := range m.Snapshot.Sessions {
		if s.WorkspaceID == w.ID && !ranked[s.ID] {
			appendSession(s.ID)
		}
	}
	for _, id := range rank {
		appendSession(id)
	}
	return result
}
func (m *Model) SetSessionPinned(ref SessionLocation, pinned bool) error {
	s, w, ok := m.navigationSession(ref)
	if !ok || pinned && (s.Archived || w.Archived) {
		return errors.New("会话归属已改变或已归档")
	}
	if m.SessionPinned(ref) == pinned {
		return nil
	}
	m.ObserveNavigation()
	if pinned {
		m.Navigation.Pinned = append(m.Navigation.Pinned, ref)
	} else {
		m.Navigation.Pinned = slices.DeleteFunc(m.Navigation.Pinned, func(p SessionLocation) bool { return p == ref })
	}
	return nil
}

// A zero before means the end of the destination section; ordinary drops never move ownership.
func (m *Model) MoveNavigationSession(ref, before SessionLocation, pinned bool) error {
	s, w, ok := m.navigationSession(ref)
	if !ok || s.Archived || w.Archived {
		return errors.New("会话归属已改变或已归档")
	}
	if !pinned && (before.DeviceID != "" && before.DeviceID != ref.DeviceID || before.WorkspaceID != "" && before.WorkspaceID != ref.WorkspaceID) {
		return errors.New("只能放回所属工作区或全局置顶区")
	}
	if before.SessionID != "" {
		b, bw, valid := m.navigationSession(before)
		if !valid || b.Archived || bw.Archived || m.SessionPinned(before) != pinned || !pinned && (before.DeviceID != ref.DeviceID || before.WorkspaceID != ref.WorkspaceID) {
			return errors.New("只能放回所属工作区或全局置顶区")
		}
	}
	if before == ref {
		return nil
	}
	m.ObserveNavigation()
	m.Navigation.Pinned = slices.DeleteFunc(m.Navigation.Pinned, func(p SessionLocation) bool { return p == ref })
	if pinned {
		i := len(m.Navigation.Pinned)
		if before.SessionID != "" {
			i = slices.Index(m.Navigation.Pinned, before)
		}
		m.Navigation.Pinned = slices.Insert(m.Navigation.Pinned, i, ref)
	} else {
		for i := range m.Navigation.Workspaces {
			p := &m.Navigation.Workspaces[i]
			if p.DeviceID != ref.DeviceID || p.WorkspaceID != ref.WorkspaceID {
				continue
			}
			p.Sessions = slices.DeleteFunc(p.Sessions, func(id string) bool { return id == ref.SessionID })
			at := len(p.Sessions)
			if before.SessionID != "" {
				at = slices.Index(p.Sessions, before.SessionID)
			}
			p.Sessions = slices.Insert(p.Sessions, at, ref.SessionID)
			break
		}
	}
	return nil
}
func (m *Model) MoveNavigationWorkspace(device, id, beforeDevice, beforeID string) error {
	found, beforeFound := false, beforeID == ""
	for _, w := range m.Snapshot.Workspaces {
		found = found || w.DeviceID == device && w.ID == id
		beforeFound = beforeFound || w.DeviceID == beforeDevice && w.ID == beforeID
	}
	if !found || !beforeFound {
		return errors.New("工作区归属已改变")
	}
	if device == beforeDevice && id == beforeID {
		return nil
	}
	m.ObserveNavigation()
	i := slices.IndexFunc(m.Navigation.Workspaces, func(p WorkspaceNavigation) bool { return p.DeviceID == device && p.WorkspaceID == id })
	item := m.Navigation.Workspaces[i]
	m.Navigation.Workspaces = slices.Delete(m.Navigation.Workspaces, i, i+1)
	at := len(m.Navigation.Workspaces)
	if beforeID != "" {
		at = slices.IndexFunc(m.Navigation.Workspaces, func(p WorkspaceNavigation) bool { return p.DeviceID == beforeDevice && p.WorkspaceID == beforeID })
	}
	m.Navigation.Workspaces = slices.Insert(m.Navigation.Workspaces, at, item)
	return nil
}

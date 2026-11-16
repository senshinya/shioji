package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"time"
)

func (v *View) persistenceModel() *app.Model {
	if v.parent != nil {
		return v.parent.Model
	}
	return v.Model
}
func (v *View) splitRoot() *View {
	if v.parent != nil {
		return v.parent
	}
	return v
}
func (v *View) focusedView() *View {
	if v.Model.FocusSecondary && v.secondary != nil && v.Model.SecondarySessionID != "" && v.secondary.Model.SessionID == v.Model.SecondarySessionID {
		return v.secondary
	}
	return v
}
func (v *View) openParallel(id string) {
	root := v.splitRoot()
	if root.archive != nil {
		root.archive.open = false
		root.archive.viewing = ""
	}
	m := root.Model
	if id == m.SessionID {
		m.FocusSecondary = false
		return
	}
	if id != m.SecondarySessionID && root.secondary != nil {
		if state := root.secondary.lists[id]; state != nil {
			state.ScrollToEnd()
		}
	}
	m.SecondarySessionID = id
	m.SecondaryWorkspaceID = ""
	for _, s := range m.Snapshot.Sessions {
		if s.ID == id {
			m.SecondaryWorkspaceID = s.WorkspaceID
			break
		}
	}
	root.setSecondaryFocus(true)
	root.prepareSecondary()
	if root.secondary != nil && root.secondary.Model.SessionID == id {
		root.secondary.focusComposer = true
	}
	m.FoldToolHistory[id] = true
	root.readCompletedReply(id)
}
func (v *View) closeParallel() {
	root := v.splitRoot()
	root.Model.SecondarySessionID = ""
	if root.secondary != nil {
		root.secondary.sessionLoad = nil
	}
	root.setSecondaryFocus(false)
	root.focusComposer = true
}
func (v *View) navigateSession(id, workspace string) {
	root := v.splitRoot()
	if root.archive != nil {
		root.archive.open = false
		root.archive.viewing = ""
	}
	m := root.Model
	current, currentWorkspace := m.SessionID, m.WorkspaceID
	if m.FocusSecondary && m.SecondarySessionID != "" {
		current, currentWorkspace = m.SecondarySessionID, m.SecondaryWorkspaceID
	}
	if current != "" && current != id {
		root.focusedView().cancelPagedHistory()
		root.previousSession, root.previousWorkspace = current, currentWorkspace
	}
	root.workspaceEditor = nil
	root.devices = false
	if id == m.SessionID {
		m.FocusSecondary = false
	} else if id == m.SecondarySessionID {
		m.FocusSecondary = true
	} else if m.FocusSecondary && m.SecondarySessionID != "" {
		m.SecondarySessionID = id
		m.SecondaryWorkspaceID = workspace
	} else {
		m.SessionID = id
		m.WorkspaceID = workspace
	}
	m.FoldToolHistory[id] = true
	root.readCompletedReply(id)
}
func (v *View) prepareSecondary() {
	m := v.Model
	if v.secondary != nil && v.secondary.Model.SessionID != m.SecondarySessionID {
		v.secondary.cancelFileLink()
		v.secondary.cancelPagedHistory()
	}
	if m.SecondarySessionID == m.SessionID {
		m.SecondarySessionID = ""
		m.FocusSecondary = false
	}
	if m.SessionID == "" || v.devices || v.workspaceEditor != nil || m.SecondarySessionID == "" {
		return
	}
	var session domain.Session
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SecondarySessionID {
			session = s
			break
		}
	}
	if session.ID == "" {
		return
	}
	m.SecondaryWorkspaceID = session.WorkspaceID // A disconnected device may not have supplied its catalog yet.
	if v.secondary == nil {
		clone := *m
		clone.Error = "" // Operation errors belong to the pane that produced them.
		v.secondary = New(&clone, v.Controller, "")
		v.secondary.parent = v
		v.secondary.historyBusy = v.historyBusy
		v.secondary.historyChecked = v.historyChecked
		v.secondary.historyForked = v.historyForked
		v.secondary.StatePath = v.StatePath
		v.secondary.Saver = v.Saver
		v.secondary.attachmentStore = v.staging()
	}
	pane := v.secondary
	if pane.Model.SessionID != session.ID {
		if state := pane.lists[session.ID]; state != nil {
			state.ScrollToEnd()
		}
	}
	previousError := pane.Model.Error
	*pane.Model = *m
	pane.Model.SessionID = session.ID
	pane.Model.WorkspaceID = session.WorkspaceID
	pane.Model.RefreshDevices()
	pane.Model.Error = previousError
	pane.frameTime, pane.reduceMotion = v.frameTime, v.reduceMotion
	pane.md.Theme = v.md.Theme
	pane.syncQueuedMotions()
	pane.syncSendMotion()
	pane.syncSessionEntry()
	pane.syncReplyStreams(m.Details[session.ID])
	pane.requestGit(false)
}
func (v *View) parallelConversation(c *n.Context, t *n.Theme) {
	m := v.Model
	if v.secondary == nil || v.secondary.Model.SessionID != m.SecondarySessionID {
		v.prepareSecondary()
	}
	width, _ := c.Size()
	wide := width >= 1180
	pane := v.secondary
	available := pane != nil && pane.Model.SessionID == m.SecondarySessionID
	layout := n.Row(c.Key("conversation-layout")).Fill()
	buildRight := func() {
		motion := fadeMotion(c, 200*time.Millisecond)
		if wide {
			motion = columnMotion(c, layout.Bounds().W/2)
		}
		col := n.Column(c.Key("secondary-conversation")).FillHeight().Grow(1).MinWidth(0).Clip().Label("右侧会话").Transition(motion)
		if wide {
			col.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
		}
		focusVersion := v.paneFocusVersion
		col.Children(func() {
			if available {
				pane.bindMarkdownLinks(c, &pane.md)
				pane.conversation(c.Key("right-conversation"), t)
			} else {
				v.secondaryPlaceholder(c, t)
			}
		})
		if m.SecondarySessionID != "" && v.paneFocusVersion == focusVersion && (n.PointerPressedWithin(col) || col.FocusWithin()) {
			if !m.FocusSecondary {
				c.After(0)
			}
			m.FocusSecondary = true
		}
	}
	layout.Children(func() {
		if wide || !m.FocusSecondary || m.SecondarySessionID == "" {
			motion := fadeMotion(c, 200*time.Millisecond)
			motion.Size = true
			left := n.Column(c.Key("primary-conversation")).FillHeight().Grow(1).MinWidth(0).Clip().Label("左侧会话").Transition(motion)
			focusVersion := v.paneFocusVersion
			left.Children(func() { v.conversation(c.Key("left-conversation"), t) })
			if v.paneFocusVersion == focusVersion && (n.PointerPressedWithin(left) || left.FocusWithin()) {
				if m.FocusSecondary {
					c.After(0)
				}
				m.FocusSecondary = false
			}
		}
		if m.SecondarySessionID != "" && (wide || m.FocusSecondary) {
			buildRight()
		}
	})
	if !available {
		return
	}
	if pane.devices {
		v.deviceSelected = pane.deviceSelected
		v.deviceList = pane.deviceList
		v.devices = true
		pane.devices = false
	}
	if pane.workspaceEditor != nil {
		v.workspaceEditor = pane.workspaceEditor
		pane.workspaceEditor = nil
	}
	pane.managementDialogs(c.Key("secondary-dialogs"), t)
	pane.fileLinkDialog(c.Key("secondary-file-link-dialog"), t)
	pane.attachmentDialog(c.Key("secondary-attachment-dialog"), t)
}
func (v *View) updateVisibleSessions() {
	if v.Controller == nil {
		return
	}
	byDevice := map[string][]string{"local": {}}
	for _, connection := range v.Model.Connections {
		byDevice[connection.ID] = nil
	}
	for _, id := range []string{v.Model.SessionID, v.Model.SecondarySessionID} {
		if id == "" || v.devices || v.workspaceEditor != nil || v.Model.SessionID == "" || v.archive != nil && v.archive.open {
			continue
		}
		for _, s := range v.Model.Snapshot.Sessions {
			if s.ID == id {
				key := v.Model.WorkspaceConnection(s.WorkspaceID)
				if key != "" {
					byDevice[key] = append(byDevice[key], id)
				}
			}
		}
	}
	if v.Model.SecondarySessionID != "" && v.Model.SessionID != "" && !v.devices && v.workspaceEditor == nil && (v.archive == nil || !v.archive.open) {
		found := false
		for _, ids := range byDevice {
			for _, id := range ids {
				found = found || id == v.Model.SecondarySessionID
			}
		}
		if !found {
			if key := v.Model.WorkspaceConnection(v.Model.SecondaryWorkspaceID); key != "" {
				byDevice[key] = append(byDevice[key], v.Model.SecondarySessionID)
			}
		}
	}
	if v.visibleSessions == nil {
		v.visibleSessions = map[string]string{}
	}
	for key, ids := range byDevice {
		signature := strings.Join(ids, "/")
		if state := v.Model.DeviceStates[key]; state != nil {
			signature += "@" + state.ConnectionEpoch
		}
		if old, ok := v.visibleSessions[key]; ok && old == signature {
			continue
		}
		if v.Controller.Do(app.Job{ConnectionID: key, Method: "$visible", Input: ids}) {
			v.visibleSessions[key] = signature
		}
	}
}

func (v *View) composerLabel() string {
	if v.parent != nil {
		return "右侧会话输入"
	}
	return "消息输入"
}

func (v *View) focusedSessionID() string {
	if v.Model.FocusSecondary && v.Model.SecondarySessionID != "" {
		return v.Model.SecondarySessionID
	}
	return v.Model.SessionID
}
func (v *View) sessionOutputAnimating(id string) bool {
	if v.Model.SessionID == id && v.visibleOutputAnimating() {
		return true
	}
	return v.secondary != nil && v.Model.SecondarySessionID == id && v.secondary.visibleOutputAnimating()
}

func (v *View) pruneHistoryCaches() {
	m := v.Model
	m.PruneHistoryCache(m.SessionID, m.SecondarySessionID)
	for _, pane := range []*View{v, v.secondary} {
		if pane == nil {
			continue
		}
		for id := range pane.lists {
			if d, ok := m.Details[id]; (!ok || len(d.Items) == 0) && id != m.SessionID && id != m.SecondarySessionID {
				delete(pane.lists, id)
			}
		}
		for id := range pane.changes {
			if _, ok := m.Details[id]; !ok {
				delete(pane.changes, id)
			}
		}
	}
}

func (v *View) secondaryPlaceholder(c *n.Context, t *n.Theme) {
	n.Column(c).Fill().Children(func() {
		n.Row(c).FillWidth().Height(48).PaddingX(18).AlignItems(n.Center).Children(func() {
			n.Text(c, "正在打开会话").FontSize(13).Grow(1)
			if iconButton(c, queueRemoveIcon, "关闭并排会话").Clicked() {
				v.closeParallel()
			}
		})
		n.Column(c).Grow(1).FillWidth().AlignItems(n.Center).Justify(n.Center).Gap(10).Children(func() {
			n.Text(c, "等待所属设备的会话信息").FontSize(12).TextColor(t.TextMuted)
			key := v.Model.WorkspaceConnection(v.Model.SecondaryWorkspaceID)
			if ghost(c, "重试").Disabled(v.Controller == nil || key == "").Clicked() {
				v.Controller.Do(app.Job{ConnectionID: key, Method: "$reconnect"})
			}
		})
	})
}

func (v *View) setSecondaryFocus(secondary bool) {
	root := v.splitRoot()
	root.Model.FocusSecondary = secondary
	root.paneFocusVersion++
}

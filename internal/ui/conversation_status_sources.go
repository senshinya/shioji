package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func (v *View) historyConversationStatus() *conversationStatus {
	m := v.Model
	sid := m.SessionID
	if motion := v.historyExit; motion != nil && motion.in.SessionID == sid && (motion.phase != historyDispatched || v.historyBusy[sid]) {
		return nil
	}
	if v.historyEditorVisible() {
		return nil
	}
	if in, ok := m.HistoryActions[sid]; ok {
		title, action := "会话操作结果待核对", "核对结果"
		if v.historyForked[sid] {
			title, action = "分叉已保存", "继续完成"
		}
		disabled := v.historyBusy[sid] || !m.Connected
		return &conversationStatus{id: "history:" + in.OperationID, title: title, body: "原会话与修改稿已保留。请先核对本次操作，再继续。", details: m.Error, icon: forkIcon, priority: 1, actions: []statusAction{{label: action, disabled: disabled, run: func(*n.Context) { v.historyCommand(in, !v.historyForked[sid]) }}, {label: "取消本次操作", disabled: disabled, run: func(*n.Context) { v.cancelHistoryAction(in) }}}}
	}
	edit := m.HistoryEdits[sid]
	if edit.ItemID != "" && edit.OperationID == "" {
		key := m.HistoryDraftKey()
		return &conversationStatus{id: "history-draft:" + edit.ItemID, title: "修改稿已保留", body: "原消息已被替换，修改稿已保留。", icon: fileAttachmentIcon, priority: 7, actions: []statusAction{{label: "复制修改稿", run: func(c *n.Context) {
			c.WriteClipboard(referencePlainText(referenceDocumentText(v.composerContent(key))))
			v.reportFeedback(app.Feedback{Title: "修改稿已复制", Kind: app.FeedbackSuccess})
		}}, {label: "放弃编辑", accessibilityLabel: "放弃历史编辑", run: func(*n.Context) {
			delete(m.Drafts, key)
			delete(m.Attachments, key)
			delete(m.DraftSkills, key)
			m.CancelHistoryEdit()
		}}}}
	}
	return nil
}
func (v *View) restoreArchivedContext(kind string) {
	m := v.Model
	if kind == "工作区" {
		for _, w := range m.Snapshot.Workspaces {
			if w.ID == m.WorkspaceID {
				v.archiveWorkspace(w, false)
				return
			}
		}
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID {
			v.sessionChange("session.archive", s, "")
			return
		}
	}
}

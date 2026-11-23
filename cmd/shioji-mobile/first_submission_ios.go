package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

// The first input can be shown in a provisional transcript while the
// Worker prepares it. The composer draft remains in the model until receipt.
type mobileFirstSubmission struct {
	mobileSendScroll
	input     domain.Submission
	list      ui.ListState
	previewed bool
	session   string
}

type firstSubmissionView struct {
	key, caption string
	motion       *mobileSendScroll
}

func (a *mobileApp) firstSubmissionVisible(parent *mygo.Window) bool {
	first, m := a.newSessionSend, a.model
	if first == nil || parent == nil || parent != a.draftPage || first.epoch != m.ConnectionEpoch ||
		first.workspace != m.WorkspaceID || m.UncertainSubmissions[first.operation] {
		return false
	}
	_, sending := m.Pending[first.operation]
	return sending || m.SessionID != "" && m.Resolved[first.operation]
}

func (a *mobileApp) noteFirstSubmissionReceipt(op domain.Operation) {
	first := a.newSessionSend
	if first == nil || first.epoch != a.model.ConnectionEpoch || op.ID != first.operation || op.SessionID == "" {
		return
	}
	switch op.State {
	case domain.OperationAccepted, domain.OperationDispatched, domain.OperationConfirmed:
		first.session = op.SessionID
	}
}

func (a *mobileApp) selectAcceptedFirstSubmission() {
	if !a.firstSubmissionVisible(a.draftPage) || a.model.SessionID != "" {
		return
	}
	first := a.newSessionSend
	for _, op := range a.model.Snapshot.Operations {
		if op.ID == first.operation {
			a.noteFirstSubmissionReceipt(op)
			break
		}
	}
	if session := a.sessionByID(first.session); session.ID != "" && session.WorkspaceID == first.workspace {
		a.model.SessionID = session.ID
	}
}

func (a *mobileApp) initialSubmissionDetail(parent *mygo.Window, session domain.Session, detail *domain.SessionDetail) firstSubmissionView {
	first := a.newSessionSend
	key := ""
	promoted := (parent == a.chat || parent == a.detail) && session.ID != "" && session.ID == a.promotedDraftSession
	if !promoted && !a.firstSubmissionVisible(parent) {
		return firstSubmissionView{}
	}
	if promoted {
		key = a.promotedDraftOperation
		if first == nil || first.operation != key {
			return firstSubmissionView{key: key}
		}
		_, pending := a.model.Pending[key]
		if a.model.UncertainSubmissions[key] || !pending && a.firstSubmissionFailed(first) {
			a.newSessionSend = nil
			return firstSubmissionView{key: key}
		}
		if slices.ContainsFunc(detail.Items, func(item domain.Item) bool { return item.ID == "input:"+key }) &&
			(!pending || session.Status == domain.ExecutionRunning) {
			a.history[session.ID] = &first.list
			if first.progress >= 1 {
				a.newSessionSend = nil
				return firstSubmissionView{key: key}
			}
			return firstSubmissionView{key: key, motion: &first.mobileSendScroll}
		}
	}
	first.previewed = true
	in := first.input
	if key == "" {
		key = first.operation
	}
	// Reuse the real message layout, but only project client-local data here.
	// Navigation, draft clearing and history still require the owning Worker.
	*detail = domain.SessionDetail{Session: session, HistoryLoaded: true, Items: []domain.Item{{
		ID: "input:" + in.OperationID, Kind: "user", Text: in.Text,
		ReferencePositions: in.ReferencePositions, Attachments: in.Attachments, Skills: in.Skills,
		SentAt: &first.began,
	}}}
	caption := a.model.FirstSubmissionProgressLabel(key)
	if first.session != "" {
		caption = "正在准备会话"
	}
	if a.model.Resolved[key] || session.Status == domain.ExecutionRunning {
		caption = "进行中"
	}
	if !a.model.Connected {
		caption = "等待重新连接"
	}
	return firstSubmissionView{key: key, caption: caption, motion: &first.mobileSendScroll}
}

func (a *mobileApp) firstSubmissionFailed(first *mobileFirstSubmission) bool {
	return slices.ContainsFunc(a.operationFeedbackRecords(first.workspace, ""), func(feedback app.Feedback) bool {
		return feedback.ID == first.operation && feedback.Kind == app.FeedbackSubmissionFailed
	})
}

func (a *mobileApp) completeDraftSubmission(id string) {
	draft := a.draftPage
	if draft == nil {
		return
	}
	a.goalEntranceSession = a.model.WorkKey(id, "")
	if a.firstSubmissionVisible(draft) {
		a.promotedDraftSession, a.promotedDraftOperation = id, a.newSessionSend.operation
	}
	// On iPhone the draft is already a full-size conversation surface. Keep
	// its transcript mounted as the provisional input receives a session ID.
	if session := a.sessionByID(id); !isTablet() && a.chat == nil && session.ID != "" && a.firstSubmissionVisible(draft) {
		a.draftPage, a.draftReturnSession = nil, ""
		a.selectConversation(id, transcriptJump{})
		a.mountConversationPage(draft, "", "", nil)
		a.updateChatHeader()
		return
	}
	draft.OnClosed(func() { a.openSession(id) })
	if isTablet() {
		if a.wide && a.detail != nil {
			a.detail.Invalidate()
		}
		a.callUIKit("dismissForm", map[string]uintptr{"window": draft.NativeHandle()}, nil)
	} else {
		draft.Pop()
	}
}

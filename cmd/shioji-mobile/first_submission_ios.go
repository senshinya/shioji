package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

// The first input can be shown in a provisional transcript while the
// Worker prepares it. The composer draft remains in the model until receipt.
type mobileFirstSubmission struct {
	mobileSendScroll
	input     domain.Submission
	list      ui.ListState
	previewed bool
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

func (a *mobileApp) initialSubmissionDetail(parent *mygo.Window, session domain.Session, detail *domain.SessionDetail) firstSubmissionView {
	first := a.newSessionSend
	key := ""
	promoted := parent == a.chat && session.ID != "" && session.ID == a.promotedDraftSession
	if !promoted && !a.firstSubmissionVisible(parent) {
		return firstSubmissionView{}
	}
	if promoted {
		key = a.promotedDraftOperation
		if first == nil || first.operation != key {
			return firstSubmissionView{key: key}
		}
		if slices.ContainsFunc(detail.Items, func(item domain.Item) bool { return item.ID == "input:"+key }) {
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
	if a.model.SessionID != "" {
		return firstSubmissionView{key: key, caption: "进行中", motion: &first.mobileSendScroll}
	}
	return firstSubmissionView{key: key, caption: mobileWaitingCaption(a.model, first.operation), motion: &first.mobileSendScroll}
}

func (a *mobileApp) completeDraftSubmission(id string) {
	draft := a.draftPage
	if draft == nil {
		return
	}
	a.goalEntranceSession = a.model.WorkKey(id, "")
	// On iPhone the draft is already a full-size conversation surface. Keep
	// its transcript mounted as the provisional input receives a session ID.
	if session := a.sessionByID(id); !isTablet() && a.chat == nil && session.ID != "" && a.firstSubmissionVisible(draft) {
		a.promotedDraftSession, a.promotedDraftOperation = id, a.newSessionSend.operation
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

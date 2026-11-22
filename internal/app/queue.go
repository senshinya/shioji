package app

import (
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type QueueEdit struct {
	Submitted            *QueueEditDraft   `json:"submitted,omitempty"`
	DisplacedSkills      []DraftSkill      `json:"displacedSkills,omitempty"`
	Token                string            `json:"token,omitempty"`
	DisplacedAttachments []DraftAttachment `json:"displacedAttachments,omitempty"`
	ID                   string            `json:"id"`
	Revision             int64             `json:"revision"`
	Displaced            string            `json:"displaced"`
}

type QueueEditDraft struct {
	Text        string            `json:"text"`
	Attachments []DraftAttachment `json:"attachments,omitempty"`
	Skills      []DraftSkill      `json:"skills,omitempty"`
}

func (m *Model) ActiveQueueEdit(sid string) QueueEdit {
	edit := m.QueueEdits[sid]
	if edit.Submitted != nil {
		return QueueEdit{}
	}
	return edit
}

func (m *Model) SuspendQueueEdit(sid string) {
	edit := m.ActiveQueueEdit(sid)
	if edit.ID == "" {
		return
	}
	key := "session:" + sid
	edit.Submitted = &QueueEditDraft{Text: m.Drafts[key], Attachments: m.Attachments[key], Skills: m.DraftSkills[key]}
	m.CancelQueueEdit(sid)
	m.QueueEdits[sid] = edit
}

func (m *Model) ResumeQueueEdit(sid string, revision int64) {
	edit := m.QueueEdits[sid]
	if edit.Submitted == nil {
		return
	}
	key := "session:" + sid
	text, attachments, skills := edit.Submitted.Text, edit.Submitted.Attachments, edit.Submitted.Skills
	edit.Displaced, edit.DisplacedAttachments, edit.DisplacedSkills = m.Drafts[key], m.Attachments[key], m.DraftSkills[key]
	edit.Submitted, edit.Revision = nil, revision
	m.QueueEdits[sid] = edit
	m.Drafts[key], m.Attachments[key], m.DraftSkills[key] = text, attachments, skills
}

func (m *Model) Queue() domain.QueueState { return m.Details[m.SessionID].Queue }
func (m *Model) Active() bool {
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID {
			return s.Status.Active()
		}
	}
	return false
}
func (m *Model) ActiveTurn() string {
	return m.ActiveTurnForSession(m.SessionID)
}

func (m *Model) ActiveTurnForSession(sid string) string {
	for _, t := range m.Details[sid].Turns {
		if t.Status == "running" {
			return t.ID
		}
	}
	return ""
}

// BeginMessageInSession uses the draft and live state of another session on
// this model's Worker without moving the selected conversation. BeginMessage
// records the submission and receipt scope in the owning model's maps.
func (m *Model) BeginMessageInSession(id string, steer bool) (domain.Submission, string, string, error) {
	selected, err := m.queueSession(id)
	if err != nil {
		return domain.Submission{}, "", "", err
	}
	return selected.BeginMessage(steer)
}

func (m *Model) queueSession(id string) (*Model, error) {
	for _, session := range m.Snapshot.Sessions {
		if id == "" || session.ID != id {
			continue
		}
		device := m.workspaceDevice(session.WorkspaceID)
		if device == "" || device != m.Snapshot.DeviceID || session.Context.DeviceID != "" && session.Context.DeviceID != device {
			break
		}
		selected := *m
		selected.SessionID, selected.WorkspaceID = id, session.WorkspaceID
		return &selected, nil
	}
	return nil, errors.New("会话不属于当前执行设备")
}

func (m *Model) BeginMessage(steer bool) (domain.Submission, string, string, error) {
	method, turn := "session.submit", ""
	_, goalCommand := domain.GoalCommand(m.Drafts[m.DraftKey()])
	goalActive := false
	for _, session := range m.Snapshot.Sessions {
		if session.ID == m.SessionID {
			goalActive = session.Goal.Active()
			break
		}
	}
	if m.SessionID != "" && (m.Active() || goalActive || len(m.Queue().Items) > 0) {
		if m.WorkerMinor < 2 {
			return domain.Submission{}, "", "", errors.New("请先升级 worker 以使用队列与 Steer")
		}
		method = "turn.enqueue"
	}
	if steer && m.Active() && !goalCommand {
		if !m.RunReadiness().Capabilities.Steer {
			return domain.Submission{}, "", "", errors.New("此 Agent 不支持引导当前执行")
		}
		method = "turn.steer"
		turn = m.ActiveTurn()
		if turn == "" {
			return domain.Submission{}, "", "", errors.New("正在等待当前轮次确认")
		}
	}
	in, err := m.beginSubmission(method)
	return in, method, turn, err
}
func (m *Model) HasPendingInput() bool {
	return m.HasPendingInputForSession(m.SessionID)
}
func (m *Model) HasPendingInputForSession(id string) bool {
	for _, in := range m.Pending {
		if in.SessionID == id && (id != "" || m.submissionDraftKey(in) == "home") {
			return true
		}
	}
	return false
}
func (m *Model) EditQueued(id string) error {
	if m.HistoryEdits[m.SessionID].ItemID != "" {
		return errors.New("请先结束历史消息编辑")
	}
	if m.HasPendingInput() {
		return errors.New("请等待消息投递确认后再编辑队列")
	}
	if m.QueueEdits[m.SessionID].ID != "" {
		return errors.New("请先保存或取消当前编辑")
	}
	q := m.Queue()
	for _, item := range q.Items {
		if item.ID == id {
			m.QueueEdits[m.SessionID] = QueueEdit{DisplacedSkills: m.DraftSkills[m.DraftKey()], Token: domain.NewID(), ID: id, Revision: q.Revision, DisplacedAttachments: m.Attachments[m.DraftKey()], Displaced: m.Drafts[m.DraftKey()]}
			m.Drafts[m.DraftKey()] = item.Input.Text
			m.DraftSkills[m.DraftKey()] = nil
			for _, ref := range item.Input.Skills {
				m.DraftSkills[m.DraftKey()] = append(m.DraftSkills[m.DraftKey()], DraftSkill{Reference: ref, WorkspaceID: item.Input.WorkspaceID, DeviceID: m.workspaceDevice(item.Input.WorkspaceID), HarnessID: m.HarnessID()})
			}
			m.Attachments[m.DraftKey()] = nil
			for _, a := range item.Input.Attachments {
				m.Attachments[m.DraftKey()] = append(m.Attachments[m.DraftKey()], DraftAttachment{Attachment: a, DeviceID: m.workspaceDevice(item.Input.WorkspaceID), WorkspaceID: item.Input.WorkspaceID, Ready: true})
			}
			m.restoreReferencePositions(m.DraftKey(), item.Input.ReferencePositions)
			return nil
		}
	}
	return errors.New("待发消息已派发或移除")
}

func (m *Model) EditQueuedInSession(sid, id string) error {
	selected, err := m.queueSession(sid)
	if err != nil {
		return err
	}
	return selected.EditQueued(id)
}

func (m *Model) QueueEditInput(sid string) (protocol.QueueInput, error) {
	var in protocol.QueueInput
	selected, err := m.queueSession(sid)
	if err != nil {
		return in, err
	}
	edit, key := m.ActiveQueueEdit(sid), selected.DraftKey()
	if edit.ID == "" || !m.HasDraftContent(key) {
		return in, errors.New("待发消息编辑已结束，或消息为空")
	}
	attachments, err := selected.SubmissionAttachments(selected.WorkspaceID)
	if err != nil {
		return in, err
	}
	skills, err := selected.SubmissionSkills()
	if err != nil {
		return in, err
	}
	if skills == nil {
		skills = []domain.SkillReference{}
	}
	return protocol.QueueInput{SessionID: sid, ID: edit.ID, Revision: edit.Revision, Text: m.Drafts[key],
		Attachments: attachments, Skills: skills, ReferencePositions: m.DraftReferencePositions(key)}, nil
}
func (m *Model) CancelQueueEdit(sid string) {
	if edit := m.ActiveQueueEdit(sid); edit.ID != "" {
		m.Drafts["session:"+sid] = edit.Displaced
		m.DraftSkills["session:"+sid] = edit.DisplacedSkills
		m.Attachments["session:"+sid] = edit.DisplacedAttachments
		delete(m.QueueEdits, sid)
	}
}
func (m *Model) SetQueue(q domain.QueueState) {
	d := m.Details[q.SessionID]
	if q.Revision >= d.Queue.Revision {
		d.Queue = q
		m.Details[q.SessionID] = d
	}
}
func (m *Model) FinishQueueEdit(sid, id, text string, q domain.QueueState, attachments ...[]domain.Attachment) {
	m.SetQueue(q)
	edit := m.ActiveQueueEdit(sid)
	if edit.ID != id {
		return
	}
	sameAttachments := true
	if len(attachments) > 0 {
		current := m.Attachments["session:"+sid]
		sameAttachments = len(current) == len(attachments[0])
		if sameAttachments {
			for i, a := range current {
				if a.Attachment != attachments[0][i] {
					sameAttachments = false
					break
				}
			}
		}
	}
	sameSkills := true
	for _, item := range q.Items {
		if item.ID == id {
			current := m.DraftSkills["session:"+sid]
			sameSkills = len(current) == len(item.Input.Skills)
			if sameSkills {
				for i, ref := range current {
					if ref.Reference != item.Input.Skills[i] {
						sameSkills = false
						break
					}
				}
			}
			break
		}
	}
	if m.Drafts["session:"+sid] == text && sameAttachments && sameSkills {
		m.CancelQueueEdit(sid)
	} else {
		edit.Revision = q.Revision
		m.QueueEdits[sid] = edit
	}
}

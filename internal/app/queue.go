package app

import (
	"errors"
	"shioji/internal/domain"
)

type QueueEdit struct {
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	Displaced string `json:"displaced"`
}

func (m *Model) Queue() domain.QueueState { return m.Details[m.SessionID].Queue }
func (m *Model) Active() bool {
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID {
			return s.Status == "running" || s.Status == "starting"
		}
	}
	return false
}
func (m *Model) ActiveTurn() string {
	for _, t := range m.Details[m.SessionID].Turns {
		if t.Status == "running" {
			return t.ID
		}
	}
	return ""
}
func (m *Model) BeginMessage(steer bool) (domain.Submission, string, string, error) {
	method, turn := "session.submit", ""
	if m.SessionID != "" && (m.Active() || len(m.Queue().Items) > 0) {
		if m.WorkerMinor < 2 {
			return domain.Submission{}, "", "", errors.New("请先升级 worker 以使用队列与 Steer")
		}
		method = "turn.enqueue"
	}
	if steer && m.Active() {
		method = "turn.steer"
		turn = m.ActiveTurn()
		if turn == "" {
			return domain.Submission{}, "", "", errors.New("正在等待当前轮次确认")
		}
	}
	in, err := m.beginSubmission(method != "session.submit")
	return in, method, turn, err
}
func (m *Model) HasPendingInput() bool {
	for _, in := range m.Pending {
		if in.SessionID == m.SessionID {
			return true
		}
	}
	return false
}
func (m *Model) EditQueued(id string) error {
	if m.HasPendingInput() {
		return errors.New("请等待消息投递确认后再编辑队列")
	}
	if m.QueueEdits[m.SessionID].ID != "" {
		return errors.New("请先保存或取消当前编辑")
	}
	q := m.Queue()
	for _, item := range q.Items {
		if item.ID == id {
			m.QueueEdits[m.SessionID] = QueueEdit{ID: id, Revision: q.Revision, Displaced: m.Drafts[m.DraftKey()]}
			m.Drafts[m.DraftKey()] = item.Input.Text
			return nil
		}
	}
	return errors.New("待发消息已派发或移除")
}
func (m *Model) CancelQueueEdit(sid string) {
	if edit := m.QueueEdits[sid]; edit.ID != "" {
		m.Drafts["session:"+sid] = edit.Displaced
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
func (m *Model) FinishQueueEdit(sid, id, text string, q domain.QueueState) {
	m.SetQueue(q)
	edit := m.QueueEdits[sid]
	if edit.ID != id {
		return
	}
	if m.Drafts["session:"+sid] == text {
		m.CancelQueueEdit(sid)
	} else {
		edit.Revision = q.Revision
		m.QueueEdits[sid] = edit
	}
}

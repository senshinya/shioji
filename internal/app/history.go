package app

import (
	"errors"
	"shioji/internal/domain"
)

type HistoryEdit struct {
	ItemID        string `json:"itemId"`
	OperationID   string `json:"operationId,omitempty"`
	SubmittedText string `json:"submittedText,omitempty"`
}

func (m *Model) BeginHistoryEdit(item domain.Item) error {
	if m.SessionID == "" || item.Kind != "user" || item.Status != "sent" || item.TurnID == "" {
		return errors.New("这条消息尚不能修改")
	}
	if m.QueueEdits[m.SessionID].ID != "" {
		return errors.New("请先结束待发送消息的编辑")
	}
	d := m.Details[m.SessionID]
	for _, earlier := range d.Items {
		if earlier.Kind == "user" && earlier.TurnID == item.TurnID && earlier.Order < item.Order {
			return errors.New("轮次内的追加消息暂不能单独重开")
		}
	}
	if old := m.HistoryEdits[m.SessionID]; old.OperationID != "" {
		return errors.New("先核对上次重开的结果")
	}
	m.HistoryEdits[m.SessionID] = HistoryEdit{ItemID: item.ID}
	key := m.HistoryDraftKey()
	if _, ok := m.Drafts[key]; !ok {
		m.Drafts[key] = item.Text
	}
	return nil
}
func (m *Model) CancelHistoryEdit() { delete(m.HistoryEdits, m.SessionID) }

func (m *Model) HistoryDraftKey() string {
	return "history:" + m.SessionID + ":" + m.HistoryEdits[m.SessionID].ItemID
}

func (m *Model) confirmHistoryOperation(op domain.Operation) {
	edit := m.HistoryEdits[op.SessionID]
	if edit.OperationID == "" || edit.OperationID != op.ID {
		return
	}
	if op.State == "rejected" || op.State == "abandoned" {
		// The committed replacement input is still visible as a failed message.
		// Keep its edit draft too; neither outcome may replace the ordinary draft.
		delete(m.HistoryEdits, op.SessionID)
		delete(m.HistoryActions, op.SessionID)
		m.Error = op.Error
		return
	}
	if op.State != "confirmed" {
		return
	}
	key := "history:" + op.SessionID + ":" + edit.ItemID
	if m.Drafts[key] == edit.SubmittedText {
		delete(m.Drafts, key)
	}
	delete(m.HistoryEdits, op.SessionID)
	delete(m.HistoryActions, op.SessionID)
	m.Error = ""
}
func (m *Model) SyncHistoryEdits() {
	for _, op := range m.Snapshot.Operations {
		m.confirmHistoryOperation(op)
	}
}

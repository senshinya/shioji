package app

import (
	"errors"
	"shioji/internal/domain"
	"slices"
)

type HistoryEdit struct {
	ItemID              string                    `json:"itemId"`
	OperationID         string                    `json:"operationId,omitempty"`
	SubmittedText       string                    `json:"submittedText,omitempty"`
	SubmittedReferences *domain.HistoryReferences `json:"submittedReferences,omitempty"`
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
	m.initializeHistoryReferences(key, item, d.Session)
	return nil
}
func (m *Model) CancelHistoryEdit() {
	// Closing an unresolved submitted operation retains its recovery draft.
	// Canceling a local edit discards its text and references together.
	if m.HistoryEdits[m.SessionID].OperationID == "" {
		key := m.HistoryDraftKey()
		delete(m.Drafts, key)
		delete(m.Attachments, key)
		delete(m.DraftSkills, key)
	}
	delete(m.HistoryEdits, m.SessionID)
}

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
	if m.Drafts[key] == edit.SubmittedText && (edit.SubmittedReferences == nil || sameHistoryReferences(m.draftReferences(key), *edit.SubmittedReferences)) {
		delete(m.Drafts, key)
		delete(m.Attachments, key)
		delete(m.DraftSkills, key)
	}
	delete(m.HistoryEdits, op.SessionID)
	delete(m.HistoryActions, op.SessionID)
	m.Error = ""
}
func (m *Model) SyncHistoryEdits() {
	for sid, edit := range m.HistoryEdits {
		if edit.OperationID != "" {
			continue
		}
		d := m.Details[sid]
		for _, item := range d.Items {
			if item.ID == edit.ItemID {
				m.initializeHistoryReferences("history:"+sid+":"+edit.ItemID, item, d.Session)
				break
			}
		}
	}
	for _, op := range m.Snapshot.Operations {
		m.confirmHistoryOperation(op)
	}
}

func (m *Model) draftReferences(key string) domain.HistoryReferences {
	r := domain.HistoryReferences{ReferencePositions: m.DraftReferencePositions(key)}
	for _, a := range m.Attachments[key] {
		r.Attachments = append(r.Attachments, a.Attachment)
	}
	for _, skill := range m.DraftSkills[key] {
		r.Skills = append(r.Skills, skill.Reference)
	}
	return r
}
func sameHistoryReferences(a, b domain.HistoryReferences) bool {
	return slices.Equal(a.Attachments, b.Attachments) && slices.Equal(a.Skills, b.Skills) && slices.Equal(a.ReferencePositions, b.ReferencePositions)
}
func (m *Model) HistoryReferences() (*domain.HistoryReferences, error) {
	key := m.HistoryDraftKey()
	attachments, err := m.submissionAttachments(key, m.WorkspaceID)
	if err != nil {
		return nil, err
	}
	skills, err := m.submissionSkills(key, m.WorkspaceID, m.HarnessID())
	if err != nil {
		return nil, err
	}
	r := &domain.HistoryReferences{Attachments: attachments, Skills: skills, ReferencePositions: m.DraftReferencePositions(key)}
	if m.WorkerMinor < 12 {
		inline := len(skills) > 0
		for _, a := range attachments {
			inline = inline || a.SourcePath != ""
		}
		for _, item := range m.Details[m.SessionID].Items {
			if item.ID == m.HistoryEdits[m.SessionID].ItemID {
				inline = inline || len(item.Skills) > 0
				for _, a := range item.Attachments {
					inline = inline || a.SourcePath != ""
				}
			}
		}
		if inline {
			return nil, errors.New("请先升级执行服务以编辑含行内引用的历史消息")
		}

		for _, item := range m.Details[m.SessionID].Items {
			if item.ID == m.HistoryEdits[m.SessionID].ItemID && slices.Equal(attachments, item.Attachments) && slices.Equal(skills, item.Skills) {
				return nil, nil
			}
		}
		return nil, errors.New("请先升级执行服务以修改历史消息的引用")
	}
	return r, nil
}

// Map-key presence distinguishes a legacy text-only draft from a new draft
// whose references were explicitly removed. Empty/null values persist the key.
func (m *Model) initializeHistoryReferences(key string, item domain.Item, source domain.Session) {
	if _, initialized := m.Attachments[key]; initialized {
		return
	}
	m.Attachments[key] = []DraftAttachment{}
	m.DraftSkills[key] = []DraftSkill{}
	for _, a := range item.Attachments {
		m.Attachments[key] = append(m.Attachments[key], DraftAttachment{Attachment: a, WorkspaceID: source.WorkspaceID, DeviceID: m.workspaceDevice(source.WorkspaceID), Ready: true})
	}
	for _, ref := range item.Skills {
		m.DraftSkills[key] = append(m.DraftSkills[key], DraftSkill{Reference: ref, WorkspaceID: source.WorkspaceID, DeviceID: m.workspaceDevice(source.WorkspaceID), HarnessID: source.HarnessID})
	}
	if m.Drafts[key] == item.Text {
		m.restoreReferencePositions(key, item.ReferencePositions)
	}
}

package app

import "shioji/internal/domain"

func (m *Model) restoreRejectedDraft(in domain.Submission) {
	if in.Continue || in.QuestionReply != nil {
		return
	}
	key := "home"
	if in.SessionID != "" {
		key = "session:" + in.SessionID
	}
	if !m.draftEmpty(key) || in.SessionID == "" && m.WorkspaceID != in.WorkspaceID {
		return
	}
	device := m.workspaceDevice(in.WorkspaceID)
	m.Drafts[key] = in.Text
	for _, a := range in.Attachments {
		m.Attachments[key] = append(m.Attachments[key], DraftAttachment{Attachment: a, DeviceID: device, WorkspaceID: in.WorkspaceID, Ready: true})
	}
	for _, ref := range in.Skills {
		m.DraftSkills[key] = append(m.DraftSkills[key], DraftSkill{Reference: ref, DeviceID: device, WorkspaceID: in.WorkspaceID, HarnessID: in.HarnessID})
	}
	m.restoreReferencePositions(key, in.ReferencePositions)
}

func (m *Model) draftEmpty(key string) bool {
	return m.Drafts[key] == "" && len(m.Attachments[key]) == 0 && len(m.DraftSkills[key]) == 0
}

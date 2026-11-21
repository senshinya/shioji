package app

import (
	"slices"
	"strings"

	"shioji/internal/domain"
)

func retainedDraftPrefix(session, workspace string) string {
	if session != "" {
		return "retained:session:" + session + ":"
	}
	return "retained:home:" + workspace + ":"
}

func (m *Model) retainRejectedDraft(in domain.Submission) {
	if in.Continue || in.QuestionReply != nil {
		return
	}
	key := "home"
	if in.SessionID != "" {
		key = "session:" + in.SessionID
	}
	if m.Drafts[key] == in.Text && slices.Equal(m.DraftReferencePositions(key), in.ReferencePositions) &&
		slices.EqualFunc(m.Attachments[key], in.Attachments, func(a DraftAttachment, b domain.Attachment) bool { return a.Attachment == b }) &&
		slices.EqualFunc(m.DraftSkills[key], in.Skills, func(a DraftSkill, b domain.SkillReference) bool { return a.Reference == b }) {
		return
	}
	if m.HasDraftContent(key) || in.SessionID == "" && m.WorkspaceID != in.WorkspaceID {
		key = retainedDraftPrefix(in.SessionID, in.WorkspaceID) + in.OperationID
	}
	device := m.workspaceDevice(in.WorkspaceID)
	m.Drafts[key] = in.Text
	m.Attachments[key], m.DraftSkills[key] = nil, nil
	for _, a := range in.Attachments {
		m.Attachments[key] = append(m.Attachments[key], DraftAttachment{Attachment: a, DeviceID: device, WorkspaceID: in.WorkspaceID, Ready: true})
	}
	for _, ref := range in.Skills {
		m.DraftSkills[key] = append(m.DraftSkills[key], DraftSkill{Reference: ref, DeviceID: device, WorkspaceID: in.WorkspaceID, HarnessID: in.HarnessID})
	}
	m.restoreReferencePositions(key, in.ReferencePositions)
}

func (m *Model) RetainedDrafts() []string {
	prefix := retainedDraftPrefix(m.SessionID, m.WorkspaceID)
	var keys []string
	for key := range m.Drafts {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func (m *Model) SwapRetainedDraft(key string) {
	if _, exists := m.Drafts[key]; !exists || !strings.HasPrefix(key, retainedDraftPrefix(m.SessionID, m.WorkspaceID)) {
		return
	}
	active := m.DraftKey()
	m.Drafts[active], m.Drafts[key] = m.Drafts[key], m.Drafts[active]
	m.Attachments[active], m.Attachments[key] = m.Attachments[key], m.Attachments[active]
	m.DraftSkills[active], m.DraftSkills[key] = m.DraftSkills[key], m.DraftSkills[active]
	if !m.HasDraftContent(key) {
		delete(m.Drafts, key)
		delete(m.Attachments, key)
		delete(m.DraftSkills, key)
	}
}

func (m *Model) deleteRetainedDrafts(session, workspace string) {
	prefix := retainedDraftPrefix(session, workspace)
	for key := range m.Drafts {
		if strings.HasPrefix(key, prefix) {
			delete(m.Drafts, key)
			delete(m.Attachments, key)
			delete(m.DraftSkills, key)
		}
	}
}

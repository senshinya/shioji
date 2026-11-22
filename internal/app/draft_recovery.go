package app

import "shioji/internal/domain"

func retainedHomeDraftKey(workspace, operation string) string {
	return "retained:home:" + workspace + ":" + operation
}

func (m *Model) submissionDraftKey(in domain.Submission) string {
	if in.SessionID != "" {
		return "session:" + in.SessionID
	}
	key := retainedHomeDraftKey(in.WorkspaceID, in.OperationID)
	if _, retained := m.Drafts[key]; retained {
		return key
	}
	return "home"
}

// A new composer releases the submitted draft while the owning worker is
// still settling its operation. The retained key survives client restarts.
func (m *Model) detachHomeSubmission() {
	for _, in := range m.Pending {
		if m.submissionDraftKey(in) == "home" {
			m.writeSubmissionDraft(retainedHomeDraftKey(in.WorkspaceID, in.OperationID), in)
			m.clearSubmittedDraft("home", in)
		}
	}
}

func (m *Model) HasRetainedHomeDraft(id string) bool {
	return m.SessionID == "" && !m.draftEmpty(retainedHomeDraftKey(m.WorkspaceID, id))
}

func (m *Model) RestoreRetainedHomeDraft(id string) {
	if !m.HasRetainedHomeDraft(id) || m.HasDraftContent("home") || m.HasPendingInput() {
		return
	}
	if _, pending := m.Pending[id]; pending {
		return
	}
	key := retainedHomeDraftKey(m.WorkspaceID, id)
	m.Drafts["home"], m.Attachments["home"], m.DraftSkills["home"] = m.Drafts[key], m.Attachments[key], m.DraftSkills[key]
	m.deleteDraft(key)
}

func (m *Model) clearSubmittedDraft(key string, in domain.Submission) {
	if in.QuestionReply != nil {
		return
	}
	if !in.Continue && m.Drafts[key] == in.Text {
		m.Drafts[key] = ""
	}
	m.clearAcceptedAttachments(key, in.Attachments)
	m.clearAcceptedSkills(key, in)
}

func (m *Model) restoreRejectedDraft(in domain.Submission) {
	if in.Continue || in.QuestionReply != nil {
		return
	}
	key := m.submissionDraftKey(in)
	if !m.draftEmpty(key) || in.SessionID == "" && m.WorkspaceID != in.WorkspaceID {
		return
	}
	m.writeSubmissionDraft(key, in)
}

func (m *Model) writeSubmissionDraft(key string, in domain.Submission) {
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

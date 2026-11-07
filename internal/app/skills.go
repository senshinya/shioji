package app

import (
	"errors"
	"shioji/internal/domain"
	"strings"
)

type DraftSkill struct {
	// InlinePosition is a one-based rune offset in the client composer.
	InlinePosition int                   `json:"inlinePosition,omitempty"`
	Reference      domain.SkillReference `json:"reference"`
	DeviceID       string                `json:"deviceId"`
	WorkspaceID    string                `json:"workspaceId"`
	HarnessID      string                `json:"harnessId"`
}

func (m *Model) SubmissionSkills() ([]domain.SkillReference, error) {
	var out []domain.SkillReference
	for _, skill := range m.DraftSkills[m.DraftKey()] {
		if m.WorkerMinor < 11 || !m.RunReadiness().Capabilities.Skills {
			return nil, errors.New("当前 Agent 不支持 Skill 引用")
		}
		if skill.WorkspaceID != m.WorkspaceID || skill.DeviceID != m.workspaceDevice(m.WorkspaceID) || skill.HarnessID != m.HarnessID() {
			return nil, errors.New("Skill 属于其他工作区或 Agent，请移除后重新选择")
		}
		out = append(out, skill.Reference)
	}
	return out, nil
}
func (m *Model) clearAcceptedSkills(key string, in domain.Submission) {
	var kept []DraftSkill
	for _, skill := range m.DraftSkills[key] {
		found := false
		for _, a := range in.Skills {
			if a == skill.Reference && skill.WorkspaceID == in.WorkspaceID && skill.DeviceID == m.workspaceDevice(in.WorkspaceID) && skill.HarnessID == in.HarnessID {
				found = true
				break
			}
		}
		if !found {
			kept = append(kept, skill)
		}
	}
	m.DraftSkills[key] = kept
}
func (m *Model) HasDraftContent(key string) bool {
	return len(m.DraftSkills[key]) > 0 || len(m.Attachments[key]) > 0 || strings.TrimSpace(m.Drafts[key]) != ""
}

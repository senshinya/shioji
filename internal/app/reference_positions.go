package app

import "shioji/internal/domain"

func (m *Model) DraftReferencePositions(key string) []domain.ReferencePosition {
	return draftReferencePositions(m.Attachments[key], m.DraftSkills[key])
}

func (d QueueEditDraft) ReferencePositions() []domain.ReferencePosition {
	return draftReferencePositions(d.Attachments, d.Skills)
}

func draftReferencePositions(files []DraftAttachment, skills []DraftSkill) []domain.ReferencePosition {
	var positions []domain.ReferencePosition
	for _, file := range files {
		if file.Attachment.InlineReference() && file.InlinePosition > 0 {
			positions = append(positions, domain.ReferencePosition{Position: file.InlinePosition, AttachmentID: file.Attachment.ID})
		}
	}
	for _, skill := range skills {
		if skill.InlinePosition > 0 {
			positions = append(positions, domain.ReferencePosition{Position: skill.InlinePosition, SkillID: skill.Reference.ID})
		}
	}
	return positions
}
func (m *Model) restoreReferencePositions(key string, positions []domain.ReferencePosition) {
	for _, position := range positions {
		for i, file := range m.Attachments[key] {
			if position.AttachmentID != "" && file.Attachment.ID == position.AttachmentID {
				m.Attachments[key][i].InlinePosition = position.Position
			}
		}
		for i, skill := range m.DraftSkills[key] {
			if position.SkillID != "" && skill.Reference.ID == position.SkillID {
				m.DraftSkills[key][i].InlinePosition = position.Position
			}
		}
	}
}

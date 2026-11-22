package mobileclient

import (
	"slices"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func QueueEditRecorded(queue domain.QueueState, edit app.QueueEdit) bool {
	if edit.Submitted == nil || queue.Revision <= edit.Revision {
		return false
	}
	draft := edit.Submitted
	for _, item := range queue.Items {
		if item.ID != edit.ID {
			continue
		}
		in := item.Input
		return in.Text == draft.Text &&
			slices.EqualFunc(in.Attachments, draft.Attachments, func(sent domain.Attachment, saved app.DraftAttachment) bool {
				return sent == saved.Attachment
			}) && slices.EqualFunc(in.Skills, draft.Skills, func(sent domain.SkillReference, saved app.DraftSkill) bool {
			return sent == saved.Reference
		}) && slices.Equal(in.ReferencePositions, draft.ReferencePositions())
	}
	return false
}

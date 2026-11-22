package mobileclient

import (
	"testing"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestQueueEditRecordedRequiresNewRevisionAndMatchingReferences(t *testing.T) {
	ref := domain.Attachment{ID: "path", Kind: domain.PathAttachmentResource}
	edit := app.QueueEdit{ID: "item", Revision: 3, Submitted: &app.QueueEditDraft{
		Text: "review", Attachments: []app.DraftAttachment{{Attachment: ref, InlinePosition: 2}},
	}}
	q := domain.QueueState{Revision: 3, Items: []domain.QueueItem{{ID: "item", Input: domain.Submission{
		Text: "review", Attachments: []domain.Attachment{ref},
		ReferencePositions: []domain.ReferencePosition{{AttachmentID: "path", Position: 2}},
	}}}}
	if QueueEditRecorded(q, edit) {
		t.Fatal("an unchanged revision cannot settle the saved edit")
	}
	q.Revision++
	if !QueueEditRecorded(q, edit) {
		t.Fatal("the authoritative queue contains the saved edit")
	}
	q.Items[0].Input.ReferencePositions[0].Position++
	if QueueEditRecorded(q, edit) {
		t.Fatal("a different inline position cannot settle the saved edit")
	}
}

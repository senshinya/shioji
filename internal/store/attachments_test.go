package store_test

import (
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestImageOnlyMessageAndQueueKeepImmutableAttachments(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	a := domain.Attachment{ID: domain.NewID(), Name: "screen.png", MIME: "image/png", Size: 23, SHA256: strings.Repeat("a", 64)}
	in := domain.Submission{OperationID: "image-only", WorkspaceID: w.ID, Attachments: []domain.Attachment{a}}
	op, err := s.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.Session(op.SessionID, 0, 100)
	if len(d.Items) != 1 || len(d.Items[0].Attachments) != 1 || d.Session.Title != "screen.png" {
		t.Fatal("image-only history lost metadata", d)
	}
	in.OperationID = "queued-image"
	in.SessionID = op.SessionID
	if _, err = s.Enqueue(in); err != nil {
		t.Fatal(err)
	}
	q, _ := s.Queue(op.SessionID)
	if len(q.Items[0].Input.Attachments) != 1 {
		t.Fatal("queued attachment missing")
	}
	if _, err = s.EditQueue(op.SessionID, q.Items[0].ID, q.Revision, "", []domain.Attachment{a}); err != nil {
		t.Fatal("image-only queue edit failed", err)
	}
}

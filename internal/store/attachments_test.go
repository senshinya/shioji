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

func TestLegacyQueueEditRetainsSkillUnlessExplicitlyRemoved(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	first, err := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetOperation(first.ID, "confirmed", "")
	s.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "running"}})
	ref := domain.SkillReference{ID: "opaque", Name: "review"}
	_, err = s.Enqueue(domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: first.SessionID, Text: "queued", Skills: []domain.SkillReference{ref}})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := s.Queue(first.SessionID)
	q, err = s.EditQueueContent(first.SessionID, q.Items[0].ID, q.Revision, "legacy edit", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Items[0].Input.Skills) != 1 {
		t.Fatal("missing old-client field removed Skill")
	}
	q, err = s.EditQueueContent(first.SessionID, q.Items[0].ID, q.Revision, "explicit removal", nil, []domain.SkillReference{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Items[0].Input.Skills) != 0 {
		t.Fatal("explicit empty list did not remove Skill")
	}
}

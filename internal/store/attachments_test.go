package store_test

import (
	"encoding/json"
	"fmt"
	"reflect"
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

func TestAcceptedMessageRetainsInlineReferencePositions(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	a := domain.Attachment{ID: domain.NewID(), Name: "main.go", SourcePath: "/project/main.go", MIME: "text/plain", Size: 12, SHA256: strings.Repeat("a", 64)}
	in := domain.Submission{OperationID: "inline", WorkspaceID: w.ID, Text: "请用 检查 ", Attachments: []domain.Attachment{a}, Skills: []domain.SkillReference{{ID: "review", Name: "check"}}, ReferencePositions: []domain.ReferencePosition{{SkillID: "review", Position: 3}, {AttachmentID: a.ID, Position: 8}}}
	op, err := s.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(d.Items[0])
	if !strings.Contains(string(body), `"referencePositions"`) {
		t.Fatal("accepted message dropped inline positions", string(body))
	}
	var stored struct {
		ReferencePositions []domain.ReferencePosition `json:"referencePositions"`
	}
	json.Unmarshal(body, &stored)
	if !reflect.DeepEqual(stored.ReferencePositions, in.ReferencePositions) {
		t.Fatal("message changed inline positions", stored)
	}
}

func TestHistoryRestartCanReplaceOrClearReferences(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprint(clear), func(t *testing.T) {
			s := open(t)
			w := workspace(t, s)
			first, err := s.Accept(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "old", Skills: []domain.SkillReference{{ID: "old", Name: "old"}}})
			if err != nil {
				t.Fatal(err)
			}
			s.SetOperation(first.ID, "confirmed", "")
			s.Apply(first.SessionID, domain.Event{Type: "session", Session: &domain.Session{ID: first.SessionID, WorkspaceID: w.ID, ThreadID: "thread", Status: "completed"}})
			s.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
			s.Apply(first.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:original", Kind: "user", Text: "old", Skills: []domain.SkillReference{{ID: "old", Name: "old"}}, TurnID: "t", Status: "sent", Final: true}})
			d, _ := s.Session(first.SessionID, 0, 100)
			references := `{"skills":[{"id":"new","name":"check"}],"referencePositions":[{"position":1,"skillId":"new"}]}`
			if clear {
				references = `{"skills":[],"attachments":[],"referencePositions":[]}`
			}
			body := fmt.Sprintf(`{"operationId":"restart","sessionId":%q,"revision":%d,"itemId":"input:original","restart":true,"text":"new","references":%s}`, first.SessionID, d.Session.Revision, references)
			var change domain.HistoryChange
			json.Unmarshal([]byte(body), &change)
			op, err := s.PrepareHistory(change)
			if err != nil {
				t.Fatal(err)
			}
			if clear {
				if len(op.Submission.Skills) != 0 {
					t.Fatal("explicit removal retained old Skill", op.Submission.Skills)
				}
			} else if len(op.Submission.Skills) != 1 || op.Submission.Skills[0].ID != "new" || len(op.Submission.ReferencePositions) != 1 {
				t.Fatal("history edit did not replace references", op.Submission)
			}
		})
	}
}

package app

import (
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func queueModel() *Model {
	m := model()
	m.WorkerMinor = 2
	m.SessionID = "s"
	s := domain.Session{ID: "s", WorkspaceID: "w", Status: "running"}
	m.Snapshot.Sessions = []domain.Session{s}
	m.Details["s"] = domain.SessionDetail{Session: s, Turns: []domain.Turn{{ID: "active", Status: "running"}}, Queue: domain.QueueState{SessionID: "s", Revision: 2, Items: []domain.QueueItem{{ID: "q", Input: domain.Submission{Text: "queued", Config: domain.RunConfig{Model: "snapshot"}}}}}}
	return m
}
func TestRunningInputQueuesOrSteersWithoutChangingQuestions(t *testing.T) {
	m := queueModel()
	m.Drafts[m.DraftKey()] = "next"
	m.Answers["s/request/q"] = "answer"
	in, method, turn, err := m.BeginMessage(false)
	if err != nil || method != "turn.enqueue" || turn != "" {
		t.Fatal(in, method, turn, err)
	}
	m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	if m.Drafts[m.DraftKey()] != "" || m.Answers["s/request/q"] != "answer" {
		t.Fatal("queue cleared unrelated state")
	}
	m.Drafts[m.DraftKey()] = "steer"
	_, method, turn, err = m.BeginMessage(true)
	if err != nil || method != "turn.steer" || turn != "active" {
		t.Fatal(method, turn, err)
	}
}
func TestQueueEditRestoresDisplacedDraftAcrossRestart(t *testing.T) {
	m := queueModel()
	key := m.DraftKey()
	m.Drafts[key] = "ordinary"
	if err := m.EditQueued("q"); err != nil {
		t.Fatal(err)
	}
	if m.Drafts[key] != "queued" {
		t.Fatal(m.Drafts)
	}
	m.Drafts[key] = "edited"
	m.QueueExpanded["s"] = true
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	r := NewModel()
	if err := r.Load(path); err != nil {
		t.Fatal(err)
	}
	r.SessionID = "s"
	if r.Drafts[key] != "edited" || !r.QueueExpanded["s"] {
		t.Fatal("edit state lost")
	}
	r.CancelQueueEdit("s")
	if r.Drafts[key] != "ordinary" {
		t.Fatal("displaced draft lost")
	}
}
func TestQueueEditAckDoesNotEraseNewerEditsOrOtherSession(t *testing.T) {
	m := queueModel()
	m.Drafts[m.DraftKey()] = "ordinary"
	m.EditQueued("q")
	m.Drafts[m.DraftKey()] = "newer"
	q := m.Queue()
	q.Revision++
	m.FinishQueueEdit("s", "q", "queued", q)
	if m.Drafts[m.DraftKey()] != "newer" || m.QueueEdits["s"].ID != "q" {
		t.Fatal("late ACK erased newer edit")
	}
	m.SessionID = "other"
	m.Drafts[m.DraftKey()] = "other"
	m.FinishQueueEdit("s", "q", "newer", q)
	if m.Drafts["session:s"] != "ordinary" || m.Drafts[m.DraftKey()] != "other" {
		t.Fatal("cross-session draft loss")
	}
}

func TestConfirmedQueuedInputInsertsBeforeAlreadyArrivedReply(t *testing.T) {
	m := queueModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "old", Order: 1, Kind: "user"}, {ID: "reply", Order: 3, Kind: "assistant"}}
	m.Details["s"] = d
	if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, SessionID: "s", Type: "item.upsert", Item: &domain.Item{ID: "queued-input", Order: 2, Kind: "user"}}); err != nil {
		t.Fatal(err)
	}
	got := m.Details["s"].Items
	if got[1].ID != "queued-input" {
		t.Fatal("live input arrived after its reply", got)
	}
}

func TestQueuedEditWaitsForOutstandingDraftAcknowledgement(t *testing.T) {
	m := queueModel()
	key := m.DraftKey()
	m.Drafts[key] = "ordinary"
	in, _, _, err := m.BeginMessage(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.EditQueued("q"); err == nil {
		t.Fatal("editing displaced an unconfirmed draft")
	}
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	if err = m.EditQueued("q"); err != nil {
		t.Fatal(err)
	}
	m.CancelQueueEdit("s")
	if m.Drafts[key] != "" {
		t.Fatal("confirmed ordinary input was restored as a draft")
	}
}

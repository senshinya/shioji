package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestHistoryResetRemovesSuffixWithoutTouchingOtherSessionOrDrafts(t *testing.T) {
	m := NewModel()
	m.Drafts["session:s"] = "ordinary draft"
	m.Details["other"] = domain.SessionDetail{Items: []domain.Item{{ID: "other", Text: "keep"}}}
	m.Details["s"] = domain.SessionDetail{Items: []domain.Item{{ID: "one", TurnID: "t1", Order: 1}, {ID: "two", TurnID: "t2", Order: 2}}, Turns: []domain.Turn{{ID: "t1", Order: 1}, {ID: "t2", Order: 2}}, Requests: []domain.PendingRequest{{ID: "r1", TurnID: "t1"}, {ID: "r2", TurnID: "t2"}}, Plan: &domain.Plan{TurnID: "t2"}}
	keep := int64(1)
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep}); err != nil {
		t.Fatal(err)
	}
	d := m.Details["s"]
	if len(d.Items) != 1 || len(d.Turns) != 1 || len(d.Requests) != 1 || d.Plan != nil || m.Drafts["session:s"] != "ordinary draft" || len(m.Details["other"].Items) != 1 {
		t.Fatal("history replacement leaked or retained suffix", d)
	}
}

func TestHistoricalEditDraftPersistsWithoutReplacingOrdinaryDraft(t *testing.T) {
	m := NewModel()
	m.SessionID = "s"
	m.WorkspaceID = "w"
	m.Drafts["session:s"] = "ordinary"
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s", Revision: 3}}
	item := domain.Item{ID: "message", Kind: "user", Status: "sent", TurnID: "turn", Text: "original"}
	if err := m.BeginHistoryEdit(item); err != nil {
		t.Fatal(err)
	}
	if m.DraftKey() != "session:s" {
		t.Fatal("historical editing replaced the ordinary composer", m.DraftKey())
	}
	m.Drafts["history:s:message"] = "edited"
	data, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	// Load keeps drafts, including a separately recoverable historical edit.
	path := t.TempDir() + "/state.json"
	if err = WriteState(path, data); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err = restored.Load(path); err != nil {
		t.Fatal(err)
	}
	restored.SessionID = "s"
	if restored.Drafts["history:s:message"] != "edited" || restored.Drafts[restored.DraftKey()] != "ordinary" {
		t.Fatal("drafts lost at restart")
	}
	restored.CancelHistoryEdit()
	if restored.DraftKey() != "session:s" {
		t.Fatal("cancel failed")
	}
}

func TestHistoryResetRetainsEarlierTurnLateOutput(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Items: []domain.Item{{ID: "first", TurnID: "t1", Order: 1}, {ID: "second", TurnID: "t2", Order: 2}, {ID: "late", TurnID: "t1", Order: 3}}, Turns: []domain.Turn{{ID: "t1", Order: 1}, {ID: "t2", Order: 2}}}
	keep := int64(1)
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep}); err != nil {
		t.Fatal(err)
	}
	d := m.Details["s"]
	if len(d.Items) != 2 || d.Items[1].ID != "late" {
		t.Fatal("earlier turn's late output removed", d.Items)
	}
}

func TestHistoryConfirmationRestoresOrdinaryDraftWithoutOrdinaryPendingSubmission(t *testing.T) {
	m := NewModel()
	m.SessionID = "s"
	m.Drafts["session:s"] = "ordinary draft"
	m.Drafts["history:s:old"] = "edited"
	m.HistoryEdits["s"] = HistoryEdit{ItemID: "old", OperationID: "restart", SubmittedText: "edited"}
	m.ApplyOperation(domain.Operation{ID: "restart", SessionID: "s", State: "confirmed"})
	if len(m.HistoryEdits) != 0 || m.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("confirmed history action did not restore independent draft", m.HistoryEdits, m.Drafts)
	}
}

func TestRejectedRestartReleasesHistoryEditingWithoutLosingEitherDraft(t *testing.T) {
	m := NewModel()
	m.SessionID = "s"
	m.Drafts["session:s"] = "ordinary"
	m.Drafts["history:s:old"] = "revised"
	m.HistoryEdits["s"] = HistoryEdit{ItemID: "old", OperationID: "restart", SubmittedText: "revised"}
	m.HistoryActions["s"] = domain.HistoryChange{OperationID: "restart", SessionID: "s", Restart: true}
	m.ApplyOperation(domain.Operation{ID: "restart", SessionID: "s", State: "rejected", Error: "turn/start refused"})
	if len(m.HistoryEdits) != 0 || len(m.HistoryActions) != 0 || m.Drafts["session:s"] != "ordinary" || m.Drafts["history:s:old"] != "revised" || m.Error != "turn/start refused" {
		t.Fatal("rejected restart stranded editing or lost a draft", m.HistoryEdits, m.HistoryActions, m.Drafts, m.Error)
	}
}

func TestClosingSubmittedHistoryEditKeepsItsRecoveryDraft(t *testing.T) {
	m := NewModel()
	m.SessionID = "s"
	m.HistoryEdits["s"] = HistoryEdit{ItemID: "old", OperationID: "restart"}
	m.Drafts["history:s:old"] = "submitted revision"
	m.HistoryActions["s"] = domain.HistoryChange{OperationID: "restart", SessionID: "s", Restart: true}
	m.CancelHistoryEdit()
	if m.Drafts["history:s:old"] != "submitted revision" || m.HistoryActions["s"].OperationID != "restart" {
		t.Fatal("closing a submitted edit discarded its recovery state")
	}
}

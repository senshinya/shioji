package store_test

import (
	"testing"

	"shioji/internal/domain"
)

func TestManagedGoalRequiresEvidenceFromItsOwnCompletedTurn(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	input, err := s.Accept(domain.Submission{OperationID: "objective", WorkspaceID: w.ID, Text: "/goal finish"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(input.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	d.Session.ThreadID = "native"
	if _, err := s.Apply(input.SessionID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginGoalInputHandoff(input.ID, "native", "finish", nil); err != nil {
		t.Fatal(err)
	}
	stale := domain.GoalProgress{GoalID: input.ID, RunID: "wrong-input", Outcome: domain.GoalComplete}
	if _, err := s.Apply(input.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "first", Status: domain.ExecutionCompleted}, GoalProgress: &stale}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(input.SessionID, 0, 1)
	if !d.Session.Goal.Active() {
		t.Fatalf("unrelated turn changed the goal: %+v", d.Session.Goal)
	}
	report := domain.GoalProgress{GoalID: input.ID, RunID: input.ID}
	if _, err := s.Apply(input.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "second", Status: domain.ExecutionCompleted}, GoalProgress: &report}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(input.SessionID, 0, 1)
	if d.Session.Goal == nil || d.Session.Goal.State != domain.GoalBlocked {
		t.Fatalf("native completion without matching evidence = %+v", d.Session.Goal)
	}
	resume := domain.GoalRequest{OperationID: "resume", SessionID: input.SessionID, Action: domain.ResumeGoal, Target: *d.Session.Goal}
	if _, err := s.ChangeManagedGoal(resume); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(input.SessionID, 0, 1)
	if !d.Session.Goal.Active() || d.Session.Goal.ContinueFrom != resume.OperationID {
		t.Fatalf("resume was not durably scheduled: %+v", d.Session.Goal)
	}
	if err := s.SetOperation(input.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	next := domain.Submission{OperationID: d.Session.Goal.ContinuationID(), SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "continue"}
	if _, err := s.AcceptManagedGoalContinuation(next, d.Session.Goal, d.Session.Context); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(input.SessionID, 0, 1)
	goalAtPreparation := *d.Session.Goal
	if _, err := s.ChangeManagedGoal(domain.GoalRequest{OperationID: "pause", SessionID: d.Session.ID, Action: domain.PauseGoal, Target: goalAtPreparation}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginGoalInputHandoff(next.OperationID, "native", "", &goalAtPreparation); err == nil {
		t.Fatal("paused continuation was allowed to hand off")
	}
}

func TestManagedGoalRecoveryDoesNotReplayAnUnconfirmedStep(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "uncertain-goal", WorkspaceID: w.ID, Text: "/goal verify"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	d.Session.ThreadID = "native"
	if _, err := s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginGoalInputHandoff(op.ID, "native", "verify", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	d, err = s.Session(op.SessionID, 0, 1)
	if err != nil || d.Session.Goal == nil || d.Session.Goal.State != domain.GoalBlocked || d.Session.Goal.ContinueFrom != "" {
		t.Fatalf("goal remained active after losing the execution owner: %+v, %v", d.Session.Goal, err)
	}
}

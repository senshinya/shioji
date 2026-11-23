package worker

import (
	"os"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/antigravity"
)

// The executable must launch the installed agy with an isolated, seeded
// profile and a loopback model provider. No user account is used by this test.
func TestAntigravityWorkerWithIsolatedCLI(t *testing.T) {
	executable := os.Getenv("SHIOJI_TEST_AGY_WRAPPER")
	if executable == "" {
		t.Skip("requires isolated agy fixture")
	}
	a := antigravity.Adapter{Executable: executable}
	s, w := memoryWorker(t, a)
	if got := s.runtimeReadiness(); got.State == domain.RuntimeReady {
		t.Fatalf("background catalog created discovery state: %+v", got)
	}
	ready, err := s.recheckRuntime(a.Descriptor().ID)
	if err != nil || ready.State != domain.RuntimeReady || !ready.Capabilities.WorkerGoals {
		t.Fatalf("explicit ACP discovery: %+v, %v", ready, err)
	}
	var cfg domain.RunConfig
	for _, model := range ready.Models {
		if model.Default {
			cfg = domain.RunConfig{Model: model.ID, Effort: model.DefaultEffort, Permission: "default"}
		}
	}
	send := func(id, session, text, objective string) domain.Operation {
		t.Helper()
		operation, err := s.submit(domain.Submission{
			OperationID: id, WorkspaceID: w.ID, SessionID: session, Text: text,
			GoalObjective: objective, Config: cfg, HarnessID: a.Descriptor().ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return operation
	}
	waitDetail := func(id string, condition func(domain.SessionDetail) bool) domain.SessionDetail {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		var d domain.SessionDetail
		for time.Now().Before(deadline) {
			d, _ = s.store.Session(id, 0, 40)
			if condition(d) {
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("session %s did not converge: %+v", id, d)
		return d
	}
	completed := func(d domain.SessionDetail) bool {
		return d.Session.Status == domain.ExecutionCompleted && d.Session.ThreadID != ""
	}
	original := send("source", "", "shioji-fork-smoke-marker", "")
	if receipt := awaitReceipt(t, s, original.ID); receipt.State != domain.OperationConfirmed {
		t.Fatalf("source receipt = %+v", receipt)
	}
	source := waitDetail(original.SessionID, completed)
	fork, err := s.changeHistory(domain.HistoryChange{OperationID: "fork", SessionID: source.Session.ID, Revision: source.Session.Revision, TurnID: source.Turns[0].ID})
	if err != nil || fork.Session.ThreadID != "" || fork.Session.ID == source.Session.ID {
		t.Fatalf("portable fork = %+v, %v", fork, err)
	}
	next := send("fork-send", fork.Session.ID, "continue from the fork", "")
	if receipt := awaitReceipt(t, s, next.ID); receipt.State != domain.OperationConfirmed {
		t.Fatalf("fork receipt = %+v", receipt)
	}
	branch := waitDetail(next.SessionID, completed)
	if branch.Session.ThreadID == source.Session.ThreadID || len(branch.Turns) != 2 {
		t.Fatalf("fork bound to the wrong history: %+v", branch)
	}
	goal := send("objective", "", "/goal test the managed goal", "test the managed goal")
	finishGoal := func(d domain.SessionDetail) bool {
		for _, request := range d.Requests {
			if request.State != domain.RequestPending {
				continue
			}
			for _, choice := range request.Choices {
				if strings.EqualFold(choice.ID, "allow_once") || strings.EqualFold(choice.ID, "allow") {
					err := s.respondRequest(AnswerInput{SessionID: d.Session.ID, Answer: domain.Answer{
						RequestID: request.ID, Revision: request.Revision, Decision: choice.ID,
					}})
					if err != nil {
						t.Fatal(err)
					}
					break
				}
			}
		}
		return d.Session.Goal != nil && d.Session.Goal.State == domain.GoalComplete
	}
	finished := waitDetail(goal.SessionID, finishGoal)
	if len(finished.Turns) != 2 || !finished.Session.Goal.Managed() {
		t.Fatalf("goal continuation: %+v", finished)
	}
	changeGoal := func(id string, d domain.SessionDetail, action domain.GoalAction) {
		t.Helper()
		op, err := s.goalControl(domain.GoalRequest{OperationID: id, SessionID: d.Session.ID, Action: action, Target: *d.Session.Goal})
		if err != nil || op.State != domain.OperationConfirmed {
			t.Fatalf("%s goal = %+v, %v", action, op, err)
		}
	}
	changeGoal("clear", finished, domain.ClearGoal)
	waitDetail(goal.SessionID, func(d domain.SessionDetail) bool { return d.Session.Goal == nil })
	pausedInput := send("second-objective", "", "/goal pause during a pending tool", "pause during a pending tool")
	running := waitDetail(pausedInput.SessionID, func(d domain.SessionDetail) bool {
		for _, request := range d.Requests {
			if request.State == domain.RequestPending && d.Session.Goal.Active() {
				return true
			}
		}
		return false
	})
	changeGoal("pause", running, domain.PauseGoal)
	paused := waitDetail(pausedInput.SessionID, func(d domain.SessionDetail) bool {
		return d.Session.Goal != nil && d.Session.Goal.State == domain.GoalPaused && d.Session.Status == domain.ExecutionInterrupted
	})
	changeGoal("resume", paused, domain.ResumeGoal)
	waitDetail(pausedInput.SessionID, finishGoal)
}

package worker_test

import (
	"reflect"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestWorkerGoalStartsOwnedTurnAndControlsSameObjective(t *testing.T) {
	_, client, provider := startServer(t)
	var workspace domain.Workspace
	call(t, client, "workspace.create", map[string]any{"name": "Goal", "folders": []string{t.TempDir()}}, &workspace)
	var ready domain.Readiness
	call(t, client, "readiness", nil, &ready)
	if !ready.Capabilities.Goals {
		t.Fatal("the isolated Codex process did not advertise enabled goals")
	}
	const objective = "SHIOJI_SLOW verify the owning workspace"
	input := domain.Submission{OperationID: "initial-goal", WorkspaceID: workspace.ID, Text: "/goal " + objective, GoalObjective: objective, Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}}
	var accepted domain.Operation
	call(t, client, "session.submit", input, &accepted)
	select {
	case <-provider.Started:
	case <-time.After(10 * time.Second):
		var state domain.SessionDetail
		call(t, client, "session", map[string]string{"id": accepted.SessionID}, &state)
		var receipt domain.Operation
		call(t, client, "operation", map[string]string{"id": accepted.ID}, &receipt)
		t.Fatalf("native goal did not reach the fixture: status=%s session error=%q goal=%+v receipt=%+v provider calls=%d", state.Session.Status, state.Session.Error, state.Session.Goal, receipt, len(provider.Inputs()))
	}
	var d domain.SessionDetail
	read := func() {
		d = domain.SessionDetail{}
		call(t, client, "session", map[string]string{"id": accepted.SessionID}, &d)
	}
	wait(t, func() bool { read(); return d.Session.Goal.Active() && d.Session.Status.Active() })
	before := d.Session
	if before.Goal.Objective != objective {
		t.Fatal("missing source-owned objective", before.Goal)
	}
	var receipt domain.Operation
	pause := domain.GoalRequest{OperationID: "pause-goal", SessionID: before.ID, Target: *before.Goal, Action: domain.PauseGoal}
	call(t, client, "session.goal.control", pause, &receipt)
	if receipt.State != domain.OperationConfirmed {
		t.Fatal("pause was not confirmed", receipt)
	}
	wait(t, func() bool { read(); return d.Session.Goal != nil && d.Session.Goal.State == domain.GoalPaused })
	call(t, client, "session.goal.control", pause, &receipt)
	if receipt.State != domain.OperationConfirmed || d.Session.ThreadID != before.ThreadID || !reflect.DeepEqual(d.Session.Config, before.Config) {
		t.Fatal("pause replay changed the runtime identity", receipt, d.Session)
	}
	stop := func() {
		for _, turn := range d.Turns {
			if turn.Status.Active() {
				call(t, client, "turn.interrupt", map[string]string{"sessionId": before.ID, "turnId": turn.ID}, nil)
				return
			}
		}
		t.Fatal("no native turn to stop")
	}
	stop()
	wait(t, func() bool { read(); return d.Session.Status.CanSubmit() })
	resume := domain.GoalRequest{OperationID: "resume-goal", SessionID: before.ID, Target: *d.Session.Goal, Action: domain.ResumeGoal}
	call(t, client, "session.goal.control", resume, &receipt)
	if receipt.State != domain.OperationConfirmed {
		t.Fatal("resume was not confirmed", receipt)
	}
	select {
	case <-provider.Started:
	case <-time.After(8 * time.Second):
		t.Fatal("resume did not start another native turn")
	}
	wait(t, func() bool { read(); return d.Session.Goal.Active() && d.Session.Status.Active() })
	stop()
	wait(t, func() bool {
		read()
		return d.Session.Goal != nil && d.Session.Goal.State == domain.GoalPaused && d.Session.Status.CanSubmit()
	})

	clear := domain.GoalRequest{OperationID: "clear-goal", SessionID: before.ID, Target: *d.Session.Goal, Action: domain.ClearGoal}
	call(t, client, "session.goal.control", clear, &receipt)
	if receipt.State != domain.OperationConfirmed {
		t.Fatal("clear was not confirmed", receipt)
	}
	wait(t, func() bool { read(); return d.Session.Goal == nil })
	close(provider.Release)
}

func TestWorkerPersistsNativeGoalContinuationBetweenReplies(t *testing.T) {
	_, client, provider := startServer(t)
	var workspace domain.Workspace
	call(t, client, "workspace.create", map[string]any{"name": "Goal", "folders": []string{t.TempDir()}}, &workspace)
	const objective = "SHIOJI_SLOW verify a goal continuation"
	var accepted domain.Operation
	call(t, client, "session.submit", domain.Submission{
		OperationID: "goal", WorkspaceID: workspace.ID, Text: "/goal " + objective, GoalObjective: objective,
		Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"},
	}, &accepted)
	awaitModel := func() {
		t.Helper()
		select {
		case <-provider.Started:
		case <-time.After(10 * time.Second):
			t.Fatal("the next Codex turn did not reach the model")
		}
	}
	read := func() domain.SessionDetail {
		t.Helper()
		var detail domain.SessionDetail
		call(t, client, "session", map[string]string{"id": accepted.SessionID}, &detail)
		return detail
	}
	releaseModel := func() {
		t.Helper()
		select {
		case provider.Release <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("no active model request to release")
		}
	}
	awaitModel()
	releaseModel()
	awaitModel()
	detail := read()
	if !detail.Session.Goal.Active() {
		t.Fatal("the goal was lost before the continuation")
	}
	call(t, client, "session.goal.control", domain.GoalRequest{
		OperationID: "pause-goal", SessionID: accepted.SessionID, Target: *detail.Session.Goal, Action: domain.PauseGoal,
	}, nil)
	releaseModel()
	wait(t, func() bool { return read().Session.Status == domain.ExecutionCompleted })
	detail = read()
	if len(detail.Items) != 4 || detail.Items[0].Kind != "user" || detail.Items[1].Kind != "assistant" ||
		detail.Items[2].Presentation != "goal-continuation" || detail.Items[3].Kind != "assistant" ||
		detail.Items[2].TurnID != detail.Items[3].TurnID || detail.Items[1].TurnID == detail.Items[2].TurnID {
		t.Fatalf("the worker history should be input, first reply, continuation marker, and next reply: %+v", detail.Items)
	}
}

package app

import (
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestGoalCommandKeepsDraftOnUnsupportedWorkerAndQueuesWithoutSteering(t *testing.T) {
	m := model()
	m.Readiness.Capabilities.Goals = true
	m.Drafts["home"] = "/goal 验证连接"
	m.WorkerMinor = protocol.GoalMinor - 1
	if _, err := m.BeginSubmission(); err == nil || m.Drafts["home"] != "/goal 验证连接" || len(m.Pending) != 0 {
		t.Fatal("an older worker accepted typed Goal or lost its draft")
	}
	m.WorkerMinor = protocol.GoalMinor
	in, err := m.BeginSubmission()
	if err != nil || in.GoalObjective != "验证连接" || in.SessionID != "" || in.Text != m.Drafts["home"] {
		t.Fatalf("home Goal was not translated into a typed submission: %+v %v", in, err)
	}
	m.SessionID = "session"
	m.Snapshot.Sessions = []domain.Session{{
		ID: "session", WorkspaceID: "w", Status: "completed", Config: in.Config,
		Goal: &domain.Goal{State: domain.GoalActive, Objective: "previous"},
	}}
	m.Drafts[m.DraftKey()] = "/goal 新目标"
	in, method, turn, err := m.BeginMessage(true)
	if err != nil || method != "turn.enqueue" || turn != "" || in.GoalObjective != "新目标" {
		t.Fatalf("an active Goal's inter-turn gap bypassed its queue: %s %q %+v %v", method, turn, in, err)
	}
}

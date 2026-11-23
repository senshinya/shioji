package worker

import (
	"context"
	"encoding/json"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

type livePermissionHarness struct{ memoryHarness }

func (a livePermissionHarness) Start(_ context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	return &livePermissionRuntime{memoryRuntime: &memoryRuntime{adapter: a.memoryHarness, options: opts, done: make(chan struct{})}}, nil
}

type livePermissionRuntime struct{ *memoryRuntime }

func (r *livePermissionRuntime) Readiness() domain.Readiness {
	ready := r.memoryRuntime.Readiness()
	ready.Permissions = append(ready.Permissions, "automatic")
	ready.PermissionChoices = []domain.OptionChoice{{ID: "limited"}, {ID: "automatic", Live: true}}
	ready.Harnesses[0].Permissions = ready.Permissions
	ready.Harnesses[0].PermissionChoices = ready.PermissionChoices
	return ready
}

func (r *livePermissionRuntime) PermissionRequiresRestart(domain.RunConfig) bool { return false }

func (r *livePermissionRuntime) ChangePermission(_ context.Context, s domain.Session, _ string, accept func() error) error {
	if err := accept(); err != nil {
		return err
	}
	return r.options.OnEvent(domain.Event{Type: "item.upsert", SessionID: s.ID, Item: &domain.Item{ID: "permission-accepted", Kind: "tool"}})
}

func TestLivePermissionSelectionAppliesToTheRunningSession(t *testing.T) {
	s, w := memoryWorker(t, livePermissionHarness{memoryHarness{id: "permission", hold: true}})
	config := domain.RunConfig{Model: "permission-model", Permission: "limited"}
	op, err := s.submit(domain.Submission{OperationID: "start", WorkspaceID: w.ID, Config: config, Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if got := awaitReceipt(t, s, op.ID); got.State != domain.OperationConfirmed {
		t.Fatal(got)
	}
	runtime := s.currentRuntime(op.SessionID).(*livePermissionRuntime)
	if runtime.options.InitialConfig == nil || *runtime.options.InitialConfig != config {
		t.Errorf("new runtime did not receive its accepted initial config: %+v", runtime.options.InitialConfig)
	}
	session, err := s.store.SessionRecord(op.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.SessionPermissionInput{SessionID: session.ID, ThreadID: session.ThreadID, Previous: "limited", Permission: "automatic"}
	change := func(in protocol.SessionPermissionInput) error {
		body, _ := json.Marshal(in)
		_, err := s.handle("session.permission", body)
		return err
	}
	other := in
	other.ThreadID = "another-thread"
	if err := change(other); err == nil {
		t.Fatal("a stale conversation changed permission")
	}
	if err := change(in); err != nil {
		t.Fatal(err)
	}
	detail, err := s.store.Session(session.ID, 0, 20)
	if err != nil || detail.Session.Config.Permission != "automatic" || detail.Session.Config.Model != config.Model ||
		detail.Session.Status != domain.ExecutionRunning || s.currentRuntime(session.ID) != runtime {
		t.Fatalf("live permission changed the session or was not persisted: %+v error=%v", detail.Session, err)
	}
	for _, item := range detail.Items {
		if item.ID == "permission-accepted" {
			return
		}
	}
	t.Fatal("the running adapter did not apply the permission")
}

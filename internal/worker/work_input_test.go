package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"testing"
)

type workMessageRuntime struct {
	harness.Runtime
	sent     []domain.WorkInput
	failure  error
	received bool
}

func (r *workMessageRuntime) SendWork(_ context.Context, _ domain.Session, in domain.WorkInput) error {
	r.sent = append(r.sent, in)
	return r.failure
}
func (r *workMessageRuntime) WorkInputReceived(context.Context, domain.Session, domain.WorkInput) (bool, error) {
	return r.received, nil
}
func TestWorkerChildSendIsOwnedAndExactlyOnce(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	op, err := s.submit(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "start", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, "root")
	d, _ := s.store.Session(op.SessionID, 0, 20)
	child := domain.WorkActivity{ID: "child", Reference: "opaque-child", OriginRef: d.Session.ThreadID, Kind: "agent", TurnID: "turn/root", State: "running", CanSteer: true}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &child}); err != nil {
		t.Fatal(err)
	}
	r := &workMessageRuntime{Runtime: s.currentRuntime(op.SessionID)}
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = r
	s.runtimeMu.Unlock()
	call := func(method string, in domain.WorkInput) (domain.WorkInputOperation, error) {
		b, _ := json.Marshal(in)
		x, e := s.handle(method, b)
		if e != nil {
			return domain.WorkInputOperation{}, e
		}
		return x.(domain.WorkInputOperation), nil
	}
	in := domain.WorkInput{OperationID: "directed", SessionID: op.SessionID, WorkID: "child", Mode: "steer", Text: "child only"}
	got, err := call("session.work.send", in)
	if err != nil || got.State != "confirmed" || len(r.sent) != 1 {
		t.Fatal("directed input not accepted", got, err, r.sent)
	}
	got, err = call("session.work.send", in)
	if err != nil || got.State != "confirmed" || len(r.sent) != 1 {
		t.Fatal("replayed input", got, err, r.sent)
	}
	in.OperationID = "wrong-mode"
	in.Mode = "continue"
	if _, err = call("session.work.send", in); err == nil || len(r.sent) != 1 {
		t.Fatal("changed steer into a new task", err)
	}
	in.Mode = "steer"
	in.OperationID = "unknown"
	r.failure = &harness.DeliveryError{Uncertain: true, Message: "lost receipt"}
	got, err = call("session.work.send", in)
	if err != nil || got.State != "indeterminate" || len(r.sent) != 2 {
		t.Fatal(got, err)
	}
	got, err = call("session.work.operation", in)
	if err != nil || got.State != "indeterminate" || len(r.sent) != 2 {
		t.Fatal("receipt check resent input", got, err)
	}
	r.received = true
	got, err = call("session.work.operation", in)
	if err != nil || got.State != "confirmed" || len(r.sent) != 2 {
		t.Fatal("receipt did not settle", got, err)
	}
	after, _ := s.store.Session(op.SessionID, 0, 20)
	if len(after.Items) != len(d.Items) || after.Session.Status != d.Session.Status {
		t.Fatal("child operation modified parent", after)
	}
	in.OperationID = "stale-source"
	in.Text = "do not send"
	session := after.Session
	session.ThreadID = "replacement"
	s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &session})
	if _, err = call("session.work.send", in); err == nil || len(r.sent) != 2 {
		t.Fatal("sent to replaced source", err)
	}
}

func TestChildContinueRevalidatesActualContextBeforeDispatch(t *testing.T) {
	for _, change := range []string{"unchanged", "branch", "directory"} {
		t.Run(change, func(t *testing.T) {
			s, w, dir := gitTestServer(t)
			sid := gitTestSession(t, s, w, "main", "completed")
			d, _ := s.store.Session(sid, 0, 1)
			d.Session.ThreadID = "source"
			s.store.Apply(sid, domain.Event{Type: "session", Session: &d.Session})
			child := domain.WorkActivity{ID: "child", Reference: "opaque-child", OriginRef: "source", Kind: "agent", TurnID: "t", State: "completed", CanContinue: true}
			s.store.Apply(sid, domain.Event{Type: "work.upsert", Work: &child})
			r := &workMessageRuntime{Runtime: &memoryRuntime{done: make(chan struct{})}}
			s.runtimes = map[string]harness.Runtime{sid: r}
			if change == "branch" {
				gitTestRun(t, dir, "switch", "other")
			} else if change == "directory" {
				moved := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(dir, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, dir); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.sendWork(domain.WorkInput{OperationID: "continue", SessionID: sid, WorkID: "child", Mode: "continue", Text: "continue child"})
			if change == "unchanged" {
				if err != nil || len(r.sent) != 1 {
					t.Fatal("unchanged execution context blocked child continuation", err, r.sent)
				}
				return
			}
			if err == nil || len(r.sent) != 0 {
				t.Fatal("child resumed before checking current execution context", change, err, r.sent)
			}
			if change == "branch" {
				latest, _ := s.store.Session(sid, 0, 1)
				if !latest.Session.ContextChanged {
					t.Fatal("external branch change did not enter existing confirmation flow")
				}
			}
		})
	}
}

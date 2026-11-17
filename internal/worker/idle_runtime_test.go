package worker

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

// A neutral resumable driver exercises real worker admission and persistence.
// Its intentional Close also emits a late loss, as a real process can do.
type idleHarness struct{}

func (idleHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "idle", Name: "Idle test"}
}
func (idleHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, e := (memoryHarness{id: "idle"}).Start(ctx, o)
	if e != nil {
		return nil, e
	}
	return &idleTestRuntime{memoryRuntime: r.(*memoryRuntime)}, nil
}

type idleTestRuntime struct {
	*memoryRuntime
	closing chan struct{}
	release <-chan struct{}
}

func (r *idleTestRuntime) ManagedCoverage() bool { return true }
func (r *idleTestRuntime) Readiness() domain.Readiness {
	d := r.memoryRuntime.Readiness()
	d.Capabilities.Continue = true
	d.Harnesses[0].Capabilities.Continue = true
	return d
}
func (r *idleTestRuntime) Close(ctx context.Context) error {
	if r.closing != nil {
		select {
		case r.closing <- struct{}{}:
		default:
		}
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.options.Scope != nil {
		if e := r.options.OnEvent(domain.Event{SessionID: r.options.Scope.Session, Type: "runtime.lost"}); e != nil {
			return e
		}
	}
	return r.memoryRuntime.Close(ctx)
}
func submitIdleFixture(t *testing.T, s *Server, w domain.Workspace, id, sid string) domain.SessionDetail {
	t.Helper()
	op, e := s.submit(domain.Submission{OperationID: id, WorkspaceID: w.ID, SessionID: sid, Text: id, Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if e != nil {
		t.Fatal(e)
	}
	if receipt := awaitReceipt(t, s, op.ID); receipt.State != "confirmed" {
		t.Fatal(receipt)
	}
	s.dispatchMu.Lock()
	d, e := s.store.Session(op.SessionID, 0, 100)
	s.dispatchMu.Unlock()
	if e != nil || d.Session.Status != "completed" {
		t.Fatal("fixture did not complete", d.Session, e)
	}
	return d
}
func sweepIdle(t *testing.T, s *Server, now time.Time) {
	t.Helper()
	if e := s.reclaimIdleRuntimes(func() time.Time { return now }); e != nil {
		t.Fatal(e)
	}
}

func TestIdleRuntimeRetiresAfterTwentyMinutesAndResumesSameHistory(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	before := submitIdleFixture(t, s, w, "first", "")
	sid := before.Session.ID
	old := s.currentRuntime(sid)
	catalog := s.currentRuntime()
	now := time.Now()
	sweepIdle(t, s, now)
	sweepIdle(t, s, now.Add(19*time.Minute+59*time.Second))
	if old.Stopped() {
		t.Fatal("runtime retired before twenty minutes of idle")
	}
	sweepIdle(t, s, now.Add(20*time.Minute))
	if !old.Stopped() || s.currentRuntime(sid) != nil {
		t.Fatal("idle runtime was not retired")
	}
	if catalog.Stopped() {
		t.Fatal("retiring a session stopped its shared catalog")
	}
	after, e := s.store.Session(sid, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retirement changed durable history, status or queue")
	}
	resumed := submitIdleFixture(t, s, w, "second", sid)
	if resumed.Session.ThreadID != before.Session.ThreadID || resumed.Session.ID != sid || s.currentRuntime(sid) == old {
		t.Fatal("next send did not resume the original history on a fresh runtime")
	}
	var inputs []string
	for _, i := range resumed.Items {
		if i.Kind == "user" {
			inputs = append(inputs, i.Text)
		}
	}
	if !reflect.DeepEqual(inputs, []string{"first", "second"}) {
		t.Fatal("waking a session duplicated or lost an input", inputs)
	}
}

func TestIdleRuntimeResetsItsClockForActivityBetweenSweeps(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	sid := d.Session.ID
	now := time.Now()
	sweepIdle(t, s, now)
	submitIdleFixture(t, s, w, "brief activity", sid)
	sweepIdle(t, s, now.Add(9*time.Minute))
	sweepIdle(t, s, now.Add(20*time.Minute))
	if s.currentRuntime(sid) == nil || s.currentRuntime(sid).Stopped() {
		t.Fatal("brief activity between polls did not restart the idle clock")
	}
	sweepIdle(t, s, now.Add(29*time.Minute))
	if s.currentRuntime(sid) != nil {
		t.Fatal("runtime did not retire after the new idle period")
	}
}

func TestIdleRuntimeMetadataDoesNotRenewExecutionClock(t *testing.T) {
	for _, kind := range []string{"history read", "completion read", "rename", "source title"} {
		t.Run(kind, func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, "first", "")
			sid := d.Session.ID
			r := s.currentRuntime(sid).(*idleTestRuntime)
			now := time.Now()
			sweepIdle(t, s, now)
			var err error
			switch kind {
			case "history read":
				_, err = s.store.Session(sid, 0, 100)
			case "completion read":
				if d.Session.Completion == nil || !d.Session.Completion.Unread {
					t.Fatal("fixture has no unread completion")
				}
				_, err = s.store.ReadCompletion(sid, d.Session.Completion.ID)
			case "rename":
				_, err = s.store.RenameSession(sid, "my title", d.Session.TitleRevision, false)
			case "source title":
				err = r.options.OnEvent(domain.Event{SessionID: sid, Type: "session.title", Text: "source title"})
			}
			if err != nil {
				t.Fatal(err)
			}
			sweepIdle(t, s, now.Add(19*time.Minute))
			sweepIdle(t, s, now.Add(20*time.Minute))
			if !r.Stopped() || s.currentRuntime(sid) != nil {
				t.Fatal("presentation metadata prolonged the execution runtime")
			}
		})
	}
}

func TestIdleRuntimeNativeActivityBetweenSweepsRenewsClock(t *testing.T) {
	for _, kind := range []string{"late output", "native turn cycle"} {
		t.Run(kind, func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, "first", "")
			sid := d.Session.ID
			r := s.currentRuntime(sid).(*idleTestRuntime)
			now := time.Now()
			sweepIdle(t, s, now)
			if kind == "late output" {
				if err := r.options.OnEvent(domain.Event{SessionID: sid, Type: "item.upsert", Item: &domain.Item{ID: "late-native-output", TurnID: "turn/first", Kind: "tool", Text: "late result", Final: true}}); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, status := range []domain.ExecutionState{"running", "completed"} {
					if err := r.options.OnEvent(domain.Event{SessionID: sid, Type: "turn", Turn: &domain.Turn{ID: "native-between-sweeps", Status: status}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			sweepIdle(t, s, now.Add(19*time.Minute))
			sweepIdle(t, s, now.Add(20*time.Minute))
			if r.Stopped() || s.currentRuntime(sid) == nil {
				t.Fatal("native execution activity did not renew the clock")
			}
			sweepIdle(t, s, now.Add(39*time.Minute))
			if !r.Stopped() || s.currentRuntime(sid) != nil {
				t.Fatal("runtime remained after the renewed idle deadline")
			}
		})
	}
}

func TestIdleRuntimePreservesBusyOrUncertainSessions(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Server, domain.SessionDetail) error
	}{
		{"running", func(s *Server, d domain.SessionDetail) error {
			d.Session.Status = "running"
			_, e := s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session})
			return e
		}},
		{"unknown", func(s *Server, d domain.SessionDetail) error {
			d.Session.Status = "unknown"
			_, e := s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session})
			return e
		}},
		{"pending question", func(s *Server, d domain.SessionDetail) error {
			_, e := s.store.Apply(d.Session.ID, domain.Event{Type: "request.open", Request: &domain.PendingRequest{ID: "q", Kind: "questions", Title: "Question", State: "pending"}})
			return e
		}},
		{"paused queue", func(s *Server, d domain.SessionDetail) error {
			if _, e := s.store.PauseQueue(d.Session.ID, "keep paused"); e != nil {
				return e
			}
			_, e := s.store.Enqueue(domain.Submission{OperationID: "queued", SessionID: d.Session.ID, WorkspaceID: d.Session.WorkspaceID, Text: "queued", Config: d.Session.Config})
			return e
		}},
		{"child agent", func(s *Server, d domain.SessionDetail) error {
			_, e := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "turn/child", State: "running", OriginRef: d.Session.ThreadID}})
			return e
		}},
		{"unknown background task", func(s *Server, d domain.SessionDetail) error {
			_, e := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "bg", Kind: "background", TurnID: "turn/bg", State: "unknown", OriginRef: d.Session.ThreadID}})
			return e
		}},
		{"unsettled input", func(s *Server, d domain.SessionDetail) error {
			if _, e := s.store.Accept(domain.Submission{OperationID: "reserved", SessionID: d.Session.ID, WorkspaceID: d.Session.WorkspaceID, Text: "reserved", Config: d.Session.Config}); e != nil {
				return e
			}
			_, e := s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session})
			return e
		}},
		{"prepared history", func(s *Server, d domain.SessionDetail) error {
			_, e := s.store.PrepareHistory(domain.HistoryChange{OperationID: "history", SessionID: d.Session.ID, Revision: d.Session.Revision, TurnID: d.Turns[0].ID, Config: d.Session.Config})
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, fmt.Sprint("first-", tc.name), "")
			r := s.currentRuntime(d.Session.ID)
			now := time.Now()
			sweepIdle(t, s, now)
			if e := tc.change(s, d); e != nil {
				t.Fatal(e)
			}
			sweepIdle(t, s, now.Add(20*time.Minute))
			sweepIdle(t, s, now.Add(20*time.Minute))
			if r.Stopped() || s.currentRuntime(d.Session.ID) != r {
				t.Fatal("reclaimed protected execution")
			}
		})
	}
}

func TestIdleRuntimeQueueChangesBetweenSweepsRestartTheClock(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	now := time.Now()
	sweepIdle(t, s, now)
	if _, e := s.store.Enqueue(domain.Submission{OperationID: "queued", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "queued", Config: d.Session.Config}); e != nil {
		t.Fatal(e)
	}
	q, e := s.store.Queue(d.Session.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.RemoveQueue(d.Session.ID, q.Items[0].ID, q.Revision); e != nil {
		t.Fatal(e)
	}
	sweepIdle(t, s, now.Add(9*time.Minute))
	sweepIdle(t, s, now.Add(20*time.Minute))
	if s.currentRuntime(d.Session.ID) == nil {
		t.Fatal("a queue edit did not reset its idle period")
	}
	sweepIdle(t, s, now.Add(29*time.Minute))
	if s.currentRuntime(d.Session.ID) != nil {
		t.Fatal("empty queue prevented later retirement")
	}
}

func TestIdleRuntimeSerializesRetirementWithTheNextSubmission(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	now := time.Now()
	sweepIdle(t, s, now)
	old := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	old.closing = make(chan struct{}, 1)
	release := make(chan struct{})
	old.release = release
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	retired := make(chan error, 1)
	go func() { retired <- s.reclaimIdleRuntimes(func() time.Time { return now.Add(20 * time.Minute) }) }()
	select {
	case <-old.closing:
	case <-time.After(2 * time.Second):
		t.Fatal("retirement did not reach its close boundary")
	}
	submitted := make(chan domain.Operation, 1)
	failed := make(chan error, 1)
	go func() {
		op, e := s.submit(domain.Submission{OperationID: "concurrent", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "next", Config: d.Session.Config})
		if e != nil {
			failed <- e
			return
		}
		submitted <- op
	}()
	select {
	case <-submitted:
		t.Fatal("new input bypassed retirement fence")
	case e := <-failed:
		t.Fatal(e)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case e := <-retired:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retirement did not finish")
	}
	select {
	case op := <-submitted:
		if r := awaitReceipt(t, s, op.ID); r.State != "confirmed" {
			t.Fatal(r)
		}
	case e := <-failed:
		t.Fatal(e)
	case <-time.After(2 * time.Second):
		t.Fatal("next submission did not resume")
	}
	s.dispatchMu.Lock()
	after, e := s.store.Session(d.Session.ID, 0, 100)
	s.dispatchMu.Unlock()
	if e != nil || after.Session.ThreadID != d.Session.ThreadID || len(after.Items) != 4 || s.currentRuntime(d.Session.ID) == old {
		t.Fatal("handoff lost history or did not replace its process", after, e)
	}
}

func TestIdleBackgroundWaitingIsProtectedUntilTwentyFourHours(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "background-wait", "")
	_, err := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "background", Kind: "background", TurnID: d.Turns[0].ID, OriginRef: d.Session.ThreadID, State: "waiting"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sweepIdle(t, s, now)
	sweepIdle(t, s, now.Add(23*time.Hour+59*time.Minute))
	if s.currentRuntime(d.Session.ID) == nil {
		t.Fatal("waiting background lost its 24 hour protection")
	}
	sweepIdle(t, s, now.Add(24*time.Hour))
	if s.currentRuntime(d.Session.ID) != nil {
		t.Fatal("waiting background prevented retirement forever")
	}
}

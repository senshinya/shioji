package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"testing"
)

type compactRuntime struct {
	harness.Runtime
	failure              error
	observation          domain.ContextObservation
	calls                int
	before               func(domain.Session)
	forceResume, resumed bool
	hold                 bool
	steers               int
}

func (r *compactRuntime) Observes(s domain.Session) bool {
	return (!r.forceResume || r.resumed) && r.Runtime.Observes(s)
}
func (r *compactRuntime) Resume(_ context.Context, s domain.Session) error {
	r.resumed = true
	return r.Runtime.(*memoryRuntime).options.OnEvent(domain.Event{Type: "session.title", SessionID: s.ID, Text: s.Title})
}

func (r *compactRuntime) Readiness() domain.Readiness {
	ready := r.Runtime.Readiness()
	ready.Capabilities.Compact = true
	ready.Capabilities.Steer = true
	return ready
}
func (r *compactRuntime) Compact(_ context.Context, s domain.Session) error {
	r.calls++
	if r.before != nil {
		r.before(s)
	}
	if r.failure != nil {
		return r.failure
	}
	source := r.Runtime.(*memoryRuntime)
	for _, e := range []domain.Event{
		{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: "compact-turn", Status: "running"}},
		{SessionID: s.ID, Type: "item.upsert", Item: &domain.Item{ID: "compact-item", TurnID: "compact-turn", Kind: "activity", Presentation: "compaction", Status: "running"}},
		{SessionID: s.ID, Type: "item.upsert", Item: &domain.Item{ID: "compact-item", TurnID: "compact-turn", Kind: "activity", Presentation: "compaction", Status: "completed", Final: true}},
		{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: "compact-turn", Status: "completed"}},
	} {
		if r.hold && (e.Turn != nil && e.Turn.Status == "completed" || e.Item != nil && e.Item.Final) {
			continue
		}
		if err := source.options.OnEvent(e); err != nil {
			return err
		}
	}
	return nil
}
func (r *compactRuntime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	r.steers++
	return nil
}

func TestWorkerCannotSteerCompactionAsAUserTurn(t *testing.T) {
	s, sid, r := compactWorker(t)
	r.hold = true
	d, _ := s.store.Session(sid, 0, 1)
	if _, err := compactCall(s, "session.context.compact", domain.ContextActionRequest{OperationID: "held", SessionID: sid, Revision: d.Session.Revision}); err != nil {
		t.Fatal(err, d.Session)
	}
	_, err := s.steer(protocol.SteerInput{Input: domain.Submission{OperationID: "bad-steer", SessionID: sid, WorkspaceID: d.Session.WorkspaceID, Text: "do not inject", Config: d.Session.Config}, TurnID: "compact-turn"})
	if err == nil || r.steers != 0 {
		t.Fatal("steered maintenance turn", err, r.steers)
	}
	if _, err = s.store.Operation("bad-steer"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("steer was durably accepted", err)
	}
}
func (r *compactRuntime) InspectCompaction(context.Context, domain.Session, domain.ContextAction) (domain.ContextObservation, error) {
	return r.observation, nil
}
func compactCall(s *Server, method string, in domain.ContextActionRequest) (domain.Operation, error) {
	b, _ := json.Marshal(in)
	out, err := s.handle(method, b)
	if err != nil {
		return domain.Operation{}, err
	}
	return out.(domain.ContextActionResult).Operation, nil
}
func compactWorker(t *testing.T) (*Server, string, *compactRuntime) {
	t.Helper()
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	op, err := s.submit(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "root", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, "root")
	s.dispatchMu.Lock()
	r := &compactRuntime{Runtime: s.currentRuntime(op.SessionID)}
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = r
	s.runtimeMu.Unlock()
	s.dispatchMu.Unlock()
	return s, op.SessionID, r
}
func TestCompactionCommandReservesBeforeCallingAndNeverAddsUserInput(t *testing.T) {
	s, sid, r := compactWorker(t)
	before, _ := s.store.Session(sid, 0, 100)
	r.before = func(session domain.Session) {
		d, _ := s.store.Session(sid, 0, 1)
		op, err := s.store.Operation("compact")
		if err != nil || !d.Session.Status.Active() || op.State != "dispatched" {
			t.Fatal("compaction executed without durable reservation", d.Session, op, err)
		}
	}
	in := domain.ContextActionRequest{OperationID: "compact", SessionID: sid, Revision: before.Session.Revision}
	op, err := compactCall(s, "session.context.compact", in)
	if err != nil || op.State != "confirmed" {
		t.Fatal("manual compaction unavailable", op, err)
	}
	after, _ := s.store.Session(sid, 0, 100)
	users := 0
	for _, item := range after.Items {
		if item.Kind == "user" {
			users++
		}
	}
	if users != 1 || !reflect.DeepEqual(after.Queue, before.Queue) || after.Session.Config != before.Session.Config || after.Session.Title != before.Session.Title || after.Session.Status != "completed" {
		t.Fatal("compaction changed input/queue/config", after)
	}
	if _, err = compactCall(s, "session.context.compact", in); err != nil || r.calls != 1 {
		t.Fatal("replayed compression", err, r.calls)
	}
	in.Revision++
	if _, err = compactCall(s, "session.context.compact", in); err == nil {
		t.Fatal("conflicting operation reused")
	}
}
func TestUnknownCompactionOnlyQueriesAndDoesNotSendAgain(t *testing.T) {
	s, sid, r := compactWorker(t)
	d, _ := s.store.Session(sid, 0, 100)
	r.failure = &harness.DeliveryError{Uncertain: true, Message: "lost reply"}
	in := domain.ContextActionRequest{OperationID: "unknown-compact", SessionID: sid, Revision: d.Session.Revision}
	op, err := compactCall(s, "session.context.compact", in)
	if err != nil || op.State != "indeterminate" {
		t.Fatal(op, err)
	}
	op, err = compactCall(s, "session.context.operation", in)
	if err != nil || op.State != "indeterminate" || r.calls != 1 {
		t.Fatal("query resent", op, err, r.calls)
	}
	r.observation = domain.ContextObservation{TurnID: "verified-compact", State: "completed"}
	op, err = compactCall(s, "session.context.operation", in)
	if err != nil || op.State != "confirmed" || r.calls != 1 {
		t.Fatal("source receipt not adopted", op, err, r.calls)
	}
}

func TestCompactionCanResumeSavedThreadDespiteReadOnlyMetadataRefresh(t *testing.T) {
	s, sid, r := compactWorker(t)
	r.forceResume = true
	d, _ := s.store.Session(sid, 0, 1)
	op, err := compactCall(s, "session.context.compact", domain.ContextActionRequest{OperationID: "resume-compact", SessionID: sid, Revision: d.Session.Revision})
	if err != nil || op.State != "confirmed" || r.calls != 1 {
		t.Fatal("metadata refresh broke first compression", op, err, r.calls)
	}
}
func TestCompactionRejectsParentOrIndependentExecutionBeforeAcceptance(t *testing.T) {
	for _, kind := range []string{"running", "child", "guard", "changed", "unsettled"} {
		t.Run(kind, func(t *testing.T) {
			s, sid, r := compactWorker(t)
			d, _ := s.store.Session(sid, 0, 100)
			switch kind {
			case "running":
				d.Session.Status = "running"
			case "guard":
				s.store.GuardExecution(sid, "lost")
			case "changed":
				d.Session.ContextChanged = true
				s.store.Apply(sid, domain.Event{Type: "session.git", Session: &d.Session})
			case "child":
				child := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "turn/root", OriginRef: d.Session.ThreadID, State: "running"}
				s.store.Apply(sid, domain.Event{Type: "work.upsert", Work: &child})
			case "unsettled":
				s.store.SetOperation("root", "indeterminate", "unknown")
			}
			if kind == "running" {
				s.store.Apply(sid, domain.Event{Type: "session", Session: &d.Session})
			}
			d, _ = s.store.Session(sid, 0, 100)
			_, err := compactCall(s, "session.context.compact", domain.ContextActionRequest{OperationID: "blocked", SessionID: sid, Revision: d.Session.Revision})
			if err == nil || r.calls != 0 {
				t.Fatal("unsafe compaction accepted", kind, err, r.calls)
			}
			if _, err = s.store.Operation("blocked"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("blocked command became durable", err)
			}
		})
	}
}

type sessionOfferedCompaction struct {
	*compactRuntime
	prepared bool
}

func (r *sessionOfferedCompaction) Readiness() domain.Readiness {
	ready := r.compactRuntime.Readiness()
	ready.Capabilities.Compact = false
	return ready
}

func (r *sessionOfferedCompaction) PrepareCompaction(_ context.Context, s domain.Session) error {
	if !r.Observes(s) {
		return errors.New("target session has not been resumed")
	}
	r.prepared = true
	return nil
}

func TestCompactionChecksTargetOfferAfterColdResume(t *testing.T) {
	s, sid, inner := compactWorker(t)
	inner.forceResume = true
	r := &sessionOfferedCompaction{compactRuntime: inner}
	s.runtimeMu.Lock()
	s.runtimes[sid] = r
	s.runtimeMu.Unlock()
	d, err := s.store.Session(sid, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	op, err := compactCall(s, "session.context.compact", domain.ContextActionRequest{OperationID: "scoped-compact", SessionID: sid, Revision: d.Session.Revision})
	if err != nil || op.State != domain.OperationConfirmed || !r.prepared || r.calls != 1 {
		t.Fatalf("cold target was not checked and dispatched: %+v, %v", op, err)
	}
}

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"testing"
	"time"
)

type heldChildRuntime struct {
	*workMessageRuntime
	entered chan struct{}
	release chan struct{}
	query   bool
}

func (r *heldChildRuntime) SendWork(ctx context.Context, s domain.Session, in domain.WorkInput) error {
	if !r.query {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.workMessageRuntime.SendWork(ctx, s, in)
}
func (r *heldChildRuntime) WorkInputReceived(ctx context.Context, s domain.Session, in domain.WorkInput) (bool, error) {
	if r.query {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return r.workMessageRuntime.WorkInputReceived(ctx, s, in)
}
func TestNativeChildWaitDoesNotBlockPrivateDrain(t *testing.T) {
	for _, query := range []bool{false, true} {
		t.Run(fmt.Sprint(query), func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, "audit-child-root", "")
			child := domain.WorkActivity{ID: "child", Reference: "opaque-child", OriginRef: d.Session.ThreadID, Kind: "agent", TurnID: d.Turns[0].ID, State: "running", CanSteer: true}
			if _, e := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &child}); e != nil {
				t.Fatal(e)
			}
			r := &heldChildRuntime{workMessageRuntime: &workMessageRuntime{Runtime: s.currentRuntime(d.Session.ID)}, entered: make(chan struct{}), release: make(chan struct{}), query: query}
			s.runtimeMu.Lock()
			s.runtimes[d.Session.ID] = r
			s.runtimeMu.Unlock()
			s.dispatchMu.Lock()
			s.config.ServiceInstance = "audit-instance"
			s.dispatchMu.Unlock()
			in := domain.WorkInput{OperationID: "audit-child-message", SessionID: d.Session.ID, WorkID: "child", Mode: "steer", Text: "native delayed"}
			if query {
				if _, _, e := s.store.AcceptWorkInput(in); e != nil {
					t.Fatal(e)
				}
				if e := s.store.SetWorkInput(in.OperationID, "indeterminate", "lost reply"); e != nil {
					t.Fatal(e)
				}
			}
			finished := make(chan error, 1)
			go func() {
				var e error
				if query {
					_, e = s.queryWorkInput(in)
				} else {
					_, e = s.sendWork(in)
				}
				finished <- e
			}()
			select {
			case <-r.entered:
			case <-time.After(time.Second):
				t.Fatal("child native I/O not entered")
			}
			ready := make(chan struct{})
			go func() {
				b, _ := json.Marshal(supervisor.Control{Instance: "audit-instance", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
				_, _ = s.serviceControl("worker.prepare-drain", b)
				close(ready)
			}()
			blocked := false
			select {
			case <-ready:
			case <-time.After(200 * time.Millisecond):
				blocked = true
			}
			close(r.release)
			if e := <-finished; e != nil {
				t.Fatal(e)
			}
			<-ready
			t.Logf("OBSERVATION receiptQuery=%v actualPrepareDrainHandlerBlocked=%v nativeCalls=1", query, blocked)
			if blocked {
				t.Fatal("child native I/O blocked device drain")
			}
		})
	}
}

type heldReplyRuntime struct {
	*requestBoundaryRuntime
	entered, release chan struct{}
}

func (r *heldReplyRuntime) Respond(a domain.Answer) error {
	close(r.entered)
	<-r.release
	return r.requestBoundaryRuntime.Respond(a)
}
func TestRequestReplyWaitDoesNotBlockPrivateDrain(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "reply-root", "")
	req := domain.PendingRequest{ID: "reply-request", SessionID: d.Session.ID, TurnID: d.Turns[0].ID, Kind: "approval", State: "pending", Revision: 1, Choices: []domain.RequestChoice{{ID: "allow", Label: "allow"}}}
	if _, e := s.store.Apply(d.Session.ID, domain.Event{Type: "request.open", Request: &req}); e != nil {
		t.Fatal(e)
	}
	r := &heldReplyRuntime{requestBoundaryRuntime: &requestBoundaryRuntime{Runtime: s.currentRuntime(d.Session.ID)}, entered: make(chan struct{}), release: make(chan struct{})}
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = r
	s.runtimeMu.Unlock()
	s.dispatchMu.Lock()
	s.config.ServiceInstance = "reply-instance"
	s.dispatchMu.Unlock()
	finished := make(chan error, 1)
	go func() {
		b, _ := json.Marshal(protocol.AnswerInput{SessionID: d.Session.ID, Answer: domain.Answer{RequestID: req.ID, Revision: 1, Decision: "allow"}})
		_, e := s.handle("request.respond", b)
		finished <- e
	}()
	<-r.entered
	ready := make(chan error, 1)
	go func() {
		b, _ := json.Marshal(supervisor.Control{Instance: "reply-instance", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
		_, e := s.serviceControl("worker.prepare-drain", b)
		ready <- e
	}()
	blocked := false
	select {
	case e := <-ready:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(200 * time.Millisecond):
		blocked = true
	}
	close(r.release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if blocked {
		<-ready
		t.Fatal("approval native reply held device dispatch")
	}
}

func TestLateWorkReplyCannotConfirmRetiredInput(t *testing.T) {
	for _, query := range []bool{false, true} {
		t.Run(fmt.Sprint(query), func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, "late-child-root", "")
			child := domain.WorkActivity{ID: "child", Reference: "opaque-child", OriginRef: d.Session.ThreadID, Kind: "agent", TurnID: d.Turns[0].ID, State: "running", CanSteer: true}
			s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &child})
			r := &heldChildRuntime{workMessageRuntime: &workMessageRuntime{Runtime: s.currentRuntime(d.Session.ID), received: true}, entered: make(chan struct{}), release: make(chan struct{}), query: query}
			s.runtimeMu.Lock()
			s.runtimes[d.Session.ID] = r
			s.runtimeMu.Unlock()
			in := domain.WorkInput{OperationID: "late-child-input", SessionID: d.Session.ID, WorkID: "child", Mode: "steer", Text: "old child only"}
			if query {
				s.store.AcceptWorkInput(in)
				s.store.SetWorkInput(in.OperationID, "indeterminate", "lost receipt")
			}
			finished := make(chan error, 1)
			go func() {
				var e error
				if query {
					_, e = s.queryWorkInput(in)
				} else {
					_, e = s.sendWork(in)
				}
				finished <- e
			}()
			<-r.entered
			retired := make(chan error, 1)
			go func() {
				s.dispatchMu.Lock()
				e := s.detachFailedRuntime(d.Session.ID, fmt.Errorf("captured retirement"))
				s.dispatchMu.Unlock()
				retired <- e
			}()
			blocked := false
			select {
			case e := <-retired:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(200 * time.Millisecond):
				blocked = true
			}
			close(r.release)
			if blocked {
				<-retired
				<-finished
				t.Fatal("old native call prevented logical retirement")
			}
			<-finished
			saved, e := s.store.WorkInput(in.OperationID)
			if e != nil || saved.State != "abandoned" {
				t.Fatal("late reply rewrote retired input", saved, e)
			}
			// A repeated caller may query the old receipt, never send it to a new child.
			again, e := s.sendWork(in)
			if e != nil || again.State != "abandoned" {
				t.Fatal("old input was replayed or receipt lost", again, e)
			}
		})
	}
}

func TestRequestReplyRetirementReleasesBeforeNativeReturn(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "retired-reply", "")
	req := domain.PendingRequest{ID: "retired-request", SessionID: d.Session.ID, TurnID: d.Turns[0].ID, Kind: "approval", State: "pending", Revision: 1, Choices: []domain.RequestChoice{{ID: "allow", Label: "allow"}}}
	if _, e := s.store.Apply(d.Session.ID, domain.Event{Type: "request.open", Request: &req}); e != nil {
		t.Fatal(e)
	}
	r := &heldReplyRuntime{requestBoundaryRuntime: &requestBoundaryRuntime{Runtime: s.currentRuntime(d.Session.ID)}, entered: make(chan struct{}), release: make(chan struct{})}
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = r
	s.runtimeMu.Unlock()
	replied := make(chan error, 1)
	go func() {
		replied <- s.respondRequest(AnswerInput{SessionID: d.Session.ID, Answer: domain.Answer{RequestID: req.ID, Revision: 1, Decision: "allow"}})
	}()
	<-r.entered
	s.dispatchMu.Lock()
	e := s.detachFailedRuntime(d.Session.ID, fmt.Errorf("retired original request owner"))
	s.dispatchMu.Unlock()
	if e != nil {
		close(r.release)
		t.Fatal(e)
	}
	select {
	case e := <-replied:
		if e == nil {
			close(r.release)
			t.Fatal("retired request reported native success")
		}
	case <-time.After(time.Second):
		close(r.release)
		<-replied
		t.Fatal("retired response still waited for native pipe")
	}
	close(r.release)
	after, e := s.store.Session(d.Session.ID, 0, 1)
	if e != nil {
		t.Fatal(e)
	}
	for _, request := range after.Requests {
		if request.ID == req.ID && request.State != "invalidated" {
			t.Fatal("late response restored retired request", request)
		}
	}
}

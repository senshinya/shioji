package worker

import (
	"context"
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"testing"
	"time"
)

// The harness ACKs interrupt but deliberately never completes the original
// turn. A Stop must retire that captured runtime without a second user action.
func TestStopAcknowledgementDoesNotLeaveUnfinishedRuntimeForever(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "hold", hold: true})
	op, err := s.submit(domain.Submission{OperationID: "hold", WorkspaceID: w.ID, Text: "hold", Config: domain.RunConfig{Model: "hold-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	r := s.currentRuntime(op.SessionID)
	d, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"sessionId": op.SessionID, "turnId": d.Turns[0].ID})
	if _, err = s.handle("turn.interrupt", body); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("interrupt ACK left unfinished runtime alive")
	}
	for deadline := time.Now().Add(time.Second); ; {
		latest, e := s.store.Session(op.SessionID, 0, 1)
		if e != nil {
			t.Fatal(e)
		}
		if latest.Session.Status == "interrupted" {
			if latest.Turns[0].Status != "interrupted" {
				t.Fatal("confirmed stop lost its interrupted turn", latest.Turns)
			}
			break
		}
		if latest.Session.Status == "failed" || time.Now().After(deadline) {
			t.Fatal("confirmed physical stop was reported as failure", latest.Session)
		}
		time.Sleep(time.Millisecond)
	}
	q, err := s.store.Queue(op.SessionID)
	if err != nil || !q.Paused {
		t.Fatal("Stop lost queue pause", q, err)
	}
}

type blockedCreationHarness struct {
	idleHarness
	started chan struct{}
	release chan struct{}
}

func (a blockedCreationHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	if o.Scope != nil {
		select {
		case a.started <- struct{}{}:
		default:
		}
		<-a.release
		ctx = context.Background()
	}
	return a.idleHarness.Start(ctx, o)
}
func TestStopDetachesUnfinishedRuntimeCreation(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, blockedCreationHarness{started: started, release: release})
	op, err := s.submit(domain.Submission{OperationID: "initializing", WorkspaceID: w.ID, Text: "must not send", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("creation did not start")
	}
	if err := s.interruptTurn(op.SessionID, "old-turn"); err == nil {
		t.Fatal("an old turn Stop cancelled a new runtime that was still starting")
	}
	raw, _ := json.Marshal(map[string]string{"sessionId": op.SessionID, "turnId": ""})
	stopped := make(chan error, 1)
	go func() { _, err := s.handle("turn.interrupt", raw); stopped <- err }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked behind unfinished creation")
	}
}

type stalledSteerHarness struct {
	entered chan struct{}
	release chan struct{}
}

func (a stalledSteerHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "stalled", Name: "stalled"}
}
func (a stalledSteerHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, e := (memoryHarness{id: "stalled", hold: true}).Start(ctx, o)
	if e != nil {
		return nil, e
	}
	return &stalledSteerRuntime{memoryRuntime: r.(*memoryRuntime), entered: a.entered, release: a.release}, nil
}

type stalledSteerRuntime struct {
	*memoryRuntime
	entered chan struct{}
	release chan struct{}
}

func (r *stalledSteerRuntime) Readiness() domain.Readiness {
	d := r.memoryRuntime.Readiness()
	d.Capabilities.Steer = true
	d.Harnesses[0].Capabilities.Steer = true
	return d
}
func (r *stalledSteerRuntime) Steer(ctx context.Context, _ domain.Session, _, _ string, _ harness.TurnInput) error {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return nil
	case <-r.Done():
		return &harness.DeliveryError{Uncertain: true, Message: "native steering did not settle before stop"}
	case <-ctx.Done():
		return &harness.DeliveryError{Uncertain: true, Message: ctx.Err().Error()}
	}
}
func TestStopIsNotBlockedByAnUnsettledSteeringRequest(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, stalledSteerHarness{entered: entered, release: release})
	op, e := s.submit(domain.Submission{OperationID: "running", WorkspaceID: w.ID, Text: "running", Config: domain.RunConfig{Model: "stalled-model", Permission: "limited"}})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, op.ID)
	d, e := s.store.Session(op.SessionID, 0, 1)
	if e != nil {
		t.Fatal(e)
	}
	steer, e := s.steer(protocol.SteerInput{Input: domain.Submission{OperationID: "steering", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "steering", Config: d.Session.Config}, TurnID: d.Turns[0].ID})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("steering did not enter native call")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- s.interruptTurn(op.SessionID, d.Turns[0].ID) }()
	select {
	case e := <-stopped:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked behind native steering")
	}
	if result := awaitReceipt(t, s, steer.ID); result.State != "indeterminate" && result.State != "abandoned" {
		t.Fatal("stopped steering was confirmed or replayed", result)
	}
}

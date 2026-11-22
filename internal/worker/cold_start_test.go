package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

type pacedStartHarness struct {
	memoryHarness
	binding chan string
	release chan struct{}
}

func (a pacedStartHarness) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, opts)
	if err != nil || opts.Scope == nil {
		return r, err
	}
	return &pacedStartRuntime{memoryRuntime: r.(*memoryRuntime), binding: a.binding, release: a.release}, nil
}

type pacedStartRuntime struct {
	*memoryRuntime
	binding chan string
	release chan struct{}
}

func (r *pacedStartRuntime) Create(ctx context.Context, s domain.Session) (string, error) {
	r.binding <- s.ID
	select {
	case <-r.release:
		return r.memoryRuntime.Create(ctx, s)
	case <-ctx.Done():
		return "", ctx.Err()
	case <-r.Done():
		return "", context.Canceled
	}
}

func (r *pacedStartRuntime) Readiness() domain.Readiness {
	ready := r.memoryRuntime.Readiness()
	ready.Capabilities.Steer = true
	return ready
}

func (r *pacedStartRuntime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	return nil
}

func TestColdStartLimitLeavesLiveSteerAvailable(t *testing.T) {
	binding := make(chan string, 7)
	release := make(chan struct{}, 7)
	s, w := memoryWorker(t, pacedStartHarness{
		memoryHarness: memoryHarness{id: "paced", hold: true},
		binding:       binding,
		release:       release,
	})
	t.Cleanup(func() {
		close(release)
	})
	submit := func(id string) domain.Operation {
		t.Helper()
		op, err := s.submit(domain.Submission{OperationID: id, WorkspaceID: w.ID, Text: id,
			Config: domain.RunConfig{Model: "paced-model", Permission: "limited"}})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}

	active := submit("live")
	release <- struct{}{}
	awaitReceipt(t, s, active.ID)
	<-binding

	waiting := make(map[string]domain.Operation)
	for i := range 6 {
		op := submit(fmt.Sprintf("cold-%d", i))
		waiting[op.SessionID] = op
	}
	for range 2 {
		select {
		case id := <-binding:
			delete(waiting, id)
		case <-time.After(time.Second):
			t.Fatal("two cold sessions did not reach their thread binding")
		}
	}
	select {
	case <-binding:
		t.Fatal("a third cold session started before an earlier thread bound")
	case <-time.After(200 * time.Millisecond):
	}

	for id, op := range waiting {
		if err := s.interruptTurn(id, "", op.ID); err != nil {
			t.Fatal("stop queued startup:", err)
		}
		if stopped := awaitReceipt(t, s, op.ID); stopped.State != domain.OperationRejected {
			t.Fatalf("queued startup was not rejected: %+v", stopped)
		}
		break
	}

	steer, err := s.steer(protocol.SteerInput{TurnID: "turn/live", Input: domain.Submission{
		OperationID: "steer-live", SessionID: active.SessionID, WorkspaceID: w.ID, Text: "continue",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt := awaitReceipt(t, s, steer.ID); receipt.State != domain.OperationConfirmed {
		t.Fatalf("live steer did not reach the harness: %+v", receipt)
	}

	release <- struct{}{}
	select {
	case id := <-binding:
		if op := waiting[id]; awaitableRejected(s, op.ID) {
			t.Fatal("cancelled startup entered thread binding")
		}
	case <-time.After(time.Second):
		t.Fatal("next cold session did not start after capacity opened")
	}
}

func awaitableRejected(s *Server, id string) bool {
	op, err := s.store.Operation(id)
	return err == nil && op.State == domain.OperationRejected
}

type versionWaitHarness struct {
	memoryHarness
	probing chan struct{}
	release chan struct{}
	block   chan struct{}
}

func (a versionWaitHarness) CatalogVersion() (string, error) {
	select {
	case <-a.block:
	default:
		return "installed", nil
	}
	select {
	case a.probing <- struct{}{}:
	default:
	}
	<-a.release
	return "installed", nil
}

func (a versionWaitHarness) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, opts)
	if err != nil || opts.Scope == nil {
		return r, err
	}
	return &versionWaitRuntime{memoryRuntime: r.(*memoryRuntime)}, nil
}

type versionWaitRuntime struct{ *memoryRuntime }

func (*versionWaitRuntime) CatalogVersion() (string, error) { return "installed", nil }

func TestInstallVersionProbeDoesNotBlockOtherSessionSteer(t *testing.T) {
	probing, release, block := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	steerReached, settleSteer := make(chan struct{}, 1), make(chan struct{})
	close(settleSteer)
	s, w := memoryWorker(t,
		versionWaitHarness{memoryHarness: memoryHarness{id: "version"}, probing: probing, release: release, block: block},
		stalledSteerHarness{entered: steerReached, release: settleSteer},
	)
	t.Cleanup(func() { close(release) })
	submit := func(id, agent, session string) domain.Operation {
		t.Helper()
		op, err := s.submit(domain.Submission{OperationID: id, HarnessID: agent, SessionID: session, WorkspaceID: w.ID,
			Text: id, Config: domain.RunConfig{Model: agent + "-model", Permission: "limited"}})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	idle := submit("idle-version", "version", "")
	active := submit("active-steer", "stalled", "")
	awaitReceipt(t, s, idle.ID)
	awaitReceipt(t, s, active.ID)
	close(block)
	submit("warm-version", "version", idle.SessionID)
	select {
	case <-probing:
	case <-time.After(time.Second):
		t.Fatal("next turn did not reach the installed-version check")
	}
	errs := make(chan error, 1)
	go func() {
		_, err := s.steer(protocol.SteerInput{TurnID: "turn/active-steer", Input: domain.Submission{
			OperationID: "steer-through-version-check", SessionID: active.SessionID, WorkspaceID: w.ID, Text: "continue",
		}})
		errs <- err
	}()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Steer receipt waited for an unrelated installation probe")
	}
	select {
	case <-steerReached:
	case <-time.After(time.Second):
		t.Fatal("Steer did not reach the running harness during the installation probe")
	}
}

func TestSteerSendsInReceiptOrderWithinOneRuntime(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{}, 2)
	s, w := memoryWorker(t, stalledSteerHarness{entered: entered, release: release})
	t.Cleanup(func() { close(release) })
	op, err := s.submit(domain.Submission{OperationID: "ordered-turn", WorkspaceID: w.ID, Text: "active",
		Config: domain.RunConfig{Model: "stalled-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	steer := func(id string) domain.Operation {
		t.Helper()
		receipt, err := s.steer(protocol.SteerInput{TurnID: "turn/ordered-turn", Input: domain.Submission{
			OperationID: id, SessionID: op.SessionID, WorkspaceID: w.ID, Text: id,
		}})
		if err != nil || receipt.State != domain.OperationAccepted {
			t.Fatalf("steer receipt: %+v %v", receipt, err)
		}
		return receipt
	}
	first := steer("first-steer")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first steer did not reach the harness")
	}
	second := steer("second-steer")
	select {
	case <-entered:
		t.Fatal("second steer reached the same runtime before its predecessor settled")
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second steer did not proceed after first returned")
	}
	release <- struct{}{}
	for _, receipt := range []domain.Operation{first, second} {
		if final := awaitReceipt(t, s, receipt.ID); final.State != domain.OperationConfirmed {
			t.Fatal("steer never confirmed", final)
		}
	}
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func auditController(t *testing.T, handler func(*protocol.Codec, protocol.Frame) bool, unavailable func() bool) (context.Context, *Controller, *Model, func(func() bool)) {
	return auditControllerWithSetup(t, handler, unavailable, nil)
}
func auditControllerWithSetup(t *testing.T, handler func(*protocol.Codec, protocol.Frame) bool, unavailable func() bool, setup func(*Controller)) (context.Context, *Controller, *Model, func(func() bool)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := NewController(t.TempDir())
	c.Dial = func(context.Context) (net.Conn, error) {
		if unavailable != nil && unavailable() {
			return nil, errors.New("isolated link down")
		}
		client, worker := net.Pipe()
		go func() {
			defer worker.Close()
			defer context.AfterFunc(ctx, func() { worker.Close() })()
			codec, _, err := protocol.WelcomeWorker(worker, "audit-device", "audit-journal", domain.WorkerBuild{})
			if err != nil {
				return
			}
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil || !handler(codec, f) {
					return
				}
			}
		}()
		return client, nil
	}
	if setup != nil {
		setup(c)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	m := NewModel()
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.After(4 * time.Second)
		for !ready() {
			select {
			case update := <-c.Updates:
				update(m)
			case <-deadline:
				t.Fatal("controller audit timed out")
			}
		}
	}
	wait(func() bool { return m.ConnectionEpoch != "" })
	return ctx, c, m, wait
}

func TestSlowSubmitLeavesOtherSessionsAndApprovalsAvailable(t *testing.T) {
	entered, release, second, answered := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	cctx, c, _, _ := auditController(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "snapshot":
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal"})
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "events.watch":
			return true
		case "session.submit":
			var p domain.Submission
			if json.Unmarshal(f.Body, &p) != nil {
				return false
			}
			if p.SessionID == "a" {
				close(entered)
				select {
				case <-release:
				case <-time.After(5 * time.Second):
					return false
				}
			} else if p.SessionID == "b" {
				close(second)
			}
			result(codec, f, domain.Operation{ID: p.OperationID, SessionID: p.SessionID, State: domain.OperationAccepted})
		case "request.respond":
			close(answered)
			result(codec, f, nil)
		}
		return true
	}, nil)
	if !c.Do(Job{Method: "session.submit", Input: domain.Submission{OperationID: "a-op", SessionID: "a"}}) {
		t.Fatal("failed to enqueue a")
	}
	for _, ch := range []chan struct{}{entered} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("submit A did not reach worker")
		}
	}
	if !c.Do(Job{Method: "session.submit", Input: domain.Submission{OperationID: "b-op", SessionID: "b"}}) ||
		!c.Do(Job{Method: "request.respond", Input: protocol.AnswerInput{SessionID: "c"}}) {
		t.Fatal("could not enqueue B and C")
	}
	for _, ch := range []chan struct{}{second, answered} {
		select {
		case <-ch:
		case <-cctx.Done():
			t.Fatal(cctx.Err())
		case <-time.After(time.Second):
			t.Fatal("an unrelated command waited for A's receipt")
		}
	}
	close(release)
}

func TestSameJournalReconnectRepairsFromEventCursor(t *testing.T) {
	var unavailable, cut atomic.Bool
	var snapshots atomic.Int32
	watchAfter := make(chan int64, 8)
	linkDown := make(chan struct{})
	cutNow := make(chan struct{})
	_, c, m, wait := auditController(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "snapshot":
			var seq int64
			if snapshots.Add(1) > 1 {
				seq = 2
			}
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal", Sequence: seq})
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "events.watch":
			var q protocol.EventsQuery
			if json.Unmarshal(f.Body, &q) != nil {
				return false
			}
			watchAfter <- q.After
			if cut.CompareAndSwap(false, true) {
				select {
				case <-cutNow:
				case <-time.After(5 * time.Second):
					return false
				}
				unavailable.Store(true)
				close(linkDown)
			} else {
				time.Sleep(2 * time.Second)
			}
			return false
		case "probe-outage":
			return false
		case "events":
			result(codec, f, protocol.EventPage{Generation: "audit-journal", Events: []domain.Event{
				{Sequence: 1, Type: "workspace", Workspace: &domain.Workspace{ID: "live", Revision: 1}},
				{Sequence: 2, Type: "workspace", Workspace: &domain.Workspace{ID: "live", Revision: 2}},
			}})
		}
		return true
	}, unavailable.Load)
	select {
	case seq := <-watchAfter:
		if seq != 0 {
			t.Fatal(seq)
		}
	case <-time.After(time.Second):
		t.Fatal("no watch")
	}
	close(cutNow)
	<-linkDown
	c.Do(Job{Method: "probe-outage"})
	wait(func() bool { return !m.Connected })
	unavailable.Store(false)
	wait(func() bool { return m.Connected && m.ConnectionEpoch != "" && m.Sequence == 2 })
	select {
	case got := <-watchAfter:
		if got != 2 || snapshots.Load() != 1 || len(m.Snapshot.Workspaces) != 1 || m.Snapshot.Workspaces[0].Revision != 2 {
			t.Fatalf("got cursor %d snapshots %d", got, snapshots.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("no watch on reconnection")
	}
}

func TestSlowReadLeavesLiveEventsAndStopAvailable(t *testing.T) {
	reading, unblock, pageSent, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	_, c, m, wait := auditController(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "snapshot":
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal"})
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "file.search":
			close(reading)
			select {
			case <-unblock:
			case <-time.After(4 * time.Second):
				return false
			}
			result(codec, f, nil)
		case "turn.interrupt":
			close(stopped)
			result(codec, f, nil)
		case "events.watch":
			var q protocol.EventsQuery
			if json.Unmarshal(f.Body, &q) != nil || q.After != 0 {
				return false
			}
			select {
			case <-reading:
			case <-time.After(4 * time.Second):
				return false
			}
			ev := domain.Event{Sequence: 1, Type: "workspace", Workspace: &domain.Workspace{ID: "live", DeviceID: "audit-device", Revision: 1}}
			result(codec, f, protocol.EventPage{Generation: "audit-journal", Events: []domain.Event{ev}})
			close(pageSent)
		}
		return true
	}, nil)
	if !c.Do(Job{Method: "file.search"}) {
		t.Fatal("could not queue search")
	}
	select {
	case <-pageSent:
	case <-time.After(time.Second):
		t.Fatal("independent event socket did not deliver")
	}
	if !c.Do(Job{Method: "turn.interrupt", Input: map[string]string{"sessionId": "other"}}) {
		t.Fatal("could not queue Stop")
	}
	wait(func() bool { return m.Sequence == 1 })
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop waited for the slow read")
	}
	close(unblock)
}

func TestRemoteUnsentMutationRequiresNewAuthorizationAfterDisconnect(t *testing.T) {
	entered, cut, release, delivered := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	var down, initialWatch atomic.Bool
	linkDown := make(chan struct{})
	_, c, m, wait := auditControllerWithSetup(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "device.ping":
			result(codec, f, map[string]string{"deviceId": "audit-device"})
		case "snapshot":
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal"})
		case "events":
			result(codec, f, protocol.EventPage{Generation: "audit-journal"})
		case "probe-outage":
			return false
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "events.watch":
			if initialWatch.CompareAndSwap(false, true) {
				select {
				case <-cut:
				case <-time.After(5 * time.Second):
					return false
				}
				down.Store(true)
				close(linkDown)
				return false
			}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			return false
		case "session.submit":
			var in domain.Submission
			if json.Unmarshal(f.Body, &in) != nil {
				return false
			}
			if in.OperationID == "wait-a" {
				close(entered)
				<-release
				return false
			}
			if in.OperationID == "queued-b" {
				close(delivered)
			}
			result(codec, f, domain.Operation{ID: in.OperationID, SessionID: in.SessionID, State: domain.OperationAccepted})
		}
		return true
	}, down.Load, func(c *Controller) {
		original := c.Dial
		c.paths = newDevicePaths(DeviceConnection{ID: "remote", DeviceID: "audit-device", SSH: transport.SSH{Target: "audit.invalid"}}, func(ctx context.Context, _ transport.SSH) (net.Conn, error) { return original(ctx) })
		c.paths.record(c.paths.active(), time.Millisecond, time.Now(), nil)
		c.Dial = c.paths.dial
	})
	epoch := m.ConnectionEpoch
	c.Do(Job{Method: "session.submit", ConnectionEpoch: epoch, Input: domain.Submission{OperationID: "wait-a", SessionID: "a"}})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("submit A never reached worker")
	}
	if !c.Do(Job{Method: "session.submit", ConnectionEpoch: epoch, Input: domain.Submission{OperationID: "queued-b", SessionID: "a"}}) {
		t.Fatal("could not queue B")
	}
	close(cut)
	<-linkDown
	c.Do(Job{Method: "probe-outage"})
	wait(func() bool { return !m.Connected })
	select {
	case <-delivered:
		t.Fatal("B had already been delivered")
	default:
	}
	down.Store(false)
	c.paths.record(c.paths.active(), time.Millisecond, time.Now(), nil)
	wait(func() bool { return m.Connected && m.ConnectionEpoch != "" })
	if m.ConnectionEpoch == epoch {
		t.Fatal("remote command permission did not change after a real disconnect")
	}
	select {
	case <-delivered:
		t.Fatal("a queued mutation sent after reauthorization was required")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestOneFailedWatchReopenKeepsHealthyCommandStream(t *testing.T) {
	var failedNext, firstWatch atomic.Bool
	breakWatch := make(chan struct{})
	resumed := make(chan struct{}, 1)
	_, c, m, wait := auditController(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "snapshot":
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal"})
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "command-probe":
			result(codec, f, true)
		case "events.watch":
			if firstWatch.CompareAndSwap(false, true) {
				<-breakWatch
				failedNext.Store(true)
				return false
			}
			select {
			case resumed <- struct{}{}:
			default:
			}
			result(codec, f, protocol.EventPage{Generation: "audit-journal"})
			return false
		}
		return true
	}, func() bool { return failedNext.CompareAndSwap(true, false) })
	replied := false
	c.Do(Job{Method: "command-probe", Apply: func(_ *Model, out json.RawMessage, e error) {
		if e != nil || string(out) != "true" {
			t.Errorf("command wasn't healthy before watch error: %s %v", out, e)
		}
		replied = true
	}})
	wait(func() bool { return replied })
	close(breakWatch)
	timer := time.After(5 * time.Second)
	for {
		select {
		case u := <-c.Updates:
			u(m)
			if !m.Connected {
				t.Fatal("one observation failure demoted the healthy command stream")
			}
		case <-resumed:
			return
		case <-timer:
			t.Fatal("event stream was not restarted")
		}
	}
}

func TestLostEnqueueReplyChecksTheOwningJournalWithoutReplay(t *testing.T) {
	const id = "missing-enqueue"
	var enqueued atomic.Int32
	queries := make(chan protocol.OperationQuery, 4)
	_, c, m, wait := auditController(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "snapshot":
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal"})
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "turn.enqueue":
			enqueued.Add(1)
			return false
		case "operation":
			var q protocol.OperationQuery
			if json.Unmarshal(f.Body, &q) != nil {
				return false
			}
			queries <- q
			result(codec, f, domain.Operation{ID: id, State: domain.OperationRejected, Error: "未接纳"})
		}
		return true
	}, nil)
	in := domain.Submission{OperationID: id, SessionID: "s", Text: "keep queued draft"}
	m.Pending[id] = in
	m.Drafts["session:s"] = in.Text
	if !c.Do(Job{Method: "turn.enqueue", Input: in}) {
		t.Fatal("failed to queue operation")
	}
	wait(func() bool { return m.Resolved[id] })
	select {
	case q := <-queries:
		if q.ID != id || q.SealGeneration != "audit-journal" {
			t.Fatalf("wrong negative receipt scope: %+v", q)
		}
	default:
		t.Fatal("missing request lookup")
	}
	if enqueued.Load() != 1 || m.Drafts["session:s"] != in.Text {
		t.Fatal("lost reply replayed input or erased its draft")
	}
}

func TestKnownStopAndReceiptBypassCatalogRebuild(t *testing.T) {
	loading, stopSent, querySent := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var snapshots, watches atomic.Int32
	_, c, m, _ := auditController(t, func(codec *protocol.Codec, f protocol.Frame) bool {
		switch f.Method {
		case "snapshot":
			if snapshots.Add(1) > 1 {
				close(loading)
				select {
				case <-release:
				case <-time.After(4 * time.Second):
					return false
				}
			}
			result(codec, f, domain.Snapshot{DeviceID: "audit-device", Generation: "audit-journal"})
		case "readiness":
			result(codec, f, domain.Readiness{State: "ready"})
		case "events.watch":
			if watches.Add(1) == 1 {
				result(codec, f, protocol.EventPage{Generation: "audit-journal", Events: []domain.Event{{Sequence: 2, Type: "workspace"}}})
			}
		case "turn.interrupt":
			close(stopSent)
			result(codec, f, nil)
		case "operation":
			close(querySent)
			result(codec, f, domain.Operation{ID: "known"})
		}
		return true
	}, nil)
	select {
	case <-loading:
	case <-time.After(time.Second):
		t.Fatal("fixture did not start the rebuild")
	}
	epoch := m.ConnectionEpoch
	if !c.Do(Job{Method: "turn.interrupt", ConnectionEpoch: epoch, Input: map[string]string{"sessionId": "known-session", "turnId": "known-turn"}}) ||
		!c.Do(Job{Method: "operation", ConnectionEpoch: epoch, Input: protocol.OperationQuery{ID: "known"}}) {
		t.Fatal("could not ask for Stop or a receipt")
	}
	for _, ready := range []chan struct{}{stopSent, querySent} {
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatal("the known operation waited behind catalog loading")
		}
	}
}

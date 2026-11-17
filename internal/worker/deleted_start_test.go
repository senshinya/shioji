package worker

import (
	"context"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"testing"
	"time"
)

type handoffPendingStartHarness struct {
	idleHarness
	entered, release chan struct{}
}

func (a handoffPendingStartHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	if o.Scope == nil {
		return a.idleHarness.Start(ctx, o)
	}
	close(a.entered)
	<-a.release
	return nil, errors.New("late initialization dependency failure")
}
func TestHandoffDeletePendingStartReleasesAdmission(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, handoffPendingStartHarness{entered: entered, release: release})
	op, err := s.submit(domain.Submission{OperationID: "pending", WorkspaceID: w.ID, Text: "pending", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("start not entered")
	}
	s.dispatchMu.Lock()
	archiveThenDeleteLocked(t, s, op.SessionID)
	s.dispatchMu.Unlock()
	time.Sleep(150 * time.Millisecond)
	s.dispatchMu.Lock()
	s.mu.Lock()
	reservations := len(s.dispatchReservations)
	s.mu.Unlock()
	s.runtimeMu.RLock()
	starts := len(s.runtimeStarts)
	s.runtimeMu.RUnlock()
	ready, err := s.businessStopReady()
	s.dispatchMu.Unlock()
	t.Logf("committed deletion, native Start held: starts=%d reservations=%d drain=%+v err=%v", starts, reservations, ready, err)
	if starts != 0 || reservations != 0 || err != nil || !ready.Ready {
		t.Fatal("deleted initialization still owns upgrade admission")
	}
}
func TestHandoffDeletedStartFailureDoesNotCancelWorker(t *testing.T) {
	for _, control := range []bool{false, true} {
		t.Run(fmt.Sprint("pre-detach=", control), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			s, w := memoryWorker(t, handoffPendingStartHarness{entered: entered, release: release})
			op, err := s.submit(domain.Submission{OperationID: "pending", WorkspaceID: w.ID, Text: "pending", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("start not entered")
			}
			s.dispatchMu.Lock()
			if control {
				s.runtimeMu.RLock()
				pending := s.runtimeStarts[op.SessionID]
				s.runtimeMu.RUnlock()
				if pending == nil {
					t.Fatal("pending start missing")
				}
				if err = s.stopInitializing(op.SessionID, pending); err != nil {
					t.Fatal(err)
				}
			}
			archiveThenDeleteLocked(t, s, op.SessionID)
			s.dispatchMu.Unlock()
			close(release)
			deadline := time.Now().Add(3 * time.Second)
			for {
				s.mu.Lock()
				held := len(s.dispatchReservations)
				s.mu.Unlock()
				if held == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("old start did not finish its dispatch result")
				}
				time.Sleep(time.Millisecond)
			}
			select {
			case <-s.ctx.Done():
				t.Fatal("late failure for deleted initialization canceled entire worker", s.ctx.Err())
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

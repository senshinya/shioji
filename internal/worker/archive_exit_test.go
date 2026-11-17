package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"sync"
	"testing"
	"time"
)

type delayedDeleteHarness struct{ entered, release chan struct{} }

func (delayedDeleteHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "idle", Name: "idle"}
}
func (a delayedDeleteHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (idleHarness{}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &delayedDeleteRuntime{idleTestRuntime: r.(*idleTestRuntime), entered: a.entered, release: a.release}, nil
}

type delayedDeleteRuntime struct {
	*idleTestRuntime
	entered, release chan struct{}
	once             sync.Once
}

func (r *delayedDeleteRuntime) Close(ctx context.Context) error {
	if r.options.Scope == nil {
		return r.memoryRuntime.Close(ctx)
	}
	if err := r.memoryRuntime.Close(ctx); err != nil {
		return err
	}
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func archiveThenDeleteLocked(t *testing.T, s *Server, id string) {
	t.Helper()
	d, err := s.store.Session(id, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := s.archiveGeneration(id)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.store.SetSessionArchived(id, d.Session.Revision, true, gen)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: id, Revision: archived.Revision})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDeletedRuntimeExitDoesNotCancelWorker(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, delayedDeleteHarness{entered: entered, release: release})
	// Keep normal completion so only the deleted owner's Done watcher is in play.
	op, err := s.submit(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "run", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	s.dispatchMu.Lock()
	archiveThenDeleteLocked(t, s, op.SessionID)
	s.dispatchMu.Unlock()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("archive cleanup never closed runtime")
	}
	select {
	case <-s.ctx.Done():
		t.Fatal("exit of deleted runtime canceled the entire owning worker", s.ctx.Err())
	case <-time.After(300 * time.Millisecond):
	}
}

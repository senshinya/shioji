package worker

import (
	"context"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sync/atomic"
	"testing"
	"time"
)

type handoffVersionedIdleHarness struct {
	idleHarness
	version atomic.Int32
}

func (a *handoffVersionedIdleHarness) CatalogVersion() (string, error) {
	return fmt.Sprint(a.version.Load()), nil
}
func (a *handoffVersionedIdleHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := a.idleHarness.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &handoffVersionedIdleRuntime{idleTestRuntime: r.(*idleTestRuntime), version: a.version.Load()}, nil
}

type handoffVersionedIdleRuntime struct {
	*idleTestRuntime
	version int32
}

func (r *handoffVersionedIdleRuntime) CatalogVersion() (string, error) {
	return fmt.Sprint(r.version), nil
}

func TestHandoffSuccessfulRetirementReleasesUnknownOwnership(t *testing.T) {
	for _, route := range []string{"archive-restore", "installation-replacement"} {
		t.Run(route, func(t *testing.T) {
			a := &handoffVersionedIdleHarness{}
			a.version.Store(1)
			s, w := memoryWorker(t, a)
			d := submitIdleFixture(t, s, w, "first", "")
			old := s.currentRuntime(d.Session.ID).(*handoffVersionedIdleRuntime)
			work := domain.WorkActivity{ID: "old-unknown", Kind: "agent", TurnID: "turn/first", OriginRef: d.Session.ThreadID, State: "unknown"}
			if err := old.options.OnEvent(domain.Event{SessionID: d.Session.ID, Type: "work.upsert", Work: &work}); err != nil {
				t.Fatal(err)
			}
			before, err := s.store.RuntimeGCIdle(d.Session.ID)
			if err != nil || before.Idle {
				t.Fatal("current unknown must remain protected", before, err)
			}
			scope, err := s.executions.Existing(d.Session.ID)
			if err != nil {
				t.Fatal(err)
			}
			// A real registered process proves cleanup, independently of harness projection.
			cmd, input, _, err := scope.StartOwned(s.ctx, "worker", []string{"/bin/cat"}, []string{"PATH=/usr/bin:/bin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				input.Close()
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = scope.Stop(cleanup)
				_ = cmd.Wait()
			}()
			if route == "archive-restore" {
				s.dispatchMu.Lock()
				func() {
					defer s.dispatchMu.Unlock()
					latest, err := s.store.Session(d.Session.ID, 0, 1)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, true, scope.Generation); err != nil {
						t.Fatal(err)
					}
					if err = s.cleanRuntimeRetirements(d.Session.ID); err != nil {
						t.Fatal(err)
					}
					if err = scope.VerifyStopped(); err != nil {
						t.Fatal("archive physical stop not proven", err)
					}
					latest, err = s.store.Session(d.Session.ID, 0, 1)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, false); err != nil {
						t.Fatal(err)
					}
				}()
			} else {
				a.version.Store(2)
				// Capture the successful replacement cleanup's stop proof before Begin overwrites the current scope.
				s.dispatchMu.Lock()
				err = s.cleanSessionRuntime(s.ctx, d.Session.ID)
				if err == nil {
					err = scope.VerifyStopped()
				}
				s.dispatchMu.Unlock()
				if err != nil {
					t.Fatal("replacement physical stop not proven", err)
				}
			}
			next := submitIdleFixture(t, s, w, "second", d.Session.ID)
			fresh := s.currentRuntime(d.Session.ID)
			if fresh.Done() == old.Done() {
				t.Fatal("fixture did not replace runtime")
			}

			state, err := s.store.RuntimeGCIdle(d.Session.ID)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := s.store.Session(d.Session.ID, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("route=%s oldStopped=%v freshState=%s oldWorks=%+v GC=%+v", route, old.Stopped(), next.Session.Status, detail.Works, state)
			if !state.Idle {
				t.Fatal("successfully retired unknown work still pins successor GC")
			}
		})
	}
}

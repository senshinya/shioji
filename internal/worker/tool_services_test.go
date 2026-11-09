package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"sync/atomic"
	"testing"
	"time"
)

type serviceHarness struct {
	memoryHarness
	starts  *atomic.Int32
	finish  chan struct{}
	onBegin func()
	waits   *atomic.Int32
}

func (a serviceHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, e := a.memoryHarness.Start(ctx, o)
	if e != nil {
		return nil, e
	}
	return &serviceRuntime{Runtime: r, owner: a}, nil
}

type serviceRuntime struct {
	harness.Runtime
	owner serviceHarness
}

func (r *serviceRuntime) ToolServices(context.Context) (domain.ToolServiceCatalog, error) {
	return domain.ToolServiceCatalog{Services: []domain.ToolService{{ID: "opaque/service", Name: "Private", CanAuthorize: true, State: domain.ToolServiceAuthorizationRequired}}, CanRefresh: true}, nil
}
func (r *serviceRuntime) RefreshToolServices(context.Context) error { return nil }
func (r *serviceRuntime) BeginToolServiceAuthorization(context.Context, string) (harness.ToolServiceAuthorization, domain.SignInChallenge, error) {
	r.owner.starts.Add(1)
	if r.owner.onBegin != nil {
		r.owner.onBegin()
	}
	return serviceLogin{r.owner.finish, r.owner.waits}, domain.SignInChallenge{URL: "https://example.test/authorize"}, nil
}

type serviceLogin struct {
	finish chan struct{}
	waits  *atomic.Int32
}

func (l serviceLogin) Wait(ctx context.Context) error {
	if l.waits != nil {
		l.waits.Add(1)
	}
	select {
	case <-l.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestWorkerToolAuthorizationIsOwnedAndDoesNotReplay(t *testing.T) {
	a := serviceHarness{memoryHarness: memoryHarness{id: "custom"}, starts: &atomic.Int32{}, finish: make(chan struct{})}
	s, _ := memoryWorker(t, a)
	in := protocol.ToolServiceInput{HarnessID: "custom", ServiceID: "opaque/service", ID: "auth-tool"}
	for i := 0; i < 2; i++ {
		if _, err := s.toolAuthorizationCommand("runtime.tools.auth.start", in); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := s.toolAuthorizationCommand("runtime.tools.auth.status", in)
		if status.Phase == domain.SignInWaiting {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if a.starts.Load() != 1 {
		t.Fatal("authorization replayed", a.starts.Load())
	}
	unknown := in
	unknown.ID = "unknown"
	got, _ := s.toolAuthorizationCommand("runtime.tools.auth.status", unknown)
	if got.Phase != domain.SignInExpired || a.starts.Load() != 1 {
		t.Fatal("unknown query restarted authorization")
	}
	if !s.hasActiveSignIn() {
		t.Fatal("service authorization does not block worker upgrade")
	}
	close(a.finish)
	for time.Now().Before(deadline) {
		got, _ = s.toolAuthorizationCommand("runtime.tools.auth.status", in)
		if got.Phase == domain.SignInCompleted {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if got.Phase != domain.SignInCompleted || got.Challenge.URL != "" {
		t.Fatal("authorization completion not projected", got)
	}
}

func TestWorkerReleasesToolAuthorizationWhenCancelledDuringStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits := &atomic.Int32{}
	a := serviceHarness{memoryHarness: memoryHarness{id: "custom"}, starts: &atomic.Int32{}, finish: make(chan struct{}), onBegin: cancel, waits: waits}
	s, _ := memoryWorker(t, a)
	in := protocol.ToolServiceInput{HarnessID: "custom", ServiceID: "opaque/service", ID: "cancel-at-start"}
	entry := &workerToolAuthorization{status: domain.ToolServiceAuthorization{ID: in.ID, HarnessID: in.HarnessID, ServiceID: in.ServiceID, Phase: domain.SignInStarting}, cancel: cancel}
	s.wg.Add(1)
	s.runToolAuthorization(ctx, in, entry)
	if waits.Load() != 1 {
		t.Fatal("acquired native authorization was not released after cancellation")
	}
	if entry.status.Phase != domain.SignInExpired || entry.status.Challenge.URL != "" {
		t.Fatal("cancelled start is not expired", entry.status)
	}
}

type editableServiceHarness struct {
	serviceHarness
	writes *atomic.Int32
}

func (a editableServiceHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := a.serviceHarness.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &editableServiceRuntime{serviceRuntime: r.(*serviceRuntime), writes: a.writes}, nil
}

type editableServiceRuntime struct {
	*serviceRuntime
	writes *atomic.Int32
}

func (r *editableServiceRuntime) ChangeToolService(_ context.Context, in domain.ToolServiceChange) (domain.ToolServiceChangeResult, error) {
	r.writes.Add(1)
	return domain.ToolServiceChangeResult{Saved: true, ServiceID: in.ServiceID}, nil
}
func TestWorkerToolConfigurationCannotRaceAnAuthorization(t *testing.T) {
	writes := &atomic.Int32{}
	a := editableServiceHarness{serviceHarness: serviceHarness{memoryHarness: memoryHarness{id: "custom"}, starts: &atomic.Int32{}, finish: make(chan struct{})}, writes: writes}
	s, _ := memoryWorker(t, a)
	in := protocol.ToolServiceInput{HarnessID: "custom", Change: &domain.ToolServiceChange{Action: "setEnabled", ServiceID: "opaque/service", Revision: "one"}}
	s.toolAuth = map[string]*workerToolAuthorization{"custom": {status: domain.ToolServiceAuthorization{Phase: domain.SignInWaiting}, cancel: func() {}}}
	if _, err := s.changeToolService(in); err == nil || writes.Load() != 0 {
		t.Fatal("configuration modified an authorizing service")
	}
	s.toolAuth["custom"].status.Phase = domain.SignInCompleted
	result, err := s.changeToolService(in)
	if err != nil || !result.Saved || result.ServiceID != "opaque/service" || writes.Load() != 1 {
		t.Fatal("owning harness did not receive the explicit mutation", result, err)
	}
}

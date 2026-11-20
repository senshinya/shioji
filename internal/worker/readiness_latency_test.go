package worker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

type installationProbeGate struct {
	blocked atomic.Bool
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type slowInstallationHarness struct {
	memoryHarness
	executable string
	probe      *installationProbeGate
}

func (a slowInstallationHarness) Installation() domain.RuntimeInstallation {
	a.probe.calls.Add(1)
	if a.probe.blocked.Load() {
		select {
		case a.probe.entered <- struct{}{}:
		default:
		}
		<-a.probe.release
	}
	return domain.RuntimeInstallation{Executable: a.executable, Configurable: true}
}

func (a slowInstallationHarness) WithExecutable(path string) (harness.Adapter, error) {
	a.executable = path
	return a, nil
}

func TestReadinessDoesNotWaitForInstallationProbe(t *testing.T) {
	probe := &installationProbeGate{entered: make(chan struct{}, 1), release: make(chan struct{})}
	release := func() { probe.once.Do(func() { close(probe.release) }) }
	defer release()
	s, _ := memoryWorker(t, slowInstallationHarness{memoryHarness: memoryHarness{id: "slow-installation"}, executable: "/usr/bin/false", probe: probe})
	if calls := probe.calls.Load(); calls != 1 {
		t.Fatalf("initial catalog repeated installation discovery %d times", calls)
	}
	probe.blocked.Store(true)
	done := make(chan domain.Readiness, 1)
	go func() { done <- s.runtimeReadiness() }()
	select {
	case ready := <-done:
		if ready.State != domain.RuntimeReady || len(ready.Harnesses) != 1 || ready.Harnesses[0].Installation == nil || ready.Harnesses[0].Installation.Executable != "/usr/bin/false" {
			t.Fatalf("readiness lost the confirmed installation: %+v", ready)
		}
	case <-time.After(time.Second):
		release()
		<-done
		t.Fatal("status polling started an installation probe")
	}

	// A configuration refresh may inspect the new installation, while status
	// readers continue to receive the previous snapshot and a refreshing state.
	configured := make(chan error, 1)
	go func() {
		_, err := s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "slow-installation", Executable: "/usr/bin/true"})
		configured <- err
	}()
	select {
	case <-probe.entered:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("configuration did not refresh installation metadata")
	}
	go func() { done <- s.runtimeReadiness() }()
	select {
	case ready := <-done:
		if ready.State != domain.RuntimeRefreshing {
			t.Fatalf("configuration refresh was not visible: %+v", ready)
		}
	case <-time.After(time.Second):
		release()
		<-done
		t.Fatal("status polling waited for the configuration probe")
	}
	release()
	if err := <-configured; err != nil {
		t.Fatal(err)
	}
	ready := s.runtimeReadiness()
	if ready.State != domain.RuntimeReady || ready.Harnesses[0].Installation.Executable != "/usr/bin/true" {
		t.Fatalf("configuration retained stale installation metadata: %+v", ready)
	}
}

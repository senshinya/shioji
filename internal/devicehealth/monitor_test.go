package devicehealth

import (
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestCPUReportsIntervalActivityAndCounterWrap(t *testing.T) {
	for _, tc := range []struct {
		before, after [4]uint32
		want          float64
		known         bool
	}{
		{[4]uint32{100, 30, 500, 20}, [4]uint32{120, 40, 570, 20}, 30, true},
		{[4]uint32{1, 2, 3, 4}, [4]uint32{1, 2, 3, 4}, 0, false},
		{[4]uint32{0xfffffffe, 0, 10, 0}, [4]uint32{2, 0, 16, 0}, 40, true},
	} {
		got := cpuPercent(tc.before, tc.after)
		if (got != nil) != tc.known || got != nil && *got != tc.want {
			t.Fatalf("cpu = %v, want %v (known %v)", got, tc.want, tc.known)
		}
	}
}

func TestHistoryIsBoundedPreservesUnknownAndRealZero(t *testing.T) {
	zero := domain.DeviceMemory{UsedBytes: 0, TotalBytes: 16000}
	m := newMonitor(t.TempDir(), func() reading { return reading{Memory: &zero} }, func() (func() error, error) { return func() error { return nil }, nil })
	defer m.Close()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		m.sample(now.Add(time.Duration(i) * 2 * time.Second))
	}
	h := m.Snapshot()
	if len(h.Samples) != 31 {
		t.Fatalf("history length = %d", len(h.Samples))
	}
	if h.Samples[0].At != now.Add(138*time.Second) {
		t.Fatal("history did not keep the last minute")
	}
	if h.Samples[0].CPU != nil || h.Samples[0].Memory == nil || h.Samples[0].Memory.UsedBytes != 0 {
		t.Fatal("unknown and zero were conflated")
	}
	h.Samples[0].Memory = nil
	if m.Snapshot().Samples[0].Memory == nil {
		t.Fatal("caller mutated monitor history")
	}
}

func TestKeepAwakeIsDeviceOwnedIdempotentAndRestored(t *testing.T) {
	dir := t.TempDir()
	acquired, released := 0, 0
	acquire := func() (func() error, error) { acquired++; return func() error { released++; return nil }, nil }
	m := newMonitor(dir, func() reading { return reading{} }, acquire)
	state, err := m.SetKeepAwake(true, 0)
	if err != nil || !state.KeepAwake.Enabled || state.KeepAwake.Revision != 1 {
		t.Fatal(state, err)
	}
	if _, err = m.SetKeepAwake(true, 1); err != nil || acquired != 1 {
		t.Fatal("duplicate enable acquired another assertion", err, acquired)
	}
	if _, err = m.SetKeepAwake(false, 0); err == nil {
		t.Fatal("stale client changed device state")
	}
	if err = m.Close(); err != nil || released != 1 {
		t.Fatal("close did not release owned assertion", err, released)
	}
	m = newMonitor(dir, func() reading { return reading{} }, acquire)
	defer m.Close()
	if !m.Snapshot().KeepAwake.Enabled || acquired != 2 {
		t.Fatal("worker restart lost the explicit device setting")
	}
	state, err = m.SetKeepAwake(false, 1)
	if err != nil || state.KeepAwake.Enabled || state.KeepAwake.Revision != 2 || released != 2 {
		t.Fatal(state, err, released)
	}
}

func TestFailedKeepAwakeAcquisitionDoesNotClaimEnabled(t *testing.T) {
	m := newMonitor(t.TempDir(), func() reading { return reading{} }, func() (func() error, error) { return nil, errors.New("power unavailable") })
	defer m.Close()
	if _, err := m.SetKeepAwake(true, 0); err == nil {
		t.Fatal("missing error")
	}
	if s := m.Snapshot(); s.KeepAwake.Enabled || s.KeepAwake.Revision != 0 {
		t.Fatal("failed operation changed state", s)
	}
}

func TestMemoryUsesAnonymousWiredAndPhysicalCompressorPages(t *testing.T) {
	got := memoryUsage(4096000, 4096, 400, 20, 100, 80)
	if got == nil || got.UsedBytes != 2293760 || got.TotalBytes != 4096000 {
		t.Fatal("memory includes reclaimable cache or double-counts compression", got)
	}
	if memoryUsage(0, 4096, 400, 20, 100, 80) != nil || memoryUsage(4096000, 4096, 1, 20, 100, 80) != nil {
		t.Fatal("invalid counters reported as a real measurement")
	}
}

func TestPowerSaveFailureReleasesNewAssertionAndKeepsPreviousState(t *testing.T) {
	dir := t.TempDir()
	released := 0
	m := newMonitor(dir, func() reading { return reading{} }, func() (func() error, error) { return func() error { released++; return nil }, nil })
	defer m.Close()
	if err := os.Mkdir(filepath.Join(dir, "keep-awake.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetKeepAwake(true, 0); err == nil {
		t.Fatal("save failure hidden")
	}
	if m.Snapshot().KeepAwake.Enabled || m.Snapshot().KeepAwake.Revision != 0 || released != 1 {
		t.Fatal("failed save leaked assertion or claimed enabled")
	}
}

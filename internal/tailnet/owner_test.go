package tailnet

import (
	"context"
	"testing"
	"time"
)

// A second owner must not be able to pause, enroll or overwrite an identity
// while another process is using its directory.
func TestSecondNodeOwnerIsRejectedUntilFirstStops(t *testing.T) {
	dir := t.TempDir()
	if err := writeIntent(dir, nodeIntent{Mode: "paused", Hostname: "owner"}); err != nil {
		t.Fatal(err)
	}
	start := func() (context.CancelFunc, <-chan struct{}, <-chan Snapshot) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		updates := make(chan Snapshot, 8)
		n := New(dir, func() ([]byte, error) { t.Error("paused identity accessed credentials"); return nil, nil })
		go func() { defer close(done); n.Run(ctx, func(s Snapshot) { updates <- s }) }()
		t.Cleanup(func() { cancel(); <-done })
		return cancel, done, updates
	}
	wait := func(updates <-chan Snapshot) Snapshot {
		t.Helper()
		select {
		case s := <-updates:
			return s
		case <-time.After(time.Second):
			t.Fatal("owner did not report status")
			return Snapshot{}
		}
	}
	stop, done, first := start()
	if s := wait(first); s.State != "Stopped" || s.Error != "" {
		t.Fatal("first owner failed", s)
	}
	_, secondDone, second := start()
	if s := wait(second); s.Error == "" {
		t.Fatal("second node acquired the same identity", s)
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("rejected owner kept running")
	}
	stop()
	<-done
	_, _, third := start()
	if s := wait(third); s.State != "Stopped" || s.Error != "" {
		t.Fatal("identity lock was not released", s)
	}
}

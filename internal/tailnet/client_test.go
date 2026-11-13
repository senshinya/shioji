package tailnet

import (
	"context"
	"testing"
	"time"
)

func TestClientDoesNotRepeatNativeAuthorizationWithoutUserAction(t *testing.T) {
	dir := t.TempDir()
	if err := writeIntent(dir, nodeIntent{Mode: "connected", Hostname: "saved"}); err != nil {
		t.Fatal(err)
	}
	attempts := make(chan bool, 8)
	c := NewClient(dir, func(_ context.Context, explicit bool) error { attempts <- explicit; return ErrUserAction }, func(int) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	updates := make(chan Snapshot, 16)
	go func() { defer close(done); c.Run(ctx, func(s Snapshot) { updates <- s }) }()
	defer func() { cancel(); <-done }()
	select {
	case explicit := <-attempts:
		if explicit {
			t.Fatal("restore opened an interactive prompt")
		}
	case <-time.After(time.Second):
		t.Fatal("restore not attempted")
	}
	// Allow another reconnect cycle. It may query the socket, but must not
	// invoke the native authorization/start boundary again on its own.
	<-updates
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("client status updates stalled")
	}
	select {
	case <-attempts:
		t.Fatal("blocked Keychain access was automatically retried")
	default:
	}
	c.Do(Action{Kind: "resume", ID: "explicit-retry"})
	select {
	case explicit := <-attempts:
		if !explicit {
			t.Fatal("manual retry was treated as automatic restore")
		}
	case <-time.After(time.Second):
		t.Fatal("manual retry ignored")
	}
}

func TestUnavailableHostDoesNotRegisterForPauseLogoutOrSettings(t *testing.T) {
	for _, kind := range []string{"pause", "logout", "incoming-off"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeIntent(dir, nodeIntent{Mode: "paused"}); err != nil {
				t.Fatal(err)
			}
			called := make(chan bool, 4)
			c := NewClient(dir, func(_ context.Context, explicit bool) error { called <- explicit; return ErrUserAction }, func(int) error { return nil })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Do(Action{Kind: kind, ID: "not-a-connection"})
			updated := make(chan Snapshot, 4)
			done := make(chan struct{})
			go func() { defer close(done); c.Run(ctx, func(s Snapshot) { updated <- s }) }()
			defer func() { cancel(); <-done }()
			select {
			case s := <-updated:
				if s.Error == "" || s.OperationID != "not-a-connection" {
					t.Fatal("unavailable-host operation was silently accepted", s)
				}
			case <-time.After(time.Second):
				t.Fatal("operation stalled")
			}
			select {
			case <-called:
				t.Fatal("non-connection action attempted service registration/authorization")
			default:
			}
		})
	}
}

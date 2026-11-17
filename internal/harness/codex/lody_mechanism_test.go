package codex

import (
	"bufio"
	"context"
	"os/exec"
	"shioji/internal/harness/codex/wire"
	"syscall"
	"testing"
	"time"
)

type lodyMechanismNativeAcceptedPipe struct{ sent chan struct{} }

func (p *lodyMechanismNativeAcceptedPipe) Write(b []byte) (int, error) {
	p.sent <- struct{}{}
	return len(b), nil
}
func (p *lodyMechanismNativeAcceptedPipe) Close() error { return nil }
func TestLodyMechanismNativePendingMustSurviveLocalCancellation(t *testing.T) {
	sent := make(chan struct{}, 1)
	native := &Runtime{pending: map[string]chan wire.Message{}, done: make(chan struct{}), stdin: &lodyMechanismNativeAcceptedPipe{sent: sent}}
	defer close(native.done)
	adapter := &adapterRuntime{Runtime: native}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	settled := make(chan error, 1)
	go func() {
		settled <- native.call(ctx, "turn/steer", map[string]string{"threadId": "thread", "turnId": "turn"}, nil)
	}()
	<-sent
	if adapter.PendingOperations() != 1 {
		t.Fatal("fixture did not own the native request")
	}
	cancel()
	err := <-settled
	if adapter.PendingOperations() != 1 {
		t.Fatalf("native request has no reply, but local cancellation removed pending ownership: pending=%d err=%v", adapter.PendingOperations(), err)
	}
}

func TestLodyMechanismCloseMustRespectCancelledBudget(t *testing.T) {
	native := &Runtime{pending: map[string]chan wire.Message{}, done: make(chan struct{}), stdin: &lodyMechanismNativeAcceptedPipe{sent: make(chan struct{}, 1)}}
	adapter := &adapterRuntime{Runtime: native}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closed := make(chan error, 1)
	go func() { closed <- adapter.Close(ctx) }()
	overran := false
	select {
	case <-closed:
	case <-time.After(250 * time.Millisecond):
		overran = true
	}
	close(native.done)
	if overran {
		<-closed
		t.Fatal("adapter Close ignored an already-cancelled deadline while waiting in Runtime.Close")
	}
}

func TestNativeCloseReservesKillObservationWithinShortBudget(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; echo ready; while :; do /bin/sleep 60; done")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-done })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal(line, err)
	}
	r := &Runtime{cmd: cmd, stdin: stdin, done: done}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err = r.close(ctx); err != nil {
		t.Fatal("close exhausted its budget before attempting KILL and observing exit", err)
	}
}

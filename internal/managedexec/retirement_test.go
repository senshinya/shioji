package managedexec

import (
	"bufio"
	"context"
	"os"
	"testing"
	"time"
)

// A registered resident tool is a retirement target even though it is not
// eligible for the old no-descendants idle fence. Force must skip TERM grace.
func TestForceRetirementKillsRegisteredResidentGroup(t *testing.T) {
	m, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Begin("resident")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd, stdin, stdout, err := s.StartOwned(ctx, "resident", []string{"/bin/sh", "-c", "trap '' TERM; sleep 60 & echo ready; wait"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	defer s.Stop(context.Background())
	if _, err = bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FenceIfIdle(); err == nil {
		t.Fatal("fixture has no resident descendants")
	}
	retire, ok := any(s).(interface {
		Terminate(context.Context, bool) error
	})
	if !ok {
		t.Fatal("registered scopes have no force retirement")
	}
	start := time.Now()
	if err = retire.Terminate(ctx, true); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("force retirement waited normal grace")
	}
	if err = s.VerifyStopped(); err != nil {
		t.Fatal("registered group survived", err)
	}
	if err = retire.Terminate(ctx, true); err != nil {
		t.Fatal("idempotent retry", err)
	}
}

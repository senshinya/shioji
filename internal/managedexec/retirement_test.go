package managedexec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"syscall"
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

// A title producer's cancellation can stop its group while upgrade retirement
// is observing or signalling it through a recovered manager. A transient kernel
// error must not veto a drain once this exact fenced generation is proven gone.
func TestForceRetirementDuringProducerCancellation(t *testing.T) {
	m, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := New(m.Directory, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, err := m.Begin(fmt.Sprint(i))
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Bind("worker", os.Getpid()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd, in, out, err := s.StartOwned(ctx, "title", []string{"/bin/sh", "-c", "echo ready; cat"}, []string{"PATH=/usr/bin:/bin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cmd.Wait()
			defer in.Close()
			defer s.Stop(context.Background())
			if _, err = bufio.NewReader(out).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			prior, err := recovered.Existing(s.Session)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				in.Close()
				_ = s.Stop(ctx)
				_ = m.Reap(ctx)
			}()
			err = prior.Terminate(ctx, true)
			<-done
			if err != nil {
				t.Fatal("concurrent cancellation prevented retirement", err, "final observation:", prior.VerifyStopped())
			}
			if err = prior.VerifyStopped(); err != nil {
				t.Fatal("retirement left a registered group", err)
			}
		})
	}
}

func TestForceRetirementRetainsUnverifiableLiveGroup(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			m, err := New(t.TempDir(), os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			s, err := m.Begin("unverifiable")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Bind("worker", os.Getpid()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cmd, in, _, err := s.StartOwned(ctx, "live", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cmd.Wait()
			defer in.Close()
			records, err := s.tickets()
			if err != nil || len(records) != 1 {
				t.Fatal("missing fixture registration", err, records)
			}
			ticket := records[0]
			defer func() { _ = writeJSON(s.launchPath(ticket.ID), ticket); _ = s.Stop(context.Background()) }()
			if missing {
				err = os.Remove(s.launchPath(ticket.ID))
			} else {
				forged := ticket
				forged.Anchor.Usec++
				err = writeJSON(s.launchPath(ticket.ID), forged)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Terminate(ctx, true); err == nil {
				t.Fatal("unverifiable group reported drained")
			}
			if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatal("unverified live group was signalled", err)
			}
			if err = s.VerifyStopped(); err == nil {
				t.Fatal("unverified group proved stopped")
			}
		})
	}
}

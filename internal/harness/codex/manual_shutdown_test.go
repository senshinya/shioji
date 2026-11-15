package codex

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/managedexec"
)

func manualShutdownFixture() bool {
	if len(os.Args) != 3 || os.Args[1] != "--manual-shutdown-fixture" {
		return false
	}
	signal.Ignore(syscall.SIGTERM)
	fmt.Println("ready")
	kind := os.Args[2]
	if kind != "runtime-close" && kind != "executor-close" {
		bufio.NewReader(os.Stdin).ReadString('\n')
		if kind == "runtime-malformed" {
			fmt.Println("malformed")
		}
		os.Stdout.Close()
	}
	for {
		time.Sleep(time.Hour)
	}
}

// EOF, parse failures and adapter destructors used to kill independently of
// Scope.Stop. A real process that ignores TERM must survive every one of them.
func TestManualShutdownKeepsImplicitKillPathsBehindConsent(t *testing.T) {
	for _, kind := range []string{"runtime-eof", "runtime-malformed", "executor-eof", "runtime-close", "executor-close"} {
		t.Run(kind, func(t *testing.T) {
			m, err := managedexec.New(t.TempDir(), os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			scope, err := m.Begin("manual")
			if err != nil {
				t.Fatal(err)
			}
			if err = scope.Bind("worker", os.Getpid()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd, stdin, stdout, err := scope.StartOwned(ctx, "runtime", []string{os.Args[0], "--manual-shutdown-fixture", kind}, []string{"PATH=/usr/bin:/bin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(stdout)
			if _, err = reader.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			observation, err := scope.InspectMaintenance()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = scope.BeginManualStop("operation", observation.Ref); err != nil {
				t.Fatal(err)
			}
			var done <-chan struct{}
			defer func() {
				scope.ForceManualStop("operation", "cleanup")
				scope.Stop(context.Background())
				if done != nil {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("adapter did not release process after force")
					}
				}
				stdin.Close()
			}()
			if kind == "executor-eof" || kind == "executor-close" {
				eCtx, eCancel := context.WithCancel(context.Background())
				defer eCancel()
				e := &Executor{cmd: cmd, stdin: stdin, scope: scope, ctx: eCtx, cancel: eCancel, done: make(chan struct{}), boots: map[string]*executorBootstrap{}}
				done = e.done
				go e.read(reader)
				if kind == "executor-close" {
					if err = e.Close(ctx); err == nil {
						t.Fatal("unfinished normal executor close reported success")
					}
				} else {
					io.WriteString(stdin, "go\n")
				}
			} else {
				r := &Runtime{cmd: cmd, stdin: stdin, scope: scope, done: make(chan struct{}), normalizer: NewNormalizer()}
				done = r.done
				go r.read(reader)
				if kind == "runtime-close" {
					if err = r.Close(); err == nil {
						t.Fatal("unfinished normal runtime close reported success")
					}
				} else {
					fmt.Fprintln(stdin, "go")
				}
			}
			select {
			case <-done:
				t.Fatal("adapter killed process without force authorization")
			case <-time.After(150 * time.Millisecond):
			}
			if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
				t.Fatal("normal shutdown killed TERM-ignoring process", err)
			}
		})
	}
}

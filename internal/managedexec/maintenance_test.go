package managedexec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func manualStopFixture() bool {
	if len(os.Args) != 2 || os.Args[1] != "--manual-stop-fixture" {
		return false
	}
	signal.Ignore(syscall.SIGTERM)
	fmt.Println("ready")
	for {
		time.Sleep(time.Hour)
	}
}

func TestManualNormalStopCannotEscalateAfterReopen(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	scope, _ := m.Begin("manual")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, stdout, err := scope.StartOwned(ctx, "runtime", []string{os.Args[0], "--manual-stop-fixture"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	defer func() { scope.ForceManualStop("operation", "test-cleanup"); scope.Stop(context.Background()) }()
	if _, err = bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	before, err := scope.InspectMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	undo, err := scope.BeginManualStop("operation", before.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if undo == nil {
		t.Fatal("missing admission rollback")
	}
	if scope.ForceAllowed() {
		t.Fatal("normal stop granted force")
	}
	if err = scope.AcceptManualStop("operation"); err != nil {
		t.Fatal(err)
	}
	if err = scope.Stop(ctx); err == nil {
		t.Fatal("TERM-ignoring process reported stopped")
	}
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("normal stop killed running work", err)
	}
	recovered, _ := New(m.Directory, os.Args[0])
	prior, err := recovered.Existing("manual")
	if err != nil {
		t.Fatal(err)
	}
	if prior.ForceAllowed() {
		t.Fatal("worker restart lost no-force authorization")
	}
	if err = prior.Stop(ctx); err == nil {
		t.Fatal("recovery pretended process stopped")
	}
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("recovery escalated without consent", err)
	}
	if err = prior.ForceManualStop("another-operation", "wrong"); err == nil {
		t.Fatal("unrelated operation granted force")
	}
	if err = prior.ForceManualStop("operation", "force-action"); err != nil {
		t.Fatal(err)
	}
	if !prior.ForceAllowed() {
		t.Fatal("explicit authorization not recorded")
	}
	if err = prior.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = prior.VerifyStopped(); err != nil {
		t.Fatal("forced lifetime still live", err)
	}
}

func TestManualPreviewChangedAndRollbackNeverSignal(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	scope, _ := m.Begin("rollback")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := scope.StartOwned(ctx, "runtime", []string{"/bin/sleep", "60"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer scope.Stop(context.Background())
	defer stdin.Close()
	if _, err = scope.BeginManualStop("operation", "stale-process-ref"); err == nil {
		t.Fatal("stale topology accepted")
	}
	before, _ := scope.InspectMaintenance()
	undo, err := scope.BeginManualStop("operation", before.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Admitting() {
		t.Fatal("new commands remained admissible")
	}
	if err = undo(); err != nil || !scope.Admitting() || !scope.ForceAllowed() {
		t.Fatal("rejected selection could not restore original lifetime", err)
	}
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("preview or rollback sent a signal", err)
	}
}

package managedexec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func detachedFixture() bool {
	if len(os.Args) != 2 || os.Args[1] != "--detached-fixture" {
		return false
	}
	child := exec.Command(os.Args[0], "--manual-stop-fixture")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		os.Exit(1)
	}
	fmt.Println(child.Process.Pid)
	bufio.NewReader(os.Stdin).ReadString('\n')
	return true
}

func explicitFixture(t *testing.T) (*Scope, ProcessInfo, *exec.Cmd) {
	t.Helper()
	m, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Begin("explicit")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, stdout, err := s.StartOwned(ctx, "detached", []string{os.Args[0], "--detached-fixture"}, BootstrapEnv(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err = fmt.Fscanln(bufio.NewReader(stdout), &pid); err != nil {
		t.Fatal(err)
	}
	list, err := Processes()
	if err != nil {
		t.Fatal(err)
	}
	var child ProcessInfo
	for _, p := range list {
		if p.Identity.PID == pid {
			child = p
		}
	}
	if child.Identity.PID == 0 {
		t.Fatal("missing detached child")
	}
	t.Cleanup(func() { SignalExact(child, unix.SIGKILL); stdin.Close(); s.Stop(context.Background()); cmd.Wait() })
	return s, child, cmd
}

func TestExplicitDetachedPreviewAndClosureAfterReparent(t *testing.T) {
	s, child, cmd := explicitFixture(t)
	ordinary, err := s.InspectMaintenance()
	if err == nil || ordinary.Ref != "" {
		t.Fatal("ordinary inspection authorized detached process", ordinary, err)
	}
	ordinaryFound := false
	for _, p := range ordinary.Processes {
		ordinaryFound = ordinaryFound || p.Identity == child.Identity
	}
	if !ordinaryFound {
		t.Fatal("partial diagnostic omitted the blocking child", ordinary)
	}
	observation, err := s.InspectExplicitMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range observation.Processes {
		found = found || p.Identity == child.Identity && p.Evidence == "observed"
	}
	if !found {
		t.Fatal("explicit preview lost detached process", observation)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	// Durable observations survive parent loss and a new manager instance.
	m, _ := New(s.manager.Directory, os.Args[0])
	recovered, _ := m.Existing(s.Session)
	observation, err = recovered.InspectExplicitMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	undo, err := recovered.BeginExplicitStop("operation", observation.Ref, true)
	if err != nil || undo == nil {
		t.Fatal(err)
	}
	if err = recovered.AcceptManualStop("operation"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = recovered.StopExplicit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = SignalExact(child, 0); err == nil {
		t.Fatal("confirmed detached process survived force")
	}
}

func TestExplicitStaleIdentityAndControlProcessNeverSignalled(t *testing.T) {
	_, child, _ := explicitFixture(t)
	stale := child
	stale.Identity.Usec++
	if err := SignalExact(stale, unix.SIGKILL); err == nil {
		t.Fatal("stale identity accepted")
	}
	if err := SignalExact(child, 0); err != nil {
		t.Fatal("stale preview killed original", err)
	}
	list, err := Processes()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Identity.PID == os.Getpid() && SignalExact(p, unix.SIGKILL) == nil {
			t.Fatal("recovery controller could kill itself")
		}
	}
}

func TestExplicitPreviewDoesNotGrantImplicitForce(t *testing.T) {
	s, child, _ := explicitFixture(t)
	observation, err := s.InspectExplicitMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	undo, err := s.BeginExplicitStop("normal", observation.Ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.ForceAllowed() {
		t.Fatal("normal shutdown granted implicit kill")
	}
	if err = undo(); err != nil || !s.Admitting() {
		t.Fatal("preview rejection fenced lifetime", err)
	}
	if err = SignalExact(child, 0); err != nil {
		t.Fatal("preview killed child", err)
	}
}

func TestDeviceExitProofCannotReleaseANewGeneration(t *testing.T) {
	manager, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	old, err := manager.Begin("device-proof")
	if err != nil {
		t.Fatal(err)
	}
	if err = old.ConfirmExplicitExit("helper-operation"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := manager.Existing(old.Session)
	if err != nil {
		t.Fatal(err)
	}
	if proven, e := reloaded.ExplicitStopProven(); !proven || e != nil {
		t.Fatal("device proof not durable", proven, e)
	}
	next, err := manager.Begin(old.Session)
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation == old.Generation {
		t.Fatal("new generation not created")
	}
	if proven, e := next.ExplicitStopProven(); proven || e != nil {
		t.Fatal("old helper proof released a new execution", proven, e)
	}
}

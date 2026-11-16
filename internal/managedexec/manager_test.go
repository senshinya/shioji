package managedexec

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if detachedFixture() { return }
	if manualStopFixture() {
		return
	}
	if handled, err := ChildMain(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRegisteredGroupIsDurableAndStopsAfterLeaderExit(t *testing.T) {
	manager, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Begin("session")
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Bind("executor", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ticket, err := scope.Prepare("executor", "tool-call")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "--managed-exec", ticket.ID)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer scope.Stop(context.Background())
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var packet Registration
	if err = json.Unmarshal([]byte(strings.TrimPrefix(line, RegistrationPrefix)), &packet); err != nil {
		t.Fatal(err)
	}
	if err = scope.Register(packet); err != nil {
		t.Fatal(err)
	}
	// A second controller sees registration before the ACK can release user code.
	recovered, err := New(manager.Directory, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	prior, err := recovered.Existing("session")
	if err != nil {
		t.Fatal(err)
	}
	if prior == nil {
		t.Fatal("missing durable scope")
	}
	marker := filepath.Join(t.TempDir(), "child-pid")
	ack := Acknowledgement{ID: ticket.ID, Argv: []string{"/bin/sh", "-c", `sleep 60 </dev/null >/dev/null 2>&1 & printf '%s' $! > "$1"`, "test", marker}, Env: []string{"PATH=/usr/bin:/bin"}}
	if err = json.NewEncoder(stdin).Encode(ack); err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = prior.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = prior.VerifyStopped(); err != nil {
		t.Fatal(err)
	}
	if members, err := groupMembers(packet.Root); err != nil || len(members) != 0 {
		t.Fatal("durable stop left a live group", err, members)
	}
	if _, err = prior.Prepare("executor", "late"); err == nil {
		t.Fatal("fenced generation accepted a new launch")
	}
	if err = prior.Register(packet); err == nil {
		t.Fatal("fenced generation acknowledged a delayed registration")
	}
}

func TestRegistrationCannotAdoptTheControllingWorkerGroup(t *testing.T) {
	manager, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Begin("session")
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Bind("executor", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ticket, err := scope.Prepare("executor", "tool")
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Register(Registration{ID: ticket.ID, Root: os.Getpid(), Anchor: os.Getpid()}); err == nil {
		t.Fatal("worker group was adopted as a tool")
	}
}

func TestReapClosesOnlyFinishedCommandLifetime(t *testing.T) {
	m, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Begin("reap")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.Stop(ctx)
	cmd, stdin, stdout, err := s.StartOwned(ctx, "short", []string{"/bin/echo", "done"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	io.ReadAll(stdout)
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = m.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	records, err := s.tickets()
	if err != nil || len(records) != 0 {
		t.Fatal("finished anchor was retained", records, err)
	}
	if !s.Admitting() {
		t.Fatal("reaper fenced the active session")
	}
	if err = s.Cover("operation/with:arbitrary-id"); err != nil || !s.Covered("operation/with:arbitrary-id") {
		t.Fatal("operation identity was interpreted as a path", err)
	}
}

func TestMissingAcknowledgedManifestCannotProveCleanup(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("missing")
	s.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := s.StartOwned(ctx, "live", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	records, _ := s.tickets()
	if len(records) != 1 {
		t.Fatal(records)
	}
	ticket := records[0]
	defer func() { writeJSON(s.launchPath(ticket.ID), ticket); s.Stop(ctx) }()
	os.Remove(s.launchPath(ticket.ID))
	if err = s.Stop(ctx); err == nil {
		t.Fatal("lost acknowledged registration was treated as stopped")
	}
}

func TestReapDoesNotQuarantineReplacementGeneration(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	old, _ := m.Begin("replace")
	old.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := old.StartOwned(ctx, "old", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	if err = old.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := m.Begin("replace")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Stop(ctx)
	if err = m.Reap(ctx); err != nil {
		t.Fatal("retired prior generation contaminated its replacement", err)
	}
	if !next.Admitting() {
		t.Fatal("replacement was fenced")
	}
}

func TestKnownRegisteredIdentityOutsideGroupCannotProveStopped(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("moved")
	// An actual live process with a known start time outside the journaled group.
	// Synthetic prior group evidence cannot authorize signalling its new group.
	cmd := exec.Command("/bin/sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()
	info, err := unix.SysctlKinfoProc("kern.proc.pid", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	ticket := Ticket{ID: nonce(), State: "registered", Root: identity(*info), Anchor: identity(*info), Group: cmd.Process.Pid + 1}
	state, _ := s.load()
	state.Launches[ticket.ID] = true
	if err = writeJSON(s.launchPath(ticket.ID), ticket); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(filepath.Join(s.Directory, "scope.json"), state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Stop(ctx); err == nil {
		t.Fatal("known process migration was accepted as stopped")
	}
	if cmd.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("cleanup signalled the unowned destination group")
	}
}

func TestManagedScopeDoesNotContainUnregisteredDetachedDescendants(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("scope-boundary")
	s.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.Stop(ctx)
	marker := filepath.Join(t.TempDir(), "escaped.pid")
	code := `import subprocess,sys
p=subprocess.Popen(["/bin/sleep","60"],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
open(sys.argv[1],"w").write(str(p.pid))`
	cmd, stdin, stdout, err := s.StartOwned(ctx, "detach", []string{python, "-c", code, marker}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	io.ReadAll(stdout)
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	var pid int
	raw, _ := os.ReadFile(marker)
	fmt.Sscan(string(raw), &pid)
	escaped, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		current, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if e == nil && identity(*current) == identity(*escaped) {
			unix.Kill(pid, unix.SIGKILL)
		}
	}()
	if err = s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || current.Proc.P_stat == 5 {
		t.Fatal("fixture did not reproduce an unregistered escaped process", err)
	}
	if int(current.Eproc.Pgid) == cmd.Process.Pid {
		t.Fatal("child never detached")
	}
	// Stop/VerifyStopped certify only the journaled process-group lifetime. They
	// cannot serve as a general OS process-tree containment guarantee.
}

func TestEmptyTargetEnvironmentRemainsEmpty(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("empty-env")
	s.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.Stop(ctx)
	cmd, stdin, stdout, err := s.StartOwned(ctx, "env", []string{"/usr/bin/env"}, []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	raw, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal("empty environment was rejected", err)
	}
	if len(raw) != 0 {
		t.Fatal("bootstrap environment leaked into target", string(raw))
	}
}

func TestMissingScopeManifestCannotAdoptAReplacementGeneration(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("lost-scope")
	s.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := s.StartOwned(ctx, "live", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	state, _ := s.load()
	defer func() { writeJSON(filepath.Join(s.Directory, "scope.json"), state); s.Stop(ctx) }()
	if err = os.Remove(filepath.Join(s.Directory, "scope.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Existing(s.Session); err == nil {
		t.Fatal("lost scope manifest downgraded to legacy")
	}
	if _, err = m.Begin(s.Session); err == nil {
		t.Fatal("lost manifest admitted a replacement generation")
	}
	if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("refusal unexpectedly signalled old process", err)
	}
}

func TestReapRetiresExternallyTerminatedGroup(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("externally-ended")
	s.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.Stop(ctx)
	cmd, stdin, _, err := s.StartOwned(ctx, "exit", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err = unix.Kill(-cmd.Process.Pid, unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	for {
		members, e := groupMembers(cmd.Process.Pid)
		if e != nil {
			t.Fatal(e)
		}
		if len(members) == 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = m.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	records, err := s.tickets()
	if err != nil || len(records) != 0 {
		t.Fatal("ended group retained a live PID lifetime", records, err)
	}
}

func TestReapRejectsChangedAnchorIdentity(t *testing.T) {
	m, _ := New(t.TempDir(), os.Args[0])
	s, _ := m.Begin("changed-anchor")
	s.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.Stop(ctx)
	cmd, stdin, _, err := s.StartOwned(ctx, "live", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Stop(context.Background()); cmd.Wait() }()
	defer stdin.Close()
	records, _ := s.tickets()
	ticket := records[0]
	forged := ticket
	forged.Anchor.Usec++
	m.live[ticket.ID] = liveLaunch{s, forged}
	if err = m.Reap(ctx); err == nil {
		t.Fatal("changed anchor identity was accepted for retirement")
	}
	if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("reaper signalled an unverified identity", err)
	}
	m.live[ticket.ID] = liveLaunch{s, ticket}
}

func TestIdleFenceRejectsPendingLaunchAndPreservesItsAdmission(t *testing.T) {
	manager, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Begin("idle-update")
	if err != nil {
		t.Fatal(err)
	}
	scope.Bind("executor", os.Getpid())
	if _, err = scope.Prepare("executor", "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err = scope.FenceIfIdle(); err == nil || !scope.Admitting() {
		t.Fatal("pending launch was fenced", err)
	}
}

func TestIdleServiceFenceStillRejectsBusyCallsAndDescendants(t *testing.T) {
	for _, background := range []string{"none", "group", "detached"} {
		t.Run(fmt.Sprint("background-", background), func(t *testing.T) {
			manager, _ := New(t.TempDir(), os.Args[0])
			scope, _ := manager.Begin("service")
			if err := scope.Bind("executor", os.Getpid()); err != nil {
				t.Fatal(err)
			}
			ticket, err := scope.Prepare("executor", "opaque-service")
			if err != nil {
				t.Fatal(err)
			}
			if err = scope.SetServiceIdle(ticket.ID, true); err == nil {
				t.Fatal("unregistered service was trusted")
			}
			cmd := exec.Command(os.Args[0], "--managed-exec", ticket.ID)
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			stdin, _ := cmd.StdinPipe()
			stdout, _ := cmd.StdoutPipe()
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Wait()
			defer scope.Stop(context.Background())
			defer stdin.Close()
			reader := bufio.NewReader(stdout)
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			var registration Registration
			if err = json.Unmarshal([]byte(strings.TrimPrefix(line, RegistrationPrefix)), &registration); err != nil {
				t.Fatal(err)
			}
			if err = scope.Register(registration); err != nil {
				t.Fatal(err)
			}
			command := "echo ready; exec cat"
			if background == "group" {
				command = "sleep 60 & echo ready; wait"
			} else if background == "detached" {
				command = `/usr/bin/python3 -c 'import os,time; os.setpgid(0,0); print(os.getpid(),flush=True); time.sleep(60)' & wait`
			}
			ack := Acknowledgement{ID: ticket.ID, Argv: []string{"/bin/sh", "-c", command}, Env: []string{"PATH=/usr/bin:/bin"}, PipeStdin: true}
			if err = json.NewEncoder(stdin).Encode(ack); err != nil {
				t.Fatal(err)
			}
			ready, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if background == "detached" {
				var pid int
				if _, err = fmt.Sscan(ready, &pid); err != nil || pid <= 1 {
					t.Fatal("missing owned test descendant", ready, err)
				}
				defer unix.Kill(pid, unix.SIGKILL)
			}
			if _, err = scope.FenceIfIdle(); err == nil {
				t.Fatal("ordinary live command was treated as an idle service")
			}
			if err = scope.SetServiceIdle(ticket.ID, true); err != nil {
				t.Fatal(err)
			}
			undo, err := scope.FenceIfIdle()
			if background != "none" {
				if err == nil || !scope.Admitting() {
					t.Fatal("service exemption hid its background child", err)
				}
				return
			}
			if err != nil || undo == nil || scope.Admitting() {
				t.Fatal("quiet registered service prevented maintenance", err)
			}
			if err = scope.SetServiceIdle(ticket.ID, false); err == nil {
				t.Fatal("fenced service accepted new activity")
			}
			if err = undo(); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(scope.Directory, scope.Generation, "launches")
			if err = os.Chmod(journal, 0500); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(journal, 0700)
			if err = scope.SetServiceIdle(ticket.ID, false); err == nil {
				t.Fatal("fixture did not make the service journal unwritable")
			}
			if _, err = scope.FenceIfIdle(); err == nil || !scope.Admitting() {
				t.Fatal("stale on-disk idle evidence survived a failed busy write", err)
			}
			if err = os.Chmod(journal, 0700); err != nil {
				t.Fatal(err)
			}
			if err = scope.SetServiceIdle(ticket.ID, false); err != nil {
				t.Fatal(err)
			}
			if _, err = scope.FenceIfIdle(); err == nil || !scope.Admitting() {
				t.Fatal("outstanding service call did not block maintenance", err)
			}
		})
	}
}

func TestIdleFenceIsReversibleBeforeWorkerReplacement(t *testing.T) {
	manager, _ := New(t.TempDir(), os.Args[0])
	scope, _ := manager.Begin("idle-update")
	undo, err := scope.FenceIfIdle()
	if err != nil || undo == nil || scope.Admitting() {
		t.Fatal("idle generation not fenced", err)
	}
	if err = undo(); err != nil || !scope.Admitting() {
		t.Fatal("failed update could not restore admission", err)
	}
}

func TestIdleFenceDoesNotInterruptBackgroundDescendants(t *testing.T) {
	manager, _ := New(t.TempDir(), os.Args[0])
	scope, _ := manager.Begin("background-update")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, stdout, err := scope.StartOwned(ctx, "runtime", []string{"/bin/sh", "-c", "sleep 60 & echo ready; wait"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer scope.Stop(context.Background())
	defer stdin.Close()
	if _, err = bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err = scope.FenceIfIdle(); err == nil || !scope.Admitting() {
		t.Fatal("background process was treated as idle", err)
	}
	if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("idle check interrupted the process", err)
	}
}

func TestDeferredUpdateRestoresLiveMonitoringAfterReap(t *testing.T) {
	manager, _ := New(t.TempDir(), os.Args[0])
	scope, _ := manager.Begin("deferred-update")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := scope.StartOwned(ctx, "runtime", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { scope.Stop(context.Background()); cmd.Wait() }()
	defer stdin.Close()
	before := len(manager.live)
	undo, err := scope.FenceIfIdle()
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if err = undo(); err != nil {
		t.Fatal(err)
	}
	if before != 1 || len(manager.live) != before {
		t.Fatal("deferred update lost live ownership monitoring", before, len(manager.live))
	}
	// A later ownership failure must still be observable by the normal reaper.
	for id, launch := range manager.live {
		launch.ticket.Anchor.Usec++
		manager.live[id] = launch
	}
	if err = manager.Reap(ctx); err == nil {
		t.Fatal("restored scope no longer detects changed ownership")
	}
}

func TestDeferredUpdateCannotReopenAnExplicitlyStoppedGeneration(t *testing.T) {
	manager, _ := New(t.TempDir(), os.Args[0])
	scope, _ := manager.Begin("stopped-update")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := scope.StartOwned(ctx, "runtime", []string{"/bin/sleep", "60"}, []string{"PATH=/bin:/usr/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	undo, err := scope.FenceIfIdle()
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = undo(); err == nil || scope.Admitting() {
		t.Fatal("deferred update reopened a permanently stopped generation", err)
	}
}

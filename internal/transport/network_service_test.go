package transport

import (
	"context"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/tailnet"
	"syscall"
	"testing"
	"time"
)

func TestLegacyGUIProfileMatchingAndNewClientMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "client", "tailnet")
	old := WorkerProcessIdentity{PID: 42, UID: uint32(os.Getuid()), Sec: 10, Usec: 20, Executable: "/Applications/Shioji.app/Contents/MacOS/shioji", Arguments: []string{"shioji", "--client-state", filepath.Join(filepath.Dir(dir), "state.json")}}
	if !legacyNetworkOwner(old, dir) {
		t.Fatal("old GUI sharing the node profile was not recognized")
	}
	for _, args := range [][]string{{"shioji", "-client-state", filepath.Join(filepath.Dir(dir), "state.json")}, {"shioji", "-client-state=" + filepath.Join(filepath.Dir(dir), "state.json")}, {"shioji", "--client-state=" + filepath.Join(filepath.Dir(dir), "state.json")}} {
		variant := old
		variant.Arguments = args
		if !legacyNetworkOwner(variant, dir) {
			t.Fatal("valid legacy profile flags missed", args)
		}
	}
	other := old
	other.Arguments = []string{"shioji", "--client-state", filepath.Join(t.TempDir(), "state.json")}
	if legacyNetworkOwner(other, dir) {
		t.Fatal("unrelated preview was treated as node owner")
	}
	cleanup, err := registerNetworkClient(dir, old)
	if err != nil {
		t.Fatal(err)
	}
	if legacyNetworkOwner(old, dir) {
		t.Fatal("new client mistaken for old GUI node owner")
	}
	reused := old
	reused.Sec++
	if !legacyNetworkOwner(reused, dir) {
		t.Fatal("stale PID marker bypassed legacy-owner check")
	}
	cleanup()
	if !legacyNetworkOwner(old, dir) {
		t.Fatal("client marker was not removed")
	}
}

func TestIsolatedNetworkHostRetainsProcessAfterGUIDetaches(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "shioji-runtime")
	dir := filepath.Join(root, "node")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/shioji-runtime").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	cleanup, err := RegisterNetworkClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var servicePID int
	t.Cleanup(func() {
		if servicePID > 0 {
			if p, e := os.FindProcess(servicePID); e == nil {
				p.Signal(syscall.SIGTERM)
			}
		}
	})
	host := exec.Command(bin, "--network-serve", "--network-dir", dir)
	if err = host.Start(); err != nil {
		t.Fatal(err)
	}
	servicePID = host.Process.Pid
	go host.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(tailnet.ServiceSocket(dir)); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test host did not start")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := NewNetworkClient(dir, "", bin)
	updates := make(chan tailnet.Snapshot, 32)
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx, func(s tailnet.Snapshot) { updates <- s }) }()
	defer func() { cancel(); <-done }()
	if !c.Do(tailnet.Action{Kind: "incoming-off", ID: "disabled"}) {
		t.Fatal("unable to queue action")
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-updates:
			if s.OperationID == "disabled" && s.Error == "" {
				goto ready
			}
			if s.Error != "" {
				t.Fatal("network host startup failed", s.Error)
			}
		case <-timer.C:
			t.Fatal("network host did not accept action")
		}
	}
ready:
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Net: "unix", Name: tailnet.ServiceSocket(dir)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := conn.SyscallConn()
	var inner error
	raw.Control(func(fd uintptr) { servicePID, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
	conn.Close()
	if inner != nil || servicePID <= 1 || servicePID == os.Getpid() {
		t.Fatal("not an independent network process", servicePID, inner)
	}
	cancel()
	<-done
	if err = syscall.Kill(servicePID, syscall.Signal(0)); err != nil {
		t.Fatal("GUI shutdown terminated network process", err)
	}
	c2 := NewNetworkClient(dir, "", bin)
	ctx2, stop := context.WithCancel(context.Background())
	defer stop()
	next := make(chan tailnet.Snapshot, 8)
	done2 := make(chan struct{})
	go func() { defer close(done2); c2.Run(ctx2, func(s tailnet.Snapshot) { next <- s }) }()
	defer func() { stop(); <-done2 }()
	for {
		select {
		case s := <-next:
			if s.OperationID == "disabled" {
				if s.InboundEnabled {
					t.Fatal("preference lost across GUI restart")
				}
				return
			}
			if s.Error != "" {
				t.Fatal("retained host failed verification", s.Error)
			}
		case <-timer.C:
			t.Fatal("GUI did not reconnect to retained host")
		}
	}
}

func TestExplicitReconnectHandsOffToNewHostBuild(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "node")
	a := filepath.Join(root, "host-a")
	b := filepath.Join(root, "host-b")
	for _, v := range []struct{ path, version string }{{a, "1"}, {b, "2"}} {
		if out, err := exec.Command("go", "build", "-ldflags", "-X shioji/internal/runtimebuild.BuiltAt="+v.version, "-o", v.path, "../../cmd/shioji-runtime").CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	cleanup, err := RegisterNetworkClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	host := exec.Command(a, "--network-serve", "--network-dir", dir)
	if err = host.Start(); err != nil {
		t.Fatal(err)
	}
	go host.Wait()
	t.Cleanup(func() {
		processes, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
		for _, info := range processes {
			p, e := WorkerProcess(int(info.Proc.P_pid))
			if e == nil && (p.Executable == a || p.Executable == b) {
				syscall.Kill(p.PID, syscall.SIGTERM)
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(tailnet.ServiceSocket(dir)); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old host never started")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewNetworkClient(dir, "", b)
	updates := make(chan tailnet.Snapshot, 32)
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx, func(s tailnet.Snapshot) { updates <- s }) }()
	defer func() { cancel(); <-done }()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-updates:
			if s.Hostname == "shioji-mac" {
				goto preflight
			}
		case <-timer.C:
			t.Fatal("old host could not be inspected")
		}
	}
preflight:
	if err = os.WriteFile(filepath.Join(dir, "intent.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Do(tailnet.Action{Kind: "resume", ID: "failed-preflight"})
	for {
		select {
		case s := <-updates:
			if s.OperationID == "failed-preflight" && s.Error != "" {
				goto retained
			}
		case <-timer.C:
			t.Fatal("candidate preflight did not fail")
		}
	}
retained:
	if err = syscall.Kill(host.Process.Pid, syscall.Signal(0)); err != nil {
		t.Fatal("failed candidate preflight stopped old host", err)
	}
	if err = os.Remove(filepath.Join(dir, "intent.json")); err != nil {
		t.Fatal(err)
	}
	c.Do(tailnet.Action{Kind: "resume", ID: "new-build"})
	for {
		select {
		case s := <-updates:
			if s.OperationID != "new-build" {
				continue
			}
			conn, e := net.Dial("unix", tailnet.ServiceSocket(dir))
			if e != nil {
				t.Fatal(e)
			}
			pid, e := networkPeerPID(conn)
			conn.Close()
			if e != nil {
				t.Fatal(e)
			}
			p, e := WorkerProcess(pid)
			if e != nil || p.Executable != b {
				t.Fatal("reconnect retained old host", p.Executable, e, s.Error)
			}
			if _, e = os.Stat(filepath.Join(dir, "identity")); !os.IsNotExist(e) {
				t.Fatal("host handoff created a replacement identity", e)
			}
			if e = syscall.Kill(host.Process.Pid, syscall.Signal(0)); e == nil {
				t.Fatal("old host still owns profile")
			}
			return
		case <-timer.C:
			t.Fatal("new host never received reconnect")
		}
	}
}

func TestFailedCandidateRestoresPreviousHostBeforeReportingFailure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "node")
	a := filepath.Join(root, "previous")
	b := filepath.Join(root, "candidate")
	if out, err := exec.Command("go", "build", "-o", a, "../../cmd/shioji-runtime").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	// The external candidate passes read-only preflight, then fails on launch.
	// No real Keychain item or Tailnet enrollment is involved in this fault.
	src := filepath.Join(root, "candidate.go")
	code := `package main
import("encoding/json";"flag";"os")
func main(){d:=flag.Bool("network-describe",false,"");p:=flag.Bool("network-preflight",false,"");s:=flag.Bool("network-serve",false,"");flag.String("network-dir","","");flag.Bool("network-authorize",false,"");flag.Parse();if *d{json.NewEncoder(os.Stdout).Encode(struct{Version int;Role string}{1,"network"})};if *p{json.NewEncoder(os.Stdout).Encode(struct{Version int;Ready bool}{1,true})};if *s{os.Exit(1)}}`
	if err := os.WriteFile(src, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", b, src).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	cleanup, err := RegisterNetworkClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	host := exec.Command(a, "--network-serve", "--network-dir", dir)
	if err = host.Start(); err != nil {
		t.Fatal(err)
	}
	go host.Wait()
	t.Cleanup(func() {
		processes, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
		for _, info := range processes {
			p, e := WorkerProcess(int(info.Proc.P_pid))
			if e == nil && (p.Executable == a || p.Executable == b) {
				syscall.Kill(p.PID, syscall.SIGTERM)
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(tailnet.ServiceSocket(dir)); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("previous host did not start")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = PrepareNetworkHost(ctx, dir, "", b); err == nil {
		t.Fatal("failed candidate reported success")
	}
	conn, e := net.Dial("unix", tailnet.ServiceSocket(dir))
	if e != nil {
		t.Fatal("failure reported before previous host restored", e)
	}
	pid, e := networkPeerPID(conn)
	conn.Close()
	if e != nil {
		t.Fatal(e)
	}
	previous, e := WorkerProcess(pid)
	if e != nil || previous.Executable != a || pid == host.Process.Pid {
		t.Fatal("previous host was not restarted after candidate failure", previous, e)
	}
}

func TestGUIWaitCancellationDoesNotCancelDetachedHandoff(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "node")
	a := filepath.Join(root, "old")
	b := filepath.Join(root, "new")
	for _, v := range []struct{ path, version string }{{a, "3"}, {b, "4"}} {
		if out, err := exec.Command("go", "build", "-ldflags", "-X shioji/internal/runtimebuild.BuiltAt="+v.version, "-o", v.path, "../../cmd/shioji-runtime").CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	cleanup, err := RegisterNetworkClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	host := exec.Command(a, "--network-serve", "--network-dir", dir)
	if err = host.Start(); err != nil {
		t.Fatal(err)
	}
	go host.Wait()
	t.Cleanup(func() {
		processes, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
		for _, info := range processes {
			p, e := WorkerProcess(int(info.Proc.P_pid))
			if e == nil && (p.Executable == a || p.Executable == b) {
				syscall.Kill(p.PID, syscall.SIGTERM)
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(tailnet.ServiceSocket(dir)); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old host did not start")
		}
		time.Sleep(time.Millisecond)
	}
	lock, err := networkLaunchLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- prepareNetworkDetached(ctx, dir, "", b) }()
	deadline = time.Now().Add(5 * time.Second)
	for {
		processes, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
		started := false
		for _, info := range processes {
			p, e := WorkerProcess(int(info.Proc.P_pid))
			if e == nil && p.Executable == b {
				for _, arg := range p.Arguments {
					if arg == "--network-prepare" {
						started = true
					}
				}
			}
		}
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached handoff never started")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("GUI wait did not cancel")
	}
	lock.Close()
	deadline = time.Now().Add(8 * time.Second)
	for {
		conn, e := net.Dial("unix", tailnet.ServiceSocket(dir))
		if e == nil {
			pid, e := networkPeerPID(conn)
			conn.Close()
			if e == nil {
				p, e := WorkerProcess(pid)
				if e == nil && p.Executable == b && networkHostProcess(p, dir) {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("GUI cancellation interrupted independent handoff")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

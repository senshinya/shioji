package transport

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/runtimebuild"
	"shioji/internal/tailnet"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReconnectPreflightsBeforeMovingLegacyNetworkRegistration(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	support := filepath.Join(home, "Library", "Application Support", "Shioji")
	dir := filepath.Join(support, "client", "tailnet")
	runtimeRoot := filepath.Join(support, "Runtime")
	legacyBundle := filepath.Join(runtimeRoot, runtimeBundleName)
	legacyBinary := filepath.Join(legacyBundle, "Contents", "MacOS", "shioji-runtime")
	if err = os.MkdirAll(filepath.Dir(legacyBinary), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "fixture.go")
	program := `package main
import ("fmt"; "io"; "net"; "os")
func main() {
	switch os.Args[1] {
	case "--network-preflight":
		if _, err := os.Stat(os.Getenv("SHIOJI_TEST_PREFLIGHT_READY")); err != nil { os.Exit(1) }
		fmt.Println("{\"Version\":1,\"Ready\":true}")
		return
	case "--action":
		if err := os.WriteFile(os.Getenv("SHIOJI_TEST_REGISTRATION_TRACE"), []byte(os.Args[0]+"\n"+os.Args[2]+"\n"+os.Args[4]), 0600); err != nil { panic(err) }
		fmt.Println("{\"format\":1,\"state\":\"failed\"}")
		os.Exit(1)
	}
	listener, err := net.Listen("unix", os.Getenv("SHIOJI_TEST_LEGACY_SOCKET"))
	if err != nil { panic(err) }
	for {
		conn, err := listener.Accept()
		if err != nil { return }
		go func() {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}()
	}
}`
	if err = os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("go", "build", "-o", legacyBinary, source).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	plist := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.shioji.runtime</string><key>CFBundleExecutable</key><string>shioji-runtime</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`)
	if err = os.WriteFile(filepath.Join(legacyBundle, "Contents", "Info.plist"), plist, 0644); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", legacyBundle).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	digest, err := runtimebuild.Digest(legacyBinary)
	if err != nil {
		t.Fatal(err)
	}
	registeredBundle := filepath.Join(runtimeRoot, "Network", digest, runtimeBundleName)
	if out, e := exec.Command("/usr/bin/ditto", legacyBundle, registeredBundle).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	registeredBinary := filepath.Join(registeredBundle, "Contents", "MacOS", "shioji-runtime")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = saveNetworkHostSelection(dir, registeredBinary); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	// With a temporary HOME, unqualified GUI arguments resolve to the test
	// profile. Mark any real GUI so it cannot be mistaken for this test's owner.
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		owner, e := WorkerProcess(int(process.Proc.P_pid))
		if e == nil && legacyNetworkOwner(owner, dir) {
			remove, e := registerNetworkClient(dir, owner)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(remove)
		}
	}
	preflightReady := filepath.Join(home, "preflight-ready")
	registrationTrace := filepath.Join(home, "registration-trace")
	t.Setenv("SHIOJI_TEST_PREFLIGHT_READY", preflightReady)
	t.Setenv("SHIOJI_TEST_REGISTRATION_TRACE", registrationTrace)

	socketPath := tailnet.ServiceSocket(dir)
	if err = os.Mkdir(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(socketPath)) })
	host := exec.Command(legacyBinary, "--network-serve")
	host.Env = append(host.Environ(), "SHIOJI_TEST_LEGACY_SOCKET="+socketPath)
	if err = host.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = host.Process.Signal(syscall.SIGTERM)
		_ = host.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, e := net.Dial("unix", tailnet.ServiceSocket(dir))
		if e == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy runtime did not listen:", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The signed candidate fixture fails preflight. Its failure must leave the
	// working legacy owner of the private socket untouched.
	verify, ensure := networkOperations(dir, "", registeredBinary)
	if err = verify(host.Process.Pid); err == nil {
		t.Fatal("ordinary GUI attached to the legacy path")
	}
	if err = ensure(t.Context(), false); err == nil {
		t.Fatal("automatic startup attempted legacy migration")
	}
	if err = ensure(t.Context(), true); !errors.Is(err, tailnet.ErrUserAction) || !strings.Contains(err.Error(), "网络身份预检未完成") {
		t.Fatalf("explicit reconnect failed before candidate identity preflight: %v", err)
	}
	if _, err = os.Stat(registrationTrace); !os.IsNotExist(err) {
		t.Fatal("failed preflight changed registration:", err)
	}
	conn, err := net.Dial("unix", tailnet.ServiceSocket(dir))
	if err != nil {
		t.Fatal("failed preflight disconnected the old host:", err)
	}
	pid, peerErr := networkPeerPID(conn)
	conn.Close()
	if peerErr != nil || pid != host.Process.Pid {
		t.Fatalf("failed preflight replaced the old owner: pid=%d, err=%v", pid, peerErr)
	}
	if err = os.WriteFile(preflightReady, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = ensure(t.Context(), true); err == nil || err.Error() != "网络后台服务注册失败" {
		t.Fatalf("reconnect did not reach the fixture's unregister: %v", err)
	}
	trace, err := os.ReadFile(registrationTrace)
	want := registeredBinary + "\nunregister\ndev.shioji.network.agent.plist"
	if err != nil || string(trace) != want {
		t.Fatalf("legacy handoff used the wrong registration: %q, %v", trace, err)
	}
}

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

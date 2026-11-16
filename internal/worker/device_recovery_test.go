package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/store"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func deviceRecoveryFixture() bool {
	if os.Getenv("SHIOJI_RECOVERY_FIXTURE") == "" {
		return false
	}
	arg := func(name string) string {
		for i, v := range os.Args {
			if v == name && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return ""
	}
	for _, v := range os.Args {
		if v == "--describe" {
			b := runtimebuild.Current()
			b.Schema = store.SchemaVersion
			b.SafeUpdate = true
			b.RecoveryHelper = true
			json.NewEncoder(os.Stdout).Encode(protocol.Frame{Type: "worker-build", Major: protocol.Major, Minor: protocol.Minor, Worker: &b})
			return true
		}
	}
	dir := arg("--state-dir")
	if dir == "" {
		return false
	}
	if arg("--recovery-helper") == "run" {
		s, err := RunDeviceRecovery(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{memoryHarness{id: "memory"}}}, arg("--recovery-id"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if s != nil {
			defer s.Close()
			s.Serve()
		}
		return true
	}
	if arg("--unresponsive") == "yes" {
		owner, err := Acquire(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer owner.Close()
		l, err := owner.Listen()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		go func() {
			for {
				if _, e := os.Stat(filepath.Join(dir, "spawn-child")); e == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			child := exec.Command("/bin/sleep", "60")
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if child.Start() == nil {
				os.WriteFile(filepath.Join(dir, "new-child"), []byte(fmt.Sprint(child.Process.Pid)), 0600)
				child.Wait()
			}
		}()
		for {
			c, err := l.Accept()
			if err != nil {
				break
			}
			go func(c net.Conn) { defer c.Close(); time.Sleep(time.Hour) }(c)
		}
		return true
	}
	return false
}

func TestDeviceRecoveryIndependentOfUnresponsiveWorker(t *testing.T) {
	t.Setenv("SHIOJI_RECOVERY_FIXTURE", "1")
	dir, err := os.MkdirTemp("", "sh-rec-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	device := db.DeviceID()
	db.Close()
	cmd := exec.Command(os.Args[0], "--state-dir", dir, "--unresponsive", "yes")
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	cmd.Env = append(os.Environ(), "SHIOJI_RECOVERY_FIXTURE=1")
	stdout, _ := cmd.StdoutPipe()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatal(line, err, diagnostic.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	preview, err := InspectDeviceRecovery(ctx, dir, device)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Processes) == 0 {
		t.Fatal("hung owner omitted")
	}
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("inspection stopped owner", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "spawn-child"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	var spawned managedexec.ProcessInfo
	until := time.Now().Add(2 * time.Second)
	for spawned.Identity.PID == 0 && time.Now().Before(until) {
		b, _ := os.ReadFile(filepath.Join(dir, "new-child"))
		var pid int
		fmt.Sscan(string(b), &pid)
		list, _ := managedexec.Processes()
		for _, p := range list {
			if p.Identity.PID == pid {
				spawned = p
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if spawned.Identity.PID == 0 {
		t.Fatal("late child fixture did not start")
	}
	t.Cleanup(func() { managedexec.SignalExact(spawned, unix.SIGKILL) })
	in := domain.WorkerRecoveryInput{ID: domain.NewID(), ActionID: domain.NewID(), Action: "restart", DeviceID: device, PreviewRef: preview.Ref, RiskAccepted: true}
	// The independent helper runs even though this owner never reads its RPC.
	op, err := StartDeviceRecovery(ctx, dir, os.Args[0], in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StartDeviceRecovery(ctx, dir, os.Args[0], in)
	if err != nil || second.ID != op.ID {
		t.Fatal("duplicate changed operation", second, err)
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		op, err = DeviceRecoveryStatus(dir, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !op.Active() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if op.Phase != "completed" {
		t.Fatal("hung owner did not recover", op)
	}
	if managedexec.ProcessState(spawned) == "running" {
		t.Fatal("force restart left a child born after preview")
	}
	c, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	}, device)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.DeviceID != device {
		t.Fatal("device identity changed")
	}
	// Recovered helper became the new owner. End only the fixture's exact PID.
	p, err := recoveryOwner(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { managedexec.SignalExact(p, unix.SIGKILL) })
}

func TestDeviceRecoveryRefusesRisklessAndStaleSelection(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	id := db.DeviceID()
	db.Close()
	preview, err := InspectDeviceRecovery(context.Background(), dir, id)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerRecoveryInput{ID: domain.NewID(), ActionID: domain.NewID(), Action: "restart", DeviceID: id, PreviewRef: preview.Ref}
	if _, err = StartDeviceRecovery(context.Background(), dir, os.Args[0], in); err == nil {
		t.Fatal("force without risk confirmation accepted")
	}
	in.RiskAccepted = true
	in.PreviewRef = "stale"
	if _, err = StartDeviceRecovery(context.Background(), dir, os.Args[0], in); err == nil {
		t.Fatal("unknown preview accepted")
	}
}

func TestDeviceRecoveryAbandonedRunnerBecomesExplicitlyRetryable(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	r := deviceRecoveryRecord{Operation: domain.WorkerRecoveryOperation{ID: "accepted", DeviceID: "device", Phase: "accepted", UpdatedAt: time.Now().Add(-time.Minute)}}
	if err := recoveryWrite(dir, "accepted", r); err != nil {
		t.Fatal(err)
	}
	op, err := DeviceRecoveryStatus(dir, "accepted")
	if err != nil || op.Active() || op.Phase != "partial" {
		t.Fatal("missing runner left recovery permanently active", op, err)
	}
}

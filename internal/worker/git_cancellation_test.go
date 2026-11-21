package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGitCancellationTerminatesOwnedFilterWithoutWaitingForPipe(t *testing.T) {
	_, w, _ := gitTestServer(t)
	gate := t.TempDir()
	filter := filepath.Join(t.TempDir(), "filter")
	script := `#!/bin/sh
cat
(
 touch "$SHIOJI_COMPLETE_PIPE/entered"
 while [ ! -f "$SHIOJI_COMPLETE_PIPE/release" ]; do /bin/sleep 0.01; done
) &
echo $! > "$SHIOJI_COMPLETE_PIPE/pid"
exit 0
`
	if e := os.WriteFile(filter, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SHIOJI_COMPLETE_PIPE", gate)
	gitTestRun(t, w.Folders[0], "config", "filter.audit.clean", filter)
	if e := os.WriteFile(filepath.Join(w.Folders[0], ".gitattributes"), []byte("*.txt filter=audit\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.Folders[0], "file.txt"), []byte("new content\n"), 0600); e != nil {
		t.Fatal(e)
	}
	defer os.WriteFile(filepath.Join(gate, "release"), nil, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unrelated := exec.Command("/bin/sleep", "30")
	unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e := unrelated.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { unrelated.Process.Kill(); unrelated.Wait() }()
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, e := gitRun(ctx, w.Folders[0], "add", "-A"); done <- e }()
	auditTimeout := time.NewTimer(5 * time.Second)
	defer auditTimeout.Stop()
	for {
		if _, e := os.Stat(filepath.Join(gate, "entered")); e == nil {
			if _, e = os.Stat(filepath.Join(gate, "pid")); e == nil {
				break
			}
		}
		select {
		case <-auditTimeout.C:
			t.Fatal("real Git clean filter did not enter")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	raw, e := os.ReadFile(filepath.Join(gate, "pid"))
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	processes, e := managedexec.Processes()
	if e != nil {
		t.Fatal(e)
	}
	var child managedexec.ProcessInfo
	for _, p := range processes {
		if p.Identity.PID == pid {
			child = p
		}
	}
	if child.Identity.PID == 0 {
		t.Fatal("filter child was not observable")
	}
	cancel()
	<-ctx.Done()
	exceeded := false
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		exceeded = true
	}
	os.WriteFile(filepath.Join(gate, "release"), nil, 0600)
	if e := syscall.Kill(unrelated.Process.Pid, 0); e != nil {
		t.Fatal("Git cancellation touched unrelated process", e)
	}
	if !exceeded && managedexec.ProcessState(child) == "running" {
		t.Fatal("Git returned but its owned filter child is still running")
	}
	if exceeded {
		e := <-done
		t.Logf("Git cancellation returned only after pipe release at %v, error=%v", time.Since(start), e)
		t.Fatal("Git cancellation did not bound waiting on inherited stdout/stderr")
	}
}

func TestGitIncompleteOutputReturnsARecognizableResult(t *testing.T) {
	bin := t.TempDir()
	// The leader exits successfully while its child still holds stdout.
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n/bin/sleep 10 &\nprintf 'partial output'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	out, err := gitRun(context.Background(), bin, "status")
	var remote *protocol.RemoteError
	if out != "" || !errors.As(err, &remote) || remote.Code != "gitOutputIncomplete" {
		t.Fatalf("incomplete Git output escaped the result boundary: %q, %v", out, err)
	}
}

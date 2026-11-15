package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
)

// A real pinned app-server/executor with a deterministic local provider, no
// user credentials/model billing. Only the worker's idle clock is advanced.
func TestIdleRuntimeRealProcessStopsAndResumesOriginalThread(t *testing.T) {
	p := testutil.NewProvider(t)
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	mcp := filepath.Join(p.Home, "idle-mcp.py")
	if e = os.WriteFile(mcp, []byte(`import sys,json
for line in sys.stdin:
    request=json.loads(line)
    if 'id' not in request: continue
    result={}
    if request.get('method')=='initialize':
        result={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'idle-fixture','version':'1'}}
    if request.get('method')=='tools/list': result={'tools':[]}
    print(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}),flush=True)
`), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = fmt.Fprintf(f, "\n[mcp_servers.idle_fixture]\ncommand=%q\nargs=[%q]\n", python, mcp)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp("", "sw-idle-live-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	s, e := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	folder, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.store.CreateWorkspace("idle process", []string{folder})
	if e != nil {
		t.Fatal(e)
	}
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	send := func(id, sid string) domain.SessionDetail {
		t.Helper()
		op, e := s.submit(domain.Submission{OperationID: id, WorkspaceID: w.ID, SessionID: sid, Text: "Only reply OK", Config: cfg})
		if e != nil {
			t.Fatal(e)
		}
		if r := awaitReceipt(t, s, id); r.State != "confirmed" {
			t.Fatal(r)
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			s.dispatchMu.Lock()
			d, err := s.store.Session(op.SessionID, 0, 100)
			s.dispatchMu.Unlock()
			if err == nil && d.Session.Status == "completed" && d.Session.SourceTitle != "" {
				// Completion can precede the last usage callback and independent
				// title job. Drain source events before comparing durable history.
				inspectCtx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
				_, err = s.currentRuntime(op.SessionID).Inspect(inspectCtx, d.Session)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				d, err = s.store.Session(op.SessionID, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("live turn never completed")
		return domain.SessionDetail{}
	}
	before := send("first", "")
	sid := before.Session.ID
	raw, e := os.ReadFile(filepath.Join(s.sessionRuntimeDirectory(sid), "runtime-process.json"))
	if e != nil {
		t.Fatal(e)
	}
	var process processRecord
	if e = json.Unmarshal(raw, &process); e != nil || process.PID <= 1 {
		t.Fatal("no owned process", process, e)
	}
	info, e := unix.SysctlKinfoProc("kern.proc.pid", process.PID)
	if e != nil {
		t.Fatal(e)
	}
	owner := identityForIdleTest(*info)
	now := time.Now()
	sweepIdle(t, s, now)
	sweepIdle(t, s, now.Add(10*time.Minute))
	if s.currentRuntime(sid) != nil {
		t.Fatal("real idle runtime was kept", s.idleRuntimes)
	}
	if current, e := unix.SysctlKinfoProc("kern.proc.pid", process.PID); e == nil && identityForIdleTest(*current) == owner && current.Proc.P_stat != 5 {
		t.Fatal("retired runtime process is still alive")
	}
	scope, e := s.executions.Existing(sid)
	if e != nil || scope == nil {
		t.Fatal(e)
	}
	if e = scope.VerifyStopped(); e != nil {
		t.Fatal("owned descendants remain", e)
	}
	after, e := s.store.Session(sid, 0, 100)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("real retirement changed saved conversation", e)
	}
	resumed := send("second", sid)
	if resumed.Session.ThreadID != before.Session.ThreadID {
		t.Fatal("resume created a new source history")
	}
	inputs := 0
	for _, i := range resumed.Items {
		if i.Kind == "user" {
			inputs++
		}
	}
	if inputs != 2 {
		t.Fatal("resuming repeated old input", inputs)
	}
	s.config.ReplaceWorker = func(domain.WorkerReplacement) error { return nil }
	result, e := s.prepareWorkerUpdate(domain.WorkerReplacement{ExpectedBuildID: s.build.BuildID, BuildID: "candidate"})
	if e != nil || !result.Ready {
		t.Fatal("idle MCP service prevented worker update", result, e)
	}
}
func identityForIdleTest(p unix.KinfoProc) [3]int64 {
	return [3]int64{int64(p.Proc.P_pid), p.Proc.P_starttime.Sec, int64(p.Proc.P_starttime.Usec)}
}

func TestIdleRuntimeDoesNotStopUnreportedBackgroundDescendants(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	scope, e := s.executions.Existing(d.Session.ID)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	sweepIdle(t, s, now)
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	cmd, stdin, stdout, e := scope.StartOwned(ctx, "background", []string{"/bin/sh", "-c", "sleep 3 & echo ready; wait"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		scope.Stop(context.Background())
		stdin.Close()
		if cmd.ProcessState == nil {
			cmd.Wait()
		}
	})
	if _, e = bufio.NewReader(stdout).ReadString('\n'); e != nil {
		t.Fatal(e)
	}
	sweepIdle(t, s, now.Add(5*time.Minute))
	if r := s.currentRuntime(d.Session.ID); r == nil || r.Stopped() {
		t.Fatal("an unreported background process was retired")
	}
	if e = cmd.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("background process was interrupted", e)
	}
	if e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	if e = s.executions.Reap(ctx); e != nil {
		t.Fatal(e)
	}
	sweepIdle(t, s, now.Add(6*time.Minute))
	sweepIdle(t, s, now.Add(10*time.Minute))
	if s.currentRuntime(d.Session.ID) == nil {
		t.Fatal("unreported background activity did not restart the idle clock")
	}
	sweepIdle(t, s, now.Add(16*time.Minute))
	if s.currentRuntime(d.Session.ID) != nil {
		t.Fatal("runtime was not reclaimed after the background process became idle")
	}
}

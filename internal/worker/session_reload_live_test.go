package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
)

// Real installed hooks, app-server, executor, worker and private callback pipe.
// The model alone is a deterministic local provider in a disposable CODEX_HOME.
func TestSessionReloadRealCodexKeepsThreadAndRestartsWatcherOnNextInput(t *testing.T) {
	root := os.Getenv("SHIOJI_RYUGI_ROOT")
	if root == "" {
		t.Skip("set SHIOJI_RYUGI_ROOT for installed-plugin reload acceptance")
	}
	p := testutil.NewProvider(t)
	app := filepath.Join(root, "skills/artifacts/app")
	data, cache := filepath.Join(p.Home, "plugin-data"), filepath.Join(p.Home, "cache")
	raw, err := os.ReadFile(filepath.Join(app, "codex-hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher,omitempty"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	hookLog := filepath.Join(p.Home, "reload-hooks.log")
	for event, groups := range manifest.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				cmd := strings.ReplaceAll(hook["command"].(string), "${PLUGIN_ROOT}", root)
				hook["command"] = "printf '%s\\n' " + quote(event+" "+cmd) + " >> " + quote(hookLog) + "; env PLUGIN_DATA=" + quote(data) + " XDG_CACHE_HOME=" + quote(cache) + " " + cmd
			}
		}
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(p.Home, "hooks.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "\nhooks=true")
	f.Close()
	folder, _ := filepath.EvalSymlinks(t.TempDir())
	trustReloadHooks(t, p.Home, folder)
	dir, err := os.MkdirTemp("", "sh-reload-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.store.CreateWorkspace("reload acceptance", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.submit(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "Only reply OK", Config: domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	var before domain.SessionDetail
	awaitReloadLive(t, func() bool {
		before, _ = s.store.Session(op.SessionID, 0, 100)
		return before.Session.Status == "completed" && before.Session.SourceTitle != ""
	})
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	_, err = s.currentRuntime(op.SessionID).Inspect(ctx, before.Session)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	before, _ = s.store.Session(op.SessionID, 0, 100)
	watchFile := filepath.Join(data, "codex-comment-watch/workers", before.Session.ThreadID+".json")
	watchPID := func() int {
		var d struct{ PID int }
		raw, _ := os.ReadFile(watchFile)
		json.Unmarshal(raw, &d)
		return d.PID
	}
	awaitReloadLive(t, func() bool { return watchPID() > 1 })
	oldWatcher := watchPID()
	defer func() {
		cmd := exec.Command("node", filepath.Join(app, "codex-comment-watch.cjs"), "stop")
		cmd.Env = append(os.Environ(), "PLUGIN_DATA="+data, "XDG_CACHE_HOME="+cache)
		cmd.Stdin = strings.NewReader(fmt.Sprintf(`{"session_id":%q,"cwd":%q}`, before.Session.ThreadID, folder))
		cmd.Run()
	}()
	readProcess := func() processRecord {
		var r processRecord
		raw, err := os.ReadFile(filepath.Join(s.sessionRuntimeDirectory(op.SessionID), "runtime-process.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	oldProcess := readProcess()
	artifact := filepath.Join(folder, ".ryugi/artifacts/document/reload-proof")
	if err = os.MkdirAll(artifact, 0700); err != nil {
		t.Fatal(err)
	}
	subscription := filepath.Join(folder, ".ryugi/artifacts/.state/codex-comment-watch", before.Session.ThreadID+".json")
	os.MkdirAll(filepath.Dir(subscription), 0700)
	os.WriteFile(subscription, []byte(`{"artifacts":["document/reload-proof"]}`), 0600)
	result, err := reloadCall(t, s, "session.reload", "real-reload", before.Session)
	if err != nil {
		t.Fatal(err)
	}
	var receipt domain.SessionReloadOperation
	if err = json.Unmarshal(result["operation"], &receipt); err != nil || receipt.State != "completed" {
		t.Fatal("real reload did not complete", receipt, err)
	}
	newProcess := readProcess()
	if oldProcess.PID == newProcess.PID {
		t.Fatal("app-server process was reused")
	}
	if current, e := unix.SysctlKinfoProc("kern.proc.pid", oldProcess.PID); e == nil && current.Proc.P_stat != 5 {
		t.Fatal("old app-server is still running")
	}
	awaitReloadLive(t, func() bool {
		current, e := unix.SysctlKinfoProc("kern.proc.pid", oldWatcher)
		return e != nil || current.Proc.P_stat == 5
	})
	after, _ := s.store.Session(op.SessionID, 0, 100)
	if after.Session.ThreadID != before.Session.ThreadID || len(after.Items) != len(before.Items) || len(after.Turns) != len(before.Turns) {
		t.Fatal("reload submitted input or changed source history")
	}
	// Codex queues SessionStart until the first real turn after resume. Reload
	// must not invent a prompt merely to run the plugin's lifecycle hooks.
	continued, err := s.submit(domain.Submission{OperationID: "after-reload", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "Only reply OK after reload", Config: before.Session.Config})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, continued.ID)
	awaitReloadLive(t, func() bool { d, _ := s.store.Session(op.SessionID, 0, 100); return d.Session.Status == "completed" })
	awaitReloadLive(t, func() bool { return watchPID() > 1 && watchPID() != oldWatcher })
	os.WriteFile(filepath.Join(artifact, "comments.json"), []byte(`{"version":1,"threads":[{"id":"reload-comment","status":"open","statusBy":"human","statusAt":"2026-11-17T00:00:00Z","anchor":{"blockId":"b1"},"messages":[{"id":"m1","author":"human","at":"2026-11-17T00:00:00Z","text":"Only reply OK to verify the new watcher"}]}]}`), 0600)
	awaitReloadLive(t, func() bool {
		d, _ := s.store.Session(op.SessionID, 0, 100)
		users := 0
		for _, i := range d.Items {
			if i.Kind == "user" {
				users++
			}
		}
		return users == 3 && d.Session.Status == "completed"
	})
	t.Logf("real app-server %d -> %d; Ryugi watcher %d -> %d after next explicit input; same thread, no reload input; new callback completed", oldProcess.PID, newProcess.PID, oldWatcher, watchPID())
}

func awaitReloadLive(t *testing.T, condition func() bool) {
	t.Helper()
	until := time.Now().Add(20 * time.Second)
	for time.Now().Before(until) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("real reload condition did not settle")
}

func trustReloadHooks(t *testing.T, home, cwd string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); cmd.Process.Kill(); cmd.Wait() }()
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 65536), 4<<20)
	call := func(id int, method string, params any) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		fmt.Fprintln(in, string(b))
		for scan.Scan() {
			var r struct {
				ID     int
				Result json.RawMessage
				Error  json.RawMessage
			}
			if json.Unmarshal(scan.Bytes(), &r) == nil && r.ID == id {
				if len(r.Error) > 0 {
					t.Fatal(string(r.Error))
				}
				return r.Result
			}
		}
		t.Fatal("fixture RPC closed", scan.Err())
		return nil
	}
	call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "reload-hook-fixture", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	fmt.Fprintln(in, `{"method":"initialized"}`)
	var listed struct {
		Data []struct {
			Hooks []struct{ Key, CurrentHash string }
		}
	}
	if err := json.Unmarshal(call(2, "hooks/list", map[string]any{"cwds": []string{cwd}}), &listed); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	count := 0
	for _, entry := range listed.Data {
		for _, h := range entry.Hooks {
			parts := strings.Split(h.CurrentHash, "/")
			fmt.Fprintf(f, "\n[hooks.state.%q]\nenabled=true\ntrusted_hash=%q\n", h.Key, parts[0])
			if len(parts) > 1 {
				fmt.Fprintf(f, "trusted_content_hash=%q\n", parts[1])
			}
			count++
		}
	}
	if count == 0 {
		t.Fatal("no installed hooks discovered")
	}
}

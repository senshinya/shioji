package worker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
)

// Inspect the actual pinned binary's hook hashes, then explicitly trust only
// these known fixture hooks in the disposable CODEX_HOME. No user config changes.
func trustFixtureHooks(t *testing.T, home, cwd string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 65536), 4<<20)
	call := func(id int, method string, params any) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		fmt.Fprintln(stdin, string(raw))
		for scan.Scan() {
			var reply struct {
				ID     int
				Result json.RawMessage
				Error  json.RawMessage
			}
			if json.Unmarshal(scan.Bytes(), &reply) == nil && reply.ID == id {
				if len(reply.Error) > 0 {
					t.Fatal("fixture RPC", method, string(reply.Error))
				}
				return reply.Result
			}
		}
		t.Fatal("fixture app-server closed", scan.Err())
		return nil
	}
	call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "shioji-hook-fixture", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	fmt.Fprintln(stdin, `{"method":"initialized"}`)
	raw := call(2, "hooks/list", map[string]any{"cwds": []string{cwd}})
	var listed struct {
		Data []struct {
			Hooks []struct {
				Key         string
				CurrentHash string `json:"currentHash"`
			}
		}
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	config, err := os.OpenFile(filepath.Join(home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer config.Close()
	count := 0
	for _, entry := range listed.Data {
		for _, hook := range entry.Hooks {
			if hook.Key == "" || hook.CurrentHash == "" {
				t.Fatal("hook fixture omitted trust identity", string(raw))
			}
			fmt.Fprintf(config, "\n[hooks.state.%q]\nenabled=true\ntrusted_hash=%q\n", hook.Key, hook.CurrentHash)
			count++
		}
	}
	if count != 3 {
		t.Fatal("fixture did not load all lifecycle watchers", count, string(raw))
	}
}

// Opt-in integration against installed, unmodified Ryugi scripts, invoked by
// real Codex lifecycle hooks. Only the model is a local deterministic fixture.
func TestInstalledRyugiWatcherQueuesIntoOwningWorker(t *testing.T) {
	root := os.Getenv("SHIOJI_RYUGI_ROOT")
	if root == "" {
		t.Skip("set SHIOJI_RYUGI_ROOT to verify an installed Ryugi")
	}
	script := filepath.Join(root, "skills/artifacts/app/codex-comment-watch.cjs")
	before, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		after, err := os.ReadFile(script)
		if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
			t.Error("installed Ryugi was modified", err)
		}
	}()
	p := testutil.NewProvider(t)
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	hooks := map[string]any{}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "SessionEnd"} {
		mode := "watch"
		if event == "SessionEnd" {
			mode = "stop"
		}
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "node " + quote(script) + " " + mode, "async": mode == "watch", "timeout": 86400}}}}
	}
	raw, _ := json.Marshal(map[string]any{"hooks": hooks})
	os.WriteFile(filepath.Join(p.Home, "hooks.json"), raw, 0600)
	f, _ := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	io.WriteString(f, "hooks=true\n")
	f.Close()
	trustFixtureHooks(t, p.Home, cwd)
	dir, err := os.MkdirTemp("", "sh-ryugi-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.store.CreateWorkspace("Ryugi callback", []string{cwd})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.submit(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "first message", Config: domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var d domain.SessionDetail
	for time.Now().Before(deadline) {
		d, _ = s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d.Session.Status != "completed" || d.Session.ThreadID == "" {
		t.Fatal("initial turn did not complete", d.Session)
	}
	// Subscribe by Ryugi's existing public launcher helper; it writes only this
	// disposable project's state, not the installed plugin or its configuration.
	app := filepath.Dir(script)
	register := exec.Command("node", "-e", `require(process.argv[1]).register(['start','--project-dir',process.cwd(),'--artifact','document/review'],process.env)`, filepath.Join(app, "comment-subscriptions.cjs"))
	register.Dir = cwd
	register.Env = append(os.Environ(), "CODEX_THREAD_ID="+d.Session.ThreadID)
	if out, err := register.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	// Stop just this test's watcher before its project directory is removed.
	defer func() {
		cmd := exec.Command("node", script, "stop")
		cmd.Env = append(os.Environ(), "PLUGIN_DATA=", "CLAUDE_PLUGIN_DATA=")
		cmd.Stdin = strings.NewReader(fmt.Sprintf(`{"session_id":%q,"cwd":%q}`, d.Session.ThreadID, cwd))
		cmd.Run()
	}()
	artifact := filepath.Join(cwd, ".ryugi/artifacts/document/review")
	os.MkdirAll(artifact, 0700)
	os.WriteFile(filepath.Join(artifact, "comments.json"), []byte(`{"version":1,"threads":[{"id":"review","status":"open","statusBy":"human","statusAt":"2026-11-07T00:00:00Z","anchor":{"blockId":"b1"},"messages":[{"id":"message-1","author":"human","at":"2026-11-07T00:00:00Z","text":"Verify the callback"}]}]}`), 0600)
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		d, _ = s.store.Session(op.SessionID, 0, 100)
		comments, replies := 0, 0
		for _, item := range d.Items {
			if item.Kind == "user" && strings.Contains(item.Text, "Ryugi review comments changed.") {
				comments++
			}
			if item.Kind == "assistant" && item.Final {
				replies++
			}
		}
		if comments == 1 && replies == 2 && d.Session.Status == "completed" {
			if len(p.Inputs()) != 2 {
				t.Fatal("callback invoked unexpected model requests", len(p.Inputs()))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("unmodified Ryugi did not wake its owning session: session=%+v queue=%+v items=%+v", d.Session, d.Queue, d.Items)
}

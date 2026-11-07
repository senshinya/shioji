package codex

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/testutil"
	"strings"
	"testing"
	"time"
)

// This exercises the same desktop framing and tool exchange used by Ryugi
// 1.58.0, against our real Codex runtime rather than a substitute app-server.
func TestDesktopCallbackAdmitsOnlyTheBoundThread(t *testing.T) {
	p := testutil.NewProvider(t)
	messages := make(chan harness.IncomingMessage, 8)
	r, err := Start(context.Background(), Options{Home: p.Home, OnMessage: func(ctx context.Context, m harness.IncomingMessage) error { messages <- m; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	values := map[string]string{}
	for _, s := range r.cmd.Env {
		if k, v, ok := strings.Cut(s, "="); ok {
			values[k] = v
		}
	}
	socket := values["CODEX_APP_TOOLS_PIPE_PATH"]
	if socket == "" {
		t.Fatal("owned app-server does not expose a host callback pipe")
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("callback pipe is not private", info, err)
	}
	parent, err := os.Stat(filepath.Dir(socket))
	if err != nil || parent.Mode().Perm() != 0700 {
		t.Fatal("callback directory is not private", parent, err)
	}
	if values["CODEX_APP_TOOLS_CALLER_HOST_ID"] != "local" || !strings.HasPrefix(values["CODEX_MCP_NODE_PATH"], "/") {
		t.Fatal("hook desktop environment is incomplete")
	}
	s := domain.Session{ID: "session", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}}
	thread, err := r.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	rpc := func(method string, params any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		var header [4]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(b)))
		conn.Write(header[:])
		conn.Write(b)
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			t.Fatal(err)
		}
		b = make([]byte, binary.LittleEndian.Uint32(header[:]))
		if _, err := io.ReadFull(conn, b); err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	listed := rpc("tools/list", map[string]any{"threadStartKind": "all"})
	result, ok := listed["result"].(map[string]any)
	if !ok {
		t.Fatal(listed)
	}
	tools := result["tools"].([]any)
	tool := tools[0].(map[string]any)
	if len(tools) != 1 || tool["name"] != "send_message_to_thread" {
		t.Fatal("callback exposed unnecessary tools", listed)
	}
	params := map[string]any{"callerSource": "codex", "hostId": "local", "namespace": tool["namespace"], "tool": "send_message_to_thread", "threadId": thread, "turnId": "mcp-turn-test", "callId": "call-one", "arguments": map[string]any{"threadId": thread, "hostId": "local", "prompt": "review comment changed"}}
	reply := rpc("tools/call", params)
	result, ok = reply["result"].(map[string]any)
	if !ok || result["success"] != true {
		t.Fatal("callback was not acknowledged", reply)
	}
	select {
	case msg := <-messages:
		if msg.SessionID != "session" || msg.SourceRef != thread || msg.Text != "review comment changed" || msg.OperationID == "" {
			t.Fatal("wire data was not translated", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no neutral callback")
	}
	// Other tools, hosts and frames cannot expand this narrow callback surface.
	for _, change := range []struct {
		key   string
		value any
	}{
		{"tool", "run_command"}, {"hostId", "remote"}, {"namespace", "another-host"}, {"callerSource", "unknown"}, {"callId", ""},
	} {
		original := params[change.key]
		params[change.key] = change.value
		if reply = rpc("tools/call", params); reply["error"] == nil {
			t.Fatal("invalid callback accepted", change.key, reply)
		}
		params[change.key] = original
	}
	if reply = rpc("thread/start", map[string]any{}); reply["error"] == nil {
		t.Fatal("callback opened the app-server API")
	}
	params["threadId"] = "foreign-thread"
	if reply = rpc("tools/call", params); reply["error"] == nil {
		t.Fatal("mismatched callback source accepted", reply)
	}
	r.normalizer.mu.Lock()
	delete(r.normalizer.sessions, thread)
	r.normalizer.mu.Unlock()
	params["threadId"] = thread
	if reply = rpc("tools/call", params); reply["error"] == nil {
		t.Fatal("retired history accepted", reply)
	}
	select {
	case msg := <-messages:
		t.Fatal("rejected callback reached the worker", msg)
	default:
	}
	r.Close()
	if _, err = os.Stat(socket); !os.IsNotExist(err) {
		t.Fatal("runtime exit left callback listener", err)
	}
}

func TestDesktopBridgeRejectsOversizedFramesAndClosesIdlePeers(t *testing.T) {
	b, err := newDesktopBridge(func(context.Context, string, string, string) error {
		t.Error("malformed frame reached callback")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	conn, err := net.Dial("unix", b.path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], (1<<20)+1)
	conn.Write(header[:])
	if _, err = conn.Read(header[:]); err == nil {
		t.Fatal("oversized frame was not rejected")
	}
	idle, err := net.Dial("unix", b.path)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	b.close()
	idle.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = idle.Read(header[:]); err == nil {
		t.Fatal("bridge exit retained an open peer")
	}
}

func TestDesktopEnvironmentKeepsIndependentPluginConfiguration(t *testing.T) {
	env := desktopEnvironment([]string{
		"CODEX_APP_TOOLS_PIPE_PATH=/tmp/parent.sock",
		"CODEX_APP_TOOLS_CALLER_HOST_ID=another-host",
		"CODEX_MCP_NODE_PATH=/parent/node",
		"RYUGI_CODEX_REMOTE=unix:///tmp/explicit-plugin.sock",
		"RYUGI_CODEX_REMOTE_AUTH_TOKEN_ENV=PLUGIN_TOKEN",
		"ANOTHER_SKILL_ENDPOINT=unix:///tmp/another.sock",
	}, &desktopBridge{path: "/tmp/owned.sock"}, "/usr/local/bin/node")
	got := map[string]string{}
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		got[k] = v
	}
	if got["RYUGI_CODEX_REMOTE"] != "unix:///tmp/explicit-plugin.sock" || got["RYUGI_CODEX_REMOTE_AUTH_TOKEN_ENV"] != "PLUGIN_TOKEN" || got["ANOTHER_SKILL_ENDPOINT"] != "unix:///tmp/another.sock" {
		t.Fatal("host transport rewrites a particular plugin's configuration", got)
	}
	if got["CODEX_APP_TOOLS_PIPE_PATH"] != "/tmp/owned.sock" || got["CODEX_APP_TOOLS_CALLER_HOST_ID"] != "local" || got["CODEX_MCP_NODE_PATH"] != "/usr/local/bin/node" {
		t.Fatal("child borrowed another host's IPC", got)
	}
}

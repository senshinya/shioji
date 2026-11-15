package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/net/websocket"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/managedexec"
	"shioji/internal/testutil"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--fixture-signin" {
		signInFixture(os.Args[2])
		os.Exit(0)
	}
	if handled, err := managedexec.ChildMain(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == "--fixture-mcp" {
		os.WriteFile(os.Args[2], []byte(fmt.Sprint(os.Getpid())), 0600)
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			var msg struct {
				ID     json.RawMessage
				Method string
			}
			json.Unmarshal(scan.Bytes(), &msg)
			if len(msg.ID) == 0 {
				continue
			}
			var result any = map[string]any{}
			switch msg.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "owned-fixture", "version": "1"}}
			case "tools/list":
				result = map[string]any{"tools": []any{}}
			}
			json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStdioServiceRequiresSettledBidirectionalProtocol(t *testing.T) {
	service := &stdioService{client: map[string]string{}, server: map[string]string{}}
	input := func(text string) { service.observe([]byte(text), true) }
	output := func(text string) { service.observe([]byte(text), false) }
	if service.idle() {
		t.Fatal("process identity alone proved service idle")
	}
	input("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
	output("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2024-11-05\",\"serverInfo\":{\"name\":\"fixture\"}}}\n")
	if service.idle() {
		t.Fatal("service initialization was not confirmed by the client")
	}
	input("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n")
	if !service.idle() {
		t.Fatal("initialized quiet service was not idle")
	}
	input("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\"}\n")
	if service.idle() {
		t.Fatal("running service call was treated as idle")
	}
	output("{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}")
	if service.idle() {
		t.Fatal("partial response released the service")
	}
	output("}\n")
	if !service.idle() {
		t.Fatal("completed service call stayed busy")
	}
	request := []byte("{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\"}\n")
	service.write("retryable-write", request)
	service.write("retryable-write", request)
	output("{\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"data\":\"")
	service.observe(bytes.Repeat([]byte{'x'}, 2<<20), false)
	if service.idle() {
		t.Fatal("large incomplete service response was idle")
	}
	output("\"}}\n")
	if !service.idle() {
		t.Fatal("deduplicated write or legitimate large response prevented idle")
	}
	output("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"sampling/createMessage\"}\n")
	if service.idle() {
		t.Fatal("server request was ignored")
	}
	input("{\"jsonrpc\":\"2.0\",\"id\":2,\"error\":{\"code\":-1}}\n")
	if !service.idle() {
		t.Fatal("settled server request stayed busy")
	}
	output("not JSON\n")
	if service.idle() {
		t.Fatal("unrecognized protocol was treated as idle")
	}
}

func TestServiceOutputSurvivesReversibleMaintenance(t *testing.T) {
	p := testutil.NewProvider(t)
	f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "\n[mcp_servers.fixture]\ncommand=%q\nargs=[\"--fixture-mcp\",%q]\n", os.Args[0], filepath.Join(p.Home, "mcp.pid"))
	f.Close()
	manager, _ := managedexec.New(t.TempDir(), os.Args[0])
	scope, _ := manager.Begin("service-output")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lost := make(chan struct{}, 1)
	e, err := StartExecutor(ctx, ExecutorOptions{Home: p.Home, Scope: scope, OnLost: func() error {
		select {
		case lost <- struct{}{}:
		default:
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { e.Close(context.Background()); scope.Stop(context.Background()) }()
	// Exercise the real executor registration and private output filter without
	// depending on a model turn or a particular MCP server implementation.
	cfg, _ := websocket.NewConfig(e.URL(), "http://localhost")
	cfg.Header = http.Header{"Authorization": []string{"Bearer " + e.Token()}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	params, _ := json.Marshal(map[string]any{"processId": "mcp-stdio-1", "argv": []string{os.Args[0], "--fixture-mcp", filepath.Join(p.Home, "mcp.pid")}, "cwd": "file:///private/tmp", "env": map[string]string{"PATH": "/usr/bin:/bin"}, "tty": false, "pipeStdin": true})
	if err = websocket.JSON.Send(conn, wire.Message{ID: json.RawMessage(`"start"`), Method: "process/start", Params: params}); err != nil {
		t.Fatal(err)
	}
	for {
		var result wire.Message
		if err = websocket.JSON.Receive(conn, &result); err != nil {
			t.Fatal(err)
		}
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		if string(result.ID) == `"start"` {
			break
		}
	}
	for {
		e.mu.Lock()
		b := e.boots["mcp-stdio-1"]
		ready := b != nil && b.registered
		e.mu.Unlock()
		if ready {
			select {
			case <-b.ready:
				goto registered
			default:
			}
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
registered:
	input := func(text string) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"chunk": []byte(text)})
		if err := e.observeServiceWrite("mcp-stdio-1", params); err != nil {
			t.Fatal(err)
		}
	}
	output := func(seq uint64, text string) bool {
		params, _ := json.Marshal(map[string]any{"processId": "mcp-stdio-1", "stream": "stdout", "seq": seq, "chunk": []byte(text)})
		return e.filterOutput(&wire.Message{Method: "process/output", Params: params})
	}
	input("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
	output(100, "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2024-11-05\",\"serverInfo\":{\"name\":\"fixture\"}}}\n")
	input("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n")
	undo, err := scope.FenceIfIdle()
	if err != nil {
		t.Fatal(err)
	}
	forwarded := make(chan bool, 1)
	go func() {
		forwarded <- output(101, "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"sampling/createMessage\"}\n")
	}()
	select {
	case <-forwarded:
		t.Fatal("output was consumed while reversibly fenced")
	case <-time.After(20 * time.Millisecond):
	}
	if err = undo(); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-forwarded:
		if !ok {
			t.Fatal("maintenance dropped a valid service request")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = scope.FenceIfIdle(); err == nil {
		t.Fatal("forwarded server request was not retained as busy")
	}
	input("{\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{}}\n")
	undo, err = scope.FenceIfIdle()
	if err != nil {
		t.Fatal("settled request still blocked maintenance", err)
	}
	if err = undo(); err != nil {
		t.Fatal(err)
	}
	output(102, "unrecognized service output\n")
	select {
	case <-lost:
		t.Fatal("unrecognized output interrupted a live runtime")
	default:
	}
	if _, err = scope.FenceIfIdle(); err == nil || !strings.Contains(err.Error(), "工具服务") {
		t.Fatal("unrecognized protocol did not explain its unverified idle state", err)
	}
}

func TestOwnedExecutorStopsCommandsAndMCPWithoutLeakingBootstrap(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint("tty-", tty), func(t *testing.T) {
			p := testutil.NewProvider(t)
			p.SetCommand("printf '%s' $$ > .shioji-recovery-tool-pid; sleep 60", tty)
			mcpPID := filepath.Join(p.Home, "mcp.pid")
			f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(f, "\n[mcp_servers.fixture]\ncommand=%q\nargs=[\"--fixture-mcp\",%q]\n", os.Args[0], mcpPID)
			f.Close()
			manager, err := managedexec.New(t.TempDir(), os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			scope, err := manager.Begin("owned-session")
			if err != nil {
				t.Fatal(err)
			}
			if err = scope.Bind("worker", os.Getpid()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			executor, err := StartExecutor(ctx, ExecutorOptions{Home: p.Home, Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { executor.Close(context.Background()); scope.Stop(context.Background()) }()
			var mu sync.Mutex
			var content strings.Builder
			r, err := Start(ctx, Options{Home: p.Home, Scope: scope, Executor: executor, OnEvent: func(e domain.Event) error {
				mu.Lock()
				defer mu.Unlock()
				if e.Item != nil {
					content.WriteString(e.Item.Text)
				}
				content.WriteString(e.Text)
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			folder, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s := domain.Session{ID: "owned-session", Context: domain.LaunchContext{CWD: folder}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "auto-approve"}}
			s.ThreadID, err = r.Create(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			if err = scope.Cover("original-input"); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Submit(ctx, s, "original-input", "SHIOJI_RECOVERY_EXEC"); err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(15 * time.Second)
			for {
				if _, err = os.Stat(filepath.Join(folder, ".shioji-recovery-tool-pid")); err == nil {
					if _, err = os.Stat(mcpPID); err == nil {
						break
					}
				}
				if time.Now().After(until) {
					mu.Lock()
					text := content.String()
					mu.Unlock()
					t.Fatal("owned command or MCP did not start", text)
				}
				time.Sleep(10 * time.Millisecond)
			}
			files, _ := filepath.Glob(filepath.Join(scope.Directory, scope.Generation, "launches", "*.json"))
			owned := 0
			for _, file := range files {
				var ticket managedexec.Ticket
				b, _ := os.ReadFile(file)
				json.Unmarshal(b, &ticket)
				if ticket.Kind == "executor" && ticket.State == "registered" {
					owned++
				}
			}
			if owned < 2 {
				t.Fatal("MCP did not pass through the managed executor", owned)
			}
			// Kill only this Agent. The executor and physical ownership remain available.
			r.cmd.Process.Kill()
			select {
			case <-r.Done():
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err = executor.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if err = scope.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			if err = scope.VerifyStopped(); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(folder, ".shioji-recovery-tool-pid"), mcpPID} {
				var pid int
				b, _ := os.ReadFile(path)
				fmt.Sscan(string(b), &pid)
				proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
				if err == nil && proc.Proc.P_stat != 5 {
					t.Fatal("registered process still executing", pid)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(content.String(), managedexec.RegistrationPrefix) || strings.Contains(content.String(), "\"PipeStdin\"") {
				t.Fatal("bootstrap protocol leaked into agent content", content.String())
			}
		})
	}
}

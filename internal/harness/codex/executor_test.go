package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/testutil"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
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

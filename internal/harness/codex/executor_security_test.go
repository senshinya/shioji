package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/managedexec"
	"shioji/internal/testutil"
)

func TestManagedBootstrapSuppliesEncodingWithoutLeakingTargetEnvironment(t *testing.T) {
	m, err := managedexec.New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := m.Begin("bootstrap-environment")
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Bind("executor", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	e := &Executor{scope: scope, boots: map[string]*executorBootstrap{}, baseEnv: map[string]string{}}
	params := map[string]any{
		"processId": "environment",
		"argv":      []string{"/bin/echo", "ready"},
		"env": map[string]string{
			"__CF_USER_TEXT_ENCODING": "target-encoding",
			"DYLD_INSERT_LIBRARIES":   "/target/loader.dylib",
			"TARGET_SECRET":           "only-after-registration",
		},
	}
	body, _ := json.Marshal(params)
	msg := wire.Message{Method: "process/start", Params: body}
	if err = e.prepare(&msg); err != nil {
		t.Fatal(err)
	}
	var forwarded struct{ Env map[string]string }
	if err = json.Unmarshal(msg.Params, &forwarded); err != nil {
		t.Fatal(err)
	}
	encoding := strings.Split(forwarded.Env["__CF_USER_TEXT_ENCODING"], ":")
	if len(encoding) != 3 {
		t.Fatal("bootstrap lacks a local encoding and would query user services before registration", forwarded.Env)
	}
	uid, err := strconv.ParseUint(encoding[0], 0, 64)
	if err != nil || uid != uint64(os.Getuid()) {
		t.Fatal("bootstrap encoding does not identify the actual user", encoding[0], err)
	}
	if encoding[1] != "0" || encoding[2] != "0" || len(forwarded.Env) != 2 || forwarded.Env["PATH"] != "/usr/bin:/bin" {
		t.Fatal("bootstrap environment must contain only the local encoding and trusted PATH", forwarded.Env)
	}
	ack := e.boots["environment"].ack.Env
	for _, expected := range []string{"__CF_USER_TEXT_ENCODING=target-encoding", "DYLD_INSERT_LIBRARIES=/target/loader.dylib", "TARGET_SECRET=only-after-registration"} {
		found := false
		for _, entry := range ack {
			found = found || entry == expected
		}
		if !found {
			t.Fatal("target environment was changed instead of being held until registration", expected)
		}
	}
}

func TestManagedReadOnlyCommandRunsButCannotWrite(t *testing.T) {
	p := testutil.NewProvider(t)
	p.SetCommand("printf 'managed-command-ran\\n'; printf forbidden > .must-not-exist; printf 'managed-command-finished\\n'", false)
	dir, err := os.MkdirTemp("/private/tmp", "shioji-readonly-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	m, _ := managedexec.New(t.TempDir(), os.Args[0])
	scope, _ := m.Begin("readonly")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	executor, err := StartExecutor(ctx, ExecutorOptions{Home: p.Home, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { executor.Close(context.Background()); scope.Stop(context.Background()) }()
	var mu sync.Mutex
	var output strings.Builder
	complete := make(chan struct{}, 1)
	r, err := Start(ctx, Options{Home: p.Home, Scope: scope, Executor: executor, OnEvent: func(e domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if e.Item != nil {
			output.WriteString(e.Item.Text)
		}
		output.WriteString(e.Text)
		if e.Turn != nil && e.Turn.Status != "running" {
			select {
			case complete <- struct{}{}:
			default:
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := domain.Session{ID: "readonly", Context: domain.LaunchContext{CWD: dir}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if !r.ManagedCoverage() {
		t.Fatal("ordinary configuration did not establish managed coverage")
	}
	if _, err = r.Submit(ctx, s, "readonly", "SHIOJI_RECOVERY_EXEC"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-complete:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mu.Lock()
	text := output.String()
	mu.Unlock()
	if !strings.Contains(text, "managed-command-ran") || !strings.Contains(text, "managed-command-finished") {
		t.Fatal("sandbox test never executed target command", text)
	}
	if _, err = os.Stat(filepath.Join(dir, ".must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("read-only command wrote its target", err)
	}
}

// A real dyld constructor would execute before Go main if target env reached
// bootstrap. It must see a durable registered ticket before its first write.
func TestLoaderEnvironmentRunsOnlyAfterDurableRegistration(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	lib := filepath.Join(dir, "constructor.dylib")
	source := filepath.Join(dir, "probe.c")
	code := `#include <stdlib.h>
#include <stdio.h>
#include <unistd.h>
#include <dirent.h>
#include <string.h>
__attribute__((constructor)) static void probe(void) {
 const char *p=getenv("SHIOJI_LOADER_MARKER"),*journal=getenv("SHIOJI_LAUNCH_JOURNAL");
 if(!p||!journal)return;
 int registered=0;DIR*d=opendir(journal);struct dirent*e;char pid[64];snprintf(pid,sizeof(pid),"\"PID\":%d,",getpid());
 if(d){while((e=readdir(d))){char path[4096],buf[8192];snprintf(path,sizeof(path),"%s/%s",journal,e->d_name);FILE*f=fopen(path,"r");if(!f)continue;size_t n=fread(buf,1,sizeof(buf)-1,f);fclose(f);buf[n]=0;if(strstr(buf,pid)&&strstr(buf,"\"State\":\"registered\""))registered=1;}closedir(d);}
 FILE*f=fopen(p,"a");if(f){fprintf(f,"%d|%s\n",getpid(),registered?"registered":"before-registration");fclose(f);}
}
int main(int argc,char**argv){printf("%s|%s\n",argv[0],getenv("TARGET_VALUE"));printf("SHIOJI_LAUNCH ordinary-user-output\n");return 0;}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{source, "-o", target}, {"-dynamiclib", source, "-o", lib}} {
		if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	m, _ := managedexec.New(t.TempDir(), os.Args[0])
	scope, _ := m.Begin("loader")
	scope.Bind("worker", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	executor, err := StartExecutor(ctx, ExecutorOptions{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { executor.Close(context.Background()); scope.Stop(context.Background()) }()
	cfg, err := websocket.NewConfig(executor.URL(), "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header = http.Header{"Authorization": []string{"Bearer " + executor.Token()}}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	marker := filepath.Join(dir, "loader-marker")
	params := map[string]any{"processId": "loader", "argv": []string{target}, "arg0": "kept-argv0", "cwd": (&url.URL{Scheme: "file", Path: dir}).String(), "env": map[string]string{"PATH": "/usr/bin:/bin", "DYLD_INSERT_LIBRARIES": lib, "SHIOJI_LOADER_MARKER": marker, "SHIOJI_LAUNCH_JOURNAL": filepath.Join(scope.Directory, scope.Generation, "launches"), "TARGET_VALUE": "preserved"}, "tty": false, "pipeStdin": false}
	b, _ := json.Marshal(params)
	if err = websocket.JSON.Send(conn, wire.Message{ID: json.RawMessage(`"loader"`), Method: "process/start", Params: b}); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for {
		var msg wire.Message
		if err = websocket.JSON.Receive(conn, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Error != nil {
			t.Fatal(msg.Error)
		}
		if msg.Method == "process/output" {
			var chunk struct{ Chunk []byte }
			json.Unmarshal(msg.Params, &chunk)
			output.Write(chunk.Chunk)
		}
		// Exit status can precede the final pipe output; closed drains both streams.
		if msg.Method == "process/closed" {
			break
		}
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal("target dyld constructor did not run", err)
	}
	if strings.Contains(string(raw), "before-registration") {
		t.Fatal("loader executed before durable registration", string(raw))
	}
	var pid int
	fmt.Sscanf(string(raw), "%d|registered", &pid)
	files, _ := filepath.Glob(filepath.Join(scope.Directory, scope.Generation, "launches", "*.json"))
	found := false
	for _, file := range files {
		var ticket managedexec.Ticket
		raw, _ := os.ReadFile(file)
		json.Unmarshal(raw, &ticket)
		if ticket.Kind == "executor" && ticket.Root.PID == pid && ticket.State == "registered" {
			found = true
		}
	}
	if !found {
		t.Fatal("loader code ran without durable registration", pid)
	}
	if !strings.Contains(output.String(), "kept-argv0|preserved") || !strings.Contains(output.String(), "SHIOJI_LAUNCH ordinary-user-output") {
		t.Fatal("target argv/env or private output changed", output.String())
	}
}

func TestManagedNetworkCannotLoseCredentialOrProxyTransformations(t *testing.T) {
	for _, field := range []string{"networkProxy", "managedNetwork", "enforceManagedNetwork"} {
		t.Run(field, func(t *testing.T) {
			params := map[string]any{"processId": "restricted", "argv": []string{"/bin/sh", "-c", "echo must-not-run"}, "env": map[string]string{"TOKEN": "must-not-pass"}}
			if field == "enforceManagedNetwork" {
				params[field] = true
			} else {
				params[field] = map[string]any{"enabled": true}
			}
			raw, _ := json.Marshal(params)
			e := &Executor{boots: map[string]*executorBootstrap{}}
			msg := wire.Message{Params: raw}
			if err := e.prepare(&msg); err == nil {
				t.Fatal("unimplemented managed network policy was silently discarded")
			}
			if len(e.boots) != 0 {
				t.Fatal("unsupported policy created a launch")
			}
		})
	}
}

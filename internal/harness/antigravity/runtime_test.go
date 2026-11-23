package antigravity

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestCatalogRefreshCreatesSessionOnlyOnRequest(t *testing.T) {
	a, log := fixtureAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dir := t.TempDir()
	probe := func(refresh bool) harness.Runtime {
		t.Helper()
		r, err := a.Start(ctx, harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: refresh})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close(context.Background()) })
		return r
	}
	first := probe(false)
	if first.Readiness().State == domain.RuntimeReady || hasMethod(log, "session/new") {
		t.Fatal("background probe created a native session or reported a model catalog")
	}
	ready := probe(true).Readiness()
	if len(ready.Models) != 1 || ready.Models[0].ID != "gemini-fixture" || !hasMethod(log, "session/close") {
		t.Fatalf("explicit discovery = %+v", ready)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	cached := probe(false).Readiness()
	if len(cached.Models) != 1 || hasMethod(log, "session/new") {
		t.Fatalf("background catalog did not reuse discovery: %+v", cached)
	}
	if err := os.WriteFile(log+".auth-required", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(ctx, harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: true}); err == nil {
		t.Fatal("explicit authentication failure was hidden")
	}
	if again := probe(false).Readiness(); again.State == domain.RuntimeReady || len(again.Models) != 0 {
		t.Fatalf("background probe restored a catalog after logout: %+v", again)
	}
}

func TestPromptAndSteerShareOneTurnUntilBothSettle(t *testing.T) {
	a, _ := fixtureAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	events := make(chan domain.Event, 32)
	r, err := a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { events <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	s := domain.Session{ID: "shioji-session", Context: domain.LaunchContext{CWD: t.TempDir(), AdditionalDirectories: []string{t.TempDir()}}, Config: domain.RunConfig{Model: "gemini-fixture", Effort: "high", Permission: "default", Plan: true}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := r.Submit(ctx, s, "op", harness.TurnInput{Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Steer(ctx, s, turn, "steer", harness.TurnInput{Text: "steer"}); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for {
		select {
		case <-ctx.Done():
			t.Fatal("no terminal event")
		case event := <-events:
			if event.Item != nil && event.Item.Final {
				texts = append(texts, event.Item.Text)
			}
			if event.Turn == nil || event.Turn.Status.Active() {
				continue
			}
			if event.SessionID != s.ID || event.Turn.ID != turn || event.Turn.Status != domain.ExecutionCompleted || !slices.Contains(texts, "primary continued") || !slices.Contains(texts, "steer continued") {
				t.Fatalf("turn completed early or used a different identity: %+v, %v", event.Turn, texts)
			}
			return
		}
	}
}

func fixtureAdapter(t *testing.T) (Adapter, string) {
	t.Helper()
	dir := t.TempDir()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "agy")
	log := filepath.Join(dir, "requests")
	t.Setenv("SHIOJI_ACP_FIXTURE_LOG", log)
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec "+shellQuote(program)+" -test.run='^TestACPFixtureProcess$' -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return Adapter{Executable: executable}, log
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func hasMethod(path, method string) bool {
	data, _ := os.ReadFile(path)
	return slices.Contains(strings.Split(string(data), "\n"), method)
}

func TestACPFixtureProcess(t *testing.T) {
	if !slices.Contains(os.Args, "acp") {
		return
	}
	log, err := os.OpenFile(os.Getenv("SHIOJI_ACP_FIXTURE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	update := func(kind, text string) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
			"sessionId": "native-session", "update": map[string]any{"sessionUpdate": kind, "content": map[string]string{"type": "text", "text": text}},
		}})
	}
	const model = "gemini-fixture"
	effort, mode := "low", "default"
	options := func() []map[string]any {
		return []map[string]any{
			{"id": "model", "category": "model", "type": "select", "currentValue": model, "options": []map[string]string{{"value": model, "name": "Gemini Fixture"}}},
			{"id": "thought_level", "category": "thought_level", "type": "select", "currentValue": effort, "options": []map[string]string{{"value": "low", "name": "Low"}, {"value": "high", "name": "High"}}},
			{"id": "mode", "category": "mode", "type": "select", "currentValue": mode, "options": []map[string]string{{"value": "default", "name": "Default"}, {"value": "plan", "name": "Plan"}}},
		}
	}
	var first, pendingCompact json.RawMessage
	for in.Scan() {
		var msg struct {
			ID     json.RawMessage
			Method string
			Params struct {
				CWD, SessionID, ConfigID, Value string
				MCPServers                      []json.RawMessage
				Prompt                          []struct{ Text string }
			}
		}
		if json.Unmarshal(in.Bytes(), &msg) != nil {
			os.Exit(3)
		}
		_, _ = log.WriteString(msg.Method + "\n")
		switch msg.Method {
		case "initialize":
			reply(msg.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "Antigravity ACP Server", "version": "1.5.15"},
				"agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"resume": map[string]any{}, "close": map[string]any{}, "additionalDirectories": map[string]any{}}, "promptCapabilities": map[string]bool{"image": true}}})
		case "session/new", "session/resume":
			if _, err := os.Stat(os.Getenv("SHIOJI_ACP_FIXTURE_LOG") + ".auth-required"); err == nil {
				_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32000, "message": "auth required"}})
				continue
			}
			if !filepath.IsAbs(msg.Params.CWD) || msg.Params.MCPServers == nil {
				os.Exit(4)
			}
			advertisedSession := "native-session"
			if _, err := os.Stat(os.Getenv("SHIOJI_ACP_FIXTURE_LOG") + ".foreign-compact"); err == nil {
				advertisedSession = "foreign-session"
			}
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": advertisedSession, "update": map[string]any{
					"sessionUpdate": "available_commands_update", "availableCommands": []map[string]string{{"name": "compact"}},
				},
			}})
			reply(msg.ID, map[string]any{"sessionId": "native-session", "configOptions": options()})
		case "session/close":
			reply(msg.ID, struct{}{})
		case "session/set_config_option":
			switch msg.Params.ConfigID {
			case "thought_level":
				effort = msg.Params.Value
			case "mode":
				mode = msg.Params.Value
			}
			reply(msg.ID, map[string]any{"configOptions": options()})
		case "session/cancel":
			if pendingCompact != nil {
				reply(pendingCompact, map[string]string{"stopReason": "cancelled"})
				pendingCompact = nil
			}
		case "session/prompt":
			if msg.Params.SessionID != "native-session" || effort != "high" || mode != "plan" || len(msg.Params.Prompt) == 0 {
				os.Exit(5)
			}
			if len(msg.Params.Prompt) == 1 && msg.Params.Prompt[0].Text == "/compact" {
				logPath := os.Getenv("SHIOJI_ACP_FIXTURE_LOG")
				if _, err := os.Stat(logPath + ".lose-compact"); err == nil {
					os.Exit(8)
				}
				if _, err := os.Stat(logPath + ".hold-compact"); err == nil {
					pendingCompact = msg.ID
					continue
				}
				reply(msg.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if msg.Params.Prompt[0].Text == "slow" {
				time.Sleep(300 * time.Millisecond)
				reply(msg.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if msg.Params.Prompt[0].Text == "start" {
				first = msg.ID
				update("agent_message_chunk", "primary continued")
			} else {
				reply(first, map[string]string{"stopReason": "end_turn"})
				if msg.Params.Prompt[0].Text == "reject" {
					_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32602, "message": "rejected fixture"}})
					continue
				}
				update("agent_message_chunk", "steer continued")
				reply(msg.ID, map[string]string{"stopReason": "end_turn"})
			}
		}
	}
	_ = log.Close()
	os.Exit(0)
}

func TestSteerReportsNativeRejectionAfterWireHandoff(t *testing.T) {
	a, _ := fixtureAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r, err := a.Start(ctx, harness.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	s := domain.Session{ID: "shioji", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "gemini-fixture", Effort: "high", Permission: "default", Plan: true}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := r.Submit(ctx, s, "op", harness.TurnInput{Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	deferred, ok := r.(harness.DeferredInputRuntime)
	if !ok {
		t.Fatal("native steer cannot settle separately")
	}
	receipt, err := deferred.SteerDeferred(ctx, s, turn, "rejected", harness.TurnInput{Text: "reject"}, nil)
	if err != nil {
		t.Fatal("wire handoff failed", err)
	}
	select {
	case result := <-receipt:
		if !harness.IsRejected(result) {
			t.Fatalf("separate steer receipt = %v", result)
		}
	case <-ctx.Done():
		t.Fatal("missing steer receipt")
	}
}

func TestPromptReceiptOutlivesPreparationTimeout(t *testing.T) {
	a, _ := fixtureAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r, err := a.Start(ctx, harness.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	s := domain.Session{ID: "shioji", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "gemini-fixture", Effort: "high", Permission: "default", Plan: true}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	preparation, stop := context.WithCancel(ctx)
	defer stop()
	_, receipt, err := r.(harness.DeferredInputRuntime).SubmitDeferred(preparation, s, "slow", harness.TurnInput{Text: "slow"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	select {
	case err := <-receipt:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("preparation context canceled the native reply")
	}
}

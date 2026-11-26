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
	if len(cached.Models) != 1 || hasMethod(log, "session/new") || cached.Account == nil || cached.Account.State != "authenticated" {
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

func TestCachedCatalogNormalizesLegacyAccountAndPreservesSubscription(t *testing.T) {
	a, log := fixtureAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dir := t.TempDir()
	const usageJSON = `{"status":"SUCCESS","num_turns":0,"command":{"name":"usage","data":{"groups":[{"name":"Gemini Models","buckets":[{"id":"g5h","window":"5h","remaining_fraction":0.8,"reset_time":"2026-11-30T12:00:00Z"}]}]}}}`
	if err := os.WriteFile(log+".usage", []byte(usageJSON), 0600); err != nil {
		t.Fatal(err)
	}
	fresh, err := a.Start(ctx, harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close(context.Background()) })
	ready := fresh.Readiness()
	if ready.Account == nil || ready.Account.State != "authenticated" || ready.Account.Subscription != "Google AI Pro" || len(ready.Account.QuotaWindows) != 0 {
		t.Fatalf("fresh discovery account = %+v", ready.Account)
	}
	catalogPath := filepath.Join(dir, "model-catalog.json")
	saved := readCatalog(catalogPath)
	if saved.Models.Account == nil || saved.Models.Account.State != "authenticated" || saved.Models.Account.Subscription != "Google AI Pro" || len(saved.Models.Account.QuotaWindows) != 0 {
		t.Fatalf("persisted catalog account = %+v", saved.Models.Account)
	}
	// Simulate an older worker's cached catalog written with state "configured".
	saved.Models.Account.State = "configured"
	if err := saveCatalog(dir, catalogPath, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	reopened, err := a.Start(ctx, harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	got := reopened.Readiness().Account
	if got == nil || got.State != "authenticated" || got.Subscription != "Google AI Pro" || len(got.QuotaWindows) != 0 {
		t.Fatalf("reopened cached account = %+v", got)
	}
	if hasMethod(log, "session/new") {
		t.Fatal("background cached catalog created an ACP session")
	}
}

func TestSubscriptionTierWithInstalledCLI(t *testing.T) {
	if os.Getenv("SHIOJI_TEST_AGY_SUBSCRIPTION") != "1" {
		t.Skip("requires an authorized device account with a subscription tier")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	dir := t.TempDir()
	adapter := Adapter{}
	catalog, err := adapter.Start(ctx, harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: true})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close(context.Background())
	account := catalog.Readiness().Account
	if account == nil || account.Subscription == "" || len(account.QuotaWindows) != 0 {
		t.Fatalf("real CLI account = %+v", account)
	}
	cached, err := adapter.Start(ctx, harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: false})
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close(context.Background())
	if got := cached.Readiness().Account; got == nil || got.Subscription != account.Subscription || len(got.QuotaWindows) != 0 {
		t.Fatalf("cached CLI account = %+v, want subscription %q", got, account.Subscription)
	}
	t.Logf("loaded native Antigravity subscription tier: %q", account.Subscription)
}

func TestYOLOIsAvailableInDiscoveredAndCachedCatalogs(t *testing.T) {
	a, _ := fixtureAdapter(t)
	dir := t.TempDir()
	for _, refresh := range []bool{true, false} {
		r, err := a.Start(t.Context(), harness.StartOptions{CatalogOnly: true, CatalogDirectory: dir, RefreshCatalog: refresh})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close(context.Background()) })
		if err := r.Validate(domain.RunConfig{Model: "gemini-fixture", Permission: "yolo"}); err != nil {
			t.Fatalf("YOLO unavailable with refresh=%v: %v", refresh, err)
		}
	}
}

func TestYOLOLaunchFlagOnlyForNewConversation(t *testing.T) {
	a, log := fixtureAdapter(t)
	choice := domain.RunConfig{Model: "gemini-fixture", Permission: "yolo"}
	for _, tc := range []struct {
		name    string
		options harness.StartOptions
		want    bool
	}{
		{name: "existing conversation"},
		{name: "new YOLO conversation", options: harness.StartOptions{InitialConfig: &choice}, want: true},
		{name: "catalog", options: harness.StartOptions{CatalogOnly: true, InitialConfig: &choice}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			r, err := a.Start(t.Context(), tc.options)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(context.Background())
			if !hasMethod(log, "initialize") || hasMethod(log, "--dangerously-skip-permissions") != tc.want {
				t.Fatalf("native launch flag present=%v, want %v", hasMethod(log, "--dangerously-skip-permissions"), tc.want)
			}
		})
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

func TestSteerReceiptSettlesWhileNativeReplyKeepsRunning(t *testing.T) {
	r, s, _, ctx := startPromptFixture(t)
	turn, err := r.Submit(ctx, s, "op", harness.TurnInput{Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"hold steer", "hold next steer"} {
		receipt, err := r.(harness.DeferredInputRuntime).SteerDeferred(ctx, s, turn, text, harness.TurnInput{Text: text}, nil)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-receipt:
			if result != nil {
				t.Fatalf("steer receipt = %v", result)
			}
		case <-time.After(time.Second):
			t.Fatal("steer receipt still waiting for the running prompt")
		}
		if got := r.(harness.PendingRuntime).PendingOperations(); got != 1 {
			t.Fatalf("native prompt should still be running, pending = %d", got)
		}
	}
}

func TestPromptInternalErrorAfterStopSettlesAsInterrupted(t *testing.T) {
	r, s, events, ctx := startPromptFixture(t)
	turn, err := r.Submit(ctx, s, "op", harness.TurnInput{Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Interrupt(ctx, s, turn); err != nil {
		t.Fatal(err)
	}
	waitPromptTerminal(t, ctx, events, turn, domain.ExecutionInterrupted)
}

func TestPromptInternalErrorAfterStopKeepsUnconfirmedDeliveryUncertain(t *testing.T) {
	r, s, events, ctx := startPromptFixture(t)
	turn, receipt, err := r.(harness.DeferredInputRuntime).SubmitDeferred(ctx, s, "op", harness.TurnInput{Text: "hold start"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Interrupt(ctx, s, turn); err != nil {
		t.Fatal(err)
	}
	waitPromptTerminal(t, ctx, events, turn, domain.ExecutionInterrupted)
	select {
	case err := <-receipt:
		if !harness.IsUncertain(err) {
			t.Fatalf("unconfirmed receipt = %v", err)
		}
	default:
		t.Fatal("missing delivery receipt")
	}
}

func TestPromptInternalErrorWithoutStopStillFails(t *testing.T) {
	r, s, events, ctx := startPromptFixture(t)
	turn, err := r.Submit(ctx, s, "op", harness.TurnInput{Text: "fail after output"})
	if err != nil {
		t.Fatal(err)
	}
	waitPromptTerminal(t, ctx, events, turn, domain.ExecutionFailed)
}

func startPromptFixture(t *testing.T) (harness.Runtime, domain.Session, <-chan domain.Event, context.Context) {
	t.Helper()
	a, _ := fixtureAdapter(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	t.Cleanup(cancel)
	events := make(chan domain.Event, 32)
	r, err := a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { events <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	s := domain.Session{ID: "shioji", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "gemini-fixture", Effort: "high", Permission: "default", Plan: true}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	return r, s, events, ctx
}

func waitPromptTerminal(t *testing.T, ctx context.Context, events <-chan domain.Event, turn string, want domain.ExecutionState) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("missing terminal turn")
		case event := <-events:
			if event.Turn == nil || event.Turn.Status.Active() {
				continue
			}
			if event.Turn.ID != turn || event.Turn.Status != want || want == domain.ExecutionInterrupted && event.Turn.Error != "" {
				t.Fatalf("terminal turn = %+v, want %s", event.Turn, want)
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
	if slices.Contains(os.Args, "--print") {
		if raw, err := os.ReadFile(os.Getenv("SHIOJI_ACP_FIXTURE_LOG") + ".usage"); err == nil {
			_, _ = os.Stdout.Write(raw)
			os.Exit(0)
		}
		os.Exit(1)
	}
	if !slices.Contains(os.Args, "acp") {
		return
	}
	log, err := os.OpenFile(os.Getenv("SHIOJI_ACP_FIXTURE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	if slices.Contains(os.Args, "--dangerously-skip-permissions") {
		_, _ = log.WriteString("--dangerously-skip-permissions\n")
	}
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	reject := func(id json.RawMessage, code int, message string) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: message}})
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
	var history historyFixture
	if raw, err := os.ReadFile(os.Getenv("SHIOJI_ACP_FIXTURE_LOG") + ".history"); err == nil {
		_ = json.Unmarshal(raw, &history)
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
		case "session/list":
			if history.Root == "" || os.Getenv("GEMINI_HOME") != history.Root {
				os.Exit(17)
			}
			reply(msg.ID, map[string]any{"sessions": []map[string]string{
				{"sessionId": fixtureHistoryID, "cwd": history.CWD, "title": "Fixture history", "updatedAt": "2026-11-26T00:00:00Z"},
				{"sessionId": "2544f711-8879-4358-a53a-43e27cc9d702", "cwd": history.Root, "title": "Foreign history"},
			}})
		case "session/load":
			home := os.Getenv("GEMINI_HOME")
			if history.Root == "" || home == history.Root || msg.Params.CWD == history.CWD || msg.Params.SessionID != fixtureHistoryID ||
				msg.Params.MCPServers == nil || len(msg.Params.MCPServers) != 0 {
				os.Exit(18)
			}
			if _, err := os.Stat(filepath.Join(home, "antigravity-cli", "conversations", fixtureHistoryID+".db")); err != nil {
				os.Exit(19)
			}
			token := filepath.Join(home, "antigravity-cli", "acp_token.json")
			if info, err := os.Stat(token); err != nil || info.Mode().Perm() != 0600 {
				os.Exit(20)
			}
			// Updates for a different session must not enter the preview.
			update("user_message_chunk", "foreign content")
			for _, raw := range []string{
				`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"first question"}}`,
				`{"sessionUpdate":"tool_call","toolCallId":"check","title":"Run command","kind":"execute","status":"in_progress","rawInput":{"command":"go test"}}`,
				`{"sessionUpdate":"tool_call_update","toolCallId":"check","status":"completed","rawOutput":{"combinedOutput":"verified"}}`,
				`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}}`,
				`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}}`,
				`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"second question"}}`,
				`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second answer"}}`,
			} {
				_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": fixtureHistoryID, "update": json.RawMessage(raw),
				}})
			}
			reply(msg.ID, map[string]any{"sessionId": fixtureHistoryID})
		case "initialize":
			sessionCaps := map[string]any{"resume": map[string]any{}, "close": map[string]any{}, "additionalDirectories": map[string]any{}}
			if history.Root != "" {
				sessionCaps["list"] = map[string]any{}
			}
			reply(msg.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "Antigravity ACP Server", "version": "1.5.15"},
				"agentCapabilities": map[string]any{"loadSession": history.Root != "", "sessionCapabilities": sessionCaps, "promptCapabilities": map[string]bool{"image": true}}})
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
			reply(msg.ID, map[string]any{
				"sessionId":     "native-session",
				"configOptions": options(),
				"_meta": map[string]any{
					"antigravity": map[string]any{
						"tier": map[string]any{"name": "Google AI Pro", "id": "g1-pro-tier"},
					},
				},
			})
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
			} else if first != nil {
				reject(first, -32603, "Internal error")
				first = nil
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
			switch msg.Params.Prompt[0].Text {
			case "start":
				first = msg.ID
				update("agent_message_chunk", "primary continued")
			case "hold start":
				first = msg.ID
			case "hold steer", "hold next steer":
				reply(first, map[string]any{"stopReason": "end_turn", "usage": map[string]int{"inputTokens": 5}})
				first = msg.ID
			case "reject":
				reject(msg.ID, -32602, "rejected fixture")
			case "fail after output":
				update("agent_message_chunk", "primary continued")
				reject(msg.ID, -32603, "Internal error")
			default:
				reply(first, map[string]string{"stopReason": "end_turn"})
				first = nil
				update("agent_message_chunk", "steer continued")
				reply(msg.ID, map[string]string{"stopReason": "end_turn"})
			}
		}
	}
	_ = log.Close()
	os.Exit(0)
}

func TestSteerReportsNativeRejectionAfterWireHandoff(t *testing.T) {
	r, s, _, ctx := startPromptFixture(t)
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

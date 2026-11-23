package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness/antigravity"
	"shioji/internal/protocol"
)

func TestAntigravityNativeSettingsWithIsolatedCLI(t *testing.T) {
	executable, profile, endpoint := os.Getenv("SHIOJI_TEST_AGY_WRAPPER"), os.Getenv("SHIOJI_TEST_AGY_PROFILE"), os.Getenv("SHIOJI_TEST_AGY_MCP")
	if executable == "" || profile == "" || endpoint == "" {
		t.Skip("requires an isolated AGY profile and loopback MCP fixture")
	}
	t.Setenv("GEMINI_HOME", profile)
	a := antigravity.Adapter{Executable: executable}
	s, w := memoryWorker(t, a)
	id := a.Descriptor().ID
	ready, err := s.recheckRuntime(id)
	if err != nil || ready.State != domain.RuntimeReady || !ready.Capabilities.ToolServices || !ready.Capabilities.HookManagement {
		t.Fatalf("real CLI did not expose configuration capabilities: %+v %v", ready, err)
	}
	tool, err := s.listToolServices(protocol.ToolServiceInput{HarnessID: id}, false)
	if err != nil || !tool.CanAdd || tool.CanRefresh {
		t.Fatalf("invalid native service catalog: %+v %v", tool, err)
	}
	change := domain.ToolServiceChange{Action: "save", Revision: tool.Revision,
		Configuration: &domain.ToolServiceConfiguration{Name: "shioji_native_fixture", Enabled: true, Transport: "http", URL: endpoint,
			Headers: []domain.ToolServiceValue{{Name: "X-Shioji-Fixture", Value: "fixture-value"}}}}
	if saved, err := s.changeToolService(protocol.ToolServiceInput{HarnessID: id, Change: &change}); err != nil || !saved.Saved {
		t.Fatalf("worker did not save native MCP definition: %+v %v", saved, err)
	}

	marker := filepath.Join(profile, "hook-native-marker")
	script := filepath.Join(profile, "config", "callback-fixture.sh")
	content := `#!/bin/sh
/usr/bin/touch ` + strconv.Quote(marker) + `
status=` + strconv.Quote(filepath.Join(profile, "hook-callback-status")) + `
if [ -z "$SHIOJI_CALLBACK_URL" ] || [ -z "$SHIOJI_CALLBACK_TOKEN" ]; then
  printf '%s\n' missing-env > "$status"
  exit 0
fi
http=$(/usr/bin/curl -q -sS --max-time 5 -w '%{http_code}' -H "Authorization: Bearer $SHIOJI_CALLBACK_TOKEN" -H "Content-Type: application/json" --data '{"id":"fixture-hook-one","text":"managed callback follow-up"}' "$SHIOJI_CALLBACK_URL")
code=$?
printf '%s:%s' "$http" "$code" > "$status"
`
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	group := map[string]any{"fixture_hook": map[string]any{"enabled": false, "PreInvocation": []map[string]any{
		{"type": "command", "command": "/bin/sh " + strconv.Quote(script), "timeout": 8},
	}}}
	raw, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	hookFile := filepath.Join(profile, "config", "hooks.json")
	if err := os.WriteFile(hookFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	hooks, err := s.listHooks(s.ctx, domain.HookRequest{HarnessID: id})
	if err != nil || len(hooks.Entries) != 1 {
		t.Fatalf("native Hook catalog: %+v %v", hooks, err)
	}
	hook := hooks.Entries[0]
	request := domain.HookRequest{HarnessID: id, HookChange: domain.HookChange{
		OperationID: domain.NewID(), ID: hook.ID, Action: domain.HookSetEnabled, Enabled: true,
		Revision: hooks.Revision, DefinitionRevision: hook.DefinitionRevision}}
	result, err := s.manageHooks("runtime.hooks.change", request)
	receipt, ok := result.(domain.HookOperation)
	if err != nil || !ok || !receipt.Saved || receipt.State != domain.OperationConfirmed {
		t.Fatalf("native Hook result: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("saving a Hook ran it: %v", err)
	}
	var cfg domain.RunConfig
	for _, model := range ready.Models {
		if model.Default {
			cfg = domain.RunConfig{Model: model.ID, Effort: model.DefaultEffort, Permission: "default"}
			break
		}
	}
	op, err := s.submit(domain.Submission{OperationID: "native-config-turn", WorkspaceID: w.ID, HarnessID: id, Config: cfg,
		Text: "Respond native-mcp-fixture-ok"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted := awaitReceipt(t, s, op.ID); accepted.State != domain.OperationConfirmed {
		t.Fatalf("next native session input: %+v", accepted)
	}
	awaitReloadLive(t, func() bool {
		d, err := s.store.Session(op.SessionID, 0, 20)
		return err == nil && d.Session.Status == domain.ExecutionCompleted
	})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("new native session did not adopt the saved Hook: %v", err)
	}
	status, _ := os.ReadFile(filepath.Join(profile, "hook-callback-status"))
	if string(status) != "202:0" {
		t.Fatalf("native callback admission did not reach the worker: %q", status)
	}
	awaitReloadLive(t, func() bool {
		d, err := s.store.Session(op.SessionID, 0, 20)
		if err != nil || len(d.Turns) != 2 || d.Session.Status != domain.ExecutionCompleted {
			return false
		}
		callbacks := 0
		for _, it := range d.Items {
			if it.Kind == "user" && it.Text == "managed callback follow-up" {
				callbacks++
			}
		}
		return callbacks == 1 && len(d.Queue.Items) == 0
	})
}

package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/modelapi"
)

func TestClaudeSDKProbeReportsSupportedPermissionsAndModels(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the SDK capability probe fixture")
	}

	writeFixture := func(allowYOLO, requireUnrestrictedYOLO bool) string {
		sdk := filepath.Join(t.TempDir(), "probe-sdk.mjs")
		body := `
export function query({ options }) {
  return {
    async initializationResult() {
      if (options.permissionMode === "bypassPermissions") {
        if (!` + jsonBool(allowYOLO) + ` || !options.allowDangerouslySkipPermissions) {
          throw new Error("bypass disabled");
        }
        if (` + jsonBool(requireUnrestrictedYOLO) + ` && Object.hasOwn(options.extraArgs ?? {}, "restricted")) {
          throw new Error("restricted conflicts with bypass");
        }
      }
      return { commands: [] };
    },
    async supportedModels() {
      if (options.model !== "claude-opus-4-6") return [];
      return [{ Value: "claude-opus-4-6", ResolvedModel: "claude-opus-4-6", SupportsEffort: true, SupportedEffortLevels: ["low", "high"], SupportsFastMode: true }];
    },
    async setPermissionMode(mode) {
      if (mode !== "acceptEdits" && mode !== "default") throw new Error("unexpected mode");
    },
    close() {},
  };
}
`
		if err := os.WriteFile(sdk, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return sdk
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	full := probeSDKCapabilities(ctx, node, writeFixture(true, true), "/fixture/claude", "claude-opus-4-6")
	if !full.Probed || !full.Permissions.ReadOnly || !full.Permissions.Default || !full.Permissions.AutoApprove ||
		!full.Permissions.AcceptEdits || !full.Permissions.YOLO || !full.Permissions.BypassPermissions || full.Permissions.BypassRestricted {
		t.Fatalf("unexpected full SDK capabilities: %+v", full)
	}
	if len(full.Models) != 1 || full.Models[0].Value != "claude-opus-4-6" {
		t.Fatalf("probe did not initialize the selected SDK model: %+v", full.Models)
	}
	perms, choices := permissionChoices(full.Permissions)
	if !slices.Equal(perms, []string{"read-only", "default", "auto-approve", "yolo"}) || len(choices) != 4 {
		t.Fatalf("unexpected full permission choices: %v %+v", perms, choices)
	}

	restricted := probeSDKCapabilities(ctx, node, writeFixture(false, false), "/fixture/claude", "claude-opus-4-6")
	if !restricted.Probed || restricted.Permissions.YOLO || restricted.Permissions.BypassPermissions {
		t.Fatalf("disabled bypass must not expose YOLO: %+v", restricted)
	}
	perms, choices = permissionChoices(restricted.Permissions)
	if !slices.Equal(perms, []string{"read-only", "default", "auto-approve"}) || len(choices) != 3 {
		t.Fatalf("unexpected restricted permission choices: %v %+v", perms, choices)
	}
}

func jsonBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestClaudeCapabilitiesApplyAPIProbeAndCache(t *testing.T) {
	home := t.TempDir()
	cfg := configuration{Endpoint: "https://proxy.example.test/v1", Model: "custom-claude-opus", Key: "fixture"}
	sdkCaps := sdkCapabilities{
		Probed: true,
		Models: []nativeModel{{Value: cfg.Model, SupportsEffort: true, SupportedEffortLevels: []string{"low", "max"}, SupportsFastMode: true}},
		Permissions: sdkPermissionCapabilities{
			ReadOnly: true, Default: true, AutoApprove: true, AcceptEdits: true, YOLO: true, BypassPermissions: true,
		},
	}

	// Custom endpoints fall back to exact-model SDK efforts independently of Fast.
	unprobed := applyCapabilities(Adapter{id: "p"}.readiness(cfg), cfg, modelapi.AnthropicCapabilities{}, sdkCaps)
	if !slices.Equal(unprobed.Models[0].Efforts, []string{"low", "max"}) || unprobed.Models[0].Fast {
		t.Fatalf("unexpected SDK fallback: %+v", unprobed.Models[0])
	}

	// Live API-probed effort and fast capabilities apply to custom endpoints and pass Validate.
	apiCaps := modelapi.AnthropicCapabilities{
		Probed:  true,
		Efforts: []string{"low", "medium", "high", "unknown"},
		Fast:    true,
	}
	ready := applyCapabilities(Adapter{id: "p"}.readiness(cfg), cfg, apiCaps, sdkCaps)
	if !slices.Equal(ready.Models[0].Efforts, []string{"low", "medium", "high"}) || !ready.Models[0].Fast {
		t.Fatalf("unexpected API-probed model options: %+v", ready.Models[0])
	}
	if !slices.Equal(ready.Permissions, []string{"read-only", "default", "auto-approve", "yolo"}) {
		t.Fatalf("unexpected permissions: %v", ready.Permissions)
	}

	r := runtime{config: cfg, ready: ready}
	if err := r.Validate(domain.RunConfig{Model: cfg.Model, Permission: "auto-approve", Effort: "high", Fast: true}); err != nil {
		t.Fatalf("probed run config rejected: %v", err)
	}
	if err := r.Validate(domain.RunConfig{Model: cfg.Model, Permission: "default", Effort: "max"}); err == nil {
		t.Fatal("unprobed effort level was accepted")
	}

	// Cache persists privately and invalidates on endpoint/model change.
	saveCapabilitiesCache(home, cachedCapabilities{
		Endpoint: cfg.Endpoint, Model: cfg.Model, RuntimeVersion: "v1", API: apiCaps, SDK: sdkCaps,
	})
	loaded, ok := loadCapabilitiesCache(home, cfg.Endpoint, cfg.Model)
	if !ok || !loaded.API.Probed || !loaded.API.Fast || !slices.Equal(loaded.API.Efforts, apiCaps.Efforts) || !loaded.SDK.Permissions.YOLO {
		t.Fatalf("capabilities cache mismatch: ok=%v loaded=%+v", ok, loaded)
	}
	if _, ok := loadCapabilitiesCache(home, cfg.Endpoint, "other-model"); ok {
		t.Fatal("cache matched a different model")
	}
	if _, ok := loadCapabilitiesCache(home, "https://other.example.test/v1", cfg.Model); ok {
		t.Fatal("cache matched a different endpoint")
	}
}

func TestClaudePermissionModesEnforceApprovalAndRestartBoundaries(t *testing.T) {
	runApproval := func(permission string, plan bool, tool string, input string) (wire map[string]any, events []domain.Event) {
		t.Helper()
		read, write := io.Pipe()
		defer read.Close()
		defer write.Close()

		var decoded map[string]any
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = json.NewDecoder(read).Decode(&decoded)
		}()

		turn := &activeTurn{
			turn:       domain.Turn{ID: "turn-1"},
			plan:       plan,
			permission: permission,
			items:      map[string]domain.Item{},
			tools:      map[string]string{"toolu_1": "item-1"},
		}
		r := runtime{
			input:              write,
			done:               make(chan struct{}),
			turn:               turn,
			launchedPermission: permission,
			approvals:          map[string]approval{},
			opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
				events = append(events, e)
				return nil
			}},
		}
		if err := r.openApproval(turn, frame{
			RequestID: "req-1", ToolName: tool, ToolUseID: "toolu_1", Input: json.RawMessage(input),
		}); err != nil {
			t.Fatal(err)
		}
		_ = write.Close()
		<-done
		return decoded, events
	}

	// Read-only denies writes without prompting.
	wire, events := runApproval(permissionReadOnly, false, "Write", `{"file_path":"/tmp/a","content":"x"}`)
	if wire["allow"] != false || len(events) != 0 {
		t.Fatalf("read-only must deny Write without opening approval: wire=%v events=%d", wire, len(events))
	}

	// Auto-approve allows Write without prompting.
	wire, events = runApproval(permissionAutoApprove, false, "Write", `{"file_path":"/tmp/a","content":"x"}`)
	if wire["allow"] != true || len(events) != 0 {
		t.Fatalf("auto-approve must allow Write without opening approval: wire=%v events=%d", wire, len(events))
	}

	// Auto-approve and YOLO still enforce Plan mode and still open AskUserQuestion forms.
	wire, events = runApproval(permissionAutoApprove, true, "Write", `{"file_path":"/tmp/a","content":"x"}`)
	if wire["allow"] != false || len(events) != 0 {
		t.Fatalf("plan mode must block Write even in auto-approve: wire=%v events=%d", wire, len(events))
	}
	wire, events = runApproval(permissionYOLO, true, "Bash", `{"command":"rm -rf /tmp/x"}`)
	if wire["allow"] != false || len(events) != 0 {
		t.Fatalf("plan mode must block Bash even in yolo: wire=%v events=%d", wire, len(events))
	}
	_, events = runApproval(permissionYOLO, false, "AskUserQuestion", `{"questions":[{"question":"Proceed?","header":"Confirm","options":[{"label":"Yes","description":"Continue"},{"label":"No","description":"Stop"}]}]}`)
	if len(events) != 1 || events[0].Request == nil || events[0].Request.Kind != "form" {
		t.Fatalf("yolo must still surface AskUserQuestion forms: %+v", events)
	}

	// Default mode opens an approval request card.
	_, events = runApproval(permissionDefault, false, "Write", `{"file_path":"/tmp/a","content":"x"}`)
	if len(events) != 1 || events[0].Request == nil || events[0].Request.Kind != "approval" {
		t.Fatalf("default mode must open approval card: %+v", events)
	}

	// Switching between permission modes requires restarting the bound Claude process.
	r := &runtime{
		process:            &exec.Cmd{},
		launchedPermission: permissionDefault,
		ready:              domain.Readiness{Permissions: []string{permissionReadOnly, permissionDefault, permissionAutoApprove, permissionYOLO}},
	}
	if r.PermissionRequiresRestart(domain.RunConfig{Permission: permissionDefault}) {
		t.Fatal("unchanged permission should not require restart")
	}
	if !r.PermissionRequiresRestart(domain.RunConfig{Permission: permissionAutoApprove}) ||
		!r.PermissionRequiresRestart(domain.RunConfig{Permission: permissionReadOnly}) ||
		!r.PermissionRequiresRestart(domain.RunConfig{Permission: permissionYOLO}) {
		t.Fatal("changing permission mode must require restarting the process with the new sandbox")
	}
}

func TestClaudeStartConfiguresSandboxAndSDKOptionsPerPermissionMode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js runs the bridge fixture")
	}

	cases := []struct {
		name       string
		permission string
		assertJS   string
	}{
		{
			name:       "read-only",
			permission: permissionReadOnly,
			assertJS: `
if (options.permissionMode !== "default") throw new Error("read-only permissionMode");
if (options.tools.includes("Write") || options.tools.includes("Edit") || options.tools.includes("Bash")) throw new Error("read-only exposed write tools");
if (!options.settings.sandbox.enabled || options.settings.sandbox.filesystem.allowWrite.length !== 0) throw new Error("read-only allowed writes");
if (!options.settings.permissions.deny.includes("Write") || !options.settings.permissions.deny.includes("Bash")) throw new Error("read-only missing deny rules");
if (!Object.hasOwn(options.extraArgs, "restricted")) throw new Error("read-only missing restricted");`,
		},
		{
			name:       "auto-approve",
			permission: permissionAutoApprove,
			assertJS: `
if (options.permissionMode !== "acceptEdits") throw new Error("auto-approve permissionMode=" + options.permissionMode);
if (!options.settings.sandbox.enabled || !options.settings.sandbox.autoAllowBashIfSandboxed || options.settings.sandbox.allowUnsandboxedCommands) throw new Error("auto-approve sandbox");
if (!Object.hasOwn(options.extraArgs, "restricted")) throw new Error("auto-approve missing restricted");`,
		},
		{
			name:       "yolo",
			permission: permissionYOLO,
			assertJS: `
if (options.permissionMode !== "bypassPermissions" || !options.allowDangerouslySkipPermissions) throw new Error("yolo permissionMode");
if (options.settings.sandbox.enabled !== false || !options.settings.sandbox.allowUnsandboxedCommands) throw new Error("yolo sandbox");
if (Object.hasOwn(options.extraArgs, "restricted")) throw new Error("yolo kept incompatible restricted flag");
if (!(options.settings.permissions?.deny ?? []).some((rule) => rule.startsWith("Read("))) throw new Error("yolo dropped private profile deny rule");`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			profiles := filepath.Join(cwd, "private-profiles")
			profile := filepath.Join(profiles, "current")
			sdk := filepath.Join(t.TempDir(), "fixture.mjs")
			body := `
export function query({ options }) {
  return {
    async initializationResult() {` + tc.assertJS + `},
    async *[Symbol.asyncIterator]() { await new Promise(() => {}); },
    close() { process.stdin.destroy(); },
  };
}`
			if err := os.WriteFile(sdk, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			sdkCaps := sdkCapabilities{
				Probed: true,
				Permissions: sdkPermissionCapabilities{
					ReadOnly: true, Default: true, AutoApprove: true, AcceptEdits: true, YOLO: true, BypassPermissions: true, BypassRestricted: false,
				},
			}
			perms, choices := permissionChoices(sdkCaps.Permissions)
			r := runtime{
				config:          configuration{Endpoint: "http://127.0.0.1:1", Model: "fixture"},
				ready:           domain.Readiness{Permissions: perms, PermissionChoices: choices, Models: []domain.Model{{ID: "fixture"}}},
				sdkCaps:         sdkCaps,
				home:            profile,
				privateProfiles: profiles,
				node:            node,
				sdk:             sdk,
				opts:            harness.StartOptions{SessionDirectory: t.TempDir()},
				done:            make(chan struct{}),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			defer func() { _ = r.Close(ctx) }()
			s := domain.Session{
				ID:      domain.NewID(),
				Config:  domain.RunConfig{Model: "fixture", Permission: tc.permission},
				Context: domain.LaunchContext{CWD: cwd},
			}
			if _, err := r.Create(ctx, s); err != nil {
				t.Fatal(err)
			}
		})
	}
}

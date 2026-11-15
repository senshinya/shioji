package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceToolInspectionSources(t *testing.T) {
	var config nativeConfiguration
	err := json.Unmarshal([]byte(`{"config":{"mcp_servers":{"files":{"command":"files"},"docs":{"url":"https://example.test/mcp?token=private","env":{"TOKEN":"secret"}}}},"layers":[{"name":{"type":"project","dotCodexFolder":"/device/project/.codex"},"config":{"mcp_servers":{"files":{"command":"files"}}}},{"name":{"type":"user","file":"/device/user/config.toml"},"config":{"mcp_servers":{"docs":{"url":"https://example.test/mcp?token=private","env":{"TOKEN":"secret"}}}}}]}`), &config)
	if err != nil {
		t.Fatal(err)
	}
	out := toolInspectionSources(config, "/device/project")
	if len(out.Entries) != 2 || out.RuntimeKnown {
		t.Fatal(out)
	}
	for _, e := range out.Entries {
		if e.Editable || e.CanAuthorize || e.State != domain.ToolServiceUnknown {
			t.Fatal("inspection presented a write or connection", e)
		}
		if e.ID == "files" && (e.Scope != domain.ToolServiceProject || e.ConfigurationPath != "/device/project/.codex/config.toml") {
			t.Fatal(e)
		}
		if e.ID == "docs" && (e.Scope != domain.ToolServiceUser || e.ConfigurationPath != "/device/user/config.toml") {
			t.Fatal(e)
		}
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "private") || strings.Contains(string(b), "secret") {
		t.Fatal("secrets escaped", string(b))
	}
	config.Layers[0].DisabledReason = new("project is not trusted")
	delete(config.Config.ToolServices, "files")
	out = toolInspectionSources(config, "/device/project")
	if len(out.Warnings) == 0 {
		t.Fatal("untrusted layer hidden")
	}
	for _, e := range out.Entries {
		if e.ID == "files" && e.IgnoredReason == "" {
			t.Fatal("ignored config claims adoption")
		}
	}
}

func TestWorkspaceToolInspectionNativeSessionDoesNotRequireAModelTurn(t *testing.T) {
	// Register the directory cleanup before the runtime cleanup: Codex can
	// still populate its project plugin cache until the process has exited.
	cwd := t.TempDir()
	peer := localToolService(t, false)
	r := nativeToolRuntime(t, peer.URL+"/mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var created struct{ Thread struct{ ID string } }
	if err := r.call(ctx, "thread/start", map[string]any{"cwd": cwd, "approvalPolicy": "never", "sandbox": "read-only"}, &created); err != nil {
		t.Fatal(err)
	}
	if created.Thread.ID == "" {
		t.Fatal("native thread reference missing")
	}
	out, err := r.InspectToolServices(ctx, domain.LaunchContext{CWD: cwd}, created.Thread.ID)
	if err != nil || !out.RuntimeKnown {
		t.Fatal(out, err)
	}
	found := false
	for _, e := range out.Entries {
		if e.ID == "fixture" {
			found = true
		}
	}
	if !found {
		t.Fatal("native session inventory missing", out)
	}
}

func TestWorkspaceToolInspectionReadsActualTrustedProjectWithoutStartingService(t *testing.T) {
	r := nativeToolRuntime(t, "http://127.0.0.1:1/mcp")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project := `[mcp_servers.project_probe]
command="/usr/bin/true"
enabled=false
`
	if err := os.WriteFile(filepath.Join(root, ".codex/config.toml"), []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	home := environmentValue(r.cmd.Env, "CODEX_HOME")
	f, err := os.OpenFile(filepath.Join(home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n[projects." + strconvQuote(root) + "]\ntrust_level=\"trusted\"\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.InspectToolServices(context.Background(), domain.LaunchContext{CWD: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range out.Entries {
		if e.ID == "project_probe" {
			found = true
			if e.Scope != domain.ToolServiceProject || e.Configuration == nil || e.Configuration.Enabled || e.ConfigurationPath != filepath.Join(root, ".codex/config.toml") {
				t.Fatal(e)
			}
		}
	}
	if !found || out.RuntimeKnown {
		t.Fatal(out)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex/config.toml"), []byte("[mcp_servers.broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = r.InspectToolServices(context.Background(), domain.LaunchContext{CWD: root}, "")
	if err != nil || len(out.Warnings) == 0 {
		t.Fatal("bad project configuration hid inherited sources", out, err)
	}
	found = false
	for _, e := range out.Entries {
		if e.ID == "fixture" {
			found = true
		}
	}
	if !found {
		t.Fatal("inherited user service lost", out)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestWorkspaceToolInspectionNeverFallsBackToGlobalStatus(t *testing.T) {
	r := toolPipe(t, "tools")
	r.protocol.scopedToolStatus = false
	out, err := r.InspectToolServices(context.Background(), domain.LaunchContext{CWD: "/device/project"}, "opaque-thread")
	if err != nil || out.RuntimeKnown || len(out.Warnings) == 0 {
		t.Fatal(out, err)
	}
	for _, e := range out.Entries {
		if e.State == domain.ToolServiceReady {
			t.Fatal("global status used for a session")
		}
	}
	r.protocol.scopedToolStatus = true
	out, err = r.InspectToolServices(context.Background(), domain.LaunchContext{CWD: "/device/project"}, "opaque-thread")
	if err != nil || !out.RuntimeKnown {
		t.Fatal(out, err)
	}
	found := false
	for _, e := range out.Entries {
		if e.ID == "docs" {
			found = e.State == domain.ToolServiceUnknown && len(e.Definitions) == 1
		}
	}
	if !found {
		t.Fatal(out)
	}
}

func TestWorkspaceToolInspectionKeepsRawSourcesAndHidesLiteralArguments(t *testing.T) {
	var c nativeConfiguration
	json.Unmarshal([]byte(`{"config":{"mcp_servers":{"same":{"command":"mcp-server","args":["--api-key","argument-secret"],"enabled":false}}},"layers":[{"name":{"type":"project","dotCodexFolder":"/project/.codex"},"config":{"mcp_servers":{"same":{"enabled":false}}}},{"name":{"type":"user","file":"/user/config.toml"},"config":{"mcp_servers":{"same":{"command":"mcp-server","args":["--api-key","argument-secret"]}}}}]}`), &c)
	out := toolInspectionSources(c, "/project")
	if len(out.Entries) != 2 {
		t.Fatal("same-name lower source disappeared", out)
	}
	for _, e := range out.Entries {
		if e.Scope == domain.ToolServiceProject && (e.Configuration.Command != "" || !e.PrimarySource || !e.EnabledConfigured) {
			t.Fatal("merged command attributed to project source", e)
		}
		if e.Scope == domain.ToolServiceUser && (e.Configuration.Command != "mcp-server" || e.PrimarySource || e.ArgumentCount != 2 || len(e.Configuration.Arguments) != 0 || e.EnabledConfigured) {
			t.Fatal("raw inherited source not represented", e)
		}
	}
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "argument-secret") {
		t.Fatal("literal secret leaked")
	}
	var command nativeConfiguration
	json.Unmarshal([]byte(`{"config":{},"layers":[{"name":{"type":"user","file":"/user/config.toml"},"config":{"mcp_servers":{"shell":{"command":"sh -c TOKEN=command-secret"}}}}]}`), &command)
	projected := toolInspectionSources(command, "/project")
	encoded, _ := json.Marshal(projected)
	if len(projected.Entries) != 1 || !projected.Entries[0].CommandHidden || strings.Contains(string(encoded), "command-secret") {
		t.Fatal("shell-style command leaked", string(encoded))
	}
}

func TestWorkspaceToolInspectionRequiresExplicitConnectionObservation(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want domain.ToolServiceState
	}{{"tools-scoped-connected", domain.ToolServiceReady}, {"tools-scoped-cached", domain.ToolServiceUnknown}} {
		r := toolPipe(t, tc.mode)
		r.protocol.scopedToolStatus = true
		out, err := r.InspectToolServices(context.Background(), domain.LaunchContext{CWD: "/project"}, "opaque-thread")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range out.Entries {
			if e.ID == "docs" {
				found = true
				if e.State != tc.want {
					t.Fatal(tc.mode, e.State)
				}
			}
		}
		if !found {
			t.Fatal(out)
		}
	}
}

package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
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
			found = e.State == domain.ToolServiceReady && len(e.Definitions) == 1
		}
	}
	if !found {
		t.Fatal(out)
	}
}

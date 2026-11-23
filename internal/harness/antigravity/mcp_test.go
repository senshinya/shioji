package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestNativeMCPConfigurationPreservesSecretsAndRejectsStaleEdits(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "config", "mcp_config.json")
	writeMCPFixture(t, path, `{"marker":{"keep":true},"mcpServers":{"remote":{"serverUrl":"https://example.invalid/mcp","headers":{"Authorization":"fixture-secret"},"oauth":{"clientSecret":"fixture-oauth"},"timeoutSeconds":47},"local":{"command":"helper","disabled":true}}}`)
	r := &runtime{skillProfile: profile}
	ctx := context.Background()
	provider, ok := any(r).(harness.ToolServiceRuntime)
	if !ok {
		t.Fatal("Antigravity does not expose the native MCP catalog")
	}
	catalog, err := provider.ToolServices(ctx)
	if err != nil || !catalog.CanAdd || catalog.CanRefresh || len(catalog.Services) != 2 {
		t.Fatalf("wrong configuration catalog: %+v %v", catalog, err)
	}
	service := catalog.Services[1]
	if service.ID != "remote" || service.State != domain.ToolServiceUnknown || service.CanAuthorize || !service.Editable ||
		service.Configuration == nil || len(service.Configuration.Headers) != 1 || !service.Configuration.Headers[0].Retained {
		t.Fatalf("unexpected projection: %+v", service)
	}
	data, _ := json.Marshal(catalog)
	if strings.Contains(string(data), "fixture-secret") || strings.Contains(string(data), "fixture-oauth") {
		t.Fatal("catalog disclosed private values")
	}
	editor := any(r).(harness.ToolServiceEditor)
	cfg := *service.Configuration
	cfg.URL = "https://different.example.invalid/mcp"
	if _, err := editor.ChangeToolService(ctx, domain.ToolServiceChange{
		Action: "save", ServiceID: service.ID, Revision: catalog.Revision, Configuration: &cfg,
	}); err == nil {
		t.Fatal("retained credentials would be forwarded to a new origin")
	}
	cfg.Name = "renamed"
	cfg.URL = "https://example.invalid/new-mcp"
	change := domain.ToolServiceChange{Action: "save", ServiceID: service.ID, Revision: catalog.Revision, Configuration: &cfg}
	saved, err := editor.ChangeToolService(ctx, change)
	if err != nil || saved.ServiceID != "renamed" || !saved.Saved {
		t.Fatalf("saving native definition: %+v %v", saved, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(after), "fixture-secret") || !strings.Contains(string(after), "fixture-oauth") ||
		!strings.Contains(string(after), "timeoutSeconds") || !strings.Contains(string(after), `"marker"`) ||
		!strings.Contains(string(after), `"renamed"`) || strings.Contains(string(after), `"remote"`) {
		t.Fatalf("native settings did not round-trip: %s %v", after, err)
	}
	if _, err = editor.ChangeToolService(ctx, change); err == nil {
		t.Fatal("stale revision overwrote the device configuration")
	}
}

func TestNativeMCPInspectionKeepsProjectAndDeviceSourcesSeparate(t *testing.T) {
	profile, project, extra := t.TempDir(), t.TempDir(), t.TempDir()
	personal := filepath.Join(profile, "config", "mcp_config.json")
	projectConfig := filepath.Join(project, ".agents", "mcp_config.json")
	writeMCPFixture(t, personal, `{"mcpServers":{"shared":{"command":"global"}}}`)
	writeMCPFixture(t, projectConfig, `{"mcpServers":{"shared":{"command":"project"},"project-only":{"serverUrl":"https://example.invalid/mcp?secret=fixture-url-secret"}}}`)
	aliasConfig := filepath.Join(extra, ".agent", "mcp_config.json")
	writeMCPFixture(t, aliasConfig, `{"mcpServers":{"extra-only":{"command":"extra"}}}`)
	r := &runtime{skillProfile: profile}
	ctx := context.Background()
	inspector, ok := any(r).(harness.ToolServiceInspector)
	if !ok {
		t.Fatal("Antigravity does not expose native MCP source inspection")
	}
	inspection, err := inspector.InspectToolServices(ctx, domain.LaunchContext{CWD: project, AdditionalDirectories: []string{extra}}, "")
	data, _ := json.Marshal(inspection)
	if err != nil || inspection.RuntimeKnown || len(inspection.Entries) != 4 || strings.Contains(string(data), "fixture-url-secret") {
		t.Fatalf("wrong source projections: %s %v", data, err)
	}
	byPath := map[string]int{}
	for _, entry := range inspection.Entries {
		byPath[entry.ConfigurationPath]++
		if entry.Scope == domain.ToolServiceProject && entry.Editable {
			t.Fatal("project definition can be edited via the device")
		}
	}
	if byPath[projectConfig] != 2 || byPath[personal] != 1 || byPath[aliasConfig] != 1 {
		t.Fatalf("device and project definitions were merged: %+v", inspection)
	}
}

func writeMCPFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMCPSavingCredentialsUsesAnOwnerOnlyFile(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "config", "mcp_config.json")
	writeMCPFixture(t, path, `{"mcpServers":{}}`)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	r := &runtime{skillProfile: profile}
	ctx := context.Background()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := domain.ToolServiceConfiguration{Enabled: true, Name: "private", Transport: "http",
		URL: "https://example.invalid/mcp", Headers: []domain.ToolServiceValue{{Name: "Authorization", Value: "test-secret"}}}
	if _, err := r.ChangeToolService(ctx, domain.ToolServiceChange{Action: "save", Revision: catalog.Revision, Configuration: &cfg}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("native MCP saved a credential with non-private permissions: %v %v", info, err)
	}
}

func TestNativeMCPRevisionCannotCrossAnIdenticalProfile(t *testing.T) {
	r := &runtime{skillProfile: t.TempDir()}
	ctx := context.Background()
	first, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.skillProfile = t.TempDir()
	change := domain.ToolServiceChange{Action: "save", Revision: first.Revision,
		Configuration: &domain.ToolServiceConfiguration{Enabled: true, Name: "private", Transport: "command", Command: "true"}}
	if _, err := r.ChangeToolService(ctx, change); err == nil {
		t.Fatal("a stale revision wrote an identical profile that has different ownership")
	}
}

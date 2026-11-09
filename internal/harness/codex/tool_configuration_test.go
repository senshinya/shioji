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

func TestNativeToolCatalogIncludesEditableConfigurationWithoutCredentials(t *testing.T) {
	r := nativeToolRuntime(t, localToolService(t, false).URL+"/mcp")
	home := environmentValue(r.cmd.Env, "CODEX_HOME")
	f, err := os.OpenFile(filepath.Join(home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("http_headers={X-Fixture=\"private-fixture-value\"}\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(catalog)
	if strings.Contains(string(b), "private-fixture-value") {
		t.Fatal("credential leaked in catalog")
	}
	var result struct {
		CanAdd   bool   `json:"canAdd"`
		Revision string `json:"revision"`
		Services []struct {
			Editable      bool `json:"editable"`
			Configuration *struct {
				Name, Transport string
				Headers         []struct {
					Name, Value string
					Retained    bool
				}
			} `json:"configuration"`
		} `json:"services"`
	}
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if !result.CanAdd || result.Revision == "" || len(result.Services) != 1 || !result.Services[0].Editable || result.Services[0].Configuration == nil {
		t.Fatalf("native configuration is not editable: %s", b)
	}
	c := result.Services[0].Configuration
	if c.Name != "fixture" || c.Transport != "http" || len(c.Headers) != 1 || c.Headers[0].Name != "X-Fixture" || !c.Headers[0].Retained || c.Headers[0].Value != "" {
		t.Fatalf("incorrect public projection: %s", b)
	}
}

func TestNativeToolConfigurationWritesPreserveSecretsAndRejectStaleRevision(t *testing.T) {
	r := nativeToolRuntime(t, localToolService(t, false).URL+"/mcp")
	path := filepath.Join(environmentValue(r.cmd.Env, "CODEX_HOME"), "config.toml")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("http_headers={X-Fixture=\"keep-private-value\"}\ntool_timeout_sec=13\n")
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := *catalog.Services[0].Configuration
	config.Name = "fixture.renamed"
	result, err := r.ChangeToolService(ctx, domain.ToolServiceChange{Action: "save", ServiceID: "fixture", Revision: catalog.Revision, Configuration: &config})
	if err != nil || !result.Saved {
		t.Fatal("rename failed", result, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "keep-private-value") || !strings.Contains(string(raw), "tool_timeout_sec = 13") {
		t.Fatal("edit lost a secret or an unexposed native setting", string(raw))
	}
	if _, err = r.ChangeToolService(ctx, domain.ToolServiceChange{Action: "delete", ServiceID: "fixture.renamed", Revision: catalog.Revision}); err == nil {
		t.Fatal("stale client removed newer configuration")
	}
	if err = r.RefreshToolServices(ctx); err != nil {
		t.Fatal(err)
	}
	catalog, err = r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 1 || catalog.Services[0].ID != "fixture.renamed" {
		t.Fatal("dotted identity split into nested config", catalog)
	}
	_, err = r.ChangeToolService(ctx, domain.ToolServiceChange{Action: "setEnabled", ServiceID: "fixture.renamed", Revision: catalog.Revision, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 1 || catalog.Services[0].State != domain.ToolServiceDisabled {
		t.Fatal("disabled service disappeared", catalog)
	}
	_, err = r.ChangeToolService(ctx, domain.ToolServiceChange{Action: "delete", ServiceID: "fixture.renamed", Revision: catalog.Revision})
	if err != nil {
		t.Fatal(err)
	}
	configNow, err := r.systemConfiguration(ctx)
	if err != nil || len(configNow.Config.ToolServices) != 0 {
		t.Fatal("delete left service configured", err)
	}
}

func TestToolHeaderNamesRejectCaseDuplicatesAndWhitespace(t *testing.T) {
	for _, rows := range [][]domain.ToolServiceValue{
		{{Name: "Authorization", Value: "a"}, {Name: "authorization", Value: "b"}},
		{{Name: "X Invalid", Value: "a"}},
	} {
		if _, err := mergeToolValues(rows, nil, true); err == nil {
			t.Fatal("invalid or ambiguous HTTP headers accepted")
		}
	}
}

func TestNativeToolConfigurationAddsDisabledCommandWithLiteralArguments(t *testing.T) {
	r := nativeToolRuntime(t, localToolService(t, false).URL+"/mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := domain.ToolServiceConfiguration{Name: "local-tool", Transport: "command", Command: "/bin/echo", Arguments: []string{"a value with spaces", "$(not-a-shell)"}, Enabled: false, Environment: []domain.ToolServiceValue{{Name: "FIXTURE_TOKEN", Value: "stored-only-on-device"}}}
	if _, err = r.ChangeToolService(ctx, domain.ToolServiceChange{Action: "save", Revision: catalog.Revision, Configuration: &config}); err != nil {
		t.Fatal(err)
	}
	catalog, err = r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range catalog.Services {
		if s.ID == "local-tool" {
			if s.State != domain.ToolServiceDisabled || s.Configuration == nil || len(s.Configuration.Arguments) != 2 || s.Configuration.Arguments[0] != "a value with spaces" || s.Configuration.Arguments[1] != "$(not-a-shell)" || !s.Configuration.Environment[0].Retained {
				t.Fatal("literal command configuration changed", s)
			}
			return
		}
	}
	t.Fatal("new disabled command missing from native catalog")
}

package mobileclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/worker"
)

func TestAuthenticatedControllerManagesOwningWorkerMCP(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Errorf("MCP configuration unexpectedly sent a model request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"fixture-model"}]}`))
	}))
	defer models.Close()
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	const retained = "fixture-header-kept-in-native-configuration"
	config := fmt.Sprintf(`
model = "fixture-model"
model_provider = "fixture"
[features]
plugins = false
plugin_discovery_prefetch = false
[model_providers.fixture]
name = "Fixture"
base_url = %q
wire_api = "responses"
requires_openai_auth = false
[mcp_servers.docs]
enabled = false
url = "http://127.0.0.1:9/mcp"
[mcp_servers.docs.http_headers]
X-Service-Key = %q
`, models.URL+"/v1", retained)
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "mcp-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	service, err := worker.New(ctx, worker.Config{Directory: dir, DefaultHarnessID: "codex",
		Harnesses: []harness.Adapter{codex.Adapter{Executable: binary, Home: home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	go service.Serve()
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	}
	client, err := protocol.Dial(ctx, dial, "")
	if err != nil {
		t.Fatal(err)
	}
	owner := client.DeviceID
	client.Close()
	controller := app.NewAuthenticatedDeviceController(t.TempDir(), owner, "verified-fixture",
		[]app.AuthenticatedDevicePath{{ID: "verified-fixture", Dial: dial}})
	done := make(chan struct{})
	go func() { defer close(done); controller.Run(ctx) }()
	defer func() { cancel(); <-done }()
	model := app.NewModel()
	nextUpdate := func() {
		t.Helper()
		select {
		case update := <-controller.Updates:
			update(model)
		case <-ctx.Done():
			t.Fatalf("controller did not respond: %v (%s)", ctx.Err(), model.Error)
		}
	}
	for !model.Connected || model.ConnectionEpoch == "" {
		nextUpdate()
	}
	call := func(method string, input, result any) error {
		t.Helper()
		finished := false
		var resultErr error
		if !controller.Do(app.Job{Method: method, ConnectionEpoch: model.ConnectionEpoch,
			Input: input,
			Apply: func(_ *app.Model, data json.RawMessage, e error) {
				if e == nil {
					e = json.Unmarshal(data, result)
				}
				resultErr, finished = e, true
			}}) {
			t.Fatalf("controller could not enqueue %s", method)
		}
		for !finished {
			nextUpdate()
		}
		return resultErr
	}
	list := func() domain.ToolServiceCatalog {
		t.Helper()
		var catalog domain.ToolServiceCatalog
		if err := call("runtime.tools.list", protocol.ToolServiceInput{HarnessID: "codex"}, &catalog); err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	mutate := func(change domain.ToolServiceChange) error {
		t.Helper()
		var result domain.ToolServiceChangeResult
		if err := call("runtime.tools.change", protocol.ToolServiceInput{HarnessID: "codex", Change: &change}, &result); err != nil {
			return err
		}
		if !result.Saved {
			return errors.New("missing durable save acknowledgement")
		}
		return nil
	}
	var readiness domain.Readiness
	if err := call("runtime.check", protocol.RuntimeConfiguration{HarnessID: "codex"}, &readiness); err != nil {
		t.Fatal(err)
	}
	if len(readiness.Harnesses) == 0 || !readiness.Harnesses[0].Capabilities.ToolServices {
		t.Fatalf("isolated agent cannot manage tool services: %+v", readiness.Harnesses)
	}
	initial := list()
	if !initial.CanAdd || initial.Revision == "" || len(initial.Services) != 1 || !initial.Services[0].Editable {
		t.Fatalf("expected editable device catalog, got %+v", initial)
	}
	settings := *initial.Services[0].Configuration
	if len(settings.Headers) != 1 || !settings.Headers[0].Retained || settings.Headers[0].Value != "" {
		t.Fatal("the owning device must return a retained header without its value")
	}
	settings.StartupSeconds = 15
	if err := mutate(domain.ToolServiceChange{Revision: initial.Revision, Action: "save", ServiceID: "docs", Configuration: &settings}); err != nil {
		t.Fatal(err)
	}
	if err := mutate(domain.ToolServiceChange{Revision: initial.Revision, Action: "delete", ServiceID: "docs"}); err == nil {
		t.Fatal("a stale write unexpectedly removed the existing service")
	}
	native, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(native), retained) {
		t.Fatal("an unrelated edit removed the original native credential")
	}
	revision := list().Revision
	newService := domain.ToolServiceConfiguration{Name: "review", Transport: "http", URL: "http://127.0.0.1:9/mcp", Enabled: false}
	if err := mutate(domain.ToolServiceChange{Revision: revision, Action: "save", Configuration: &newService}); err != nil {
		t.Fatal(err)
	}
	added := list()
	if len(added.Services) != 2 {
		t.Fatalf("expected two services after adding, got %d", len(added.Services))
	}
	if err := mutate(domain.ToolServiceChange{Revision: added.Revision, Action: "setEnabled", ServiceID: "review", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	var enabled domain.ToolServiceCatalog
	if err := call("runtime.tools.refresh", protocol.ToolServiceInput{HarnessID: "codex"}, &enabled); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, service := range enabled.Services {
		if service.ID == "review" {
			found = service.Configuration != nil && service.Configuration.Enabled
		}
	}
	if !found {
		t.Fatal("the owning Worker did not return the enabled configuration")
	}
	if err := mutate(domain.ToolServiceChange{Revision: enabled.Revision, Action: "delete", ServiceID: "review"}); err != nil {
		t.Fatal(err)
	}
	final := list()
	if model.Snapshot.DeviceID != owner || len(model.Snapshot.Sessions) != 0 ||
		len(final.Services) != 1 || final.Services[0].ID != "docs" {
		t.Fatal("MCP management changed the device or created a session")
	}
}

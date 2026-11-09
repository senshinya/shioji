package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

// A real HTTP MCP/OAuth peer for isolated native app-server integration. It
// exposes one tool and never makes a model request or authorizes a user account.
func localToolService(t *testing.T, auth bool) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, ".well-known/oauth-protected-resource"):
			json.NewEncoder(w).Encode(map[string]any{"resource": server.URL + "/mcp", "authorization_servers": []string{server.URL}, "scopes_supported": []string{"tools"}})
		case strings.Contains(r.URL.Path, ".well-known/oauth-authorization-server") || strings.Contains(r.URL.Path, ".well-known/openid-configuration"):
			json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "registration_endpoint": server.URL + "/register", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}})
		case r.URL.Path == "/register":
			var metadata map[string]any
			json.NewDecoder(r.Body).Decode(&metadata)
			metadata["client_id"] = "fixture-client"
			metadata["token_endpoint_auth_method"] = "none"
			json.NewEncoder(w).Encode(metadata)
		case r.URL.Path == "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-service-access", "refresh_token": "fixture-service-refresh", "token_type": "Bearer", "expires_in": 3600})
		case r.URL.Path == "/authorize":
			target, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			q := target.Query()
			q.Set("code", "fixture-code")
			q.Set("state", r.URL.Query().Get("state"))
			target.RawQuery = q.Encode()
			http.Redirect(w, r, target.String(), http.StatusFound)
		case r.URL.Path == "/mcp":
			if auth && r.Header.Get("Authorization") != "Bearer fixture-service-access" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"authorization_required"}`)
				return
			}
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			json.NewDecoder(r.Body).Decode(&request)
			if len(request.ID) == 0 {
				w.WriteHeader(202)
				return
			}
			var result any = map[string]any{}
			switch request.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture-docs", "title": "Fixture Documentation", "version": "1.0"}}
			case "tools/list":
				result = map[string]any{"tools": []any{map[string]any{"name": "lookup", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
			case "resources/list":
				result = map[string]any{"resources": []any{}}
			case "resources/templates/list":
				result = map[string]any{"resourceTemplates": []any{}}
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
func nativeToolRuntime(t *testing.T, endpoint string) *Runtime {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	home := t.TempDir()
	config := fmt.Sprintf("mcp_oauth_credentials_store=\"file\"\n[mcp_servers.fixture]\nurl=%q\n", endpoint)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	runtime, err := Start(ctx, Options{Executable: binary, Home: home, authentication: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}
func TestNativeToolServiceDiscoversActualHTTPTool(t *testing.T) {
	server := localToolService(t, false)
	r := nativeToolRuntime(t, server.URL+"/mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 1 || catalog.Services[0].State != domain.ToolServiceReady || catalog.Services[0].Tools != 1 {
		t.Fatalf("native discovery failed: %+v", catalog)
	}
	if err = r.RefreshToolServices(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("actual native MCP discovery and configuration reload verified; no model or user login request")
}
func TestNativeToolServiceAuthorizationCompletesAndDiscoversTools(t *testing.T) {
	server := localToolService(t, true)
	r := nativeToolRuntime(t, server.URL+"/mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 1 || catalog.Services[0].State != domain.ToolServiceAuthorizationRequired {
		t.Fatalf("missing authorization state: %+v", catalog)
	}
	login, challenge, err := r.BeginToolServiceAuthorization(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Callback == nil {
		t.Fatal("native callback missing")
	}
	response, err := http.Get(challenge.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err = login.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.RefreshToolServices(ctx); err != nil {
		t.Fatal(err)
	}
	catalog, err = r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 1 || catalog.Services[0].State != domain.ToolServiceReady || catalog.Services[0].Tools != 1 {
		t.Fatalf("authorization did not enable actual tools: %+v", catalog)
	}
	data, _ := json.Marshal(catalog)
	if strings.Contains(string(data), "fixture-service-access") || strings.Contains(string(data), "fixture-service-refresh") {
		t.Fatal("source credentials escaped adapter")
	}
}

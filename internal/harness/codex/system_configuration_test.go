package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSystemProviderSummaryUsesEffectiveProviderWithoutSecrets(t *testing.T) {
	var config nativeConfiguration
	json.Unmarshal([]byte(`{"config":{"model_provider":"private","model":"custom-model","model_providers":{"private":{"name":"Private API","base_url":"https://user:password@example.com/v1?key=hidden","env_key":"PRIVATE_KEY","experimental_bearer_token":"never-publish"}}},"layers":[{"name":{"type":"user","file":"/home/dev/.codex/personal.config.toml","profile":"personal"},"version":"v1"}]}`), &config)
	summary := config.summary(accountStatus{}, []string{"PRIVATE_KEY=also-secret"})
	if summary.Label != "Private API" || summary.Endpoint != "https://example.com/v1" || summary.Model != "custom-model" || summary.Credential != "configured" || summary.Profile != "personal" {
		t.Fatalf("wrong summary: %+v", summary)
	}
	data, _ := json.Marshal(summary)
	for _, secret := range []string{"never-publish", "password", "hidden", "also-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
}

func TestSystemProviderSummaryDoesNotTreatMissingEnvironmentAsNoConfiguration(t *testing.T) {
	var config nativeConfiguration
	json.Unmarshal([]byte(`{"config":{"model_provider":"private","model_providers":{"private":{"name":"Private API","env_key":"PRIVATE_KEY","base_url":"https://example.com/v1"}}}}`), &config)
	summary := config.summary(accountStatus{}, nil)
	if summary.State != "missingCredential" || summary.Credential != "missing" {
		t.Fatalf("missing credential misclassified: %+v", summary)
	}
	if config.canInitialize(accountStatus{}) {
		t.Fatal("must not overwrite an existing provider")
	}
}

func TestSystemAccountSummaryPreservesSubscription(t *testing.T) {
	var config nativeConfiguration
	json.Unmarshal([]byte(`{"config":{"model":"gpt-6.1-sol"}}`), &config)
	account := accountStatus{Account: json.RawMessage(`{"type":"chatgpt","email":"dev@example.com","planType":"pro"}`), RequiresOpenAIAuth: true}
	summary := config.summary(account, nil)
	if summary.Subscription != "Pro" || summary.Label != "ChatGPT" || config.canInitialize(account) {
		t.Fatalf("configured account lost: %+v", summary)
	}
}

func TestAPISetupWritesNativeConfigAndPreservesUnrelatedSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	os.WriteFile(path, []byte("# keep user settings\nmodel_reasoning_effort=\"medium\"\n[features]\nplugins=false\n"), 0600)
	server := apiSetupModelServer(t, "fixture-model")
	adapter := Adapter{Executable: executable, Home: home}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready, err := adapter.ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: server.URL + "/v1", Credential: "fake-api-only-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Account == nil || ready.Account.Mode != "api" || ready.Account.Model != "fixture-model" || ready.Account.Credential != "configured" {
		t.Fatalf("native config not read back: %+v", ready.Account)
	}
	found := false
	for _, model := range ready.Models {
		if model.ID == "fixture-model" {
			found = true
		}
	}
	if !found {
		t.Fatal("configured API model missing from catalog")
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("# keep user settings")) || !bytes.Contains(data, []byte("model_reasoning_effort")) || !bytes.Contains(data, []byte("fake-api-only-secret")) {
		t.Fatal("native config lost fields")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("credential file permissions", st.Mode())
	}
	if _, err = os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("custom API must not replace ChatGPT credential cache")
	}
	encoded, _ := json.Marshal(ready)
	if bytes.Contains(encoded, []byte("fake-api-only-secret")) {
		t.Fatal("credential escaped adapter")
	}
	if _, err = adapter.ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: "https://other.example/v1", Model: "other"}); err == nil {
		t.Fatal("overwrote existing system provider")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("rejected setup changed native config")
	}
}

func TestAPISetupRejectsUnsafeEndpointBeforeWriting(t *testing.T) {
	for _, endpoint := range []string{"https://user:secret@example.com/v1", "https://example.com/v1?key=secret", "file:///tmp/config"} {
		_, err := (Adapter{}).ConfigureAPI(context.Background(), domain.APIConfiguration{Endpoint: endpoint, Model: "m", Credential: "do-not-echo"})
		if err == nil || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatal("unsafe setup accepted", endpoint, err)
		}
	}
}

func TestAPISetupAcceptsNonLoopbackHTTP(t *testing.T) {
	in := domain.APIConfiguration{Endpoint: "http://203.0.113.42:8080/v1", Model: "m", Credential: "fixture"}
	if got, err := (Adapter{}).apiConfiguration(in); err != nil || got != in {
		t.Fatalf("API configuration rejected non-loopback HTTP: %v", err)
	}
}

func TestAPISetupUsesInheritedSystemHome(t *testing.T) {
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := apiSetupModelServer(t, "inherited-model")
	ready, err := (Adapter{Executable: executable}).ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: server.URL + "/v1", Model: "inherited-model"})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Account.ConfigurationFile == "" || ready.Account.Model != "inherited-model" || ready.Account.Credential != "notRequired" {
		t.Fatal("effective system home not followed", ready.Account)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || !bytes.Contains(data, []byte("inherited-model")) {
		t.Fatal("did not write inherited system home", err)
	}
}

func apiSetupModelServer(t *testing.T, model string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Error("unexpected request", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": model}}})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestBuiltInProviderSummaryDoesNotBecomeAnAccount(t *testing.T) {
	var config nativeConfiguration
	json.Unmarshal([]byte(`{"config":{"model_provider":"ollama","model":"local-model"}}`), &config)
	summary := config.summary(accountStatus{Account: json.RawMessage(`null`)}, []string{"CODEX_OSS_BASE_URL=http://127.0.0.1:19999/v1"})
	if summary.Mode != "api" || summary.Label != "ollama" || summary.Model != "local-model" || summary.Endpoint != "http://127.0.0.1:19999/v1" {
		t.Fatal("built-in provider hidden", summary)
	}
}
func TestSystemAPIUsesNativeStoredKeyAndActualModelIDs(t *testing.T) {
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	home := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "unrelated-env-key")
	t.Setenv("OPENAI_BASE_URL", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer native-fixture-key" {
			t.Error("incorrect native credential discovery")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"actually-available"}]}`))
	}))
	defer server.Close()
	config := fmt.Sprintf("model=\"unavailable-old-model\"\nmodel_provider=\"existing\"\n[model_providers.existing]\nname=\"Existing API\"\nbase_url=%q\nrequires_openai_auth=true\n", server.URL+"/v1")
	os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600)
	os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"native-fixture-key"}`), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime, err := Start(ctx, Options{Executable: executable, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ready := runtime.Readiness()
	if len(ready.Models) != 1 || ready.Models[0].ID != "actually-available" {
		t.Fatal("bundled/unavailable model leaked into available list", ready.Models)
	}
}

func TestNativeAccountAuthorizationRemainsAnAccountBehindGateway(t *testing.T) {
	var config nativeConfiguration
	json.Unmarshal([]byte(`{"config":{"model_provider":"gateway","model_providers":{"gateway":{"name":"Gateway","base_url":"https://gateway.example/v1","requires_openai_auth":true}}}}`), &config)
	account := accountStatus{RequiresOpenAIAuth: true, Account: json.RawMessage(`{"type":"chatgpt","email":"dev@example.com","planType":"pro"}`)}
	summary := config.summary(account, nil)
	if summary.Mode != "account" || summary.Label != "ChatGPT" || summary.Subscription != "Pro" || summary.Identity != "dev@example.com" {
		t.Fatal("native account authorization was replaced by an API key UI", summary)
	}
}

func TestAPISetupPreservesConfirmedConfigurationWhenModelRefreshFails(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"actual-model"}]}`)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = (Adapter{Executable: executable, Home: t.TempDir()}).ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: server.URL + "/v1"})
	var setup *harness.SetupError
	if !errors.As(err, &setup) || setup.Account == nil || setup.Account.Mode != "api" || setup.Account.Model != "actual-model" || setup.Account.State != "notRequired" {
		t.Fatalf("confirmed config lost on discovery error: %+v", err)
	}
}

func TestAPISetupDoesNotOverrideAuthorizationArrivingDuringDiscovery(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"concurrent-native-key"}`), 0600)
		fmt.Fprint(w, `{"data":[{"id":"actual-model"}]}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = (Adapter{Executable: executable, Home: home}).ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: server.URL + "/v1", Credential: "draft-key"})
	if err == nil {
		t.Fatal("overrode authorization created during discovery")
	}
	data, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if bytes.Contains(data, []byte("model_provider")) || bytes.Contains(data, []byte("draft-key")) {
		t.Fatal("native account overwritten")
	}
}

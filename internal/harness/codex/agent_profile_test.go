package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestCodexProfileUsesPrivateHomeAndIgnoresInheritedCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-default-account")
	t.Setenv("OPENAI_BASE_URL", "https://provider.invalid")
	t.Setenv("CODEX_HOME", t.TempDir())
	homes := []string{t.TempDir(), t.TempDir()}
	provider, ok := any(Adapter{}).(harness.AgentProfileProvider)
	if !ok {
		t.Fatal("Codex does not support isolated profiles")
	}
	for i, id := range []string{"work", "personal"} {
		instance, err := provider.WithAgentProfile(id, id, homes[i], domain.AgentProfileAccount)
		if err != nil {
			t.Fatal(err)
		}
		adapter := instance.(Adapter)
		env, err := adapter.environment(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := environmentValue(env, "CODEX_HOME"); filepath.Clean(got) != homes[i] {
			t.Fatalf("profile %s used home %q", id, got)
		}
		if environmentValue(env, "OPENAI_API_KEY") != "" || environmentValue(env, "OPENAI_BASE_URL") != "" {
			t.Fatal("profile inherited credentials or endpoint")
		}
		if got := instance.Descriptor().ID; got != id {
			t.Fatalf("profile lost routing ID: %q", got)
		}
	}
}

func TestManagedCodexAPIProfilesHaveIndependentNativeConfiguration(t *testing.T) {
	executable, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	legacy := t.TempDir()
	t.Setenv("CODEX_HOME", legacy)
	t.Setenv("OPENAI_API_KEY", "synthetic-default-account")
	const legacyConfig = "model = \"legacy-model\"\n"
	path := filepath.Join(legacy, "config.toml")
	if err := os.WriteFile(path, []byte(legacyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for _, name := range []string{"work", "personal"} {
		home := t.TempDir()
		configured, err := (Adapter{Executable: executable}).WithAgentProfile(name, name, home, domain.AgentProfileAPI)
		if err != nil {
			t.Fatal(err)
		}
		adapter := configured.(Adapter)
		if _, _, err := adapter.BeginSignIn(ctx, domain.SignInBrowser); err == nil {
			t.Fatal("API profile allowed an account login")
		}
		model, credential := name+"-fixture-model", name+"-fixture-key"
		endpoint := apiSetupModelServer(t, model)
		ready, err := adapter.ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: endpoint.URL + "/v1", Credential: credential})
		if err != nil {
			t.Fatal(err)
		}
		if ready.Account == nil || ready.Account.Model != model || ready.Account.Endpoint != endpoint.URL+"/v1" {
			t.Fatalf("wrong native account: %+v", ready.Account)
		}
		nativePath := filepath.Join(home, "config.toml")
		data, err := os.ReadFile(nativePath)
		if err != nil || !strings.Contains(string(data), credential) || strings.Contains(string(data), "synthetic-default-account") {
			t.Fatal("native configuration failed to isolate this credential")
		}
		if stat, err := os.Stat(nativePath); err != nil || stat.Mode().Perm() != 0600 {
			t.Fatalf("unexpected credential file permissions: %v, %v", stat, err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != legacyConfig {
		t.Fatalf("legacy config changed: %q, %v", data, err)
	}
}

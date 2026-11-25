package antigravity

import (
	"context"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

func TestAntigravityProfileUsesTheSameFileAuthScopeForACPAndSignIn(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "synthetic-other-account")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/another-account")
	provider, ok := any(Adapter{}).(harness.AgentProfileProvider)
	if !ok {
		t.Fatal("Antigravity does not support isolated profiles")
	}
	dir := t.TempDir()
	instance, err := provider.WithAgentProfile("example", "Test", dir, domain.AgentProfileAccount)
	if err != nil {
		t.Fatal(err)
	}
	a := instance.(Adapter)
	env, err := a.environment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"GEMINI_HOME": dir, "AGY_ACP_FORCE_FILE_STORAGE": "1", "SSH_CONNECTION": "127.0.0.1 0 127.0.0.1 0",
		"GEMINI_API_KEY": "", "GOOGLE_APPLICATION_CREDENTIALS": "",
	} {
		if got := systemexec.EnvironmentValue(env, key); got != want {
			t.Errorf("%s got %q, want %q", key, got, want)
		}
	}
	if a.Descriptor().ID != "example" {
		t.Fatal("managed profile lost its routing ID")
	}
}

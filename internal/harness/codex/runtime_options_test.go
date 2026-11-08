package codex

import (
	"shioji/internal/domain"
	"testing"
)

func TestRuntimeOptionsAreTranslatedAndCannotReplaceManagedTransport(t *testing.T) {
	a := Adapter{Executable: "codex", Home: "original"}
	configured, err := a.WithRuntimeOptions(domain.RuntimeOptions{Arguments: []string{"--profile", "personal"}, Directory: t.TempDir(), Environment: []domain.EnvironmentVariable{{Name: "HTTP_PROXY", Value: "http://localhost:7890"}}, Instructions: "Explain changes", TitleModel: "luna"})
	if err != nil {
		t.Fatal(err)
	}
	b := configured.(Adapter)
	if b.Home == "original" || b.runtimeOptions.TitleModel != "luna" {
		t.Fatal("options ignored", b)
	}
	if a.Home != "original" {
		t.Fatal("mutated old adapter")
	}
	if _, err = a.WithRuntimeOptions(domain.RuntimeOptions{Arguments: []string{"--listen", "tcp://0.0.0.0"}}); err == nil {
		t.Fatal("overrode worker-owned transport")
	}
	if _, err = a.WithRuntimeOptions(domain.RuntimeOptions{Environment: []domain.EnvironmentVariable{{Name: "CODEX_HOME", Value: "elsewhere"}}}); err == nil {
		t.Fatal("reserved variable bypassed config directory")
	}
}

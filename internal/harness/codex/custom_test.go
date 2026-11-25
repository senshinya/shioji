package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestCustomResponsesUsesVerifiedManualModelWithoutCatalog(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex app-server not installed")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			http.Error(w, "bad auth", 401)
			return
		}
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var request struct{ Model string }
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "manual-model" {
			t.Errorf("incorrect Responses verification model")
		}
		fmt.Fprint(w, `{"object":"response","status":"completed"}`)
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := NewResponsesAdapter(binary, t.TempDir())
	p, err := a.WithAgentProfile("manual-profile", "Manual", t.TempDir(), domain.AgentProfileAPI)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := p.(harness.APIConfigurationProvider).ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: provider.URL + "/v1", Credential: "fixture", Model: "manual-model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Models) != 1 || ready.Models[0].ID != "manual-model" || !ready.Models[0].Default {
		t.Fatalf("verified manual model missing from catalog: %+v", ready.Models)
	}
}

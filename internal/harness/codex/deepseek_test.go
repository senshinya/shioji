package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestDeepSeekProfileUsesFixedEndpointAndStreamsStatelessTurns(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex app-server is unavailable")
	}
	const key = "fixture-deepseek-key"
	var turns atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing provider credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"deepseek-v4-pro"},{"id":"deepseek-flash","name":"DeepSeek Flash"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/responses":
			var request struct {
				Model    string          `json:"model"`
				Input    json.RawMessage `json:"input"`
				Previous string          `json:"previous_response_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			turn := turns.Add(1)
			if request.Model != "deepseek-flash" || request.Previous != "" || !strings.Contains(string(request.Input), "FIRST_DEEPSEEK_INPUT") {
				t.Errorf("model or full stateless history missing from turn %d", turn)
			}
			if turn == 2 && !strings.Contains(string(request.Input), "SECOND_DEEPSEEK_INPUT") {
				t.Error("second turn was not included")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r%d\"}}\n\n", turn)
			fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m%d\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"DeepSeek fixture\"}]}}\n\n", turn)
			fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r%d\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n", turn)
		default:
			t.Errorf("unexpected provider method or route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(provider.Close)

	base := NewDeepSeekAdapter(binary)
	if base.apiPreset.endpoint != "https://api.deepseek.com" {
		t.Fatal("unexpected public service endpoint")
	}
	if _, err := base.APIModels(context.Background(), domain.APIConfiguration{Endpoint: provider.URL, Credential: key}); err == nil {
		t.Fatal("accepted a caller-supplied server")
	}
	base.apiPreset.endpoint = provider.URL
	home := t.TempDir()
	nativePath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(nativePath, []byte("[features]\nplugins=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	const profileID = "deepseek-profile"
	profile, err := base.WithAgentProfile(profileID, "DeepSeek fixture", home, domain.AgentProfileAPI)
	if err != nil {
		t.Fatal(err)
	}
	adapter := profile.(Adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err := adapter.ConfigureAPI(ctx, domain.APIConfiguration{}); err == nil {
		t.Fatal("accepted missing API Key")
	}
	ready, err := adapter.ConfigureAPI(ctx, domain.APIConfiguration{Credential: key})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Account == nil || ready.Account.Model != "deepseek-flash" || ready.Account.State != "authenticated" {
		t.Fatal("worker did not verify the selected model and authentication")
	}
	if descriptor := adapter.Descriptor(); descriptor.ID != profileID || descriptor.Icon != "deepseek" {
		t.Fatal("profile lost its distinct execution identity")
	}
	completed := make(chan struct{}, 4)
	run, err := adapter.Start(ctx, harness.StartOptions{OnEvent: func(event domain.Event) error {
		if event.Turn != nil && event.Turn.Status == "completed" {
			completed <- struct{}{}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close(context.Background())
	session := domain.Session{ID: "fixture", HarnessID: profileID, Context: domain.LaunchContext{CWD: t.TempDir()},
		Config: domain.RunConfig{Model: "deepseek-flash", Permission: "read-only"}}
	session.ThreadID, err = run.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	for i, message := range []string{"FIRST_DEEPSEEK_INPUT", "SECOND_DEEPSEEK_INPUT"} {
		if _, err := run.Submit(ctx, session, fmt.Sprintf("turn-%d", i), harness.TurnInput{Text: message}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal("DeepSeek Responses stream did not complete")
		}
	}
	if turns.Load() != 2 {
		t.Fatalf("unexpected number of provider calls: %d", turns.Load())
	}
}

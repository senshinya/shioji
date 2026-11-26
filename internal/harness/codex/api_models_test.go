package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestAPIModelEffortDeclarationTakesPrecedenceOverAppServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"override","supported_reasoning_efforts":[{"reasoning_effort":"xhigh"}],"default_reasoning_effort":"xhigh"},{"id":"fallback"},{"id":"disabled","supported_reasoning_efforts":[]}]}`))
	}))
	defer server.Close()
	known := []domain.Model{{ID: "override", Efforts: []string{"medium"}, DefaultEffort: "medium", Fast: true}, {ID: "fallback", Efforts: []string{"high"}}, {ID: "disabled", Efforts: []string{"high"}, DefaultEffort: "high"}}
	models, err := fetchAPIModels(context.Background(), server.URL, http.Header{}, known)
	if err != nil || len(models) != 3 {
		t.Fatal(models, err)
	}
	if !slices.Equal(models[0].Efforts, []string{"xhigh"}) || models[0].DefaultEffort != "xhigh" || !models[0].Fast || !slices.Equal(models[1].Efforts, []string{"high"}) || len(models[2].Efforts) != 0 || models[2].DefaultEffort != "" {
		t.Fatalf("unexpected effort choices: %+v", models)
	}
}

func TestCodexProfileEffortOverrideUpdatesRuntimeAndCatalogValidation(t *testing.T) {
	home := t.TempDir()
	ready := domain.Readiness{State: domain.RuntimeReady, Permissions: []string{"default"},
		Models: []domain.Model{{ID: "custom", Efforts: []string{"low", "high"}, DefaultEffort: "high"}}}
	live := &Runtime{effortHome: home, ready: ready}
	catalog := &catalogRuntime{adapter: Adapter{profileID: "profile", Home: home}, ready: ready}
	path := filepath.Join(home, harness.ProfileEffortsFile)
	if err := os.WriteFile(path, []byte(`{"custom":["xhigh"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []interface {
		Validate(domain.RunConfig) error
		Readiness() domain.Readiness
	}{live, catalog} {
		model := runtime.Readiness().Models[0]
		config := domain.RunConfig{Model: "custom", Permission: "default", Effort: "xhigh"}
		if model.DefaultEffort != "" || runtime.Validate(config) != nil {
			t.Fatalf("manual effort not applied: %+v", model)
		}
		config.Effort = "high"
		if runtime.Validate(config) == nil {
			t.Fatal("old effort survived the override")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if live.Readiness().Models[0].DefaultEffort != "high" || catalog.Readiness().Models[0].DefaultEffort != "high" {
		t.Fatal("automatic defaults did not return after manual override was removed")
	}
}

func TestAPIModelsUsesAuthenticatedRemoteListInsteadOfBundledModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("wrong provider request", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"available-model"},{"id":"available-model"},{"id":"second-model"}]}`))
	}))
	defer server.Close()
	models, err := (Adapter{}).APIModels(context.Background(), domain.APIConfiguration{Endpoint: server.URL + "/v1", Credential: "fixture-key"})
	if err != nil || len(models) != 2 || models[0].ID != "available-model" || models[1].ID != "second-model" {
		t.Fatal("did not use actual list", models, err)
	}
}
func TestAPIModelsRejectsRedirectsWithoutForwardingCredential(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	_, err := (Adapter{}).APIModels(context.Background(), domain.APIConfiguration{Endpoint: server.URL, Credential: "fixture-key"})
	if err == nil || forwarded {
		t.Fatal("redirect followed")
	}
}

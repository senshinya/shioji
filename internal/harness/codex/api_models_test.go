package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"shioji/internal/domain"
	"testing"
)

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

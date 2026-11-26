package modelapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"shioji/internal/domain"
)

func TestNormalizeAcceptsHTTPOnAnyHost(t *testing.T) {
	for _, endpoint := range []string{"http://203.0.113.42:8080/v1", "http://[2001:db8::42]:8080/v1", "http://192.168.1.42/v1", "http://api.example.test/v1", "http://127.0.0.1/v1"} {
		in := domain.APIConfiguration{Endpoint: endpoint, Model: "m", Credential: "fixture"}
		if got, err := Normalize(in); err != nil || got != in {
			t.Errorf("Normalize(%q) = %+v, %v", endpoint, got, err)
		}
	}
}

func TestListsResponsesAndPaginatedAnthropicModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/custom/v1/models" && r.Header.Get("Authorization") == "Bearer fixture":
			fmt.Fprint(w, `{"data":[{"id":"response-model"}]}`)
		case r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") == "fixture" && r.Header.Get("anthropic-version") != "":
			if r.URL.Query().Get("after_id") == "a" {
				fmt.Fprint(w, `{"data":[{"id":"b","display_name":"Second"}],"has_more":false}`)
			} else {
				fmt.Fprint(w, `{"data":[{"id":"a","display_name":"First"}],"has_more":true,"last_id":"a"}`)
			}
		default:
			t.Errorf("unexpected model request %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	first, err := Models(context.Background(), domain.AgentResponses, domain.APIConfiguration{Endpoint: server.URL + "/custom/v1", Credential: "fixture"})
	if err != nil || len(first) != 1 || first[0].ID != "response-model" {
		t.Fatal(first, err)
	}
	for _, endpoint := range []string{server.URL, server.URL + "/v1/"} {
		list, err := Models(context.Background(), domain.AgentAnthropic, domain.APIConfiguration{Endpoint: endpoint, Credential: "fixture"})
		if err != nil || len(list) != 2 || list[1].Name != "Second" {
			t.Fatal(list, err)
		}
	}
}

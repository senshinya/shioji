package codex

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"slices"
	"strings"
	"testing"
)

func TestCatalogStartsWithUnconfirmedAccountOnlyForTransientWorkspaceRouting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		account   wire.Message
		wantReady bool
		wantIssue domain.RuntimeIssue
	}{
		{name: "routing failed", account: wire.Message{Error: &wire.RPCError{Code: -32603, Message: "workspace routing discovery failed"}}, wantReady: true},
		{name: "routing timed out", account: wire.Message{Error: &wire.RPCError{Code: -32603, Message: "workspace routing discovery timed out"}}, wantReady: true},
		{name: "token rejected", account: wire.Message{Error: &wire.RPCError{Code: -32603, Message: "workspace routing discovery unauthorized (401)"}}, wantIssue: domain.RuntimeAuthenticationExpired},
		{name: "signed out", account: wire.Message{Result: json.RawMessage(`{"account":null,"requiresOpenaiAuth":true}`)}, wantIssue: domain.RuntimeSignInRequired},
		{name: "unknown internal error", account: wire.Message{Error: &wire.RPCError{Code: -32603, Message: "workspace routing discovery failed: unexpected detail"}}},
		{name: "other error code", account: wire.Message{Error: &wire.RPCError{Code: -32602, Message: "workspace routing discovery failed"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runtime{
				protocol: &protocolSupport{methods: map[string]bool{"account/read": true, "config/read": true}},
				pending:  map[string]chan wire.Message{},
				done:     make(chan struct{}),
			}
			r.stdin = workInputWriter{write: func(data []byte) (int, error) {
				var request wire.Message
				if err := json.Unmarshal(data, &request); err != nil {
					t.Fatal(err)
				}
				var response wire.Message
				switch request.Method {
				case "account/read":
					response = tc.account
				case "config/read":
					response.Result = json.RawMessage(`{"config":{"model_provider":"openai"}}`)
				case "model/list":
					response.Result = json.RawMessage(`{"data":[{"model":"fixture-model","displayName":"Fixture","isDefault":true}]}`)
				case "configRequirements/read":
					response.Result = json.RawMessage(`{"requirements":{"allowedSandboxModes":["read-only"]}}`)
				default:
					t.Fatalf("unexpected request %q", request.Method)
				}
				r.pending[string(request.ID)] <- response
				return len(data), nil
			}}
			err := r.catalog(context.Background())
			if tc.wantReady {
				if err != nil {
					t.Fatalf("transient account error blocked startup: %v", err)
				}
				ready := r.Readiness()
				if ready.State != domain.RuntimeReady || ready.Account != nil ||
					len(ready.Models) != 1 || ready.Models[0].ID != "fixture-model" ||
					!slices.Equal(ready.Permissions, []string{"read-only"}) {
					t.Fatalf("catalog must keep account unknown and use native models and policy: %+v", ready)
				}
				return
			}
			if err == nil {
				t.Fatal("unexpected startup with an account error")
			}
			var setup *harness.SetupError
			isSetup := errors.As(err, &setup)
			if tc.wantIssue != "" && (!isSetup || setup.Issue != tc.wantIssue) || tc.wantIssue == "" && isSetup {
				t.Fatalf("startup error = %v, want issue %q", err, tc.wantIssue)
			}
		})
	}
}

func TestAuthenticationDiagnosisDoesNotRequireOpenAIForPrivateProviders(t *testing.T) {
	for _, tc := range []struct {
		body string
		want domain.RuntimeIssue
	}{
		{`{"requiresOpenaiAuth":false,"account":null}`, ""},
		{`{"requiresOpenaiAuth":true,"account":null}`, domain.RuntimeSignInRequired},
		{`{"requiresOpenaiAuth":true,"account":{"type":"apiKey"}}`, ""},
	} {
		var response accountStatus
		json.Unmarshal([]byte(tc.body), &response)
		if got := response.issue(); got != tc.want {
			t.Fatal(tc.body, got, tc.want)
		}
	}
}
func TestOnlyActualAuthenticationRejectionsAreClassified(t *testing.T) {
	for _, tc := range []struct {
		code    int
		message string
		want    domain.RuntimeIssue
	}{
		{401, "Unauthorized", domain.RuntimeAuthenticationExpired},
		{-32000, "refresh_token_reused", domain.RuntimeAuthenticationExpired},
		{-32000, "file not found", ""},
		{403, "permission denied to read file", ""},
	} {
		if got := authenticationIssue(tc.code, tc.message); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestAccountSummaryContainsOnlyPublicIdentityAndActualState(t *testing.T) {
	var a accountStatus
	if err := json.Unmarshal([]byte(`{"requiresOpenaiAuth":true,"account":{"type":"chatgpt","email":"dev@example.com","planType":"pro","accessToken":"must-not-leak"}}`), &a); err != nil {
		t.Fatal(err)
	}
	summary := a.summary()
	if summary == nil || summary.Label != "ChatGPT" || summary.Subscription != "Pro" || summary.State != "authenticated" || summary.Identity != "dev@example.com" {
		t.Fatal("missing actual account", summary)
	}
	data, _ := json.Marshal(summary)
	if strings.Contains(string(data), "must-not-leak") {
		t.Fatal("secret leaked into public account summary")
	}
}

func TestLoginGuidanceUsesEffectiveConfigurationDirectoryAndProfile(t *testing.T) {
	a := Adapter{Executable: "codex", Home: t.TempDir()}
	configured, err := a.WithRuntimeOptions(domain.RuntimeOptions{Directory: t.TempDir(), Arguments: []string{"--profile", "personal"}})
	if err != nil {
		t.Fatal(err)
	}
	b := configured.(Adapter)
	command := b.Installation().SignInCommand
	if !slices.Contains(command, "CODEX_HOME="+b.Home) || !slices.Contains(command, "personal") {
		t.Fatal("login guidance targets a different account", command)
	}
}

func TestAccountSubscriptionLabelStaysSeparateFromAuthenticationMethod(t *testing.T) {
	for _, tc := range []struct{ plan, want string }{{"prolite", "Pro Lite"}, {"plus", "Plus"}, {"new-plan", "new-plan"}} {
		body, _ := json.Marshal(map[string]any{"type": "chatgpt", "planType": tc.plan})
		summary := (accountStatus{Account: body, RequiresOpenAIAuth: true}).summary()
		if summary.Label != "ChatGPT" || summary.Subscription != tc.want {
			t.Fatal("subscription was not translated separately", summary)
		}
	}
}

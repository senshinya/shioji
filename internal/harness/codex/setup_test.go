package codex

import (
	"encoding/json"
	"shioji/internal/domain"
	"slices"
	"strings"
	"testing"
)

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

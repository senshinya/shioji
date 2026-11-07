package codex

import (
	"encoding/json"
	"fmt"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func requestFor(t *testing.T, n *Normalizer, raw string) domain.PendingRequest {
	t.Helper()
	events, reply, err := n.Feed([]byte(raw))
	if err != nil || reply != nil || len(events) != 1 || events[0].Request == nil {
		t.Fatalf("request lost: %+v %s %v", events, reply, err)
	}
	return *events[0].Request
}
func TestMCPFormReturnsTypedContentAndRejectsInvalidValues(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":0,"method":"mcpServer/elicitation/request","params":{"threadId":"t","turnId":"u","serverName":"checks","mode":"form","message":"配置","requestedSchema":{"type":"object","properties":{"name":{"type":"string","minLength":2},"retries":{"type":"integer","minimum":0,"maximum":8},"enabled":{"type":"boolean"},"checks":{"type":"array","items":{"type":"string","enum":["transport","draft"]},"minItems":1,"maxItems":2}},"required":["name","checks"]}}}`)
	if r.Kind != "form" {
		t.Fatal("not a native form", r.Kind)
	}
	answers := map[string][]string{"name": {"air"}, "retries": {"2"}, "enabled": {"false"}, "checks": {"draft", "transport"}}
	bad := domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept", Answers: map[string][]string{"name": {"a"}, "checks": {"outside"}}}
	if _, err := n.Response(bad); err == nil {
		t.Fatal("invalid form response accepted")
	}
	b, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept", Answers: answers})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ID     json.RawMessage
		Result struct {
			Action  string
			Content map[string]any
		}
	}
	json.Unmarshal(b, &out)
	if string(out.ID) != "0" || out.Result.Action != "accept" || out.Result.Content["retries"] != float64(2) || out.Result.Content["enabled"] != false {
		t.Fatal("form lost typed values", string(b))
	}
	if _, err = n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "decline"}); err == nil {
		t.Fatal("duplicate response accepted")
	}
}
func TestPermissionsGrantOnlySelectedRequestedSubset(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"p","method":"item/permissions/requestApproval","params":{"threadId":"t","turnId":"u","itemId":"i","permissions":{"network":{"enabled":true},"fileSystem":{"read":["/remote/read"],"write":["/remote/write"]}},"reason":"检查"}}`)
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "session", Answers: map[string][]string{"permissions": {"outside"}}}); err == nil {
		t.Fatal("unrequested permission accepted")
	}
	b, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "turn", Answers: map[string][]string{"permissions": {"read:0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "network") || strings.Contains(string(b), "/remote/write") || !strings.Contains(string(b), "/remote/read") || !strings.Contains(string(b), `"scope":"turn"`) {
		t.Fatal("permission subset expanded", string(b))
	}
}
func TestApprovalRespectsActualAvailableDecisions(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"approval","method":"item/commandExecution/requestApproval","params":{"threadId":"t","turnId":"u","itemId":"i","command":"echo hello","availableDecisions":["decline","cancel"]}}`)
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept"}); err == nil {
		t.Fatal("unavailable allow choice accepted")
	}
	b, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "cancel"})
	if err != nil || !strings.Contains(string(b), `"decision":"cancel"`) {
		t.Fatal("cancel choice lost", string(b), err)
	}
}
func TestSecretQuestionIsAcceptedWithoutPersistingAnswerInReceipt(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"secret","method":"item/tool/requestUserInput","params":{"threadId":"t","turnId":"u","itemId":"i","questions":[{"id":"q","question":"临时口令","isSecret":true,"isOther":false}]}}`)
	if len(r.Questions) != 1 || !r.Questions[0].Secret {
		t.Fatal("secret flag lost")
	}
	_, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Answers: map[string][]string{"q": {"never-persist-this"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(n.requests[r.ID].request)
	if strings.Contains(string(b), "never-persist-this") {
		t.Fatal("secret copied into receipt")
	}
}

func TestTitledFormEnumsKeepWireValuesAndRequiredUnknownFieldsFailClosed(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"titled","method":"mcpServer/elicitation/request","params":{"threadId":"t","mode":"form","requestedSchema":{"type":"object","properties":{"strategy":{"type":"string","oneOf":[{"const":"auto","title":"自动"},{"const":"manual","title":"手动"}]}},"required":["strategy"]}}}`)
	if len(r.Fields) != 1 || r.Fields[0].Options[0].Label != "自动" || r.Fields[0].Options[0].Value != "auto" {
		t.Fatal("enum label replaced machine value", r.Fields)
	}
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept", Answers: map[string][]string{"strategy": {"自动"}}}); err == nil {
		t.Fatal("human enum label accepted as wire value")
	}
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept", Answers: map[string][]string{"strategy": {"auto"}}}); err != nil {
		t.Fatal(err)
	}
	pending, _ := n.Pending(r.ID)
	if len(pending.Receipt) != 1 || pending.Receipt[0].Text != "自动" {
		t.Fatal("receipt lost readable enum label", pending.Receipt)
	}
	r = requestFor(t, n, `{"id":"missing","method":"mcpServer/elicitation/request","params":{"threadId":"t","mode":"form","requestedSchema":{"type":"object","properties":{},"required":["hidden"]}}}`)
	if r.Kind != "external" {
		t.Fatal("required field absent from form was silently ignored")
	}
}
func TestPermissionEntriesPreserveDenyAndDepthWhenGrantingSubset(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"entries","method":"item/permissions/requestApproval","params":{"threadId":"t","turnId":"u","permissions":{"fileSystem":{"entries":[{"path":{"type":"path","path":"/remote/reports"},"access":"read"},{"path":{"type":"path","path":"/remote/private"},"access":"deny"},{"path":{"type":"glob_pattern","pattern":"/remote/*.log"},"access":"write"}],"globScanMaxDepth":4}}}}`)
	b, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "session", Answers: map[string][]string{"permissions": {"entry:0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/remote/private") || !strings.Contains(string(b), `"globScanMaxDepth":4`) || strings.Contains(string(b), "*.log") {
		t.Fatal("permission restrictions changed", string(b))
	}
}
func TestRepeatedServerRequestDoesNotCreateAnotherForm(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	raw := `{"id":"same","method":"item/tool/requestUserInput","params":{"threadId":"t","turnId":"u","questions":[{"id":"q","question":"Continue?","isOther":true}]}}`
	requestFor(t, n, raw)
	events, _, err := n.Feed([]byte(raw))
	if err != nil || len(events) != 0 {
		t.Fatal("repeated request created duplicate form", events, err)
	}
}

func TestLegacyPermissionGrantRetainsEntryRestrictions(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"mixed","method":"item/permissions/requestApproval","params":{"threadId":"t","permissions":{"fileSystem":{"read":["/remote/reports"],"entries":[{"path":{"type":"path","path":"/remote/reports/private"},"access":"deny"}],"globScanMaxDepth":3}}}}`)
	b, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "turn", Answers: map[string][]string{"permissions": {"read:0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/remote/reports/private") || !strings.Contains(string(b), `"globScanMaxDepth":3`) {
		t.Fatal("mixed permissions lost restrictions", string(b))
	}
}
func TestEmptyAvailableDecisionsDoesNotAddAllow(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":"none","method":"item/commandExecution/requestApproval","params":{"threadId":"t","command":"echo check","availableDecisions":[]}}`)
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept"}); err == nil {
		t.Fatal("empty decisions silently allowed execution")
	}
}

func TestUnknownFormConstraintsCannotBeAccepted(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","properties":{"name":{"type":"string"}},"allOf":[{"properties":{"name":{"const":"required-value"}}}]}`,
		`{"type":"object","properties":{"checks":{"type":"array","items":{"type":"string","enum":["a"],"pattern":"^b$"}}}}`,
	} {
		n := NewNormalizer()
		n.Bind("t", "s", true)
		raw := `{"id":"extended","method":"mcpServer/elicitation/request","params":{"threadId":"t","mode":"openai/form","requestedSchema":` + schema + `}}`
		r := requestFor(t, n, raw)
		if r.Kind != "external" {
			t.Fatal("unsupported constraint was ignored", schema)
		}
		if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Decision: "accept"}); err == nil {
			t.Fatal("unsupported form accepted")
		}
	}
}

func TestMoreThanFourRequestsRemainIndependentlyAnswerable(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	var requests []domain.PendingRequest
	for i := 0; i < 12; i++ {
		raw := fmt.Sprintf(`{"id":"request-%d","method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"turn","itemId":"question-%d","questions":[{"id":"q","question":"Question %d?","isOther":true}]}}`, i, i, i)
		requests = append(requests, requestFor(t, n, raw))
	}
	target := requests[4]
	if _, err := n.Response(domain.Answer{RequestID: target.ID, Revision: target.Revision, Answers: map[string][]string{"q": {"answer five"}}}); err != nil {
		t.Fatal(err)
	}
	for i, r := range requests {
		pending, ok := n.Pending(r.ID)
		if !ok || (i != 4 && pending.State != "pending") {
			t.Fatal("answering the fifth request changed another request", i, pending)
		}
	}
}

func TestOversizedRequestStillReturnsAnExplicitError(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	raw := fmt.Sprintf(`{"id":"large","method":"item/tool/requestUserInput","params":{"threadId":"thread","questions":[{"id":"q","question":%q,"isOther":true}]}}`, strings.Repeat("x", 20<<10))
	events, reply, err := n.Feed([]byte(raw))
	if err != nil || len(events) != 0 || !strings.Contains(string(reply), "resource limit") {
		t.Fatal("oversized request was accepted or silently discarded", events, string(reply), err)
	}
}

func TestApprovalPolicyIsTranslatedToSummaryAndBackToNativeDecision(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	r := requestFor(t, n, `{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"t","turnId":"u","itemId":"i","command":"git status","availableDecisions":[{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}},"decline"]}}`)
	if strings.Contains(r.Text, "execpolicy") || strings.Contains(r.Text, "acceptWith") || !strings.Contains(r.Text, `允许命令前缀："git" "status"`) {
		t.Fatal("native policy object escaped into shared content", r.Text)
	}
	response, err := n.Response(domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: r.Choices[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Result struct{ Decision json.RawMessage }
	}
	if err = json.Unmarshal(response, &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Result.Decision) != `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}}` {
		t.Fatal("translation changed offered approval scope", string(response))
	}
}

func TestApprovalPrefixSummaryPreservesArgumentBoundaries(t *testing.T) {
	summaries := map[string]bool{}
	for _, prefix := range []string{`["tool","a b"]`, `["tool","a","b"]`, `["tool",""]`, `["tool","\"quoted\""]`} {
		n := NewNormalizer()
		n.Bind("t", "s", true)
		raw := `{"id":"p","method":"item/commandExecution/requestApproval","params":{"threadId":"t","command":"tool","availableDecisions":[{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":` + prefix + `}}]}}`
		r := requestFor(t, n, raw)
		if summaries[r.Text] {
			t.Fatal("ambiguous authorization prefix", r.Text)
		}
		summaries[r.Text] = true
	}
}

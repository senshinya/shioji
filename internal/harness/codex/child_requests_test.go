package codex

import (
	"encoding/json"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func childRequestFixture(t *testing.T) *Normalizer {
	t.Helper()
	n := NewNormalizer()
	n.Bind("root", "session", false)
	workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"parent-turn","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"prompt":"检查边界条件","agentsStates":{"child":{"status":"running"}}}}}`)
	workFeed(t, n, `{"method":"turn/started","params":{"threadId":"child","turn":{"id":"child-turn","status":"inProgress"}}}`)
	return n
}
func childQuestion(t *testing.T, n *Normalizer, thread, id string) domain.PendingRequest {
	t.Helper()
	raw := `{"id":"` + id + `","method":"item/tool/requestUserInput","params":{"threadId":"` + thread + `","turnId":"child-turn","itemId":"ask","questions":[{"id":"q","question":"检查根节点？","isOther":true}]}}`
	es, reply, err := n.Feed([]byte(raw))
	if err != nil || reply != nil {
		t.Fatalf("child request rejected: %s %v", reply, err)
	}
	for _, e := range es {
		if e.Request != nil {
			return *e.Request
		}
	}
	t.Fatal("child request was lost")
	return domain.PendingRequest{}
}
func requestWorkID(r domain.PendingRequest) string {
	b, _ := json.Marshal(r)
	var v struct{ WorkID string }
	json.Unmarshal(b, &v)
	return v.WorkID
}
func TestChildRequestUsesVerifiedWorkAndNativeResponseID(t *testing.T) {
	n := childRequestFixture(t)
	r := childQuestion(t, n, "child", "native-child")
	if r.SessionID != "session" || r.TurnID != "parent-turn" || requestWorkID(r) != "agent:parent-turn:child" {
		t.Fatalf("wrong root/work attribution: %+v", r)
	}
	root := requestFor(t, n, `{"id":"native-root","method":"item/tool/requestUserInput","params":{"threadId":"root","turnId":"parent-turn","itemId":"ask","questions":[{"id":"q","question":"主问题","isOther":true}]}}`)
	out, err := n.Response(domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"包含根节点"}}})
	if err != nil || !strings.Contains(string(out), `"id":"native-child"`) {
		t.Fatal(string(out), err)
	}
	if got, _ := n.Pending(root.ID); got.State != "pending" {
		t.Fatal("child answer consumed root request")
	}
	es := workFeed(t, n, `{"method":"serverRequest/resolved","params":{"threadId":"child","requestId":"native-child"}}`)
	var result *domain.Item
	for _, e := range es {
		if e.Request != nil && e.Request.State != "answered" {
			t.Fatal(e)
		}
		if e.Item != nil {
			result = e.Item
		}
	}
	if result == nil || result.TurnID != "parent-turn" || result.Kind != "request_result" || result.Parts[0].Text != "包含根节点" {
		t.Fatal("missing ordered child receipt", es)
	}
	if es := workFeed(t, n, `{"method":"serverRequest/resolved","params":{"threadId":"child","requestId":"native-child"}}`); len(es) != 0 {
		t.Fatal("duplicate receipt", es)
	}
}
func TestChildRequestSurvivesParentCompletionButNotChildStop(t *testing.T) {
	n := childRequestFixture(t)
	r := childQuestion(t, n, "child", "pending")
	workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"parent-turn","status":"completed"}}}`)
	if got, _ := n.Pending(r.ID); got.State != "pending" {
		t.Fatal("parent completion consumed child request")
	}
	es := workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"child-turn","status":"interrupted"}}}`)
	got, _ := n.Pending(r.ID)
	if got.State != "invalidated" {
		t.Fatal("stopped request remained actionable", es, got)
	}
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"yes"}}}); err == nil {
		t.Fatal("stopped child accepted an answer")
	}
}
func TestEarlyChildRequestWaitsForStructuredSpawn(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "session", false)
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"parent-turn","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","status":"inProgress","receiverThreadIds":[]}}}`)
	es, reply, err := n.Feed([]byte(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"child","turnId":"child-turn","itemId":"cmd","command":"go test ./...","availableDecisions":["accept","decline"]}}`))
	if err != nil || reply != nil || len(es) != 0 {
		t.Fatalf("early request rejected or exposed before lineage: %s %+v %v", reply, es, err)
	}
	es = workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"parent-turn","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`)
	workSeen := false
	found := false
	for _, e := range es {
		if e.Work != nil {
			workSeen = true
		}
		if e.Request != nil {
			found = true
			if !workSeen || requestWorkID(*e.Request) != "agent:parent-turn:child" {
				t.Fatal("request precedes verified work", es)
			}
		}
	}
	if !found {
		t.Fatal("early request never released", es)
	}
}
func TestRetiredChildAndUnknownThreadCannotReceiveAnswers(t *testing.T) {
	n := childRequestFixture(t)
	r := childQuestion(t, n, "child", "old")
	delete(n.sessions, "root")
	n.Bind("replacement", "session", false)
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"yes"}}}); err == nil {
		t.Fatal("retired child accepted answer")
	}
	es, reply, err := n.Feed([]byte(`{"id":9,"method":"item/commandExecution/requestApproval","params":{"threadId":"stranger","turnId":"t","itemId":"c","command":"true"}}`))
	if err != nil || len(es) != 0 || reply == nil {
		t.Fatal("unknown request was bound", es, string(reply), err)
	}
}

func TestChildFollowupInvalidatesPriorWorkRequests(t *testing.T) {
	n := childRequestFixture(t)
	r := childQuestion(t, n, "child", "old-question")
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"next-root-turn","item":{"type":"collabAgentToolCall","id":"followup","tool":"sendInput","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`)
	got, _ := n.Pending(r.ID)
	if got.State != "invalidated" {
		t.Fatal("new child execution left prior request actionable", got, es)
	}
}

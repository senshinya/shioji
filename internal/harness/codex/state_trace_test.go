package codex

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"testing"
)

func TestStateTraceHarnessRequest(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "s", true)
	feed := func(step, raw string) []domain.Event {
		events, reply, err := n.Feed([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		testutil.StateTrace(t, step, struct {
			Events []domain.Event
			Reply  json.RawMessage
		}{events, reply})
		return events
	}
	feed("running", `{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"t","status":"inProgress"}}}`)
	feed("usage", `{"method":"thread/tokenUsage/updated","params":{"threadId":"thread","turnId":"t","tokenUsage":{"total":{"inputTokens":120,"outputTokens":30,"cachedInputTokens":20}}}}`)
	events := feed("request", `{"id":7,"method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"t","itemId":"tool","questions":[{"id":"q","header":"Pick","question":"Continue?","isOther":true,"isSecret":false,"options":null}]}}`)
	request := events[0].Request
	reply, err := n.Response(domain.Answer{RequestID: request.ID, Revision: request.Revision, Answers: map[string][]string{"q": {"Yes"}}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.StateTrace(t, "response", json.RawMessage(reply))
	feed("resolved", `{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":7}}`)
	feed("duplicate-resolved", `{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":7}}`)
	feed("completed", `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"t","status":"completed"}}}`)
	testutil.StateTrace(t, "lost", n.Invalidate())
}

func TestStateTraceHarnessRequestOrders(t *testing.T) {
	for _, order := range []string{"answer-resolve", "resolve-answer", "answer-invalidate", "invalidate-answer"} {
		t.Run(order, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("thread", "s", true)
			events, _, err := n.Feed([]byte(`{"id":7,"method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"t","questions":[{"id":"q","header":"Pick","question":"Continue?","isOther":true,"isSecret":false,"options":null}]}}`))
			if err != nil || len(events) != 1 {
				t.Fatalf("open: %v %v", events, err)
			}
			r := events[0].Request
			answer := func(step string) {
				reply, err := n.Response(domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"Yes"}}})
				message := ""
				if err != nil {
					message = err.Error()
				}
				current, _ := n.Pending(r.ID)
				testutil.StateTrace(t, step, struct {
					Reply   json.RawMessage
					Error   string
					Request domain.PendingRequest
				}{reply, message, current})
			}
			finish := func(step string) {
				if order == "answer-invalidate" || order == "invalidate-answer" {
					testutil.StateTrace(t, step, n.Invalidate())
					return
				}
				out, _, err := n.Feed([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":7}}`))
				if err != nil {
					t.Fatal(err)
				}
				testutil.StateTrace(t, step, out)
			}
			if order == "answer-resolve" || order == "answer-invalidate" {
				answer("answer")
				answer("duplicate")
				finish("finish")
			} else {
				finish("finish")
				answer("late-answer")
			}
		})
	}
}

func TestStateTraceUsageOrders(t *testing.T) {
	for _, order := range []string{"missing", "before-start", "after-start", "partial", "regression"} {
		t.Run(order, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("thread", "s", true)
			feed := func(raw string) {
				events, _, err := n.Feed([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				if len(events) > 0 {
					testutil.StateTrace(t, "turn-"+string(events[0].Turn.Status), events)
				}
			}
			usage := `{"method":"thread/tokenUsage/updated","params":{"threadId":"thread","turnId":"t","tokenUsage":{"total":{"inputTokens":120,"outputTokens":30,"cachedInputTokens":20}}}}`
			if order == "before-start" || order == "regression" {
				feed(usage)
			}
			feed(`{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"t","status":"inProgress"}}}`)
			switch order {
			case "after-start":
				feed(usage)
			case "partial":
				feed(`{"method":"thread/tokenUsage/updated","params":{"threadId":"thread","turnId":"t","tokenUsage":{"total":{"outputTokens":30}}}}`)
			case "regression":
				feed(`{"method":"thread/tokenUsage/updated","params":{"threadId":"thread","turnId":"t","tokenUsage":{"total":{"inputTokens":10,"outputTokens":20,"cachedInputTokens":5}}}}`)
			}
			feed(`{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"t","status":"completed"}}}`)
		})
	}
}

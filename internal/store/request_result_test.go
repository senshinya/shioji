package store_test

import (
	"encoding/json"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/store"
	"strings"
	"testing"
)

func TestAnswerResultFollowsLaterMessagesAndSurvivesReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "start", WorkspaceID: w.ID, Text: "开始"})
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := s.Snapshot()
	detail, _ := s.Session(op.SessionID, 0, 100)
	live := app.NewModel()
	live.ApplySnapshot(initial)
	live.Details[op.SessionID] = detail
	n := codex.NewNormalizer()
	n.Bind("thread", op.SessionID, true)
	apply := func(e domain.Event) {
		t.Helper()
		if _, err := s.Apply(op.SessionID, e); err != nil {
			t.Fatal(err)
		}
	}
	feed := func(raw string) []domain.Event {
		t.Helper()
		events, _, err := n.Feed([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			apply(e)
		}
		return events
	}
	events := feed(`{"id":"question","method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"turn","itemId":"question","questions":[{"id":"q","question":"选择哪种方案？","isOther":true},{"id":"secret","question":"测试敏感输入","isSecret":true}]}}`)
	r := *events[0].Request
	for _, id := range []string{"later-1", "later-2", "later-3"} {
		apply(domain.Event{Type: "item.upsert", Item: &domain.Item{ID: id, Kind: "assistant", TurnID: "turn", Text: id, Final: true}})
	}
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"分步验收"}, "secret": {"never-record-this"}}}); err != nil {
		t.Fatal(err)
	}
	r, _ = n.Pending(r.ID)
	apply(domain.Event{Type: "request.open", Request: &r})
	resolved := `{"method":"serverRequest/resolved","params":{"threadId":"thread","requestId":"question"}}`
	feed(resolved)
	feed(resolved) // A repeated confirmation must not add another result.
	apply(domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "after-answer", Kind: "assistant", TurnID: "turn", Text: "收到回答", Final: true}})
	detail, err = s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var result *domain.Item
	for i := range detail.Items {
		item := &detail.Items[i]
		if item.Kind == "request_result" {
			if result != nil {
				t.Fatal("duplicate answer results")
			}
			result = item
		}
	}
	if result == nil {
		t.Fatal("confirmed answer has no separate result in the transcript")
	}
	if result.Order != 6 || detail.Items[1].Kind != "request" || detail.Items[1].Status != "answered" || result.Order >= detail.Items[len(detail.Items)-1].Order {
		t.Fatal("answer result did not follow all three later messages at its confirmation position", detail.Items)
	}
	if len(result.Parts) != 2 || result.Parts[0].Label != "选择哪种方案？" || result.Parts[0].Text != "分步验收" || result.Parts[1].Text != "已提交敏感答案" {
		t.Fatal("result lost readable question/answer or secret redaction", result.Parts)
	}
	journal, err := s.Events(initial.Sequence, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range journal {
		if err := live.ApplyEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	liveItems, _ := json.Marshal(live.Details[op.SessionID].Items)
	want, _ := json.Marshal(detail.Items)
	if string(liveItems) != string(want) || strings.Contains(string(want), "never-record-this") {
		t.Fatal("live event projection differs from persisted redacted transcript")
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(reloaded.Items)
	if string(got) != string(want) {
		t.Fatal("reopening changed result identity or chronology")
	}
}

package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestRecordedConversationsNormalizeWithoutDuplicatingFinalMessages(t *testing.T) {
	for _, file := range []string{"loopback.jsonl", "live.jsonl"} {
		t.Run(file, func(t *testing.T) {
			f, err := os.Open("../../../tests/fixtures/codex/" + file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			n := NewNormalizer()
			items := map[string]domain.Item{}
			var requests, completed int
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			for scanner.Scan() {
				var entry struct {
					Direction string          `json:"direction"`
					Message   json.RawMessage `json:"message"`
				}
				if err = json.Unmarshal(scanner.Bytes(), &entry); err != nil {
					t.Fatal(err)
				}
				if entry.Direction == "out" {
					continue
				}
				var envelope struct {
					Params struct {
						ThreadID string `json:"threadId"`
					} `json:"params"`
				}
				json.Unmarshal(entry.Message, &envelope)
				n.Bind(envelope.Params.ThreadID, "session:"+envelope.Params.ThreadID, true)
				events, reply, err := n.Feed(entry.Message)
				if err != nil || reply != nil {
					t.Fatalf("normalize: %v %s", err, reply)
				}
				for _, e := range events {
					if e.Item != nil {
						items[e.Item.ID] = *e.Item
					}
					if e.Type == "item.delta" {
						i := items[e.ItemID]
						i.Text += e.Text
						items[e.ItemID] = i
					}
					if e.Request != nil && e.Request.State == "pending" {
						requests++
						_, err = n.Response(domain.Answer{RequestID: e.Request.ID, Revision: e.Request.Revision, Answers: map[string][]string{"color": {"Gray"}}})
						if err != nil {
							t.Fatal(err)
						}
					}
					if e.Turn != nil && e.Turn.Status == "completed" {
						completed++
					}
				}
			}
			if scanner.Err() != nil {
				t.Fatal(scanner.Err())
			}
			if file == "loopback.jsonl" {
				if completed != 3 || requests != 1 {
					t.Fatalf("completed=%d requests=%d", completed, requests)
				}
				var tool bool
				for _, i := range items {
					if i.Kind == "tool" && i.Text == "SHIOJI_TOOL_OK" && i.ExitCode != nil && *i.ExitCode == 0 {
						tool = true
					}
				}
				if !tool {
					t.Fatal("missing real tool result")
				}
			} else {
				if completed != 2 {
					t.Fatalf("completed=%d", completed)
				}
				var done bool
				for _, i := range items {
					if strings.Contains(i.Text, "SHIOJI_RECORDING_DONE") && i.Final {
						done = true
					}
				}
				if !done {
					t.Fatal("missing recorded account reply")
				}
			}
		})
	}
}
func TestNumericZeroAndStringZeroRemainIndependentRequests(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	keys := []string{}
	for _, id := range []string{`0`, `"0"`} {
		events, _, err := n.Feed([]byte(`{"id":` + id + `,"method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"turn","itemId":"tool","questions":[{"id":"q","header":"Pick","question":"Continue?","isOther":true,"isSecret":false,"options":null}],"isBlocking":true,"autoResolutionMs":null}}`))
		if err != nil || len(events) != 1 {
			t.Fatalf("request: %v %+v", err, events)
		}
		keys = append(keys, events[0].Request.ID)
	}
	if keys[0] == keys[1] {
		t.Fatal("typed IDs collapsed")
	}
	for i, key := range keys {
		reply, err := n.Response(domain.Answer{RequestID: key, Revision: 1, Answers: map[string][]string{"q": {"yes"}}})
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(reply, &r)
		want := []string{`0`, `"0"`}[i]
		if string(r.ID) != want {
			t.Fatalf("id=%s want=%s", r.ID, want)
		}
	}
}
func TestResolvedBeforeAnswerRejectsDraftAndUnknownRequestsFailClosed(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	events, _, _ := n.Feed([]byte(`{"id":0,"method":"item/commandExecution/requestApproval","params":{"threadId":"t","turnId":"u","itemId":"i","command":"echo test","cwd":"/fixture"}}`))
	key := events[0].Request.ID
	n.Feed([]byte(`{"method":"serverRequest/resolved","params":{"threadId":"t","requestId":0}}`))
	if _, err := n.Response(domain.Answer{RequestID: key, Revision: 1, Decision: "accept"}); err == nil {
		t.Fatal("answered resolved request")
	}
	_, reply, err := n.Feed([]byte(`{"id":"unknown","method":"unknown/execute","params":{}}`))
	if err != nil || !strings.Contains(string(reply), "-32601") {
		t.Fatalf("unknown request: %s %v", reply, err)
	}
}
func TestQuestionRejectsPartialAnswersAndDuplicateResponses(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	events, _, _ := n.Feed([]byte(`{"id":0,"method":"item/tool/requestUserInput","params":{"threadId":"t","turnId":"u","itemId":"i","questions":[{"id":"a","question":"A?","isOther":true},{"id":"b","question":"B?","isOther":true}]}}`))
	r := events[0].Request
	if _, err := n.Response(domain.Answer{RequestID: r.ID, Revision: 1, Answers: map[string][]string{"a": {"yes"}}}); err == nil {
		t.Fatal("partial answer accepted")
	}
	answer := domain.Answer{RequestID: r.ID, Revision: 1, Answers: map[string][]string{"a": {"yes"}, "b": {"no"}}}
	if _, err := n.Response(answer); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Response(answer); err == nil {
		t.Fatal("duplicate wire response")
	}
}

func TestUsageWithoutBaselineRemainsUnknown(t *testing.T){
 n:=NewNormalizer();n.Bind("t","s",false)
 n.Feed([]byte(`{"method":"turn/started","params":{"threadId":"t","turn":{"id":"u","status":"inProgress","items":[]}}}`))
 n.Feed([]byte(`{"method":"thread/tokenUsage/updated","params":{"threadId":"t","turnId":"u","tokenUsage":{"total":{"inputTokens":100,"outputTokens":20,"cachedInputTokens":50}}}}`))
 events,_,err:=n.Feed([]byte(`{"method":"turn/completed","params":{"threadId":"t","turn":{"id":"u","status":"completed","items":[]}}}`));if err!=nil{t.Fatal(err)};if events[0].Turn.InputTokens!=nil||events[0].Turn.OutputTokens!=nil||events[0].Turn.CachedInputTokens!=nil{t.Fatal("session totals became turn usage")}
}

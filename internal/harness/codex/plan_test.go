package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredPlanUpdatesNormalizeWithoutConversationItems(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	for _, source := range []string{
		`{"method":"turn/plan/updated","params":{"threadId":"thread","turnId":"turn","explanation":"先检查，再修改","plan":[{"step":"检查代码","status":"completed"},{"step":"修改实现","status":"inProgress"},{"step":"运行测试","status":"pending"}]}}`,
		`{"method":"turn/plan/updated","params":{"threadId":"thread","turnId":"turn","explanation":null,"plan":[{"step":"运行测试","status":"completed"}]}}`,
		`{"method":"turn/plan/updated","params":{"threadId":"thread","turnId":"next","plan":[]}}`,
	} {
		events, reply, err := n.Feed([]byte(source))
		if err != nil || len(events) != 1 || reply != nil || events[0].Type != "plan" || events[0].SessionID != "session" || events[0].Item != nil {
			t.Fatalf("structured plan was ignored or added to transcript: %+v %s %v", events, reply, err)
		}
		data, _ := json.Marshal(events[0])
		var got struct {
			Plan struct {
				TurnID string                          `json:"turnId"`
				Steps  []struct{ Text, Status string } `json:"steps"`
			} `json:"plan"`
		}
		json.Unmarshal(data, &got)
		if got.Plan.TurnID == "" || len(got.Plan.Steps) == 3 && got.Plan.Steps[1].Status != "running" {
			t.Fatal("plan lost turn identity or status normalization", string(data))
		}
	}
}

func TestPlanAndNameEventsStayWithinJournalDeliveryBudget(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	steps := []map[string]string{}
	for i := 0; i < 10; i++ {
		steps = append(steps, map[string]string{"step": strings.Repeat("\x00", 128<<10), "status": "pending"})
	}
	data, _ := json.Marshal(map[string]any{"method": "turn/plan/updated", "params": map[string]any{"threadId": "thread", "turnId": "t", "explanation": strings.Repeat("\x00", 128<<10), "plan": steps}})
	events, _, err := n.Feed(data)
	if err != nil || len(events) != 1 {
		t.Fatal(err, events)
	}
	encoded, _ := json.Marshal(events[0])
	if len(encoded) > 256<<10 {
		t.Fatal("plan can block the device journal", len(encoded))
	}
	var normalized struct {
		Plan struct {
			Truncated bool `json:"truncated"`
		} `json:"plan"`
	}
	json.Unmarshal(encoded, &normalized)
	if !normalized.Plan.Truncated {
		t.Fatal("plan truncation is not disclosed")
	}
	data, _ = json.Marshal(map[string]any{"method": "thread/name/updated", "params": map[string]any{"threadId": "thread", "threadName": strings.Repeat("\x00", 128<<10)}})
	events, _, err = n.Feed(data)
	if err != nil || len(events) != 1 {
		t.Fatal(err, events)
	}
	encoded, _ = json.Marshal(events[0])
	if len(encoded) > 128<<10 {
		t.Fatal("name can block device journal when attached to session", len(encoded))
	}
}

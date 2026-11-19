package codex

import (
	"encoding/json"
	"testing"
)

func TestGoalNotificationsProjectOnlyTheBoundConversation(t *testing.T) {
	n := NewNormalizer()
	n.Bind("source", "session", false)
	for _, test := range []struct {
		line string
		want string
	}{
		{`{"method":"thread/goal/updated","params":{"threadId":"foreign","goal":{"threadId":"foreign","objective":"Bad","status":"active"}}}`, ""},
		{`{"method":"thread/goal/updated","params":{"threadId":"source","goal":{"threadId":"foreign","objective":"Bad","status":"active"}}}`, ""},
		{`{"method":"thread/goal/updated","params":{"threadId":"source","goal":{"threadId":"source","objective":"Run tests","status":"budgetLimited","timeUsedSeconds":382,"createdAt":100,"updatedAt":120}}}`, `{"sessionId":"session","type":"goal","goalSource":"source","goal":{"objective":"Run tests","state":"budgetLimited","elapsedSeconds":382,"createdAt":100}}`},
		{`{"method":"thread/goal/cleared","params":{"threadId":"source"}}`, `{"sessionId":"session","type":"goal","goalSource":"source"}`},
	} {
		events, _, err := n.Feed([]byte(test.line))
		if err != nil {
			t.Fatal(err)
		}
		if test.want == "" {
			if len(events) != 0 {
				t.Fatal("foreign goal reached the bound session", events)
			}
			continue
		}
		if len(events) != 1 {
			t.Fatalf("expected one goal event: %v", events)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(test.want), &want); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(events[0])
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		delete(got, "sequence")
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("goal event = %s; want %s", gotJSON, wantJSON)
		}
	}
}

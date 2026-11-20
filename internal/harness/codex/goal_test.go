package codex

import (
	"encoding/json"
	"fmt"
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

func TestGoalContinuationRequiresAnObservedGoalBoundary(t *testing.T) {
	const active = `{"method":"thread/goal/updated","params":{"threadId":"root","goal":{"threadId":"root","objective":"Run tests","status":"active","createdAt":100}}}`
	const paused = `{"method":"thread/goal/updated","params":{"threadId":"root","goal":{"threadId":"root","objective":"Run tests","status":"paused","createdAt":100}}}`
	const userInput = `{"method":"item/completed","params":{"threadId":"root","turnId":"next","item":{"type":"userMessage","id":"input","content":[{"type":"text","text":"Check this too"}]}}}`
	for _, test := range []struct {
		name, previousStatus     string
		beforeStart, beforeReply []string
		want                     int
	}{
		{"native continuation", "completed", nil, nil, 1},
		{"initial goal launch", "", nil, nil, 0},
		{"manual or queued input", "completed", nil, []string{userInput}, 0},
		{"interrupted predecessor", "interrupted", nil, nil, 0},
		{"explicit resume", "completed", []string{paused, active}, nil, 0},
		{"pause after continuation starts", "completed", nil, []string{paused}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("root", "session", true)
			markers := 0
			feed := func(frame string) {
				t.Helper()
				events, _, err := n.Feed([]byte(frame))
				if err != nil {
					t.Fatal(err)
				}
				for i, event := range events {
					if event.Item == nil || event.Item.Presentation != "goal-continuation" {
						continue
					}
					markers++
					if event.Item.TurnID != "next" || event.Item.Kind != "activity" || i != 0 || len(events) < 2 || events[1].Item == nil || events[1].Item.Kind != "assistant" {
						t.Fatalf("continuation must precede its reply and use the same turn: %+v", events)
					}
				}
			}
			feed(active)
			if test.previousStatus != "" {
				feed(`{"method":"turn/started","params":{"threadId":"root","turn":{"id":"prior","status":"inProgress"}}}`)
				feed(fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"prior","status":%q}}}`, test.previousStatus))
			}
			for _, frame := range test.beforeStart {
				feed(frame)
			}
			feed(`{"method":"turn/started","params":{"threadId":"root","turn":{"id":"next","status":"inProgress"}}}`)
			for _, frame := range test.beforeReply {
				feed(frame)
			}
			for _, method := range []string{"item/started", "item/completed"} {
				feed(fmt.Sprintf(`{"method":%q,"params":{"threadId":"root","turnId":"next","item":{"type":"agentMessage","id":"reply","text":"Testing."}}}`, method))
			}
			if markers != test.want {
				t.Fatalf("got %d continuation markers, want %d", markers, test.want)
			}
		})
	}
}

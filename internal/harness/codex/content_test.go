package codex

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestConversationItemsHaveNativePresentations(t *testing.T) {
	b, err := os.ReadFile("../../../design/session-components.data.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Entries []struct {
			ID       string
			Source   struct{ Kind string }
			Variants []struct {
				ID     string
				Packet json.RawMessage
			}
		}
	}
	if err = json.Unmarshal(b, &catalog); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{"agentMessage": "assistant", "commandExecution": "tool", "fileChange": "tool", "mcpToolCall": "tool", "dynamicToolCall": "tool", "collabAgentToolCall": "tool", "functionCallOutput": "tool", "webSearch": "tool", "imageView": "tool", "imageGeneration": "tool", "sleep": "tool", "subAgentActivity": "activity", "reasoning": "activity", "hookPrompt": "activity", "enteredReviewMode": "activity", "exitedReviewMode": "activity", "contextCompaction": "activity"}
	for _, entry := range catalog.Entries {
		if entry.Source.Kind != "item" {
			continue
		}
		want, ok := kinds[entry.ID]
		if !ok {
			continue
		}
		for _, v := range entry.Variants {
			t.Run(entry.ID+"/"+v.ID, func(t *testing.T) {
				n := NewNormalizer()
				n.Bind("thread-demo", "session", true)
				packet, _ := json.Marshal(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-demo", "turnId": "turn-demo", "item": v.Packet}})
				events, reply, err := n.Feed(packet)
				if err != nil || reply != nil || len(events) != 1 || events[0].Item == nil {
					t.Fatalf("content lost: %+v %s %v", events, reply, err)
				}
				if events[0].Item.Kind != want {
					t.Fatalf("content rendered as %q, want %q", events[0].Item.Kind, want)
				}
				encoded, _ := json.Marshal(events[0].Item)
				if strings.Contains(string(encoded), "opaque-demo-payload") || strings.Contains(string(encoded), "sample-media") {
					t.Fatal("opaque media/ciphertext stored as visible content")
				}
			})
		}
	}
}

func TestMCPProgressUpdatesExistingCallAndFinalReplacesProgress(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	n.Feed([]byte(`{"method":"item/started","params":{"threadId":"t","turnId":"u","item":{"id":"i","type":"mcpToolCall","server":"files","tool":"read","arguments":{},"status":"inProgress"}}}`))
	events, _, err := n.Feed([]byte(`{"method":"item/mcpToolCall/progress","params":{"threadId":"t","turnId":"u","itemId":"i","message":"读取到第 3 页"}}`))
	if err != nil || len(events) != 1 || events[0].Item == nil || events[0].Item.ID != "u/i" {
		t.Fatalf("progress not attached: %+v %v", events, err)
	}
	encoded, _ := json.Marshal(events[0])
	if !strings.Contains(string(encoded), "读取到第 3 页") {
		t.Fatal("progress missing")
	}
	events, _, err = n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"t","turnId":"u","item":{"id":"i","type":"mcpToolCall","server":"files","tool":"read","arguments":{},"status":"completed","result":{"content":[{"type":"text","text":"全部读取完成"}]}}}}`))
	encoded, _ = json.Marshal(events)
	if err != nil || strings.Contains(string(encoded), "第 3 页") || !strings.Contains(string(encoded), "全部读取完成") {
		t.Fatal("final did not replace progress", string(encoded), err)
	}
	events, _, _ = n.Feed([]byte(`{"method":"item/mcpToolCall/progress","params":{"threadId":"t","turnId":"u","itemId":"i","message":"late"}}`))
	if len(events) != 0 {
		t.Fatal("late progress reopened final call")
	}
}

func TestPlanTextDoesNotBecomeAssistantProseOrStructuredSteps(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	events, _, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"t","turnId":"u","item":{"id":"p","type":"plan","text":"先测试，再修复。"}}}`))
	if err != nil || len(events) != 1 || events[0].Type != "plan.text" || events[0].Text != "先测试，再修复。" || events[0].Item != nil {
		t.Fatalf("plan is not a separate inspector event: %+v %v", events, err)
	}
}

func TestResourceLabelsAndToolNamesStayWithinEventBudget(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	item := map[string]any{"id": "large", "type": "mcpToolCall", "server": strings.Repeat("s", 1<<20), "tool": strings.Repeat("x", 1<<20), "arguments": map[string]any{}, "status": "completed", "result": map[string]any{"content": []any{map[string]any{"type": "resource_link", "name": strings.Repeat("n", 1<<20), "uri": "project://report"}}}}
	b, _ := json.Marshal(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "t", "turnId": "u", "item": item}})
	events, _, err := n.Feed(b)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(events)
	if len(out) > 192<<10 {
		t.Fatal("activity exceeds recoverable event budget", len(out))
	}
}

func TestEscapedContentCannotExceedSerializedItemBudget(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	packet, _ := json.Marshal(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "t", "turnId": "u", "item": map[string]string{"id": "control", "type": "agentMessage", "text": strings.Repeat("\x01", 128<<10)}}})
	events, _, err := n.Feed(packet)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(events)
	if len(b) > 192<<10 {
		t.Fatal("JSON escaping bypassed activity budget", len(b))
	}
}

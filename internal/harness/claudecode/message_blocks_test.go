package claudecode

import (
	"encoding/json"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestClaudeSplitAssistantFramesPreserveStreamedBlockIdentity(t *testing.T) {
	now := time.Now()
	items := map[string]domain.Item{}
	r := runtime{
		turn: &activeTurn{turn: domain.Turn{ID: "turn", StartedAt: &now}, items: map[string]domain.Item{}},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			if e.Item != nil {
				if previous, ok := items[e.Item.ID]; ok && previous.Kind != e.Item.Kind {
					t.Errorf("item %s changed kind from %s to %s", e.Item.ID, previous.Kind, e.Item.Kind)
				}
				items[e.Item.ID] = *e.Item
			}
			return nil
		}},
	}
	for _, raw := range []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"answer"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"核对事件顺序。"}}}`,
		`{"type":"assistant","uuid":"reason","message":{"id":"answer","content":[{"type":"thinking","thinking":"核对事件顺序。"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"已核对。"}}}`,
		`{"type":"assistant","uuid":"reply","message":{"id":"answer","content":[{"type":"text","text":"已核对。"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		`{"type":"result","subtype":"success"}`,
	} {
		var in frame
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	reason, reply := items["turn:answer:0"], items["turn:answer:1"]
	if len(items) != 2 || !domain.ReasoningSummary(reason) || !reason.Final || reason.Text != "核对事件顺序。" ||
		reply.Kind != "assistant" || !reply.Final || reply.Text != "已核对。" {
		t.Fatalf("expected one thinking summary and one final reply, got %+v", items)
	}
}

func TestClaudeSplitChildFramesKeepDistinctItemsAndToolResult(t *testing.T) {
	items := map[string]domain.Item{}
	r := runtime{
		work: map[string]domain.WorkActivity{"child": {ID: "work", TurnID: "turn"}},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			if e.WorkItem != nil {
				items[e.WorkItem.ID] = *e.WorkItem
			}
			return nil
		}},
	}
	for _, raw := range []string{
		`{"type":"assistant","agent_id":"child","uuid":"reason","message":{"id":"shared","content":[{"type":"thinking","thinking":"检查文件"}]}}`,
		`{"type":"assistant","agent_id":"child","uuid":"call","message":{"id":"shared","content":[{"type":"tool_use","id":"tool","name":"Read","input":{"file_path":"fixture.txt"}}]}}`,
		`{"type":"assistant","agent_id":"child","uuid":"reply","message":{"id":"shared","content":[{"type":"text","text":"已读取。"}]}}`,
		`{"type":"user","agent_id":"child","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"文件内容"}]}}`,
	} {
		var in frame
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	if len(items) != 3 {
		t.Fatalf("child reasoning, tool and reply must stay distinct: %+v", items)
	}
	for _, item := range items {
		switch item.Kind {
		case "reasoning":
			if !domain.ReasoningSummary(item) || item.Text != "检查文件" {
				t.Fatalf("invalid child thinking summary: %+v", item)
			}
		case "tool":
			if item.Name != "Read" || !item.Final || len(item.Parts) != 1 || item.Parts[0].Text != "文件内容" {
				t.Fatalf("child result did not update its tool: %+v", item)
			}
		case "assistant":
			if item.Text != "已读取。" {
				t.Fatalf("invalid child reply: %+v", item)
			}
		default:
			t.Fatalf("unexpected child item: %+v", item)
		}
	}
}

func TestClaudeTurnResultDoesNotConfirmUnobservedToolResult(t *testing.T) {
	now := time.Now()
	active := &activeTurn{
		turn: domain.Turn{ID: "turn", StartedAt: &now},
		items: map[string]domain.Item{
			"tool": {ID: "tool", TurnID: "turn", Kind: "tool", Name: "Read", Status: "running"},
		},
	}
	var item *domain.Item
	r := runtime{turn: active, opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
		if e.Item != nil {
			item = e.Item
		}
		return nil
	}}}
	if err := r.processFrame(frame{Type: "result", Subtype: "success"}); err != nil {
		t.Fatal(err)
	}
	if item == nil || !item.Final || item.Status != "unknown" {
		t.Fatalf("turn success without a tool result must leave the tool outcome unknown: %+v", item)
	}
}

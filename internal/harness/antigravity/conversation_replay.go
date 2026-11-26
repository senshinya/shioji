package antigravity

import (
	"encoding/json"
	"fmt"

	"shioji/internal/domain"
)

const historyReplayLimit = 48 << 20

type historyReplay struct {
	source       string
	items        []domain.Item
	turns        []domain.Turn
	tools        map[string]int
	previousKind string
	previousID   string
	bytes        int
	closed       bool
	tooLarge     bool
}

func (h *historyReplay) update(raw json.RawMessage) {
	if h.closed || h.tooLarge {
		return
	}
	h.bytes += len(raw)
	if h.bytes > historyReplayLimit || len(h.items) >= 100000 || len(h.turns) >= 10000 {
		h.tooLarge = true
		return
	}
	var event struct {
		SessionID string
		Update    struct {
			Kind      string `json:"sessionUpdate"`
			MessageID string
			Content   json.RawMessage
			Entries   []struct{ Content, Status string }
			toolUpdate
		}
	}
	if json.Unmarshal(raw, &event) != nil || event.SessionID != h.source {
		return
	}
	u := event.Update
	switch u.Kind {
	case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk":
		var value contentBlock
		if json.Unmarshal(u.Content, &value) != nil {
			return
		}
		text := value.Text
		switch value.Type {
		case "text":
		case "image":
			text = "\n[历史图片无法预览]\n"
		case "audio":
			text = "\n[历史音频无法预览]\n"
		case "resource", "resource_link":
			if value.Resource != nil && value.Resource.Text != "" {
				text = value.Resource.Text
			} else {
				text = value.Name
				if text == "" {
					text = value.URI
				}
			}
		default:
			return
		}
		if text != "" {
			h.message(u.Kind, u.MessageID, text)
		}
	case "tool_call", "tool_call_update":
		if len(h.turns) > 0 && u.ToolCallID != "" {
			h.tool(u.toolUpdate)
		}
	case "plan":
		if len(h.turns) == 0 || len(u.Entries) == 0 {
			return
		}
		item := h.nextItem("activity")
		item.Presentation, item.Name = "context", "任务清单"
		for _, entry := range u.Entries[:min(64, len(u.Entries))] {
			label := map[string]string{"pending": "待处理", "running": "进行中", "completed": "已完成"}[planStatus(entry.Status)]
			item.Parts = append(item.Parts, domain.ContentPart{Kind: "text", Label: label, Text: clip(entry.Content, 1024)})
		}
		item.Truncated = len(u.Entries) > 64
		h.items = append(h.items, item)
		h.previousKind = ""
	}
}

func (h *historyReplay) nextItem(kind string) domain.Item {
	return domain.Item{ID: fmt.Sprintf("agy-history-item:%d", len(h.items)+1), TurnID: h.turns[len(h.turns)-1].ID,
		Kind: kind, Status: "completed", Final: true}
}

func (h *historyReplay) message(frameKind, id, text string) {
	if len(h.items) > 0 && h.previousKind == frameKind && h.previousID == id {
		item := &h.items[len(h.items)-1]
		full := item.Text + text
		item.Text = clip(full, 128<<10)
		item.Truncated = item.Truncated || len(full) != len(item.Text)
		return
	}
	kind := "assistant"
	if frameKind == "user_message_chunk" {
		kind = "user"
		h.turns = append(h.turns, domain.Turn{ID: fmt.Sprintf("agy-history-turn:%d", len(h.turns)+1), Status: domain.ExecutionUnknown})
	} else if len(h.turns) == 0 {
		return
	} else if frameKind == "agent_thought_chunk" {
		kind = "reasoning"
	}
	item := h.nextItem(kind)
	item.Text = clip(text, 128<<10)
	item.Truncated = len(text) != len(item.Text)
	h.items = append(h.items, item)
	h.previousKind, h.previousID = frameKind, id
}

func (h *historyReplay) tool(update toolUpdate) {
	index, exists := h.tools[update.ToolCallID]
	if !exists {
		index = len(h.items)
		item := h.nextItem("tool")
		item.Status, item.Final = "unknown", false
		h.items = append(h.items, item)
		h.tools[update.ToolCallID] = index
	}
	item := &h.items[index]
	if applyToolUpdate(item, update) {
		// Replay only normalizes the supplied data. No live resource importer
		// is available to a preview, and source paths are never opened here.
		(&runtime{}).updateToolContent(item, update)
	}
	h.previousKind = ""
}

func (h *historyReplay) finish() {
	h.closed = true
	for i := range h.items {
		if !h.items[i].Final {
			h.items[i].Status, h.items[i].Final = "unknown", true
		}
	}
}

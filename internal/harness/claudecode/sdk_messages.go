package claudecode

import (
	"encoding/json"
	"slices"
	"strings"

	"shioji/internal/domain"
)

// SDKMessage is a tagged union. In particular, a user/assistant message is
// an object and a system permission notice's message is a string.
func (f *frame) UnmarshalJSON(data []byte) error {
	var kind struct{ Type, Subtype string }
	if err := json.Unmarshal(data, &kind); err != nil {
		return err
	}
	*f = frame{Type: kind.Type, Subtype: kind.Subtype}
	switch kind.Type {
	case "system":
		switch kind.Subtype {
		case "compact_boundary", "local_command_output", "permission_denied",
			"task_started", "task_progress", "task_notification":
		default:
			return nil
		}
	case "assistant", "user", "stream_event", "result", "rate_limit_event",
		"ready", "context_usage", "approval", "approval_applied", "approval_cancelled",
		"command_lifecycle", "input_refused", "interrupt_applied", "mcp_status", "work_stopped":
	default:
		return nil
	}
	type fields frame
	wire := struct {
		*fields
		Message json.RawMessage `json:"message"`
		Content json.RawMessage `json:"content"`
	}{fields: (*fields)(f)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Message) != 0 {
		switch {
		case f.Type == "assistant" || f.Type == "user":
			return json.Unmarshal(wire.Message, &f.Message)
		case f.Type == "system" && f.Subtype == "permission_denied":
			return json.Unmarshal(wire.Message, &f.DenialMessage)
		}
	}
	if f.Type == "system" && f.Subtype == "local_command_output" && len(wire.Content) != 0 {
		return json.Unmarshal(wire.Content, &f.CommandOutput)
	}
	return nil
}

func (r *runtime) settlePermissionDenials(t *activeTurn, in frame) error {
	for _, denial := range in.PermissionDenials {
		if t.denials == nil {
			t.denials = make(map[string]string)
		}
		if _, observed := t.denials[denial.ToolUseID]; !observed {
			t.denials[denial.ToolUseID] = ""
		}
	}
	for nativeID, message := range t.denials {
		item := t.items[t.tools[nativeID]]
		if item.ID == "" || item.Final {
			continue
		}
		if message == "" {
			message = "Claude Code 拒绝了此工具调用"
		}
		item.Status, item.Final = "failed", true
		item.Parts = []domain.ContentPart{{Kind: "text", Text: boundedText(r.redact(message), 12<<10)}}
		if err := r.item(t, item, false); err != nil {
			return err
		}
	}
	return nil
}

type lateToolTurn struct {
	thread   string
	inputs   []string
	tools    map[string]domain.Item
	detached map[string]bool
}

func (r *runtime) rememberUnsettledTools(t *activeTurn) {
	var tools map[string]domain.Item
	for nativeID, id := range t.tools {
		item := t.items[id]
		if item.Status == "unknown" || item.Status == "interrupted" {
			if tools == nil {
				tools = make(map[string]domain.Item)
			}
			tools[nativeID] = item
		}
	}
	if len(tools) == 0 {
		return
	}
	inputs := []string{t.uuid}
	for uuid := range t.acceptedSteer {
		inputs = append(inputs, uuid)
	}
	r.lateToolTurns = append(r.lateToolTurns, lateToolTurn{
		thread: r.session.ThreadID, inputs: inputs, tools: tools, detached: t.detachedTools,
	})
}

func (r *runtime) observeToolResults(t *activeTurn, in frame) error {
	sidecar := singleToolResultSidecar(in.Message.Content, in.ToolUseResult)
	detaches := sidecarDetachesTool(sidecar)
	for _, block := range in.Message.Content {
		if block.Type != "tool_result" || block.ToolUseID == "" {
			continue
		}
		if t != nil && t.ownsFrame(in) {
			if item, ok := t.items[t.tools[block.ToolUseID]]; ok {
				if !item.Final {
					if detaches {
						if t.detachedTools == nil {
							t.detachedTools = make(map[string]bool)
						}
						t.detachedTools[block.ToolUseID] = true
						continue
					}
					if err := r.item(t, r.completeTool(item, block, sidecar), false); err != nil {
						return err
					}
					delete(t.detachedTools, block.ToolUseID)
				}
				continue
			}
		}
		if detaches {
			continue
		}
		for i := len(r.lateToolTurns) - 1; i >= 0; i-- {
			owner := &r.lateToolTurns[i]
			item, ok := owner.tools[block.ToolUseID]
			if !ok || owner.thread != r.session.ThreadID || !owner.detached[block.ToolUseID] &&
				!in.matchesInput(func(id string) bool { return id != "" && slices.Contains(owner.inputs, id) }) {
				continue
			}
			item = r.redactItem(r.completeTool(item, block, sidecar))
			if err := r.emit(domain.Event{Type: "item.upsert", Item: &item}); err != nil {
				return err
			}
			delete(owner.tools, block.ToolUseID)
			if len(owner.tools) == 0 {
				r.lateToolTurns = slices.Delete(r.lateToolTurns, i, i+1)
			}
			break
		}
	}
	return nil
}

func (r *runtime) replyText(t *activeTurn, source, text string) error {
	if strings.TrimSpace(text) == "" || t.turn.Presentation == "compaction" {
		return nil
	}
	return r.item(t, domain.Item{ID: t.turn.ID + ":" + source, TurnID: t.turn.ID, Kind: "assistant",
		Text: text, Status: "completed", Final: true}, false)
}

func (t *activeTurn) hasReply() bool {
	for _, item := range t.items {
		if item.Kind == "assistant" && strings.TrimSpace(item.Text) != "" {
			return true
		}
	}
	return false
}

func terminalFailure(in frame, assistantCode string) string {
	switch {
	case in.APIErrorStatus == 429:
		return "rate_limit"
	case in.APIErrorStatus == 529:
		return "overloaded"
	case assistantCode != "":
		return assistantCode
	}
	switch in.TerminalReason {
	case "blocking_limit", "rapid_refill_breaker", "prompt_too_long", "image_error", "model_error", "api_error",
		"malformed_tool_use_exhausted", "budget_exhausted", "structured_output_retry_exhausted",
		"tool_deferred_unavailable", "turn_setup_failed", "max_turns":
		return in.TerminalReason
	default:
		return ""
	}
}

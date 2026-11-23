package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type sessionUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		Kind       string `json:"sessionUpdate"`
		Content    json.RawMessage
		Used, Size *int64
		Title      *string
		Entries    []struct{ Content, Status string }
	} `json:"update"`
}

func (r *runtime) handle(msg packet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch msg.Method {
	case "session/update":
		var update sessionUpdate
		if json.Unmarshal(msg.Params, &update) != nil || r.session.ThreadID == "" || update.SessionID != r.session.ThreadID {
			return nil
		}
		return r.update(update, msg.Params)
	case "session/request_permission":
		if len(msg.ID) != 0 {
			return r.receivePermission(msg)
		}
	default:
		if len(msg.ID) != 0 {
			return r.reply(context.Background(), msg.ID, nil, &rpcError{Code: -32601, Message: "Method not supported"})
		}
	}
	return nil
}

func (r *runtime) update(event sessionUpdate, raw json.RawMessage) error {
	t, u := r.active, event.Update
	if t == nil {
		return nil
	}
	switch u.Kind {
	case "agent_message_chunk", "agent_thought_chunk":
		r.confirm(t, nil)
		var content contentBlock
		if json.Unmarshal(u.Content, &content) != nil {
			return nil
		}
		return r.stream(t, u.Kind, content)
	case "tool_call", "tool_call_update":
		r.confirm(t, nil)
		var tool struct{ Update toolUpdate }
		if json.Unmarshal(raw, &tool) != nil {
			return nil
		}
		if err := r.finishStream(t); err != nil {
			return err
		}
		return r.updateTool(t, tool.Update)
	case "plan":
		r.confirm(t, nil)
		plan := domain.Plan{TurnID: t.turn.ID}
		for _, entry := range u.Entries {
			plan.Steps = append(plan.Steps, domain.PlanStep{Text: clip(entry.Content, 4096), Status: entry.Status})
		}
		return r.emit(domain.Event{Type: "plan", Plan: &plan})
	case "usage_update":
		if u.Used == nil && u.Size == nil {
			return nil
		}
		return r.emit(domain.Event{Type: "context.usage", ContextUsage: &domain.ContextUsage{
			TurnID: t.turn.ID, UsedTokens: u.Used, LimitTokens: u.Size, UpdatedAt: time.Now().UTC(),
		}})
	}
	return nil
}

func (r *runtime) stream(t *logicalTurn, source string, content contentBlock) error {
	if content.Type != "text" && (content.Type != "image" || r.options.ImportResource == nil || len(content.Data) == 0) {
		return nil
	}
	kind, presentation := "assistant", ""
	if source == "agent_thought_chunk" {
		kind, presentation = "activity", "reasoning"
	}
	start := t.stream == nil || t.stream.Kind != kind
	if start {
		if err := r.finishStream(t); err != nil {
			return err
		}
		t.stream = &domain.Item{ID: "agy-item:" + domain.NewID(), TurnID: t.turn.ID, Kind: kind, Presentation: presentation, Status: "running"}
		if presentation == "reasoning" {
			t.stream.Name = "思考摘要"
		}
	}
	item := t.stream
	switch content.Type {
	case "text":
		text := item.Text + content.Text
		item.Text = clip(text, 128<<10)
		item.Truncated = item.Truncated || len(item.Text) != len(text)
	case "image":
		if len(item.Attachments) >= domain.MaxMessageAttachments {
			return nil
		}
		attachment, err := r.importImage(item.ID, len(item.Attachments), content)
		if err != nil {
			return nil
		}
		item.Attachments = append(item.Attachments, attachment)
	}
	snapshot := *item
	snapshot.Attachments = slices.Clone(item.Attachments)
	return r.emit(domain.Event{Type: "item.upsert", Item: &snapshot, ActivityStart: start})
}

func (r *runtime) importImage(id string, index int, content contentBlock) (domain.Attachment, error) {
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[content.MIMEType]
	if ext == "" || len(content.Data) > 8<<20 {
		return domain.Attachment{}, errors.New("无法导入 Antigravity 返回的图片")
	}
	return r.options.ImportResource(harness.OutputResource{Kind: "image", SessionID: r.session.ID, ItemID: id, Index: index, Name: fmt.Sprintf("image-%d%s", index+1, ext), Data: content.Data})
}

type toolUpdate struct {
	ToolCallID, Title, Kind, Status string
	Content                         []struct {
		Type, Path string
		OldText    *string
		NewText    string
		Content    contentBlock
	}
	Locations []struct{ Path string }
}

func (r *runtime) updateTool(t *logicalTurn, update toolUpdate) error {
	if update.ToolCallID == "" {
		return nil
	}
	item, known := t.tools[update.ToolCallID]
	if !known {
		item = domain.Item{ID: "agy-tool:" + domain.NewID(), TurnID: t.turn.ID, Kind: "tool", Status: "starting"}
	}
	if item.Final {
		return nil
	}
	if update.Title != "" {
		item.Name = clip(update.Title, 4096)
	}
	switch update.Kind {
	case "execute":
		item.Presentation = "command"
	case "edit", "delete", "move", "read":
		item.Presentation = "files"
	case "search", "fetch":
		item.Presentation = "search"
	}
	switch update.Status {
	case "pending":
		item.Status = "starting"
	case "in_progress":
		item.Status = "running"
	case "completed":
		item.Status, item.Final = "completed", true
	case "failed":
		item.Status, item.Final = "failed", true
	}
	if update.Content != nil {
		item.Parts, item.Files = nil, nil
		budget := 64 << 10
		for _, part := range update.Content {
			switch {
			case part.Type == "diff" && part.Path != "":
				kind, old := "modified", ""
				if part.OldText == nil {
					kind = "added"
				} else {
					old = *part.OldText
				}
				item.Presentation = "files"
				path := clip(part.Path, 4096)
				diff := patch(old, part.NewText)
				if len(diff)+len(path) > budget {
					item.Truncated, diff = true, ""
				}
				budget -= len(diff) + len(path)
				item.Files = append(item.Files, domain.FileChange{Path: path, Kind: kind, Patch: diff})
			case part.Type == "content" && part.Content.Type == "text":
				text := clip(part.Content.Text, min(32<<10, budget))
				item.Truncated = item.Truncated || len(text) != len(part.Content.Text)
				budget -= len(text)
				item.Parts = append(item.Parts, domain.ContentPart{Kind: "text", Text: text})
			}
			if budget <= 0 || len(item.Parts)+len(item.Files) >= 24 {
				item.Truncated = true
				break
			}
		}
	}
	t.tools[update.ToolCallID] = item
	return r.emit(domain.Event{Type: "item.upsert", Item: &item, ActivityStart: !known})
}

// ACP provides literal before and after text. Preserve both in a unified hunk
// when they fit the transcript budget.
func patch(before, after string) string {
	if before == after || len(before)+len(after) > 24<<10 {
		return ""
	}
	lines := func(text string) []string {
		if text == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	old, new := lines(before), lines(after)
	var out strings.Builder
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", min(1, len(old)), len(old), min(1, len(new)), len(new))
	write := func(prefix byte, parts []string, text string) {
		for _, line := range parts {
			out.WriteByte(prefix)
			out.WriteString(line)
			out.WriteByte('\n')
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
	write('-', old, before)
	write('+', new, after)
	return out.String()
}

type permissionRequest struct {
	WireID  json.RawMessage
	Request domain.PendingRequest
}

func (r *runtime) receivePermission(msg packet) error {
	var payload struct {
		SessionID string `json:"sessionId"`
		ToolCall  toolUpdate
		Options   []struct{ OptionID, Name, Kind string }
	}
	if json.Unmarshal(msg.Params, &payload) != nil || r.active == nil || r.active.interrupted || payload.SessionID != r.session.ThreadID {
		return r.reply(context.Background(), msg.ID, permissionOutcome(""), nil)
	}
	t := r.active
	r.confirm(t, nil)
	request := domain.PendingRequest{
		ID: "agy-request:" + domain.NewID(), SessionID: r.session.ID,
		TurnID: t.turn.ID, Title: clip(payload.ToolCall.Title, 4096), Kind: "approval",
		Revision: 1, State: domain.RequestPending,
	}
	if request.Title == "" {
		request.Title = "Antigravity 请求执行工具"
	}
	if item, ok := t.tools[payload.ToolCall.ToolCallID]; ok {
		request.ItemID = item.ID
	}
	for _, location := range payload.ToolCall.Locations {
		if filepath.IsAbs(location.Path) {
			request.Text = clip(request.Text+location.Path+"\n", 4096)
		}
	}
	for _, option := range payload.Options {
		if option.OptionID == "" || option.Name == "" {
			continue
		}
		request.Choices = append(request.Choices, domain.RequestChoice{ID: option.OptionID, Label: clip(option.Name, 256), Restriction: strings.HasPrefix(option.Kind, "reject")})
	}
	if len(request.Choices) == 0 {
		return r.reply(context.Background(), msg.ID, permissionOutcome(""), nil)
	}
	r.requests[request.ID] = permissionRequest{WireID: msg.ID, Request: request}
	return r.emit(domain.Event{Type: "request.open", Request: &request})
}

type permissionResponse struct {
	Outcome struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId,omitempty"`
	} `json:"outcome"`
}

func permissionOutcome(option string) permissionResponse {
	var response permissionResponse
	response.Outcome.Outcome = "cancelled"
	if option != "" {
		response.Outcome.Outcome, response.Outcome.OptionID = "selected", option
	}
	return response
}

func (r *runtime) reply(ctx context.Context, id json.RawMessage, outcome any, rpcErr *rpcError) error {
	data, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		data = nil
	}
	return r.process.write(ctx, packet{Version: "2.0", ID: id, Result: data, Error: rpcErr})
}

func (r *runtime) Respond(answer domain.Answer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.requests[answer.RequestID]
	if !ok || p.Request.Revision != answer.Revision {
		return errors.New("请求已提交或失效")
	}
	if !slices.ContainsFunc(p.Request.Choices, func(c domain.RequestChoice) bool { return c.ID == answer.Decision }) {
		return errors.New("该审批选项不可用")
	}
	return r.resolvePermission(context.Background(), answer.RequestID, p, answer.Decision)
}

func (r *runtime) resolvePermission(ctx context.Context, key string, p permissionRequest, option string) error {
	if err := r.reply(ctx, p.WireID, permissionOutcome(option), nil); err != nil {
		return err
	}
	delete(r.requests, key)
	p.Request.Revision++
	p.Request.State = domain.RequestResolved
	if option == "" {
		p.Request.State = domain.RequestInvalidated
	}
	if err := r.emit(domain.Event{Type: "request.resolve", Request: &p.Request}); err != nil {
		return err
	}
	return r.finishIfSettled(r.active)
}

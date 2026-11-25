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
		Kind              string `json:"sessionUpdate"`
		Content           json.RawMessage
		Used, Size        *int64
		Title             *string
		AvailableCommands []struct{ Name string }
		Entries           []struct{ Content, Status string }
	} `json:"update"`
}

func (r *runtime) handle(msg packet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch msg.Method {
	case "session/update":
		var update sessionUpdate
		if json.Unmarshal(msg.Params, &update) != nil {
			return nil
		}
		if update.Update.Kind == "available_commands_update" {
			r.observeCommands(update.SessionID, update.Update.AvailableCommands)
		} else if r.session.ThreadID != "" && update.SessionID == r.session.ThreadID {
			return r.update(update, msg.Params)
		}
	case "session/request_permission":
		if len(msg.ID) != 0 {
			return r.receivePermission(msg)
		}
	case "elicitation/create":
		if len(msg.ID) != 0 {
			return r.receiveQuestion(msg)
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
	if u.Kind == "session_info_update" && u.Title != nil {
		title := clip(strings.TrimSpace(*u.Title), 4096)
		if title != "" && title != r.nativeTitle {
			r.nativeTitle = title
			return r.emit(domain.Event{Type: "session.title", Text: title})
		}
	}
	if u.Kind == "usage_update" {
		turn := r.settledTurn
		if t != nil {
			turn = t.turn.ID
		}
		if turn == "" || u.Used == nil && u.Size == nil {
			return nil
		}
		if t != nil {
			t.sawUsage = true
		}
		return r.emit(domain.Event{Type: "context.usage", ContextUsage: &domain.ContextUsage{
			TurnID: turn, UsedTokens: u.Used, LimitTokens: u.Size, UpdatedAt: time.Now().UTC(),
		}})
	}
	if u.Kind == "tool_call" || u.Kind == "tool_call_update" {
		var tool struct{ Update toolUpdate }
		if json.Unmarshal(raw, &tool) != nil || tool.Update.ToolCallID == "" {
			return nil
		}
		if late, ok := r.lateTools[tool.Update.ToolCallID]; ok {
			return r.updateLateTool(late, tool.Update)
		}
		if t == nil || t.interrupted {
			return nil
		}
		r.confirm(t, nil)
		if steps, handled := t.checklist(tool.Update); handled {
			if steps == nil {
				return nil
			}
			return r.emit(domain.Event{Type: "plan", Plan: &domain.Plan{TurnID: t.turn.ID, Steps: steps}})
		}
		if err := r.finishStream(t); err != nil {
			return err
		}
		return r.updateTool(t.tools, t.turn.ID, tool.Update)
	}
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
	case "plan":
		r.confirm(t, nil)
		plan := domain.Plan{TurnID: t.turn.ID}
		for _, entry := range u.Entries {
			plan.Steps = append(plan.Steps, domain.PlanStep{Text: clip(entry.Content, 4096), Status: planStatus(entry.Status)})
		}
		return r.emit(domain.Event{Type: "plan", Plan: &plan})
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
	ToolCallID, Name, Title, Kind, Status string
	RawInput                              json.RawMessage
	RawOutput                             json.RawMessage
	Content                               []struct {
		Type, Path string
		OldText    *string
		NewText    string
		Content    contentBlock
	}
	Locations []struct{ Path string }
}

func planStatus(status string) string {
	switch status {
	case "running", "in_progress":
		return "running"
	case "done", "completed":
		return "completed"
	default:
		return "pending"
	}
}

func (t *logicalTurn) checklist(update toolUpdate) ([]domain.PlanStep, bool) {
	name := update.Name
	// Older AGY versions supplied the native identity only in this title.
	if name == "" && (update.Title == "Run update_steps" || update.Title == "Running update_steps") {
		name = "update_steps"
	}
	if name != "update_steps" && !t.planCalls[update.ToolCallID] {
		return nil, false
	}
	if t.planCalls == nil {
		t.planCalls = make(map[string]bool)
	}
	t.planCalls[update.ToolCallID] = true
	var input struct {
		Steps []struct{ Title, Status string }
	}
	if update.Status == "failed" || json.Unmarshal(update.RawInput, &input) != nil || input.Steps == nil {
		return nil, true
	}
	steps := make([]domain.PlanStep, 0, min(len(input.Steps), 64))
	for _, step := range input.Steps[:min(len(input.Steps), 64)] {
		if text := strings.TrimSpace(step.Title); text != "" {
			steps = append(steps, domain.PlanStep{Text: clip(text, 1024), Status: planStatus(step.Status)})
		}
	}
	return steps, true
}

func (r *runtime) updateTool(tools map[string]domain.Item, turn string, update toolUpdate) error {
	item, known := tools[update.ToolCallID]
	if !known {
		item = domain.Item{ID: "agy-tool:" + domain.NewID(), TurnID: turn, Kind: "tool", Status: "starting"}
	}
	if !applyToolUpdate(&item, update) {
		return nil
	}
	r.updateToolContent(&item, update)
	tools[update.ToolCallID] = item
	return r.emit(domain.Event{Type: "item.upsert", Item: &item, ActivityStart: !known})
}

func applyToolUpdate(item *domain.Item, update toolUpdate) bool {
	if item.Final && (item.Status != "unknown" || update.Status != "completed" && update.Status != "failed") {
		return false
	}
	if update.Title != "" && (item.Presentation != "command" || item.Name == "") {
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
	return true
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r *runtime) updateToolContent(item *domain.Item, update toolUpdate) {
	var directory []domain.ContentPart
	if item.Presentation == "command" {
		if len(item.Parts) > 0 && item.Parts[0].Label == "工作目录" {
			directory = item.Parts[:1:1]
		}
		var input struct {
			CommandLine, Command, WorkingDirectory, WorkingDir, CWD string
			NativeCommand                                           string `json:"command_line"`
			NativeDir                                               string `json:"working_dir"`
		}
		if json.Unmarshal(update.RawInput, &input) == nil {
			if command := firstNonempty(input.CommandLine, input.NativeCommand, input.Command); command != "" {
				item.Name = clip(command, 4096)
			}
			if cwd := firstNonempty(input.WorkingDirectory, input.WorkingDir, input.NativeDir, input.CWD); cwd != "" {
				directory = []domain.ContentPart{{Kind: "text", Label: "工作目录", Text: clip(cwd, 4096)}}
				if len(item.Parts) > 0 && item.Parts[0].Label == "工作目录" {
					item.Parts = item.Parts[1:]
				}
				item.Parts = append(slices.Clone(directory), item.Parts...)
			}
		}
	}

	if item.Presentation == "command" && len(update.RawOutput) != 0 {
		var result struct {
			CombinedOutput *string
			ExitCode       *int
			NativeOutput   *string `json:"combined_output"`
			NativeExitCode *int    `json:"exit_code"`
		}
		if json.Unmarshal(update.RawOutput, &result) == nil {
			if result.ExitCode == nil {
				result.ExitCode = result.NativeExitCode
			}
			if result.ExitCode != nil {
				item.ExitCode = result.ExitCode
			}
			if result.CombinedOutput == nil {
				result.CombinedOutput = result.NativeOutput
			}
			if len(update.Content) == 0 && result.CombinedOutput != nil {
				value := clip(*result.CombinedOutput, 32<<10)
				item.Text = value
				item.Truncated = item.Truncated || len(value) != len(*result.CombinedOutput)
				return
			}
		}
	}
	if update.Content != nil {
		item.Parts, item.Files = slices.Clone(directory), nil
		budget := 64 << 10
		resourceIndices := map[string]int{}
		for _, part := range update.Content {
			switch {
			case part.Type == "diff" && part.Path != "":
				kind, old := "update", ""
				if part.OldText == nil {
					kind = "add"
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
			case item.Final && part.Type == "content":
				if resource, ok := r.importToolContent(item.ID, part.Content, resourceIndices); ok {
					item.Parts = append(item.Parts, resource)
				}
			}
			if budget <= 0 || len(item.Parts)+len(item.Files) >= 24 {
				item.Truncated = true
				break
			}
		}
	}
}

type lateTool struct {
	item    domain.Item
	blocked bool
}

const maxLateTools = 256

// Keep the original routing of tools after a prompt ends. ACP gives these
// callbacks a tool ID but no turn ID, even if a different prompt has started.
func (r *runtime) rememberTools(t *logicalTurn) {
	if r.lateTools == nil {
		r.lateTools = make(map[string]lateTool)
	}
	blocked := t.interrupted || t.outcome != domain.ExecutionCompleted || t.turn.Presentation == "compaction"
	for id, item := range t.tools {
		if _, exists := r.lateTools[id]; exists {
			continue
		}
		if len(r.lateToolIDs) == maxLateTools {
			delete(r.lateTools, r.lateToolIDs[0])
			r.lateToolIDs = r.lateToolIDs[1:]
		}
		if blocked || item.Status != "unknown" {
			item = domain.Item{ID: item.ID, TurnID: item.TurnID, Status: item.Status, Final: true}
		}
		r.lateTools[id] = lateTool{item: item, blocked: blocked}
		r.lateToolIDs = append(r.lateToolIDs, id)
	}
}

func (r *runtime) updateLateTool(late lateTool, update toolUpdate) error {
	item := late.item
	if late.blocked || !applyToolUpdate(&item, update) {
		return nil
	}
	r.updateToolContent(&item, update)
	late.item = domain.Item{ID: item.ID, TurnID: item.TurnID, Status: item.Status, Final: true}
	r.lateTools[update.ToolCallID] = late
	return r.emit(domain.Event{Type: "item.upsert", Item: &item})
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

type nativeRequest struct {
	WireID    json.RawMessage
	Request   domain.PendingRequest
	AllowOnce string
	ToolCall  toolUpdate
}

func (r *runtime) receivePermission(msg packet) error {
	var payload struct {
		SessionID string `json:"sessionId"`
		ToolCall  toolUpdate
		Options   []struct{ OptionID, Name, Kind string }
	}
	if json.Unmarshal(msg.Params, &payload) != nil {
		return r.reply(context.Background(), msg.ID, permissionOutcome(""), nil)
	}
	request, turn, ok := r.requestSource(payload.SessionID, payload.ToolCall.ToolCallID)
	if !ok {
		return r.reply(context.Background(), msg.ID, permissionOutcome(""), nil)
	}
	request.Title, request.Kind = clip(payload.ToolCall.Title, 4096), "approval"
	if request.Title == "" {
		request.Title = "Antigravity 请求执行工具"
	}
	for _, location := range payload.ToolCall.Locations {
		if filepath.IsAbs(location.Path) {
			request.Text = clip(request.Text+location.Path+"\n", 4096)
		}
	}
	var allowOnce string
	for _, option := range payload.Options {
		if option.OptionID == "" || option.Name == "" {
			continue
		}
		if allowOnce == "" && option.Kind == "allow_once" {
			allowOnce = option.OptionID
		}
		request.Choices = append(request.Choices, domain.RequestChoice{ID: option.OptionID, Label: clip(option.Name, 256), Restriction: strings.HasPrefix(option.Kind, "reject")})
	}
	pending := nativeRequest{WireID: msg.ID, Request: request, AllowOnce: allowOnce, ToolCall: payload.ToolCall}
	if r.session.Config.Permission == yoloPermission {
		if turn != nil {
			r.confirm(turn, nil)
		}
		return r.autoApprove(context.Background(), "", pending, turn)
	}
	if len(request.Choices) == 0 {
		return r.reply(context.Background(), msg.ID, permissionOutcome(""), nil)
	}
	return r.openRequest(pending, turn)
}

func (r *runtime) requestSource(sessionID, toolID string) (domain.PendingRequest, *logicalTurn, bool) {
	if sessionID == "" || sessionID != r.session.ThreadID {
		return domain.PendingRequest{}, nil, false
	}
	request := domain.PendingRequest{
		ID: "agy-request:" + domain.NewID(), SessionID: r.session.ID,
		Revision: 1, State: domain.RequestPending,
	}
	if late, known := r.lateTools[toolID]; known {
		request.TurnID, request.ItemID = late.item.TurnID, late.item.ID
		return request, nil, !late.blocked
	}
	t := r.active
	if t == nil || t.interrupted || t.turn.Presentation == "compaction" {
		return domain.PendingRequest{}, nil, false
	}
	request.TurnID, request.ItemID = t.turn.ID, t.tools[toolID].ID
	return request, t, true
}

func (r *runtime) openRequest(p nativeRequest, turn *logicalTurn) error {
	if turn != nil {
		r.confirm(turn, nil)
	}
	r.requests[p.Request.ID] = p
	return r.emit(domain.Event{Type: "request.open", Request: &p.Request})
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
	if p.Request.Kind == "form" {
		outcome, err := questionResponse(p.Request.Fields, answer)
		if err != nil {
			return err
		}
		return r.resolveRequest(context.Background(), answer.RequestID, p, outcome, false)
	}
	if !slices.ContainsFunc(p.Request.Choices, func(c domain.RequestChoice) bool { return c.ID == answer.Decision }) {
		return errors.New("该审批选项不可用")
	}
	return r.resolveRequest(context.Background(), answer.RequestID, p, permissionOutcome(answer.Decision), false)
}

func (r *runtime) resolveRequest(ctx context.Context, key string, p nativeRequest, outcome any, invalidated bool) error {
	if err := r.reply(ctx, p.WireID, outcome, nil); err != nil {
		return err
	}
	return r.closeRequest(key, p, invalidated, false)
}

func (r *runtime) closeRequest(key string, p nativeRequest, invalidated, automatic bool) error {
	delete(r.requests, key)
	p.Request.Revision++
	p.Request.State = domain.RequestResolved
	if invalidated {
		p.Request.State = domain.RequestInvalidated
	}
	if err := r.emit(domain.Event{Type: "request.resolve", Request: &p.Request, AutomaticallyApproved: automatic}); err != nil {
		return err
	}
	return r.finishIfSettled(r.active)
}

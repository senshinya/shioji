package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"

	"shioji/internal/domain"
)

func (r *runtime) openApproval(turn *activeTurn, in frame) error {
	req := domain.PendingRequest{
		ID: domain.NewID(), SessionID: r.session.ID, Kind: "approval",
		Title: r.redact(in.ToolName), Revision: 1, State: domain.RequestPending,
		Choices: []domain.RequestChoice{{ID: "accept", Label: "批准本次"}, {ID: "decline", Label: "拒绝"}},
	}
	p := approval{native: in.RequestID, agent: in.AgentID}
	var plan bool
	var permission string
	if p.agent == "" {
		if turn == nil {
			return r.reply(p.native, false, nil)
		}
		req.TurnID, req.ItemID, plan, permission = turn.turn.ID, turn.tools[in.ToolUseID], turn.plan, turn.permission
	} else if work, ok := r.work[p.agent]; ok {
		req.WorkID, req.TurnID, plan, permission = work.ID, work.TurnID, r.workPlan[p.agent], r.workPermission[p.agent]
	} else {
		return r.reply(p.native, false, nil)
	}
	if permission == "" {
		permission = r.launchedPermission
	}
	permission = normalizePermission(permission)
	p.request = req
	if !r.canAnswer(p) {
		return r.reply(p.native, false, nil)
	}
	if in.ToolName == "ExitPlanMode" {
		if p.agent == "" {
			if err := r.publishPlan(turn, in.ToolUseID, in.Input); err != nil {
				return err
			}
		}
		return r.reply(p.native, false, nil)
	}
	readOnlyTool := in.ToolName == "AskUserQuestion" || in.ToolName == "Read" || in.ToolName == "Glob" || in.ToolName == "Grep"
	if (plan || permission == permissionReadOnly) && !readOnlyTool {
		return r.reply(p.native, false, nil)
	}
	if in.ToolName == "AskUserQuestion" {
		if len(in.Input) > 64<<10 {
			return r.reply(p.native, false, nil)
		}
		questions, fields, err := questionForm(in.Input, r.config.Key)
		if err != nil {
			return r.reply(p.native, false, nil)
		}
		p.questions = questions
		p.request.Kind, p.request.Title, p.request.Text = "form", "回答问题", ""
		p.request.Fields, p.request.Choices = fields, nil
	} else if permission == permissionAutoApprove || permission == permissionYOLO {
		return r.reply(p.native, true, nil)
	} else {
		text, ok := approvalText(in.ToolName, in.Input)
		if !ok {
			return r.reply(p.native, false, nil)
		}
		p.request.Text = r.redact(text)
	}
	if r.approvals == nil {
		r.approvals = make(map[string]approval)
	}
	r.approvals[p.request.ID] = p
	return r.emit(domain.Event{Type: "request.open", Request: &p.request})
}

func approvalText(tool string, raw json.RawMessage) (string, bool) {
	body := strings.TrimSpace(string(raw))
	if len(body) <= 4000 || tool == "Bash" && len(body) <= 64<<10 {
		return body, true
	}
	if tool != "Write" && tool != "Edit" {
		return "", false
	}
	var args struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return "", false
	}
	path := args.FilePath
	if path == "" {
		path = args.Path
	}
	text := fmt.Sprintf("目标文件 %q\n修改内容见工具记录。", path)
	return text, path != "" && len(text) <= 4000
}

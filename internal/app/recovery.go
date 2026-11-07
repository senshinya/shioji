package app

import (
	"errors"
	"shioji/internal/domain"
	"strings"
)

// PrepareRecoveryDraft does not create a runtime session, move a queue, or send.
func (m *Model) PrepareRecoveryDraft(id string) error {
	d, ok := m.Details[id]
	if !ok || d.Session.Status != "abandoned" && (d.Session.Status != "failed" && d.Session.Status != "interrupted" || d.Session.Recovery == nil || !d.Session.Recovery.CanContinue) {
		return errors.New("请先核对并结束旧轮")
	}
	excerpt := recoveryExcerpt(d.Items)
	reason := "旧轮已手动结束"
	if d.Session.Status == "failed" {
		reason = "上一轮执行失败"
	}
	if d.Session.Status == "interrupted" {
		reason = "上一轮已停止"
	}
	text := "接续会话「" + d.Session.Title + "」。" + reason + "，以下为最近保存的对话，继续前请先检查已有结果。\n\n" + excerpt
	if old := strings.TrimSpace(m.Drafts["session:"+id]); old != "" {
		text += "\n\n尚未发送的补充：\n" + old
	}
	if old := strings.TrimSpace(m.Drafts["home"]); old != "" {
		text += "\n\n原有新会话草稿：\n" + old
	}
	if len(text) > 128<<10 {
		return errors.New("接续内容超过输入上限，请先整理草稿")
	}
	m.Drafts["home"] = text
	m.Attachments["home"] = append(m.Attachments["home"], m.Attachments["session:"+id]...)
	if m.homeConfigContext != "" {
		if m.HarnessConfigs == nil {
			m.HarnessConfigs = map[string]domain.RunConfig{}
		}
		m.HarnessConfigs[m.homeConfigContext] = m.Configs["home"]
	}
	m.homeConfigContext = d.Session.WorkspaceID + "/" + d.Session.HarnessID
	m.Configs["home"] = d.Session.Config
	if m.HomeHarnesses == nil {
		m.HomeHarnesses = map[string]string{}
	}
	m.HomeHarnesses[d.Session.WorkspaceID] = d.Session.HarnessID
	m.WorkspaceID = d.Session.WorkspaceID
	m.SessionID = ""
	return nil
}

// Only conversation text enters the handoff draft. Tool output, private forms,
// pending answers and queued messages remain owned by the original session.
func recoveryExcerpt(items []domain.Item) string {
	var parts []string
	remaining := 16 << 10
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Kind != "user" && item.Kind != "assistant" {
			continue
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		text := item.Text
		if len(text) > remaining {
			text = strings.ToValidUTF8(text[len(text)-remaining:], "�")
		}
		label := "助手："
		if item.Kind == "user" {
			label = "用户："
		}
		parts = append(parts, label+text)
		remaining -= len(text)
		if remaining <= 0 {
			break
		}
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n\n")
}

// BeginContinuation is an explicit new turn in the original thread. It does
// not consume the ordinary draft, attachments, or the saved queue.
func (m *Model) BeginContinuation() (domain.Submission, error) {
	var none domain.Submission
	if !m.Connected || m.RunReadiness().State != "ready" || m.WorkerMinor < 7 {
		return none, errors.New("请先连接就绪的执行设备")
	}
	d, ok := m.Details[m.SessionID]
	if !ok || d.Session.Recovery == nil || !d.Session.Recovery.CanContinue || d.Session.Status != "abandoned" && d.Session.Status != "interrupted" && d.Session.Status != "failed" {
		return none, errors.New("原会话尚不能继续，请重新核对")
	}
	if d.Session.ContextChanged || m.ArchivedContext() != "" {
		return none, errors.New("请先确认原会话的工作目录与分支")
	}
	for _, request := range d.Requests {
		if request.State.AwaitingResolution() {
			return none, errors.New("请先处理原会话的待办请求")
		}
	}
	for _, input := range m.Pending {
		if input.SessionID == m.SessionID {
			return none, errors.New("已有提交等待确认")
		}
	}
	if !m.RunReadiness().Capabilities.Continue {
		return none, errors.New("此 Agent 不支持继续原会话")
	}
	in := domain.Submission{HarnessID: d.Session.HarnessID, OperationID: domain.NewID(), SessionID: d.Session.ID, WorkspaceID: d.Session.WorkspaceID, Config: m.Config(), Text: domain.ContinuationPrompt, Continue: true}
	m.Pending[in.OperationID] = in
	m.PendingDevices[in.OperationID] = m.ConnectionID()
	return in, nil
}

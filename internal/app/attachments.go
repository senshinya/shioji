package app

import (
	"errors"
	"shioji/internal/domain"
)

type DraftAttachment struct {
	// InlinePosition is a one-based rune offset in the client composer.
	InlinePosition int               `json:"inlinePosition,omitempty"`
	Attachment     domain.Attachment `json:"attachment"`
	DeviceID       string            `json:"deviceId,omitempty"`
	WorkspaceID    string            `json:"workspaceId,omitempty"`
	Ready          bool              `json:"ready,omitempty"`
	Error          string            `json:"error,omitempty"`
	UploadApproved bool              `json:"uploadApproved,omitempty"`
	Transferred    int64             `json:"-"`
}

func (m *Model) workspaceDevice(workspace string) string {
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == workspace {
			return w.DeviceID
		}
	}
	return ""
}
func (m *Model) SubmissionAttachments(workspace string) ([]domain.Attachment, error) {
	return m.submissionAttachments(m.DraftKey(), workspace)
}
func (m *Model) submissionAttachments(key, workspace string) ([]domain.Attachment, error) {
	var out []domain.Attachment
	for _, draft := range m.Attachments[key] {
		if draft.Attachment.Kind == domain.PathAttachmentResource && (m.WorkerMinor < 47 || !m.RunReadiness().Capabilities.PathAttachments) {
			return nil, errors.New("当前执行服务或 Agent 不支持远程路径引用，请升级后重试")
		}
		if draft.Attachment.Kind == domain.DiffCommentResource && m.WorkerMinor < 33 {
			return nil, errors.New("请先升级执行服务以发送代码评论")
		}
		if m.WorkerMinor < 6 {
			return nil, errors.New("请先升级执行服务以发送附件")
		}
		if !draft.Ready || draft.WorkspaceID != workspace || draft.DeviceID != m.workspaceDevice(workspace) {
			return nil, errors.New("附件尚未准备好，请重试上传")
		}
		out = append(out, draft.Attachment)
	}
	return out, nil
}
func (m *Model) clearAcceptedAttachments(key string, accepted []domain.Attachment) {
	var kept []DraftAttachment
	for _, draft := range m.Attachments[key] {
		found := false
		for _, a := range accepted {
			if a == draft.Attachment {
				found = true
				break
			}
		}
		if !found {
			kept = append(kept, draft)
		}
	}
	m.Attachments[key] = kept
}

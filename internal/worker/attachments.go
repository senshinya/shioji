package worker

import (
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

func (s *Server) attachmentCommand(method string, body []byte) (any, error) {
	if method == "attachment.put" {
		in, err := decode[protocol.AttachmentPut](body)
		if err != nil {
			return nil, err
		}
		if in.Attachment.Kind != "" && in.Attachment.Kind != domain.PathAttachmentResource {
			return nil, errors.New("internal references must be created by the owning worker")
		}
		w, err := s.store.Workspace(in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if w.Archived {
			return nil, errors.New("restore workspace before uploading")
		}
		return s.attachments.Put(in.WorkspaceID, in.Attachment, in.Offset, in.Data)
	}
	var in protocol.AttachmentGet
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	if _, err := s.store.Workspace(in.WorkspaceID); err != nil {
		return nil, err
	}
	return s.attachments.Get(in.WorkspaceID, in.ID, in.Offset)
}
func (s *Server) attachmentInputs(in domain.Submission) ([]harness.AttachmentInput, error) {
	if err := in.ValidateContent(); err != nil {
		return nil, err
	}
	if err := s.validateSkills(in); err != nil {
		return nil, err
	}
	var out []harness.AttachmentInput
	for _, a := range in.Attachments {
		path, err := s.attachments.Path(in.WorkspaceID, a)
		if err != nil {
			return nil, errors.New("附件尚未上传完成或不属于当前工作区")
		}
		if a.Kind == domain.DiffCommentResource {
			comment, branch, e := s.commentInput(in, a)
			if e != nil {
				return nil, e
			}
			out = append(out, harness.AttachmentInput{Attachment: a, Comment: &comment, ExecutionBranch: branch})
		} else if a.Kind == domain.DiffSnapshotResource {
			return nil, errors.New("Diff snapshots cannot be sent as model input")
		} else {
			out = append(out, harness.AttachmentInput{Attachment: a, Path: path})
		}
	}
	size := len(in.Text)
	for _, input := range out {
		if input.Comment != nil {
			encoded, _ := json.Marshal(input.Comment)
			size += len(encoded) + len(input.ExecutionBranch)
		}
	}
	if size > 128<<10 {
		return nil, errors.New("消息、评论及选中文字合计不能超过 128 KiB")
	}
	return out, nil
}

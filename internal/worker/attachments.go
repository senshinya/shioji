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
	var out []harness.AttachmentInput
	for _, a := range in.Attachments {
		path, err := s.attachments.Path(in.WorkspaceID, a)
		if err != nil {
			return nil, errors.New("附件尚未上传完成或不属于当前工作区")
		}
		out = append(out, harness.AttachmentInput{Attachment: a, Path: path})
	}
	return out, nil
}

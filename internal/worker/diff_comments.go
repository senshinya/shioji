package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"unicode/utf8"
)

func (s *Server) putDiffResource(workspace, kind, name string, value any) (domain.Attachment, error) {
	if s.attachments == nil {
		return domain.Attachment{}, errors.New("reference storage unavailable")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return domain.Attachment{}, err
	}
	a, err := attachments.DescribeDiffResource(kind, name, data)
	if err != nil {
		return a, err
	}
	for offset := 0; offset < len(data); offset += attachments.ChunkSize {
		if _, err = s.attachments.Put(workspace, a, int64(offset), data[offset:min(offset+attachments.ChunkSize, len(data))]); err != nil {
			return domain.Attachment{}, err
		}
	}
	return a, nil
}
func (s *Server) storeDiffSnapshot(workspace string, versions domain.FileVersions) (domain.Attachment, error) {
	versions.Snapshot = domain.Attachment{}
	return s.putDiffResource(workspace, domain.DiffSnapshotResource, "diff-snapshot.json", versions)
}
func (s *Server) readDiffResource(workspace string, a domain.Attachment, out any) error {
	if s.attachments == nil {
		return errors.New("reference storage unavailable")
	}
	path, err := s.attachments.Path(workspace, a)
	if err != nil {
		return errors.New("原 Diff 快照不可用，请重新选择")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func sourceLocation(text string, offset int) (line, column int) {
	line = strings.Count(text[:offset], "\n") + 1
	start := strings.LastIndexByte(text[:offset], '\n') + 1
	return line, utf8.RuneCountInString(text[start:offset]) + 1
}
func commentLabel(path string, fragments []domain.CodeSelection) string {
	first, last := fragments[0], fragments[len(fragments)-1]
	label := fmt.Sprintf("%s:%d", filepath.Base(path), first.StartLine)
	if first.StartLine != last.EndLine {
		label += fmt.Sprintf("–%d", last.EndLine)
	}
	return label
}
func (s *Server) captureDiffComment(in protocol.DiffCommentInput) (domain.Attachment, error) {
	var out domain.Attachment
	if in.Snapshot.Kind != domain.DiffSnapshotResource || !utf8.ValidString(in.Comment) || strings.TrimSpace(in.Comment) == "" || len(in.Comment) > 128<<10 || len(in.Fragments) == 0 || len(in.Fragments) > 256 {
		return out, errors.New("invalid code comment")
	}
	var versions domain.FileVersions
	if err := s.readDiffResource(in.WorkspaceID, in.Snapshot, &versions); err != nil {
		return out, err
	}
	status, err := s.gitStatus(in.GitQuery)
	if err != nil {
		return out, err
	}
	if !status.Applicable || versions.Comparison.Repository != status.Checkout || !validReviewPath(versions.Path) {
		return out, errors.New("Diff does not belong to this execution directory")
	}
	content := domain.CodeCommentContent{FileSnapshot: in.Snapshot.SHA256, Comparison: versions.Comparison, Path: versions.Path, Comment: in.Comment}
	for _, part := range in.Fragments {
		source := versions.New
		if part.Side == "base" {
			source = versions.Old
		} else if part.Side != "target" {
			return out, errors.New("invalid selection side")
		}
		if part.Start < 0 || part.End <= part.Start || part.End > len(source) || part.Start < len(source) && !utf8.RuneStart(source[part.Start]) || part.End < len(source) && !utf8.RuneStart(source[part.End]) {
			return out, errors.New("invalid UTF-8 selection")
		}
		part.Text = source[part.Start:part.End]
		part.StartLine, part.StartColumn = sourceLocation(source, part.Start)
		part.EndLine, _ = sourceLocation(source, part.End-1)
		_, part.EndColumn = sourceLocation(source, part.End)
		content.Fragments = append(content.Fragments, part)
	}
	record := domain.DiffCommentRecord{Content: content, Snapshot: in.Snapshot}
	return s.putDiffResource(in.WorkspaceID, domain.DiffCommentResource, commentLabel(content.Path, content.Fragments), record)
}
func (s *Server) commentInput(in domain.Submission, a domain.Attachment) (domain.CodeCommentContent, string, error) {
	var record domain.DiffCommentRecord
	if err := s.readDiffResource(in.WorkspaceID, a, &record); err != nil {
		return record.Content, "", err
	}
	status, err := s.gitStatus(protocol.GitQuery{WorkspaceID: in.WorkspaceID, SessionID: in.SessionID})
	if err != nil {
		return record.Content, "", err
	}
	if !status.Applicable || record.Content.Comparison.Repository != status.Checkout {
		return record.Content, "", errors.New("评论不属于当前执行目录")
	}
	var snapshot domain.FileVersions
	if record.Snapshot.Kind != domain.DiffSnapshotResource {
		return record.Content, "", errors.New("invalid Diff snapshot")
	}
	if err = s.readDiffResource(in.WorkspaceID, record.Snapshot, &snapshot); err != nil {
		return record.Content, "", err
	}
	branch := status.Branch
	if branch == "" {
		branch = "HEAD @ " + status.Head
	}
	return record.Content, branch, nil
}

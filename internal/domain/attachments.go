package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Attachment identifies an immutable resource on the workspace's worker.
// SourcePath records a file reference's device-owned origin; resource storage
// paths and content bytes stay out of journals and queue entries.
type Attachment struct {
	Kind       string `json:"kind,omitempty"`
	SourcePath string `json:"sourcePath,omitempty"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

const DiffCommentResource = "diff-comment"
const DiffSnapshotResource = "diff-snapshot"
const PathAttachmentResource = "file-path"
const MaxMessageAttachments = 8

func (a Attachment) InlineReference() bool {
	return a.SourcePath != "" || a.Kind == DiffCommentResource || a.Kind == PathAttachmentResource
}

func (in Submission) ValidateContent() error {
	if err := in.ValidateGoal(); err != nil {
		return err
	}
	if !utf8.ValidString(in.Text) || len(in.Text) > 128<<10 || (strings.TrimSpace(in.Text) == "" && len(in.Attachments) == 0 && len(in.Skills) == 0) {
		return errors.New("message requires text or an attachment")
	}
	if len(in.Skills) > 8 {
		return errors.New("最多引用 8 个 Skill")
	}
	skillIDs := map[string]bool{}
	for _, skill := range in.Skills {
		if skill.ID == "" || skill.Name == "" || len(skill.ID) > 4096 || len(skill.Name) > 256 || !utf8.ValidString(skill.ID+skill.Name) || strings.ContainsAny(skill.ID+skill.Name, "\x00\r\n") || skillIDs[skill.ID] {
			return errors.New("invalid Skill reference")
		}
		skillIDs[skill.ID] = true
	}
	comments, files := 0, 0
	for _, a := range in.Attachments {
		if a.Kind == DiffCommentResource {
			comments++
		} else {
			files++
		}
	}
	if comments > 8 {
		return errors.New("最多添加 8 条代码评论")
	}
	if files > MaxMessageAttachments {
		return errors.New("最多添加 8 个附件")
	}
	var total int64
	seen := map[string]bool{}
	for _, a := range in.Attachments {
		if a.Kind != "" && a.Kind != DiffCommentResource && a.Kind != DiffSnapshotResource && a.Kind != PathAttachmentResource {
			return errors.New("invalid reference resource")
		}
		if a.Kind == PathAttachmentResource && (a.SourcePath != "" || a.MIME != "application/octet-stream") {
			return errors.New("invalid path attachment")
		}
		if len(a.SourcePath) > 4096 || !utf8.ValidString(a.SourcePath) || strings.ContainsAny(a.SourcePath, "\x00\r\n") || a.ID == "" || seen[a.ID] || len(a.ID) > 64 || !utf8.ValidString(a.Name) || len(a.Name) > 256 || a.Size <= 0 || a.Size > 8<<20 || len(a.SHA256) != 64 || len(a.MIME) > 64 {
			return errors.New("invalid attachment")
		}
		seen[a.ID] = true
		total += a.Size
	}
	if len(in.ReferencePositions) > len(in.Attachments)+len(in.Skills) {
		return errors.New("invalid inline references")
	}
	usedPositions, usedRefs := map[int]bool{}, map[string]bool{}
	for _, ref := range in.ReferencePositions {
		key := "file:" + ref.AttachmentID
		valid := ref.AttachmentID != "" && ref.SkillID == "" && seen[ref.AttachmentID]
		if ref.SkillID != "" {
			key = "skill:" + ref.SkillID
			valid = ref.AttachmentID == "" && skillIDs[ref.SkillID]
		}
		if !valid || ref.Position < 1 || ref.Position > utf8.RuneCountInString(in.Text)+len(in.ReferencePositions) || usedPositions[ref.Position] || usedRefs[key] {
			return errors.New("invalid inline references")
		}
		usedPositions[ref.Position], usedRefs[key] = true, true
	}
	if total > 16<<20 {
		return errors.New("附件总大小不能超过 16 MiB")
	}
	return nil
}

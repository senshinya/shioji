package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Attachment identifies an immutable resource on the workspace's worker.
// Paths and bytes are deliberately absent from journals and queue entries.
type Attachment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	MIME   string `json:"mime"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (in Submission) ValidateContent() error {
	if !utf8.ValidString(in.Text) || len(in.Text) > 128<<10 || (strings.TrimSpace(in.Text) == "" && len(in.Attachments) == 0) {
		return errors.New("message requires text or an attachment")
	}
	if len(in.Attachments) > 8 {
		return errors.New("最多添加 8 个附件")
	}
	var total int64
	seen := map[string]bool{}
	for _, a := range in.Attachments {
		if a.ID == "" || seen[a.ID] || len(a.ID) > 64 || !utf8.ValidString(a.Name) || len(a.Name) > 256 || a.Size <= 0 || a.Size > 8<<20 || len(a.SHA256) != 64 || len(a.MIME) > 64 {
			return errors.New("invalid attachment")
		}
		seen[a.ID] = true
		total += a.Size
	}
	if total > 16<<20 {
		return errors.New("附件总大小不能超过 16 MiB")
	}
	return nil
}

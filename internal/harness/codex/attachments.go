package codex

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"strconv"
	"strings"
)

type AttachmentInput = harness.AttachmentInput

func attachmentInputs(text string, attachments []AttachmentInput) ([]wire.Input, error) {
	inputs := []wire.Input{}
	if strings.TrimSpace(text) != "" {
		inputs = append(inputs, wire.Input{Type: "text", Text: text})
	}
	for _, a := range attachments {
		if a.Comment != nil {
			inputs = append(inputs, wire.Input{Type: "text", Text: codeCommentText(*a.Comment, a.ExecutionBranch)})
			continue
		}
		if a.Attachment.Kind == domain.PathAttachmentResource {
			if !filepath.IsAbs(a.Path) || a.Attachment.SourcePath != "" || a.Attachment.MIME != "application/octet-stream" {
				return nil, errors.New("invalid owning-device file path")
			}
			inputs = append(inputs, wire.Input{Type: "text", Text: "Uploaded file: " + a.Attachment.Name + "\nPath on this device: " + strconv.Quote(a.Path)})
			continue
		}
		if a.Attachment.Kind != "" {
			return nil, errors.New("unsupported reference resource")
		}
		if strings.HasPrefix(a.Attachment.MIME, "image/") {
			inputs = append(inputs, wire.Input{Type: "localImage", Path: a.Path})
			continue
		}
		if a.Attachment.MIME != "text/plain" {
			return nil, errors.New("unsupported attachment type")
		}
		f, err := os.Open(a.Path)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > 128<<10 {
			return nil, errors.New("text attachment exceeds 128 KiB")
		}
		label := "Attachment: " + a.Attachment.Name
		if a.Attachment.SourcePath != "" {
			label = "Workspace file: " + a.Attachment.SourcePath
		}
		inputs = append(inputs, wire.Input{Type: "text", Text: label + "\n\n" + string(data)})
	}
	return inputs, nil
}

func codeCommentText(comment domain.CodeCommentContent, branch string) string {
	endpoint := func(e domain.DiffEndpoint) string {
		if e.Kind == "empty" {
			return "空版本"
		}
		if e.Kind == "worktree" {
			return "工作目录 · " + e.Branch + "，基于 " + e.Commit
		}
		name := e.Name
		if e.Kind == "head" && e.Branch != "" {
			name = e.Branch + " · HEAD"
		}
		return name + " @ " + e.Commit
	}
	var out strings.Builder
	fmt.Fprintf(&out, "代码评论\n比较基准：%s\n比较目标：%s\n文件：%s\n", endpoint(comment.Comparison.Base), endpoint(comment.Comparison.Target), comment.Path)
	if comment.FileSnapshot != "" {
		fmt.Fprintf(&out, "文件快照：%s\n", comment.FileSnapshot)
	}
	for _, part := range comment.Fragments {
		side := "目标侧"
		if part.Side == "base" {
			side = "基准侧"
		}
		endLine := part.StartLine + strings.Count(part.Text, "\n")
		fmt.Fprintf(&out, "选中侧：%s\n位置：%s:%d:%d–%d:%d（末端不含）\n选中文字：\n%s\n", side, comment.Path, part.StartLine, part.StartColumn, endLine, part.EndColumn, part.Text)
	}
	fmt.Fprintf(&out, "评论：%s\n会话工作目录分支：%s", comment.Comment, branch)
	return out.String()
}

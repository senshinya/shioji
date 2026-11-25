package claudecode

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func inputBlocks(in harness.TurnInput) ([]map[string]any, error) {
	if len(in.Skills) != 0 || in.Goal != nil || in.ForkContext != nil || in.QuestionReply != nil {
		return nil, harness.Unsupported("claude-structured-input")
	}
	var out []map[string]any
	if text := strings.TrimSpace(in.Text); text != "" {
		// Only the adapter may issue native session commands.
		switch strings.Fields(text)[0] {
		case "/compact", "/clear", "/reset", "/resume", "/rewind", "/permissions", "/mcp":
			return nil, harness.Unsupported("claude-session-command")
		}
		out = append(out, map[string]any{"type": "text", "text": in.Text})
	}
	total := 0
	for _, resource := range in.Attachments {
		if resource.Comment != nil {
			out = append(out, map[string]any{"type": "text", "text": harness.FormatCodeComment(*resource.Comment, resource.ExecutionBranch)})
			continue
		}
		if resource.Attachment.Kind == domain.PathAttachmentResource {
			return nil, harness.Unsupported("claude-path-attachment")
		}
		f, err := os.Open(resource.Path)
		if err != nil {
			return nil, errors.New("无法读取已保存的附件")
		}
		data, err := io.ReadAll(io.LimitReader(f, (5<<20)+1))
		_ = f.Close()
		if err != nil || len(data) > 5<<20 || total+len(data) > 12<<20 {
			return nil, errors.New("Claude Code 附件超出大小限制")
		}
		total += len(data)
		switch resource.Attachment.MIME {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			out = append(out, map[string]any{"type": "image", "source": map[string]string{
				"type": "base64", "media_type": resource.Attachment.MIME, "data": base64.StdEncoding.EncodeToString(data)}})
		default:
			if len(data) > 128<<10 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
				return nil, errors.New("此 Claude Code Profile 仅支持图片和 UTF-8 文本附件")
			}
			out = append(out, map[string]any{"type": "text", "text": "附件 " + resource.Attachment.Name + "\n" + string(data)})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("消息需要文字或附件")
	}
	return out, nil
}

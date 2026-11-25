package claudecode

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func inputBlocks(in harness.TurnInput, attachmentDir string) ([]map[string]any, error) {
	if len(in.Skills) != 0 || in.Goal != nil || in.ForkContext != nil || in.QuestionReply != nil {
		return nil, harness.Unsupported("claude-structured-input")
	}
	var out []map[string]any
	if text := strings.TrimSpace(in.Text); text != "" {
		// Only the adapter may issue native session commands.
		command := strings.Fields(text)[0]
		if strings.HasPrefix(command, "/shioji-") {
			return nil, harness.Unsupported("claude-session-command")
		}
		switch command {
		case "/compact", "/clear", "/reset", "/resume", "/rewind", "/permissions", "/mcp", "/goal":
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
			path, err := stagePathAttachment(attachmentDir, resource)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"type": "text", "text": "Uploaded file " + strconv.Quote(resource.Attachment.Name) + "\nRead-only snapshot on the execution device: " + strconv.Quote(path)})
			continue
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

func (r *runtime) attachmentDirectory(session domain.Session) string {
	if r.opts.SessionDirectory != "" {
		return filepath.Join(r.opts.SessionDirectory, "claude-attachments")
	}
	digest := sha256.Sum256([]byte(session.ID))
	return filepath.Join(r.home, "shioji-attachments", hex.EncodeToString(digest[:]))
}

var attachmentExtension = regexp.MustCompile(`^\.[A-Za-z0-9]{1,12}$`)

func stagePathAttachment(dir string, input harness.AttachmentInput) (string, error) {
	resource := input.Attachment
	if !filepath.IsAbs(dir) || !filepath.IsAbs(input.Path) || resource.SourcePath != "" ||
		resource.MIME != "application/octet-stream" || resource.Size <= 0 || resource.Size > 8<<20 {
		return "", errors.New("Claude Code 文件附件无效")
	}
	file, err := os.OpenFile(input.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("无法读取文件附件")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != resource.Size {
		return "", errors.New("文件附件与已接纳的快照不符")
	}
	raw, err := io.ReadAll(io.LimitReader(file, resource.Size+1))
	digest := sha256.Sum256(raw)
	if err != nil || int64(len(raw)) != resource.Size || !strings.EqualFold(hex.EncodeToString(digest[:]), resource.SHA256) {
		return "", errors.New("文件附件与已接纳的快照不符")
	}
	name := resource.SHA256
	if ext := filepath.Ext(resource.Name); attachmentExtension.MatchString(ext) {
		name += ext
	}
	path := filepath.Join(dir, name)
	if err := savePrivate(path, raw); err != nil {
		return "", errors.New("无法准备只读文件附件")
	}
	return path, nil
}

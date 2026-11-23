package antigravity

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (r *runtime) attachmentBlock(file harness.AttachmentInput) (contentBlock, error) {
	a := file.Attachment
	if file.Comment != nil {
		return contentBlock{Type: "text", Text: harness.FormatCodeComment(*file.Comment, file.ExecutionBranch)}, nil
	}
	switch a.Kind {
	case domain.PathAttachmentResource:
		if !filepath.IsAbs(file.Path) || a.SourcePath != "" || a.MIME != "application/octet-stream" {
			return contentBlock{}, errors.New("无效的设备附件路径")
		}
		return contentBlock{Type: "text", Text: "Uploaded file: " + strconv.Quote(a.Name) + "\nPath on this device: " + strconv.Quote(file.Path)}, nil
	case "":
	default:
		return contentBlock{}, harness.Unsupported("attachment")
	}
	switch a.MIME {
	case "text/plain":
		data, err := readAttachment(file.Path, 128<<10)
		if err != nil {
			return contentBlock{}, err
		}
		if !utf8.Valid(data) {
			return contentBlock{}, errors.New("文本附件必须使用 UTF-8")
		}
		label := "Attachment: " + a.Name
		if a.SourcePath != "" {
			label = "Workspace file snapshot: " + a.SourcePath
		}
		text := label + "\n\n" + string(data)
		if !r.native.AgentCapabilities.PromptCapabilities.EmbeddedContext {
			return contentBlock{Type: "text", Text: text}, nil
		}
		uri := "shioji://attachment/" + a.ID
		return contentBlock{Type: "resource", Resource: &textResource{URI: uri, MIMEType: "text/plain", Text: text}}, nil
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		if !r.native.AgentCapabilities.PromptCapabilities.Image {
			return contentBlock{}, harness.Unsupported("image-attachment")
		}
		data, err := readAttachment(file.Path, 8<<20)
		if err != nil {
			return contentBlock{}, err
		}
		return contentBlock{Type: "image", MIMEType: a.MIME, Data: data}, nil
	default:
		return contentBlock{}, harness.Unsupported("attachment")
	}
}

func readAttachment(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法读取该附件")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) == 0 {
		return nil, errors.New("无法读取该附件")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("附件超过 %d KiB", limit>>10)
	}
	return data, nil
}

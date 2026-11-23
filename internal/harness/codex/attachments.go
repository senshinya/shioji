package codex

import (
	"errors"
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
			inputs = append(inputs, wire.Input{Type: "text", Text: harness.FormatCodeComment(*a.Comment, a.ExecutionBranch)})
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

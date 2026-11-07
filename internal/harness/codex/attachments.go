package codex

import (
	"errors"
	"io"
	"os"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"strings"
)

type AttachmentInput = harness.AttachmentInput

func attachmentInputs(text string, attachments []AttachmentInput) ([]wire.Input, error) {
	inputs := []wire.Input{}
	if strings.TrimSpace(text) != "" {
		inputs = append(inputs, wire.Input{Type: "text", Text: text})
	}
	for _, a := range attachments {
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
		inputs = append(inputs, wire.Input{Type: "text", Text: "Attachment: " + a.Attachment.Name + "\n\n" + string(data)})
	}
	return inputs, nil
}

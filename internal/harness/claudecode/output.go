package claudecode

import (
	"encoding/base64"
	"encoding/json"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (r *runtime) toolResultParts(item string, raw json.RawMessage) []domain.ContentPart {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []domain.ContentPart{{Kind: "text", Text: text}}
	}
	var native []struct {
		Type, Text string
		Source     struct {
			Type, Data string
			MIME       string `json:"media_type"`
		}
	}
	if json.Unmarshal(raw, &native) != nil {
		return nil
	}
	var out []domain.ContentPart
	for index, block := range native {
		if block.Type == "text" {
			out = append(out, domain.ContentPart{Kind: "text", Text: block.Text})
			continue
		}
		if block.Type != "image" || block.Source.Type != "base64" || r.opts.ImportResource == nil ||
			base64.StdEncoding.DecodedLen(len(block.Source.Data)) > 8<<20 {
			continue
		}
		name := ""
		switch block.Source.MIME {
		case "image/png":
			name = "Claude Code.png"
		case "image/jpeg":
			name = "Claude Code.jpg"
		case "image/webp":
			name = "Claude Code.webp"
		case "image/gif":
			name = "Claude Code.gif"
		default:
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(block.Source.Data)
		if err != nil || len(raw) == 0 {
			continue
		}
		image, err := r.opts.ImportResource(harness.OutputResource{
			Kind: "image", SessionID: r.session.ID, ItemID: item, Index: index, Name: name, Data: raw,
		})
		if err == nil && image.ID != "" {
			out = append(out, domain.ContentPart{Kind: "image", Attachment: &image})
		}
	}
	return out
}

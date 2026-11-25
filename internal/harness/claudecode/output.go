package claudecode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

// The SDK sidecar is frame-level. An ambiguous result frame still has its
// per-call content, but cannot reliably associate the sidecar with any call.
func singleToolResultSidecar(blocks contentBlocks, raw json.RawMessage) json.RawMessage {
	count := 0
	for _, block := range blocks {
		if block.Type == "tool_result" {
			count++
		}
	}
	if count == 1 {
		return raw
	}
	return nil
}

func sdkToolPresentation(name string) string {
	switch {
	case name == "Edit" || name == "Write":
		return "files"
	case strings.HasPrefix(name, "mcp__"):
		return "mcp"
	default:
		return "result"
	}
}

func (r *runtime) completeTool(item domain.Item, result content, sidecar json.RawMessage) domain.Item {
	return r.applyToolResult(item, result, sidecar, true)
}

func (r *runtime) applyToolResult(item domain.Item, result content, sidecar json.RawMessage, live bool) domain.Item {
	item.Final, item.Status = true, "completed"
	item.Presentation = sdkToolPresentation(item.Name)
	if result.IsError {
		item.Status = "failed"
	}
	parts, limited := r.toolResultParts(item.ID, result.Content, live)
	item.Parts, item.Truncated = parts, item.Truncated || limited
	switch {
	case !result.IsError && (item.Name == "Edit" || item.Name == "Write"):
		if file, limited := sdkFileResult(sidecar); file.Path != "" {
			item.Presentation = "files"
			item.Files = []domain.FileChange{file}
			item.Truncated = item.Truncated || limited
		}
	case strings.HasPrefix(item.Name, "mcp__"):
		parts, limited := sdkMCPResult(sidecar)
		item.Parts = append(item.Parts, parts...)
		item.Truncated = item.Truncated || limited
	}
	return item
}

func sdkFileResult(raw json.RawMessage) (domain.FileChange, bool) {
	var result struct {
		FilePath, Type  string
		StructuredPatch []struct {
			OldStart, OldLines, NewStart, NewLines int
			Lines                                  []string
		}
	}
	if json.Unmarshal(raw, &result) != nil || result.FilePath == "" || len(result.FilePath) > 4096 ||
		strings.ContainsAny(result.FilePath, "\r\n\x00") {
		return domain.FileChange{}, false
	}
	file := domain.FileChange{Path: result.FilePath, Kind: "update"}
	if result.Type == "create" {
		file.Kind = "add"
	}
	var patch strings.Builder
	for _, hunk := range result.StructuredPatch {
		if hunk.OldStart < 0 || hunk.OldLines < 0 || hunk.NewStart < 0 || hunk.NewLines < 0 {
			return file, true
		}
		fmt.Fprintf(&patch, "@@ -%d,%d +%d,%d @@\n", hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines)
		if patch.Len() > 12<<10 {
			return file, true
		}
		for _, line := range hunk.Lines {
			if line == "" || !strings.ContainsRune(" +-\\", rune(line[0])) ||
				strings.ContainsAny(line, "\r\n\x00") || patch.Len()+len(line)+1 > 12<<10 {
				return file, true
			}
			patch.WriteString(line)
			patch.WriteByte('\n')
		}
	}
	file.Patch = patch.String()
	return file, false
}

func sdkMCPResult(raw json.RawMessage) ([]domain.ContentPart, bool) {
	var result struct {
		StructuredContent json.RawMessage
		ResourceLinks     []struct{ URI, Name string }
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, false
	}
	var parts []domain.ContentPart
	limited := len(result.ResourceLinks) > 8
	if len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		var formatted bytes.Buffer
		if len(result.StructuredContent) <= 12<<10 && json.Indent(&formatted, result.StructuredContent, "", "  ") == nil && formatted.Len() <= 12<<10 {
			parts = append(parts, domain.ContentPart{Kind: "code", Label: "结构化结果", Text: formatted.String()})
		} else {
			limited = true
		}
	}
	for _, link := range result.ResourceLinks[:min(8, len(result.ResourceLinks))] {
		uri, err := url.Parse(link.URI)
		if err != nil || len(link.URI) > 1024 || uri.Scheme == "" || uri.User != nil || uri.RawQuery != "" ||
			uri.ForceQuery || uri.Fragment != "" || uri.Opaque != "" {
			continue
		}
		label := "资源 URI"
		if link.Name != "" && len(link.Name) <= 256 && !strings.ContainsAny(link.Name, "\r\n\x00") {
			label = link.Name
		}
		parts = append(parts, domain.ContentPart{Kind: "resource", Label: label, Text: uri.String()})
	}
	return parts, limited
}

func boundedToolText(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	for maxBytes > 0 && !utf8.RuneStart(text[maxBytes]) {
		maxBytes--
	}
	return text[:maxBytes]
}

func (r *runtime) toolResultParts(item string, raw json.RawMessage, live bool) ([]domain.ContentPart, bool) {
	budget := 12 << 10
	var text string
	if json.Unmarshal(raw, &text) == nil {
		bounded := boundedToolText(text, budget)
		return []domain.ContentPart{{Kind: "text", Text: bounded}}, len(text) != len(bounded)
	}
	var native []struct {
		Type, Text string
		Source     struct {
			Type, Data string
			MIME       string `json:"media_type"`
		}
	}
	if json.Unmarshal(raw, &native) != nil {
		return nil, false
	}
	var out []domain.ContentPart
	index, limited := 0, false
	for _, block := range native {
		if len(out) >= 24 {
			limited = true
			break
		}
		if block.Type == "text" {
			bounded := boundedToolText(block.Text, budget)
			budget -= len(bounded)
			limited = limited || len(bounded) != len(block.Text)
			if bounded != "" {
				out = append(out, domain.ContentPart{Kind: "text", Text: bounded})
			}
			continue
		}
		if block.Type == "image" && !live {
			out = append(out, domain.ContentPart{Kind: "text", Label: "图片", Text: "历史图片无法预览"})
			continue
		}
		if block.Type != "image" || block.Source.Type != "base64" || r.opts.ImportResource == nil ||
			index >= domain.MaxMessageAttachments ||
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
		index++
		if err == nil && image.ID != "" {
			out = append(out, domain.ContentPart{Kind: "image", Attachment: &image})
		}
	}
	return out, limited
}

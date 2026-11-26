package conversationui

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"shioji/internal/domain"
)

// ToolTitle fills in an omitted filename on a single-file tool result. The
// detail can omit its duplicate filename when the heading provides it.
func ToolTitle(item domain.Item) (title, inlinePath string) {
	if item.Presentation != "files" || len(item.Files) != 1 || item.Files[0].Path == "" || item.Name == "" {
		return item.Name, ""
	}
	file := item.Files[0].Path
	base := path.Base(file)
	if strings.HasSuffix(item.Name, " "+base) || strings.HasSuffix(item.Name, " "+file) || item.Name == base || item.Name == file {
		return item.Name, ""
	}
	return item.Name + " " + base, file
}

func ToolForDisplay(item domain.Item) domain.Item {
	if item.Presentation != "command" {
		return item
	}
	for i, part := range item.Parts {
		if part.Kind == "text" && part.Label == "工作目录" {
			item.Parts = slices.Delete(slices.Clone(item.Parts), i, i+1)
			break
		}
	}
	return item
}

// ToolResult keeps technical MCP data available behind a single disclosure.
func ToolResult(item domain.Item) (string, []domain.ContentPart, []domain.ContentPart) {
	text := item.Text
	if item.Presentation != "mcp" {
		if _, inline := ToolTitle(item); inline != "" && json.Valid([]byte(text)) {
			text = ""
		}
		return text, item.Parts, nil
	}
	var readable, raw []domain.ContentPart
	if json.Valid([]byte(text)) {
		raw = append(raw, domain.ContentPart{Kind: "code", Label: "参数", Text: text})
		text = ""
	}
	for _, part := range item.Parts {
		switch {
		case part.Kind == "resource", part.Kind == "code" && (part.Label == "结构化结果" || part.Label == "参数"):
			raw = append(raw, part)
		default:
			readable = append(readable, part)
		}
	}
	// Some servers return exactly the same object as text and structured data.
	// Keep that text accessible once, in the structured view.
	readable = slices.DeleteFunc(readable, func(part domain.ContentPart) bool {
		if part.Kind != "text" {
			return false
		}
		var plain bytes.Buffer
		if json.Compact(&plain, []byte(part.Text)) != nil {
			return false
		}
		for _, detail := range raw {
			var structured bytes.Buffer
			if detail.Label == "结构化结果" && json.Compact(&structured, []byte(detail.Text)) == nil &&
				bytes.Equal(plain.Bytes(), structured.Bytes()) {
				return true
			}
		}
		return false
	})
	return text, readable, raw
}

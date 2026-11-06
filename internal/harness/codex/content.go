package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

// Only normalized, bounded presentation data leaves the adapter. Media bytes,
// encrypted payloads and private MCP metadata are never persisted as prose.
func normalizeItem(source wire.Item, final bool) domain.Item {
	item := domain.Item{Kind: "tool", Final: final, Status: source.Status}
	if item.Status == "inProgress" || item.Status == "in_progress" {
		item.Status = "running"
	}
	if item.Status == "" {
		if final {
			item.Status = "completed"
		} else {
			item.Status = "running"
		}
	}
	remaining := 96 << 10
	text := func(s string) string {
		limit := min(16<<10, remaining)
		if len(s) > limit {
			item.Truncated = true
		}
		out := boundedTo(s, limit)
		remaining -= min(len(s), limit)
		return out
	}
	part := func(kind, label, value string) {
		if value != "" && len(item.Parts) < 64 && remaining > 0 {
			item.Parts = append(item.Parts, domain.ContentPart{Kind: kind, Label: text(boundedTo(label, 1024)), Text: text(value)})
		}
	}
	field := func(key string) string { var value string; json.Unmarshal(source.Fields[key], &value); return value }
	pretty := func(raw json.RawMessage) string {
		if len(raw) == 0 || string(raw) == "null" {
			return ""
		}
		var b bytes.Buffer
		if json.Indent(&b, raw, "", "  ") == nil {
			return b.String()
		}
		return ""
	}
	content := func(raw json.RawMessage) {
		var plain string
		if json.Unmarshal(raw, &plain) == nil {
			part("text", "", plain)
			return
		}
		var blocks []json.RawMessage
		if json.Unmarshal(raw, &blocks) != nil {
			return
		}
		for _, block := range blocks {
			if remaining <= 0 {
				item.Truncated = true
				break
			}
			var b struct {
				Type, Text, Name, URI, MimeType, URL, Path, ImageURL, AudioURL string
				ImageSnake                                                     string `json:"image_url"`
				AudioSnake                                                     string `json:"audio_url"`
				Resource                                                       *struct{ URI, MimeType, Text string }
			}
			json.Unmarshal(block, &b)
			switch b.Type {
			case "text", "input_text", "inputText":
				part("text", "", b.Text)
			case "image", "input_image", "inputImage", "audio", "input_audio", "inputAudio":
				kind := "image"
				if strings.Contains(strings.ToLower(b.Type), "audio") {
					kind = "audio"
				}
				uri := b.URL
				for _, candidate := range []string{b.ImageURL, b.ImageSnake, b.AudioURL, b.AudioSnake} {
					if candidate != "" {
						uri = candidate
					}
				}
				if strings.HasPrefix(uri, "data:") {
					uri = ""
				}
				label := b.MimeType
				if label == "" {
					label = kind
				}
				part(kind, label, firstNonempty(uri, "媒体内容（资源预览尚未接入）"))
			case "resource_link":
				part("resource", firstNonempty(b.Name, b.MimeType), b.URI)
			case "resource":
				if b.Resource != nil {
					part("resource", b.Resource.MimeType, b.Resource.URI)
					part("text", "", b.Resource.Text)
				}
			case "encrypted_content":
				part("protected", "受保护的工具结果", "内容由运行时保护，不展示密文。")
			default:
				part("code", "其他工具结果", pretty(block))
			}
		}
	}
	switch source.Type {
	case "agentMessage":
		item.Kind = "assistant"
		item.Text = bounded(source.Text)
	case "commandExecution":
		item.Presentation = "command"
		item.Name = bounded(source.Command)
		item.Text = bounded(source.AggregatedOutput)
		item.ExitCode = source.ExitCode
	case "fileChange":
		item.Presentation = "files"
		var files []struct {
			Path, Diff string
			Kind       struct {
				Type     string
				MovePath string `json:"move_path"`
			}
		}
		json.Unmarshal(source.Fields["changes"], &files)
		for _, f := range files {
			if len(item.Files) >= 32 || remaining <= 0 {
				item.Truncated = true
				break
			}
			item.Files = append(item.Files, domain.FileChange{Path: text(f.Path), Destination: text(f.Kind.MovePath), Kind: f.Kind.Type, Patch: text(f.Diff)})
		}
		item.Name = fmt.Sprintf("编辑 %d 个文件", len(files))
		if len(files) == 1 {
			item.Name = "编辑 " + path.Base(files[0].Path)
		}
	case "mcpToolCall", "dynamicToolCall":
		item.Presentation = "mcp"
		item.Name = field("tool")
		if server := field("server"); server != "" {
			item.Name = server + " · " + item.Name
		}
		part("code", "参数", pretty(source.Fields["arguments"]))
		if source.Type == "mcpToolCall" {
			var result struct {
				Content           json.RawMessage
				StructuredContent json.RawMessage
			}
			json.Unmarshal(source.Fields["result"], &result)
			content(result.Content)
			part("code", "结构化结果", pretty(result.StructuredContent))
			var app struct{ ResourceURI string }
			json.Unmarshal(source.Fields["mcpAppUi"], &app)
			part("resource", "交互资源（未启用嵌入界面）", app.ResourceURI)
		} else {
			content(source.Fields["contentItems"])
		}
		var failure struct{ Message string }
		json.Unmarshal(source.Fields["error"], &failure)
		part("error", "错误", failure.Message)
	case "functionCallOutput":
		item.Presentation = "result"
		item.Name = field("name")
		if ns := field("namespace"); ns != "" {
			item.Name = ns + "." + item.Name
		}
		content(source.Fields["output"])
	case "collabAgentToolCall":
		item.Presentation = "agents"
		tool := field("tool")
		item.Name = map[string]string{"spawnAgent": "启动代理", "sendInput": "发送代理输入", "resumeAgent": "恢复代理", "wait": "等待代理", "closeAgent": "关闭代理", "sendMessage": "向代理发送消息", "followupTask": "追加代理任务", "interruptAgent": "中断代理", "listAgents": "查看代理"}[tool]
		if item.Name == "" {
			item.Name = text(tool)
		}
		part("text", "任务", field("prompt"))
		var agents map[string]struct{ Status, Message string }
		json.Unmarshal(source.Fields["agentsStates"], &agents)
		ids := []string{}
		for id := range agents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if len(item.Agents) >= 32 || remaining <= 0 {
				item.Truncated = true
				break
			}
			a := agents[id]
			item.Agents = append(item.Agents, domain.AgentActivity{ID: text(id), Status: text(a.Status), Message: text(a.Message)})
		}
	case "webSearch":
		item.Presentation = "search"
		var action struct {
			Type, URL, Pattern, Query string
			Queries                   []string
		}
		json.Unmarshal(source.Fields["action"], &action)
		item.Name = map[string]string{"openPage": "打开网页", "findInPage": "查找页面内容", "other": "网页操作"}[action.Type]
		if item.Name == "" {
			item.Name = "搜索网页"
		}
		part("text", "", firstNonempty(field("query"), action.Query))
		part("resource", "网页", action.URL)
		part("text", "查找", action.Pattern)
		for _, q := range action.Queries {
			part("text", "查询", q)
		}
	case "imageView":
		item.Presentation = "image"
		item.Name = "查看 " + path.Base(field("path"))
		part("image", "设备图片", field("path"))
	case "imageGeneration":
		item.Presentation = "image"
		item.Name = "生成图片"
		part("text", "提示词", field("revisedPrompt"))
		part("image", "设备图片", field("savedPath"))
		part("resource", "文件 ID", field("fileId"))
		var failure struct{ Message string }
		json.Unmarshal(source.Fields["failure"], &failure)
		part("error", "错误", failure.Message)
	case "sleep":
		item.Presentation = "wait"
		item.Name = "等待"
		var ms int64
		json.Unmarshal(source.Fields["durationMs"], &ms)
		part("text", "时长", fmt.Sprintf("%.1f 秒", float64(ms)/1000))
	case "reasoning":
		item.Kind = "activity"
		item.Presentation = "reasoning"
		item.Name = "思考摘要"
		var summary []string
		json.Unmarshal(source.Fields["summary"], &summary)
		for _, s := range summary {
			part("text", "", s)
		}
	case "hookPrompt":
		item.Kind = "activity"
		item.Presentation = "context"
		item.Name = "已补充工作区上下文"
		var fragments []struct{ Text string }
		json.Unmarshal(source.Fields["fragments"], &fragments)
		for _, f := range fragments {
			part("text", "", f.Text)
		}
	case "subAgentActivity":
		item.Kind = "activity"
		item.Presentation = "agents"
		item.Name = path.Base(field("agentPath"))
		item.Text = map[string]string{"started": "代理已开始", "interacted": "代理有新活动", "interrupted": "代理已中断", "completed": "代理已完成"}[field("kind")]
	case "enteredReviewMode", "exitedReviewMode":
		item.Kind = "activity"
		item.Presentation = "review"
		item.Name = "开始审阅"
		if source.Type == "exitedReviewMode" {
			item.Name = "审阅完成"
		}
		part("markdown", "", field("review"))
	case "contextCompaction":
		item.Kind = "activity"
		item.Presentation = "context"
		item.Name = "正在压缩上下文"
		if final {
			item.Name = "上下文已压缩"
		}
	default:
		item.Kind = "unsupported"
		item.Name = source.Type
		item.Text = "暂不支持的内容：" + source.Type
	}
	if len(item.Status) > 512 {
		item.Status = boundedTo(item.Status, 512)
		item.Truncated = true
	}
	if item.Presentation != "command" && len(item.Name) > 4096 {
		item.Name = boundedTo(item.Name, 4096)
		item.Truncated = true
	}

	limitItem(&item)
	return item
}

func limitItem(item *domain.Item) {
	for {
		encoded, _ := json.Marshal(item)
		if len(encoded) <= 160<<10 {
			return
		}
		item.Truncated = true
		shrink := func(value *string) {
			if len(*value) > 64 {
				*value = boundedTo(*value, max(32, len(*value)*(128<<10)/len(encoded)))
			}
		}
		shrink(&item.Name)
		shrink(&item.Text)
		for i := range item.Parts {
			shrink(&item.Parts[i].Text)
			shrink(&item.Parts[i].Label)
		}
		for i := range item.Files {
			shrink(&item.Files[i].Path)
			shrink(&item.Files[i].Destination)
			shrink(&item.Files[i].Patch)
		}
		for i := range item.Agents {
			shrink(&item.Agents[i].ID)
			shrink(&item.Agents[i].Status)
			shrink(&item.Agents[i].Message)
		}
	}
}

func boundedJSONText(s string, limit int) string {
	for {
		b, _ := json.Marshal(s)
		if len(b) <= limit {
			return s
		}
		s = boundedTo(s, max(32, len(s)*(limit-64)/len(b)))
	}
}

func firstNonempty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

package codex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
)

// Only normalized, bounded presentation data leaves the adapter. Media bytes,
// encrypted payloads and private MCP metadata are never persisted as prose.
func normalizeItem(source wire.Item, final bool) domain.Item {
	return normalizeItemWithResources(source, final, nil)
}

func (n *Normalizer) normalizeResourceItem(sessionID, itemID string, source wire.Item, final bool) domain.Item {
	if n.importResource == nil {
		return normalizeItem(source, final)
	}
	return normalizeItemWithResources(source, final, func(image harness.OutputResource) (domain.Attachment, error) {
		image.SessionID = sessionID
		image.ItemID = itemID
		return n.importResource(image)
	})
}

func normalizeItemWithResources(source wire.Item, final bool, save func(harness.OutputResource) (domain.Attachment, error)) domain.Item {
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
	switch item.Status {
	case "running", "completed", "failed", "declined", "interrupted", "pending":
	default:
		item.Status = "unknown"
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
	indices := map[string]int{}
	resources := 0
	resourcePart := func(kind, label, path, encoded string, data []byte) {
		if len(item.Parts) >= 64 || remaining <= 0 {
			item.Truncated = true
			return
		}
		if !final {
			return
		}
		if save == nil {
			part(kind, label, firstNonempty(path, "资源预览不可用，请更新执行服务"))
			return
		}
		image := harness.OutputResource{Kind: kind, Name: label, Path: path, Index: indices[kind], Data: data}
		indices[kind]++
		resources++
		var err error
		if encoded != "" {
			if len(encoded) > base64.StdEncoding.EncodedLen(8<<20) {
				err = errors.New("资源超过 8 MiB")
			} else {
				image.Data, err = base64.StdEncoding.DecodeString(encoded)
			}
		}
		if resources > 8 {
			err = errors.New("本次工具的资源超过预览上限")
		}
		var ref domain.Attachment
		if err == nil {
			ref, err = save(image)
		}
		if err != nil {
			part(kind, label, "无法预览："+err.Error())
			return
		}
		item.Parts = append(item.Parts, domain.ContentPart{Kind: kind, Label: text(boundedTo(label, 1024)), Text: ref.Name, Attachment: &ref})
	}
	imagePart := func(label, path, encoded string) { resourcePart("image", label, path, encoded, nil) }
	filePath := func(uri string) string {
		u, err := url.Parse(uri)
		if err == nil && u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") && u.Path != "" && u.RawQuery == "" && u.Fragment == "" {
			return u.Path
		}
		return ""
	}
	resourceName := func(uri string) string {
		u, err := url.Parse(uri)
		if err == nil && path.Base(u.Path) != "." && path.Base(u.Path) != "/" && u.Path != "" {
			return path.Base(u.Path)
		}
		return "工具结果.txt"
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
				Type, Text, Name, URI, MimeType, URL, Path, Data, ImageURL, AudioURL string
				ImageSnake                                                           string `json:"image_url"`
				AudioSnake                                                           string `json:"audio_url"`
				Resource                                                             *struct{ URI, MimeType, Text, Blob string }
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
				if kind == "image" {
					encoded := b.Data
					if strings.HasPrefix(uri, "data:") {
						prefix, payload, ok := strings.Cut(uri, ",")
						if ok && strings.HasSuffix(prefix, ";base64") {
							encoded = payload
						} else {
							encoded = "!invalid"
						}
						uri = ""
					}
					if encoded != "" {
						imagePart("工具图片", "", encoded)
						continue
					}
					if strings.HasPrefix(uri, "file:") {
						parsed, err := url.Parse(uri)
						if err == nil && (parsed.Host == "" || parsed.Host == "localhost") {
							imagePart("设备图片", parsed.Path, "")
							continue
						}
					}
					if b.Path != "" {
						imagePart("设备图片", b.Path, "")
						continue
					}
					indices["image"]++
					part("image", "图片链接", firstNonempty(uri, "图片没有可预览的资源"))
					continue
				}
				encoded, mime := b.Data, b.MimeType
				if strings.HasPrefix(uri, "data:") {
					header, payload, ok := strings.Cut(uri, ",")
					if detected, base64 := strings.CutSuffix(strings.TrimPrefix(header, "data:"), ";base64"); ok && base64 {
						encoded, mime = payload, detected
					}
					uri = ""
				}
				if encoded != "" && (mime == "audio/wav" || mime == "audio/wave" || mime == "audio/x-wav") {
					resourcePart("audio", "tool-output.wav", "", encoded, nil)
					continue
				}
				if encoded != "" && mime == "audio/mpeg" {
					resourcePart("audio", "tool-output.mp3", "", encoded, nil)
					continue
				}
				part("audio", firstNonempty(mime, "音频"), firstNonempty(uri, "没有可播放的 WAV 或 MP3 副本"))
			case "resource_link":
				if local := filePath(b.URI); local != "" && (b.MimeType == "" || textualResource(b.MimeType)) {
					resourcePart("file", firstNonempty(b.Name, resourceName(b.URI)), local, "", nil)
					continue
				}
				part("resource", firstNonempty(b.Name, b.MimeType), b.URI)
			case "resource":
				if b.Resource != nil {
					if strings.HasPrefix(b.Resource.MimeType, "image/") && b.Resource.Blob != "" {
						imagePart("资源图片", "", b.Resource.Blob)
						continue
					}
					if (textualResource(b.Resource.MimeType) || b.Resource.MimeType == "" && b.Resource.Text != "") && (b.Resource.Text != "" || b.Resource.Blob != "") {
						resourcePart("file", resourceName(b.Resource.URI), "", b.Resource.Blob, []byte(b.Resource.Text))
						continue
					}
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
		item.Questions = asyncQuestions(source)
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
			item.Agents = append(item.Agents, domain.AgentActivity{ID: text(id), Status: normalizedAgentStatus(a.Status), Message: text(a.Message)})
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
		imagePart("设备图片", field("path"), "")
	case "imageGeneration":
		item.Presentation = "image"
		item.Name = "生成图片"
		part("text", "提示词", field("revisedPrompt"))
		if saved := field("savedPath"); saved != "" {
			imagePart("生成图片", saved, "")
		} else if result := field("result"); result != "" {
			imagePart("生成图片", "", result)
		}
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
		item.Presentation = "compaction"
		item.Name = "正在压缩上下文"
		if final {
			switch item.Status {
			case "completed":
				item.Name = "上下文已压缩"
			case "failed":
				item.Name = "上下文压缩失败"
			default:
				item.Name = "上下文压缩已停止"
			}
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

func textualResource(mime string) bool {
	mime, _, _ = strings.Cut(mime, ";")
	return strings.HasPrefix(mime, "text/") || mime == "application/json" || mime == "application/xml" || mime == "application/javascript" || mime == "application/x-sh" || strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
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

func normalizedAgentStatus(status string) string {
	switch status {
	case "pendingInit":
		return "starting"
	case "running", "completed", "interrupted":
		return status
	case "errored":
		return "failed"
	case "shutdown":
		return "stopped"
	default:
		return "unknown"
	}
}

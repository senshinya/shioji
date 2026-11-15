package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"math"
	"shioji/internal/domain"
	"strings"
	"time"
)

type activityPatch struct {
	Source, Kind string
	Headers      int
	Raw          bool
	Height       float32
	State        editor.DiffState
}

var fileActivityIcon = svg("M5 2h7l4 4v12H5zM12 2v5h4M8 11h5M8 14h5")
var agentActivityIcon = svg("M3 4h6v5H3zM11 11h6v5h-6zM6 9v5h5")

func activityIcon(kind string) *n.SVG {
	switch kind {
	case "files", "context", "compaction":
		return fileActivityIcon
	case "search", "review":
		return searchIcon
	case "agents":
		return agentActivityIcon
	case "reasoning":
		return questionIcon
	}
	return terminalIcon
}
func activityStatus(status string) string {
	switch status {
	case "running":
		return "进行中"
	case "starting":
		return "正在启动"
	case "stopped":
		return "已停止"
	case "unknown":
		return "状态未知"
	case "failed":
		return "失败"
	case "declined":
		return "已拒绝"
	case "completed":
		return "已完成"
	case "interrupted":
		return "已中断"
	}
	return status
}

func activityHasBody(item domain.Item) bool {
	return item.Text != "" || len(item.Parts) > 0 || len(item.Files) > 0 || len(item.Agents) > 0
}

// Exit codes describe the call, not the outcome of the agent's whole turn.
func toolIssueLabel(item domain.Item) string {
	if !item.Final {
		return ""
	}
	if item.ExitCode != nil && *item.ExitCode != 0 {
		return fmt.Sprintf("退出码 %d", *item.ExitCode)
	}
	switch item.Status {
	case "failed":
		return "调用失败"
	case "declined":
		return "已拒绝"
	case "interrupted":
		return "已中断"
	}
	return ""
}

func (v *View) activityItem(c *n.Context, t *n.Theme, item domain.Item, transcriptAnchor ...bool) {
	if item.Presentation == "compaction" {
		v.contextCompactionItem(c, t, item)
		return
	}
	if item.Kind == "activity" && item.Presentation == "agents" {
		label := strings.TrimSpace(item.Name + " " + strings.TrimPrefix(item.Text, "代理"))
		font := n.Font{Family: t.Font, Size: 12, Weight: 400}
		n.Row(c.Key("agent-event")).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
			inlineSymbol(c, agentActivityIcon, label, font, 14, t.TextMuted)
			n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable().
				ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
		})
		return
	}
	key := "activity:" + v.Model.SessionID + "/" + item.ID
	if _, seen := v.Model.Collapsed[key]; !seen {
		v.Model.Collapsed[key] = item.Kind == "activity"
	}
	hasBody := item.Presentation != "wait" && activityHasBody(item)
	duration := ""
	if item.Presentation == "wait" {
		for _, part := range item.Parts {
			if part.Kind == "text" && part.Label == "时长" {
				duration = part.Text
				break
			}
		}
	}
	label := item.Name
	if label == "" {
		label = "工具活动"
	}
	button := n.ButtonBase(c.Key("activity-heading")).FillWidth().MinHeight(20).Justify(n.Start).Label(label)
	color := textHoverColor(c, button, t.TextMuted)
	button.Children(func() {
		n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
			font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
			metadataMotion := resultBlockMotion(c)
			if item.Kind == "activity" {
				inlineSymbol(c, activityIcon(item.Presentation), label, font, 14, color)
			}
			title := n.Text(c, label).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FontSize(12).FixedLineHeight(20).TextColor(color).MinWidth(0).MaxLines(2).Ellipsis("…")
			if duration != "" {
				title.SingleLine()
				n.Text(c.Key("wait-duration"), duration).SingleLine().Shrink(0).FontSize(12).FixedLineHeight(20).TextColor(color)
			}
			title.TooltipWhenTruncated(label)
			if item.Status != "" {
				status := activityStatus(item.Status)
				if issue := toolIssueLabel(item); issue != "" {
					status = issue
				}
				animation := motionAnimation(c, 160*time.Millisecond)
				n.Text(c.Key("activity-status"), status).SingleLine().Shrink(0).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).
					ContentTransition(n.ContentTransition{Animation: &animation}).Transition(metadataMotion)
			}
			if hasBody {
				angle := float32(0)
				if v.Model.Collapsed[key] {
					angle = -90
				}
				inlineSymbol(c.Key("activity-chevron"), chevron, label, font, 14, color, angle).Shrink(0).Transition(metadataMotion)
			}
		})
	})
	if button.Clicked() && hasBody {
		if list := v.lists[v.Model.SessionID]; list != nil && (len(transcriptAnchor) == 0 || transcriptAnchor[0]) {
			list.KeepRow(v.currentTranscriptRow, button.Bounds().Y-list.Bounds(c).Y)
		}
		v.Model.Collapsed[key] = !v.Model.Collapsed[key]
	}
	if item.Presentation == "wait" {
		return
	}
	// Keep the measured stack in layout while it closes. Element exit
	// snapshots alone do not reserve height for the transcript that follows.
	frame := n.Column(c.Key("activity-body")).FillWidth().Clip().Margin(-8, 0, 0, 0)
	retained := n.Local(frame, "item", func() domain.Item { return item })
	open := hasBody && !v.Model.Collapsed[key]
	if open {
		*retained = item
	}
	animatedContentHeight(c, frame, open, 180*time.Millisecond, func() float32 {
		innerHeightDelta := float32(0)
		item := *retained
		n.Column(c.Key("result-stack")).FillWidth().Padding(8, 0, 0, 0).Gap(8).Children(func() {
			if item.Text != "" {
				resultText(c.Key("summary"), item.Text, t.TextMuted, func(text n.Element) { v.decoratePlainFind(c, text, item, nil) }).
					Transition(resultBlockMotion(c)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
			}
			for _, file := range item.Files {
				n.Column(c.Key("file:" + file.Path)).FillWidth().Gap(6).Transition(resultBlockMotion(c)).Children(func() {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						n.Text(c, file.Path).FontSize(11).FixedLineHeight(20).Grow(1).Selectable()
						kind := map[string]string{"add": "新增", "delete": "删除", "update": "修改"}[file.Kind]
						if file.Destination != "" {
							kind = "移动"
						}
						n.Text(c, kind).FontSize(11).TextColor(t.TextMuted)
					})
					if file.Destination != "" {
						n.Text(c.Key("destination"), "→ "+file.Destination).Transition(resultBlockMotion(c)).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
					}
					if file.Patch != "" {
						v.activityDiff(c, t, key+"/"+file.Path, file)
					}
				})
			}
			occurrences := map[string]int{}
			for i, part := range item.Parts {
				identity := resultPartKey(part, i)
				occurrence := occurrences[identity]
				occurrences[identity]++
				block := n.Column(c.Key(fmt.Sprintf("%s:%d", identity, occurrence))).FillWidth().Gap(4).Transition(resultBlockMotion(c))
				block.Children(func() {
					if part.Kind == "image" && part.Attachment != nil {
						innerHeightDelta += v.outputImage(c, t, *part.Attachment)
						return
					}
					if part.Kind == "file" && part.Attachment != nil {
						v.outputFile(c, t, *part.Attachment)
						return
					}
					if part.Label != "" {
						n.Text(c.Key("label"), part.Label).Transition(resultBlockMotion(c)).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
					}
					switch part.Kind {
					case "code":
						n.Scroll(c).FillWidth().MaxHeight(180).Children(func() {
							resultText(c.Key("code-text"), part.Text, t.Text).FillWidth().Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(18).Padding(8, 10).Radius(6).Background(codeSurface(t)).Selectable()
						})
					case "markdown":
						v.activityMarkdown.Theme = v.md.Theme
						v.bindMarkdownLinks(c, &v.activityMarkdown)
						v.activityMarkdown.ShareSelection = true
						v.activityMarkdown.WrapCode = true
						v.activityMarkdown.Reveal = &markdown.Reveal{Unit: markdown.ByCharacter, Fade: 180 * time.Millisecond, Smooth: true}
						root := n.Column(c.Key("markdown")).FillWidth()
						mounted := n.Local(root, "mounted", func() bool { return false })
						root.Children(func() {
							if *mounted && !item.Final && !c.Preferences().ReduceMotion {
								markdown.Stream(c, part.Text, &v.activityMarkdown).FillWidth().FontSize(12)
							} else {
								markdown.View(c, part.Text, &v.activityMarkdown).FillWidth().FontSize(12)
							}
						})
						*mounted = true
					default:
						resultText(c.Key("part-text"), part.Text, t.TextMuted).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
					}
				})
			}
			for _, agent := range item.Agents {
				n.Column(c.Key("agent:" + agent.ID)).FillWidth().Gap(4).Transition(resultBlockMotion(c)).Children(func() {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
						statusDot(c, agent.ID, font, t.TextMuted, false)
						n.Text(c, agent.ID).FontSize(12).Grow(1)
						n.Text(c.Key("status"), activityStatus(agent.Status)).FontSize(11).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
					})
					if agent.Message != "" {
						n.Text(c.Key("message"), agent.Message).Transition(resultBlockMotion(c)).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingX(18)
					}
				})
			}
			if item.Truncated {
				n.Text(c.Key("truncated"), "内容过长，已截断显示").Transition(resultBlockMotion(c)).FontSize(11).TextColor(t.TextMuted)
			}
		})
		return innerHeightDelta
	})
}

// Fade received suffixes in place. Earlier bytes keep their original ink;
// do not crossfade a whole tool result every time another delta arrives.
type resultTextState struct {
	text    string
	appends []commandOutputAppend
}

func resultText(c *n.Context, text string, color n.Color, decorate ...func(n.Element)) n.Element {
	root := n.Column(c).FillWidth()
	state := n.Local(root, "appends", func() resultTextState { return resultTextState{text: text} })
	if text != state.text {
		if strings.HasPrefix(text, state.text) && !c.Preferences().ReduceMotion {
			state.appends = append(state.appends, commandOutputAppend{len(state.text), len(text), c.Now()})
		} else {
			state.appends = nil
		}
		state.text = text
	}
	spans := []n.Span{}
	from := 0
	active := state.appends[:0]
	for _, addition := range state.appends {
		progress := min(1, float32(c.Now().Sub(addition.at))/float32(180*time.Millisecond))
		if progress >= 1 || c.Preferences().ReduceMotion {
			continue
		}
		active = append(active, addition)
		if from < addition.start {
			spans = append(spans, n.Span{Text: text[from:addition.start]})
		}
		spans = append(spans, n.Span{Text: text[addition.start:addition.end], Color: color.Alpha(n.EaseOut(max(0, progress)))})
		from = addition.end
		c.AnimationFrame()
	}
	state.appends = active
	if from < len(text) {
		spans = append(spans, n.Span{Text: text[from:]})
	}
	root.Children(func() {
		e := n.RichText(c, spans...).FillWidth().TextColor(color)
		for _, apply := range decorate {
			apply(e)
		}
		if len(active) == 0 {
			e.ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
		}
	})
	return root
}

// Resource identity survives an authoritative result replacing/reordering parts.
// Plain segments have no source ID; their position within the item is stable.
func resultPartKey(part domain.ContentPart, index int) string {
	if part.Attachment != nil {
		return "part:" + part.Kind + ":" + part.Attachment.ID
	}
	return fmt.Sprintf("part:%s:%d", part.Kind, index)
}

func resultBlockMotion(c *n.Context) n.ElementTransition {
	motion := fadeMotion(c, 180*time.Millisecond)
	motion.Position = !c.Preferences().ReduceMotion
	return motion
}

func (v *View) activityDiff(c *n.Context, t *n.Theme, key string, file domain.FileChange) {
	if v.activityPatches == nil {
		v.activityPatches = map[string]*activityPatch{}
	}
	state := v.activityPatches[key]
	if state == nil || state.Source != file.Patch || state.Kind != file.Kind {
		state = &activityPatch{Source: file.Patch, Kind: file.Kind, State: editor.DiffState{Layout: editor.DiffUnified, Wrap: true}}
		// Added/deleted files carry their full contents; only updates are patches.
		switch file.Kind {
		case "add":
			state.State.Old, state.State.New = editor.NewBuffer(""), editor.NewBuffer(file.Patch)
		case "delete":
			state.State.Old, state.State.New = editor.NewBuffer(file.Patch), editor.NewBuffer("")
		default:
			if files, err := editor.ParsePatch(file.Patch); err == nil && len(files) > 0 && len(files[0].Hunks) > 0 {
				state.State.SetPatch(files[0])
				state.Headers = len(files[0].Hunks)
			} else {
				state.Raw = true
			}
		}
		v.activityPatches[key] = state
	}
	if state.Raw {
		n.Scroll(c.Key(key)).FillWidth().MaxHeight(220).Label("文件补丁：" + file.Path).Children(func() {
			n.Text(c, file.Patch).FillWidth().Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(20).Padding(8, 10).Background(codeSurface(t)).Selectable()
		})
		return
	}
	added, deleted, addFill, delFill := gitColors(t)
	state.State.Font = n.Font{Family: "Menlo", Size: 11, Weight: 400, Antialiased: true}
	state.State.LineHeight = 20.0 / 11
	state.State.Indicators = editor.DiffClassic
	state.State.Theme = &editor.Theme{Background: codeSurface(t), Text: t.Text, LineNumber: subtle(t), Added: added, Deleted: deleted, AddedBackground: addFill, DeletedBackground: delFill}
	if state.Height == 0 {
		_, removedLines := state.State.Stats()
		state.Height = min(float32(220), float32(state.State.New.LineCount()+removedLines)*20+float32(state.Headers)*32)
	}
	// Let the native viewer account for wraps and headers after layout. Keep
	// its bounded viewport and internal scrolling for large changes.
	invalidate := c.Services().Invalidate
	editor.DiffView(c.Key(key), &state.State).Transition(resultBlockMotion(c)).FillWidth().Height(state.Height).Label("文件补丁：" + file.Path).DrawOver(func(_ *n.Painter, _ n.Rect) {
		if measured := state.State.ContentHeight(); measured > 0 {
			height := min(float32(220), max(float32(20), measured))
			if math.Abs(float64(height-state.Height)) > .5 {
				state.Height = height
				invalidate()
			}
		}
	})
}

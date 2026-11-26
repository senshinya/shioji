package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"math"
	"shioji/internal/conversationui"
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

var fileActivityIcon = newTranscriptIcon("M5 2h7l4 4v12H5zM12 2v5h4M8 11h5M8 14h5")

const agentActivityIconPath = "M3 4h6v5H3zM11 11h6v5h-6zM6 9v5h5"

var agentActivityIcon = svg(agentActivityIconPath)

func activityIcon(kind string) *n.SVG {
	switch kind {
	case "files", "context", "compaction":
		return fileActivityIcon
	case "search", "review":
		return transcriptSearchIcon
	case "agents":
		return transcriptAgentIcon
	case "reasoning":
		return transcriptQuestionIcon
	}
	return transcriptTerminalIcon
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
	if item.Presentation == domain.GoalContinuationPresentation {
		font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
		n.Row(c).Gap(5).AlignItems(n.Baseline).Children(func() {
			transcriptSymbol(c, transcriptGoalIcon, item.Name, font, t.TextMuted)
			n.Text(c, item.Name).FontSize(font.Size).FixedLineHeight(20).SingleLine().TextColor(t.TextMuted)
		})
		return
	}
	if item.Presentation == "compaction" {
		v.contextCompactionItem(c, t, item)
		return
	}
	if item.Kind == "activity" && item.Presentation == "agents" {
		label := strings.TrimSpace(item.Name + " " + strings.TrimPrefix(item.Text, "代理"))
		font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
		n.Row(c.Key("agent-event")).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
			transcriptSymbol(c, transcriptAgentIcon, label, font, t.TextMuted)
			n.Text(c, label).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(t.TextMuted).Selectable().
				ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
		})
		return
	}
	item = conversationui.ReasoningForDisplay(item)
	key := "activity:" + v.Model.SessionID + "/" + item.ID
	if _, seen := v.Model.Collapsed[key]; !seen && item.Presentation != "wait" {
		v.Model.Collapsed[key] = true
	}
	hasBody := item.Presentation != "wait" && activityHasBody(item)
	if domain.ReasoningSummary(item) {
		v.reasoningActivityItem(c, t, item, key, hasBody, transcriptAnchor...)
		return
	}
	button := v.activityHeading(c, t, item, key, hasBody)
	if button.Clicked() && hasBody {
		if list := v.lists[v.Model.SessionID]; list != nil && (len(transcriptAnchor) == 0 || transcriptAnchor[0]) {
			list.KeepRowWithinContent(v.currentTranscriptRow, button.Bounds().Y-list.Bounds(c).Y)
		}
		v.Model.Collapsed[key] = !v.Model.Collapsed[key]
	}
	if item.Presentation == "wait" {
		return
	}
	v.activityBodyDisclosure(c, t, item, key, hasBody && !v.Model.Collapsed[key], false, false, transcriptAnchor...)
}

func (v *View) activityHeading(c *n.Context, t *n.Theme, item domain.Item, key string, hasBody bool) n.Element {
	duration := ""
	if item.Presentation == "wait" {
		for _, part := range item.Parts {
			if part.Kind == "text" && part.Label == "时长" {
				duration = part.Text
				break
			}
		}
	}
	label, filename := conversationui.ToolTitle(item)
	if label == "" {
		label = "工具活动"
	}
	command := item.Presentation == "command"
	titleText := label
	if command {
		titleText = commandPreview(label)
	}
	heading := c.Key("activity-heading")
	var button n.Element
	if hasBody {
		button = n.ButtonBase(heading).Expanded(!v.Model.Collapsed[key])
	} else {
		button = n.Row(heading)
	}
	color := textHoverColor(c, button, t.TextMuted)
	button.AlignSelf(n.Start).MinHeight(20).Justify(n.Start)
	if hasBody || command {
		button.Label(label)
	}
	if command {
		button.Tooltip(label).ContextMenu(commandCopyMenu(c, item.Name))
	}
	if filename != "" {
		button.Tooltip(filename)
	}
	button.Children(func() {
		n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
			font := n.Font{Family: c.Theme().Font, Size: transcriptCaptionSize, Weight: 400}
			metadataMotion := resultBlockMotion(c)
			if item.Kind == "activity" {
				transcriptSymbol(c, activityIcon(item.Presentation), label, font, color)
			}
			title := n.Text(c, titleText).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(color).MinWidth(0).MaxLines(2).Ellipsis("…")
			if duration != "" {
				title.SingleLine()
				n.Text(c.Key("wait-duration"), duration).SingleLine().Shrink(0).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(color)
			}
			title.TooltipWhenTruncated(label)
			if issue := toolIssueLabel(item); issue != "" || !command && item.Status != "" {
				status := activityStatus(item.Status)
				if issue != "" {
					status = issue
				}
				animation := motionAnimation(c, 160*time.Millisecond)
				n.Text(c.Key("activity-status"), status).SingleLine().Shrink(0).FontSize(font.Size).FixedLineHeight(20).TextColor(t.TextMuted).
					ContentTransition(n.ContentTransition{Animation: &animation}).Transition(metadataMotion)
			}
			if hasBody {
				angle := float32(0)
				if v.Model.Collapsed[key] {
					angle = -90
				}
				inlineSymbol(c.Key("activity-chevron"), transcriptChevron, label, font, 14, color, angle).Shrink(0).Transition(metadataMotion)
			}
		})
	})
	return button
}

type reasoningLiveState struct {
	phase   time.Time
	caption string
	started *time.Time
}

const reasoningMorphDuration = 260 * time.Millisecond

func (v *View) reasoningActivityItem(c *n.Context, t *n.Theme, item domain.Item, key string, hasBody bool, transcriptAnchor ...bool) {
	live := v.runningReasoning == item.ID && v.activityTurn == item.TurnID && item.WorkID == ""
	streaming := live && hasBody
	liveStatus := live && !hasBody
	entry := v.reasoningStreamEntry(item.ID)
	if streaming {
		entry.wasLive = true
	} else if entry.wasLive {
		entry.wasLive = false
		entry.collapsedByUser = false
		v.Model.Collapsed[key] = true
	}
	open := hasBody && !v.Model.Collapsed[key]
	if streaming {
		open = !entry.collapsedByUser
	}
	summaryLabel := item.Name
	if summaryLabel == "" {
		summaryLabel = "思考摘要"
	}
	button := n.ButtonBase(c.Key("activity-heading")).AlignSelf(n.Start).MinHeight(20).Justify(n.Start)
	liveState := n.Local(button, "reasoning-live-state", func() reasoningLiveState { return reasoningLiveState{} })
	if liveStatus {
		caption, phase := "正在思考", c.Now()
		if p := v.runningProgress; p != nil && p.turn == item.TurnID {
			caption = p.activityCaption(c, "正在思考")
			phase = p.started
		}
		liveState.caption = caption
		liveState.phase = phase
		liveState.started = v.toolElapsed(item.TurnID)
	}
	target := float32(0)
	if liveStatus {
		target = 1
	}
	morphAnim := motionAnimation(c, reasoningMorphDuration)
	if !c.Preferences().ReduceMotion {
		morphAnim.Ease = n.EaseInOut
	}
	liveProgress := target
	if c.Preferences().ReduceMotion || streaming {
		button.Jump("reasoning-live", target)
	} else {
		liveProgress = button.AnimateTo("reasoning-live", target, morphAnim)
	}
	displayLabel := summaryLabel
	if liveStatus {
		displayLabel = liveState.caption
		if displayLabel == "" {
			displayLabel = "正在思考"
		}
	}
	button.Label(displayLabel)
	color := t.TextMuted
	if !liveStatus && hasBody {
		color = textHoverColor(c, button, t.TextMuted)
	}
	button.Children(func() {
		line := n.Row(c).Height(20).AlignItems(n.Baseline)
		if liveProgress > 0 {
			shimmerToolStatus(c, line, liveState.phase, t.Text, liveProgress)
		}
		line.Children(func() {
			font := n.Font{Family: c.Theme().Font, Size: transcriptCaptionSize, Weight: 400}
			transcriptMarkerSlot(c.Key("icon")).MarginEnd(6).Children(func() {
				if liveProgress < 1 {
					icon := transcriptSymbol(c.Key("summary-icon"), activityIcon(item.Presentation), summaryLabel, font, color).Opacity(1 - liveProgress)
					if liveProgress > 0 {
						icon.Absolute().Left(0).Top(0)
					}
				}
				if liveProgress > 0 {
					statusDot(c.Key("dot"), displayLabel, font, t.Success, liveStatus, liveState.phase).Opacity(liveProgress)
				}
			})
			titleAnim := morphAnim
			title := n.Text(c.Key("reasoning-label"), displayLabel).
				ContentTransition(n.ContentTransition{Animation: &titleAnim}).
				FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(color).MinWidth(0).MaxLines(2).Ellipsis("…")
			if !liveStatus {
				title.TooltipWhenTruncated(summaryLabel)
			}
			if liveStatus {
				if liveState.started != nil && !liveState.started.IsZero() {
					clockSlot := n.Row(c.Key("elapsed-slot")).Height(20).AlignItems(n.Baseline)
					clockSlot.Children(func() {
						runningElapsed(c.Key("elapsed"), liveState.started, t.TextMuted, true)
					})
				}
			} else {
				status := ""
				if !streaming && item.Status != "" && item.Status != "completed" && !(item.Status == "running" && !item.Final) {
					status = activityStatus(item.Status)
					if issue := toolIssueLabel(item); issue != "" {
						status = issue
					}
				}
				clockOpacity := float32(0)
				if liveProgress > 0 && liveState.started != nil && !liveState.started.IsZero() {
					clockOpacity = max(0, (liveProgress-.45)/.55)
				}
				metaOpacity := float32(1)
				if liveProgress > 0 {
					metaOpacity = max(0, min(1, (.75-liveProgress)/.75))
				}
				if clockOpacity > 0 || status != "" || hasBody {
					trailing := n.Row(c.Key("trailing-slot")).Height(20).Shrink(0).AlignItems(n.Baseline)
					if clockOpacity > 0 {
						clockFont := n.Font{Family: c.Theme().Font, Size: transcriptCaptionSize, Weight: 400, Features: "tnum"}
						clockW, _ := n.ShapeText(elapsedActivityLabel(*liveState.started, c.Now()), clockFont, 0).Size()
						trailing.MinWidth(clockW + 6)
					}
					trailing.Children(func() {
						if status != "" || hasBody {
							meta := n.Row(c.Key("settled-meta")).MarginStart(6).Gap(6).Shrink(0).AlignItems(n.Baseline).Opacity(metaOpacity)
							meta.Children(func() {
								if status != "" {
									animation := motionAnimation(c, 160*time.Millisecond)
									n.Text(c.Key("activity-status"), status).SingleLine().Shrink(0).FontSize(font.Size).FixedLineHeight(20).TextColor(t.TextMuted).
										ContentTransition(n.ContentTransition{Animation: &animation})
								}
								if hasBody {
									angle := float32(0)
									if !open {
										angle = -90
									}
									inlineSymbol(c.Key("activity-chevron"), transcriptChevron, summaryLabel, font, 14, color, angle).Shrink(0)
								}
							})
						}
						if clockOpacity > 0 {
							clockSlot := n.Row(c.Key("elapsed-slot")).Absolute().Left(0).Top(0).Height(20).AlignItems(n.Baseline).Opacity(clockOpacity)
							clockSlot.Children(func() {
								runningElapsed(c.Key("elapsed"), liveState.started, t.TextMuted, true)
							})
						}
					})
				}
			}
		})
	})
	if button.Clicked() && !liveStatus && hasBody {
		if list := v.lists[v.Model.SessionID]; list != nil && (len(transcriptAnchor) == 0 || transcriptAnchor[0]) {
			list.KeepRowWithinContent(v.currentTranscriptRow, button.Bounds().Y-list.Bounds(c).Y)
		}
		if streaming {
			entry.collapsedByUser = !entry.collapsedByUser
			open = !entry.collapsedByUser
		} else {
			v.Model.Collapsed[key] = !v.Model.Collapsed[key]
			open = !v.Model.Collapsed[key]
		}
	}
	v.activityBodyDisclosure(c, t, item, key, open, streaming, entry.fresh, transcriptAnchor...)
}

func (v *View) activityBodyDisclosure(c *n.Context, t *n.Theme, item domain.Item, key string, open, liveReasoning, freshReasoning bool, transcriptAnchor ...bool) {
	// Keep the measured stack in layout while it closes. Element exit
	// snapshots alone do not reserve height for the transcript that follows.
	frame := n.Column(c.Key("activity-body")).FillWidth().Clip().Margin(-8, 0, 0, 0)
	retained := n.Local(frame, "item", func() domain.Item { return item })
	if open {
		*retained = item
	}
	build := func() float32 {
		return v.activityResultContents(c, t, *retained, key, liveReasoning, freshReasoning)
	}
	if len(transcriptAnchor) > 0 && !transcriptAnchor[0] {
		// Child work details have their own scroll container. Their height
		// changes must not reset the main conversation's follow animation.
		animatedContentHeight(c, frame, open, 180*time.Millisecond, build)
	} else {
		baseline := open && !(liveReasoning && freshReasoning)
		v.transcriptContentHeight(c, frame, open, baseline, 180*time.Millisecond, build)
	}
}

func (v *View) activityResultContents(c *n.Context, t *n.Theme, item domain.Item, key string, liveReasoning, freshReasoning bool) float32 {
	reasoning := domain.ReasoningSummary(item)
	_, inlineFile := conversationui.ToolTitle(item)
	text, parts, raw := conversationui.ToolResult(item)
	innerHeightDelta := float32(0)
	stack := n.Column(c.Key("result-stack")).FillWidth().Gap(8)
	if reasoning {
		stack.Padding(8, 0, 0, transcriptGlyphSlot+6).Draw(func(p *n.Painter, r n.Rect) {
			if h := r.H - 10; h > 0 {
				p.Fill(n.Rect{X: r.X + transcriptGlyphSlot/2 - .5, Y: r.Y + 8, W: 1, H: h}, t.Border, 0)
			}
		})
	} else {
		stack.Padding(8, 0, 0, 0)
	}
	stack.Children(func() {
		if text != "" {
			if reasoning {
				v.reasoningStreamText(c.Key("summary"), item.ID, text, liveReasoning, freshReasoning, t.TextMuted, func(text n.Element) {
					text.FontSize(12).FixedLineHeight(20).Wrap().Selectable()
					v.decoratePlainFind(c, text, item, nil)
				})
			} else {
				resultText(c.Key("summary"), text, t.TextMuted, func(text n.Element) { v.decoratePlainFind(c, text, item, nil) }).
					Transition(resultBlockMotion(c)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
			}
		}
		for _, file := range item.Files {
			n.Column(c.Key("file:" + file.Path)).FillWidth().Gap(6).Transition(resultBlockMotion(c)).Children(func() {
				if inlineFile == "" {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						n.Text(c, file.Path).FontSize(11).FixedLineHeight(20).Grow(1).Selectable()
						kind := map[string]string{"add": "新增", "delete": "删除", "update": "修改"}[file.Kind]
						if file.Destination != "" {
							kind = "移动"
						}
						n.Text(c, kind).FontSize(11).TextColor(t.TextMuted)
					})
				}
				if file.Destination != "" {
					n.Text(c.Key("destination"), "→ "+file.Destination).Transition(resultBlockMotion(c)).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				}
				if file.Patch != "" {
					v.activityDiff(c, t, key+"/"+file.Path, file)
				}
			})
		}
		occurrences := map[string]int{}
		for i, part := range parts {
			identity := resultPartKey(part, i)
			occurrence := occurrences[identity]
			occurrences[identity]++
			block := n.Column(c.Key(fmt.Sprintf("%s:%d", identity, occurrence))).FillWidth().Gap(4).Transition(resultBlockMotion(c))
			block.Children(func() {
				if part.Kind == "image" && part.Attachment != nil {
					innerHeightDelta += v.outputImage(c, t, *part.Attachment)
					return
				}
				if (part.Kind == "file" || part.Kind == "audio") && part.Attachment != nil {
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
					if c.Preferences().ReduceMotion {
						v.activityMarkdown.Reveal = &markdown.Reveal{Unit: markdown.AtOnce}
					} else {
						v.activityMarkdown.Reveal = &markdown.Reveal{Unit: markdown.ByCharacter, Fade: 180 * time.Millisecond, Smooth: true}
					}
					root := n.Column(c.Key("markdown")).FillWidth()
					mounted := n.Local(root, "mounted", func() bool { return false })
					root.Children(func() {
						innerHeightDelta += v.markdownHeightDelta(func() {
							if *mounted && !item.Final && !c.Preferences().ReduceMotion {
								markdown.Stream(c, part.Text, &v.activityMarkdown).FillWidth().FontSize(12)
							} else {
								markdown.View(c, part.Text, &v.activityMarkdown).FillWidth().FontSize(12)
							}
						})
					})
					*mounted = true
				default:
					if reasoning {
						v.reasoningStreamText(c.Key("part-text"), fmt.Sprintf("%s:part:%d", item.ID, i), part.Text, liveReasoning, freshReasoning, t.TextMuted, func(text n.Element) {
							text.FontSize(12).FixedLineHeight(20).Wrap().Selectable()
						})
					} else {
						resultText(c.Key("part-text"), part.Text, t.TextMuted).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
					}
				}
			})
		}
		if len(raw) != 0 {
			root := c.Key("raw-tool-result")
			details := domain.Item{ID: item.ID + ":raw", Name: "查看原始结果", Parts: raw, Final: item.Final}
			rawKey := key + "/raw"
			if _, seen := v.Model.Collapsed[rawKey]; !seen {
				v.Model.Collapsed[rawKey] = true
			}
			n.Column(root).FillWidth().Gap(8).Children(func() {
				if v.activityHeading(root, t, details, rawKey, true).Clicked() {
					v.Model.Collapsed[rawKey] = !v.Model.Collapsed[rawKey]
				}
				open := !v.Model.Collapsed[rawKey]
				frame := n.Column(root.Key("body")).FillWidth().Clip()
				innerHeightDelta += conversationui.AnimatedContentHeight(root, frame, open, open, 180*time.Millisecond, func() float32 {
					return v.activityResultContents(root, t, details, rawKey, false, false)
				})
			})
		}
		for _, agent := range item.Agents {
			n.Column(c.Key("agent:" + agent.ID)).FillWidth().Gap(4).Transition(resultBlockMotion(c)).Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
					font := n.Font{Family: c.Theme().Font, Size: transcriptCaptionSize, Weight: 400}
					transcriptStatusDot(c, agent.ID, font, t.TextMuted, false)
					n.Text(c, agent.ID).FontSize(transcriptCaptionSize).FixedLineHeight(20).Grow(1)
					n.Text(c.Key("status"), activityStatus(agent.Status)).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
				})
				if agent.Message != "" {
					n.Text(c.Key("message"), agent.Message).Transition(resultBlockMotion(c)).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingX(transcriptGlyphSlot + 8)
				}
			})
		}
		if item.Truncated {
			n.Text(c.Key("truncated"), "内容过长，已截断显示").Transition(resultBlockMotion(c)).FontSize(11).TextColor(t.TextMuted)
		}
	})
	return innerHeightDelta
}

func (v *View) reasoningStreamText(c *n.Context, streamKey, text string, live, fresh bool, color n.Color, decorate ...func(n.Element)) n.Element {
	entry := v.reasoningStreamEntry(streamKey)
	frame := entry.stream.Advance(c.Now(), text, live, fresh, c.Preferences().ReduceMotion)
	if frame.Animating {
		c.AnimationFrame()
	}
	e := n.RichText(c, conversationui.ReasoningSpans(frame, color)...).FillWidth().TextColor(color)
	for _, apply := range decorate {
		apply(e)
	}
	return e
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

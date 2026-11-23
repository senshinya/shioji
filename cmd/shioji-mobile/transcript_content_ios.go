package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/conversationui"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) transcriptMarkdown(c *ui.Context, parent *mygo.Window, source domain.Session, text string, size float32, mark *markdown.RangeDecoration) {
	if text == "" {
		return
	}
	a.drawTranscriptMarkdown(c, parent, source, size, mark, func(body *ui.Context, options *markdown.Options) {
		markdown.View(body, text, options).Selectable()
	})
}

func (a *mobileApp) drawTranscriptMarkdown(c *ui.Context, parent *mygo.Window, source domain.Session, size float32, mark *markdown.RangeDecoration, draw func(*ui.Context, *markdown.Options), heightOwner ...*float32) {
	body := c.Key("transcript-markdown")
	frame := ui.Column(body).FillWidth()
	// Links are activated after Markdown has examined the current anchor.
	// Keep that navigation request with this document until the next draw.
	options := ui.Local(frame, "markdown-options", func() markdown.Options { return markdown.Options{} })
	options.WrapCode, options.DecorateRange = true, mark
	options.Reveal, options.OnRevealState = nil, nil
	options.AnchorScroll = ui.ScrollIntoViewOptions{Block: ui.ScrollStart}
	options.Theme = &markdown.Theme{
		FontSize:     size * c.Preferences().TextScale,
		CodeFont:     a.codeFont(),
		BlockSpacing: transcriptItemGap / float32(bodyTextSize),
		ItemSpacing:  .15,
	}
	var delta *float32
	if len(heightOwner) > 0 {
		delta = heightOwner[0]
	}
	a.bindMobileFileLinks(parent, source, "", options)
	a.bindMobileMarkdownImages(parent, source, "", options, delta)
	a.bindMobileDiagrams(body, c.Root(), "", options, delta)
	frame.Children(func() { draw(body, options) })
}

func (a *mobileApp) contentParts(c *ui.Context, parent *mygo.Window, session domain.Session, parts []domain.ContentPart) {
	p := palette(c)
	type partKey struct {
		kind, label, attachment string
		occurrence              int
	}
	occurrences := make(map[partKey]int, len(parts))
	for _, part := range parts {
		key := partKey{kind: part.Kind, label: part.Label}
		if part.Attachment != nil && part.Attachment.ID != "" {
			key.attachment, key.label = part.Attachment.ID, ""
		}
		occurrence := occurrences[key]
		occurrences[key]++
		key.occurrence = occurrence
		row := c.Key(key)
		result := ui.Column(row).Gap(3)
		animateToolResultBlock(c, result)
		result.Children(func() {
			if part.Label != "" {
				label(row, part.Label, captionTextSize, p.secondary).Selectable()
			}
			if part.Attachment != nil {
				item := *part.Attachment
				if imageAttachment(item) {
					a.transcriptImageAttachment(row, parent, session, item, false)
					return
				}
				name := item.Name
				if name == "" {
					name = "附件"
				}
				var open func()
				subtitle := part.Text
				if parent != nil && session.ID != "" {
					if part.Kind == "file" {
						open = func() {
							a.openLinkedFile(parent, session, "", &mobileLinkedPreview{target: name, snapshot: &item})
						}
					} else {
						open = func() { a.attachmentPage(parent, session, item, false) }
					}
				}
				if part.Kind == "file" {
					subtitle = snapshotSize(item.Size)
				}
				mobileDetailRow(row, name, subtitle, "预览 "+name, fileCodeSymbol, open, nil)
				return
			}
			switch part.Kind {
			case "markdown":
				if part.Text != "" {
					stream := ui.Local(result, "markdown-result", func() replyStream {
						return replyStream{previous: part.Text}
					})
					item := domain.Item{Text: part.Text, Final: true}
					stream.prepare(c.Now(), item, !c.Preferences().ReduceMotion, true, false)
					a.drawTranscriptMarkdown(row, parent, session, controlTextSize, nil, func(body *ui.Context, options *markdown.Options) {
						stream.draw(body, item, options)
					})
				}
			case "code":
				if part.Text != "" {
					state := ui.Local(result, "code-result", func() mobileOutputText { return mobileOutputText{text: part.Text} })
					state.update(c, part.Text, true)
					a.codeViewportView(row, "", part.Text, structuredCodeHeight, state.spans(c, part.Text, p.ink), nil, nil)
				}
			default:
				if part.Text != "" {
					toolResultText(row, part.Text, nil)
				}
			}
		})
	}
}

func animateToolResultBlock(c *ui.Context, block ui.Element) {
	if !c.Preferences().ReduceMotion {
		block.Transition(ui.ElementTransition{Duration: mobilePanelTransition.Duration, Position: true,
			Enter: &ui.Motion{Opacity: 0}, Exit: &ui.Motion{Opacity: 0}})
	}
}

func toolResultText(c *ui.Context, text string, mark *markdown.RangeDecoration) {
	root := ui.Column(c)
	state := ui.Local(root, "result-text", func() mobileOutputText { return mobileOutputText{text: text} })
	state.update(c, text, true)
	root.Children(func() {
		color := palette(c).ink
		spans := state.spans(c, text, color)
		if len(spans) == 0 {
			spans = []ui.Span{{Text: text}}
		}
		value := ui.RichText(c, spans...).Wrap().FontSize(controlTextSize * c.Preferences().TextScale).
			TextColor(color).Selectable().Label(text)
		decorateTranscriptText(c, value, text, mark, nil)
	})
}

func snapshotSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d 字节", size)
	}
	return fmt.Sprintf("%.1f KB", float64(size)/1024)
}

func (a *mobileApp) transcriptUserBubble(c *ui.Context, parent *mygo.Window, session domain.Session, item domain.Item, runs []mobileclient.SentMessageRun, mark *markdown.RangeDecoration, motion *mobileSendScroll) {
	bubble := mobileUserBubble(c.Key("user-surface")).Transition(mobileHistoryBubbleMotion)
	if motion != nil {
		motion.bubble(c, bubble, item.ID)
	}
	bubble.Children(func() {
		if item.Kind == "command" {
			text := item.Name
			if text == "" {
				text = sentMessageText(runs)
			}
			label(c, text, float32(a.model.Preferences.ReplySize), palette(c).ink).LineHeight(transcriptLeading).Selectable().Font(a.codeFont())
		} else {
			a.sentMessageBody(c, parent, session, runs, item.Text, mark)
		}
		for _, attachment := range item.Attachments {
			if parent == nil || attachment.InlineReference() {
				continue
			}
			if imageAttachment(attachment) {
				a.transcriptImageAttachment(c.Key(attachment.ID), parent, session, attachment, true)
				continue
			}
			action(c.Key(attachment.ID), attachment.Name, func() {
				a.openAttachment(parent, a.sessionByID(session.ID), attachment)
			})
		}
	})
}

func (a *mobileApp) codeResult(c *ui.Context, text string) {
	if text == "" {
		return
	}
	a.codeViewportView(c, "", text, structuredCodeHeight, nil, nil, nil)
}

func (a *mobileApp) requestResult(c *ui.Context, parent *mygo.Window, session domain.Session, item domain.Item, progress float32) {
	p := palette(c)
	ui.Column(c).Gap(6).Padding(4, 0, 4, 10).Opacity(progress).Transform(ui.Translation(0, 6*(1-progress))).BorderStartWidth(1).BorderColor(p.line).Label("回答结果").Children(func() {
		if len(item.Parts) == 0 && item.Text != "" {
			label(c, item.Text, captionTextSize, p.secondary).Selectable()
		}
		a.contentParts(c, parent, session, item.Parts)
	})
}

type toolResultBodyKey string

func (a *mobileApp) toolItem(c *ui.Context, parent *mygo.Window, session domain.Session, item domain.Item, open *bool, mark *markdown.RangeDecoration, grouped bool, anchors ...func()) ui.Element {
	item = conversationui.ReasoningForDisplay(item)
	p := palette(c)
	name, symbol := item.Name, terminalSymbol
	switch item.Presentation {
	case "reasoning":
		symbol = infoSymbol
		if name == "" {
			name = "思考摘要"
		}
	case "files", "context", "compaction":
		symbol = filesSymbol
	case "review", "search":
		symbol = searchSymbol
	case "wait":
		symbol = clockSymbol
	}
	if name == "" {
		name = "工具活动"
	}
	isCommand := commandTool(item)
	size, ink := float32(captionTextSize), p.ink
	font := mobileStatusFont(c, size)
	if isCommand {
		font.Family = "monospace"
	}
	if grouped {
		ink = p.secondary
		if isCommand {
			size = captionTextSize
			font = mobileStatusFont(c, size)
			font.Family = "Menlo"
			symbol = toolTerminalSymbol
		}
	}
	lineHeight := mobileStatusLineHeight(font)
	titleName := name
	if isCommand {
		titleName = strings.Join(strings.Fields(name), " ")
	}
	status := map[string]string{
		"starting": "正在启动", "running": "进行中", "completed": "已完成",
		"failed": "调用失败", "declined": "已拒绝", "interrupted": "已中断",
		"stopped": "已停止", "unknown": "状态未知",
	}[item.Status]
	issue := item.Status == "failed" || item.Status == "interrupted" || item.Status == "declined"
	if item.Final && item.ExitCode != nil && *item.ExitCode != 0 {
		status = fmt.Sprintf("退出码 %d", *item.ExitCode)
		issue = true
	}
	duration := ""
	if item.Presentation == "wait" {
		for _, part := range item.Parts {
			if part.Kind == "text" && part.Label == "时长" && part.Text != "" {
				duration = part.Text
				break
			}
		}
	}
	hasText := item.Text != "" && (!isCommand || strings.TrimRight(item.Text, "\r\n") != "")
	hasBody := item.Presentation != "wait" && (hasText || len(item.Parts) != 0 || len(item.Files) != 0 || item.Truncated)
	if open == nil {
		open = ui.Local(c.Root(), "tool:"+session.ID+":"+item.ID, func() bool {
			return !item.Final && !a.model.Preferences.CollapseLatestTools
		})
	}
	var disclosureProgress float32
	children := func() {
		if !grouped {
			transcriptSymbol(c, symbol, name, font, 14, p.secondary)
		}
		title := label(c, titleName, size, ink).Font(font.Family).FixedLineHeight(lineHeight).
			MinWidth(0).MaxLines(2).Ellipsis("…").Shrink(1)
		if duration != "" {
			title.SingleLine()
			oneLine(c, duration, size, p.secondary).Shrink(0).FixedLineHeight(lineHeight)
		}
		if status != "" && !(grouped && isCommand && !issue) {
			color := p.secondary
			if issue && !grouped {
				color = p.warning
			}
			oneLine(c.Key("tool-outcome"), status, size, color).Shrink(0).FixedLineHeight(lineHeight).
				Transition(mobileFeedbackFade(mobileReplyFade)).
				ContentTransition(ui.ContentTransition{Animation: &mobileTitleMotion})
		}
		if hasBody {
			if grouped {
				paintMobileToolChevron(c.Key("chevron"), name, font, -90*(1-disclosureProgress), nil)
			} else {
				mark := rightSymbol
				if *open {
					mark = downSymbol
				}
				transcriptSymbol(c, mark, name, font, 12, p.secondary)
			}
		}
	}
	var heading ui.Element
	if hasBody {
		value := status
		disclosure := "已折叠"
		if *open {
			disclosure = "已展开"
		}
		if value == "" {
			value = disclosure
		} else {
			value += " · " + disclosure
		}
		heading = ui.ButtonBase(c).MinHeight(mobileTapSize(c)).AlignSelf(ui.Start).Gap(6).AlignItems(ui.Baseline).
			Role(ui.RoleButton).Label(name).Value(value).OnClick(func() {
			if len(anchors) > 0 {
				anchors[0]()
			}
			*open = !*open
			if *open {
				a.previewCommandOpened(session.ID, item.ID)
			}
		})
	} else {
		heading = ui.Row(c).MinHeight(28).Gap(6).AlignItems(ui.Baseline)
	}
	if grouped {
		heading.MinHeight(lineHeight).FillWidth().MarginStart(mobileToolTextColumn).AlignItems(ui.Baseline).Justify(ui.Start)
		mobileToolGlyph(c.Key("timeline-icon"), symbol, name, font).Absolute().Left(mobileToolTimelineInset).Top(0)
		if hasBody {
			disclosureProgress = mobileToolReveal(c, heading, *open, mark != nil)
		}
	}
	var command *mobileCommandOutput
	if isCommand {
		command = ui.Local(heading, "command-output", func() mobileCommandOutput { return mobileCommandOutput{} })
		if item.Name != "" {
			heading.ContextMenu(func(menu *ui.Menu) {
				if menu.Item("复制命令").Chosen() {
					mygo.Clipboard.WriteText(item.Name)
				}
			})
		}
		if !hasBody || !*open {
			command.render(c, item, false, nil, name, a.codeFont())
		}
	}
	heading.Children(children)
	visible := hasBody && *open
	build := func(c *ui.Context) {
		block := ui.Column(c).FillWidth().Gap(7)
		if grouped {
			block.Padding(8, 0, 0, mobileToolTextColumn)
		} else {
			block.Padding(4, 0, 8, 10).BorderStartWidth(1).BorderColor(p.line)
		}
		block.Children(func() {
			if command != nil {
				command.render(c.Key("command-output"), item, true, mark, name, a.codeFont())
			} else if item.Text != "" {
				row := c.Key("tool-summary")
				block := ui.Column(row)
				animateToolResultBlock(c, block)
				block.Children(func() { toolResultText(row, item.Text, mark) })
			}
			type fileKey struct {
				path       string
				occurrence int
			}
			files := make(map[string]int, len(item.Files))
			for _, file := range item.Files {
				row := c.Key(fileKey{file.Path, files[file.Path]})
				files[file.Path]++
				block := ui.Column(row)
				animateToolResultBlock(c, block)
				block.Children(func() { a.transcriptFile(row, file) })
			}
			a.contentParts(c, parent, session, item.Parts)
			if item.Truncated {
				note := label(c.Key("tool-truncated"), "内容过长，已截断显示", captionTextSize, p.secondary)
				animateToolResultBlock(c, note)
			}
		})
	}
	body := c.Key(toolResultBodyKey(item.ID))
	if grouped && hasBody {
		frame := ui.Column(body).FillWidth().Shrink(0).Clip()
		conversationui.AnimatedContentHeight(body, frame, visible, visible, mobileToolDisclosureDuration, func() float32 {
			build(body.Key("result"))
			return 0
		})
		return heading
	}
	toolResultPanel(body, visible, mark != nil, nil, build)
	return heading
}

// Resize the measured list content so the following rows move with it.
// The intrinsic stack stays at full width inside the growing clip.
func toolResultPanel(c *ui.Context, visible, immediate bool, progress *float32, build func(*ui.Context), viewport ...float32) {
	frame := ui.Column(c).FillWidth().Shrink(0).Clip()
	type measurement struct {
		width, height float32
		baseline      bool
	}
	size := ui.Local(frame, "tool-result-panel", func() measurement { return measurement{baseline: visible} })
	target := float32(0)
	if visible || progress != nil {
		target = size.height
	}
	shown := target
	if immediate || c.Preferences().ReduceMotion || progress != nil && *progress < 1 {
		frame.Jump("height", target)
	} else {
		shown = frame.AnimateTo("height", target, ui.Animation{Duration: mobileToolDisclosureDuration})
	}
	if progress != nil {
		if len(viewport) > 0 && viewport[0] > 0 && *progress < 1 {
			shown = min(shown, viewport[0])
		}
		shown *= *progress
	}
	// While fully open the group takes the real height of its animated
	// children. The parent only owns clipping during group disclosure.
	naturalFlow := progress != nil && visible && *progress >= 1
	if !naturalFlow && (!visible || size.height > 0 || !size.baseline) {
		frame.Height(shown)
	}
	if !visible && shown <= 0 {
		return
	}
	frame.Children(func() {
		body := c.Key("measured")
		content := ui.Column(body).FillWidth().Shrink(0).Disabled(!visible).DimDisabled(false)
		if !naturalFlow && (size.height > 0 || !size.baseline) {
			content.Absolute().Left(0).Right(0).Top(0)
		}
		invalidate := c.Services().Invalidate
		measure := func(r ui.Rect) {
			if r.H <= 0 || r.W <= 0 {
				return
			}
			resized := size.width != 0 && math.Abs(float64(size.width-r.W)) > .5
			if resized || size.height == 0 && size.baseline {
				frame.Jump("height", r.H)
			}
			if resized || math.Abs(float64(size.height-r.H)) > .5 {
				size.width, size.height = r.W, r.H
				invalidate()
			}
		}
		content.DrawOver(func(_ *ui.Painter, r ui.Rect) { measure(r) })
		content.Children(func() { build(body) })
		if size.height == 0 && !size.baseline {
			// A zero-height clip has layout bounds before it starts painting.
			measure(content.LayoutBounds())
			c.AnimationFrame()
		}
	})
}

func (a *mobileApp) transcriptFile(c *ui.Context, file domain.FileChange) {
	p := palette(c)
	kind := map[string]string{"add": "新增", "delete": "删除", "update": "修改"}[file.Kind]
	if file.Destination != "" {
		kind = "移动"
	}
	section := ui.Column(c).Gap(4)
	section.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			label(c, file.Path, captionTextSize, p.ink).Font("monospace").MinWidth(0).Grow(1).Selectable()
			if kind != "" {
				label(c, kind, captionTextSize, p.secondary).Shrink(0)
			}
		})
		if file.Destination != "" {
			label(c, file.Destination, captionTextSize, p.secondary).Font("monospace").Selectable()
		}
		a.transcriptPatch(c.Key("file-patch"), file)
	})
}

func (a *mobileApp) transcriptPatch(c *ui.Context, file domain.FileChange) {
	if file.Patch == "" {
		return
	}
	p := palette(c)
	section := ui.Column(c)
	section.Children(func() {
		type patchView struct {
			source, kind string
			diff         editor.DiffState
			height       float32
		}
		patch := ui.Local(section, "patch", func() patchView { return patchView{} })
		if patch.source != file.Patch || patch.kind != file.Kind {
			patch.source, patch.kind = file.Patch, file.Kind
			patch.diff = editor.DiffState{Layout: editor.DiffUnified, Wrap: true, TextSelection: true}
			patch.height = 0
			switch file.Kind {
			case "add":
				patch.diff.Old, patch.diff.New = editor.NewBuffer(""), editor.NewBuffer(file.Patch)
			case "delete":
				patch.diff.Old, patch.diff.New = editor.NewBuffer(file.Patch), editor.NewBuffer("")
			default:
				if patches, err := editor.ParsePatch(file.Patch); err == nil && len(patches) != 0 && len(patches[0].Hunks) != 0 {
					patches[0].OldName, patches[0].NewName = file.Path, file.Path
					patch.diff.SetPatch(patches[0])
				}
			}
		}
		diff := &patch.diff
		if diff.Old == nil || diff.New == nil {
			a.codeResult(c, file.Patch)
			return
		}
		diff.Theme = editor.DefaultTheme(c.Theme())
		diff.Theme.Background, diff.Theme.Text = p.inset, p.ink
		diff.Theme.Added, diff.Theme.Deleted = p.active, p.diffRemoved
		diff.Theme.AddedBackground, diff.Theme.DeletedBackground = p.diffAddedFill, p.diffRemovedFill
		diff.Font = ui.Font{Family: a.codeFont(), Size: captionTextSize * c.Preferences().TextScale}
		const maxHeight = 200
		if patch.height == 0 {
			_, deleted := diff.Stats()
			patch.height = min(maxHeight, float32(diff.New.LineCount()+deleted+1)*20)
		}
		invalidate := c.Services().Invalidate
		editor.DiffView(c, diff).FillWidth().Height(patch.height).Label("文件补丁：" + file.Path).
			DrawOver(func(_ *ui.Painter, _ ui.Rect) {
				if measured := diff.ContentHeight(); measured > 0 {
					next := min(maxHeight, max(24, measured))
					if math.Abs(float64(next-patch.height)) > .5 {
						patch.height = next
						invalidate()
					}
				}
			})
	})
}

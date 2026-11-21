package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"strings"
	"time"
)

type conversationOutline struct {
	Warm, Dismissed bool
	FocusNext       bool
	KeyboardIndex   int
	PreviewEntry    string
	PreviewHeight   float32
	List            n.ListState
	Session         string
	Hover, Current  int
	Since           time.Time
	Anchor          n.Rect
	Entry           string
	Count           int
	Height          float32
}
type outlineEntry struct {
	ID, Prompt, Reply string
	Row               int
	Order             int64
}

var latestIcon = svg("M5 7.5l5 5 5-5")

func outlineEntries(rows []transcriptRow) []outlineEntry {
	entries := []outlineEntry{}
	for i, row := range rows {
		if row.item.Kind == "user" && row.group == "" {
			entries = append(entries, outlineEntry{ID: row.item.ID, Prompt: row.item.Text, Row: i + 1, Order: row.item.Order})
		} else if row.item.Kind == "assistant" && len(entries) > 0 && entries[len(entries)-1].Reply == "" {
			entries[len(entries)-1].Reply = row.item.Text
		}
	}
	return entries
}
func (v *View) conversationOutline(c *n.Context, t *n.Theme, state *n.ListState, rows []transcriptRow) {
	bounds := state.Bounds(c)
	if bounds.W == 0 {
		c.AnimationFrame()
		return
	}
	if bounds.W < 880 {
		return
	}
	entries := outlineEntries(rows)
	if index, ok := v.Model.HistoryOutlines[v.Model.SessionID]; ok {
		positions := map[string]int{}
		for i, row := range rows {
			positions[row.item.ID] = i + 1
		}
		entries = nil
		for _, p := range index.Prompts {
			entries = append(entries, outlineEntry{ID: p.ID, Prompt: p.Text, Row: positions[p.ID], Order: p.Order})
		}
	}
	if len(entries) < 4 {
		return
	}
	if v.outline == nil || v.outline.Session != v.Model.SessionID {
		v.outline = &conversationOutline{Session: v.Model.SessionID, Hover: -1, Current: -1, KeyboardIndex: -1}
	}
	outline := v.outline
	first, _ := state.Visible()
	current := 0
	var order int64
	for i := 0; i < len(rows) && i < first; i++ {
		if rows[i].item.Order > 0 {
			order = rows[i].item.Order
		}
	}
	for i, e := range entries {
		if e.Row > 0 && e.Row <= first || order > 0 && e.Order <= order {
			current = i
		}
	}
	// Short final rounds cannot reach the top when scrolling is clamped.
	// Only the conversation's end counts; a history page may have newer rows.
	window := v.Model.Details[v.Model.SessionID].History
	if state.AtEnd() && (window == nil || !window.Newer) {
		current = len(entries) - 1
	}
	// The toolbar and composer paint over history. Keep navigation inside
	// the exposed reading area too, including an expanded queue/question dock.
	top := float32(48)
	dockHeight := v.dockHeight
	if dockHeight == 0 {
		dockHeight = 108
	}
	available := max(float32(0), bounds.H-top-dockHeight-16)
	if available < 26 {
		return
	}
	// Center in the window rather than the shorter area above the composer.
	// Long rails shrink symmetrically to stay clear of the fixed surfaces.
	center := bounds.H / 2
	bottom := top + available
	if center < top+13 || center > bottom-13 {
		center = (top + bottom) / 2
	}
	height := min(float32(len(entries)*10+16), min(640, min(bounds.H*.7, 2*min(center-top, bottom-center))))
	top = center - height/2
	hovered := -1
	outline.List.Key = func(i int) any { return entries[i].ID }
	track := n.Column(c.Key("conversation-outline-rail")).Absolute().Left(16).Top(top).Width(36).Height(height).Label("对话导航")
	// Queue the target before building virtual rows, using their stable pitch.
	// Centering gives measurement corrections room on both sides of the tick.
	// Keep manual rail browsing independent; catch up once the pointer leaves.
	if !track.Hovered() && !track.FocusWithin() && (current != outline.Current || entries[current].ID != outline.Entry || len(entries) != outline.Count || height != outline.Height) {
		outline.List.ScrollTo(current, n.Center)
		outline.Current, outline.Entry = current, entries[current].ID
		outline.Count, outline.Height = len(entries), height
	}
	track.Children(func() {
		n.List(c.Key("conversation-outline"), &outline.List, len(entries), func(i int) {
			entry := entries[i]
			// Keep the line pitch while making the existing end padding part
			// of the edge buttons, so approaching from below keeps the last hit.
			var top, bottom float32
			if i == 0 {
				top = 8
			}
			if i == len(entries)-1 {
				bottom = 8
			}
			button := n.ButtonBase(c.Key(entry.ID)).KeepFocus().Width(36).Height(10+top+bottom).Padding(top, 0, bottom, 0).Justify(n.Start).Label(fmt.Sprintf("跳转到消息 %d", i+1))
			if outline.FocusNext && outline.KeyboardIndex == i {
				if button.FocusVisible() {
					outline.FocusNext = false
				} else {
					button.Focus()
				}
			}
			if button.FocusVisible() && !outline.FocusNext {
				outline.KeyboardIndex = i
			}
			if button.Hovered() || button.FocusVisible() && !track.Hovered() {
				hovered = i
				outline.Anchor = button.Bounds()
				outline.Anchor.Y += top
				outline.Anchor.H = 10
			}
			length := float32(6)
			if outline.Hover >= 0 {
				widths := [...]float32{26, 20, 14, 10}
				if distance := abs(i - outline.Hover); distance < len(widths) {
					length = widths[distance]
				}
			}
			opacity := float32(.4)
			color := t.TextMuted
			if i == current && outline.Hover < 0 {
				opacity = .6
				color = t.Text
			}
			if i == outline.Hover {
				opacity = 1
				color = t.Text
			}
			button.Children(func() {
				n.Column(c).Width(button.AnimateTo("tick-width", length, motionAnimation(c, 160*time.Millisecond))).Height(2).Background(color.Alpha(opacity))
			})
			if button.Clicked() {
				hadFocus := v.composerHadFocus
				if entry.Row > 0 {
					state.KeepRowWithinContent(entry.Row, 80)
				} else {
					v.requestHistory(domain.HistoryQuery{AroundID: entry.ID}, true)
				}
				v.focusComposer = hadFocus
			}
		}).Fill().Scrollbars(false)
	})
	if track.FocusWithin() {
		next := outline.KeyboardIndex
		switch {
		case c.Shortcut(0, n.KeyDown):
			next = min(len(entries)-1, next+1)
		case c.Shortcut(0, n.KeyUp):
			next = max(0, next-1)
		case c.Shortcut(0, n.KeyHome):
			next = 0
		case c.Shortcut(0, n.KeyEnd):
			next = len(entries) - 1
		}
		if next != outline.KeyboardIndex {
			outline.KeyboardIndex = next
			outline.FocusNext = true
			outline.Dismissed = false
			outline.List.ScrollTo(next, n.Center)
			c.AnimationFrame()
		}
	}
	if hovered != outline.Hover {
		outline.Hover = hovered
		outline.Since = c.Now()
		outline.Dismissed = false
		if hovered < 0 {
			outline.Warm = false
		}
		c.AnimationFrame()
	}
	if hovered >= 0 && !outline.Dismissed && !c.MenuOpen() {
		if !outline.Warm && c.Now().Sub(outline.Since) < 200*time.Millisecond {
			c.After(200*time.Millisecond - c.Now().Sub(outline.Since))
		} else {
			outline.Warm = true
			entry := entries[hovered]
			anchor := outline.Anchor
			if outline.PreviewEntry != entry.ID {
				outline.PreviewEntry = entry.ID
				outline.PreviewHeight = 0
			}
			n.Overlay(c.Key("outline-preview"), func() {
				w, _ := c.Size()
				readingTop := bounds.Y + 48 + 8
				readingBottom := bounds.Y + bounds.H - dockHeight - 8
				y := max(readingTop, min(anchor.Y+anchor.H/2-outline.PreviewHeight/2, readingBottom-outline.PreviewHeight))
				panel := n.Column(c.Key("preview:"+entry.ID)).Label("对话导航预览").Role(n.RoleTooltip).Absolute().Left(min(anchor.X+40, w-328)).Top(y).Width(320).MaxHeight(max(0, readingBottom-readingTop)).Clip().Padding(8).Gap(4).Radius(12).Background(raised(t)).Border(1, t.Border).Shadow(0, 4, 8, 0, n.RGBA(0, 0, 0, .12)).PassThrough()
				if outline.PreviewHeight == 0 {
					if h := panel.LayoutBounds().H; h > 0 {
						outline.PreviewHeight = h
						panel.Top(max(readingTop, min(anchor.Y+anchor.H/2-h/2, readingBottom-h)))
					} else {
						panel.Opacity(0)
						c.AnimationFrame()
					}
				}
				invalidate := c.Services().Invalidate
				panel.DrawOver(func(_ *n.Painter, r n.Rect) {
					if math.Abs(float64(r.H-outline.PreviewHeight)) > .1 {
						outline.PreviewHeight = r.H
						invalidate()
					}
				})
				if panel.OverlayShortcut(0, n.KeyEscape) {
					outline.Dismissed = true
					c.AnimationFrame()
				}
				panel.Children(func() {
					prompt := strings.TrimSpace(entry.Prompt)
					if prompt == "" {
						prompt = "图片与附件"
					}
					n.Text(c, prompt).Label("预览标题").SingleLine().Ellipsis("…").FontSize(13).FixedLineHeight(20).FontWeight(500)
					if entry.Reply != "" {
						n.Column(c).FillWidth().MaxHeight(60).Clip().Children(func() {
							n.Text(c, outlineReplyPreview(entry.Reply)).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
						})
					}
				})
			})
		}
	}
}
func shortPreview(text string, maxRunes int) string {
	r := []rune(strings.Join(strings.Fields(text), " "))
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return string(r)
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func (v *View) returnToLatest(c *n.Context, t *n.Theme, state *n.ListState, dock n.Element) {
	window := v.Model.Details[v.Model.SessionID].History
	newer := window != nil && window.Newer
	if !newer && (state.AtEnd() || state.FollowingEnd()) || v.Model.HistoryEdits[v.Model.SessionID].ItemID != "" {
		return
	}
	motion := fadeMotion(c, 160*time.Millisecond)
	if !c.Preferences().ReduceMotion {
		motion.Enter.Y, motion.Exit.Y = 4, 4
	}
	button := control(c, 14).KeepFocus().Height(28).PaddingX(10).Clip().Radius(14).Background(raised(t)).Border(1, t.Border.Alpha(.65)).
		Label("回到最新").Tooltip("回到最新消息").AttachTo(dock, n.AnchorTop, n.AnchorBottom).Margin(0, 0, 12, 0).Transition(motion)
	color := textHoverColor(c, button, t.TextMuted)
	width := float32(20) // Horizontal capsule insets; child glyphs keep natural widths.
	button.Children(func() {
		font := n.Font{Family: t.Font, Size: 12, Weight: 400}
		turn, active := v.activeConversationTurn()
		var a runningActivity
		if active {
			a = v.runningActivity(turn)
		}
		if !active || !v.Model.Connected || a.waiting && a.sending == "" {
			textWidth, _ := n.ShapeText("最新消息", font, 0).Size()
			width += 14 + 5 + textWidth
			inlineIconText(c.Key("latest-idle"), latestIcon, "最新消息", font, 14, 5, color).Shrink(0).Transition(fadeMotion(c, 160*time.Millisecond))
			return
		}
		label, pulse, dotColor := a.label, true, t.Success
		if a.sending != "" {
			label, pulse, dotColor = a.sending, false, t.TextMuted
		} else if a.compaction != "" {
			label = "正在压缩上下文"
		} else {
			p := v.progressFor(c, turn)
			if v.runningGroup != "" && v.activityTurn == turn.ID {
				label = p.label
			}
			label = p.activityCaption(c, label)
		}
		font.Size = 11
		textWidth, _ := n.ShapeText(label, font, 0).Size()
		width += 12 + textWidth + 6 + 14
		n.Row(c.Key("latest-activity")).Height(20).Shrink(0).AlignItems(n.Baseline).Label("最新运行状态").Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
			phase := c.Now()
			if turn.StartedAt != nil {
				phase = *turn.StartedAt
			}
			if p := v.runningProgress; p != nil && p.turn == turn.ID {
				phase = p.started
			}
			statusDot(c, label, font, dotColor, pulse, phase).Width(12)
			caption := n.Column(c.Key("caption")).Height(20).Shrink(0).Clip()
			caption.Width(latestControlWidth(c, caption, textWidth)).Children(func() {
				n.Text(c.Key("label"), label).FontSize(11).FixedLineHeight(20).SingleLine().Shrink(0).TextColor(color).
					ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			})
			// An empty clock has no text baseline: exclude its box-bottom
			// fallback from the shared text baseline when no time is known.
			clock := n.Column(c.Key("clock")).Height(20).Shrink(0).AlignSelf(n.Start).Clip()
			var elapsedWidth float32
			clock.Children(func() {
				if pulse {
					elapsed, naturalWidth := runningElapsed(c.Key("elapsed"), a.started, color, true)
					elapsedWidth = naturalWidth
					if elapsed.Valid() {
						elapsed.Transition(fadeMotion(c, 160*time.Millisecond))
					}
				}
			})
			clock.Width(latestControlWidth(c, clock, elapsedWidth))
			width += elapsedWidth
			inlineSymbol(c, latestIcon, label, font, 14, color).Margin(0, 0, 0, 6)
		})
	})
	// Animate layout width before AttachTo places the capsule, so its center
	// follows the dock immediately even while the composer changes height.
	button.Width(latestControlWidth(c, button, width))
	if button.Clicked() {
		if newer {
			v.requestHistory(domain.HistoryQuery{}, true)
		} else if c.Preferences().ReduceMotion {
			state.ScrollToEnd()
		} else {
			state.ScrollToEnd(n.Animation{Duration: 320 * time.Millisecond, Ease: composerDescentEase})
		}
	}
}

func latestControlWidth(c *n.Context, element n.Element, target float32) float32 {
	if c.Preferences().ReduceMotion {
		return element.Jump("width", target)
	}
	return element.AnimateTo("width", target, motionAnimation(c, 160*time.Millisecond))
}

func outlineReplyPreview(text string) string {
	lines := strings.SplitN(text, "\n", 96)
	var paragraph []string
	fenced := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		paragraph = append(paragraph, line)
	}
	if len(paragraph) == 0 {
		return shortPreview(text, 120)
	}
	return shortPreview(strings.Join(paragraph, " "), 120)
}

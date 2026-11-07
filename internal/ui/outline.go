package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"strings"
	"time"
)

type conversationOutline struct {
	List           n.ListState
	Session        string
	Hover, Current int
	Since          time.Time
	Anchor         n.Rect
}
type outlineEntry struct {
	ID, Prompt, Reply string
	Row               int
}

var latestIcon = svg("M5 7.5l5 5 5-5")

func outlineEntries(rows []transcriptRow) []outlineEntry {
	entries := []outlineEntry{}
	for i, row := range rows {
		if row.item.Kind == "user" && row.group == "" {
			entries = append(entries, outlineEntry{ID: row.item.ID, Prompt: row.item.Text, Row: i + 1})
		} else if row.item.Kind == "assistant" && len(entries) > 0 && entries[len(entries)-1].Reply == "" {
			entries[len(entries)-1].Reply = row.item.Text
		}
	}
	return entries
}
func (v *View) conversationOutline(c *n.Context, t *n.Theme, state *n.ListState, rows []transcriptRow) {
	bounds := state.Bounds(c)
	if bounds.W < 880 {
		return
	}
	entries := outlineEntries(rows)
	if len(entries) < 4 {
		return
	}
	if v.outline == nil || v.outline.Session != v.Model.SessionID {
		v.outline = &conversationOutline{Session: v.Model.SessionID, Hover: -1, Current: -1}
	}
	outline := v.outline
	first, _ := state.Visible()
	current := 0
	for i, e := range entries {
		if e.Row <= first {
			current = i
		}
	}
	height := min(float32(len(entries)*10+16), min(640, bounds.H*.7))
	hovered := -1
	track := n.Scroll(c.Key("conversation-outline")).Absolute().Left(16).Top((bounds.H - height) / 2).Width(36).Height(height).Label("对话导航").Children(func() {
		outline.List.Key = func(i int) any { return entries[i].ID }
		n.List(c, &outline.List, len(entries), func(i int) {
			entry := entries[i]
			button := n.ButtonBase(c.Key(entry.ID)).Width(36).Height(10).Justify(n.Start).Label(fmt.Sprintf("跳转到消息 %d", i+1))
			if button.Hovered() {
				hovered = i
				outline.Anchor = button.Bounds()
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
				state.KeepRow(entry.Row, 80)
				v.focusComposer = hadFocus
			}
		}).FillWidth().FillHeight().PaddingY(8)
	})
	if current != outline.Current && !track.Hovered() {
		outline.List.ScrollIntoView(current)
		outline.Current = current
	}
	if hovered != outline.Hover {
		outline.Hover = hovered
		outline.Since = c.Now()
		c.AnimationFrame()
	}
	if hovered >= 0 {
		if c.Now().Sub(outline.Since) < 300*time.Millisecond {
			c.After(300*time.Millisecond - c.Now().Sub(outline.Since))
		} else {
			entry := entries[hovered]
			anchor := outline.Anchor
			n.Overlay(c.Key("outline-preview"), func() {
				w, h := c.Size()
				n.Column(c).Absolute().Left(min(anchor.X+40, w-336)).Top(max(8, min(anchor.Y-24, h-160))).Width(320).Padding(8).Gap(4).Radius(10).Background(raised(t)).Border(1, t.Border).Shadow(0, 4, 20, 0, n.RGBA(0, 0, 0, .12)).IgnoreInput(true).Transition(popupMotion(c)).Children(func() {
					prompt := strings.TrimSpace(entry.Prompt)
					if prompt == "" {
						prompt = "图片与附件"
					}
					n.Text(c, prompt).SingleLine().Ellipsis("…").FontSize(13).FontWeight(500)
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
	if state.AtEnd() || state.FollowingEnd() || v.Model.HistoryEdits[v.Model.SessionID].ItemID != "" {
		return
	}
	motion := fadeMotion(c, 160*time.Millisecond)
	if !c.Preferences().ReduceMotion {
		motion.Enter.Y, motion.Exit.Y = 4, 4
	}
	button := control(c, 14).KeepFocus().Height(28).PaddingX(10).Radius(14).Background(raised(t)).Border(1, t.Border.Alpha(.65)).
		Label("回到最新").Tooltip("回到最新消息").AttachTo(dock, n.AnchorTop, n.AnchorBottom).Margin(0, 0, 12, 0).Transition(motion)
	color := textHoverColor(c, button, t.TextMuted)
	button.Children(func() {
		font := n.Font{Family: t.Font, Size: 12, Weight: 400}
		inlineIconText(c, latestIcon, "最新消息", font, 14, 5, color)
	})
	if button.Clicked() {
		if c.Preferences().ReduceMotion {
			state.ScrollToEnd()
		} else {
			state.ScrollToEnd(n.Animation{Duration: 320 * time.Millisecond, Ease: composerDescentEase})
		}
	}
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

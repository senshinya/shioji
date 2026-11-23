package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

const (
	mobileToolGlyphSlot     = transcriptGlyphSlot
	mobileToolTimelineInset = 10
	mobileToolTextColumn    = mobileToolTimelineInset + mobileToolGlyphSlot + 8
	mobileToolRowGap        = 8
	mobileToolSummaryGap    = 6
)

var (
	toolTerminalSymbol = toolTranscriptSymbol("M4 5l5 5-5 5M11 15h6")
	toolChevronSymbol  = toolTranscriptSymbol("M6 8l4 4 4-4")
	latestMobileSymbol = toolTranscriptSymbol("M5 7.5l5 5 5-5")
)

func toolTranscriptSymbol(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

type mobileToolTimeline struct {
	thread            string
	bodies            map[string]*bool
	disclosureVersion uint64
	projection        *mobileTranscriptProjection
	mobileclient.ToolTimeline
}

func (t *mobileToolTimeline) Toggle(key string) {
	t.ToolTimeline.Toggle(key)
	t.disclosureVersion++
}

type mobileToolDisclosure struct {
	from, to float32
	start    time.Time
	duration time.Duration
}

func (m *mobileToolDisclosure) position(now time.Time) (float32, bool) {
	elapsed := max(0, now.Sub(m.start))
	if elapsed >= m.duration {
		return m.to, false
	}
	return m.from + (m.to-m.from)*float32(elapsed)/float32(m.duration), true
}

func mobileToolReveal(c *ui.Context, header ui.Element, open, immediate bool) float32 {
	target := float32(0)
	if open {
		target = 1
	}
	motion := ui.Local(header, "tool-disclosure", func() mobileToolDisclosure {
		return mobileToolDisclosure{to: target}
	})
	if immediate || c.Preferences().ReduceMotion {
		*motion = mobileToolDisclosure{to: target}
		return target
	}
	now := c.Now()
	position, active := motion.position(now)
	if motion.to != target {
		distance := max(position-target, target-position)
		*motion = mobileToolDisclosure{from: position, to: target, start: now,
			duration: time.Duration(float32(mobileToolDisclosureDuration) * distance)}
		active = motion.duration > 0
	}
	if active {
		c.AnimationFrame()
	}
	return ui.EaseOut(position)
}

func (a *mobileApp) toolTimeline(s domain.Session) *mobileToolTimeline {
	if a.toolTimelines == nil {
		a.toolTimelines = make(map[app.WorkKey]*mobileToolTimeline)
	}
	key := a.model.WorkKey(s.ID, "")
	scope := a.toolTimelines[key]
	if scope == nil || scope.thread != s.ThreadID {
		scope = &mobileToolTimeline{thread: s.ThreadID, bodies: make(map[string]*bool)}
		a.toolTimelines[key] = scope
	}
	return scope
}

func (a *mobileApp) toolGroup(c *ui.Context, parent *mygo.Window, session domain.Session, tools []domain.Item, group mobileclient.ToolGroup,
	timeline *mobileToolTimeline, list *ui.ListState, rowIndex int, phase string, started *time.Time, observed, animateCalls bool, target string, reveal bool,
	decorate func(domain.Item) *markdown.RangeDecoration) {
	p := palette(c)
	title := fmt.Sprintf("%d 次工具调用", group.Count)
	outcome := ""
	switch group.Outcome {
	case mobileclient.ToolGroupCompleted:
		outcome = "已完成"
	case mobileclient.ToolGroupEnded:
		outcome = "调用结束"
	}
	live := observed && group.Live
	inline := live && !group.Open
	header := c.Key("disclosure")
	font := mobileStatusFont(c, captionTextSize)
	lineHeight := mobileStatusLineHeight(font)
	button := transcriptDisclosure(mobileButton(header, title, nil)).AlignItems(ui.Start).Expanded(group.Open)
	progress := mobileToolReveal(c, button, group.Open, target != "")
	arrivals := ui.Local(button, "tool-call-arrivals", func() mobileToolCallArrivals { return nil })
	arrivals.observe(c.Now(), tools, animateCalls && group.Open && progress == 1 && target == "")
	sweepStart := *ui.Local(button, "tool-sweep-start", func() time.Time { return c.Now() })
	if started != nil && !started.IsZero() {
		sweepStart = *started
	}
	value := outcome
	if inline {
		value = phase
		if started != nil && !started.IsZero() {
			value += " · 本轮计时，开始于 " + started.Local().Format("15:04:05")
		}
		if outcome != "" {
			value += " · " + outcome
		}
	}
	keepPosition := func() {
		list.KeepRowWithinContent(rowIndex, button.Bounds().Y-list.Bounds(c).Y-float32(transcriptItemGap)/2)
	}
	button.Value(value).OnClick(func() {
		keepPosition()
		timeline.Toggle(group.Key)
	}).Children(func() {
		line := header.Key("line")
		row := ui.Row(line).MinWidth(0).MinHeight(lineHeight).Shrink(1).AlignItems(ui.Baseline).Gap(mobileToolSummaryGap).PassThrough()
		var sweep *mobileToolSweep
		if inline {
			sweep = newMobileToolSweep(line, row, sweepStart)
		}
		row.Children(func() {
			glyph := line.Key("group-glyph")
			if live && progress < 1 {
				icon := ui.Row(glyph).Width(mobileToolGlyphSlot).Height(lineHeight).AlignItems(ui.Baseline)
				icon.Children(func() {
					mobileToolGlyph(glyph.Key("terminal"), toolTerminalSymbol, title, font).Opacity(progress)
					mobileToolStatusDot(glyph.Key("live"), title, font, sweep).Absolute().Left(0).Top(0).Opacity(1 - progress)
				})
			} else {
				mobileToolGlyph(glyph, toolTerminalSymbol, title, font)
			}
			numeric := ui.ContentTransition{Numeric: true, Animation: &ui.Animation{Duration: mobileReplyFade}}
			sweep.caption(line.Key("count"), title, numeric)
			if outcome != "" {
				sweep.caption(line.Key("outcome"), outcome, numeric)
			}
			if live && progress < 1 {
				phaseKey := line.Key("inline-phase")
				if progress == 0 {
					mobileToolPhase(phaseKey, phase, started, sweep)
				} else {
					width, _ := ui.ShapeText(phase, font, 0).Size()
					if started != nil && !started.IsZero() {
						clockFont := font
						clockFont.Features = "tnum"
						clockWidth, _ := ui.ShapeText(mobileElapsed(c.Now().Sub(*started).Milliseconds()), clockFont, 0).Size()
						width += clockWidth + mobileToolSummaryGap
					}
					ui.Row(phaseKey).Gap(mobileToolSummaryGap).AlignItems(ui.Baseline).
						Width(width * (1 - progress)).MinWidth(0).Clip().Opacity(1 - progress).
						Children(func() { mobileToolPhase(phaseKey, phase, started, sweep) })
				}
			}
			paintMobileToolChevron(line.Key("arrow"), title, font, -90*(1-progress), sweep)
		})
	})
	type previousToolProgress struct {
		active  bool
		phase   string
		started *time.Time
	}
	trailing := ui.Local(button, "previous-tool-progress", func() previousToolProgress { return previousToolProgress{} })
	if live {
		*trailing = previousToolProgress{active: true, phase: phase, started: started}
	} else if group.Open || progress == 0 {
		*trailing = previousToolProgress{}
	}
	toolResultPanel(c.Key("body"), group.Open, target != "", &progress, func(c *ui.Context) {
		ui.Box(c.Key("heading-clearance")).Height(mobileToolRowGap - (transcriptMetadataHeight - 20)).Shrink(0)
		for index, tool := range tools {
			body := timeline.bodies[tool.ID]
			if body == nil {
				body = new(bool)
				timeline.bodies[tool.ID] = body
			}
			row := c.Key(tool.ID)
			entrance := arrivals.progress(c.Now(), tool.ID)
			if entrance < 1 {
				// The measured row already moves the followed viewport.
				list.FollowAnimation = nil
				c.AnimationFrame()
			}
			mobileToolCallLayout(row, entrance, func(call ui.Element) {
				call.Padding(0, 0, mobileToolRowGap, 0)
				if tool.ID == target {
					call.Background(p.selected).Radius(cardRadius)
					if reveal && group.Open {
						call.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter})
					}
				}
				var mark *markdown.RangeDecoration
				if decorate != nil && tool.ID != target {
					mark = decorate(tool)
				}
				var heading ui.Element
				if len(tools) > 1 {
					call.Draw(func(painter *ui.Painter, bounds ui.Rect) {
						bar := heading.LayoutBounds()
						center := bar.Y + lineHeight/2
						x := bounds.X + mobileToolTimelineInset + mobileToolGlyphSlot/2 - .5
						if index > 0 && center > bounds.Y+9 {
							painter.Fill(ui.Rect{X: x, Y: bounds.Y, W: 1, H: center - bounds.Y - 9}, p.line, 0)
						}
						if index+1 < len(tools) && bounds.Y+bounds.H > center+9 {
							painter.Fill(ui.Rect{X: x, Y: center + 9, W: 1, H: bounds.Y + bounds.H - center - 9}, p.line, 0)
						}
					})
				}
				call.Children(func() {
					heading = a.toolItem(row, parent, session, tool, body, mark, true, keepPosition)
				})
			})
		}
		// Keep the outgoing status in the measured stack until the clip closes.
		// Removing its height on click would move the bounded scroll anchor early.
		if trailing.active {
			row := c.Key("progress")
			status := ui.Row(row).MinHeight(lineHeight).AlignSelf(ui.Start).Gap(mobileToolSummaryGap).AlignItems(ui.Baseline).Opacity(progress)
			sweep := newMobileToolSweep(row, status, sweepStart)
			status.Children(func() {
				mobileToolStatusDot(row, trailing.phase, font, sweep)
				mobileToolPhase(row, trailing.phase, trailing.started, sweep)
			})
		}
	}, list.Bounds(c).H)
}

func mobileToolGlyph(c *ui.Context, symbol *ui.SVG, title string, font ui.Font) ui.Element {
	return transcriptSymbol(c, symbol, title, font, 14, palette(c).secondary)
}

func mobileToolStatusDot(c *ui.Context, title string, font ui.Font, sweep *mobileToolSweep) ui.Element {
	const size = 6
	ink := palette(c).active
	pulse := !c.Preferences().ReduceMotion
	return transcriptGlyph(c, title, font, transcriptGlyphSlot, size, func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, mobileBreathingInk(p, sweep.color(p, r, ink), pulse), size/2)
	})
}

func paintMobileToolChevron(c *ui.Context, title string, font ui.Font, angle float32, sweep *mobileToolSweep) {
	const size = 14
	ink := palette(c).secondary
	transcriptGlyph(c, title, font, size, size, func(p *ui.Painter, r ui.Rect) {
		p.Transform(ui.Rotation(angle), r.X+size/2, r.Y+size/2, func() {
			paintCenteredSymbol(p, toolChevronSymbol, r, sweep.color(p, r, ink))
		})
	})
}

func mobileToolPhase(c *ui.Context, phase string, started *time.Time, sweep *mobileToolSweep) {
	sweep.caption(c.Key("phase"), phase, ui.ContentTransition{Animation: &ui.Animation{Duration: mobileReplyFade}})
	if started != nil && !started.IsZero() {
		mobileClock(c.Key("elapsed"), *started, nil, mobileStatusFont(c, captionTextSize), sweep)
	}
}

func (a *mobileApp) transcriptLatest(c *ui.Context, dock ui.Element, sid string, detail domain.SessionDetail, list *ui.ListState, prefetch *transcriptPrefetch, load bool, caption string, started *time.Time, live bool) {
	p := palette(c)
	read := a.model.HistoryReads[sid]
	button := mobileButton(c, "回到最新消息", func() {
		prefetch.stop()
		if load {
			a.readTranscriptHistory(sid, detail, domain.HistoryQuery{})
		} else if c.Preferences().ReduceMotion {
			list.ScrollToEnd()
		} else {
			list.ScrollToEnd(ui.Animation{Duration: 320 * time.Millisecond, Ease: mobileComposerMotionEase})
		}
	}).KeepFocus().Value(caption).AlignItems(ui.Center).Justify(ui.Center).
		AttachTo(dock, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 4, 0).ZIndex(2).
		Disabled(load && (!a.model.Connected || detail.History.Invalidated || read != nil && read.Pending && prefetch.cancel == nil))
	const padding, gap, border = 10, 5, 1
	font := mobileStatusFont(c, captionTextSize)
	textWidth, _ := ui.ShapeText(caption, font, 0).Size()
	width := 2*(padding+border) + textWidth + footerIconSize + gap
	if live {
		width += transcriptGlyphSlot + gap
	}
	target := max(float32(math.Ceil(float64(width))), mobileTapSize(c))
	shown := button.AnimateTo("latest-width", target, mobileTitleMotion)
	button.Width(max(target, shown))
	if pane := list.Bounds(c); pane.W > 0 {
		button.MaxWidth(max(mobileTapSize(c), pane.W-2*pageInset))
	}
	button.Children(func() {
		ink := p.secondary
		lineHeight := mobileStatusLineHeight(font)
		chip := ui.Row(c.Key("chip")).FillWidth().Height(max(28, lineHeight+6)).PaddingX(padding).
			AlignItems(ui.Center).Justify(ui.Center).Radius(14).Background(p.floating).Border(border, p.floatingEdge).
			PassThrough().Transition(mobilePanelTransition)
		chip.Children(func() {
			line := c.Key("content")
			row := ui.Row(line.Key(live)).MinWidth(0).Shrink(1).AlignItems(ui.Baseline).Gap(gap).PassThrough().Transition(mobileFeedbackFade(160 * time.Millisecond)).
				Transform(ui.Translation(0, -inkCenterOffset(caption, font)))
			var sweep *mobileToolSweep
			if live {
				phase := *ui.Local(row, "latest-sweep-start", c.Now)
				if started != nil && !started.IsZero() {
					phase = *started
				}
				sweep = newMobileToolSweep(line, row, phase)
			}
			row.Children(func() {
				arrow := func() {
					transcriptGlyph(line.Key("arrow"), caption, font, footerIconSize, footerIconSize, func(p *ui.Painter, r ui.Rect) {
						paintCenteredSymbol(p, latestMobileSymbol, r, sweep.color(p, r, ink))
					})
				}
				if live {
					mobileToolStatusDot(line.Key("dot"), caption, font, sweep)
				} else {
					arrow()
				}
				sweep.caption(line.Key("caption"), caption, ui.ContentTransition{Animation: &ui.Animation{Duration: 160 * time.Millisecond}})
				if live {
					arrow()
				}
			})
		})
	})
}

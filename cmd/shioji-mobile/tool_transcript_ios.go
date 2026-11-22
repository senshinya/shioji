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
)

func toolTranscriptSymbol(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

type mobileToolTimeline struct {
	thread string
	bodies map[string]*bool
	mobileclient.ToolTimeline
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
	timeline *mobileToolTimeline, list *ui.ListState, index int, phase string, started *time.Time, observed bool, target string, reveal bool,
	decorate func(domain.Item) *markdown.RangeDecoration) {
	p := palette(c)
	title := fmt.Sprintf("%d 次工具调用", group.Count)
	outcome := ""
	switch {
	case group.Issues > 0:
		outcome = fmt.Sprintf("%d 项未成功", group.Issues)
	case group.Outcome == mobileclient.ToolGroupCompleted:
		outcome = "已完成"
	case group.Outcome == mobileclient.ToolGroupEnded:
		outcome = "调用结束"
	}
	live := observed && group.Live
	inline := live && !group.Open
	header := c.Key("disclosure")
	font := mobileStatusFont(c, mobileToolCaptionSize)
	lineHeight := mobileStatusLineHeight(font)
	button := transcriptDisclosure(mobileButton(header, title, nil)).AlignItems(ui.Start).Expanded(group.Open)
	progress := mobileToolReveal(c, button, group.Open, target != "")
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
	button.Value(value).OnClick(func() {
		list.KeepRowWithinContent(index, button.Bounds().Y-list.Bounds(c).Y-float32(transcriptItemGap)/2)
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
			if inline {
				mobileToolStatusDot(glyph, title, font, sweep)
			} else {
				mobileToolGlyph(glyph, toolTerminalSymbol, title, font)
			}
			numeric := ui.ContentTransition{Numeric: true, Animation: &ui.Animation{Duration: mobileReplyFade}}
			sweep.caption(line.Key("count"), title, numeric)
			if outcome != "" {
				sweep.caption(line.Key("outcome"), outcome, numeric)
			}
			if inline {
				mobileToolPhase(line, phase, started, sweep)
			}
			paintMobileToolChevron(line.Key("arrow"), title, font, -90*(1-progress), sweep)
		})
	})
	toolResultPanel(c.Key("body"), group.Open, target != "", &progress, func(c *ui.Context) {
		ui.Box(c.Key("heading-clearance")).Height(mobileToolRowGap - (transcriptMetadataHeight - 20)).Shrink(0)
		for index, tool := range tools {
			body := timeline.bodies[tool.ID]
			if body == nil {
				body = new(bool)
				timeline.bodies[tool.ID] = body
			}
			row := c.Key(tool.ID)
			call := ui.Column(row).FillWidth().Padding(0, 0, mobileToolRowGap, 0)
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
			call.Children(func() { heading = a.toolItem(row, parent, session, tool, body, mark, true) })
		}
		if live && group.Open {
			row := c.Key("progress")
			status := ui.Row(row).MinHeight(lineHeight).AlignSelf(ui.Start).Gap(mobileToolSummaryGap).AlignItems(ui.Baseline)
			sweep := newMobileToolSweep(row, status, sweepStart)
			status.Children(func() {
				mobileToolStatusDot(row, phase, font, sweep)
				mobileToolPhase(row, phase, started, sweep)
			})
		}
	})
}

func mobileToolGlyph(c *ui.Context, symbol *ui.SVG, title string, font ui.Font) ui.Element {
	return transcriptSymbol(c, symbol, title, font, 14, palette(c).secondary)
}

func mobileToolStatusDot(c *ui.Context, title string, font ui.Font, sweep *mobileToolSweep) {
	const size = 6
	ink := palette(c).active
	transcriptGlyph(c, title, font, transcriptGlyphSlot, size, func(p *ui.Painter, r ui.Rect) {
		color := sweep.color(p, r, ink)
		if sweep != nil {
			phase := p.Now().Sub(sweep.started).Seconds() / 1.4
			color = color.Alpha(.4 + .6*float32((1-math.Cos(2*math.Pi*phase))/2))
		}
		p.Fill(r, color, size/2)
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
		mobileClock(c.Key("elapsed"), *started, nil, mobileStatusFont(c, mobileToolCaptionSize), sweep)
	}
}

func (a *mobileApp) transcriptLatest(c *ui.Context, sid string, detail domain.SessionDetail, list *ui.ListState, prefetch *transcriptPrefetch, load bool, caption string, started *time.Time, live bool) {
	p := palette(c)
	read := a.model.HistoryReads[sid]
	ui.Row(c).Absolute().Left(0).Right(0).Bottom(8).Justify(ui.Center).PassThrough().Children(func() {
		ui.Row(c.Key("track")).WidthPercent(100).MaxWidth(conversationComposerWidth).PaddingX(conversationComposerInset).
			Justify(ui.End).PassThrough().Children(func() {
			button := mobileButton(c.Key("button"), "回到最新消息", func() {
				prefetch.stop()
				if load {
					a.readTranscriptHistory(sid, detail, domain.HistoryQuery{})
				} else if c.Preferences().ReduceMotion {
					list.ScrollToEnd()
				} else {
					list.ScrollToEnd(ui.Animation{Duration: 280 * time.Millisecond})
				}
			}).AlignItems(ui.Baseline).PaddingX(10).Gap(5).KeepFocus().Value(caption).
				Disabled(load && (!a.model.Connected || detail.History.Invalidated || read != nil && read.Pending && prefetch.cancel == nil))
			floatingSurface(c, button, 22).Transition(mobilePanelTransition).Children(func() {
				font := mobileStatusFont(c, captionTextSize)
				if live {
					transcriptStatusDot(c.Key("dot"), caption, font, p.active)
				}
				label(c.Key("caption"), caption, captionTextSize, p.secondary).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).
					ContentTransition(ui.ContentTransition{Animation: &ui.Animation{Duration: 180 * time.Millisecond}})
				if live && started != nil && !started.IsZero() {
					mobileRunningClock(c.Key("time"), *started)
				}
				transcriptSymbol(c.Key("arrow"), downSymbol, caption, font, 13, p.secondary)
			})
		})
	})
}

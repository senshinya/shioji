package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/conversationui"
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
	toolThinkingSymbol = mobileToolTimelineSymbol(conversationui.ThinkingIconPath)
	toolTerminalSymbol = mobileToolTimelineSymbol("M4 5l5 5-5 5M11 15h6")
	toolFileSymbol     = mobileToolTimelineSymbol("M5 2h7l4 4v12H5zM12 2v5h4M8 11h5M8 14h5")
	toolSearchSymbol   = mobileToolTimelineSymbol("M17 17l4 4M19 11a8 8 0 1 1-16 0 8 8 0 1 1 16 0")
	toolWaitSymbol     = mobileToolTimelineSymbol("M21 12a9 9 0 1 1-18 0 9 9 0 1 1 18 0M12 6v6l4 2")
	toolChevronSymbol  = toolTranscriptSymbol("M6 8l4 4 4-4")
	latestMobileSymbol = toolTranscriptSymbol("M5 7.5l5 5 5-5")
)

func toolTranscriptSymbol(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

const mobileToolIconSlot = 14

// Match the actual ink of each timeline icon so the paths can have different
// view boxes without changing the perceived weight or height of the row.
func mobileToolTimelineSymbol(path string) *ui.SVG {
	const inkHeight, stroke = 8, 1.05
	bounds := ui.MustParsePath(path).Bounds()
	scale := (inkHeight - stroke) / bounds.H
	side := mobileToolIconSlot / scale
	x, y := bounds.X+(bounds.W-side)/2, bounds.Y+(bounds.H-side)/2
	return ui.MustParseSVG([]byte(fmt.Sprintf(
		`<svg viewBox="%g %g %g %g" xmlns="http://www.w3.org/2000/svg"><path d="%s" fill="none" stroke="currentColor" stroke-width="%g" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
		x, y, side, side, path, stroke/scale,
	)))
}

func mobileToolTimelineIcon(presentation string) *ui.SVG {
	switch presentation {
	case "files", "context", "compaction":
		return toolFileSymbol
	case "review", "search":
		return toolSearchSymbol
	case "wait":
		return toolWaitSymbol
	default:
		return toolTerminalSymbol
	}
}

type mobileStandaloneReasoning struct {
	itemID  string
	label   string
	wasLive bool
	wasOpen bool
	hasBody bool
	phase   time.Time
	started *time.Time
}

type mobileToolGroupMorph struct {
	origin     mobileStandaloneReasoning
	started    time.Time
	duration   time.Duration
	wasOpen    bool
	openIDs    map[string]bool
	keepStatus bool
}

func (m *mobileToolGroupMorph) progress(now time.Time, reduce bool) float32 {
	if m == nil || reduce || m.duration <= 0 {
		return 0
	}
	elapsed := now.Sub(m.started)
	if elapsed >= m.duration {
		return 0
	}
	t := min(1, max(0, float32(elapsed)/float32(m.duration)))
	return 1 - ui.EaseInOut(t)
}

type mobileToolTimeline struct {
	thread            string
	bodies            map[string]*bool
	disclosureVersion uint64
	projection        *mobileTranscriptProjection
	groupKeys         map[string]string
	morphs            map[string]*mobileToolGroupMorph
	standalone        map[string]mobileStandaloneReasoning
	seenGroups        map[string]bool
	mobileclient.ToolTimeline
}

func (t *mobileToolTimeline) activeMorph(c *ui.Context, groupKey string) (*mobileToolGroupMorph, float32) {
	if t == nil || t.morphs == nil {
		return nil, 0
	}
	m := t.morphs[groupKey]
	if m == nil {
		return nil, 0
	}
	p := m.progress(c.Now(), c.Preferences().ReduceMotion)
	if p <= 0 {
		return nil, 0
	}
	return m, p
}

func (t *mobileToolTimeline) observeTranscript(c *ui.Context, transcript []mobileclient.TranscriptRow, items []domain.Item, groups map[int]mobileclient.ToolGroup, toolsShowProgress bool, liveReasoning string, liveEmptyReasoning, canAnimate bool) {
	now := c.Now()
	reduce := c.Preferences().ReduceMotion
	presentGroups := make(map[string]bool, len(groups))
	var nextStandalone map[string]mobileStandaloneReasoning
	for _, row := range transcript {
		if row.Footer != "" {
			continue
		}
		item := items[row.First]
		group, ok := groups[row.First]
		if !ok {
			if domain.ReasoningSummary(item) {
				if nextStandalone == nil {
					nextStandalone = make(map[string]mobileStandaloneReasoning)
				}
				wasLive := liveEmptyReasoning && item.ID == liveReasoning
				if prev, exists := t.standalone[item.ID]; exists {
					prev.wasLive = wasLive
					nextStandalone[item.ID] = prev
				} else {
					display := conversationui.ReasoningForDisplay(item)
					label := display.Name
					if label == "" {
						label = "思考摘要"
					}
					if wasLive {
						label = "正在思考"
					}
					hasBody := display.Text != "" || len(display.Parts) != 0 || len(display.Files) != 0 || display.Truncated
					nextStandalone[item.ID] = mobileStandaloneReasoning{
						itemID:  item.ID,
						label:   label,
						wasLive: wasLive,
						hasBody: hasBody,
					}
				}
			}
			continue
		}
		presentGroups[group.Key] = true
		if t.seenGroups != nil && !t.seenGroups[group.Key] && len(t.standalone) > 0 {
			var origin mobileStandaloneReasoning
			found := false
			if domain.ReasoningSummary(item) {
				origin, found = t.standalone[item.ID]
			} else {
				for k := row.First - 1; k >= 0 && items[k].TurnID == item.TurnID && domain.HistorySpacer(items[k]); k-- {
					if domain.ReasoningSummary(items[k]) {
						if prev, exists := t.standalone[items[k].ID]; exists {
							origin, found = prev, true
							break
						}
					}
				}
			}
			if found {
				if t.groupKeys == nil {
					t.groupKeys = make(map[string]string)
				}
				t.groupKeys[group.Key] = origin.itemID
				openIDs := make(map[string]bool)
				if origin.wasOpen {
					openIDs[origin.itemID] = true
				}
				for j := row.First; j < row.End; j++ {
					if prev, exists := t.standalone[items[j].ID]; exists && prev.wasOpen {
						openIDs[items[j].ID] = true
					}
				}
				keepStatus := toolsShowProgress && group.Live && !origin.wasLive && (group.Open || origin.wasOpen)
				if canAnimate && !reduce {
					if t.morphs == nil {
						t.morphs = make(map[string]*mobileToolGroupMorph)
					}
					t.morphs[group.Key] = &mobileToolGroupMorph{
						origin:     origin,
						started:    now,
						duration:   mobileReasoningMorph,
						wasOpen:    origin.wasOpen,
						openIDs:    openIDs,
						keepStatus: keepStatus,
					}
					c.AnimationFrame()
				}
			}
		}
	}
	t.seenGroups = presentGroups
	t.standalone = nextStandalone
	for key := range t.groupKeys {
		if !presentGroups[key] {
			delete(t.groupKeys, key)
		}
	}
	for key, morph := range t.morphs {
		if !presentGroups[key] || morph.progress(now, reduce) <= 0 {
			delete(t.morphs, key)
		} else {
			c.AnimationFrame()
		}
	}
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

func mobileToolReveal(c *ui.Context, header ui.Element, open, immediate bool, initialFrom ...float32) float32 {
	target := float32(0)
	if open {
		target = 1
	}
	motion := ui.Local(header, "tool-disclosure", func() mobileToolDisclosure {
		if len(initialFrom) > 0 && !immediate && !c.Preferences().ReduceMotion && initialFrom[0] != target {
			return mobileToolDisclosure{from: initialFrom[0], to: target, start: c.Now(), duration: mobileReasoningMorph}
		}
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
	freshReasoning func(string) bool, decorate func(domain.Item) *markdown.RangeDecoration) {
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
	morph, morphP := timeline.activeMorph(c, group.Key)
	originLive := morph != nil && morph.origin.wasLive
	suppressInline := morph != nil && morph.keepStatus && group.Open
	header := c.Key("disclosure")
	font := mobileStatusFont(c, captionTextSize)
	lineHeight := mobileStatusLineHeight(font)
	button := transcriptDisclosure(mobileButton(header, title, nil)).
		MinHeight(transcriptMetadataHeight).AlignItems(ui.Start).Expanded(group.Open)
	var progress float32
	if morph != nil && !morph.wasOpen && group.Open {
		progress = mobileToolReveal(c, button, group.Open, target != "", 0)
	} else if morph != nil && morph.wasOpen && !group.Open {
		progress = mobileToolReveal(c, button, group.Open, target != "", 1)
	} else {
		progress = mobileToolReveal(c, button, group.Open, target != "")
	}
	showInline := live && progress < 1 && !suppressInline
	closingMorph := morph != nil && morph.wasOpen && !group.Open
	inlineWeight := float32(0)
	if showInline {
		inlineWeight = 1 - progress
	}
	inlineFactor := 1 - morphP
	if closingMorph {
		inlineFactor = 1
	}
	dotWeight := inlineWeight * inlineFactor
	if originLive {
		dotWeight += morphP
	}
	thinkingWeight := float32(0)
	if morph != nil && !originLive {
		thinkingWeight = morphP
	}
	terminalWeight := max(0, 1-dotWeight-thinkingWeight)
	arrivals := ui.Local(button, "tool-call-arrivals", func() mobileToolCallArrivals { return nil })
	if *arrivals == nil && morph != nil && morph.wasOpen && group.Open && len(morph.openIDs) > 0 {
		*arrivals = make(mobileToolCallArrivals, len(morph.openIDs))
		for id := range morph.openIDs {
			(*arrivals)[id] = time.Time{}
		}
	}
	arrivals.observe(c.Now(), tools, animateCalls && group.Open && progress == 1 && target == "")
	sweepStart := *ui.Local(button, "tool-sweep-start", func() time.Time { return c.Now() })
	if started != nil && !started.IsZero() {
		sweepStart = *started
	}
	if originLive && !morph.origin.phase.IsZero() {
		sweepStart = morph.origin.phase
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
		if dotWeight > 0 {
			sweep = newMobileToolSweep(line, row, sweepStart)
			if sweep != nil {
				sweep.strength = dotWeight
			}
		}
		row.Children(func() {
			glyph := line.Key("glyph")
			if dotWeight > 0 || thinkingWeight > 0 {
				icon := ui.Row(glyph).Width(mobileToolGlyphSlot).Height(lineHeight).AlignItems(ui.Baseline)
				icon.Children(func() {
					mobileToolGlyph(glyph.Key("terminal"), toolTerminalSymbol, title, font).Opacity(terminalWeight)
					if thinkingWeight > 0 {
						transcriptSymbol(glyph.Key("summary"), infoSymbol, title, font, 14, p.secondary).Absolute().Left(0).Top(0).Opacity(thinkingWeight)
					}
					if dotWeight > 0 {
						mobileToolStatusDot(glyph.Key("live"), title, font, sweep).Absolute().Left(0).Top(0).Opacity(dotWeight)
					}
				})
			} else {
				mobileToolGlyph(glyph, toolTerminalSymbol, title, font)
			}
			numeric := ui.ContentTransition{Numeric: true, Animation: &ui.Animation{Duration: mobileReplyFade}}
			titleBox := ui.Column(line.Key("title-box")).MinWidth(0).Shrink(0)
			if morphP > 0 && morph.origin.label != "" {
				originW, _ := ui.ShapeText(morph.origin.label, font, 0).Size()
				targetW, _ := ui.ShapeText(title, font, 0).Size()
				titleBox.Width(originW + (targetW-originW)*(1-morphP)).Clip()
			}
			titleBox.Children(func() {
				if morphP > 0 {
					morphAnim := ui.Animation{Duration: mobileReasoningMorph, Ease: ui.EaseInOut}
					sweep.caption(line.Key("title"), title, ui.ContentTransition{Animation: &morphAnim})
				} else {
					sweep.caption(line.Key("title"), title, numeric)
				}
			})
			if outcome != "" {
				sweep.caption(line.Key("outcome"), outcome, numeric)
			}
			inlineExtent := inlineWeight * inlineFactor
			if inlineExtent > 0 {
				phaseKey := line.Key("inline-phase")
				if inlineExtent >= 1 {
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
						Width(width * inlineExtent).MinWidth(0).Clip().Opacity(inlineExtent).
						Children(func() { mobileToolPhase(phaseKey, phase, started, sweep) })
				}
			}
			chevronOpacity := float32(1)
			if originLive && morphP > 0 {
				chevronOpacity = max(0, min(1, (.75-morphP)/.75))
			}
			clockOpacity := float32(0)
			if originLive && !showInline && morphP > 0 && morph.origin.started != nil && !morph.origin.started.IsZero() {
				clockOpacity = max(0, (morphP-.45)/.55)
			}
			if clockOpacity > 0 {
				trailing := ui.Row(line.Key("trailing-slot")).MinHeight(lineHeight).Shrink(0).AlignItems(ui.Baseline)
				clockFont := font
				clockFont.Features = "tnum"
				clockWidth, _ := ui.ShapeText(mobileElapsed(c.Now().Sub(*morph.origin.started).Milliseconds()), clockFont, 0).Size()
				trailing.MinWidth(clockWidth + mobileToolSummaryGap)
				trailing.Children(func() {
					ui.Row(line.Key("arrow-slot")).Opacity(chevronOpacity).Children(func() {
						paintMobileToolChevron(line.Key("arrow"), title, font, -90*(1-progress), sweep)
					})
					clockKey := line.Key("elapsed")
					slot := ui.Row(clockKey).Absolute().Left(0).Top(0).AlignItems(ui.Baseline).Opacity(clockOpacity)
					slot.Children(func() { mobileClock(clockKey, *morph.origin.started, nil, font, sweep) })
				})
			} else if chevronOpacity < 1 {
				ui.Row(line.Key("arrow-slot")).Opacity(chevronOpacity).Children(func() {
					paintMobileToolChevron(line.Key("arrow"), title, font, -90*(1-progress), sweep)
				})
			} else {
				paintMobileToolChevron(line.Key("arrow"), title, font, -90*(1-progress), sweep)
			}
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
	keepTrailingOutside := trailing.active && morph != nil && morph.keepStatus && group.Open && progress < 1
	if delta := toolResultPanel(c.Key("body"), group.Open, target != "", &progress, func(c *ui.Context) {
		ui.Box(c.Key("heading-clearance")).Height(mobileToolRowGap - (transcriptMetadataHeight - 20)).Shrink(0)
		for index, tool := range tools {
			if domain.HistorySpacer(tool) {
				continue
			}
			if closingMorph && !morph.openIDs[tool.ID] {
				continue
			}
			row := c.Key(tool.ID)
			entrance := arrivals.progress(c.Now(), tool.ID)
			if entrance < 1 {
				// The measured row already moves the followed viewport.
				list.FollowAnimation = nil
				c.AnimationFrame()
			}
			leadMorphP := float32(0)
			if morph != nil && morphP > 0 && morph.openIDs[tool.ID] {
				if closingMorph {
					leadMorphP = 1
				} else {
					leadMorphP = morphP
				}
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
						x := bounds.X + mobileToolTimelineInset*(1-leadMorphP) + mobileToolGlyphSlot/2 - .5
						if index > 0 && center > bounds.Y+9 {
							painter.Fill(ui.Rect{X: x, Y: bounds.Y, W: 1, H: center - bounds.Y - 9}, p.line, 0)
						}
						if index+1 < len(tools) && bounds.Y+bounds.H > center+9 {
							painter.Fill(ui.Rect{X: x, Y: center + 9, W: 1, H: bounds.Y + bounds.H - center - 9}, p.line, 0)
						}
					})
				}
				call.Children(func() {
					if domain.ReasoningSummary(tool) {
						fresh := freshReasoning != nil && freshReasoning(tool.ID)
						heading = mobileToolReasoning(row, session.ID, tool, font, mark, fresh, leadMorphP)
						return
					}
					body := timeline.bodies[tool.ID]
					if body == nil {
						body = new(bool)
						timeline.bodies[tool.ID] = body
					}
					heading = a.toolItem(row, parent, session, tool, body, mark, true, list, keepPosition)
				})
			})
		}
		// Keep the outgoing status in the measured stack until the clip closes.
		// Removing its height on click would move the bounded scroll anchor early.
		if trailing.active && !keepTrailingOutside {
			row := c.Key("progress")
			statusOpacity := progress
			if morph != nil && morph.keepStatus && group.Open {
				statusOpacity = 1
			}
			status := ui.Row(row).MinHeight(lineHeight).AlignSelf(ui.Start).Gap(mobileToolSummaryGap).AlignItems(ui.Baseline).Opacity(statusOpacity)
			sweep := newMobileToolSweep(row, status, sweepStart)
			status.Children(func() {
				mobileToolStatusDot(row, trailing.phase, font, sweep)
				mobileToolPhase(row, trailing.phase, trailing.started, sweep)
			})
		}
	}, list.Bounds(c).H); delta != 0 && list != nil {
		list.FollowAnimation = nil
	}
	if keepTrailingOutside {
		row := c.Key("morph-progress")
		status := ui.Row(row).MarginTop(transcriptItemGap).MinHeight(transcriptMetadataHeight).AlignSelf(ui.Start).Gap(mobileToolSummaryGap).AlignItems(ui.Baseline)
		sweep := newMobileToolSweep(row, status, sweepStart)
		status.Children(func() {
			mobileToolStatusDot(row, trailing.phase, font, sweep)
			mobileToolPhase(row, trailing.phase, trailing.started, sweep)
		})
	}
}

func mobileToolReasoning(c *ui.Context, sessionID string, item domain.Item, font ui.Font, mark *markdown.RangeDecoration, fresh bool, morphP ...float32) ui.Element {
	body := conversationui.ReasoningSummaryText(item)
	firstLine, _, _ := strings.Cut(body, "\n")
	rs := ui.Local(c.Root(), "reasoning-state:"+sessionID+":"+item.ID, func() mobileReasoningState { return mobileReasoningState{} })
	p := float32(0)
	if len(morphP) > 0 {
		p = morphP[0]
	}
	text := mobileReasoningStreamText(c.Key("reasoning"), &rs.stream, body, !item.Final, fresh, palette(c).secondary, font, mobileStatusLineHeight(font), mark).
		FillWidth().MinWidth(0).MarginStart(mobileToolTextColumn)
	if p > 0 {
		const indentDelta = mobileToolTextColumn - (mobileToolGlyphSlot + 6)
		text.Transform(ui.Translation(-indentDelta*p, 0))
	}
	if body != "" {
		glyph := mobileToolGlyph(c.Key("timeline-icon"), toolThinkingSymbol, firstLine, font).Absolute().Left(mobileToolTimelineInset).Top(0)
		if p > 0 {
			glyph.Opacity(max(0, (.55-p)/.55))
		}
	}
	return text
}

func mobileToolGlyph(c *ui.Context, symbol *ui.SVG, title string, font ui.Font) ui.Element {
	return transcriptSymbol(c, symbol, title, font, mobileToolIconSlot, palette(c).secondary)
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
		Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Ease: ui.EaseOut, Colors: true,
			Enter: &ui.Motion{Y: 6, Opacity: 0}, Exit: &ui.Motion{Y: 6, Opacity: 0}}).
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
	button.Width(shown)
	if pane := list.Bounds(c); pane.W > 0 {
		button.MaxWidth(max(mobileTapSize(c), pane.W-2*pageInset))
	}
	button.Children(func() {
		ink := p.secondary
		lineHeight := mobileStatusLineHeight(font)
		chip := ui.Row(c.Key("chip")).FillWidth().Height(max(28, lineHeight+6)).PaddingX(padding).
			AlignItems(ui.Center).Justify(ui.Center).Radius(14).Background(p.floating).Border(border, p.floatingEdge).
			PassThrough().Transition(ui.ElementTransition{Colors: true, Duration: mobilePanelTransition.Duration, Ease: ui.EaseOut})
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

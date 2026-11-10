package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

type workReadPhase uint8

const (
	workUnread workReadPhase = iota
	workReading
	workRead
	workReadFailed
)

type workDetailUI struct {
	hydrated          bool
	readSequence      int64
	streams           map[string]*replyStream
	streamEpoch       string
	latestOrder       int64
	phase             workReadPhase
	error             string
	epoch, source     string
	generation        uint64
	scroll            n.ScrollState
	following, placed bool
	target            float32
	lastY             float32
	stopping          bool
	markdown          markdown.Options
	markdownTheme     markdown.Theme
}
type workPanelUI struct {
	selected string
	list     n.ListState
	arrivals map[string]time.Time
	details  map[string]*workDetailUI
}

func (v *View) workPanel(kind string) *workPanelUI {
	if v.workPanels == nil {
		v.workPanels = map[app.WorkKey]*workPanelUI{}
	}
	key := v.Model.WorkKey(v.Model.SessionID, kind)
	if p := v.workPanels[key]; p != nil {
		return p
	}
	p := &workPanelUI{details: map[string]*workDetailUI{}}
	v.workPanels[key] = p
	return p
}
func (p *workPanelUI) detail(id string) *workDetailUI {
	if d := p.details[id]; d != nil {
		return d
	}
	d := &workDetailUI{following: true}
	p.details[id] = d
	return d
}
func workStateLabel(w domain.WorkActivity) string {
	switch w.State {
	case "starting":
		return "启动中"
	case "running":
		return "进行中"
	case "waiting":
		return "等待中"
	case "completed":
		return "已完成"
	case "failed":
		return "执行失败"
	case "stopped":
		return "已停止"
	}
	return "状态未知"
}
func (v *View) displayedWork(w domain.WorkActivity) domain.WorkActivity {
	if w.Active() && (!v.Model.Connected || w.OriginRef != "" && w.OriginRef != v.Model.Details[v.Model.SessionID].Session.ThreadID) {
		w.State = "unknown"
		w.CanStop = false
	}
	return w
}
func (v *View) workStatus(c *n.Context, t *n.Theme, w domain.WorkActivity, label string, font n.Font) {
	// A shared slot keeps both the first-line text and detail caption stationary
	// when a small dot is replaced by a question, shield or completion check.
	n.Row(c.Key("work-status")).Width(14).Height(20).Shrink(0).Justify(n.Center).AlignItems(n.Baseline).Children(func() {
		if r := v.pendingWorkRequest(w); r != nil {
			icon := questionIcon
			if r.Kind == "approval" || r.Kind == "permissions" {
				icon = shieldIcon
			}
			inlineSymbol(c, icon, label, font, 14, t.TextMuted)
		} else if w.Active() {
			statusDot(c, label, font, t.Success, true)
		} else if w.State == "completed" || w.State == "stopped" {
			inlineSymbol(c, check, label, font, 14, t.TextMuted)
		} else {
			statusDot(c, label, font, t.TextMuted, false)
		}
	})
}
func workDuration(w domain.WorkActivity, now time.Time) string {
	ms := int64(-1)
	if w.DurationMS != nil {
		ms = *w.DurationMS
	} else if w.StartedAt != nil {
		end := now
		if w.CompletedAt != nil {
			end = *w.CompletedAt
		} else if !w.Active() {
			return ""
		}
		ms = end.Sub(*w.StartedAt).Milliseconds()
	}
	if ms < 0 {
		return ""
	}
	s := ms / 1000
	if s >= 60 {
		return fmt.Sprintf("%d分%02d秒", s/60, s%60)
	}
	return fmt.Sprintf("%d秒", s)
}
func (v *View) sessionWorkBody(c *n.Context, t *n.Theme, kind string, all []domain.WorkActivity, width float32) {
	p := v.workPanel(kind)
	works := []domain.WorkActivity{}
	sourceKind := "agent"
	if kind == "background" {
		sourceKind = "background"
	}
	var selected domain.WorkActivity
	for _, w := range all {
		if w.Kind == sourceKind {
			w = v.displayedWork(w)
			works = append(works, w)
			if w.ID == p.selected {
				selected = w
			}
		}
	}
	if p.selected != "" && selected.ID == "" {
		for _, r := range v.Model.Details[v.Model.SessionID].Requests {
			if r.WorkID == p.selected {
				selected = v.displayedWork(v.requestWork(p.selected))
				break
			}
		}
		if selected.ID == "" {
			p.selected = ""
		}
	}
	index := 0
	if p.selected != "" {
		index = 1
	}
	configurationTabPanel(c.Key("work-navigation:"+kind), fmt.Sprint(index), index, configurationPanelFill, func() {
		if index == 1 {
			v.sessionWorkDetail(c, t, kind, p, selected, width)
			return
		}
		if v.Model.Details[v.Model.SessionID].WorksTruncated {
			n.Text(c, "仅显示最近的工作记录").FontSize(11).TextColor(t.TextMuted).Padding(4, 12)
		}
		if len(works) == 0 {
			label := "暂无子 Agent"
			if kind == "background" {
				label = "暂无后台任务"
			}
			n.Text(c, label).FontSize(12).TextColor(t.TextMuted).Padding(12)
			return
		}
		if p.arrivals == nil {
			p.arrivals = map[string]time.Time{}
			for _, w := range works {
				p.arrivals[w.ID] = time.Time{}
			}
		} else {
			for _, w := range works {
				if _, seen := p.arrivals[w.ID]; !seen {
					p.arrivals[w.ID] = c.Now()
				}
			}
		}
		p.list.Key = func(i int) any { return works[i].ID }
		n.List(c.Key("work-list:"+kind), &p.list, len(works), func(i int) {
			w := works[i]
			name := "查看" + inspectorWorkName(kind) + "：" + w.Title
			progress := float32(1)
			if at := p.arrivals[w.ID]; !at.IsZero() && !c.Preferences().ReduceMotion {
				progress = n.EaseOut(min(1, float32(c.Now().Sub(at))/float32(180*time.Millisecond)))
				if progress < 1 {
					c.AnimationFrame()
				}
			}
			row := control(c, 5).FillWidth().Height(60*progress).Opacity(progress).Clip().Padding(8, 6).Label(name)
			row.Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Start).Children(func() {
					v.workStatus(c, t, w, w.Title, n.Font{Family: t.Font, Size: 12, Weight: 400})
					n.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
							title := n.Text(c, w.Title).Grow(1).MinWidth(0).SingleLine().FontSize(12).FixedLineHeight(20).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
							if w.Active() && v.pendingWorkRequest(w) == nil {
								shimmer(c, title, t.TextMuted, t.Text)
							}
							v.workRequestLink(c, t, w)
						})
						if r := v.pendingWorkRequest(w); r != nil {
							w.Latest = r.Text
							if len(r.Questions) > 0 {
								w.Latest = r.Questions[0].Text
							}
						}
						if w.Latest != "" {
							n.Text(c, w.Latest).FillWidth().SingleLine().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
						}
					})
					if duration := workDuration(w, c.Now()); duration != "" {
						n.Text(c, duration).Font("Menlo").FontSize(10).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)})
					}
					inlineSymbol(c, disclosureRight, w.Title, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.TextMuted).Shrink(0)
				})
			})
			if row.Clicked() {
				p.selected = w.ID
				v.readSessionWork(w, p.detail(w.ID), false)
			}
		}).FillWidth().FillHeight().Grow(1).PaddingX(6)
	}, false)
}
func (v *View) readSessionWork(w domain.WorkActivity, u *workDetailUI, force bool) {
	if w.Revision == 0 && !force || !v.Model.Connected || v.Controller == nil {
		return
	}
	sid := v.Model.SessionID
	source := v.Model.Details[sid].Session.ThreadID
	epoch := v.Model.ConnectionEpoch
	if u.epoch != epoch || u.source != source {
		u.phase = workUnread
		u.hydrated = false
		u.streams = nil
		u.epoch = epoch
		u.source = source
		u.generation++
	}
	_, cached := v.Model.WorkDetails[v.Model.WorkKey(sid, w.ID)]
	if u.phase == workReading || !force && (u.phase == workReadFailed || u.phase == workRead && cached) {
		return
	}
	v.splitRoot().Model.BeginWorkReadKey(v.Model.WorkKey(sid, w.ID))
	hydrate := !u.hydrated
	readSequence := v.Model.Sequence
	if hydrate {
		u.readSequence = readSequence
	}
	u.phase = workReading
	u.error = ""
	u.generation++
	generation := u.generation
	if !v.do(app.Job{Method: "session.work", Input: protocol.WorkQuery{SessionID: sid, ID: w.ID}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if u.generation != generation || m.ConnectionEpoch != epoch || m.Details[sid].Session.ThreadID != source {
			return
		}
		if err != nil {
			u.phase = workReadFailed
			u.error = err.Error()
			return
		}
		var detail domain.WorkDetail
		if err = json.Unmarshal(b, &detail); err != nil {
			u.phase = workReadFailed
			u.error = err.Error()
			return
		}
		if detail.SessionID != sid || detail.Work.ID != w.ID {
			u.phase = workReadFailed
			u.error = "工作详情不匹配"
			return
		}
		observed := map[string]bool{}
		if hydrate {
			for _, entry := range m.WorkDetails[m.WorkKey(sid, w.ID)].Items {
				if entry.Revision > readSequence {
					observed[entry.Item.ID] = true
				}
			}
		}
		m.MergeWorkDetail(detail)
		if hydrate {
			u.seedHistory(m.WorkDetails[m.WorkKey(sid, w.ID)], observed, epoch)
		}
		u.hydrated = true
		u.phase = workRead
	}}) {
		u.phase = workUnread
	}
}
func (v *View) refreshSelectedWork() {
	for _, kind := range []string{"agents", "background"} {
		p := v.workPanel(kind)
		for _, w := range domain.SessionWorks(v.Model.Details[v.Model.SessionID]) {
			if w.ID == p.selected {
				v.readSessionWork(w, p.detail(w.ID), true)
			}
		}
	}
}
func (v *View) sessionWorkDetail(c *n.Context, t *n.Theme, kind string, p *workPanelUI, w domain.WorkActivity, width float32) {
	u := p.detail(w.ID)
	v.readSessionWork(w, u, false)
	detail, exists := v.Model.WorkDetails[v.Model.WorkKey(v.Model.SessionID, w.ID)]
	if exists && detail.Work.Brief != "" {
		w.Brief = detail.Work.Brief
	}
	n.Row(c.Key("work-detail-header")).FillWidth().Shrink(0).Height(36).PaddingX(12).Gap(6).AlignItems(n.Center).Children(func() {
		back := n.ButtonBase(c.Key("work-detail-back")).Height(28).Grow(1).MinWidth(0).Justify(n.Start).Label("返回" + inspectorWorkName(kind) + "列表").Description(w.Title).Tooltip(w.Title)
		color := textHoverColor(c, back, t.TextMuted)
		back.Children(func() {
			inlineIconText(c, workBackIcon, w.Title, n.Font{Family: t.Font, Size: 12, Weight: 400}, 16, 0, color).TextColor(t.Text).MinWidth(0).Grow(1)
		})
		if back.Clicked() {
			p.selected = ""
		}
		n.Row(c.Key("work-detail-state")).Height(20).Gap(6).AlignItems(n.Baseline).Children(func() {
			text := v.workLabel(w)
			font := n.Font{Family: t.Font, Size: 11, Weight: 400}
			v.workStatus(c, t, w, text, font)
			label := n.Text(c, text).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
			if w.Active() && v.pendingWorkRequest(w) == nil {
				shimmer(c, label, t.TextMuted, t.Text)
			}
		})
	})
	if u.placed && !u.scroll.Scrolling() && u.scroll.Y != u.lastY {
		u.following = u.scroll.MaxY-u.scroll.Y < 24
	}
	port := n.Scroll(c.Key("work-detail-output:" + w.ID)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.scroll).Label(inspectorWorkName(kind) + "内容")
	terminalTheme := sshTerminalLight
	if t.Dark {
		terminalTheme = sshTerminalDark
	}
	outputColor := t.TextMuted
	outputFont := v.Model.Preferences.CodeFont
	if kind == "background" {
		port.Width(width-24).Margin(0, 12).Radius(6).Background(terminalTheme.Background).Border(1, t.Border)
		outputColor = terminalTheme.Foreground
		outputFont += ", monospace"
	}

	u.syncStreams(c, detail, v.Model.ConnectionEpoch)
	u.markdownTheme = *v.md.Theme
	u.markdownTheme.FontSize = 12
	u.markdownTheme.LineHeight = 20.0 / 12
	u.markdownTheme.CodeSize = 11.0 / 12
	u.markdownTheme.BlockSpacing = 8.0 / 12
	u.markdown.Theme = &u.markdownTheme
	u.markdown.WrapCode = true
	port.Children(func() {
		n.Column(c).FillWidth().Padding(8, 12, 12, 12).Gap(12).Children(func() {
			metadata := []string{}
			if w.Model != "" {
				metadata = append(metadata, w.Model)
			}
			if w.Effort != "" {
				metadata = append(metadata, w.Effort)
			}
			if duration := workDuration(w, c.Now()); duration != "" {
				metadata = append(metadata, duration)
			}
			if len(metadata) > 0 {
				n.Text(c, strings.Join(metadata, " · ")).FontSize(11).TextColor(t.TextMuted)
			}
			if w.Brief != "" {
				if kind == "background" {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						inlineSymbol(c, terminalIcon, w.Brief, n.Font{Family: outputFont, Size: 11}, 14, outputColor)
						n.Text(c, w.Brief).Grow(1).MinWidth(0).Font(outputFont).FontSize(11).FixedLineHeight(18).TextColor(outputColor).Selectable()
					})
				} else {
					n.Text(c, w.Brief).AlignSelf(n.End).MaxWidth(width-24).FontSize(12).FixedLineHeight(20).Padding(8, 10).Radius(6).Background(userBubble(t)).Selectable()
				}
			}
			if detail.Truncated {
				n.Text(c, "仅显示最近的输出").FontSize(11).TextColor(t.TextMuted)
			}
			if !exists || len(detail.Items) == 0 {
				label := "暂无输出"
				if w.Revision == 0 {
					label = "此历史记录仅包含摘要"
				} else if u.phase == workReading {
					label = "读取中…"
				}
				n.Text(c, label).FontSize(12).TextColor(t.TextMuted)
			}
			for _, entry := range detail.Items {
				item := entry.Item
				stream := u.streams[item.ID]
				if item.Kind == "assistant" && stream != nil && stream.live() && !c.Preferences().ReduceMotion && c.Now().Before(stream.revealAfter) {
					c.AnimationFrame()
					continue
				}
				n.Column(c.Key("work-item:" + item.ID)).FillWidth().Gap(6).Transition(resultBlockMotion(c)).Children(func() {
					if item.Kind == "assistant" || item.Kind == "reasoning" {

						initialized := n.Local(n.Column(c.Key("mount")), "seeded", func() bool { return false })
						streaming := stream != nil && stream.live() && (!item.Final || stream.changedAt.Equal(c.Now()) || stream.fresh())
						if !*initialized && (stream == nil || !stream.fresh()) {
							streaming = false
						}
						*initialized = true
						if stream != nil {
							stream.mount()
						}
						if c.Preferences().ReduceMotion || stream == nil || !stream.live() {
							u.markdown.Reveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}
						} else {
							u.markdown.Reveal = &markdown.Reveal{Unit: markdown.ByCharacter, Fade: 180 * time.Millisecond, Smooth: true}
						}
						if streaming {
							markdown.Stream(c.Key("body"), item.Text, &u.markdown).FillWidth().FontSize(12)
						} else {
							markdown.View(c.Key("body"), item.Text, &u.markdown).FillWidth().FontSize(12)
						}
					} else if item.Kind == "user" {
						n.Text(c, item.Text).AlignSelf(n.End).MaxWidth(width-24).FontSize(12).FixedLineHeight(20).Padding(8, 10).Radius(6).Background(userBubble(t)).Selectable()
					} else if item.Kind == "request" {
						v.requestSource(c, t, item, true)
					} else if item.Kind == "request_result" {
						v.requestResultWithStream(c, t, item, stream, false)
					} else if item.Presentation != "command" {
						item.ID = w.ID + "\x00" + item.ID
						v.activityItem(c, t, item, false)
					} else {
						if item.Name != "" && (kind != "background" || w.Brief == "") {
							inlineIconText(c, backgroundWorkIcon, item.Name, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted)
						}
						if item.Text != "" {
							resultText(c.Key("output"), strings.TrimRight(item.Text, "\r\n"), outputColor, func(e n.Element) { e.Font(outputFont).FontSize(11).FixedLineHeight(18).Selectable() })
						}
						for i, part := range item.Parts {
							if part.Text != "" && part.Text != item.Text {
								resultText(c.Key(fmt.Sprint("part", i)), part.Text, t.TextMuted, func(e n.Element) { e.FontSize(12).Selectable() })
							}
						}
					}
				})
			}
			if u.error != "" {
				n.Text(c, u.error).FontSize(11).TextColor(t.TextMuted)
				if control(c).Padding(4, 8).Children(func() { n.Text(c, "重试").FontSize(12) }).Clicked() {
					v.readSessionWork(w, u, true)
				}
			}
		})
	})
	animation := motionAnimation(c, 160*time.Millisecond)
	invalidate := c.Services().Invalidate
	port.DrawOver(func(_ *n.Painter, _ n.Rect) {
		u.lastY = u.scroll.Y
		if !u.following {
			return
		}
		target := u.scroll.MaxY
		if !u.placed || target != u.target {
			u.placed = true
			u.target = target
			u.scroll.ScrollTo(0, target, animation)
			invalidate()
		}
	})
	n.Row(c.Key("work-detail-actions")).FillWidth().Shrink(0).Height(36).PaddingX(12).Gap(8).AlignItems(n.Center).Children(func() {
		if w.SourceItemID != "" {
			if control(c).Padding(3, 5).Children(func() {
				inlineIconText(c, locateWorkIcon, "定位来源", n.Font{Family: t.Font, Size: 11}, 12, 5, t.TextMuted)
			}).Clicked() {
				v.locateWorkSource(c, w.SourceItemID)
			}
		}
		n.Spacer(c).Grow(1)
		if !u.following && u.scroll.MaxY > 0 {
			if control(c).Padding(3, 5).Label("跟随最新输出").Children(func() {
				inlineIconText(c, workDownIcon, "最新输出", n.Font{Family: t.Font, Size: 11}, 12, 5, t.TextMuted)
			}).Clicked() {
				u.following = true
				u.target = -1
			}
		}
		if kind == "background" {
			if iconButton(c, copyReplyIcon, "复制后台输出").Clicked() {
				parts := []string{}
				for _, entry := range detail.Items {
					if entry.Item.Text != "" {
						parts = append(parts, entry.Item.Text)
					}
				}
				c.WriteClipboard(strings.Join(parts, "\n\n"))
			}
		}
		if w.CanStop && w.Active() {
			if control(c).Padding(3, 6).Disabled(!v.Model.Connected || u.stopping).Label("停止" + inspectorWorkName(kind)).Children(func() { n.Text(c, "停止").FontSize(11).TextColor(t.TextMuted) }).Clicked() {
				u.stopping = true
				sid := v.Model.SessionID
				if !v.do(app.Job{Method: "session.work.stop", Input: protocol.WorkQuery{SessionID: sid, ID: w.ID}, Apply: func(_ *app.Model, _ json.RawMessage, err error) {
					u.stopping = false
					if err != nil {
						u.error = err.Error()
					}
				}}) {
					u.stopping = false
				}
			}
		}
	})
}

var locateWorkIcon = svg("M10 3v3M10 14v3M3 10h3M14 10h3M10 6a4 4 0 1 0 0 8 4 4 0 0 0 0-8")
var workDownIcon = svg("M10 3v13M5 11l5 5 5-5")

func (v *View) locateWorkSource(c *n.Context, id string) {
	state := v.lists[v.Model.SessionID]
	if state == nil {
		return
	}
	items := v.Model.Details[v.Model.SessionID].Items
	target := -1
	for i, item := range items {
		if item.ID == id {
			target = i
			break
		}
	}
	if target < 0 {
		return
	}
	for i, row := range v.transcriptRows(items) {
		if row.item.ID == id {
			state.ScrollToRow(i+1, 112, motionAnimation(c, 240*time.Millisecond))
			return
		}
		if row.group != "" && row.end >= target {
			start := -1
			for j, item := range items {
				if item.ID == row.item.ID {
					start = j
					break
				}
			}
			if start >= 0 && start <= target {
				state.ScrollToRow(i+1, 112, motionAnimation(c, 240*time.Millisecond))
				return
			}
		}
	}
}

var workBackIcon = svg("M12 4l-6 6 6 6")

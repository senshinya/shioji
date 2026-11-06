package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

type requestPage struct {
	request  domain.PendingRequest
	question int
}

func (p requestPage) key() string {
	if p.question >= 0 {
		return p.request.ID + "/" + p.request.Questions[p.question].ID
	}
	return p.request.ID
}

type requestPageMotion struct {
	current   requestPage
	previous  *requestPage
	index     int
	height    float32
	measured  bool
	started   time.Time
	direction float32
	immediate bool
}
type requestPageMotionKey struct{}

func (v *View) pendingBody(c *n.Context, t *n.Theme, current requestPage, index int, collapsed, entering bool) float32 {
	frame := n.Column(c.Key("request-body")).FillWidth().Clip().Label("请求内容区域")
	state := n.Local(frame, requestPageMotionKey{}, func() requestPageMotion {
		return requestPageMotion{current: current, index: index, direction: 1, immediate: v.dockHeight == 0 || entering}
	})
	if state.current.key() != current.key() {
		old := state.current
		state.previous = &old
		state.direction = 1
		if index < state.index {
			state.direction = -1
		}
		state.current, state.index = current, index
		state.measured = false
		state.started = time.Time{}
	}
	state.current = current
	animation := motionAnimation(c, 240*time.Millisecond)
	progress := float32(1)
	if state.previous != nil {
		if !state.measured {
			progress = 0
		} else {
			if state.started.IsZero() {
				state.started = c.Now()
			}
			progress = min(1, float32(c.Now().Sub(state.started))/float32(animation.Duration))
			if c.Preferences().ReduceMotion {
				progress = 1
			}
			progress = animation.Ease(max(0, progress))
		}
		if progress >= 1 {
			state.previous = nil
		} else {
			c.AnimationFrame()
		}
	}
	target := state.height
	if collapsed {
		target = 0
	}
	shown := frame.AnimateTo("request-height", target, animation)
	frame.Height(shown).IgnoreInput(collapsed).Disabled(collapsed)
	distance := frame.LayoutBounds().W * state.direction
	if c.Preferences().ReduceMotion {
		distance = 0
	}
	build := func(page requestPage, old bool) {
		key := "page:" + page.key()
		if old {
			key = "outgoing:" + page.key()
		}
		content := n.Column(c.Key(key)).FillWidth().Shrink(0).Padding(8, 0, 0, 0).Gap(8)
		if old {
			content.Absolute().Left(0).Right(0).Top(0).IgnoreInput(true).Disabled(true).Transform(n.Translation(-distance*progress, 0))
		} else if state.previous != nil {
			content.Transform(n.Translation(distance*(1-progress), 0))
		}
		content.Children(func() {
			n.Scroll(c.Key("fields:" + page.key())).FillWidth().MaxHeight(questionBodyHeight(c)).Children(func() {
				n.Column(c).FillWidth().Gap(8).Children(func() { v.requestBody(c, t, page.request, page.question) })
			})
			v.requestActions(c, t, page.request)
		})
		if old {
			return
		}
		// Read the untransformed, unclipped natural box. Unlike an element
		// transition, this actual height participates in the dock's layout.
		height := content.LayoutBounds().H
		if height > 0 {
			if !state.measured || math.Abs(float64(height-state.height)) > .5 {
				if state.immediate && state.height == 0 && !collapsed {
					frame.Jump("request-height", height)
					shown = height
					frame.Height(shown)
				}
				state.height, state.measured = height, true
				c.Services().Invalidate()
			}
		} else {
			c.Services().Invalidate()
		}
	}
	frame.Children(func() {
		if state.previous != nil {
			build(*state.previous, true)
		}
		build(current, false)
	})
	return shown
}

// Keep the keyed card mounted while its actual dock height closes. Snapshot
// only its presentation; answered requests never remain actionable.
type requestCardMotion struct {
	pages       []requestPage
	index       int
	known       map[string]bool
	unread      map[string]bool
	active      bool
	noticeUntil time.Time
	noticeCount int
	noticeKind  string
}
type requestCardMotionKey struct{}

func (v *View) pending(c *n.Context, t *n.Theme, detail domain.SessionDetail) {
	v.pruneSecretAnswers(detail)
	var pages []requestPage
	for _, r := range detail.Requests {
		if r.State != "pending" && r.State != "submitting" {
			continue
		}
		if len(r.Questions) == 0 {
			pages = append(pages, requestPage{r, -1})
		} else {
			for i := range r.Questions {
				pages = append(pages, requestPage{r, i})
			}
		}
	}
	m := v.Model
	card := n.Column(c.Key("pending:"+m.SessionID)).FillWidth().MaxWidth(768).Label("待处理请求卡片").Radius(requestRadius).Background(t.Surface).Border(1, t.Border).Clip()
	state := n.Local(card, requestCardMotionKey{}, func() requestCardMotion {
		known := map[string]bool{}
		for _, page := range pages {
			known[page.key()] = true
		}
		return requestCardMotion{known: known, unread: map[string]bool{}, active: len(pages) > 0}
	})
	active := len(pages) > 0
	if active && !state.active {
		list := v.lists[m.SessionID]
		composing := v.composerHadFocus && (strings.TrimSpace(m.Drafts[m.DraftKey()]) != "" || len(m.Attachments[m.DraftKey()]) > 0)
		// New requests remain available without opening over history reading
		// or an ordinary draft. Empty, bottom-following composers may open.
		if composing || list != nil && !list.FollowingEnd() {
			m.Collapsed[m.SessionID] = true
		}
	}
	if active {
		index := min(max(0, m.Pager[m.SessionID]), len(pages)-1)
		// Removal of an earlier request must not move the reader to a different
		// question. An explicit pager action still takes priority.
		if len(state.pages) > 0 && m.Pager[m.SessionID] == state.index {
			for i, page := range pages {
				if page.key() == state.pages[state.index].key() {
					index = i
					break
				}
			}
		}
		known := map[string]bool{}
		for _, page := range pages {
			key := page.key()
			known[key] = true
			if state.active && !state.known[key] {
				state.unread[key] = true
				state.noticeUntil = c.Now().Add(4 * time.Second)
			}
		}
		for key := range state.unread {
			if !known[key] {
				delete(state.unread, key)
			}
		}
		if !m.Collapsed[m.SessionID] {
			delete(state.unread, pages[index].key())
		}
		state.known, state.pages, state.index = known, pages, index
		m.Pager[m.SessionID] = index
	} else {
		clear(state.unread)
		clear(state.known)
	}
	state.active = active
	target := float32(0)
	if active {
		target = 1
	}
	animation := n.Animation{Duration: 320 * time.Millisecond, Ease: n.CubicBezier(.2, 0, 0, 1)}
	presence := card.AnimateTo("presence", target, animation)
	card.Transform(n.Translation(0, 8*(1-presence)))
	n.Inert(card, !active)
	// The dock owns an 8px gap. Include it in this same progress so there is
	// no last-frame jump when the closed card leaves the layout.
	card.Margin(0, 0, -8*(1-presence), 0).Hidden(presence == 0)
	if len(state.pages) == 0 || (!active && presence == 0) {
		state.pages = nil
		card.Height(0)
		return
	}
	current, index := state.pages[state.index], state.index
	r := current.request
	unread, first := 0, -1
	kind := "问题"
	for i, page := range state.pages {
		if state.unread[page.key()] {
			unread++
			if page.question < 0 {
				kind = "请求"
			}
			if first == -1 {
				first = i
			}
		}
	}
	noticeTarget := float32(0)
	if unread > 0 {
		state.noticeCount, state.noticeKind = unread, kind
		if c.Now().Before(state.noticeUntil) {
			noticeTarget = 92
			c.After(state.noticeUntil.Sub(c.Now()))
		}
	}
	noticeWidth := card.AnimateTo("notice-width", noticeTarget, motionAnimation(c, 240*time.Millisecond))
	if noticeTarget == 0 && noticeWidth == 0 {
		clear(state.unread)
		state.noticeCount = 0
	}
	bodyHeight := float32(0)
	var content n.Element
	card.Children(func() {
		content = n.Column(c.Key("request-card-content")).FillWidth().Shrink(0)
		content.Children(func() {
			n.Row(c.Key("request-header")).FillWidth().Height(24).Shrink(0).Gap(4).AlignItems(n.Center).Children(func() {
				titleText, summary := r.Title, ""
				if m.Collapsed[m.SessionID] {
					summary = fmt.Sprintf("%d 个问题", len(state.pages))
					for _, page := range state.pages {
						if page.question < 0 {
							titleText, summary = "待处理", fmt.Sprintf("%d 项", len(state.pages))
							break
						}
					}
				}
				title := control(c, 4).Height(24).Shrink(0).PaddingX(2).Justify(n.Start).Label(r.Title).Description(titleText + " " + summary).Children(func() {
					n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
						icon := questionIcon
						if r.Kind == "approval" || r.Kind == "permissions" {
							icon = shieldIcon
						}
						font := n.Font{Family: "system-ui", Size: 12, Weight: 500}
						inlineSymbol(c, icon, r.Title, font, 14, t.TextMuted)
						n.Text(c, titleText).FontSize(12).FontWeight(500).FixedLineHeight(20)
						angle := float32(0)
						if m.Collapsed[m.SessionID] {
							angle = -90
						}
						inlineSymbol(c, disclosureDown, r.Title, font, 11, t.TextMuted, angle)
					})
				})
				if title.Clicked() {
					m.Collapsed[m.SessionID] = !m.Collapsed[m.SessionID]
				}
				n.Row(c).Grow(1).MinWidth(0)
				notice := n.Column(c.Key("request-arrival")).Width(noticeWidth).Height(24).Shrink(0).Clip().Hidden(noticeWidth == 0)
				n.Inert(notice, unread == 0)
				notice.Children(func() {
					label := fmt.Sprintf("新增 %d 个%s", state.noticeCount, state.noticeKind)
					button := n.ButtonBase(c).Width(92).Height(24).Shrink(0).Justify(n.End).Label("查看新增" + state.noticeKind)
					color := textHoverColor(c, button, t.TextMuted)
					button.Children(func() {
						n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(color).SingleLine()
					})
					if button.Clicked() && first >= 0 {
						m.Pager[m.SessionID], m.Collapsed[m.SessionID] = first, false
					}
				})
				unit := " 个问题"
				for _, page := range state.pages {
					if page.question < 0 {
						unit = " 项"
						break
					}
				}
				v.requestPager(c, t, index, len(state.pages), unit)
			})
			bodyHeight = v.pendingBody(c, t, current, index, m.Collapsed[m.SessionID], presence < 1)
		})
	})
	paddingTarget := float32(12)
	if m.Collapsed[m.SessionID] {
		paddingTarget = 4
	}
	padding := card.AnimateTo("request-padding", paddingTarget, motionAnimation(c, 240*time.Millisecond))
	content.Padding(padding, 14)
	card.Height((26 + 2*padding + bodyHeight) * presence)
}

// Keep one total glyph mounted while its surrounding words become a pager.
func (v *View) requestPager(c *n.Context, t *n.Theme, index, total int, unit string) {
	collapsed := v.Model.Collapsed[v.Model.SessionID]
	target := float32(0)
	if !collapsed && total > 1 {
		target = 1
	}
	row := n.Row(c.Key("request-pager")).Height(24).Shrink(0).Gap(0).AlignItems(n.Center).TextColor(t.TextMuted)
	progress := row.AnimateTo("expansion", target, motionAnimation(c, 240*time.Millisecond))
	label := fmt.Sprintf("%d%s", total, unit)
	if target == 1 {
		label = fmt.Sprintf("%d / %d", index+1, total)
	}
	row.Label(label)
	prefix := fmt.Sprintf("%d /", index+1)
	prefixWidth, _ := c.MeasureText(0, n.Span{Text: prefix, Font: "system-ui", Size: 11, Weight: 400, Features: "tnum"})
	suffixWidth, _ := c.MeasureText(0, n.Span{Text: unit, Font: "system-ui", Size: 11, Weight: 400})
	row.Children(func() {
		previous := n.Column(c.Key("previous")).Width(28 * progress).Height(24).Shrink(0).Clip().Hidden(progress == 0)
		n.Inert(previous, target == 0)
		previous.Children(func() {
			if ghost(c, "‹", requestRadius).Size(24, 24).Padding(0).Justify(n.Center).Label("上一个请求").Disabled(index == 0).Clicked() {
				v.Model.Pager[v.Model.SessionID]--
			}
		})
		n.Column(c.Key("prefix")).Width((prefixWidth + 4) * progress).Height(24).Shrink(0).Justify(n.Center).Clip().Hidden(progress == 0).Children(func() {
			n.Text(c, prefix).Width(prefixWidth).Shrink(0).FontSize(11).FontWeight(400).FontFeatures("tnum").FixedLineHeight(20).SingleLine()
		})
		n.Text(c.Key("total"), fmt.Sprint(total)).Shrink(0).FontSize(11).FontWeight(400).FontFeatures("tnum").FixedLineHeight(20).Label("待处理总数")
		n.Column(c.Key("suffix")).Width(suffixWidth * (1 - progress)).Height(24).Shrink(0).Justify(n.Center).Clip().Hidden(progress == 1).Children(func() {
			n.Text(c, unit).Width(suffixWidth).Shrink(0).FontSize(11).FontWeight(400).FixedLineHeight(20).SingleLine()
		})
		next := n.Column(c.Key("next")).Width(28 * progress).Height(24).Shrink(0).AlignItems(n.End).Clip().Hidden(progress == 0)
		n.Inert(next, target == 0)
		next.Children(func() {
			if ghost(c, "›", requestRadius).Size(24, 24).Padding(0).Justify(n.Center).Label("下一个请求").Disabled(index == total-1).Clicked() {
				v.Model.Pager[v.Model.SessionID]++
			}
		})
	})
}

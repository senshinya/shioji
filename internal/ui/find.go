package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/domain"
)

type findEntry struct {
	id, text string
	count    int
}
type findTarget struct {
	id         string
	start, end int
}
type conversationFind struct {
	open, focus        bool
	transient          bool
	query, compiled    string
	needle             string
	cache              map[string]findEntry
	entries            []findEntry
	total, current     int
	target             findTarget
	pending            *domain.SearchResult
	locate, positioned bool
	geometry           n.Rect
	version            uint64
	settle, started    time.Time
}

func (v *View) findState() *conversationFind {
	if v.finds == nil {
		v.finds = map[string]*conversationFind{}
	}
	sid := v.Model.SessionID
	if v.finds[sid] == nil {
		v.finds[sid] = &conversationFind{current: -1}
	}
	return v.finds[sid]
}

func (v *View) openConversationFind() {
	if v.Model.SessionID == "" || v.devices {
		return
	}
	s := v.findState()
	s.open, s.focus = true, true
	s.transient = false
	if s.target.id != "" {
		s.locate, s.positioned, s.started, s.geometry = true, false, time.Time{}, n.Rect{}
		s.version++
	}
}

func (v *View) closeConversationFind() {
	s := v.findState()
	s.open, s.transient = false, false
	s.version++
	v.focusComposer = true
}

func (v *View) refreshConversationFind() {
	for sid, s := range v.finds {
		if _, ok := v.Model.Details[sid]; !ok {
			s.entries, s.cache = nil, nil
		}
	}
	s := v.findState()
	if s.transient && !s.started.IsZero() && v.frameTime.Sub(s.started) >= 2580*time.Millisecond {
		s.transient = false
	}
	if !s.open && !s.transient {
		return
	}
	query := strings.TrimSpace(s.query)
	queryChanged := query != s.compiled
	changed := queryChanged
	if changed {
		s.compiled, s.needle, s.cache, s.target = query, "", nil, findTarget{}
		if query != "" && utf8.ValidString(query) && len(query) <= 256 {
			s.needle = strings.ToLower(query)
		}
	}
	if s.needle == "" {
		s.entries, s.cache, s.total, s.current, s.target = nil, nil, 0, -1, findTarget{}
		return
	}
	entries := make([]findEntry, 0)
	cache := map[string]findEntry{}
	for _, item := range v.Model.Details[v.Model.SessionID].Items {
		if item.Text == "" || (item.Kind != "user" && item.Kind != "assistant" && item.Kind != "tool" && item.Kind != "activity") {
			continue
		}
		entry, ok := s.cache[item.ID]
		if !ok || entry.text != item.Text {
			entry = findEntry{id: item.ID, text: item.Text, count: strings.Count(strings.ToLower(item.Text), s.needle)}
			changed = true
		}
		cache[item.ID] = entry
		if entry.count > 0 {
			entries = append(entries, entry)
		}
	}
	if len(cache) != len(s.cache) {
		changed = true
	}
	s.cache = cache
	if !changed && s.pending == nil {
		return
	}
	s.entries, s.total = entries, 0
	for _, e := range entries {
		s.total += e.count
	}
	index := 0
	if !changed || s.target.id != "" {
		index = -1
	}
	wanted := s.target
	if s.pending != nil {
		wanted = findTarget{id: s.pending.ItemID, start: s.pending.MatchStart, end: s.pending.MatchEnd}
	}
	if queryChanged && wanted.id == "" {
		index = 0
	}
	before := 0
	for _, e := range entries {
		if e.id == wanted.id {
			prefix := e.text[:min(max(wanted.start, 0), len(e.text))]
			index = before + min(strings.Count(strings.ToLower(prefix), s.needle), e.count-1)
			if s.pending != nil {
				s.pending = nil
			}
			break
		}
		before += e.count
	}
	if s.pending != nil {
		return
	} // A selected result may still be hydrating.
	if s.total == 0 {
		s.current, s.target = -1, findTarget{}
		return
	}
	if index >= 0 {
		v.selectFindMatch(index)
	} else {
		s.current, s.target = -1, findTarget{}
	}
}

func (v *View) selectFindMatch(index int) {
	s := v.findState()
	if s.total == 0 {
		return
	}
	index = (index + s.total) % s.total
	s.current = index
	for _, entry := range s.entries {
		if index >= entry.count {
			index -= entry.count
			continue
		}
		start, end := domain.FindLiteral(entry.text, s.compiled, index)
		if start < 0 {
			return
		}
		target := findTarget{id: entry.id, start: start, end: end}
		if target != s.target {
			s.target = target
			s.locate, s.positioned, s.started, s.geometry = true, false, time.Time{}, n.Rect{}
			s.version++
		}
		return
	}
}

func (v *View) stepConversationFind(direction int) {
	s := v.findState()
	s.pending = nil
	old := s.target
	index := s.current + direction
	if s.current < 0 && direction < 0 {
		index = s.total - 1
	}
	v.selectFindMatch(index)
	if s.target.id != "" && s.target == old {
		s.locate, s.positioned, s.started, s.geometry = true, false, time.Time{}, n.Rect{}
		s.version++
	}
}

func (v *View) positionFind(c *n.Context, state *n.ListState, rows []transcriptRow) {
	s := v.findState()
	if (!s.open && !s.transient) || s.target.id == "" {
		return
	}
	for i, row := range rows {
		if row.key != s.target.id {
			continue
		}
		if s.locate {
			if _, visible := v.historyRowBounds[row.key]; !visible {
				state.ScrollToRow(i+1, 112, motionAnimation(c, 240*time.Millisecond))
			}
			s.locate = false
		}
		if !s.positioned && s.geometry.H > 0 {
			if bounds, ok := v.historyRowBounds[row.key]; ok && bounds.H > 0 {
				y := float32(112) - (s.geometry.Y - bounds.Y)
				state.ScrollToRow(i+1, y, motionAnimation(c, 240*time.Millisecond))
				s.positioned, s.settle = true, c.Now().Add(260*time.Millisecond)
				c.AnimationFrame()
			}
		}
		return
	}
}

func (v *View) revealFindTarget(rows []transcriptRow, reopening bool) []transcriptRow {
	s := v.findState()
	if (!s.open && !s.transient) || s.target.id == "" || !s.locate && !reopening {
		return rows
	}
	items := v.Model.Details[v.Model.SessionID].Items
	target := -1
	for i, item := range items {
		if item.ID == s.target.id {
			target = i
			break
		}
	}
	if target < 0 {
		return rows
	}
	if items[target].Kind == "tool" {
		v.Model.Collapsed["tool:"+s.target.id] = false
	}
	if items[target].Kind == "tool" || items[target].Kind == "activity" {
		v.Model.Collapsed["activity:"+v.Model.SessionID+"/"+s.target.id] = false
	}
	for _, row := range rows {
		if row.group == "" || row.end < target {
			continue
		}
		start := -1
		for i, item := range items {
			if item.ID == row.item.ID {
				start = i
				break
			}
		}
		if start >= 0 && start <= target {
			v.Model.Collapsed[row.group], v.Model.Collapsed[row.group+":prose"] = false, true
			delete(v.toolMotions, row.group)
			return v.transcriptRows(items)
		}
	}
	return rows
}

func (v *View) findDecoration(item domain.Item, dark bool) *markdown.RangeDecoration {
	s := v.findState()
	if (!s.open && !s.transient) || s.target.id != item.ID || s.target.end > len(item.Text) || s.target.start < 0 || !s.open && !s.started.IsZero() && v.frameTime.Sub(s.started) >= 2580*time.Millisecond {
		return nil
	}
	version := s.version
	invalidate := v.services.Invalidate
	return &markdown.RangeDecoration{Start: s.target.start, End: s.target.end, Draw: func(p *n.Painter, box n.Rect) {
		if s.version != version {
			return
		}
		if !s.positioned && s.geometry.H == 0 {
			s.geometry = box
			invalidate()
			return
		}
		if !s.positioned {
			return
		}
		if p.Now().Before(s.settle) {
			p.AnimationFrame()
			return
		}
		if s.started.IsZero() {
			s.started = p.Now()
		}
		alpha := float32(1)
		if !s.open {
			elapsed := p.Now().Sub(s.started)
			if elapsed < 2180*time.Millisecond {
				p.After(2180*time.Millisecond - elapsed)
			} else {
				alpha = max(0, 1-float32(elapsed-2180*time.Millisecond)/float32(400*time.Millisecond))
				if alpha > 0 {
					p.AnimationFrame()
				}
			}
		}
		color := n.RGBA(255, 212, 56, .66*alpha)
		if dark {
			color = n.RGBA(255, 201, 38, .38*alpha)
		}
		p.Fill(n.Rect{X: box.X - 2, Y: box.Y - 1, W: box.W + 4, H: box.H + 2}, color, 3)
	}}
}

func (v *View) decoratePlainFind(c *n.Context, el n.Element, item domain.Item, scroll *n.ScrollState) {
	if decoration := v.findDecoration(item, c.Theme().Dark); decoration != nil {
		start, end := utf8.RuneCountInString(item.Text[:decoration.Start]), utf8.RuneCountInString(item.Text[:decoration.End])
		handle := n.Local(el, "find-text-handle", func() n.Handle { return n.Handle{} })
		el.Bind(handle)
		s := v.findState()
		version := s.version
		scrollVersion := n.Local(el, "find-scroll-version", func() uint64 { return 0 })
		animation := motionAnimation(c, 240*time.Millisecond)
		el.Draw(func(p *n.Painter, _ n.Rect) {
			if s.version != version {
				return
			}
			boxes := c.Resolve(*handle).TextRangeRects(start, end)
			if scroll != nil && len(boxes) > 0 {
				if *scrollVersion != version {
					viewport := scroll.Bounds(c)
					if viewport.H <= 0 {
						return
					}
					*scrollVersion = version
					r := boxes[0]
					delta := float32(0)
					if r.Y < viewport.Y+4 {
						delta = r.Y - viewport.Y - 4
					} else if r.Y+r.H > viewport.Y+viewport.H-4 {
						delta = r.Y + r.H - viewport.Y - viewport.H + 4
					}
					if delta != 0 {
						scroll.ScrollTo(scroll.X, max(0, min(scroll.MaxY, scroll.Y+delta)), animation)
						v.services.Invalidate()
						return
					}
				}
				if scroll.Scrolling() {
					p.AnimationFrame()
					return
				}
			}
			for _, r := range boxes {
				decoration.Draw(p, r)
			}
		})
	}
}

func (v *View) conversationFindBar(c *n.Context, t *n.Theme, width float32) {
	s := v.findState()
	if !s.open {
		return
	}
	bar := n.Row(c.Key("conversation-find")).Absolute().Right(24).Top(56).Width(min(352, max(200, width-36))).Height(36).Padding(4, 6).Gap(6).AlignItems(n.Center).Radius(8).Background(raised(t)).Border(1, t.Border).Shadow(0, 3, 10, 0, n.RGBA(0, 0, 0, .1)).Label("会话内查找").Transition(n.ElementTransition{Duration: 180 * time.Millisecond, Enter: &n.Motion{Y: -6, Opacity: 1}, Exit: &n.Motion{Y: -6, Opacity: 1}})
	bar.Children(func() {
		n.Icon(c, searchIcon).Size(14, 14).TextColor(t.TextMuted)
		field := n.TextInputBase(c.Key("query"), &s.query).Grow(1).MinWidth(0).Height(26).Padding(0).FontSize(13).Label("在会话中查找").Placeholder("查找…")
		if s.focus {
			field.Focus().SetTextSelection(0, utf8.RuneCountInString(s.query))
			s.focus = false
		}
		field.OnTextKey(func(key n.TextKey) bool {
			if key.Key == n.KeyEscape {
				v.closeConversationFind()
				return true
			}
			if key.Key == n.KeyEnter {
				d := 1
				if key.Mods&n.Shift != 0 {
					d = -1
				}
				v.stepConversationFind(d)
				return true
			}
			return false
		})
		if field.Changed() {
			s.pending = nil
		}
		count := ""
		if s.query != "" {
			count = fmt.Sprintf("%d/%d", s.current+1, s.total)
		}
		n.Text(c, count).FontSize(11).FontFeatures("tnum").FixedLineHeight(20).TextColor(t.TextMuted).Label("匹配位置：" + count)
		for _, direction := range []int{-1, 1} {
			label, angle := "上一个匹配", float32(180)
			if direction == 1 {
				label, angle = "下一个匹配", 0
			}
			button := n.ButtonBase(c).Size(24, 24).Label(label).Tooltip(label).Disabled(s.total == 0)
			color := textHoverColor(c, button, t.TextMuted)
			button.Children(func() { n.Icon(c, chevron).Size(14, 14).Rotate(angle).TextColor(color) })
			if button.Clicked() {
				v.stepConversationFind(direction)
			}
		}
		button := n.ButtonBase(c).Size(24, 24).Label("关闭会话查找").Tooltip("关闭")
		color := textHoverColor(c, button, t.TextMuted)
		button.Children(func() { n.Icon(c, closeIcon).Size(14, 14).TextColor(color) })
		if button.Clicked() {
			v.closeConversationFind()
		}
	})
}

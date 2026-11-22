package main

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/domain"
)

const commandOutputHeight float32 = 160
const structuredCodeHeight float32 = 180

type outputTextAppend struct {
	start, end int
	at         time.Time
}

type mobileCommandOutput struct {
	codeViewport
	mobileOutputText
	open bool
	live bool
	tail bool
}

type mobileOutputText struct {
	text    string
	appends []outputTextAppend
}

func commandTool(item domain.Item) bool {
	return item.Kind == "tool" && (item.Presentation == "command" || item.Presentation == "")
}

func (s *mobileCommandOutput) render(c *ui.Context, item domain.Item, open bool, mark *markdown.RangeDecoration, name, font string) {
	display := strings.TrimRight(item.Text, "\r\n")
	live := !item.Final && (item.Status == "running" || item.Status == "starting")
	previouslyLive, previouslyOpen := s.live, s.open
	s.live, s.open = live, open && display != ""
	if !s.open {
		s.text, s.tail, s.appends = display, false, nil
		return
	}
	follow := live || previouslyLive
	if !previouslyOpen {
		s.appends = nil
		if follow && mark == nil {
			// Seed the first layout even before it has a measured MaxY.
			s.scroll.Y = math.MaxFloat32
		}
	} else if display != s.text {
		s.tail = follow && mark == nil
	}
	s.update(c, display, previouslyOpen && follow)
	if mark != nil {
		s.tail = false
	}

	p := palette(c)
	spans := s.spans(c, display, p.secondary)
	view := codeViewportSurface(c, "命令输出："+name, display, commandOutputHeight, spans, mark, &s.codeViewport,
		p.panel, p.secondary, font, captionTextSize).Radius(6)
	view.DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
		if !s.tail || bounds.H <= 0 {
			return
		}
		s.tail = false
		if s.scroll.MaxY > s.scroll.Y+.5 {
			s.scroll.ScrollTo(0, s.scroll.MaxY, mobileOutputFollowMotion(c))
			c.Services().Invalidate()
		}
	})
}

func (s *mobileOutputText) update(c *ui.Context, text string, animate bool) {
	if text == s.text {
		return
	}
	if animate && !c.Preferences().ReduceMotion && strings.HasPrefix(text, s.text) {
		s.appends = append(s.appends, outputTextAppend{len(s.text), len(text), c.Now()})
	} else {
		s.appends = nil
	}
	s.text = text
}

func (s *mobileOutputText) spans(c *ui.Context, text string, color ui.Color) []ui.Span {
	if c.Preferences().ReduceMotion || len(s.appends) == 0 {
		s.appends = nil
		return nil
	}
	var spans []ui.Span
	from := 0
	active := s.appends[:0]
	for _, appendage := range s.appends {
		progress := float32(c.Now().Sub(appendage.at)) / float32(mobilePanelTransition.Duration)
		if progress >= 1 {
			continue
		}
		if from < appendage.start {
			spans = append(spans, ui.Span{Text: text[from:appendage.start]})
		}
		active = append(active, appendage)
		spans = append(spans, ui.Span{Text: text[appendage.start:appendage.end], Color: color.Alpha(ui.EaseOut(max(0, progress)))})
		from = appendage.end
	}
	s.appends = active
	if len(active) == 0 {
		return nil
	}
	if from < len(text) {
		spans = append(spans, ui.Span{Text: text[from:]})
	}
	c.AnimationFrame()
	return spans
}

type codeViewport struct {
	scroll               ui.ScrollState
	matchStart, matchEnd int
	matchPositioned      bool
}

func (a *mobileApp) codeViewportView(c *ui.Context, title, text string, height float32, spans []ui.Span, mark *markdown.RangeDecoration, state *codeViewport) ui.Element {
	p := palette(c)
	return codeViewportSurface(c, title, text, height, spans, mark, state, p.inset, p.ink, a.codeFont(), captionTextSize)
}

func codeViewportSurface(c *ui.Context, title, text string, height float32, spans []ui.Span, mark *markdown.RangeDecoration,
	state *codeViewport, surface, ink ui.Color, font string, fontSize float32) ui.Element {
	view := ui.Scroll(c).FillWidth().Radius(8).Background(surface)
	if height > 0 {
		view.MaxHeight(height)
	} else {
		view.Grow(1).Basis(0).MinHeight(mobileTapSize(c))
	}
	if state != nil {
		view.TrackScroll(&state.scroll)
		mark = state.viewportMark(c, mark)
	}
	if title != "" {
		view.Label(title)
	}
	return view.Children(func() {
		scale := c.Preferences().TextScale
		var code ui.Element
		if len(spans) != 0 {
			code = ui.RichText(c, spans...).Label(text).Wrap()
		} else {
			code = ui.Text(c, text).Wrap()
		}
		code.Font(font).FontSize(fontSize*scale).FixedLineHeight(18*scale).TextColor(ink).
			FillWidth().Padding(8, 10).Selectable()
		decorateTranscriptText(c, code, text, mark, nil)
	})
}

// Let a text match reveal itself inside the code viewport before the
// transcript attempts to position the now-visible match in the outer list.
func (s *codeViewport) viewportMark(c *ui.Context, mark *markdown.RangeDecoration) *markdown.RangeDecoration {
	if mark == nil {
		s.matchPositioned = false
		return nil
	}
	if s.matchStart != mark.Start || s.matchEnd != mark.End {
		s.matchStart, s.matchEnd, s.matchPositioned = mark.Start, mark.End, false
	}
	return &markdown.RangeDecoration{Start: mark.Start, End: mark.End, Draw: func(p *ui.Painter, box ui.Rect) {
		bounds := s.scroll.Bounds(c)
		if bounds.H <= 0 {
			return
		}
		if !s.matchPositioned {
			s.matchPositioned = true
			if box.Y < bounds.Y+4 || box.Y+box.H > bounds.Y+bounds.H-4 {
				offset := s.scroll.Y + box.Y - bounds.Y - bounds.H/3
				s.scroll.ScrollTo(0, offset, ui.Animation{})
				c.Services().Invalidate()
				return
			}
		}
		if !s.scroll.Scrolling() && box.Y+box.H > bounds.Y && box.Y < bounds.Y+bounds.H {
			mark.Draw(p, box)
		}
	}}
}

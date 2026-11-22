package conversationui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

const (
	usageNoticeMotion = 220 * time.Millisecond
	tideMotion        = 420 * time.Millisecond
	usageTitle        = "You’ve hit your usage limit."
)

type usageNoticeState struct {
	turn  domain.Turn
	start time.Time
	width float32
}

// UsageLimitNotice shares the geometry and one-shot motion across the Mac
// conversation and the mobile dock. The clipped wrapper takes up only the
// height already revealed, keeping the composer anchored below it.
func UsageLimitNotice(c *n.Context, turn domain.Turn, surface, ink, muted n.Color, textScale, tideWidth, bottomGap float32) {
	frame := n.Column(c).FillWidth().Shrink(0).Clip()
	state := n.Local(frame, "usage-limit", func() usageNoticeState {
		frame.Jump("height", 0)
		return usageNoticeState{}
	})
	now := c.Now()
	reduced := c.Preferences().ReduceMotion
	if turn.UsageLimit != nil && turn.ID != state.turn.ID {
		state.start = now
	}
	if turn.UsageLimit != nil {
		state.turn = turn
		if reset := turn.UsageLimit.ResetAt; reset != nil && reset.After(now) {
			c.After(reset.Sub(now))
		}
	}
	if state.turn.UsageLimit == nil {
		frame.Hidden(true)
		return
	}
	const inset, pad, gap = float32(12), float32(9), float32(10)
	width := state.width
	if width <= 0 {
		width = 300
	}
	if width < 240 || textScale > 1.4 {
		tideWidth = 0
	}
	textWidth := width - inset*2
	if tideWidth > 0 {
		textWidth -= tideWidth + gap
	}
	title := n.Font{Family: c.Theme().Font, Size: 13 * textScale, Weight: 600}
	body := n.Font{Family: c.Theme().Font, Size: 12 * textScale, Weight: 400}
	titleLine, bodyLine := 18*textScale, 17*textScale
	caption := usageResetCaption(state.turn.UsageLimit.ResetAt, now)
	titleHeight := float32(len(n.ShapeText(usageTitle, title, max(1, textWidth)).Lines())) * titleLine
	bodyHeight := float32(len(n.ShapeText(caption, body, max(1, textWidth)).Lines())) * bodyLine
	height := max(40, titleHeight+2+bodyHeight) + 2*pad
	target := float32(0)
	if turn.UsageLimit != nil {
		target = height + bottomGap
	}
	shown := target
	if reduced {
		frame.Jump("height", target)
	} else {
		shown = frame.AnimateTo("height", target, n.Animation{Duration: usageNoticeMotion, Ease: n.EaseOut})
	}
	if shown <= 0.01 {
		// The first opening frame still schedules the animated layout.
		if target == 0 {
			state.turn = domain.Turn{}
			frame.Hidden(true)
			return
		}
	}
	frame.Height(shown)
	n.Inert(frame, turn.UsageLimit == nil)
	invalidate := c.Services().Invalidate
	frame.Draw(func(_ *n.Painter, box n.Rect) {
		if box.W > 0 && state.width != box.W {
			state.width = box.W
			invalidate()
		}
	})
	departing, started := turn.UsageLimit == nil, state.start
	frame.Children(func() {
		n.Row(c).Absolute().Left(0).Right(0).Bottom(min(bottomGap, shown)).Height(height).
			Padding(pad, inset).Gap(gap).Radius(12).Background(surface).AlignItems(n.Center).Children(func() {
			if tideWidth > 0 {
				n.Box(c).Size(tideWidth, 40).Shrink(0).Role(n.RoleNone).Draw(func(p *n.Painter, box n.Rect) {
					progress := 1.0
					if !departing && !reduced {
						progress = min(1, max(0, float64(p.Now().Sub(started))/float64(tideMotion)))
						if progress < 1 {
							p.AnimationFrameRate(60)
						}
					}
					paintUsageTide(p, box, muted, 1-math.Pow(1-progress, 3))
				})
			}
			n.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				n.Text(c, usageTitle).Font(title.Family).FontSize(title.Size).FontWeight(title.Weight).
					FixedLineHeight(titleLine).TextColor(ink).Wrap().Role(n.RoleStatus)
				n.Text(c, caption).Font(body.Family).FontSize(body.Size).FixedLineHeight(bodyLine).TextColor(muted).Wrap()
			})
		})
	})
}

func usageResetCaption(reset *time.Time, now time.Time) string {
	if reset == nil {
		return "重置时间暂未知"
	}
	local := reset.In(now.Location())
	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
		return "预计今日 " + local.Format("15:04") + " 重置"
	}
	return "预计 " + local.Format("1 月 2 日 15:04") + " 重置"
}

func paintUsageTide(p *n.Painter, box n.Rect, ink n.Color, progress float64) {
	sx := box.W / 76
	tick := func(x, y, w float32) {
		p.Fill(n.Rect{X: box.X + x*sx, Y: box.Y + y, W: w * sx, H: 1}, ink.Alpha(.23), 0)
	}
	for _, y := range []float32{8, 15, 22, 29} {
		tick(5, y, 4)
		tick(67, y, 4)
	}
	for x := float32(13); x < 65; x += 5 {
		tick(x, 8, 2)
	}
	var wave, water n.Path
	for step := 0; step <= 24; step++ {
		f := float64(step) / 24
		x := box.X + float32(13+50*f)*sx
		y := box.Y + float32(8+21*progress+(2.7-1.2*progress)*math.Sin(f*2*math.Pi-progress*2.4))
		if step == 0 {
			wave.MoveTo(x, y)
			water.MoveTo(x, y)
		} else {
			wave.LineTo(x, y)
			water.LineTo(x, y)
		}
	}
	water.LineTo(box.X+63*sx, box.Y+34).LineTo(box.X+13*sx, box.Y+34).Close()
	p.FillPath(&water, ink.Alpha(.14))
	p.StrokePath(&wave, 1.4, ink)
}

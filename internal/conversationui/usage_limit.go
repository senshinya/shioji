package conversationui

import (
	"fmt"
	"math"
	"time"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

const (
	usageNoticeMotion = 220 * time.Millisecond
	tideMotion        = 420 * time.Millisecond
	usageTitle        = "使用额度已用尽"
	noticeInset       = float32(12)
	noticePadding     = float32(9)
	noticeGap         = float32(10)
	noticeTideHeight  = float32(40)
)

type usageNoticeState struct {
	id          string
	limit       *domain.UsageLimit
	start       time.Time
	width       float32
	initialized bool
	// Cached text height measurement to avoid calling ShapeText twice every frame.
	cachedCaption string
	cachedTitle   string
	cachedFamily  string
	cachedScale   float32
	cachedWidth   float32
	cachedHeight  float32
}

// ComposerWarning selects the notice and the reply it represents. A saved
// relative estimate is anchored to the original turn's completion time.
func ComposerWarning(detail domain.SessionDetail, limit domain.Turn) (domain.Turn, string) {
	turn, item := latestAntigravityReply(detail)
	_, retryAfter, quota := SplitAntigravityQuotaWarning(detail.Session.HarnessID, item)
	if limit.UsageLimit != nil {
		if quota && turn.ID == limit.ID {
			return limit, item.ID
		}
		return limit, ""
	}
	if quota {
		turn.UsageLimit = &domain.UsageLimit{}
		if turn.CompletedAt != nil && !turn.CompletedAt.IsZero() && retryAfter != nil {
			reset := turn.CompletedAt.Add(*retryAfter)
			turn.UsageLimit.ResetAt = &reset
		}
		return turn, item.ID
	}
	if _, regional := SplitRegionalAccessWarning(item); regional {
		return domain.Turn{}, item.ID
	}
	return domain.Turn{}, ""
}

// ComposerNotice shares the layout and one-shot motion of the notices in the
// Mac and mobile docks, keeping the composer anchored below them.
func ComposerNotice(c *n.Context, turn domain.Turn, itemID string, surface, ink, muted n.Color, textScale, tideWidth, bottomGap, retryHeight float32, retry func(n.Rect)) {
	id := itemID
	if turn.UsageLimit != nil {
		id = turn.ID
	}
	frame := n.Column(c).FillWidth().Shrink(0).Clip()
	state := n.Local(frame, "usage-limit", func() usageNoticeState {
		return usageNoticeState{}
	})
	now := c.Now()
	reduced := c.Preferences().ReduceMotion
	initialMount := !state.initialized
	state.initialized = true
	if initialMount && id == "" {
		frame.Jump("height", 0)
	} else if !initialMount && id != "" && id != state.id {
		state.start = now
	}
	if id != "" {
		state.id, state.limit = id, turn.UsageLimit
	}
	if state.id == "" {
		frame.Hidden(true)
		return
	}
	width := state.width
	if width <= 0 {
		width = 300
	}
	retryWidth := max(48, 26*textScale+16)
	textWidth := width - noticeInset*2 - retryWidth - noticeGap
	minContentHeight := max(noticeTideHeight, retryHeight)
	if textWidth < 240-2*noticeInset || textScale > 1.4 {
		tideWidth = 0
	}
	if tideWidth > 0 {
		textWidth -= tideWidth + noticeGap
	}
	title, body, titleLine, bodyLine := noticeTypography(c, textScale)
	headline, caption := regionalAccessTitle, regionalAccessCaption
	if state.limit != nil {
		var next time.Duration
		headline = usageTitle
		caption, next = usageResetCaption(state.limit.ResetAt, now)
		if id != "" && next > 0 {
			c.After(next)
		}
	}
	if state.cachedHeight == 0 || state.cachedTitle != headline || state.cachedCaption != caption || state.cachedFamily != title.Family ||
		state.cachedScale != textScale || state.cachedWidth != textWidth {
		titleHeight := float32(len(n.ShapeText(headline, title, max(1, textWidth)).Lines())) * titleLine
		bodyHeight := float32(len(n.ShapeText(caption, body, max(1, textWidth)).Lines())) * bodyLine
		state.cachedTitle = headline
		state.cachedCaption, state.cachedFamily, state.cachedScale, state.cachedWidth = caption, title.Family, textScale, textWidth
		state.cachedHeight = max(minContentHeight, titleHeight+2+bodyHeight) + 2*noticePadding
	}
	height := state.cachedHeight
	target := float32(0)
	if id != "" {
		target = height + bottomGap
	}
	shown := target
	if reduced || initialMount {
		frame.Jump("height", target)
	} else {
		shown = frame.AnimateTo("height", target, n.Animation{Duration: usageNoticeMotion, Ease: n.EaseOut})
	}
	if shown <= 0.01 {
		// The first opening frame still schedules the animated layout.
		if target == 0 {
			state.id, state.limit = "", nil
			frame.Hidden(true)
			return
		}
	}
	frame.Height(shown)
	n.Inert(frame, id == "")
	invalidate := c.Services().Invalidate
	frame.Draw(func(_ *n.Painter, box n.Rect) {
		if box.W > 0 && state.width != box.W {
			state.width = box.W
			invalidate()
		}
	})
	started := state.start
	if id == "" || reduced {
		started = time.Time{}
	}
	frame.Children(func() {
		trailing := func() {
			button := n.ButtonBase(c.Key("retry")).Role(n.RoleButton).Label("重试").Disabled(retry == nil).KeepFocus().
				Width(retryWidth).MinHeight(retryHeight).Shrink(0).Justify(n.Center).AlignItems(n.Center).Radius(6).Border(1, ink.Alpha(.15))
			textColor := ink
			if retry == nil {
				textColor = muted
			} else {
				if button.Hovered() || button.FocusVisible() {
					button.Background(ink.Alpha(.07))
				}
				if button.Pressed() {
					button.Background(ink.Alpha(.13))
				}
				button.OnClick(func() { retry(button.Bounds()) })
			}
			button.Children(func() {
				n.Text(c, "重试").FontSize(12 * textScale).FontWeight(500).SingleLine().TextColor(textColor)
			})
		}
		noticeCard(c, headline, caption, surface, ink, muted, textScale, tideWidth, started, trailing).
			Absolute().Left(0).Right(0).Bottom(min(bottomGap, shown)).Height(height)
	})
}

func noticeTypography(c *n.Context, textScale float32) (title, body n.Font, titleLine, bodyLine float32) {
	family := c.Theme().Font
	return n.Font{Family: family, Size: 13 * textScale, Weight: 600},
		n.Font{Family: family, Size: 12 * textScale, Weight: 400, Features: "tnum"}, 18 * textScale, 17 * textScale
}

func noticeCard(c *n.Context, headline, caption string, surface, ink, muted n.Color, textScale, tideWidth float32, started time.Time, trailing func()) n.Element {
	title, body, titleLine, bodyLine := noticeTypography(c, textScale)
	row := n.Row(c).Padding(noticePadding, noticeInset).Gap(noticeGap).Radius(12).Background(surface).AlignItems(n.Center)
	row.Children(func() {
		if tideWidth > 0 && textScale <= 1.4 {
			n.Box(c).Size(tideWidth, noticeTideHeight).Shrink(0).Role(n.RoleNone).Draw(func(p *n.Painter, box n.Rect) {
				progress := 1.0
				if !started.IsZero() {
					progress = min(1, max(0, float64(p.Now().Sub(started))/float64(tideMotion)))
					if progress < 1 {
						p.AnimationFrameRate(60)
					}
				}
				paintUsageTide(p, box, muted, 1-math.Pow(1-progress, 3))
			})
		}
		n.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			n.Text(c, headline).Font(title.Family).FontSize(title.Size).FontWeight(title.Weight).
				FixedLineHeight(titleLine).TextColor(ink).Wrap().Role(n.RoleStatus)
			n.Text(c, caption).Font(body.Family).FontSize(body.Size).FontFeatures(body.Features).FixedLineHeight(bodyLine).TextColor(muted).Wrap()
		})
		trailing()
	})
	return row
}

func usageResetCaption(reset *time.Time, now time.Time) (string, time.Duration) {
	if reset == nil {
		return "重置时间暂未知", 0
	}
	remaining := reset.Sub(now)
	if remaining <= 0 {
		return "预计重置时间已到", 0
	}
	// Round up so zero is never displayed before the deadline.
	seconds := int64((remaining-1)/time.Second) + 1
	next := remaining - time.Duration(seconds-1)*time.Second
	return fmt.Sprintf("预计 %02d:%02d:%02d 后重置", seconds/3600, seconds/60%60, seconds%60), next
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

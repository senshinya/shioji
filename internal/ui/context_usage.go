package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"time"
)

func (v *View) contextMeter(c *n.Context, t *n.Theme) {
	var session domain.Session
	for _, s := range v.Model.Snapshot.Sessions {
		if s.ID == v.Model.SessionID {
			session = s
			break
		}
	}
	if session.ID == "" {
		return
	}
	usage := session.ContextUsage
	compacting := false
	detail := v.Model.Details[session.ID]
	if session.Status == "running" {
		for i := len(detail.Items) - 1; i >= 0; i-- {
			item := detail.Items[i]
			if item.Presentation != "compaction" {
				continue
			}
			if !item.Final && item.Status == "running" {
				for _, turn := range detail.Turns {
					if turn.ID == item.TurnID && turn.Status == "running" {
						compacting = true
						break
					}
				}
			}
			break
		}
	}
	fraction := float32(0)
	known := usage != nil && usage.UsedTokens != nil && usage.LimitTokens != nil && *usage.LimitTokens > 0
	label := "—"
	if known {
		fraction = min(1, max(0, float32(float64(*usage.UsedTokens)/float64(*usage.LimitTokens))))
		label = fmt.Sprintf("%.0f%%", fraction*100)
	}
	if compacting {
		label = "压缩中"
	}
	trigger := control(c.Key("context-meter:" + session.ID)).Width(28).Height(28).PaddingX(4).Radius(6).Label("上下文用量").Children(func() {
		font := n.Font{Family: t.Font, Size: 11, Weight: 400}
		n.Row(c).FillWidth().Justify(n.Center).AlignItems(n.Baseline).Children(func() {
			metrics := inlineFontMetrics("上下文", font)
			ring := n.RichText(c.Key("ring"), n.Span{Text: "\uFFFC", ObjectWidth: 18, ObjectHeight: metrics.ascent, ObjectDescent: metrics.descent}).Font(t.Font).FontSize(11).FixedLineHeight(20).Label("上下文圆环").Role(n.RoleNone)
			progress := ring.AnimateTo("used", fraction, motionAnimation(c, 500*time.Millisecond))
			mix := float32(0)
			if compacting {
				mix = 1
			}
			mix = ring.AnimateTo("compacting", mix, motionAnimation(c, 180*time.Millisecond))
			started := *n.Local(ring, "spin-start", func() time.Time { return c.Now() })
			reduce := c.Preferences().ReduceMotion
			ring.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
				cx, cy := r.X+9, r.Y+metrics.ascent-metrics.center
				var track n.Path
				track.Circle(cx, cy, 7.25)
				p.StrokePath(&track, 1.5, t.TextMuted.Alpha(.2))
				arc := func(from, to float32, alpha float32) {
					if to <= from || alpha <= 0 {
						return
					}
					var path n.Path
					path.Circle(cx, cy, 7.25).Trim(from, to)
					p.Transform(n.Rotation(-90), cx, cy, func() { p.StrokePath(&path, 1.5, t.TextMuted.Alpha(alpha)) })
				}
				arc(0, progress, 1-mix)
				if mix > 0 {
					phase := float32(0)
					if !reduce {
						phase = float32(math.Mod(p.Now().Sub(started).Seconds()/1.2, 1))
						p.AnimationFrameRate(60)
					}
					arc(phase, min(1, phase+.25), mix)
					if phase > .75 {
						arc(0, phase-.75, mix)
					}
				}
			})
		})
	})
	open := n.Local(trigger, "open", func() bool { return false })
	lastInside := n.Local(trigger, "last-inside", func() time.Time { return c.Now() })
	dismissed := n.Local(trigger, "dismissed", func() bool { return false })
	inside := trigger.Hovered() || trigger.FocusVisible()
	if inside && !*dismissed {
		*open = true
		*lastInside = c.Now()
	}
	panel := n.PopoverBase(c.Key("context-details:"+session.ID), trigger, open, func(panel n.Element) {
		panel.Transition(popupMotion(c))
		panel.AttachTo(trigger, n.AnchorTopEnd, n.AnchorBottomEnd).Margin(0, 0, 6, 0).Label("上下文详情").Width(232).Padding(10).Radius(9).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() {
				n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
					n.Text(c, "上下文").FontSize(12).FontWeight(500)
					n.Spacer(c).Grow(1)
					animation := motionAnimation(c, 240*time.Millisecond)
					n.Text(c, label).FontSize(11).FontFeatures("tnum").TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Numeric: known && !compacting, Animation: &animation})
				})

				bar := n.Column(c.Key("context-bar")).FillWidth().Height(4).Radius(2).Clip().Background(t.TextMuted.Alpha(.12)).Label("上下文进度")
				shown := bar.AnimateTo("fraction", fraction, motionAnimation(c, 500*time.Millisecond))
				opacity := float32(0)
				if known {
					opacity = 1
				}
				bar.Opacity(bar.AnimateTo("known", opacity, motionAnimation(c, 160*time.Millisecond)))
				bar.Draw(func(p *n.Painter, r n.Rect) { r.W *= shown; p.Fill(r, t.TextMuted.Alpha(.7), 2) })
				used, remaining, limit := "—", "—", "—"
				if usage != nil && usage.UsedTokens != nil {
					used = contextTokenNumber(*usage.UsedTokens)
				}
				if known {
					remaining = contextTokenNumber(max(0, *usage.LimitTokens-*usage.UsedTokens))
					limit = contextTokenNumber(*usage.LimitTokens)
				}
				n.Column(c).FillWidth().Gap(0).Children(func() {
					contextDetailRow(c, t, "已用", used)
					contextDetailRow(c, t, "剩余", remaining)
					contextDetailRow(c, t, "上限", limit)
				})
				status := "尚未报告上下文用量"
				if usage != nil && usage.UsedTokens != nil {
					status = "上限未知"
					if known {
						status = "最近报告"
						if !usage.UpdatedAt.IsZero() {
							status += " " + usage.UpdatedAt.Local().Format("15:04:05")
						}
					}
				}
				if !v.Model.Connected {
					status = "离线 · 上次报告"
				}
				if compacting {
					status = "正在压缩上下文"
				}
				animation := motionAnimation(c, 160*time.Millisecond)
				n.Text(c.Key("context-status"), status).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: &animation})
			})
		})
	})
	if panel.Valid() {
		if panel.Hovered() || panel.FocusWithin() {
			inside = true
			*lastInside = c.Now()
		}
		if panel.Dismissed() {
			*dismissed = true
		}
	}
	if !inside {
		*dismissed = false
		if *open {
			remaining := 180*time.Millisecond - c.Now().Sub(*lastInside)
			if remaining <= 0 {
				*open = false
				c.AnimationFrame()
			} else {
				c.After(remaining)
			}
		}
	}
}

func contextDetailRow(c *n.Context, t *n.Theme, label, value string) {
	n.Row(c).FillWidth().Height(18).AlignItems(n.Baseline).Children(func() {
		n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
		n.Spacer(c).Grow(1)
		animation := motionAnimation(c, 240*time.Millisecond)
		n.Text(c, value).FontSize(11).FontFeatures("tnum").FixedLineHeight(18).ContentTransition(n.ContentTransition{Numeric: true, Animation: &animation})
	})
}
func contextTokenNumber(value int64) string {
	s := strconv.FormatInt(value, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

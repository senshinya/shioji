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

func (v *View) contextMeter(c *n.Context, t *n.Theme, cfg domain.RunConfig) {
	v.syncContextInput()
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
	compacting := v.currentCompaction() != ""
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
		font := n.Font{Family: t.Font, Size: 12, Weight: 400}
		n.Row(c).FillWidth().Justify(n.Center).AlignItems(n.Baseline).Children(func() {
			ring := n.RichText(c.Key("ring"), inlineIconSpan(cfg.Model, font, 16, 0)).Font(t.Font).FontSize(12).FixedLineHeight(20).Label("上下文圆环").Role(n.RoleNone)
			progress := ring.AnimateTo("used", fraction, motionAnimation(c, 500*time.Millisecond))
			mix := float32(0)
			if compacting {
				mix = 1
			}
			mix = ring.AnimateTo("compacting", mix, motionAnimation(c, 180*time.Millisecond))
			started := *n.Local(ring, "spin-start", func() time.Time { return c.Now() })
			reduce := c.Preferences().ReduceMotion
			ring.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				var track n.Path
				track.Circle(cx, cy, 6.25)
				p.StrokePath(&track, 1.5, t.TextMuted.Alpha(.2))
				arc := func(from, to float32, alpha float32) {
					if to <= from || alpha <= 0 {
						return
					}
					var path n.Path
					path.Circle(cx, cy, 6.25).Trim(from, to)
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
	hasAction := v.Model.WorkerMinor >= 29 && v.Model.RunReadiness().Capabilities.Compact
	hoverCard(c.Key("context-details:"+session.ID), trigger, func(panel n.Element) {
		panel.Transition(popupMotion(c))
		informationPopoverSurface(panel, t).AttachTo(trigger, n.AnchorTopEnd, n.AnchorBottomEnd).
			Margin(0, 0, 6, 0).Label("上下文详情").Width(232).Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() {
				n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
					n.Text(c, "上下文").FontSize(13).FixedLineHeight(20)
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
				if state := v.contextInput(); state.error != "" {
					status = state.error
				}
				animation := motionAnimation(c, 160*time.Millisecond)
				n.Text(c.Key("context-status"), status).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: &animation})
				if hasAction {
					n.Column(c).FillWidth().Gap(3).Children(func() {
						n.Column(c).FillWidth().Height(1).Background(t.Border)
						state := v.contextInput()
						label := "压缩上下文"
						if a := v.contextAction(); a != nil && (a.State == "failed" || a.State == "interrupted") {
							label = "重试压缩"
						}
						reason := v.compactReason()
						query := state.phase == contextUnknown
						if compacting {
							label = "正在压缩"
						}
						if query {
							label = "核对压缩状态"
						}
						if state.phase == contextChecking {
							label = "正在核对"
						}
						disabled := reason != ""
						if query {
							disabled = !v.Model.Connected || v.Controller == nil
						}
						button := control(c.Key("compact-context")).AlignSelf(n.Stretch).MarginX(-6).Height(26).PaddingX(6).Radius(4).Disabled(disabled).Tooltip(reason).Label("压缩上下文")
						button.Children(func() {
							font := n.Font{Family: t.Font, Size: 12, Weight: 400}
							if compacting || state.phase == contextChecking {
								serviceSpinner(c.Key("compact-spinner"), label, font, t.TextMuted)
							} else {
								inlineSymbol(c, compactContextIcon, label, font, 14, t.TextMuted)
							}
							n.Text(c.Key("compact-label"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).MarginStart(6).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
							n.Spacer(c).Grow(1)
							n.Text(c, "/compact").FontSize(10).TextColor(t.TextMuted)
						})
						if button.Clicked() {
							if query {
								v.sendContextOperation(state, true)
							} else {
								v.beginContextCompaction(false)
							}
						}
					})
				}
			})
		})
	})
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

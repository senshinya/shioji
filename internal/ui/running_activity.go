package ui

import (
	"fmt"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// This is a projection of the current normalized turn, not another execution
// lifecycle. No phase is inferred from a tool name, source ID or elapsed time.
type runningActivity struct {
	label   string
	waiting bool
	started *time.Time
}

func (v *View) runningActivity(turn domain.Turn) runningActivity {
	a := runningActivity{label: "进行中", started: turn.StartedAt}
	if turn.Status == domain.ExecutionStarting {
		a.label = "正在准备"
	}
	d := v.Model.Details[v.Model.SessionID]
	for _, r := range d.Requests {
		if r.WorkID != "" || r.TurnID != "" && r.TurnID != turn.ID || !r.State.AwaitingResolution() {
			continue
		}
		a.waiting = true
		switch r.Kind {
		case "approval", "permissions":
			a.label = "等待授权"
		case "questions", "form":
			a.label = "等待回答"
		default:
			a.label = "等待确认"
		}
		return a
	}
	for i := len(d.Items) - 1; i >= 0; i-- {
		item := d.Items[i]
		if item.TurnID != turn.ID || item.WorkID != "" {
			continue
		}
		switch item.Kind {
		case "assistant":
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			if !item.Final {
				a.label = "正在回复"
			}
			return a
		case "tool":
			if !item.Final {
				if item.Presentation == "command" || item.Presentation == "" {
					a.label = "正在执行命令"
				} else {
					a.label = "正在执行工具"
				}
			}
			return a
		case "activity":
			if item.Presentation == "reasoning" {
				if !item.Final {
					a.label = "正在思考"
				}
				return a
			}
		}
	}
	return a
}

// Only the live bottom group can own the status. While disclosure is moving,
// the footer stays until all child rows have actually left the projection.
func (v *View) syncRunningGroup(rows []transcriptRow, detail domain.SessionDetail) {
	v.runningGroup, v.activityGroup, v.activityTurn = "", "", ""
	if !v.Model.Connected || len(rows) == 0 {
		return
	}
	row := rows[len(rows)-1]
	if row.group == "" {
		if row.toolParent == "" {
			return
		}
		for _, heading := range rows {
			if heading.group == row.toolParent {
				row = heading
				break
			}
		}
	}
	if row.group == "" {
		return
	}
	for i := len(detail.Turns) - 1; i >= 0; i-- {
		turn := detail.Turns[i]
		if turn.Status != domain.ExecutionRunning && turn.Status != domain.ExecutionStarting {
			continue
		}
		if turn.ID != row.item.TurnID || v.runningActivity(turn).waiting {
			return
		}
		v.activityGroup, v.activityTurn = row.group, turn.ID
		if row.end >= 0 && v.Model.ToolDisclosure(row.group).Closed() {
			v.runningGroup = row.group
		}
		return
	}
}

func elapsedActivityLabel(started, now time.Time) string {
	seconds := int64(max(time.Duration(0), now.Sub(started)) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	return fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
}

// The painter schedules only itself on the next worker-relative second. Text
// shaping is cached until the displayed second changes; the transcript, list
// and composer never rebuild because of this clock.
func runningElapsed(c *n.Context, started *time.Time, color n.Color) {
	if started == nil || started.IsZero() {
		return
	}
	since := *started
	font := n.Font{Family: c.Theme().Font, Size: 11, Weight: 400, Features: "tnum"}
	m := inlineFontMetrics("秒", font)
	width, _ := n.ShapeText("9999 分 59 秒", font, 0).Size()
	clock := n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: width, ObjectHeight: m.ascent, ObjectDescent: m.descent}).Font(font.Family).FontSize(11).FontFeatures("tnum").FixedLineHeight(20).TextColor(color).Label("运行时长").Transition(fadeMotion(c, 120*time.Millisecond))
	var text string
	var layout *n.TextLayout
	clock.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		now := p.Now()
		value := elapsedActivityLabel(since, now)
		if value != text {
			layout = n.ShapeText(value, font, 0)
			text = value
		}
		p.TextLayout(layout, r.X, r.Y+m.ascent-layout.Lines()[0].Ascent, color)
		elapsed := max(time.Duration(0), now.Sub(since))
		p.After(time.Second - elapsed%time.Second)
	})
}

func (v *View) runningStatus(c *n.Context, t *n.Theme, turn domain.Turn) {
	a := v.runningActivity(turn)
	n.Row(c.Key("running-status")).Gap(6).AlignItems(n.Baseline).Children(func() {
		font := n.Font{Family: t.Font, Size: 11, Weight: 400}
		color := t.Success
		if a.waiting {
			color = t.TextMuted
		}
		dot := statusDot(c.Key("dot"), a.label, font, color, !a.waiting)
		if a.waiting {
			dot.Label(a.label + "状态")
		}
		text := n.Text(c.Key("activity-label"), a.label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
		if !a.waiting {
			shimmer(c, text, t.TextMuted, t.Text)
			runningElapsed(c.Key("elapsed"), a.started, t.TextMuted)
		}
	})
}

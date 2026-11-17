package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// This is a projection of the current normalized turn, not another execution
// lifecycle. No phase is inferred from a tool name, source ID or elapsed time.
type runningActivity struct {
	label      string
	item       string
	kind       string
	index      int
	waiting    bool
	compaction string
	started    *time.Time
}

func (v *View) runningActivity(turn domain.Turn) runningActivity {
	a := runningActivity{label: "进行中", started: v.runningElapsedStart(turn)}
	if turn.Status == domain.ExecutionStarting {
		a.label = "正在准备"
	}
	d := v.Model.Details[v.Model.SessionID]
	for _, r := range d.Requests {
		if r.WorkID != "" || r.TurnID != "" && r.TurnID != turn.ID || !r.State.AwaitingResolution() {
			continue
		}
		// Existing request sources and the dock already describe what is
		// pending; a second waiting line would repeat the same information.
		a.waiting = true
		return a
	}
	var latest *domain.Item
	for i := len(d.Items) - 1; i >= 0; i-- {
		item := d.Items[i]
		if item.TurnID != turn.ID || item.WorkID != "" {
			continue
		}
		eligible := item.Kind == "tool" || item.Kind == "assistant" && (item.ActivityOrder > 0 || strings.TrimSpace(item.Text) != "") || item.Kind == "activity" && (item.Presentation == "compaction" || item.Presentation == "reasoning")
		if eligible && (latest == nil || item.ActivityOrder > latest.ActivityOrder) {
			latest = &d.Items[i]
			a.index = i
		}
	}
	// ActivityOrder records starts, never output delivery. Older workers/history
	// without this metadata retain their stable display-order fallback.
	if latest != nil {
		item := *latest
		a.item, a.kind = item.ID, item.Kind
		switch item.Kind {
		case "assistant":
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
			if item.Presentation == "compaction" {
				if !item.Final {
					a.compaction = item.ID
				}
				return a
			}
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

// A same-turn insertion starts a new visible elapsed interval. Worker-owned
// timestamps keep it stable across clients and history reloads; the original
// turn start and totals remain intact.
func (v *View) runningElapsedStart(turn domain.Turn) *time.Time {
	start := turn.StartedAt
	items := v.Model.Details[v.Model.SessionID].Items
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Kind != "user" || item.WorkID != "" || item.TurnID != turn.ID || item.SentAt == nil || item.SentAt.IsZero() {
			continue
		}
		if start == nil || item.SentAt.After(*start) {
			start = item.SentAt
		}
		break
	}
	return start
}

func (v *View) activeConversationTurn() (domain.Turn, bool) {
	turns := v.Model.Details[v.Model.SessionID].Turns
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Status.Active() {
			return turns[i], true
		}
	}
	return domain.Turn{}, false
}

// Live progress is the transcript's tail, even when an older tool delivers
// output into a later display record. Keep persisted content order untouched.
func (v *View) bottomActivityRows(rows []transcriptRow) []transcriptRow {
	id := v.currentCompaction()
	if id == "" {
		return rows
	}
	for i, row := range rows {
		if row.item.ID == id && i < len(rows)-1 {
			copy(rows[i:], rows[i+1:])
			rows[len(rows)-1] = row
			break
		}
	}
	return rows
}

// This visual clock preserves progress animation phase across insertions.
// Elapsed labels independently use the latest accepted input's timestamp.
type runningProgress struct {
	session, epoch, turn string
	label                string
	started              time.Time
	handoff              *progressHandoff
	caption              string
	pendingCaption       string
	captionSince         time.Time
}

const (
	progressFadeDuration    = 140 * time.Millisecond
	progressReclaimDuration = 180 * time.Millisecond
	toolCaptionDelay        = 180 * time.Millisecond
)

// Coalesce brief tool activity before starting a text/width transition. This
// only filters presentation: completion and other phases still replace the
// caption immediately, and the worker's execution state is never delayed.
// Keep it on the turn so disclosure changes and the offscreen proxy share it.
func (p *runningProgress) activityCaption(c *n.Context, label string) string {
	if label != "正在执行命令" && label != "正在执行工具" {
		p.caption, p.pendingCaption = label, ""
		return label
	}
	if p.caption == "" {
		p.caption = "进行中"
	}
	if p.caption == label {
		p.pendingCaption = ""
		return label
	}
	if p.pendingCaption != label {
		p.pendingCaption, p.captionSince = label, c.Now()
	}
	if c.Now().Sub(p.captionSince) < toolCaptionDelay {
		c.AnimationFrame()
		return p.caption
	}
	p.caption, p.pendingCaption = label, ""
	return label
}

type progressHandoff struct {
	group  string
	fadeAt time.Time
}

func (p *runningProgress) handoffFraction(now time.Time) float32 {
	if p.handoff == nil {
		return 0
	}
	return min(1, max(0, float32(now.Sub(p.handoff.fadeAt))/float32(progressFadeDuration)))
}

// Width reclamation continues the handoff's absolute timeline. It must not
// start a fresh per-element animation when the faded indicator unmounts.
func (p *runningProgress) handoffSlotExtent(now time.Time) float32 {
	if p.handoff == nil {
		return 1
	}
	t := float32(now.Sub(p.handoff.fadeAt.Add(progressFadeDuration))) / float32(progressReclaimDuration)
	return 1 - n.EaseOut(min(1, max(0, t)))
}

func (v *View) progressFor(c *n.Context, turn domain.Turn) *runningProgress {
	p := v.runningProgress
	if p == nil || p.session != v.Model.SessionID || p.epoch != v.Model.ConnectionEpoch || p.turn != turn.ID {
		started := c.Now()
		if turn.StartedAt != nil && !turn.StartedAt.IsZero() {
			started = *turn.StartedAt
		}
		p = &runningProgress{session: v.Model.SessionID, epoch: v.Model.ConnectionEpoch, turn: turn.ID, started: started}
		v.runningProgress = p
	}
	return p
}

// The live bottom tool group owns progress in both disclosure states. On first
// prose, use the actual disclosure deadline (no delay for an already folded
// group), then continue crossfade and slot reclamation on the same timeline.
func (v *View) syncRunningGroup(c *n.Context, rows []transcriptRow, detail domain.SessionDetail) {
	previousGroup, previousTurn := v.runningGroup, v.activityTurn
	v.runningGroup, v.activityTurn = "", ""
	if !v.Model.Connected || len(rows) == 0 {
		v.runningProgress = nil
		return
	}
	var turn domain.Turn
	for i := len(detail.Turns) - 1; i >= 0; i-- {
		if detail.Turns[i].Status.Active() {
			turn = detail.Turns[i]
			break
		}
	}
	activity := v.runningActivity(turn)
	if turn.ID == "" || activity.waiting || activity.compaction != "" {
		v.runningProgress = nil
		return
	}
	oldProgress := v.runningProgress
	p := v.progressFor(c, turn)
	if p != oldProgress {
		previousGroup, previousTurn = "", ""
	}
	row := rows[len(rows)-1]
	group := row.group
	if group == "" {
		group = row.toolParent
	}
	if row.item.TurnID != turn.ID {
		group = ""
	}
	if activity.kind != "tool" {
		group = ""
	}
	if group != "" {
		for first, item := range detail.Items {
			if item.ID == row.item.ID {
				if activity.index < first || activity.index > row.end {
					group = ""
				}
				break
			}
		}
	}
	if group != "" {
		p.handoff = nil
		p.label = activity.label
		v.runningGroup, v.activityTurn = group, turn.ID
		return
	}
	if p.handoff == nil && previousGroup != "" && previousTurn == turn.ID && row.item.Kind == "assistant" {
		fadeAt := c.Now()
		if motion := v.toolMotions[previousGroup]; motion != nil {
			fadeAt = motion.start.Add(motion.duration)
		}
		// The outgoing group keeps the words it actually presented. The new
		// footer can resolve its own phase without retargeting that snapshot.
		if p.caption != "" {
			p.label = p.caption
		}
		p.pendingCaption = ""
		p.handoff = &progressHandoff{group: previousGroup, fadeAt: fadeAt}
	}
	if p.handoff != nil {
		if c.Preferences().ReduceMotion {
			p.handoff.fadeAt = c.Now().Add(-progressFadeDuration - progressReclaimDuration)
		}
		if c.Now().Before(p.handoff.fadeAt.Add(progressFadeDuration + progressReclaimDuration)) {
			c.AnimationFrame()
		}
	}
}

func elapsedActivityLabel(started, now time.Time) string {
	seconds := int64(max(time.Duration(0), now.Sub(started)) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	return fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
}

// Same-width ticks only repaint the clock on the next worker-relative second.
// A compact clock refreshes layout only when its text width actually changes
// (for example when seconds become minutes); shaping is cached between ticks.
// Compact tool titles expose the natural width to their owning handoff, so
// width removal has a single animation owner instead of another local clock.
func runningElapsed(c *n.Context, started *time.Time, color n.Color, compact bool) (n.Element, float32) {
	if started == nil || started.IsZero() {
		return n.Element{}, 0
	}
	since := *started
	font := n.Font{Family: c.Theme().Font, Size: 11, Weight: 400, Features: "tnum"}
	m := inlineFontMetrics("秒", font)
	width, _ := n.ShapeText("9999 分 59 秒", font, 0).Size()
	inset := float32(0)
	if compact {
		width, _ = n.ShapeText(elapsedActivityLabel(since, c.Now()), font, 0).Size()
		inset = 6
		width += inset
	}
	clock := n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: width, ObjectHeight: m.ascent, ObjectDescent: m.descent}).Font(font.Family).FontSize(11).FontFeatures("tnum").FixedLineHeight(20).TextColor(color).Label("运行时长")
	if compact {
		clock.Height(20).Shrink(0).SingleLine().Clip()
	}
	invalidate := c.Services().Invalidate
	var text string
	var layout *n.TextLayout
	clock.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		now := p.Now()
		value := elapsedActivityLabel(since, now)
		if value != text {
			layout = n.ShapeText(value, font, 0)
			text = value
			if compact {
				actual, _ := layout.Size()
				if math.Abs(float64(actual+inset-width)) > .5 {
					invalidate()
				}
			}
		}
		p.TextLayout(layout, r.X+inset, r.Y+m.ascent-layout.Lines()[0].Ascent, color)
		elapsed := max(time.Duration(0), now.Sub(since))
		p.After(time.Second - elapsed%time.Second)
	})
	return clock, width
}

func (v *View) runningStatus(c *n.Context, t *n.Theme, turn domain.Turn) {
	a := v.runningActivity(turn)
	if a.waiting {
		return
	}
	p := v.progressFor(c, turn)
	v.progressStatus(c, t, a, p)
}

type progressLabelExtent struct {
	target, shown float32
	release       time.Time
}

func (v *View) progressStatus(c *n.Context, t *n.Theme, a runningActivity, p *runningProgress) {
	if p != nil {
		a.label = p.activityCaption(c, a.label)
	}
	row := n.Row(c.Key("running-status")).Gap(6).AlignItems(n.Baseline)
	if p == nil || p.handoff == nil {
		row.Transition(fadeMotion(c, 120*time.Millisecond))
	}
	// Keep shimmer phase through the local receipt → authoritative turn
	// handoff. Elapsed time still comes only from the worker's timestamps.
	preparation := n.Local(row, "preparation-start", func() time.Time { return time.Time{} })
	phase := c.Now()
	if p == nil && preparation.IsZero() {
		*preparation = phase
	}
	if p != nil {
		phase = p.started
	}
	if !preparation.IsZero() {
		phase = *preparation
	}
	running := p != nil || a.label == "进行中"
	color := t.TextMuted
	if running {
		color = t.Success
	}
	color = row.AnimateColor("status-color", color, motionAnimation(c, 180*time.Millisecond))
	row.Children(func() {
		statusDot(c.Key("dot"), a.label, n.Font{Family: t.Font, Size: 11, Weight: 400}, color, running, phase)
		width, _ := n.ShapeText(a.label, n.Font{Family: t.Font, Size: 11, Weight: 400}, 0).Size()
		caption := n.Column(c.Key("activity-caption")).Height(20).Shrink(0).Clip()
		extent := n.Local(caption, "label-extent", func() progressLabelExtent {
			return progressLabelExtent{target: width, shown: width}
		})
		if extent.target != width {
			extent.target = width
			extent.release = time.Time{}
			if width < extent.shown {
				// Retain the outgoing words' raster until they have faded, then
				// reclaim the slot. Growth can move the clock immediately.
				extent.release = c.Now().Add(180 * time.Millisecond)
			}
		}
		shown := width
		if c.Preferences().ReduceMotion {
			caption.Jump("label-width", width)
		} else {
			if c.Now().Before(extent.release) {
				width = max(width, extent.shown)
				c.AnimationFrame()
			}
			shown = caption.AnimateTo("label-width", width, motionAnimation(c, 180*time.Millisecond))
		}
		extent.shown = shown
		caption.Width(shown).Children(func() {
			n.Text(c.Key("activity-label"), a.label).FontSize(11).FixedLineHeight(20).SingleLine().Shrink(0).TextColor(t.TextMuted).
				ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
		})
		// Clip the text and its outgoing snapshot together while reclaiming
		// width, keeping the adjacent clock on the same animation timeline.
		shimmer(c, caption, t.TextMuted, t.Text, phase)
		clock, _ := runningElapsed(c.Key("elapsed"), a.started, t.TextMuted, false)
		if clock.Valid() {
			// A newly available clock enters at the settled caption, rather
			// than flashing beside the outgoing long preparation label first.
			// Once visible, phase changes keep moving the existing clock.
			visible := n.Local(row, "clock-visible", func() bool { return false })
			if math.Abs(float64(shown-extent.target)) < .5 {
				*visible = true
			}
			alpha := float32(0)
			if *visible {
				alpha = 1
			}
			clock.Opacity(clock.AnimateTo("entrance", alpha, motionAnimation(c, 120*time.Millisecond)))
		}
	})
}

// Both are nominally separated by 8 DIP. Smaller metadata has less leading
// above its glyphs, so compensate using the same stable Chinese reference as
// inline typography, including the actual font fallback and configured size.
func (v *View) bodyMetadataGap() float32 {
	prefs := v.Model.Preferences
	inkTop := func(size, lineHeight float32) float32 {
		font := n.Font{Family: prefs.InterfaceFont, Size: size, Weight: 400, LineHeight: lineHeight / size}
		line := n.ShapeText("国", font, 0).Lines()[0]
		return line.Baseline + font.InkBounds("国").Y
	}
	bodySize := float32(prefs.ReplySize)
	return 8 + max(0, float32(math.Round(float64(inkTop(bodySize, bodySize*24/14)-inkTop(11, 20)))))
}

package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strings"
	"time"
)

type transcriptRow struct {
	key        string
	item       domain.Item
	end        int // original event index; -1 for an expanded group heading
	group      string
	count      int
	issues     int
	toolParent string
	opacity    float32
	footer     string
	preparing  string
	restarting bool
	toolResult string
}

// Keep arrival identity outside virtual rows: scrolling an existing call back
// into view must not replay its entrance. A new visit/hydration is a baseline.
type toolArrivalState struct {
	session    string
	epoch      string
	seen       map[string]bool
	motions    map[string]*toolDisclosureMotion
	footerKeys map[string]string // Restart waiting rows retain their native animation state.
}

// A turn receipt is its own stable row below a disclosure, so it follows the
// group's changing height rather than disappearing inside its clipped body.
func withTurnFooters(rows []transcriptRow, items []domain.Item, turns map[string]domain.Turn) []transcriptRow {
	result := make([]transcriptRow, 0, len(rows)+len(turns))
	for i, row := range rows {
		result = append(result, row)
		if i+1 < len(rows) && rows[i+1].toolResult != "" && rows[i+1].toolParent == row.toolParent {
			continue
		}
		if row.end >= 0 && row.item.TurnID != "" && (row.end == len(items)-1 || items[row.end+1].TurnID != row.item.TurnID) {
			if turn, ok := turns[row.item.TurnID]; ok && turn.Presentation != "compaction" {
				result = append(result, transcriptRow{key: "turn-footer:" + row.item.TurnID, footer: row.item.TurnID, end: -1, opacity: 1})
			}
		}
	}
	return result
}

func emptyTranscriptItem(item domain.Item) bool {
	return item.Kind == "assistant" && strings.TrimSpace(item.Text) == "" || item.Kind == "activity" && item.Presentation == "reasoning" && !activityHasBody(item)
}

// Project groups into the outer virtual list so expanded tool history still
// unmounts normally. While viewing, only nonempty agent prose auto-closes a group.
func (v *View) transcriptRows(items []domain.Item) []transcriptRow {
	v.toolsAnimating = false
	// A cached projection and a freshly hydrated history each start collapsed
	// when opened. Consume this only after actual session history is available;
	// pending-request snapshots can create an empty detail before hydration.
	collapseHistory := v.Model.FoldToolHistory[v.Model.SessionID] && v.Model.Details[v.Model.SessionID].Session.ID != ""
	if collapseHistory {
		delete(v.Model.FoldToolHistory, v.Model.SessionID)
	}
	if v.toolArrivals == nil || v.toolArrivals.session != v.Model.SessionID || v.toolArrivals.epoch != v.Model.ConnectionEpoch || collapseHistory {
		v.toolArrivals = &toolArrivalState{session: v.Model.SessionID, epoch: v.Model.ConnectionEpoch, motions: map[string]*toolDisclosureMotion{}}
	}
	arrivals := v.toolArrivals
	for turn := range arrivals.footerKeys {
		present := false
		for _, current := range v.Model.Details[v.Model.SessionID].Turns {
			if current.ID == turn {
				present = true
				break
			}
		}
		if !present {
			delete(arrivals.footerKeys, turn)
		}
	}
	seen := map[string]bool{}
	arrival := func(key string) float32 {
		seen[key] = true
		if arrivals.seen != nil && !arrivals.seen[key] && !v.reduceMotion {
			if arrivals.motions[key] == nil {
				arrivals.motions[key] = &toolDisclosureMotion{to: 1, start: v.frameTime, duration: 200 * time.Millisecond}
			}
		}
		if motion := arrivals.motions[key]; motion != nil {
			progress, active := motion.sample(v.frameTime)
			if active && !v.reduceMotion {
				v.toolsAnimating = true
				return progress
			}
			delete(arrivals.motions, key)
		}
		return 1
	}
	defer func() {
		arrivals.seen = seen
		for key := range arrivals.motions {
			if !seen[key] {
				delete(arrivals.motions, key)
			}
		}
	}()
	laterProse := make([]bool, len(items)+1)
	for i := len(items) - 1; i >= 0; i-- {
		laterProse[i] = laterProse[i+1] || items[i].Kind == "assistant" && strings.TrimSpace(items[i].Text) != ""
	}
	endedTurns := map[string]bool{}
	for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
		switch turn.Status {
		case domain.ExecutionCompleted, domain.ExecutionFailed, domain.ExecutionInterrupted, domain.ExecutionAbandoned:
			endedTurns[turn.ID] = true
		}
	}
	rows := make([]transcriptRow, 0, len(items))
	for i := 0; i < len(items); {
		item := items[i]
		if emptyTranscriptItem(item) {
			if len(rows) > 0 {
				rows[len(rows)-1].end = i
			}
			i++
			continue
		}
		if item.Kind != "tool" {
			progress := float32(1)
			if item.Kind != "user" && item.Kind != "assistant" && item.Kind != "request" && item.Kind != "request_result" {
				progress = arrival(item.ID)
			}
			rows = append(rows, transcriptRow{key: item.ID, item: item, end: i, opacity: progress})
			i++
			continue
		}
		end, count := i+1, 1
		for end < len(items) && items[end].TurnID == item.TurnID {
			if items[end].Kind == "tool" {
				count++
				end++
				continue
			}
			if emptyTranscriptItem(items[end]) {
				end++
				continue
			}
			break
		}
		key := "tool-group:" + v.Model.SessionID + "/" + item.ID
		disclosure := v.Model.ToolDisclosure(key)
		if collapseHistory {
			disclosure = disclosure.OpenHistory()
			v.Model.SetToolDisclosure(key, disclosure)
			delete(v.toolMotions, key)
		}
		if laterProse[end] && !disclosure.SawProse() {
			// Only previously displayed content exits. Hydrated history starts
			// folded without briefly mounting all of its tools.
			_, visible := v.historyRowBounds[key]
			for j := i; !visible && j < end; j++ {
				_, visible = v.historyRowBounds[items[j].ID]
			}
			if visible && !disclosure.Closed() && !v.reduceMotion {
				from := float32(1)
				if motion := v.toolMotions[key]; motion != nil {
					from, _ = motion.sample(v.frameTime)
				}
				if v.toolMotions == nil {
					v.toolMotions = map[string]*toolDisclosureMotion{}
				}
				v.toolMotions[key] = &toolDisclosureMotion{from: from, to: 0, start: v.frameTime, duration: 160 * time.Millisecond}
			}
			disclosure = disclosure.ObserveProse()
			v.Model.SetToolDisclosure(key, disclosure)
		}
		opacity, show := float32(1), !disclosure.Closed()
		if motion := v.toolMotions[key]; motion != nil {
			if v.reduceMotion {
				motion.duration = min(motion.duration, 60*time.Millisecond)
			}
			var active bool
			opacity, active = motion.sample(v.frameTime)
			if active {
				v.toolsAnimating, show = true, true
			} else {
				delete(v.toolMotions, key)
			}
		}
		row := transcriptRow{key: key, item: item, end: -1, group: key, count: count, opacity: arrival(key)}
		for _, call := range items[i:end] {
			if call.Kind == "tool" && toolIssueLabel(call) != "" {
				row.issues++
			}
		}
		if !show {
			row.end = end - 1
		}
		rows = append(rows, row)
		for j := i; j < end; j++ {
			if items[j].Kind == "tool" {
				progress := min(opacity, arrival(items[j].ID))
				if show {
					rows = append(rows, transcriptRow{key: items[j].ID, item: items[j], end: j, toolParent: key, opacity: progress})
				}
			} else if show && len(rows) > 0 {
				rows[len(rows)-1].end = j
			}
		}
		// A quiet gap between calls is not a group boundary: another call
		// may still join. Only prose or a confirmed turn end seals the group.
		if result := toolGroupResult(items[i:end]); result != "" && (laterProse[end] || endedTurns[item.TurnID]) {
			progress := min(opacity, arrival(key+":result"))
			if show {
				rows = append(rows, transcriptRow{key: key + ":result", item: item, end: end - 1, toolParent: key, toolResult: result, opacity: progress})
			}
		}
		i = end
	}
	for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
		key := "turn-footer:" + turn.ID
		// Restart's local waiting line already occupies this slot. Acceptance
		// replaces its identity, rather than introducing another visible element.
		if m := v.historyExit; m != nil && m.in.Restart && m.in.SessionID == v.Model.SessionID && m.phase != historyExiting {
			if _, old := m.turns[turn.ID]; !old {
				seen[key] = true
				delete(arrivals.motions, key)
				continue
			}
		}
		arrival(key)
	}
	return rows
}
func (v *View) toolGroup(c *n.Context, t *n.Theme, row transcriptRow, index int) {
	label := fmt.Sprintf("%d 次工具调用", row.count)
	button := n.ButtonBase(c).FillWidth().Height(20).Justify(n.Start).Label(label)
	color := textHoverColor(c, button, t.TextMuted)
	live := v.runningGroup == row.group
	if button.Children(func() {
		heading := n.Row(c).FillWidth().Gap(0).AlignItems(n.Baseline)
		if live {
			heading.Label("工具组运行状态")
		}
		heading.Children(func() {
			angle := float32(0)
			if v.Model.ToolDisclosure(row.group).Closed() {
				angle = -90
			}
			font := n.Font{Family: c.Theme().Font, Size: 11, Weight: 400}
			width, alpha, phase := float32(0), float32(0), c.Now()
			if live {
				width, alpha, phase = 12, 1-v.runningProgress.handoffFraction(c.Now()), v.runningProgress.started
			}
			// Fade and reclaim use one absolute turn timeline. Native local
			// width animation is only used for exits outside that handoff.
			p := v.runningProgress
			handoff := p != nil && p.handoff != nil && p.handoff.group == row.group
			extent := float32(0)
			if live {
				extent = 1
			}
			if handoff {
				extent = p.handoffSlotExtent(c.Now())
			}
			prefix := statusDot(c.Key("group-dot"), label, font, t.Success, live, phase).Opacity(alpha).Shrink(0).SingleLine().Height(20).Clip()
			if handoff {
				prefix.Width(prefix.Jump("prefix-width", 12*extent))
			} else {
				prefix.Width(prefix.AnimateTo("prefix-width", width, motionAnimation(c, progressReclaimDuration)))
			}
			if !live {
				prefix.Label("")
			}

			text := n.Text(c.Key("group-label"), label).FontSize(11).FixedLineHeight(20).TextColor(color).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)})
			if live {
				shimmer(c, text, color, t.Text, v.runningProgress.started)
			}
			for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
				if turn.ID == row.item.TurnID {
					clock, natural := runningElapsed(c.Key("group-elapsed"), turn.StartedAt, t.TextMuted, true)
					if handoff {
						clock.Width(clock.Jump("elapsed-width", natural*extent))
					} else {
						clock.Width(clock.AnimateTo("elapsed-width", natural*extent, motionAnimation(c, progressReclaimDuration)))
					}
					clock.Opacity(alpha)
					break
				}
			}
			inlineSymbol(c.Key("group-chevron"), chevron, label, font, 16, color, angle).Margin(0, 0, 0, 6)

			if row.issues > 0 && v.Model.ToolDisclosure(row.group).Closed() {
				animation := motionAnimation(c, 160*time.Millisecond)
				n.Text(c.Key("issues"), fmt.Sprintf("%d 项未成功", row.issues)).Margin(0, 0, 0, 6).FontSize(11).FixedLineHeight(20).TextColor(color).
					Transition(disclosureMotion(c, 160*time.Millisecond)).ContentTransition(n.ContentTransition{Numeric: true, Animation: &animation})
			}
		})
	}).Clicked() {
		list := v.lists[v.Model.SessionID]
		list.KeepRowWithinContent(index, button.Bounds().Y-list.Bounds(c).Y)
		from, to := float32(1), float32(0)
		if v.Model.ToolDisclosure(row.group).Closed() {
			from, to = 0, 1
		}
		if motion := v.toolMotions[row.group]; motion != nil {
			from, _ = motion.sample(c.Now())
		}
		if v.toolMotions == nil {
			v.toolMotions = map[string]*toolDisclosureMotion{}
		}
		v.toolMotions[row.group] = &toolDisclosureMotion{from: from, to: to, start: c.Now(), duration: motionAnimation(c, 160*time.Millisecond).Duration}
		v.Model.SetToolDisclosure(row.group, v.Model.ToolDisclosure(row.group).Toggle())
		c.AnimationFrame()
	}
}

var rightChevron = svg("M8 5l5 5-5 5")

// A tool receipt describes closed calls, never a guessed turn completion.
func toolGroupResult(items []domain.Item) string {
	result := "completed"
	for _, item := range items {
		if item.Kind != "tool" {
			continue
		}
		if !item.Final {
			return ""
		}
		if toolIssueLabel(item) != "" || item.Status == "unknown" {
			result = "ended"
		}
	}
	return result
}

var toolCompleteIcon = svg("M18 10a8 8 0 1 1-16 0a8 8 0 0 1 16 0M6 10l3 3 5-6")
var toolEndedIcon = svg("M18 10a8 8 0 1 1-16 0a8 8 0 0 1 16 0M6 10h8")

func (v *View) toolTimelineItem(c *n.Context, t *n.Theme, row transcriptRow) {
	icon := activityIcon(row.item.Presentation)
	if row.toolResult == "" {
		v.item(c, t, row.item)
	} else {
		label := "已完成"
		icon = toolCompleteIcon
		if row.toolResult == "ended" {
			label, icon = "调用结束", toolEndedIcon
		}
		n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
	}
	// A small neutral mask interrupts the rail around each native glyph.
	n.Column(c).Absolute().Left(0).Top(0).Size(16, 20).AlignItems(n.Center).Justify(n.Center).Background(t.Background).Children(func() {
		n.Icon(c, icon).Size(14, 14).TextColor(t.TextMuted).Role(n.RoleNone)
	})
}

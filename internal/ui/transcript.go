package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
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
	toolTitle  bool
	toolEnded  bool
	toolGap    bool
	opacity    float32
	footer     string
	preparing  string
	restarting bool
	toolResult string
}

// Keep arrival identity outside virtual rows: scrolling an existing call back
// into view must not replay its entrance. A new visit/hydration is a baseline.
type toolArrivalState struct {
	latestCommand string
	commands      []string
	session       string
	epoch         string
	seen          map[string]bool
	motions       map[string]*toolDisclosureMotion
	footerKeys    map[string]string // Restart waiting rows retain their native animation state.
}

// An automatic compaction or hook can arrive before the adapter can confirm
// the continuation. Keep the marker at the start of its turn in the view.
func withGoalContinuationMarkers(rows []transcriptRow) []transcriptRow {
	var markers map[string]int
	for i, row := range rows {
		if row.item.Presentation == domain.GoalContinuationPresentation {
			if markers == nil {
				markers = make(map[string]int)
			}
			markers[row.item.TurnID] = i
		}
	}
	for i, row := range rows {
		if len(markers) == 0 {
			return rows
		}
		index, ok := markers[row.item.TurnID]
		if !ok {
			continue
		}
		if index == i {
			delete(markers, row.item.TurnID)
			continue
		}
		ordered := append(make([]transcriptRow, 0, len(rows)), rows[:i]...)
		for _, entry := range rows[i:] {
			if at, pending := markers[entry.item.TurnID]; pending {
				ordered = append(ordered, rows[at])
				delete(markers, entry.item.TurnID)
			}
			if entry.item.Presentation != domain.GoalContinuationPresentation {
				ordered = append(ordered, entry)
			}
		}
		return ordered
	}
	return rows
}

// A turn receipt is its own stable row below a disclosure, so it follows the
// group's changing height rather than disappearing inside its clipped body.
func withTurnFooters(rows []transcriptRow, turns map[string]domain.Turn) []transcriptRow {
	last := map[string]int{}
	for i, row := range rows {
		if row.end >= 0 && row.item.TurnID != "" {
			last[row.item.TurnID] = i
		}
	}
	footerCount := 0
	for id := range last {
		if turn, ok := turns[id]; ok && turn.Presentation != "compaction" {
			footerCount++
		}
	}
	if footerCount == 0 {
		return rows
	}
	// The projection reserves receipt slots. Insert backwards in that same
	// buffer instead of copying the entire expanded history every frame.
	length := len(rows)
	rows = append(rows, make([]transcriptRow, footerCount)...)
	to := len(rows) - 1
	// Late output from an earlier turn can follow the active turn's records.
	// Its result stays in history; the active status remains the final row.
	for id, turn := range turns {
		if _, present := last[id]; present && turn.Status.Active() && turn.Presentation != "compaction" {
			rows[to] = transcriptRow{key: "turn-footer:" + id, footer: id, end: -1, opacity: 1}
			to--
			delete(last, id)
		}
	}
	for i := length - 1; i >= 0; i-- {
		row := rows[i]
		if end, present := last[row.item.TurnID]; row.end >= 0 && row.item.TurnID != "" && present && end == i {
			if turn, ok := turns[row.item.TurnID]; ok && turn.Presentation != "compaction" {
				rows[to] = transcriptRow{key: "turn-footer:" + row.item.TurnID, footer: row.item.TurnID, end: -1, opacity: 1}
				to--
			}
		}
		rows[to] = row
		to--
	}
	return rows
}

// Keep the extra turn-to-user clearance outside the tool body's reveal and
// outside the message's own bounds, which history navigation uses as anchors.
func withToolUserGaps(rows []transcriptRow) []transcriptRow {
	count := 0
	for i := 1; i < len(rows); i++ {
		if rows[i-1].item.Kind == "tool" && rows[i].item.Kind == "user" {
			count++
		}
	}
	if count == 0 {
		return rows
	}
	length := len(rows)
	rows = append(rows, make([]transcriptRow, count)...)
	to, nextUser := len(rows)-1, ""
	for i := length - 1; i >= 0; i-- {
		row := rows[i]
		if row.item.Kind == "tool" && nextUser != "" {
			rows[to] = transcriptRow{key: "tool-user-gap:" + nextUser, toolGap: true, end: -1, opacity: 1}
			to--
		}
		rows[to] = row
		to--
		nextUser = ""
		if row.item.Kind == "user" {
			nextUser = row.key
		}
	}
	return rows
}

// Project groups into the outer virtual list so expanded tool history still
// unmounts normally. Any following visible non-tool content auto-closes a group.
func (v *View) transcriptRows(items []domain.Item) []transcriptRow {
	receipts := requestReceipts(v.Model.Details[v.Model.SessionID].Requests)
	baseline := v.historyBaseline
	defer func() { v.historyBaseline = false }()
	v.toolsAnimating = false
	// Opening folds history except for the latest group still executing.
	// Consume this only after actual session history is available;
	// pending-request snapshots can create an empty detail before hydration.
	collapseHistory := v.Model.FoldToolHistory[v.Model.SessionID] && v.Model.Details[v.Model.SessionID].Session.ID != ""
	if collapseHistory {
		delete(v.Model.FoldToolHistory, v.Model.SessionID)
	}
	if v.toolArrivals == nil || v.toolArrivals.session != v.Model.SessionID || v.toolArrivals.epoch != v.Model.ConnectionEpoch || collapseHistory {
		v.toolArrivals = &toolArrivalState{session: v.Model.SessionID, epoch: v.Model.ConnectionEpoch, motions: map[string]*toolDisclosureMotion{}}
	}
	arrivals := v.toolArrivals
	commands := arrivals.commands[:0]
	commandsChanged := false
	for _, item := range items {
		receipts.recordResult(item)
		if commandItem(item) {
			if len(commands) >= len(arrivals.commands) || arrivals.commands[len(commands)] != item.ID {
				commandsChanged = true
			}
			commands = append(commands, item.ID)
		}
	}
	if commandsChanged || len(commands) != len(arrivals.commands) {
		v.syncCommandOutputs(commands, &arrivals.latestCommand, "")
	}
	arrivals.commands = commands
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
	seen := make(map[string]bool, len(items)+len(v.Model.Details[v.Model.SessionID].Turns))
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
	laterContent := make([]bool, len(items)+1)
	latestTool := -1
	groups := 0
	var nextKind, nextTurn string
	for i := len(items) - 1; i >= 0; i-- {
		visible := !domain.HistorySpacer(items[i])
		laterContent[i] = laterContent[i+1] || items[i].Kind != "tool" && visible
		if visible {
			if items[i].Kind == "tool" && (nextKind != "tool" || nextTurn != items[i].TurnID) {
				groups++
			}
			nextKind, nextTurn = items[i].Kind, items[i].TurnID
		}
		if latestTool < 0 && items[i].Kind == "tool" {
			latestTool = i
		}
	}
	endedTurns := map[string]bool{}
	activeTurn := ""
	for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
		status := turn.Status
		if status.Active() && v.stoppingTurn(turn.ID) {
			status = domain.ExecutionInterrupted
		}
		switch status {
		case domain.ExecutionStarting, domain.ExecutionRunning:
			activeTurn = turn.ID
		case domain.ExecutionCompleted, domain.ExecutionFailed, domain.ExecutionInterrupted, domain.ExecutionAbandoned:
			endedTurns[turn.ID] = true
		}
	}
	// Each tool run adds a title, trailing summary, optional result and gap.
	// Without these slots a large run reallocates as its last row is appended.
	rows := make([]transcriptRow, 0, len(items)+4*groups+len(v.Model.Details[v.Model.SessionID].Turns))
	for i := 0; i < len(items); {
		item := items[i]
		if domain.HistorySpacer(item) {
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
			rows = append(rows, transcriptRow{key: item.ID, item: receipts.attach(item), end: i, opacity: progress})
			i++
			continue
		}
		end, count := i+1, 1
		executing := !item.Final
		for end < len(items) && items[end].TurnID == item.TurnID {
			if items[end].Kind == "tool" {
				executing = executing || !items[end].Final
				count++
				end++
				continue
			}
			if domain.HistorySpacer(items[end]) {
				end++
				continue
			}
			break
		}
		key := "tool-group:" + v.Model.SessionID + "/" + item.ID
		var summary *domain.HistoryToolGroup
		if h := v.Model.Details[v.Model.SessionID].History; h != nil {
			for j := range h.Groups {
				g := &h.Groups[j]
				if item.Order >= g.First && item.Order <= g.Last && item.TurnID == g.TurnID {
					key = "tool-group:" + v.Model.SessionID + "/" + g.ID
					count = g.Count
					summary = g
					break
				}
			}
		}
		disclosure := v.Model.ToolDisclosure(key)
		if baseline {
			if _, known := v.Model.Collapsed[key]; !known {
				disclosure = disclosure.OpenHistory()
				v.Model.SetToolDisclosure(key, disclosure)
			}
		}
		if collapseHistory {
			disclosure = disclosure.OpenHistory()
			if !v.Model.Preferences.CollapseLatestTools && v.Model.Connected && activeTurn != "" && item.TurnID == activeTurn && latestTool < end && executing && !laterContent[end] {
				disclosure = app.ToolOpenedBeforeProse
			}
			v.Model.SetToolDisclosure(key, disclosure)
			delete(v.toolMotions, key)
		}
		if laterContent[end] && !disclosure.SawProse() {
			// The persisted prose marker records this one-time boundary for
			// requests, inserted user messages and other non-tool content too.
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
		if summary != nil {
			row.issues = summary.Issues
		}
		row.end = end - 1
		// Keep the title's zero-height row while folded so the one-line
		// summary can hand its stable reading anchor to the expanded title.
		titleOpacity := min(opacity, arrival(key+":title"))
		if !show {
			titleOpacity = 0
		}
		title := transcriptRow{key: key + ":title", item: item, end: -1, toolParent: key, toolTitle: true, opacity: titleOpacity}
		if endedTurns[item.TurnID] {
			// Finished history has one count heading in both disclosure states.
			// It also owns the original event boundary when its body is hidden.
			title.group, title.count, title.issues = key, count, row.issues
			title.toolEnded, title.end, title.opacity = true, end-1, arrival(key+":title")
		}
		rows = append(rows, title)
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
		// may still join. Non-tool content or a confirmed turn end seals it.
		if result := toolGroupResult(items[i:end]); result != "" && (laterContent[end] || endedTurns[item.TurnID]) {
			progress := min(opacity, arrival(key+":result"))
			if show {
				rows = append(rows, transcriptRow{key: key + ":result", item: item, end: end - 1, toolParent: key, toolResult: result, opacity: progress})
			}
		}
		if !title.toolEnded {
			rows = append(rows, row)
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
	return withGoalContinuationMarkers(rows)
}

const (
	toolGlyphSlot     = 16
	toolTimelineInset = toolGlyphSlot
	toolTimelineGap   = 8
	toolSummaryGap    = 6
)

func toolGlyph(c *n.Context, icon *n.SVG, label string, font n.Font, color n.Color) n.Element {
	return n.Row(c).Size(toolGlyphSlot, 20).Shrink(0).AlignItems(n.Baseline).Justify(n.Center).
		Children(func() { inlineSymbol(c, icon, label, font, 14, color) })
}

func (v *View) toolGroup(c *n.Context, t *n.Theme, row transcriptRow, index int) {
	label := fmt.Sprintf("%d 次工具调用", row.count)
	// Once folded, this summary takes the title's place as the sole header.
	// While expanded (including its exit), the trailing status is read-only.
	folded := v.Model.ToolDisclosure(row.group).Closed() && v.toolMotions[row.group] == nil
	var heading n.Element
	color := t.TextMuted
	if folded {
		heading = n.ButtonBase(c).AlignSelf(n.Start).Justify(n.Start)
		color = textHoverColor(c, heading, color)
	} else {
		heading = n.Row(c).FillWidth()
	}
	heading.Height(20).Label(label)
	live := v.runningGroup == row.group
	heading.Children(func() {
		heading := n.Row(c).Gap(0).AlignItems(n.Baseline)
		if live {
			heading.Label("工具组运行状态")
		}
		heading.Children(func() {
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
			toolGlyph(c.Key("group-command-icon"), commandTerminalIcon, label, font, t.TextMuted).Margin(0, toolSummaryGap, 0, 0)

			// One continuous sweep covers the count and its changing metadata.
			// Keep the turn's phase; text transitions and clock ticks never restart it.
			summary := n.Row(c.Key("group-summary")).Gap(0).AlignItems(n.Baseline).Shrink(0)
			if live {
				shimmer(c, summary, color, t.Text, v.runningProgress.started)
			}
			summary.Children(func() {
				n.Text(c.Key("group-label"), label).FontSize(11).FixedLineHeight(20).TextColor(color).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)})
				if live || handoff {
					phase := v.runningProgress.label
					if phase == "" {
						phase = "进行中"
					}
					if !handoff {
						phase = v.runningProgress.activityCaption(c, phase)
					}
					w, _ := n.ShapeText(phase, font, 0).Size()
					// Padding must leave with the slot; native layout cannot
					// shrink a box below its remaining padding.
					caption := n.Text(c.Key("group-phase"), phase).SingleLine().FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).Padding(0, 0, 0, 6*extent).Clip().Opacity(alpha).
						ContentTransition(n.ContentTransition{Animation: animationPointer(c, progressReclaimDuration)})
					if handoff {
						caption.Width(caption.Jump("phase-width", (w+6)*extent))
					} else {
						caption.Width(caption.AnimateTo("phase-width", (w+6)*extent, motionAnimation(c, progressReclaimDuration)))
					}
				}
				for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
					if !live && !handoff {
						break
					}
					if turn.ID == row.item.TurnID {
						clock, natural := runningElapsed(c.Key("group-elapsed"), v.runningElapsedStart(turn), t.TextMuted, true)
						if handoff {
							clock.Width(clock.Jump("elapsed-width", natural*extent))
						} else {
							clock.Width(clock.AnimateTo("elapsed-width", natural*extent, motionAnimation(c, progressReclaimDuration)))
						}
						clock.Opacity(alpha)
						break
					}
				}
				if row.issues > 0 && v.Model.ToolDisclosure(row.group).Closed() {
					animation := motionAnimation(c, 160*time.Millisecond)
					n.Text(c.Key("issues"), fmt.Sprintf("%d 项未成功", row.issues)).Margin(0, 0, 0, 6).FontSize(11).FixedLineHeight(20).TextColor(color).
						Transition(disclosureMotion(c, 160*time.Millisecond)).ContentTransition(n.ContentTransition{Numeric: true, Animation: &animation})
				}
			})
			if folded {
				inlineSymbol(c.Key("group-chevron"), rightChevron, label, font, 14, color).Margin(0, 0, 0, 6)
			}
		})
	})
	if folded && heading.Clicked() {
		v.toggleToolGroup(c, row.group, index-1, heading)
	}
}

func (v *View) toolGroupTitle(c *n.Context, t *n.Theme, row transcriptRow, index int) {
	label, angle := "执行工具", float32(0)
	closed := v.Model.ToolDisclosure(row.toolParent).Closed()
	if row.toolEnded {
		label = fmt.Sprintf("%d 次工具调用", row.count)
		if closed {
			angle = -90
		}
	}
	button := n.ButtonBase(c).AlignSelf(n.Start).Height(20).Justify(n.Start).Label(label)
	color := textHoverColor(c, button, t.TextMuted)
	button.Children(func() {
		n.Row(c).Gap(toolSummaryGap).AlignItems(n.Baseline).Children(func() {
			font := n.Font{Family: t.Font, Size: 11, Weight: 400}
			if row.toolEnded {
				toolGlyph(c.Key("group-command-icon"), commandTerminalIcon, label, font, t.TextMuted)
			}
			n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(color)
			if row.toolEnded && closed && row.issues > 0 {
				n.Text(c, fmt.Sprintf("%d 项未成功", row.issues)).FontSize(11).FixedLineHeight(20).TextColor(color)
			}
			inlineSymbol(c, chevron, label, font, 14, color, angle)
		})
	})
	if button.Clicked() {
		v.toggleToolGroup(c, row.toolParent, index, button)
	}
}

func (v *View) toggleToolGroup(c *n.Context, key string, index int, button n.Element) {
	from, to := float32(1), float32(0)
	opening := v.Model.ToolDisclosure(key).Closed()
	if opening {
		from, to = 0, 1
	}
	if motion := v.toolMotions[key]; motion != nil {
		from, _ = motion.sample(c.Now())
	}
	if list := v.lists[v.Model.SessionID]; list != nil {
		// The folded summary hands its position to the top title on opening;
		// closing uses that same title anchor throughout the transition.
		list.KeepRowWithinContent(index, button.Bounds().Y-list.Bounds(c).Y)
	}
	if v.toolMotions == nil {
		v.toolMotions = map[string]*toolDisclosureMotion{}
	}
	v.toolMotions[key] = &toolDisclosureMotion{from: from, to: to, start: c.Now(), duration: motionAnimation(c, 160*time.Millisecond).Duration}
	v.Model.SetToolDisclosure(key, v.Model.ToolDisclosure(key).Toggle())
	c.AnimationFrame()
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

func (v *View) toolTimelineItem(c *n.Context, t *n.Theme, row transcriptRow, inset float32) {
	icon := activityIcon(row.item.Presentation)
	label := row.item.Name
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	if commandItem(row.item) {
		icon = commandActivityIcon(row.item.Name)
		font.Family, font.Size = "Menlo", 11
	}
	if row.toolResult == "" {
		v.item(c, t, row.item)
	} else {
		label = "已完成"
		font.Family, font.Size = t.Font, 11
		icon = toolCompleteIcon
		if row.toolResult == "ended" {
			label, icon = "调用结束", toolEndedIcon
		}
		n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
	}
	// A small neutral mask interrupts the rail around each native glyph.
	toolGlyph(c, icon, label, font, t.TextMuted).Absolute().Left(inset).Top(0).Background(t.Background)
}

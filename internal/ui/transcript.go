package ui

import (
	"fmt"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/conversationui"
	"shioji/internal/domain"
)

type transcriptRow struct {
	key         string
	item        domain.Item
	end         int // original event index; -1 for an expanded group heading
	group       string
	count       int
	toolParent  string
	toolTitle   bool
	toolStatus  bool
	toolReveal  float32
	toolGap     bool
	opacity     float32
	footer      string
	footerAfter string
	replyBefore *replyBoundary
	preparing   string
	restarting  bool
	toolResult  string
}

type replyBoundary struct {
	inputID    string
	after      string
	start, end *time.Time
}

func acceptedReplyInput(item domain.Item) bool {
	return item.Kind == "user" && item.WorkID == "" && (item.Status == "sent" || item.Status == "")
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
	steerFooter   *steerFooterHandoff
	heldSteerTurn string
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
	type turnTail struct {
		index          int
		start          *time.Time
		after          string
		replied, input bool
	}
	tails := make(map[string]turnTail, len(turns))
	var boundaries map[int]*replyBoundary
	for i, row := range rows {
		item := row.item
		turn, ok := turns[item.TurnID]
		if !ok || item.TurnID == "" || row.end < 0 || turn.Presentation == "compaction" {
			continue
		}
		tail, seen := tails[item.TurnID]
		if !seen {
			tail.start = turn.StartedAt
		}
		if acceptedReplyInput(item) {
			if tail.replied {
				if boundaries == nil {
					boundaries = make(map[int]*replyBoundary)
				}
				boundaries[tail.index] = &replyBoundary{inputID: item.ID, after: tail.after, start: tail.start, end: item.SentAt}
				tail.after = item.ID
			}
			if tail.input || tail.start == nil || item.SentAt != nil && item.SentAt.After(*tail.start) {
				tail.start = item.SentAt
			}
			tail.replied, tail.input = false, true
		} else if item.Kind == "assistant" && item.WorkID == "" && !domain.HistorySpacer(item) {
			tail.replied = true
		}
		tail.index = i
		tails[item.TurnID] = tail
	}
	footerCount := len(tails) + len(boundaries)
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
	for id, tail := range tails {
		if turns[id].Status.Active() {
			rows[to] = transcriptRow{key: "turn-footer:" + id, footer: id, footerAfter: tail.after, end: -1, opacity: 1}
			to--
			delete(tails, id)
		}
	}
	for i := length - 1; i >= 0; i-- {
		row := rows[i]
		if tail, present := tails[row.item.TurnID]; present && tail.index == i {
			rows[to] = transcriptRow{key: "turn-footer:" + row.item.TurnID, footer: row.item.TurnID, footerAfter: tail.after, end: -1, opacity: 1}
			to--
		}
		if boundary := boundaries[i]; boundary != nil {
			rows[to] = transcriptRow{key: "reply-before:" + boundary.inputID, footer: row.item.TurnID, footerAfter: boundary.after, replyBefore: boundary, end: -1, opacity: 1}
			to--
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
		if (rows[i-1].toolTitle || rows[i-1].toolParent != "") && rows[i].item.Kind == "user" {
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
		if (row.toolTitle || row.toolParent != "") && nextUser != "" {
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
// unmounts normally. A same-turn user insertion preserves the preceding
// group's disclosure when it becomes a separate reading block.
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
	nextContent := make([]int, len(items)+1)
	nextContent[len(items)] = len(items)
	latestTool := -1
	spans := app.ToolSpans(items, v.Model.Details[v.Model.SessionID].History, domain.HistorySpacer)
	inToolSpan := make([]bool, len(items))
	for start, span := range spans {
		for j := start; j < span.End; j++ {
			inToolSpan[j] = true
		}
	}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		nextContent[i] = nextContent[i+1]
		if !inToolSpan[i] && !domain.HistorySpacer(item) {
			nextContent[i] = i
		}
		if latestTool < 0 && inToolSpan[i] && !domain.HistorySpacer(item) {
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
	// Each tool run adds a title, optional trailing summary and gap.
	// Without these slots a large run reallocates as its last row is appended.
	rows := make([]transcriptRow, 0, len(items)+3*len(spans)+len(v.Model.Details[v.Model.SessionID].Turns))
	for i := 0; i < len(items); {
		item := items[i]
		if domain.HistorySpacer(item) {
			if len(rows) > 0 {
				rows[len(rows)-1].end = i
			}
			i++
			continue
		}
		span, grouped := spans[i]
		if !grouped {
			progress := float32(1)
			if item.Kind == "user" {
				seen[item.ID] = true
			}
			if item.Kind != "user" && item.Kind != "assistant" && item.Kind != "request" && item.Kind != "request_result" {
				progress = arrival(item.ID)
			}
			rows = append(rows, transcriptRow{key: item.ID, item: receipts.attach(item), end: i, opacity: progress})
			i++
			continue
		}
		end, count, loaded := span.End, span.Count, span.Loaded
		executing := span.Unfinished
		key := "tool-group:" + v.Model.SessionID + "/" + span.ID
		disclosure := v.Model.ToolDisclosure(key)
		following := nextContent[end]
		hasLaterContent := following < len(items)
		if baseline {
			if _, known := v.Model.Collapsed[key]; !known {
				disclosure = disclosure.OpenHistory()
				v.Model.SetToolDisclosure(key, disclosure)
			}
		}
		if collapseHistory {
			disclosure = disclosure.OpenHistory()
			if !v.Model.Preferences.CollapseLatestTools && v.Model.Connected && activeTurn != "" && item.TurnID == activeTurn && latestTool < end && executing && !hasLaterContent {
				disclosure = app.ToolOpenedBeforeProse
			}
			v.Model.SetToolDisclosure(key, disclosure)
			delete(v.toolMotions, key)
		}
		newBoundary := hasLaterContent && !disclosure.SawProse()
		if newBoundary {
			next := items[following]
			if !disclosure.Closed() && item.TurnID != "" && next.TurnID == item.TurnID && next.Kind == "user" {
				// Keep the open group and record the boundary so later output
				// cannot close it after the reader has moved past the insertion.
				disclosure = app.ToolOpenedAfterProse
			} else {
				// Only previously displayed content exits. Hydrated history
				// starts folded without briefly mounting all of its tools.
				_, visible := v.historyRowBounds[key]
				for j := i; !visible && j < end; j++ {
					_, visible = v.historyRowBounds[items[j].ID]
				}
				if visible && !disclosure.Closed() && !v.reduceMotion {
					v.animateToolGroup(key, 0, v.frameTime, toolDisclosureDuration)
				}
				disclosure = disclosure.ObserveProse()
			}
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
		heading := transcriptRow{key: key, item: item, end: end - 1, group: key, count: count, toolTitle: true, opacity: arrival(key)}
		for _, call := range items[i:end] {
			if domain.HistorySpacer(call) {
				continue
			}
			if !show {
				// Reopening an existing call uses only the group's reveal.
				seen[call.ID] = true
				delete(arrivals.motions, call.ID)
			}
		}
		// Only visible later content or a terminal turn seals a group. A
		// page containing only some of the calls cannot establish success.
		if (endedTurns[item.TurnID] || hasLaterContent) && loaded == count {
			heading.toolResult = toolGroupResult(items[i:end])
		}
		if show {
			heading.toolReveal = opacity
		}
		rows = append(rows, heading)
		for j := i; show && j < end; j++ {
			if !domain.HistorySpacer(items[j]) {
				progress := min(opacity, arrival(items[j].ID))
				rows = append(rows, transcriptRow{key: items[j].ID, item: items[j], end: j, toolParent: key, opacity: progress})
			} else {
				rows[len(rows)-1].end = j
			}
		}
		// Keep the outgoing status slot in the same height reveal when
		// another item takes over. The status renderer checks the current owner.
		liveTail := activeTurn != "" && item.TurnID == activeTurn && latestTool < end && !hasLaterContent
		progress := v.runningProgress
		outgoing := v.runningGroup == key || progress != nil && progress.handoff != nil && progress.handoff.group == key
		if show && v.Model.Connected && (liveTail || outgoing && v.toolMotions[key] != nil) {
			rows = append(rows, transcriptRow{key: key + ":status", item: item, end: end - 1, toolParent: key, toolStatus: true, opacity: min(opacity, heading.opacity)})
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
	toolGlyphSlot          = transcriptGlyphSlot
	toolTimelineInset      = toolGlyphSlot
	toolTimelineGap        = 8
	toolSummaryGap         = 6
	toolDisclosureDuration = 160 * time.Millisecond
)

func toolGlyph(c *n.Context, icon *n.SVG, label string, font n.Font, color n.Color) n.Element {
	return transcriptMarkerSlot(c).Children(func() { transcriptSymbol(c, icon, label, font, color) })
}

func (v *View) toolGroupTitle(c *n.Context, t *n.Theme, row transcriptRow, index int) {
	label := fmt.Sprintf("%d 次工具调用", row.count)
	outcome := ""
	switch row.toolResult {
	case "completed":
		outcome = "已完成"
	case "ended":
		outcome = "调用结束"
	}

	opened := !v.Model.ToolDisclosure(row.group).Closed()
	progress := v.runningProgress
	inline := float32(0)
	live := progress != nil && v.runningGroup == row.group
	var phase string
	var elapsed *time.Time
	var shimmerAt time.Time
	if live {
		inline = 1 - row.toolReveal
		if inline > 0 {
			phase, elapsed, shimmerAt = progress.toolCaption(c), v.toolElapsed(row.item.TurnID), progress.started
		}
	} else if h := v.steerFooterTransition(); h != nil && h.tool != nil && h.tool.group == row.group && h.tool.inline {
		inline = 1 - h.fraction(c.Now())
		phase, elapsed, shimmerAt = h.tool.label, h.tool.elapsed, h.tool.shimmerAt
	}
	description := outcome
	if live {
		description = "进行中"
		if outcome != "" {
			description += "，" + outcome
		}
	}
	button := n.ButtonBase(c).AlignSelf(n.Start).Height(20).Justify(n.Start).Label(label).Description(description).Expanded(opened)
	color := textHoverColor(c, button, t.TextMuted)
	font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
	button.Children(func() {
		line := n.Row(c).Height(20).AlignItems(n.Baseline)
		if inline > 0 {
			line.Label("工具组运行状态")
			shimmerToolStatus(c, line, shimmerAt, t.Text, inline)
		}
		line.Children(func() {
			transcriptMarkerSlot(c.Key("icon")).MarginEnd(toolSummaryGap).Children(func() {
				transcriptSymbol(c.Key("terminal"), transcriptCommandIcon, label, font, color).Opacity(1 - inline)
				if inline > 0 {
					transcriptStatusDot(c.Key("dot"), label, font, t.Success, true, shimmerAt).Absolute().Left(0).Top(0).Opacity(inline)
				}
			})
			countAnimation := animationPointer(c, 180*time.Millisecond)
			n.Text(c.Key("count"), label).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(color).
				ContentTransition(n.ContentTransition{Numeric: true, Animation: countAnimation})
			if outcome != "" {
				n.Text(c.Key("outcome"), outcome).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(t.TextMuted).MarginStart(toolSummaryGap).
					ContentTransition(n.ContentTransition{Numeric: true, Animation: countAnimation})
			}
			if inline > 0 {
				v.toolProgressCaption(c.Key("inline"), t, phase, elapsed, inline, true)
			}
			angle := -90 * (1 - row.toolReveal)
			if c.Preferences().ReduceMotion {
				angle = -90
				if opened {
					angle = 0
				}
			}
			inlineSymbolWithAnimation(c.Key("chevron"), transcriptChevron, label, font, 14, color, n.Animation{}, angle).MarginStart(toolSummaryGap)
		})
	})
	if button.Clicked() {
		v.toggleToolGroup(c, row.group, index, button)
	}
}

func (v *View) toolGroupProgress(c *n.Context, t *n.Theme, row transcriptRow) {
	progress := v.runningProgress
	if progress == nil || v.runningGroup != row.toolParent {
		return
	}
	v.toolProgressLine(c, t, progress.toolCaption(c), v.toolElapsed(row.item.TurnID), progress.started, row.opacity)
}

func (v *View) toolElapsed(turnID string) *time.Time {
	for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
		if turn.ID == turnID {
			return v.runningElapsedStart(turn)
		}
	}
	return nil
}

func (v *View) toolProgressLine(c *n.Context, t *n.Theme, phase string, elapsed *time.Time, shimmerAt time.Time, opacity float32) {
	line := n.Row(c).Height(20).AlignSelf(n.Start).AlignItems(n.Baseline).Label("工具组运行状态")
	shimmerToolStatus(c, line, shimmerAt, t.Text, opacity)
	font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
	line.Children(func() {
		transcriptStatusDot(c.Key("dot"), phase, font, t.Success, true, shimmerAt)
		v.toolProgressCaption(c.Key("caption"), t, phase, elapsed, 1, false)
	})
}

func (p *runningProgress) shownToolCaption() string {
	if p.caption != "" {
		return p.caption
	}
	if p.label != "" {
		return p.label
	}
	return "进行中"
}

func (p *runningProgress) toolCaption(c *n.Context) string {
	phase := p.label
	if phase == "" {
		phase = "进行中"
	}
	return p.activityCaption(c, phase)
}

func (v *View) toolProgressCaption(c *n.Context, t *n.Theme, phase string, started *time.Time, extent float32, clipped bool) {
	font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
	line := n.Row(c).AlignItems(n.Baseline).Height(20)
	line.Children(func() {
		phaseWidth, _ := n.ShapeText(phase, font, 0).Size()
		caption := n.Text(c.Key("phase"), phase).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(t.TextMuted).SingleLine().Shrink(0).Padding(0, 0, 0, 6).Clip().
			ContentTransition(n.ContentTransition{Animation: animationPointer(c, progressReclaimDuration)})
		width := caption.AnimateTo("phase-width", phaseWidth+6, motionAnimation(c, progressReclaimDuration))
		caption.Width(width)
		_, clockWidth := runningElapsed(c.Key("elapsed"), started, t.TextMuted, true)
		if clipped && extent < 1 {
			// Fade the intact caption ahead of collapsing the width of its slot.
			fade := max(0, (extent-.45)/.55)
			line.Width((width + clockWidth) * min(1, extent/.72)).Clip().Opacity(fade)
		}
	})
}

func (v *View) toggleToolGroup(c *n.Context, key string, index int, button n.Element) {
	to := float32(0)
	opening := v.Model.ToolDisclosure(key).Closed()
	if opening {
		to = 1
	}
	if list := v.lists[v.Model.SessionID]; list != nil {
		// Both directions preserve the same native heading as the reading anchor.
		list.KeepRowWithinContent(index, button.Bounds().Y-list.Bounds(c).Y)
	}
	v.animateToolGroup(key, to, c.Now(), motionAnimation(c, toolDisclosureDuration).Duration)
	v.Model.SetToolDisclosure(key, v.Model.ToolDisclosure(key).Toggle())
	c.AnimationFrame()
}

func (v *View) animateToolGroup(key string, to float32, now time.Time, duration time.Duration) {
	from := 1 - to
	if motion := v.toolMotions[key]; motion != nil {
		from, _ = motion.position(now)
	}
	if v.toolMotions == nil {
		v.toolMotions = map[string]*toolDisclosureMotion{}
	}
	duration = time.Duration(float32(duration) * max(to-from, from-to))
	v.toolMotions[key] = &toolDisclosureMotion{from: from, to: to, start: now, duration: duration}
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
		if toolIssueLabel(item) != "" || item.Status != "completed" {
			result = "ended"
		}
	}
	return result
}

var toolThinkingIcon = newTranscriptIcon(conversationui.ThinkingIconPath)

func (v *View) toolTimelineItem(c *n.Context, t *n.Theme, row transcriptRow, inset float32) {
	icon := activityIcon(row.item.Presentation)
	label := row.item.Name
	font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
	if commandItem(row.item) {
		icon = commandActivityIcon(row.item.Name)
		font.Family = "Menlo"
	}
	if domain.ReasoningSummary(row.item) {
		body := conversationui.ReasoningSummaryText(row.item)
		if body == "" {
			return
		}
		icon = toolThinkingIcon
		label, _, _ = strings.Cut(body, "\n")
		resultText(c.Key("reasoning"), body, t.TextMuted, func(text n.Element) {
			text.Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).Wrap().Selectable()
			v.decoratePlainFind(c, text, row.item, nil)
		})
	} else {
		v.item(c, t, row.item)
	}
	// A small neutral mask interrupts the rail around each native glyph.
	toolGlyph(c, icon, label, font, t.TextMuted).Absolute().Left(inset).Top(0).Background(t.Background)
}

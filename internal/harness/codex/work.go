package codex

import (
	"context"
	"encoding/json"
	"path"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"sort"
	"strings"
	"time"
)

type agentWorkBinding struct{ session, rootThread, rootTurn, id, sourceItem, parent, currentTurn string }
type commandWork struct {
	session, thread, turn, rootTurn, parent, sourceItem, process string
	body                                                         domain.Item
	work                                                         domain.WorkActivity
	promoted                                                     bool
}
type workTracker struct {
	spawns       map[string]bool
	earlyTurns   map[string]wire.Turn
	earlyPrompts map[string]string
	agents       map[string]agentWorkBinding
	childTurns   map[string]agentWorkBinding
	currentTurns map[string]string
	endedTurns   map[string]bool
	works        map[string]domain.WorkActivity
	commands     map[string]*commandWork
}

func (n *Normalizer) workState() *workTracker {
	if n.work == nil {
		n.work = &workTracker{spawns: map[string]bool{}, earlyTurns: map[string]wire.Turn{}, earlyPrompts: map[string]string{}, agents: map[string]agentWorkBinding{}, childTurns: map[string]agentWorkBinding{}, currentTurns: map[string]string{}, endedTurns: map[string]bool{}, works: map[string]domain.WorkActivity{}, commands: map[string]*commandWork{}}
	}
	return n.work
}
func workString(i wire.Item, key string) string {
	var s string
	json.Unmarshal(i.Fields[key], &s)
	return s
}
func itemTime(i wire.Item, key string) *time.Time {
	var ms *int64
	json.Unmarshal(i.Fields[key], &ms)
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}
func itemDuration(i wire.Item) *int64 {
	var v *int64
	json.Unmarshal(i.Fields["durationMs"], &v)
	return v
}
func workTitle(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	return boundedTo(s, 256)
}
func latestWorkText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return ""
	}
	return boundedTo(lines[len(lines)-1], 512)
}
func workEvent(sid string, w domain.WorkActivity) domain.Event {
	return domain.Event{SessionID: sid, Type: "work.upsert", Work: &w}
}
func workItemEvent(sid, id string, i domain.Item) domain.Event {
	return domain.Event{SessionID: sid, Type: "work.item", WorkID: id, WorkItem: &i}
}
func (n *Normalizer) registerAgent(parentThread, rootTurn, sourceItem, child, title, brief, model, effort string) (agentWorkBinding, bool) {
	tracker := n.workState()
	sid := n.sessions[parentThread]
	rootThread := parentThread
	parent := ""
	if owner, ok := tracker.agents[parentThread]; ok {
		sid, rootThread, rootTurn, parent = owner.session, owner.rootThread, owner.rootTurn, owner.id
		sourceItem = owner.sourceItem
	}
	if sid == "" || child == "" || child == rootThread || n.sessions[child] != "" || rootTurn == "" {
		return agentWorkBinding{}, false
	}
	// Only a structured spawn/activity from an owned thread establishes lineage.
	id := "agent:" + rootTurn + ":" + child
	if old, ok := tracker.agents[child]; ok && old.session != sid {
		return agentWorkBinding{}, false
	}
	binding := agentWorkBinding{session: sid, rootThread: rootThread, rootTurn: rootTurn, id: id, sourceItem: sourceItem, parent: parent}
	if old, ok := tracker.agents[child]; ok && old.id == id {
		binding.currentTurn = old.currentTurn
	}
	tracker.agents[child] = binding
	w, exists := tracker.works[id]
	if !exists {
		w = domain.WorkActivity{Reference: child, OriginRef: rootThread, ID: id, Kind: "agent", TurnID: rootTurn, ParentID: parent, SourceItemID: sourceItem, Title: workTitle(title), Brief: workBound(brief, 8<<10), Model: boundedTo(model, 128), Effort: boundedTo(effort, 32), State: "starting"}
		if w.Title == "" {
			w.Title = "子 Agent"
		}
	}
	if w.Brief == "" {
		w.Brief = workBound(brief, 8<<10)
		if w.Brief == "" {
			w.Brief = tracker.earlyPrompts[child]
		}
	}
	delete(tracker.earlyPrompts, child)
	if model != "" {
		w.Model = boundedTo(model, 128)
	}
	if effort != "" {
		w.Effort = boundedTo(effort, 32)
	}
	if turn, ok := tracker.earlyTurns[child]; ok {
		binding.currentTurn = turn.ID
		tracker.agents[child] = binding
		tracker.childTurns[child+"\x00"+turn.ID] = binding
		applyChildTurn(&w, turn, n.workControl)
		delete(tracker.earlyTurns, child)
	}
	tracker.works[id] = w
	return binding, true
}
func (n *Normalizer) observeDelegation(p wire.Params, sourceItem string) []domain.Event {
	tracker := n.workState()
	source := p.Item
	tool := workString(source, "tool")
	if tool == "spawnAgent" {
		key := p.ThreadID + "\x00" + sourceItem
		if workString(source, "status") == "inProgress" {
			tracker.spawns[key] = true
		} else {
			delete(tracker.spawns, key)
		}
	}
	out := []domain.Event{}
	var states map[string]struct{ Status, Message string }
	json.Unmarshal(source.Fields["agentsStates"], &states)
	var children []string
	json.Unmarshal(source.Fields["receiverThreadIds"], &children)
	for id := range states {
		found := false
		for _, known := range children {
			found = found || known == id
		}
		if !found {
			children = append(children, id)
		}
	}
	sort.Strings(children)
	for _, child := range children {
		binding, known := tracker.agents[child]
		followup := known && (tool == "sendInput" || tool == "resumeAgent" || tool == "followupTask") && binding.rootTurn != p.TurnID
		if followup {
			out = append(out, n.invalidateChildRequests(binding.id, "")...)
		}
		if tool == "spawnAgent" || followup {
			var ok bool
			binding, ok = n.registerAgent(p.ThreadID, p.TurnID, sourceItem, child, workString(source, "prompt"), workString(source, "prompt"), workString(source, "model"), workString(source, "reasoningEffort"))
			if !ok {
				continue
			}
			known = true
		}
		if !known {
			continue
		}
		parentSid := n.sessions[p.ThreadID]
		if owner, ok := tracker.agents[p.ThreadID]; ok {
			parentSid = owner.session
		}
		if binding.session != parentSid {
			continue
		}
		w := tracker.works[binding.id]
		state, observed := states[child]
		if observed {
			next := normalizedAgentStatus(state.Status)
			if next == "interrupted" {
				next = "stopped"
			}
			if !(w.State == "running" && next == "starting") && (w.Active() || next != "running" && next != "starting") {
				w.State = next
			}
			if state.Message != "" {
				w.Latest = boundedTo(state.Message, 512)
			}
		}
		if !w.Active() {
			w.CanStop = false
		}
		n.workInputCapabilities(&w, binding)
		tracker.works[w.ID] = w
		out = append(out, workEvent(binding.session, w))
		if w.State == "stopped" || w.State == "failed" {
			out = append(out, n.invalidateChildRequests(binding.id, "")...)
		}
	}
	return out
}
func (n *Normalizer) observeRootWork(m wire.Message, p wire.Params, event domain.Event) []domain.Event {
	tracker := n.workState()
	sid := n.sessions[p.ThreadID]
	rootTurn := p.TurnID
	parent := ""
	origin := ""
	if owner, ok := tracker.agents[p.ThreadID]; ok {
		sid, rootTurn, parent, origin = owner.session, owner.rootTurn, owner.id, owner.sourceItem
	}
	if sid == "" {
		return nil
	}
	if event.Turn != nil {
		tracker.currentTurns[p.ThreadID] = event.Turn.ID
		switch event.Turn.Status {
		case "completed", "interrupted", "failed", "abandoned":
			tracker.endedTurns[p.ThreadID+"\x00"+event.Turn.ID] = true
		}
	}
	out := []domain.Event{}
	if p.Item.Type == "collabAgentToolCall" {
		out = append(out, n.observeDelegation(p, itemKey(p.TurnID, p.Item.ID))...)
	}
	if p.Item.Type == "subAgentActivity" {
		child := workString(p.Item, "agentThreadId")
		title := path.Base(workString(p.Item, "agentPath"))
		b, ok := n.registerAgent(p.ThreadID, p.TurnID, itemKey(p.TurnID, p.Item.ID), child, title, "", workString(p.Item, "model"), workString(p.Item, "reasoningEffort"))
		if ok {
			w := tracker.works[b.id]
			switch workString(p.Item, "kind") {
			case "started":
				w.State = "running"
			case "completed":
				w.State = "completed"
				w.CanStop = false
			case "interrupted":
				w.State = "stopped"
				w.CanStop = false
			}
			tracker.works[w.ID] = w
			out = append(out, workEvent(b.session, w))
			if w.State == "stopped" {
				out = append(out, n.invalidateChildRequests(b.id, "")...)
			}
		}
	}
	// A still-running process becomes background work only when structured
	// execution moves to another item or its owner's turn ends. No prose parsing.
	if m.Method == "item/started" || event.Turn != nil && event.Turn.Status != "running" {
		keys := []string{}
		for k := range tracker.commands {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			cmd := tracker.commands[key]
			if cmd.thread != p.ThreadID || cmd.body.Final || cmd.process == "" {
				continue
			}
			if cmd.promoted && event.Turn == nil {
				continue
			}
			if m.Method == "item/started" && cmd.body.ID == itemKey(p.TurnID, p.Item.ID) {
				continue
			}
			if event.Turn != nil && cmd.turn != event.Turn.ID {
				continue
			}
			cmd.promoted = true
			cmd.work.State = tracker.commandState(cmd, cmd.work.State)
			tracker.works[cmd.work.ID] = cmd.work
			out = append(out, workEvent(sid, cmd.work), workItemEvent(sid, cmd.work.ID, cmd.body))
		}
	}
	if event.Item != nil && event.Item.Presentation == "command" {
		key := p.ThreadID + "\x00" + event.Item.ID
		cmd := tracker.commands[key]
		if cmd == nil && p.Item.Type == "commandExecution" {
			process := workString(p.Item, "processId")
			if process != "" {
				source := event.Item.ID
				if origin != "" {
					source = origin
				}
				cmd = &commandWork{session: sid, thread: p.ThreadID, turn: p.TurnID, rootTurn: rootTurn, parent: parent, sourceItem: source, process: process, work: domain.WorkActivity{ID: "command:" + p.ThreadID + ":" + event.Item.ID, Kind: "background", OriginRef: func() string {
					if b, ok := tracker.agents[p.ThreadID]; ok {
						return b.rootThread
					}
					return p.ThreadID
				}(), TurnID: rootTurn, ParentID: parent, SourceItemID: source, Title: workTitle(p.Item.Command), Brief: workBound(p.Item.Command, 8<<10), State: neutralWorkState(event.Item.Status), StartedAt: itemTime(p.Item, "startedAtMs")}}
				tracker.commands[key] = cmd
			}
		}
		if cmd != nil {
			cmd.body = *event.Item
			cmd.work.State = tracker.commandState(cmd, neutralWorkState(event.Item.Status))
			// The owning turn may have completed before this delayed item arrived.
			if !cmd.body.Final && tracker.endedTurns[cmd.thread+"\x00"+cmd.turn] {
				cmd.promoted = true
			}
			cmd.work.Latest = latestWorkText(event.Item.Text)
			if p.Item.Type == "commandExecution" {
				cmd.work.CompletedAt = itemTime(p.Item, "completedAtMs")
				cmd.work.DurationMS = itemDuration(p.Item)
			}
			if cmd.promoted {
				tracker.works[cmd.work.ID] = cmd.work
				out = append(out, workEvent(sid, cmd.work), workItemEvent(sid, cmd.work.ID, cmd.body))
			}
		}
	}
	return out
}

func (n *Normalizer) feedChildWork(m wire.Message, p wire.Params) ([]domain.Event, bool) {
	tracker := n.workState()
	binding, ok := tracker.agents[p.ThreadID]
	if !ok {
		return nil, false
	}
	if n.sessions[binding.rootThread] != binding.session {
		return nil, true
	}
	lookupTurn := p.TurnID
	if p.Turn.ID != "" {
		lookupTurn = p.Turn.ID
	}
	if lookupTurn != "" {
		if saved, found := tracker.childTurns[p.ThreadID+"\x00"+lookupTurn]; found {
			binding = saved
		}
	}
	w := tracker.works[binding.id]
	out := []domain.Event{}
	switch m.Method {
	case "turn/started", "turn/completed":
		if m.Method == "turn/started" {
			if binding.currentTurn != "" && binding.currentTurn != p.Turn.ID {
				out = append(out, n.invalidateChildRequests(binding.id, binding.currentTurn)...)
			}
			binding.currentTurn = p.Turn.ID
			tracker.agents[p.ThreadID] = binding
			tracker.childTurns[p.ThreadID+"\x00"+p.Turn.ID] = binding
		}
		w.State = string(normalizedTurnStatus(p.Turn.Status))
		if w.State == "interrupted" {
			w.State = "stopped"
		}
		if p.Turn.StartedAt != nil {
			w.StartedAt = stamp(p.Turn.StartedAt)
		}
		w.CompletedAt = stamp(p.Turn.CompletedAt)
		w.DurationMS = p.Turn.DurationMS
		w.CanStop = n.workControl && w.Active() && binding.currentTurn != ""
		n.workInputCapabilities(&w, binding)
		tracker.works[w.ID] = w
		// Its commands may outlive this child. Persist their ownership first.
		out = append(out, n.observeRootWork(m, p, domain.Event{Turn: &domain.Turn{ID: p.Turn.ID, Status: normalizedTurnStatus(p.Turn.Status)}})...)
		out = append(out, workEvent(binding.session, w))
		if w.State == "stopped" || w.State == "failed" {
			out = append(out, n.invalidateChildRequests(binding.id, "")...)
		}
	case "thread/name/updated":
		if p.ThreadName != nil && strings.TrimSpace(*p.ThreadName) != "" {
			w.Title = workTitle(*p.ThreadName)
			tracker.works[w.ID] = w
			out = append(out, workEvent(binding.session, w))
			if w.State == "stopped" || w.State == "failed" {
				out = append(out, n.invalidateChildRequests(binding.id, "")...)
			}
		}
	case "item/started", "item/completed":
		if p.Item.Type == "userMessage" {
			if w.Brief == "" {
				if prompt := childInitialPrompt(p.Item); prompt != "" {
					w.Brief = prompt
					tracker.works[w.ID] = w
					return []domain.Event{workEvent(binding.session, w)}, true
				}
			}
			return nil, true
		}
		key := binding.id + "\x00" + itemKey(p.TurnID, p.Item.ID)
		old := n.items[key]
		if old.Final && m.Method == "item/started" {
			return nil, true
		}
		i := n.normalizeResourceItem(binding.session, itemKey(p.TurnID, p.Item.ID), p.Item, m.Method == "item/completed")
		i.ID, i.TurnID = itemKey(p.TurnID, p.Item.ID), binding.rootTurn
		n.items[key] = i
		if i.Name != "" {
			w.Latest = boundedTo(i.Name, 512)
		} else if i.Text != "" {
			w.Latest = latestWorkText(i.Text)
		}
		if t := itemTime(p.Item, "startedAtMs"); w.StartedAt == nil && t != nil {
			w.StartedAt = t
		}
		tracker.works[w.ID] = w
		out = append(out, workEvent(binding.session, w), workItemEvent(binding.session, w.ID, i))
		out = append(out, n.observeRootWork(m, p, domain.Event{Item: &i})...)
	case "item/agentMessage/delta", "item/commandExecution/outputDelta":
		key := binding.id + "\x00" + itemKey(p.TurnID, p.ItemID)
		i, found := n.items[key]
		if !found || i.Final {
			return nil, true
		}
		i.Text = bounded(i.Text + p.Delta)
		limitItem(&i)
		n.items[key] = i
		w.Latest = latestWorkText(i.Text)
		tracker.works[w.ID] = w
		out = append(out, workEvent(binding.session, w), workItemEvent(binding.session, w.ID, i))
		out = append(out, n.observeRootWork(m, p, domain.Event{Item: &i})...)
	}
	return out, true
}
func (n *Normalizer) invalidateWork() []domain.Event {
	if n.work == nil {
		return nil
	}
	out := []domain.Event{}
	ids := []string{}
	for id := range n.work.works {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := n.work.works[id]
		if !w.Active() && !w.CanSteer && !w.CanContinue {
			continue
		}
		if w.Active() {
			w.State = "unknown"
		}
		w.CanStop = false
		w.CanSteer, w.CanContinue = false, false
		n.work.works[id] = w
		sid := ""
		for _, b := range n.work.agents {
			if b.id == id && n.sessions[b.rootThread] == b.session {
				sid = b.session
				break
			}
		}
		if sid == "" {
			for _, c := range n.work.commands {
				if c.work.ID == id && n.ownsWorkThread(c.thread, c.session) {
					sid = c.session
					break
				}
			}
		}
		if sid != "" {
			out = append(out, workEvent(sid, w))
		}
	}
	return out
}

func (r *Runtime) RestoreWork(s domain.Session, works []domain.WorkActivity) {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	r.normalizer.mu.Lock()
	defer r.normalizer.mu.Unlock()
	n := r.normalizer
	if n.sessions[s.ThreadID] != s.ID {
		return
	}
	tracker := n.workState()
	for _, w := range works {
		if w.Kind != "agent" || w.Reference == "" || w.OriginRef != s.ThreadID {
			continue
		}
		w.CanStop = false
		w.CanSteer, w.CanContinue = false, false
		tracker.works[w.ID] = w
		old, ok := tracker.agents[w.Reference]
		if ok && tracker.works[old.id].Revision > w.Revision {
			continue
		}
		tracker.agents[w.Reference] = agentWorkBinding{session: s.ID, rootThread: s.ThreadID, rootTurn: w.TurnID, id: w.ID, sourceItem: w.SourceItemID, parent: w.ParentID}
	}
}
func (r *Runtime) StopWork(ctx context.Context, s domain.Session, id string) error {
	r.normalizer.mu.Lock()
	n := r.normalizer
	tracker := n.workState()
	var child, turn string
	if n.sessions[s.ThreadID] == s.ID {
		for ref, b := range tracker.agents {
			w := tracker.works[b.id]
			if b.id == id && b.session == s.ID && b.rootThread == s.ThreadID && w.Active() && w.CanStop && b.currentTurn != "" {
				child, turn = ref, b.currentTurn
				break
			}
		}
	}
	r.normalizer.mu.Unlock()
	if child == "" || !r.supports("turn/interrupt") {
		return harness.Unsupported("independent-work-stop")
	}
	return r.call(ctx, "turn/interrupt", map[string]string{"threadId": child, "turnId": turn}, nil)
}

func (n *Normalizer) ownsWorkThread(thread, sid string) bool {
	if n.sessions[thread] == sid {
		return true
	}
	if b, ok := n.workState().agents[thread]; ok {
		return b.session == sid && n.sessions[b.rootThread] == sid
	}
	return false
}

// A spawned thread can announce its turn before the spawn tool returns its
// identity. Retain bounded lifecycle observations, but adopt none until the
// owned structured spawn confirms that exact thread reference.
func (n *Normalizer) rememberEarlyWorkTurn(m wire.Message, p wire.Params) {
	if p.ThreadID == "" {
		return
	}
	t := n.workState()
	if _, known := t.agents[p.ThreadID]; known {
		return
	}
	if (m.Method == "item/started" || m.Method == "item/completed") && p.Item.Type == "userMessage" {
		if len(t.earlyPrompts) < 256 && t.earlyPrompts[p.ThreadID] == "" {
			if prompt := childInitialPrompt(p.Item); prompt != "" {
				t.earlyPrompts[p.ThreadID] = prompt
			}
		}
		return
	}
	if m.Method != "turn/started" && m.Method != "turn/completed" {
		return
	}
	if _, known := t.earlyTurns[p.ThreadID]; !known && len(t.earlyTurns) >= 256 {
		return
	}
	t.earlyTurns[p.ThreadID] = p.Turn
}

// Worker-accepted followups already have their own input items. Only the
// initial unmarked child input supplies the persistent task heading.
func childInitialPrompt(item wire.Item) string {
	if workString(item, "clientId") != "" {
		return ""
	}
	var content []wire.Input
	if json.Unmarshal(item.Content, &content) != nil {
		return ""
	}
	var text []string
	for _, part := range content {
		if part.Type == "text" && part.Text != "" {
			text = append(text, part.Text)
		}
	}
	return workBound(strings.Join(text, "\n"), 8<<10)
}
func applyChildTurn(w *domain.WorkActivity, t wire.Turn, control bool) {
	w.State = string(normalizedTurnStatus(t.Status))
	if w.State == "interrupted" {
		w.State = "stopped"
	}
	if t.StartedAt != nil {
		w.StartedAt = stamp(t.StartedAt)
	}
	w.CompletedAt = stamp(t.CompletedAt)
	w.DurationMS = t.DurationMS
	w.CanStop = control && w.Active() && t.ID != ""
}

func workBound(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit-3], "…")
}
func neutralWorkState(s string) string {
	switch s {
	case "running", "starting", "waiting", "completed", "failed", "stopped":
		return s
	case "interrupted", "declined":
		return "stopped"
	case "pending":
		return "waiting"
	}
	return "unknown"
}

func (t *workTracker) commandState(cmd *commandWork, state string) string {
	if t.endedTurns[cmd.thread+"\x00"+cmd.turn] && (state == "running" || state == "starting") {
		return "waiting"
	}
	return state
}

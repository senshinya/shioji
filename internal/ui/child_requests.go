package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"strings"
	"time"
)

type requestActionState struct {
	epoch    string
	revision int64
	busy     bool
	err      string
}

func (v *View) requestAction(r domain.PendingRequest) *requestActionState {
	if v.requestActionsState == nil {
		v.requestActionsState = map[app.WorkKey]*requestActionState{}
	}
	key := v.Model.WorkKey(r.SessionID, r.ID)
	s := v.requestActionsState[key]
	if s == nil || s.epoch != v.Model.ConnectionEpoch || s.revision != r.Revision {
		s = &requestActionState{epoch: v.Model.ConnectionEpoch, revision: r.Revision}
		v.requestActionsState[key] = s
	}
	return s
}
func (v *View) requestWork(id string) domain.WorkActivity {
	for _, w := range v.Model.Details[v.Model.SessionID].Works {
		if w.ID == id {
			return w
		}
	}
	if cached, ok := v.Model.WorkDetails[v.Model.WorkKey(v.Model.SessionID, id)]; ok && cached.Work.ID == id {
		return cached.Work
	}
	return domain.WorkActivity{ID: id, Kind: "agent", Title: "子 Agent", State: "unknown"}
}
func (v *View) pendingWorkRequest(w domain.WorkActivity) *domain.PendingRequest {
	if !v.Model.Connected || w.State == "unknown" || w.State == "stopped" || w.State == "failed" {
		return nil
	}
	for _, r := range v.Model.Details[v.Model.SessionID].Requests {
		if r.WorkID == w.ID && r.State.AwaitingResolution() {
			return &r
		}
	}
	return nil
}
func (v *View) workLabel(w domain.WorkActivity) string {
	if r := v.pendingWorkRequest(w); r != nil {
		if r.Kind == "approval" || r.Kind == "permissions" {
			return "等待授权"
		}
		return "等待回答"
	}
	return workStateLabel(w)
}
func (v *View) openRequestWork(r domain.PendingRequest) {
	if r.WorkID == "" {
		return
	}
	m := v.Model
	m.Inspectors[m.SessionID] = true
	state := v.changesFor(m.SessionID)
	groups := m.InspectorLayout.Groups
	if len(groups) == 0 {
		groups = state.WorkGroups
	}
	if len(groups) == 0 {
		groups = workDockGroups(m.InspectorLayout, false, m.Details[m.SessionID].Plan != nil)
	}
	groups = app.CloneInspectorGroups(groups)
	for i := range groups {
		if slices.Contains(groups[i].Views, "agents") {
			groups[i].Selected = "agents"
		}
	}
	m.InspectorLayout.Groups = groups
	state.WorkGroups = app.CloneInspectorGroups(groups)
	p := v.workPanel("agents")
	p.selected = r.WorkID
	p.requested = r.WorkID
	v.readSessionWork(v.requestWork(r.WorkID), p.detail(r.WorkID), true)
}
func (v *View) requestOrigin(c *n.Context, t *n.Theme, r domain.PendingRequest) {
	slot := n.Column(c.Key("request-origin-slot")).MinWidth(0).MaxWidth(180).Shrink(1).Height(24).Clip().Label("请求来源区域")
	saved := n.Local(slot, "source", func() domain.PendingRequest { return r })
	if r.WorkID != "" {
		*saved = r
	}
	source := *saved
	title := v.requestWork(source.WorkID).Title
	textWidth, _ := c.MeasureText(0, n.Span{Text: title, Font: t.Font, Size: 11, Weight: 400})
	const iconSize float32 = 12
	contentWidth := min(float32(180), textWidth+8+5+iconSize)
	target := float32(0)
	if r.WorkID != "" {
		target = contentWidth
	}
	width := slot.AnimateTo("source-width", target, motionAnimation(c, 180*time.Millisecond))
	slot.Width(width)
	slot.Children(func() {
		if source.WorkID == "" {
			return
		}
		button := n.ButtonBase(c.Key("request-origin")).Width(contentWidth).Height(24).MinWidth(0).Shrink(0).PaddingX(4).Label("查看来源：" + title).Tooltip(title)
		n.Inert(button, r.WorkID == "")
		color := textHoverColor(c, button, t.TextMuted)
		button.Children(func() {
			n.Row(c).FillWidth().Gap(5).MinWidth(0).AlignItems(n.Baseline).Children(func() {
				n.Text(c, title).SingleLine().MinWidth(0).Grow(1).FontSize(11).FontWeight(400).FixedLineHeight(20).TextColor(color).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
				inlineSymbol(c, referenceExternalIcon, title, n.Font{Family: t.Font, Size: 11, Weight: 400}, iconSize, color)
			})
		})
		if button.Clicked() && r.WorkID != "" {
			v.openRequestWork(r)
		}
	})
}
func (v *View) requestSource(c *n.Context, t *n.Theme, item domain.Item, child bool) {
	label := item.Text
	if !child && item.WorkID != "" {
		label = v.requestWork(item.WorkID).Title + " · " + label
	}
	switch item.Status {
	case "answered":
		label += " · 已处理"
	case "resolved":
		label += " · 已结束"
	case "invalidated":
		label += " · 已失效"
	case "submitting":
		label += " · 提交中"
	}
	icon := questionIcon
	for _, r := range v.Model.Details[v.Model.SessionID].Requests {
		if item.ID == "request:"+r.ID && (r.Kind == "approval" || r.Kind == "permissions") {
			icon = shieldIcon
			break
		}
	}
	active := item.Status == "pending" || item.Status == "submitting"
	name := label
	if child {
		name = "子 Agent 请求来源：" + label
	}
	var source n.Element
	if active {
		source = control(c).AlignSelf(n.Start).MinHeight(20).Justify(n.Start).Label(name)
	} else {
		source = n.Row(c).FillWidth().MinHeight(20).Label(name)
	}
	source.Children(func() {
		n.Row(c).Gap(7).AlignItems(n.Baseline).Children(func() {
			font := n.Font{Family: t.Font, Size: 11, Weight: 400}
			inlineSymbol(c, icon, label, font, 12, subtle(t))
			v.requestSourceLabel(c, t, item, label)
			if active {
				inlineSymbol(c.Key("pending-disclosure"), disclosureRight, label, font, 10, subtle(t)).Transition(fadeMotion(c, 180*time.Millisecond))
			}
		})
	})
	if active && source.Clicked() {
		v.openSourceRequest(item)
	}
}
func (v *View) workRequestLink(c *n.Context, t *n.Theme, w domain.WorkActivity) {
	r := v.pendingWorkRequest(w)
	if r == nil {
		return
	}
	label := v.workLabel(w)
	button := n.ButtonBase(c).Height(20).Shrink(0).Label("处理子 Agent 请求：" + w.Title).Transition(resultBlockMotion(c))
	color := textHoverColor(c, button, t.TextMuted)
	button.Children(func() {
		n.Text(c, label).FontSize(11).FontWeight(400).FixedLineHeight(20).TextColor(color).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
	})
	if button.Clicked() {
		v.openSourceRequest(domain.Item{ID: "request:" + r.ID})
	}
}
func (v *View) pruneRequestActions(detail domain.SessionDetail) {
	for key := range v.requestActionsState {
		if key.DeviceID != v.Model.Snapshot.DeviceID || key.SessionID != detail.Session.ID {
			continue
		}
		live := false
		for _, r := range detail.Requests {
			if r.ID == key.ID && r.State.AwaitingResolution() {
				live = true
				break
			}
		}
		if !live {
			delete(v.requestActionsState, key)
		}
	}
}

// Each open child detail owns a bounded presentation clock. Historical reads
// seed it settled; a live receipt gates only its own later output.
func (u *workDetailUI) syncStreams(c *n.Context, d domain.WorkDetail, epoch string) {
	reset := u.streams == nil || u.streamEpoch != epoch
	if reset {
		u.streams = map[string]*replyStream{}
		u.streamEpoch = epoch
		u.latestOrder = 0
	}
	known := map[string]bool{}
	var receiptUntil time.Time
	for _, entry := range d.Items {
		item := entry.Item
		known[item.ID] = true
		stream := u.streams[item.ID]
		if stream == nil {
			live := !reset && (item.Order == 0 || item.Order > u.latestOrder) || reset && u.phase == workReading && !u.hydrated && entry.Revision > u.readSequence
			stream = &replyStream{text: item.Text, phase: replyPhaseFor(live, live), changedAt: c.Now()}
			if live && item.Kind == "assistant" {
				stream.revealAfter = receiptUntil
			}
			u.streams[item.ID] = stream
		} else if stream.text != item.Text {
			stream.replace(strings.HasPrefix(item.Text, stream.text))
			stream.text = item.Text
			stream.changedAt = c.Now()
		}
		if item.Kind == "request_result" && stream.live() {
			receiptUntil = stream.changedAt.Add(receiptEntranceDuration)
		}
	}
	for _, entry := range d.Items {
		u.latestOrder = max(u.latestOrder, entry.Item.Order)
	}
	for id := range u.streams {
		if !known[id] {
			delete(u.streams, id)
		}
	}
}

// Source links use the same baseline metrics as the existing file-reference UI.
var referenceExternalIcon = svg("M10.5 3.5h6v6M16.5 3.5l-8 8M7.5 4.5h-4v12h12v-4")

// Snapshot hydration is history. Keep only increments actually observed on the
// client after this read started live, including those merged across the read.
func (u *workDetailUI) seedHistory(d domain.WorkDetail, observed map[string]bool, epoch string) {
	if u.streams == nil {
		u.streams = map[string]*replyStream{}
	}
	u.streamEpoch = epoch
	u.latestOrder = 0
	for _, entry := range d.Items {
		item := entry.Item
		if observed[item.ID] {
			continue
		}
		u.streams[item.ID] = &replyStream{text: item.Text, phase: replyHistorical}
		u.latestOrder = max(u.latestOrder, item.Order)
	}
}

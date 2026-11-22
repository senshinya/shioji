package main

import (
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/conversationui"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type mobileHistoryVisual struct {
	input               domain.HistoryChange
	started             time.Time
	suffix              map[string]bool
	preview             domain.Item
	y                   float32
	placed, bound, sent bool
}

func (a *mobileApp) animateHistoryExit(parent *mygo.Window, session domain.Session, req *mobileHistoryRequest, in domain.HistoryChange, send func()) {
	detail := a.model.Details[session.ID]
	visual := &mobileHistoryVisual{input: in, started: time.Now(), suffix: make(map[string]bool)}
	target := in.TurnID
	if in.Restart {
		for _, item := range detail.Items {
			if item.ID != in.ItemID {
				continue
			}
			target = item.TurnID
			item.Text, item.Status = in.Text, "dispatching"
			if refs := in.References; refs != nil {
				item.Attachments, item.Skills, item.ReferencePositions = refs.Attachments, refs.Skills, refs.ReferencePositions
			}
			visual.preview = item
			break
		}
	}
	order := int64(-1)
	for _, turn := range detail.Turns {
		if turn.ID == target || target == "" && !in.Restart && turn.Status == domain.ExecutionCompleted {
			order = max(order, turn.Order)
		}
	}
	for _, turn := range detail.Turns {
		visual.suffix[turn.ID] = order >= 0 && (turn.Order > order || in.Restart && turn.Order == order)
	}
	visual.y, visual.placed = req.anchorY, req.hasAnchor
	req.visual, req.err = visual, ""
	model, epoch := a.model, a.model.ConnectionEpoch
	delay := conversationui.HistoryForkFadeDuration
	if in.Restart {
		delay = conversationui.HistoryShatterDuration + conversationui.HistoryCollapseDuration
	}
	if a.reduceConversationMotion {
		delay = 0
	}
	dispatch := func() {
		if req.visual != visual || a.model != model || model.ConnectionEpoch != epoch || !a.visibleLinkSource(parent, session) ||
			!a.historyActionAvailable(a.sessionByID(session.ID)) || a.sessionByID(session.ID).Revision != in.Revision ||
			in.Restart && model.HistoryEdits[session.ID].ItemID != in.ItemID {
			if req.visual == visual {
				req.visual = nil
			}
			a.refresh()
			return
		}
		visual.sent = true
		send()
	}
	if delay == 0 {
		dispatch()
	} else {
		time.AfterFunc(delay, func() { mygo.RunOnMain(dispatch) })
		a.refresh()
	}
}

func (a *mobileApp) historyVisualDetail(detail domain.SessionDetail, sid string) (domain.SessionDetail, *mobileHistoryVisual) {
	req := a.historyRequests[a.model.WorkKey(sid, "")]
	if req == nil || req.visual == nil {
		return detail, nil
	}
	visual := req.visual
	if req.err != "" {
		req.visual = nil
		return detail, nil
	}
	if !visual.input.Restart || visual.preview.ID == "" {
		return detail, visual
	}
	for i, item := range detail.Items {
		if item.ID == "input:"+visual.input.OperationID {
			return detail, visual
		}
		if item.ID == visual.preview.ID {
			detail.Items = slices.Clone(detail.Items)
			detail.Items[i] = visual.preview
			return detail, visual
		}
	}
	detail.Items = append(slices.Clone(detail.Items), visual.preview)
	return detail, visual
}

func (m *mobileHistoryVisual) rowExits(row mobileclient.TranscriptRow, items []domain.Item) bool {
	if row.Footer != "" {
		return m.suffix[row.Footer]
	}
	item := items[row.First]
	return item.ID != m.input.ItemID && m.suffix[item.TurnID]
}

func (m *mobileHistoryVisual) paint(e ui.Element, key string, frame conversationui.HistoryExitFrame) {
	ui.Inert(e, true)
	e.Opacity(frame.Opacity)
	if frame.Shatter > 0 {
		e.Effect(conversationui.HistoryShatter(frame.Shatter, key))
	}
}

type mobileForkPosition struct {
	session, thread, turn string
	y                     float32
}

var mobileHistoryBubbleMotion = ui.ElementTransition{Duration: 280 * time.Millisecond,
	Ease: mobileComposerMotionEase, Size: true, Colors: true, Position: true}

func mobileHistoryMessageHeight(c *ui.Context, editing, restarting bool, render func(*ui.Context)) {
	wrapper := ui.Column(c.Key("history-message-height")).FillWidth()
	active := ui.Local(wrapper, "has-editor", func() bool { return false })
	*active = *active || editing || restarting
	if !*active {
		// Keep the ordinary send's lifted text free of an editor clip.
		wrapper.Children(func() { render(c.Key("history-message-body")) })
		return
	}
	wrapper.Clip()
	conversationui.AnimatedContentHeight(c, wrapper, true, true, 280*time.Millisecond, func() float32 {
		render(c.Key("history-message-body"))
		return 0
	})
}

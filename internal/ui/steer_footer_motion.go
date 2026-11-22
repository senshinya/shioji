package ui

import (
	"slices"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

const steerFooterDuration = 180 * time.Millisecond

type steerFooterHandoff struct {
	input   string
	started time.Time
	tool    *steerToolStatus
}

type steerToolStatus struct {
	group, label string
	shimmerAt    time.Time
	elapsed      *time.Time
	inline       bool
}

func (h *steerFooterHandoff) fraction(now time.Time) float32 {
	return n.EaseOut(min(1, max(0, float32(now.Sub(h.started))/float32(steerFooterDuration))))
}

func (v *View) steerFooterTransition() *steerFooterHandoff {
	if v.toolArrivals == nil {
		return nil
	}
	return v.toolArrivals.steerFooter
}

// Keep an insertion after a reply out of the transcript until its reply
// boundary is available. The worker's items remain authoritative.
func (v *View) holdSteerMessage(c *n.Context, detail domain.SessionDetail) ([]domain.Item, *domain.Item) {
	items := detail.Items
	m, arrivals := v.Model, v.toolArrivals
	if arrivals == nil {
		return items, nil
	}
	arrivals.heldSteerTurn = ""
	if v.historyBaseline || m.FoldToolHistory[m.SessionID] || !m.Connected ||
		arrivals.session != m.SessionID || arrivals.epoch != m.ConnectionEpoch || detail.History != nil && detail.History.Newer {
		arrivals.steerFooter = nil
		return items, nil
	}
	if handoff := arrivals.steerFooter; handoff != nil && handoff.tool != nil {
		if v.reduceMotion || c.Now().Sub(handoff.started) >= steerFooterDuration {
			arrivals.steerFooter = nil
		} else {
			c.AnimationFrame()
		}
	}
	if handoff := arrivals.steerFooter; handoff != nil && handoff.tool == nil {
		at := slices.IndexFunc(items, func(item domain.Item) bool { return item.ID == handoff.input })
		if at >= 0 && acceptedReplyInput(items[at]) {
			if !v.reduceMotion && c.Now().Sub(handoff.started) < steerFooterDuration {
				c.AnimationFrame()
				return items[:at], &items[at]
			}
			if !v.reduceMotion && v.sent == nil {
				// An insertion sent from another client has no composer origin.
				v.sent = &sentMessageMotion{input: handoff.input, origin: m.SessionID, session: m.SessionID,
					epoch: m.ConversationGeneration(), queued: true, focused: true}
			}
		}
		arrivals.steerFooter = nil
		return items, nil
	}

	progress := v.runningProgress
	if progress == nil || progress.turn == "" {
		return items, nil
	}
	toolOwnsProgress := v.runningGroup != "" && v.activityTurn == progress.turn
	if !toolOwnsProgress && v.historyRowBounds["turn-footer:"+progress.turn].H <= 0 {
		return items, nil
	}
	replied := false
	for i, item := range items {
		if item.TurnID != progress.turn || item.WorkID != "" {
			continue
		}
		if item.Kind == "assistant" && !domain.HistorySpacer(item) {
			replied = true
			continue
		}
		if item.Kind != "user" {
			continue
		}
		if replied && !arrivals.seen[item.ID] {
			if acceptedReplyInput(item) {
				if v.reduceMotion {
					return items, nil
				}
				handoff := &steerFooterHandoff{input: item.ID, started: c.Now()}
				arrivals.steerFooter = handoff
				c.AnimationFrame()
				if toolOwnsProgress {
					// Retain the caption that was on screen before the new
					// input reset the live clock. Its summary fades into the
					// same slot as the message opens space below it.
					handoff.tool = &steerToolStatus{
						group: v.runningGroup, label: progress.shownToolCaption(),
						shimmerAt: progress.started, elapsed: progress.elapsed,
						inline: v.Model.ToolDisclosure(v.runningGroup).Closed(),
					}
					return items, nil
				}
				return items[:i], &items[i]
			}
			if item.Status == "dispatching" && detail.Session.Status.Active() && !m.UncertainSubmissions[strings.TrimPrefix(item.ID, "input:")] {
				arrivals.heldSteerTurn = progress.turn
				return items[:i], nil
			}
		}
		if acceptedReplyInput(item) {
			replied = false
		}
	}
	return items, nil
}

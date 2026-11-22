package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

type mobileQueueSource struct {
	bounds ui.Rect
	left   time.Time
	turn   string
}

type mobileQueueMotion struct {
	epoch, arrival, animating string
	arrivalSource             ui.Rect
	display                   domain.QueueState
	sources                   map[string]mobileQueueSource
	consumed                  string
	following                 bool
}

const queueSourceLifetime = 5 * time.Second

func (m *mobileQueueMotion) remember(item domain.QueueItem, bounds ui.Rect) {
	if item.Input.OperationID == "" || bounds.W <= 0 {
		return
	}
	if m.sources == nil {
		m.sources = make(map[string]mobileQueueSource)
	}
	source := m.sources[item.Input.OperationID]
	source.bounds, source.left = bounds, time.Time{}
	m.sources[item.Input.OperationID] = source
}

func (m *mobileQueueMotion) observe(c *ui.Context, queue domain.QueueState, items []domain.Item, epoch string, send *mobileSendScroll, animate bool) {
	if m.epoch != epoch {
		m.epoch, m.sources, m.consumed = epoch, nil, ""
	}
	m.following = animate
	if m.consumed != "" && !slices.ContainsFunc(queue.Items, func(item domain.QueueItem) bool {
		return item.Input.OperationID == m.consumed
	}) {
		m.consumed = ""
	}
	for operation, source := range m.sources {
		if slices.ContainsFunc(queue.Items, func(item domain.QueueItem) bool { return item.Input.OperationID == operation }) {
			continue
		}
		if source.left.IsZero() {
			source.left = c.Now()
			m.sources[operation] = source
		} else if c.Now().Sub(source.left) >= queueSourceLifetime {
			delete(m.sources, operation)
		}
	}
	if len(m.sources) == 0 {
		return
	}
	for _, item := range items {
		operation, input := strings.CutPrefix(item.ID, "input:")
		if !input || item.Kind != "user" || item.Status != "sent" {
			continue
		}
		source, previous := m.sources[operation]
		if !previous {
			continue
		}
		delete(m.sources, operation)
		if animate && send.operation == "" {
			steer := source.turn != "" && source.turn == item.TurnID
			send.begin(operation, epoch, source.bounds, steer, true)
			if steer && len(queue.Items) == 1 && queue.Items[0].Input.OperationID == operation {
				// A sent item can reach the transcript before its queue update.
				m.consumed = operation
			}
		}
	}
}

func (m *mobileQueueMotion) steerDeparture(c *ui.Context, send *mobileSendScroll, active bool) (remaining float32, lifting, ok bool) {
	if len(m.display.Items) != 1 || c.Preferences().ReduceMotion {
		return 0, false, false
	}
	operation := m.display.Items[0].Input.OperationID
	if send.operation == operation && send.steer && send.queued && (send.started || m.following) {
		return 1 - send.progress, send.started, true
	}
	if source := m.sources[operation]; m.following && active && source.turn != "" && !source.left.IsZero() {
		// The queue update can also arrive first. Keep the panel in place
		// while the confirmed input is still on its way to the transcript.
		c.After(queueSourceLifetime - c.Now().Sub(source.left))
		return 1, false, true
	}
	return 0, false, false
}

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
	steer  bool
}

type mobileQueueMotion struct {
	epoch, arrival, animating string
	arrivalSource             ui.Rect
	display                   domain.QueueState
	sources                   map[string]mobileQueueSource
}

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
		m.epoch, m.sources = epoch, nil
	}
	for operation, source := range m.sources {
		if slices.ContainsFunc(queue.Items, func(item domain.QueueItem) bool { return item.Input.OperationID == operation }) {
			continue
		}
		if source.left.IsZero() {
			source.left = c.Now()
			m.sources[operation] = source
		} else if c.Now().Sub(source.left) > 5*time.Second {
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
			send.begin(operation, epoch, source.bounds, source.steer, true)
		}
	}
}

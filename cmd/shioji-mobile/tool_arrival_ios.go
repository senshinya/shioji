package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

const mobileToolCallEntrance = 200 * time.Millisecond

type mobileToolCallArrivals map[string]time.Time

func (a *mobileToolCallArrivals) observe(now time.Time, tools []domain.Item, animate bool) {
	current := make(mobileToolCallArrivals, len(tools))
	for _, tool := range tools {
		var start time.Time
		if animate && *a != nil {
			var seen bool
			start, seen = (*a)[tool.ID]
			if !seen {
				start = now
			}
		}
		current[tool.ID] = start
	}
	*a = current
}

func (a mobileToolCallArrivals) progress(now time.Time, id string) float32 {
	start := a[id]
	if start.IsZero() {
		return 1
	}
	elapsed := min(1, float32(now.Sub(start))/float32(mobileToolCallEntrance))
	return ui.EaseOut(max(0, elapsed))
}

func mobileToolCallLayout(c *ui.Context, progress float32, build func(ui.Element)) {
	slot := ui.Column(c).FillWidth().Shrink(0)
	slot.Children(func() {
		content := ui.Column(c.Key("natural-call")).FillWidth().Shrink(0)
		if progress < 1 {
			// The outer virtual list measures this slot. Keep the call's text
			// and controls at their natural size inside the animated clip.
			slot.Height(content.LayoutBounds().H * progress).ClipY().Opacity(progress)
			content.Absolute().Left(0).Top(0).Right(0)
			ui.Inert(slot, progress == 0)
		}
		build(content)
	})
}

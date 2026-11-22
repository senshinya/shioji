package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

type workHistorySection uint8

const (
	workHistoryHeader workHistorySection = iota
	workHistoryOlder
)

func workHistoryList(c *ui.Context, list *ui.ListState, history *domain.HistoryWindow, items []domain.Item, read *mobileRequest, connected bool,
	load func(int64), header func(*ui.Context), entry func(*ui.Context, domain.Item)) {
	first := 1
	if history != nil && history.Older {
		first++
	}
	end := first + len(items)
	list.Key = func(index int) any {
		switch {
		case index == 0:
			return workHistoryHeader
		case index < first:
			return workHistoryOlder
		default:
			return items[index-first].ID
		}
	}
	list.SkipAnchor = func(index int) bool { return index < first }
	p := palette(c)
	ui.List(c, list, end, func(index int) {
		row := c.Key(list.Key(index))
		content := mobileContent(row, contentMaxWidth).PaddingX(pageInset)
		content.Children(func() {
			switch {
			case index == 0:
				header(row)
			case index < first:
				control := ui.Column(row).AlignSelf(ui.Start)
				control.Children(func() {
					read.status(row)
					action(row, "加载更早消息", func() {
						bounds := control.Bounds()
						// The recent page can be shorter than the viewport. Retain
						// its trailing reading space when prepending older rows.
						list.KeepRow(first, bounds.Y+bounds.H-list.Bounds(c).Y)
						load(history.First)
					}).PaddingX(0).Disabled(!connected || read.busy)
				})
			default:
				top := float32(transcriptItemGap / 2)
				if item := items[index-first]; item.Kind == "user" && index > first {
					top = transcriptTurnGap - transcriptItemGap/2
				}
				ui.Column(row).Padding(top, 0, transcriptItemGap/2).Children(func() {
					entry(row, items[index-first])
				})
			}
		})
	}).Grow(1).Basis(0).MinHeight(0).Background(p.canvas)
}

// Child history includes fragments and can lack turn IDs. Group final reply
// fragments until the next user message or a known turn boundary.
func childReplyFooters(items []domain.Item) map[string]string {
	footers := make(map[string]string)
	var turn, lastID string
	var parts []string
	finish := func() {
		if lastID != "" {
			footers[lastID] = strings.Join(parts, "\n\n")
		}
		lastID = ""
		parts = parts[:0]
	}
	for _, item := range items {
		if item.Kind == "user" || turn != "" && item.TurnID != "" && turn != item.TurnID {
			finish()
		}
		if item.TurnID != "" {
			turn = item.TurnID
		}
		if item.Kind == "assistant" && item.Final && strings.TrimSpace(item.Text) != "" {
			lastID = item.ID
			parts = append(parts, item.Text)
		}
	}
	finish()
	return footers
}

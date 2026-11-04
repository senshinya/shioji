package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strings"
)

type transcriptRow struct {
	key   string
	item  domain.Item
	end   int // original event index; -1 for an expanded group heading
	group string
	count int
}

// Project groups into the outer virtual list so expanded tool history still
// unmounts normally. While viewing, only nonempty agent prose auto-closes a group.
func (v *View) transcriptRows(items []domain.Item) []transcriptRow {
	// A cached projection and a freshly hydrated history each start collapsed
	// when opened. Consume this only after actual session history is available;
	// pending-request snapshots can create an empty detail before hydration.
	collapseHistory := v.Model.FoldToolHistory[v.Model.SessionID] && v.Model.Details[v.Model.SessionID].Session.ID != ""
	if collapseHistory {
		delete(v.Model.FoldToolHistory, v.Model.SessionID)
	}
	laterProse := make([]bool, len(items)+1)
	for i := len(items) - 1; i >= 0; i-- {
		laterProse[i] = laterProse[i+1] || items[i].Kind == "assistant" && strings.TrimSpace(items[i].Text) != ""
	}
	rows := make([]transcriptRow, 0, len(items))
	for i := 0; i < len(items); {
		item := items[i]
		if item.Kind == "assistant" && strings.TrimSpace(item.Text) == "" {
			if len(rows) > 0 {
				rows[len(rows)-1].end = i
			}
			i++
			continue
		}
		if item.Kind != "tool" {
			rows = append(rows, transcriptRow{key: item.ID, item: item, end: i})
			i++
			continue
		}
		end, count := i+1, 1
		for end < len(items) && items[end].TurnID == item.TurnID {
			if items[end].Kind == "tool" {
				count++
				end++
				continue
			}
			if items[end].Kind == "assistant" && strings.TrimSpace(items[end].Text) == "" {
				end++
				continue
			}
			break
		}
		key := "tool-group:" + v.Model.SessionID + "/" + item.ID
		seen := key + ":prose"
		if collapseHistory {
			v.Model.Collapsed[key] = true
		}
		if laterProse[end] && !v.Model.Collapsed[seen] {
			v.Model.Collapsed[key] = true
			v.Model.Collapsed[seen] = true
		}
		row := transcriptRow{key: key, item: item, end: -1, group: key, count: count}
		if v.Model.Collapsed[key] {
			row.end = end - 1
		}
		rows = append(rows, row)
		if !v.Model.Collapsed[key] {
			for j := i; j < end; j++ {
				if items[j].Kind == "tool" {
					rows = append(rows, transcriptRow{key: items[j].ID, item: items[j], end: j})
				} else if len(rows) > 0 {
					rows[len(rows)-1].end = j
				}
			}
		}
		i = end
	}
	return rows
}
func (v *View) toolGroup(c *n.Context, t *n.Theme, row transcriptRow) {
	label := fmt.Sprintf("%d 次工具调用", row.count)
	if n.ButtonBase(c).FillWidth().Height(20).Justify(n.Start).Label(label).Children(func() {
		n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
			icon := chevron
			if v.Model.Collapsed[row.group] {
				icon = rightChevron
			}
			font := n.Font{Family: "system-ui", Size: 11, Weight: 400}
			inlineSymbol(c, terminalIcon, label, font, 12, subtle(t))
			n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			inlineSymbol(c, icon, label, font, 10, subtle(t))
		})
	}).Clicked() {
		v.Model.Collapsed[row.group] = !v.Model.Collapsed[row.group]
	}
}

var rightChevron = svg("M8 5l5 5-5 5")

package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strings"
	"time"
)

type transcriptRow struct {
	key        string
	item       domain.Item
	end        int // original event index; -1 for an expanded group heading
	group      string
	count      int
	toolParent string
	opacity    float32
	footer     string
	toolResult string
}

// A turn receipt is its own stable row below a disclosure, so it follows the
// group's changing height rather than disappearing inside its clipped body.
func withTurnFooters(rows []transcriptRow, items []domain.Item, turns map[string]domain.Turn) []transcriptRow {
	result := make([]transcriptRow, 0, len(rows)+len(turns))
	for i, row := range rows {
		result = append(result, row)
		if i+1 < len(rows) && rows[i+1].toolResult != "" && rows[i+1].toolParent == row.toolParent {
			continue
		}
		if row.end >= 0 && row.item.TurnID != "" && (row.end == len(items)-1 || items[row.end+1].TurnID != row.item.TurnID) {
			if _, ok := turns[row.item.TurnID]; ok {
				result = append(result, transcriptRow{key: "turn-footer:" + row.item.TurnID, footer: row.item.TurnID, end: -1})
			}
		}
	}
	return result
}

// Project groups into the outer virtual list so expanded tool history still
// unmounts normally. While viewing, only nonempty agent prose auto-closes a group.
func (v *View) transcriptRows(items []domain.Item) []transcriptRow {
	v.toolsAnimating = false
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
			delete(v.toolMotions, key)
		}
		if laterProse[end] && !v.Model.Collapsed[seen] {
			v.Model.Collapsed[key] = true
			v.Model.Collapsed[seen] = true
			delete(v.toolMotions, key)
		}
		opacity, show := float32(1), !v.Model.Collapsed[key]
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
		row := transcriptRow{key: key, item: item, end: -1, group: key, count: count}
		if !show {
			row.end = end - 1
		}
		rows = append(rows, row)
		if show {
			for j := i; j < end; j++ {
				if items[j].Kind == "tool" {
					rows = append(rows, transcriptRow{key: items[j].ID, item: items[j], end: j, toolParent: key, opacity: opacity})
				} else if len(rows) > 0 {
					rows[len(rows)-1].end = j
				}
			}
			if result := toolGroupResult(items[i:end]); result != "" {
				rows = append(rows, transcriptRow{key: key + ":result", item: item, end: end - 1, toolParent: key, toolResult: result, opacity: opacity})
			}
		}
		i = end
	}
	return rows
}
func (v *View) toolGroup(c *n.Context, t *n.Theme, row transcriptRow, index int) {
	label := fmt.Sprintf("%d 次工具调用", row.count)
	button := n.ButtonBase(c).FillWidth().Height(20).Justify(n.Start).Label(label)
	color := textHoverColor(c, button, t.TextMuted)
	if button.Children(func() {
		n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
			angle := float32(0)
			if v.Model.Collapsed[row.group] {
				angle = -90
			}
			font := n.Font{Family: "system-ui", Size: 11, Weight: 400}
			n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(color)
			inlineSymbol(c, chevron, label, font, 10, subtle(t), angle)
		})
	}).Clicked() {
		list := v.lists[v.Model.SessionID]
		list.KeepRow(index, button.Bounds().Y-list.Bounds(c).Y)
		from, to := float32(1), float32(0)
		if v.Model.Collapsed[row.group] {
			from, to = 0, 1
		}
		if motion := v.toolMotions[row.group]; motion != nil {
			from, _ = motion.sample(c.Now())
		}
		if v.toolMotions == nil {
			v.toolMotions = map[string]*toolDisclosureMotion{}
		}
		v.toolMotions[row.group] = &toolDisclosureMotion{from: from, to: to, start: c.Now(), duration: motionAnimation(c, 160*time.Millisecond).Duration}
		v.Model.Collapsed[row.group] = !v.Model.Collapsed[row.group]
		c.AnimationFrame()
	}
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
		if item.Status == "failed" || item.ExitCode != nil && *item.ExitCode != 0 {
			result = "failed"
		} else if item.Status == "declined" && result != "failed" {
			result = "declined"
		}
	}
	return result
}

var toolCompleteIcon = svg("M18 10a8 8 0 1 1-16 0a8 8 0 0 1 16 0M6 10l3 3 5-6")
var toolFailedIcon = svg("M18 10a8 8 0 1 1-16 0a8 8 0 0 1 16 0M7 7l6 6M13 7l-6 6")
var toolDeclinedIcon = svg("M18 10a8 8 0 1 1-16 0a8 8 0 0 1 16 0M6 10h8")

func (v *View) toolTimelineItem(c *n.Context, t *n.Theme, row transcriptRow) {
	icon := terminalIcon
	if row.toolResult == "" {
		v.item(c, t, row.item)
	} else {
		label := "已完成"
		icon = toolCompleteIcon
		if row.toolResult == "failed" {
			label, icon = "执行失败", toolFailedIcon
		}
		if row.toolResult == "declined" {
			label, icon = "已拒绝", toolDeclinedIcon
		}
		n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
	}
	// A small neutral mask interrupts the rail around each native glyph.
	n.Column(c).Absolute().Left(0).Top(0).Size(16, 20).AlignItems(n.Center).Justify(n.Center).Background(t.Background).Children(func() {
		n.Icon(c, icon).Size(14, 14).TextColor(t.TextMuted).Role(n.RoleNone)
	})
}

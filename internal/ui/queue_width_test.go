package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestQueuePreviewUsesHiddenActionsSpaceAndRestoresHoveredControls(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := nativeQueueFixture()
			ui.SetDark(dark)
			text := strings.Repeat("较长的待发送消息，应在未悬停时展示更多内容。", 10)
			q := v.Model.Queue()
			q.Revision++
			q.Items[0].Input.Text = text
			v.Model.SetQueue(q)
			v.Model.QueueExpanded["s"] = true
			ui.Frame()
			ui.Advance(time.Second)
			viewport, _ := ui.Find("待发送消息列表")
			number, _ := ui.Find("1")
			rest, ok := ui.Find(text)
			if !ok {
				t.Fatal("queued preview missing")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("queue-width-%v-rest.png", dark))
			left := number.X - viewport.X
			right := viewport.X + viewport.W - rest.X - rest.W
			if math.Abs(float64(left-right)) > .5 {
				t.Error("hidden actions still reserve space instead of matching row insets", left, right, rest)
			}
			ui.Move(rest.X+40, rest.Y+rest.H/2)
			ui.Advance(180 * time.Millisecond)
			hovered, _ := ui.Find(text)
			send, _ := ui.Find("立即发送待发消息 1")
			remove, _ := ui.Find("移除待发消息 1")
			saveNativeSkillImage(t, ui, fmt.Sprintf("queue-width-%v-hover.png", dark))
			if hovered.X != rest.X || hovered.Y != rest.Y || hovered.W >= rest.W-60 || hovered.X+hovered.W > send.X-2.5 || math.Abs(float64(remove.X+remove.W-rest.X-rest.W)) > .5 {
				t.Error("hover does not restore the original controls without shifting the row", rest, hovered, send, remove)
			}
			ui.Move(0, 0)
			ui.Advance(50 * time.Millisecond)
			partial, _ := ui.Find(text)
			if partial.W <= hovered.W || partial.W >= rest.W {
				t.Error("pointer exit jumps instead of returning the text width", hovered, partial, rest)
			}
			ui.Move(rest.X+40, rest.Y+rest.H/2)
			ui.Advance(180 * time.Millisecond)
			reversed, _ := ui.Find(text)
			if math.Abs(float64(reversed.W-hovered.W)) > .5 {
				t.Error("reversing hover does not settle at the control layout", reversed, hovered)
			}
			ui.Move(0, 0)
			ui.Advance(180 * time.Millisecond)
			back, _ := ui.Find(text)
			if math.Abs(float64(back.W-rest.W)) > .5 {
				t.Error("text does not reclaim hidden button space", back, rest)
			}
			for i := 0; i < 80 && !ui.Focused("编辑待发消息 1"); i++ {
				ui.Key(0, n.KeyTab)
			}
			ui.Advance(180 * time.Millisecond)
			focused, _ := ui.Find(text)
			if !ui.Focused("编辑待发消息 1") || math.Abs(float64(focused.W-hovered.W)) > .5 {
				t.Error("keyboard focus cannot expose queued actions", focused, hovered)
			}
		})
	}
}

package ui

import (
	"fmt"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestSendingAtBottomNeverFlashesLatestControl(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprint("reduced=", reduced), func(t *testing.T) {
			v, ui, job := sendFixture(t, false, func(m *app.Model) {
				d := m.Details["s"]
				for i := 0; i < 40; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("history-", i), Kind: "assistant", Text: strings.Repeat("已经阅读的历史消息。", 20), Final: true})
				}
				m.Details["s"] = d
			})
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)
			if !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
				t.Fatal("fixture is not following the bottom")
			}
			acceptSend(t, v, job, true)
			d := v.Model.Details["s"]
			d.Items[len(d.Items)-1].TurnID = "new-turn"
			started := ui.Now()
			d.Turns = append(d.Turns, domain.Turn{ID: "new-turn", Status: domain.ExecutionRunning, StartedAt: &started})
			v.Model.Details["s"] = d
			for frame := 0; frame < 40; frame++ {
				ui.Frame()
				saveNativeSkillImage(t, ui, fmt.Sprintf("send-frame-%03d.png", frame))
				if _, shown := ui.Find("回到最新"); shown {
					saveNativeSkillImage(t, ui, "send-latest-flash.png")
					t.Errorf("frame %d flashes the latest control during an accepted bottom send", frame)
				}
				ui.Advance(time.Second / 60)
			}
		})
	}
}

package ui

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestRunningActivityElapsedUsesLatestAcceptedSameTurnInput(t *testing.T) {
	start := time.Unix(1800000000, 0)
	first := start.Add(-time.Second)
	steer := start.Add(4 * time.Minute)
	newest := start.Add(6 * time.Minute)
	for _, tc := range []struct {
		name string
		last domain.Item
		want time.Time
	}{
		{"initial-input", domain.Item{}, start},
		{"inserted", domain.Item{Kind: "user", TurnID: "t", SentAt: &steer}, steer},
		{"waiting-for-insertion", domain.Item{Kind: "user", TurnID: "t", Status: "dispatching", SentAt: &steer}, start},
		{"next-insertion", domain.Item{Kind: "user", TurnID: "t", SentAt: &newest}, newest},
		{"queued-not-inserted", domain.Item{Kind: "queued", TurnID: "t", SentAt: &newest}, start},
		{"another-turn", domain.Item{Kind: "user", TurnID: "other", SentAt: &newest}, start},
		{"child-input", domain.Item{Kind: "user", TurnID: "t", WorkID: "child", SentAt: &newest}, start},
		{"request-result", domain.Item{Kind: "request_result", TurnID: "t", SentAt: &newest}, start},
		{"missing-worker-time", domain.Item{Kind: "user", TurnID: "t"}, start},
		{"missing-time-after-insertion", domain.Item{Kind: "user", TurnID: "t"}, steer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			d.Turns[0].StartedAt = &start
			d.Items = []domain.Item{{ID: "initial", Kind: "user", TurnID: "t", SentAt: &first, Text: "原问题", Final: true}}
			if tc.name == "next-insertion" || tc.name == "missing-time-after-insertion" {
				d.Items = append(d.Items, domain.Item{ID: "earlier-steer", Kind: "user", TurnID: "t", SentAt: &steer, Text: "第一次插入", Final: true})
			}
			tc.last.ID = "latest"
			d.Items = append(d.Items, tc.last)
			v.Model.Details["s"] = d
			v.Model.Drafts["session:s"] = "未发送草稿"
			if got := v.runningActivity(d.Turns[0]).started; got == nil || !got.Equal(tc.want) {
				t.Fatal("incorrect live elapsed origin", got, tc.want)
			}
			if !v.Model.Details["s"].Turns[0].StartedAt.Equal(start) {
				t.Fatal("display reset rewrites the authoritative turn start")
			}
		})
	}
}

// The footer and compact tool header must consume the same new input time.
// Native clock pixels/width catch a component that still reads StartedAt.
func TestNativeElapsedResetsInFooterAndToolHeader(t *testing.T) {
	for _, header := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("header=%v/dark=%v", header, dark), func(t *testing.T) {
				v := liveCompletionModel()
				d := v.Model.Details["s"]
				start := time.Now().Add(-10 * time.Minute)
				d.Turns[0].StartedAt = &start
				d.Items = []domain.Item{{ID: "initial", TurnID: "t", Kind: "user", Text: "原问题", SentAt: &start, Final: true}}
				v.Model.Details["s"] = d
				var font string
				ui := n.NewTester(func(c *n.Context) {
					font = c.Theme().Font
					n.Column(c).Fill().Padding(20).Background(c.Theme().Background).Children(func() {
						if header {
							v.runningGroup = "current-group"
							v.runningProgress = v.progressFor(c, d.Turns[0])
							v.toolGroupTitle(c, c.Theme(), transcriptRow{group: "current-group", count: 1, item: domain.Item{TurnID: "t"}}, 0)
						} else {
							v.turnFooter(c, c.Theme(), d.Turns[0])
						}
					})
				}, 620, 110)
				ui.SetDark(dark)
				start = ui.Now().Add(-10 * time.Minute)
				ui.Frame()
				clock, ok := ui.Find("运行时长")
				if !ok {
					t.Fatal("missing native elapsed clock")
				}
				before := contextPixels(ui, clock)
				saveNativeSkillImage(t, ui, fmt.Sprintf("clock-header-%v-dark-%v-before.png", header, dark))
				accepted := ui.Now()
				d.Items = append(d.Items, domain.Item{ID: "inserted", TurnID: "t", Kind: "user", Text: "继续插入", Status: "sent", SentAt: &accepted, Final: true})
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(250 * time.Millisecond)
				clock, _ = ui.Find("运行时长")
				saveNativeSkillImage(t, ui, fmt.Sprintf("clock-header-%v-dark-%v-after.png", header, dark))
				if header {
					want, _ := n.ShapeText("0 秒", n.Font{Family: font, Size: 11, Weight: 400, Features: "tnum"}, 0).Size()
					if diff := clock.W - want - 6; diff > .5 || diff < -.5 {
						t.Error("compact header did not reset to zero seconds", clock.W, want+6)
					}
				} else if bytes.Equal(before, contextPixels(ui, clock)) {
					t.Error("footer clock still paints the original ten-minute duration")
				}
				ui.Advance(time.Second)
				v.Model.ConnectionEpoch = "reconnected"
				ui.Frame()
				if got := v.runningActivity(d.Turns[0]).started; got == nil || !got.Equal(accepted) {
					t.Error("reconnect loses the accepted insertion's elapsed origin", got)
				}
				if !header {
					total := int64(600000)
					d.Turns[0].Status, d.Turns[0].DurationMS = "completed", &total
					v.Model.Details["s"] = d
					ui.Frame()
					ui.Advance(250 * time.Millisecond)
					if !ui.HasText("用时 10分0秒") {
						t.Fatal("display reset replaced the worker's completed-turn duration", ui.Texts())
					}
				}
			})
		}
	}
}

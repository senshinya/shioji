package ui

import (
	"fmt"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestOpeningSessionExpandsOnlyItsLatestExecutingToolGroup(t *testing.T) {
	cases := []struct {
		name       string
		status     domain.ExecutionState
		firstFinal bool
		lastFinal  bool
		following  string
		connected  bool
		open       bool
	}{
		{"running", "running", true, false, "", true, true},
		{"starting", "starting", true, false, "", true, true},
		{"earlier-call-running", "running", false, true, "", true, true},
		{"completed", "completed", true, true, "", true, false},
		{"unknown", "unknown", true, false, "", true, false},
		{"disconnected", "running", true, false, "", false, false},
		{"between-calls", "running", true, true, "", true, false},
		{"following-user", "running", true, false, "user", true, false},
		{"following-request", "running", true, false, "request", true, false},
	}
	for _, dark := range []bool{false, true} {
		for _, delayed := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/dark=%v/delayed=%v", tc.name, dark, delayed), func(t *testing.T) {
					m := layoutModel()
					m.Connected = tc.connected
					m.Collapsed["tool-group:s/first"] = true
					m.Collapsed["tool-group:s/first:prose"] = true
					d := m.Details["s"]
					d.Session.Status = tc.status
					d.Items = []domain.Item{
						{ID: "old", TurnID: "old-turn", Kind: "tool", Name: "较早的工具调用", Status: "completed", Final: true},
						{ID: "body", TurnID: "old-turn", Kind: "assistant", Text: "上一轮已经完成。", Final: true},
						{ID: "first", TurnID: "current", Kind: "tool", Presentation: "command", Name: "最新组的第一个调用", Text: "first output", Final: tc.firstFinal},
						{ID: "last", TurnID: "current", Kind: "tool", Presentation: "command", Name: "最新组的第二个调用", Text: "last output", Final: tc.lastFinal},
					}
					if tc.following != "" {
						d.Items = append(d.Items, domain.Item{ID: "following", TurnID: "current", Kind: tc.following, Text: "后续非工具内容", Final: true})
					}
					d.Turns = []domain.Turn{{ID: "old-turn", Status: "completed"}, {ID: "current", Status: tc.status}}
					m.Snapshot.Sessions[0] = d.Session
					m.Details["s"] = d
					if delayed {
						m.Details["s"] = domain.SessionDetail{}
					}
					v := New(m, nil, "")
					ui := n.NewTester(v.View, 1240, 820)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					if delayed {
						m.Details["s"] = d
						ui.Frame()
						ui.Advance(time.Second)
					}
					if tc.name == "running" && !delayed {
						saveNativeSkillImage(t, ui, fmt.Sprintf("live-history-dark-%v.png", dark))
					}
					if ui.HasText("较早的工具调用") {
						t.Error("entering a running session expands an old tool group")
					}
					if ui.HasText("最新组的第二个调用") != tc.open {
						t.Fatalf("latest executing tool group has wrong initial disclosure: want open=%v", tc.open)
					}
					if !tc.open {
						return
					}
					if err := ui.Click("2 次工具调用"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(200 * time.Millisecond)
					d.Items[3].Text = "后续工具输出"
					m.Details["s"] = d
					ui.Frame()
					ui.Advance(time.Second)
					if ui.HasText("最新组的第二个调用") {
						t.Fatal("ordinary updates override manual collapse")
					}
					v.home()
					ui.Frame()
					if os.Getenv("SHIOJI_LIVE_TOOL_HISTORY_MOTION") != "" && tc.name == "running" && !delayed && dark {
						for frame := 0; frame < 90; frame++ {
							if frame == 24 {
								m.SessionID = "s"
								ui.Frame()
							}
							saveNativeSkillImage(t, ui, fmt.Sprintf("entry-frame-%03d.png", frame))
							ui.Advance(time.Second / 60)
						}
					}
					m.SessionID = "s"
					ui.Frame()
					ui.Advance(time.Second)
					if !ui.HasText("最新组的第二个调用") || ui.HasText("较早的工具调用") {
						t.Fatal("switching back does not reopen only the latest executing group")
					}
					d.Items = append(d.Items, domain.Item{ID: "after-entry", TurnID: "next-turn", Kind: "user", Text: "切回后发起的新一轮", Status: "sent", Final: true})
					m.Details["s"] = d
					ui.Frame()
					ui.Advance(200 * time.Millisecond)
					if ui.HasText("最新组的第二个调用") {
						t.Fatal("new non-tool content does not close the reopened executing group")
					}
				})
			}
		}
	}
}

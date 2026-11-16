package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// A completed turn containing an appended user input, as in the user's
// recording: prose, three calls, another user message, more calls and a reply.
func disclosureJitterFixture() *View {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 24; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("older-", i), Kind: "assistant", Text: "Earlier conversation paragraph.", Final: true})
	}
	d.Items = append(d.Items,
		domain.Item{ID: "user", TurnID: "t", Kind: "user", Text: "已恢复", Final: true},
		domain.Item{ID: "before", TurnID: "t", Kind: "assistant", Text: "我会重新核对 origin/main，然后推送已完成的提交并确认远端结果。", Final: true},
	)
	for i, command := range []string{"git status --short; git log -3 --oneline; git rev-parse HEAD; git branch --show-current; git remote get-url origin; git ls-remote origin refs/heads/main", "git push origin main", "git ls-remote origin refs/heads/main; git status --short; git rev-parse HEAD origin/main"} {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("first-", i), TurnID: "t", Kind: "tool", Presentation: "command", Name: command, Status: "completed", Final: true})
	}
	d.Items = append(d.Items,
		domain.Item{ID: "steer", TurnID: "t", Kind: "user", Text: "两个上游都 push", Final: true},
		domain.Item{ID: "after", TurnID: "t", Kind: "assistant", Text: "origin/main 已推送成功。我会继续推送 upstream（GitHub）的 main，并核对两个远端都指向 e9c3286。", Final: true},
	)
	for i := 0; i < 4; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("second-", i), TurnID: "t", Kind: "tool", Presentation: "command", Name: fmt.Sprint("git verification ", i), Status: "completed", Final: true})
	}
	d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "两个远端的 main 均已推送，并确认指向 e9c3286。\n\nGitHub CI 正在运行。", Final: true})
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	m.Details["s"] = d
	return New(m, nil, "")
}

func TestEndedToolDisclosureBeforeUserMessageKeepsReadingPositionEveryFrame(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, zoom := range []float32{1, 1.25} {
			for _, reduced := range []bool{false, true} {
				for _, reading := range []bool{false, true} {
					t.Run(fmt.Sprintf("dark=%v/zoom=%v/reduced=%v/reading=%v", dark, zoom, reduced, reading), func(t *testing.T) {
						v := disclosureJitterFixture()
						v.Model.Preferences.InterfaceZoom = zoom
						v.Model.Preferences.ReduceMotion = reduced
						v.Model.Drafts["session:s"] = "保留普通草稿"
						ui := n.NewTester(v.View, int(1440*zoom), int(700*zoom))
						ui.SetDark(dark)
						ui.Advance(time.Second)
						if reading {
							ui.Scroll(1300*zoom, 400*zoom, 0, -40)
						}
						const prose = "我会重新核对 origin/main，然后推送已完成的提交并确认远端结果。"
						before, ok := ui.Find(prose)
						if !ok {
							t.Fatal("missing visible prose above the toggled group")
						}
						// Include full transitions and immediate reversals. The old
						// 40/180 ms endpoint checks missed the first/last bad frames.
						for action, frames := range []int{18, 18, 3, 2, 18, 18} {
							if err := ui.Click("3 次工具调用"); err != nil {
								t.Fatal(err)
							}
							for frame := 0; frame < frames; frame++ {
								current, ok := ui.Find(prose)
								if !ok || math.Abs(float64(current.Y-before.Y)) > .5 {
									t.Fatalf("action %d frame %d moved established prose: before=%+v current=%+v", action, frame, before, current)
								}
								ui.Advance(time.Second / 60)
							}
						}
						if v.Model.Drafts["session:s"] != "保留普通草稿" {
							t.Fatal("disclosure changed the independent draft")
						}
					})
				}
			}
		}
	}
}

func TestEndedToolDisclosureFollowsEachClickWithoutExtraLayoutReversals(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, zoom := range []float32{1, 1.25} {
			for _, rapid := range []bool{false, true} {
				t.Run(fmt.Sprintf("dark=%v/zoom=%v/rapid=%v", dark, zoom, rapid), func(t *testing.T) {
					v := disclosureJitterFixture()
					v.Model.Preferences.InterfaceZoom = zoom
					ui := n.NewTester(v.View, int(1440*zoom), int(700*zoom))
					ui.SetDark(dark)
					ui.Advance(time.Second)
					durations := []time.Duration{time.Second, time.Second, time.Second, time.Second}
					if rapid {
						// Reproduce the rejected recording's three closely spaced
						// clicks, rather than treating them as one opening action.
						durations = []time.Duration{time.Second, time.Second, 85300 * time.Microsecond, 106100 * time.Microsecond, time.Second, time.Second}
					}
					for action, duration := range durations {
						ui.Frame()
						previous := v.historyRowBounds["steer"]
						if previous.H == 0 {
							t.Fatal("missing following user message")
						}
						if err := ui.Click("3 次工具调用"); err != nil {
							t.Fatal(err)
						}
						current := v.historyRowBounds["steer"]
						if math.Abs(float64(current.Y-previous.Y)) > .5 {
							t.Fatalf("action %d snapped on the click: %v -> %v", action, previous.Y, current.Y)
						}
						opening, sawFirst := action%2 == 0, false
						for elapsed := time.Duration(0); elapsed < duration; {
							step := min(time.Second/60, duration-elapsed)
							ui.Advance(step)
							ui.Frame()
							current = v.historyRowBounds["steer"]
							if current.H == 0 || opening && current.Y < previous.Y-.5 || !opening && current.Y > previous.Y+.5 {
								t.Fatalf("action %d reversed layout without another click at %v: %v -> %v", action, elapsed+step, previous.Y, current.Y)
							}
							_, first := v.historyRowBounds["first-0"]
							if opening && sawFirst && !first {
								t.Fatalf("action %d hid the first tool while opening at %v", action, elapsed+step)
							}
							sawFirst = sawFirst || first
							previous = current
							elapsed += step
						}
					}
				})
			}
		}
	}
}

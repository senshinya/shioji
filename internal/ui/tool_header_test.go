package ui

import (
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestToolGroupOutcomeAndLiveStateInBothDisclosureStates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, state := range []string{"running", "completed", "sealed", "failed", "unknown"} {
			t.Run(fmt.Sprintf("dark=%v/state=%s", dark, state), func(t *testing.T) {
				v := toolTailFixture(2)
				live := state == "running"
				outcome := "已完成"
				if !live {
					d := v.Model.Details["s"]
					d.Items[2].Status, d.Items[2].Final = "completed", true
					d.Turns[0].Status = "completed"
					switch state {
					case "sealed":
						d.Turns[0].Status = "running"
						d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "检查通过。"})
					case "failed":
						d.Items[2].Status, outcome = "failed", "1 项未成功"
					case "unknown":
						d.Items[2].Status, outcome = "unknown", "调用结束"
					}
					v.Model.Details["s"] = d
				}
				width := 1240
				if dark {
					width = 950
				}
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if !live {
					if err := ui.Click("2 次工具调用"); err != nil {
						t.Fatal(err)
					}
					ui.Move(float32(width-40), 790)
					ui.Advance(time.Second)
				}
				const disclosure = "2 次工具调用"
				var openHeading n.Rect
				for _, folded := range []bool{false, true} {
					saveNativeSkillImage(t, ui, fmt.Sprintf("tool-group-dark-%v-%s-folded-%v.png", dark, state, folded))
					count, ok := ui.Find("2 次工具调用")
					if !ok || ui.HasText("go test ./internal/ui -run Check0") == folded {
						t.Fatal("tool group failed to disclose its calls", ui.Texts())
					}
					if live {
						phase, phaseOK := ui.Find("正在执行命令")
						clock, clockOK := ui.Find("运行时长")
						status, statusOK := ui.Find("工具组运行状态")
						if !statusOK || !activityGreenIn(ui, status) || !phaseOK || !clockOK || phase.Y != status.Y || clock.Y != status.Y || (status.Y == count.Y) != folded || ui.HasText("已完成") {
							t.Fatal("live group needs one activity line with dot, phase and time", ui.Texts())
						}
					} else {
						result, resultOK := ui.Find(outcome)
						if !resultOK || result.Y != count.Y || activityGreenIn(ui, count) || ui.HasText("执行工具") {
							t.Fatalf("settled result must live on the neutral count heading: heading=%+v result=%+v texts=%v", count, result, ui.Texts())
						}
						if folded && count != openHeading {
							t.Fatal("settled heading moved or resized during disclosure", openHeading, count)
						}
					}
					if !folded {
						openHeading = count
						if err := ui.Click(disclosure); err != nil {
							t.Fatal(err)
						}
						ui.Move(float32(width-40), 790)
						ui.Advance(time.Second)
					}
				}
			})
		}
	}
}

func TestToolGroupOnlyTitleControlsDisclosure(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v := toolTailFixture(2)
			detail := v.Model.Details["s"]
			detail.Items[1].Status = "failed"
			v.Model.Details["s"] = detail
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			key := "tool-group:s/call-0"
			const title = "2 次工具调用"
			before, ok := ui.Find(title)
			issue, issueOK := ui.Find("1 项未成功")
			if !ok || !issueOK || issue.Y != before.Y {
				t.Fatal("expanded activity must keep its issue count in the group header", before, issue)
			}
			for _, label := range []string{"工具组运行状态", "正在执行命令", "运行时长"} {
				r, ok := ui.Find(label)
				if !ok {
					t.Fatalf("lost status %s", label)
				}
				ui.ClickAt(r.X+r.W/2, r.Y+r.H/2)
				ui.Advance(200 * time.Millisecond)
				if v.Model.ToolDisclosure(key).Closed() {
					t.Fatalf("expanded bottom status %s folded the tools", label)
				}
			}
			if err := ui.Click(title); err != nil {
				t.Fatal(err)
			}
			ui.Advance(200 * time.Millisecond)
			if !v.Model.ToolDisclosure(key).Closed() || ui.HasText("执行工具") || ui.HasText("go test ./internal/ui -run Check0") {
				t.Fatal("folded tools must leave only the one-line summary", ui.Texts())
			}
			summary, ok := ui.Find(title)
			foldedIssue, foundIssue := ui.Find("1 项未成功")
			phase, phaseOK := ui.Find("正在执行命令")
			clock, clockOK := ui.Find("运行时长")
			if !ok || !phaseOK || !clockOK || !foundIssue || foldedIssue != issue || summary.Y != before.Y || summary.Y != phase.Y || summary.Y != clock.Y {
				t.Fatal("folded header lost its single-line state and clock", summary, phase, clock)
			}
			if err := ui.Click(title); err != nil {
				t.Fatal(err)
			}
			ui.Advance(200 * time.Millisecond)
			if v.Model.ToolDisclosure(key).Closed() || !ui.HasText("go test ./internal/ui -run Check0") {
				t.Fatal("folded header did not reopen tools")
			}
		})
	}
}

// Completed tools keep a visible cue on their single count heading.
func TestCompletedToolGroupKeepsVisibleDisclosureCue(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v, ui := timelineFixture(t, dark)
			for _, closed := range []bool{false, true} {
				ui.Move(1200, 790)
				ui.Advance(time.Second)
				summary, ok := ui.Find("2 次工具调用")
				if !ok {
					t.Fatal("finished heading missing")
				}
				outcome, found := ui.Find("已完成")
				if right := activityLastInkX(ui, summary, dark); !found || right <= outcome.X+outcome.W+toolSummaryGap {
					t.Fatal("finished heading has no visible disclosure cue", closed, right, summary)
				}
				if v.Model.ToolDisclosure("tool-group:s/a").Closed() != closed {
					t.Fatal("wrong disclosure state", closed)
				}
				if err := ui.Click("2 次工具调用"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCompletedToolCountIsTheOnlyDisclosureHeading(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v, ui := timelineFixture(t, dark)
			heading, ok := ui.Find("2 次工具调用")
			first, firstOK := ui.Find("first command")
			if !ok || !firstOK || heading.Y >= first.Y || ui.HasText("执行工具") {
				t.Fatal("finished count must be the top disclosure title", heading, first, ui.Texts())
			}
			titleY := heading.Y
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			heading, ok = ui.Find("2 次工具调用")
			if !ok || ui.HasText("first command") || heading.Y != titleY {
				t.Fatal("finished title does not fold in place", heading, titleY)
			}
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText("first command") || !ui.HasText("回复信息") {
				t.Fatal("finished heading cannot reopen content and receipt")
			}
			// A single title represents this group; there is no redundant tail row.
			groups := 0
			for _, row := range v.transcriptRows(v.Model.Details["s"].Items) {
				if row.group != "" {
					groups++
					if !row.toolTitle {
						t.Fatal("finished group retains a bottom count row")
					}
				}
			}
			if groups != 1 {
				t.Fatal("finished group lost its single disclosure heading", groups)
			}
		})
	}
}

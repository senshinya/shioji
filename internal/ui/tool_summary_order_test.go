package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestActivityStatusStaysBesideItsTitle(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{375, 768} {
			for _, kind := range []string{"agents", "reasoning", "files", "search", "mcp", "wait"} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%d", kind, dark, width), func(t *testing.T) {
					v := New(layoutModel(), nil, "")
					item := domain.Item{ID: "activity", Kind: "tool", Presentation: kind, Name: "检查边界条件", Text: "结果", Status: "completed", Final: true}
					if kind == "reasoning" {
						item.Kind = "activity"
					}
					ui := n.NewTester(func(c *n.Context) {
						n.Column(c).Fill().Padding(24).Background(c.Theme().Background).Children(func() { v.activityItem(c, c.Theme(), item) })
					}, width, 200)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					title, _ := ui.Find(item.Name)
					status, ok := ui.Find("已完成")
					font := n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 12}
					textWidth, _ := n.ShapeText(item.Name, font, 0).Size()
					iconWidth := float32(0)
					if item.Kind == "activity" {
						iconWidth = 20
					}
					if gap := status.X - title.X - textWidth - iconWidth; !ok || math.Abs(float64(gap-6)) > 1 {
						t.Fatalf("activity status is detached from its title: gap=%.1f", gap)
					}
					item.Name = strings.Repeat("很长的检查名称", 30)
					ui.Frame()
					ui.Advance(time.Second)
					status, ok = ui.Find("已完成")
					if !ok || status.W < 30 || status.X+status.W > float32(width)-23.5 {
						t.Fatal("long activity title displaced its status", status)
					}
				})
			}
		}
	}
}

func TestCommandDisclosureCueRequiresExpandableOutput(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, output := range []string{"", "retained output"} {
			t.Run(fmt.Sprintf("child=%v/output=%v", child, output != ""), func(t *testing.T) {
				v := New(layoutModel(), nil, "")
				item := domain.Item{ID: "command", Kind: "tool", Presentation: "command", Name: "go test ./internal/ui", Text: output, Final: true}
				if child {
					v, _ = sessionWorkFixture(t)
					key := v.Model.WorkKey("s", "agent-a")
					d := v.Model.WorkDetails[key]
					d.Items = []domain.WorkItem{{Item: item}}
					v.Model.WorkDetails[key] = d
				}
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Background(c.Theme().Background).Padding(24).Children(func() {
						if child {
							w := v.Model.Details["s"].Works[0]
							v.sessionWorkDetail(c, c.Theme(), "agents", v.workPanel("agents"), w, 572)
						} else {
							v.item(c, c.Theme(), item)
						}
					})
				}, 620, 400)
				ui.Advance(time.Second)
				heading, found := ui.Find(item.Name)
				if !found {
					t.Fatal("command heading missing")
				}
				font := n.Font{Family: "Menlo", Size: 11}
				prefix := float32(0)
				if child {
					font = n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 12}
					prefix = 20 // command icon and its gap
				}
				width, _ := n.ShapeText(item.Name, font, 0).Size()
				im := ui.Image()
				background := im.RGBAAt(0, 0)
				ink := false
				for y := int(heading.Y); y < int(heading.Y+20); y++ {
					for x := int(heading.X + prefix + width + 6); x < int(heading.X+prefix+width+20); x++ {
						p := im.RGBAAt(x, y)
						ink = ink || max(absChannel(p.R, background.R), absChannel(p.G, background.G), absChannel(p.B, background.B)) > 25
					}
				}
				if ink != (output != "") {
					t.Fatal("command disclosure cue does not match available output", child, output)
				}
				if output != "" {
					wasVisible := ui.HasText(output)
					if err := ui.Click(item.Name); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					if ui.HasText(output) == wasVisible {
						t.Fatal("command disclosure failed to toggle output")
					}
				}
			})
		}
	}
}

func TestToolIssueSummaryPrecedesDisclosureCue(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, ended := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/ended=%v", dark, ended), func(t *testing.T) {
				v := toolTailFixture(2)
				row := transcriptRow{group: "tools", toolParent: "tools", count: 13, issues: 3, toolEnded: ended}
				v.Model.SetToolDisclosure(row.group, v.Model.ToolDisclosure(row.group).Toggle())
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Background(c.Theme().Background).Padding(16).Children(func() {
						if ended {
							v.toolGroupTitle(c, c.Theme(), row, 0)
						} else {
							v.toolGroup(c, c.Theme(), row, 0)
						}
					})
				}, 440, 56)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("dark-%v-ended-%v.png", dark, ended))
				heading, ok := ui.Find("13 次工具调用")
				issue, issueOK := ui.Find("3 项未成功")
				if !ok || !issueOK {
					t.Fatal("missing folded summary", ui.Texts())
				}
				font := n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 11, Weight: 400}
				width, _ := n.ShapeText("13 次工具调用", font, 0).Size()
				if gap := issue.X - heading.X - width - toolGlyphSlot - toolSummaryGap; math.Abs(float64(gap-toolSummaryGap)) > 1 {
					t.Fatalf("issue count must immediately follow tool count, before the chevron; gap=%.1f", gap)
				}
			})
		}
	}
}

// Production native View with normalized worker fixtures; no live worker calls.
func TestToolSummaryOrderNativeRecording(t *testing.T) {
	root := os.Getenv("SHIOJI_SUMMARY_RECORDING")
	if root == "" {
		t.Skip("set SHIOJI_SUMMARY_RECORDING for native evidence")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprint(dark)))
			v := toolTailFixture(2)
			d := v.Model.Details["s"]
			d.Items[1].Status = "failed"
			d.Items[2].Status = "completed"
			d.Items[1].Final, d.Items[2].Final = true, true
			d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "工具调用已结束，继续整理结果。", Final: true})
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			click := func(label string) {
				t.Helper()
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			for frame := 0; frame < 210; frame++ {
				switch frame {
				case 30, 90, 150, 180:
					click("2 次工具调用")
				case 60, 93:
					click("执行工具")
				case 120:
					now := ui.Now()
					d.Turns[0].Status, d.Turns[0].CompletedAt = "completed", &now
					d.Session.Status = "completed"
					v.Model.Details["s"] = d
					v.Model.Snapshot.Sessions[0] = d.Session
					ui.Frame()
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}

func TestMessageDisclosureAuditNativeEvidence(t *testing.T) {
	snapshots := os.Getenv("SHIOJI_SKILL_SNAPSHOTS")
	if snapshots == "" {
		t.Skip("opt-in native component audit")
	}
	for _, dark := range []bool{false, true} {
		for _, width := range []int{768, 375} {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", snapshots)
			v := New(layoutModel(), nil, "")
			items := []domain.Item{
				{ID: "agents", Kind: "tool", Presentation: "agents", Name: "派出子 Agent 检查边界条件", Text: "检查结果", Status: "completed", Final: true},
				{ID: "reason", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "核对执行顺序", Status: "completed", Final: true},
				{ID: "files", Kind: "tool", Presentation: "files", Name: "编辑 registry.go", Text: "修改结果", Status: "completed", Final: true},
				{ID: "search", Kind: "tool", Presentation: "search", Name: "搜索工具调用", Text: "搜索结果", Status: "completed", Final: true},
				{ID: "mcp", Kind: "tool", Presentation: "mcp", Name: "读取 MCP 资源", Text: "资源内容", Status: "completed", Final: true},
				{ID: "wait", Kind: "tool", Presentation: "wait", Name: "等待", Status: "completed", Final: true, Parts: []domain.ContentPart{{Kind: "text", Label: "时长", Text: "45.0 秒"}}},
				{ID: "cmd", Kind: "tool", Presentation: "command", Name: "go test ./internal/ui", Text: "FAIL", Status: "failed", Final: true},
				{ID: "compact", Kind: "activity", Presentation: "compaction", Name: "上下文已压缩", Status: "completed", Final: true},
				{ID: "request", Kind: "request", Text: "选择验收项目", Status: "pending"},
			}
			for _, item := range items {
				v.Model.Collapsed["activity:"+v.Model.SessionID+"/"+item.ID] = true
			}
			v.Model.Collapsed[v.commandOutputKey("cmd", "")] = true
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Background).Padding(24).Gap(20).Children(func() {
					for _, item := range items {
						n.Column(c.Key(item.ID)).FillWidth().Children(func() { v.item(c, c.Theme(), item) })
					}
				})
			}, width, 430)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("audit-%d-dark-%v.png", width, dark))
			if root := os.Getenv("SHIOJI_DISCLOSURE_RECORDING"); root != "" {
				t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprintf("%d-%v", width, dark)))
				click := func(label string) {
					t.Helper()
					if err := ui.Click(label); err != nil {
						t.Fatal(err)
					}
				}
				for frame := 0; frame < 300; frame++ {
					switch frame {
					case 30, 60:
						click(items[0].Name)
					case 90, 93, 96, 150:
						click(items[1].Name)
					case 120:
						items[1].Text += "\n新增的思考摘要内容。"
					case 165:
						items[4].Status, items[4].Final = "running", false
					case 180, 210:
						click(items[4].Name)
					case 195:
						items[4].Text += "\n新到的资源结果。"
					case 225, 255:
						click(items[6].Name)
					case 240:
						items[0].Name = strings.Repeat("较长的子 Agent 检查任务", 8)
					case 270:
						items[4].Status, items[4].Final = "completed", true
					}
					ui.Advance(time.Second / 30)
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				}
			}
		}
	}
}

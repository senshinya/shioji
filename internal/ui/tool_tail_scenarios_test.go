package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Drives the production native View with normalized worker data and pointer
// input. Capture only when requested; these clips do not exercise a live worker.
func TestToolTailScenarioRecordings(t *testing.T) {
	root := os.Getenv("SHIOJI_TOOL_SCENARIOS")
	if root == "" {
		t.Skip("set SHIOJI_TOOL_SCENARIOS for the native acceptance videos")
	}
	for _, dark := range []bool{true, false} {
		for _, scene := range []struct {
			name    string
			seconds int
			count   int
		}{
			{"disclosure", 7, 6},
			{"arrivals", 7, 2},
			{"handoff", 7, 2},
			{"requests", 11, 2},
			{"compaction", 9, 2},
			{"large", 16, 76},
		} {
			t.Run(fmt.Sprintf("%s/dark=%v", scene.name, dark), func(t *testing.T) {
				t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprintf("%s-%v", scene.name, dark)))
				v := toolTailFixture(scene.count)
				d := v.Model.Details["s"]
				d.Items[0].Text = "验证执行工具：" + scene.name
				v.Model.Details["s"] = d
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				edit := func(change func(*domain.SessionDetail)) {
					d := v.Model.Details["s"]
					change(&d)
					v.Model.Details["s"] = d
					v.Model.Snapshot.Sessions[0] = d.Session
					ui.Frame()
				}
				click := func(label string) {
					t.Helper()
					if err := ui.Click(label); err != nil {
						t.Fatal(err)
					}
				}
				tool := func() {
					edit(func(d *domain.SessionDetail) {
						count := 0
						for i := range d.Items {
							if d.Items[i].Kind == "tool" {
								d.Items[i].Final, d.Items[i].Status = true, "completed"
								count++
							}
						}
						d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("call-%d", count), TurnID: "t", Kind: "tool", Presentation: "command", Name: fmt.Sprintf("go test ./internal/ui -run Check%d", count), Text: "正在验证新增工具与运行状态…", Status: "running"})
					})
				}
				appendItem := func(item domain.Item) {
					edit(func(d *domain.SessionDetail) { d.Items = append(d.Items, item) })
				}
				request := func(id, kind, title string) {
					edit(func(d *domain.SessionDetail) {
						d.Items = append(d.Items, domain.Item{ID: "source-" + id, TurnID: "t", Kind: "request", Text: title, Status: "pending", Final: true})
						r := domain.PendingRequest{ID: id, ItemID: "source-" + id, SessionID: "s", TurnID: "t", Kind: kind, Title: title, Text: "检查命令输出与阅读锚点", State: "pending", Revision: 1}
						if kind == "questions" {
							r.Questions = []domain.Question{{ID: "q", Text: "先检查哪一项？", Options: []domain.Option{{Label: "展开折叠"}, {Label: "状态交接"}}}}
						}
						d.Requests = []domain.PendingRequest{r}
					})
				}
				resolve := func(id, kind, answer string) {
					edit(func(d *domain.SessionDetail) {
						for i := range d.Items {
							if d.Items[i].ID == "source-"+id {
								d.Items[i].Status = "answered"
							}
						}
						d.Requests = nil
						d.Items = append(d.Items, domain.Item{ID: "receipt-" + id, TurnID: "t", Kind: "request_result", Presentation: kind, Text: answer, Parts: []domain.ContentPart{{Text: answer}}, Final: true})
					})
				}
				var readingY float32
				for frame := 0; frame < scene.seconds*60; frame++ {
					switch scene.name {
					case "disclosure":
						switch frame {
						case 60, 180:
							click("6 次工具调用")
						case 120, 240:
							click("6 次工具调用")
						case 248, 252:
							click("6 次工具调用") // reverse the in-flight motion
						case 300:
							ui.SetPreferences(n.Preferences{ReduceMotion: true})
							click("6 次工具调用")
						case 360:
							click("6 次工具调用")
						}
					case "arrivals":
						switch frame {
						case 60:
							edit(func(d *domain.SessionDetail) {
								d.Items[2].Text += "\n检查输出第一段…\n检查输出第二段…"
							})
						case 120, 240:
							tool()
						case 180:
							click("3 次工具调用")
						case 300:
							click("4 次工具调用")
						case 360:
							edit(func(d *domain.SessionDetail) { d.Items[len(d.Items)-1].Text += "\n新增命令的增量输出。" })
						}
					case "handoff":
						switch frame {
						case 60:
							appendItem(domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "工具摘要仍在末尾，当前回复接过运行状态。"})
						case 120:
							edit(func(d *domain.SessionDetail) {
								d.Items[len(d.Items)-1].Text += "\n\n继续输出正文时，不重新展开旧工具。"
							})
						case 180:
							edit(func(d *domain.SessionDetail) { d.Items[len(d.Items)-1].Final = true })
							tool()
						case 240:
							edit(func(d *domain.SessionDetail) {
								d.Items[len(d.Items)-1].Final, d.Items[len(d.Items)-1].Status = true, "completed"
							})
						case 300:
							tool() // a gap between calls stays in the same group
						case 360:
							appendItem(domain.Item{ID: "reply-again", TurnID: "t", Kind: "assistant", Text: "第二组工具结束，正文再次承接运行状态。"})
						}
					case "requests":
						switch frame {
						case 60:
							now := ui.Now()
							appendItem(domain.Item{ID: "steer", TurnID: "t", Kind: "user", Text: "继续检查，插入这条消息后重新计时。", SentAt: &now, Final: true})
						case 120, 300, 480:
							tool()
						case 180:
							request("question", "questions", "选择验收项目")
						case 240:
							resolve("question", "questions", "已提交回答：展开折叠")
						case 360:
							request("approval", "approval", "执行检查需要授权")
						case 420:
							resolve("approval", "approval", "已允许本次检查")
						case 540:
							appendItem(domain.Item{ID: "reasoning", TurnID: "t", Kind: "activity", Presentation: "reasoning", Text: "整理验收结果。"})
						case 600:
							appendItem(domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "请求已处理，继续输出结果。"})
						}
					case "compaction":
						switch frame {
						case 60:
							appendItem(domain.Item{ID: "compact", TurnID: "t", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", Status: "running"})
						case 180:
							edit(func(d *domain.SessionDetail) {
								d.Items[len(d.Items)-1].Final, d.Items[len(d.Items)-1].Status, d.Items[len(d.Items)-1].Name = true, "completed", "上下文已压缩"
							})
							appendItem(domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "上下文压缩完成，继续处理工具结果。", Final: true})
						case 240:
							tool()
						case 300:
							now := ui.Now()
							edit(func(d *domain.SessionDetail) {
								d.Items[len(d.Items)-1].Final, d.Items[len(d.Items)-1].Status = true, "completed"
								d.Turns[0].Status, d.Turns[0].CompletedAt, d.Session.Status = "completed", &now, "completed"
							})
						case 360:
							click("1 次工具调用")
						case 420:
							click("1 次工具调用")
						}
					case "large":
						switch frame {
						case 60, 600:
							click("消息输入")
							ui.Scroll(1000, 300, 0, -380)
							ui.Frame()
						case 119:
							box, ok := ui.Find("go test ./internal/ui -run Check50")
							if !ok {
								t.Fatal("reading reference is not visible")
							}
							readingY = box.Y
						case 120, 300:
							tool()
						case 180, 780:
							click("回到最新")
						case 210:
							for i, row := range v.transcriptRows(v.Model.Details["s"].Items) {
								if row.toolTitle {
									v.lists["s"].ScrollToRow(i+1, 120, n.Animation{Duration: 80 * time.Millisecond})
									break
								}
							}
							ui.Frame()
						case 240:
							click("78 次工具调用")
						case 360:
							click("78 次工具调用")
						case 420:
							click("78 次工具调用")
						case 480:
							v.home()
							ui.Frame()
						case 540:
							v.Model.SessionID = "s"
							ui.Frame()
						case 660:
							v.Model.Connected = false
							ui.Frame()
						case 720:
							v.Model.Connected, v.Model.ConnectionEpoch = true, "reconnected"
							ui.Frame()
						case 840:
							now := ui.Now()
							edit(func(d *domain.SessionDetail) {
								d.Turns[0].Status, d.Turns[0].CompletedAt, d.Session.Status = "interrupted", &now, "interrupted"
							})
						}
					}
					// Assert settled acceptance states in addition to recording them.
					if scene.name == "arrivals" && frame == 285 {
						if ui.HasText("go test ./internal/ui -run Check3") {
							t.Fatal("a new call reopened the manually folded group")
						}
					}
					if scene.name == "large" && frame == 165 {
						box, ok := ui.Find("go test ./internal/ui -run Check50")
						if !ok || math.Abs(float64(box.Y-readingY)) > 1 {
							t.Fatal("new tool moved the historical reading position", readingY, box)
						}
					}
					if scene.name == "large" && frame == 705 {
						if !ui.HasText("状态待确认") || ui.HasText("运行时长") {
							t.Fatal("disconnected proxy claims live execution")
						}
					}
					if scene.name == "large" && frame == 825 && (!ui.Focused("消息输入") || !v.lists["s"].FollowingEnd()) {
						t.Fatal("return to latest lost typing focus or bottom following")
					}
					ui.Move(1200, 790)
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					ui.Advance(time.Second / 60)
				}
			})
		}
	}
}

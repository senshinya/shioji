package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func occlusionView(kind string) *View {
	m := layoutModel()
	d := m.Details["s"]
	line := strings.Repeat("constBoundary=window.document.querySelector('long-content');", 5)
	code := strings.Repeat(line+"\n", 80)
	switch kind {
	case "command":
		d.Items = nil
		for i := 0; i < 35; i++ {
			d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("command-", i), Kind: "tool", Presentation: "command", Name: line, Text: code, Final: true})
		}
	case "tool-code":
		parts := []domain.ContentPart{}
		for i := 0; i < 12; i++ {
			parts = append(parts, domain.ContentPart{Kind: "code", Text: code})
		}
		d.Items = []domain.Item{{ID: "body", Kind: "tool", Presentation: "mcp", Name: "长代码结果", Parts: parts, Final: true}}
	case "nested-markdown":
		d.Items = []domain.Item{{ID: "body", Kind: "assistant", Text: strings.Repeat(strings.Repeat("> ", 60)+"deeply_nested_content\n\n", 8), Final: true}}
	default:
		d.Items = []domain.Item{{ID: "body", Kind: "assistant", Text: "```javascript\n" + code + "```", Final: true}}
	}
	m.Details["s"] = d
	return New(m, nil, "")
}

func TestConversationChromeCoversHistoryAtThePaneEdges(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1000, 1240, 1800} {
			v := occlusionView("markdown")
			ui := n.NewTester(v.View, width, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			ui.Scroll(float32(width-200), 300, 0, -380)
			ui.Move(0, 0)
			ui.Frame()
			ui.Advance(time.Second)
			history, _ := ui.Find("对话记录")
			toolbar, _ := ui.Find("会话顶栏")
			composer, _ := ui.Find("消息编辑器")
			if toolbar.X != history.X || toolbar.W != history.W {
				t.Errorf("top chrome does not cover the full pane: width=%d history=%v toolbar=%v", width, history, toolbar)
			}
			empty := New(layoutModel(), nil, "")
			d := empty.Model.Details["s"]
			d.Items = nil
			empty.Model.Details["s"] = d
			baseline := n.NewTester(empty.View, width, 820)
			baseline.SetDark(dark)
			baseline.Advance(time.Second)
			a, b := ui.Image(), baseline.Image()
			region := image.Rect(int(composer.X), int(composer.Y+composer.H)-16, int(composer.X+composer.W), 820)
			if differentPixels(a, b, region) > 0 {
				t.Errorf("history is visible below the composer: width=%d dark=%v", width, dark)
			}
		}
	}
}

func TestConversationToolbarActionsAlignWithSidebarFrame(t *testing.T) {
	for _, width := range []int{1000, 1240} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			ui := n.NewTester(v.View, width, 820)
			ui.Advance(time.Second)
			const sidebarFrameTop = 8
			checkTop := func(label string) {
				t.Helper()
				if bounds, ok := ui.Find(label); !ok || bounds.Y != sidebarFrameTop {
					t.Errorf("%s starts at y=%g (found: %v), want sidebar frame y=%d", label, bounds.Y, ok, sidebarFrameTop)
				}
			}
			for _, label := range []string{"当前设备", "保持设备唤醒", "最近消息", "展开侧栏"} {
				checkTop(label)
			}
			if err := ui.Click("展开侧栏"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			checkTop("刷新更改")
			checkTop("关闭更改侧栏")
		})
	}
}

func TestMainWindowPageHeaderInsets(t *testing.T) {
	for _, layout := range []struct {
		width            int
		configurationTop float32
		deviceTop        float32
		deviceLead       string
		tailnetLead      string
	}{
		{1240, 24, 24, "修改设备名称", "Tailnet 网络操作"},
		// Find reports the vertical position of the text inside the narrow back button.
		{850, 8, 15, "返回设备列表", "返回设备列表"},
	} {
		t.Run(fmt.Sprint(layout.width), func(t *testing.T) {
			model := deviceUIModel()
			model.DeviceStates["local"].WorkerMinor = 22
			model.DeviceStates["local"].Readiness.Harnesses = setupCatalog()
			v := New(model, nil, "")
			ui := n.NewTester(v.View, layout.width, 820)
			ui.SetDark(layout.width == 850)
			checkTop := func(page, label string, want float32) {
				t.Helper()
				ui.Frame()
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("header-%s-%d.png", page, layout.width))
				if bounds, ok := ui.Find(label); !ok || bounds.Y < want-.25 || bounds.Y > want+.25 {
					t.Errorf("%s starts at y=%g (found: %v), want y=%g", label, bounds.Y, ok, want)
				}
			}
			if layout.width == 1240 {
				model.SecondarySessionID = "pending"
				checkTop("parallel-loading", "关闭并排会话", 8)
				v.closeParallel()
			}
			v.home()
			checkTop("home", "当前设备", 8)

			v.openArchive()
			checkTop("archive", "归档保留设置", 24)

			v.openSchedulePage()
			checkTop("schedule-list", "新建任务", 24)
			v.newSchedule(v.schedules)
			checkTop("schedule-editor", "返回任务列表", 24)

			v.editWorkspace(model.Snapshot.Workspaces[0])
			checkTop("workspace", "返回", layout.configurationTop)

			v.workspaceEditor = nil
			v.devices = true
			v.deviceSelected = "local"
			checkTop("device", layout.deviceLead, layout.deviceTop)
			for _, label := range []string{"Agent", "配置 Custom Agent 路径"} {
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			checkTop("agent", "返回 Agent 列表", layout.configurationTop)

			model.Tailnet.State = "Running"
			v.deviceSelected = "tailnet"
			checkTop("tailnet", layout.tailnetLead, layout.deviceTop)
		})
	}
}

func differentPixels(a, b *image.RGBA, r image.Rectangle) int {
	count := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				count++
			}
		}
	}
	return count
}

func TestConversationDockKeepsUpperCornersOpenAndCoversLowerCorners(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, mode := range []string{"none", "collapsed", "expanded", "editing"} {
			t.Run(fmt.Sprintf("dark=%v/queue=%s", dark, mode), func(t *testing.T) {
				makeView := func(history bool) (*View, *n.Tester) {
					v := occlusionView("markdown")
					if !history {
						d := v.Model.Details["s"]
						d.Items = nil
						v.Model.Details["s"] = d
					}
					if mode != "none" {
						v.Model.SetQueue(domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "next", Input: domain.Submission{Text: "下一条消息"}}}})
						v.Model.QueueExpanded["s"] = mode != "collapsed"
						if mode == "editing" {
							if err := v.Model.EditQueued("next"); err != nil {
								t.Fatal(err)
							}
						}
					}
					ui := n.NewTester(v.View, 1000, 820)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					return v, ui
				}
				_, ui := makeView(true)
				_, baseline := makeView(false)
				for i := 0; i < 4; i++ {
					ui.Scroll(800, 300, 0, -float32(90+i*7))
					ui.Move(0, 0)
					ui.Frame()
					ui.Advance(time.Second)
					dock, _ := ui.Find("会话底部面板")
					frame, _ := ui.Find("消息编辑区")
					editor, _ := ui.Find("消息编辑器")
					saveNativeSkillImage(t, ui, fmt.Sprintf("corners-dark-%v-queue-%s-step-%d.png", dark, mode, i))
					// Upper corners remain open to scrolling history; lower
					// corners must continue the opaque clearance below the editor.
					for edge, y := range []int{int(dock.Y) + 1, int(editor.Y+editor.H) - 3} {
						for side, x := range []int{int(frame.X) + 1, int(frame.X+frame.W) - 3} {
							r := image.Rect(x, y, x+2, y+2)
							count := differentPixels(ui.Image(), baseline.Image(), r)
							if edge == 1 {
								if count != 0 {
									t.Errorf("history leaks through lower corner: step=%d side=%d pixels=%d region=%v", i, side, count, r)
								}
							} else if count != 4 {
								t.Errorf("rectangular backplate hides history outside rounded corner: step=%d pixels=%d region=%v", i, count, r)
							}
						}
					}
					// Check the interior, the queue/editor overlap and all space
					// below the editor, including both lower corner contours.
					regions := []image.Rectangle{
						image.Rect(int(frame.X)+32, int(dock.Y), int(frame.X+frame.W)-32, int(dock.Y+dock.H)),
						image.Rect(int(frame.X)+2, int(dock.Y)+32, int(frame.X+frame.W)-2, int(editor.Y+editor.H)-32),
						image.Rect(int(frame.X), int(editor.Y+editor.H)-16, int(frame.X+frame.W), 820),
					}
					for _, r := range regions {
						if count := differentPixels(ui.Image(), baseline.Image(), r); count != 0 {
							t.Errorf("history leaks through queue/editor surface: step=%d pixels=%d region=%v", i, count, r)
						}
					}
				}
			})
		}
	}
}

func TestConversationDockPlainStatusStillHasAnOpaqueBackground(t *testing.T) {
	for _, mode := range []string{"notice", "recovery"} {
		makeView := func(history bool) *n.Tester {
			v := occlusionView("markdown")
			d := v.Model.Details["s"]
			if !history {
				d.Items = nil
			}
			if mode == "notice" {
				v.draftSaveError = "save failed"
			} else {
				v.Model.WorkerMinor = 7
				d.Session.Status = "unknown"
				d.Session.Recovery = &domain.SessionRecovery{Phase: "checking"}
			}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1000, 820)
			ui.Advance(time.Second)
			return ui
		}
		ui, baseline := makeView(true), makeView(false)
		ui.Scroll(800, 300, 0, -400)
		ui.Advance(time.Second)
		baseline.Advance(time.Second)
		label := "会话状态面板"
		if mode == "recovery" {
			label = "执行恢复"
		}
		status, ok := ui.Find(label)
		if !ok || status.W < 600 || status.H < 18 {
			t.Fatal("missing status surface", mode, status)
		}
		region := image.Rect(int(status.X)+400, int(status.Y)+2, int(status.X)+600, int(status.Y+status.H)-2)
		if count := differentPixels(ui.Image(), baseline.Image(), region); count != 0 {
			t.Errorf("history leaks through plain dock status: mode=%s pixels=%d", mode, count)
		}
	}
}

func TestConversationContentStaysInsideTheComposerRightEdge(t *testing.T) {
	for _, kind := range []string{"markdown", "tool-code", "command", "nested-markdown"} {
		for _, width := range []int{1000, 1240, 1800} {
			v := occlusionView(kind)
			ui := n.NewTester(v.View, width, 820)
			ui.SetDark(true)
			ui.Advance(time.Second)
			ui.Scroll(float32(width-200), 300, 0, -380)
			ui.Move(0, 0)
			ui.Frame()
			ui.Advance(time.Second)
			composer, _ := ui.Find("消息编辑器")
			history, _ := ui.Find("对话记录")
			dock, _ := ui.Find("会话底部面板")
			im := ui.Image()
			bg := im.RGBAAt(int(history.X+1), 70)
			outside := 0
			for y := 110; y < int(dock.Y-30); y++ {
				for x := int(composer.X + composer.W + 1); x < int(history.X+history.W-conversationScrollbarGutter-1); x++ {
					if im.RGBAAt(x, y) != bg {
						outside++
					}
				}
			}
			if outside > 0 {
				t.Errorf("%s paints past composer right edge: width=%d pixels=%d composer=%v", kind, width, outside, composer)
			}
		}
	}
}

func TestConversationOcclusionNativeRecording(t *testing.T) {
	root := os.Getenv("SHIOJI_OCCLUSION_CAPTURE")
	if root == "" {
		t.Skip("set SHIOJI_OCCLUSION_CAPTURE for native videos")
	}
	kind := os.Getenv("SHIOJI_OCCLUSION_KIND")
	if kind == "" {
		kind = "markdown"
	}
	for _, dark := range []bool{false, true} {
		v := occlusionView(kind)
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprint(dark)))
		for frame := 0; frame < 480; frame++ {
			switch frame {
			case 60, 120:
				ui.Scroll(1000, 300, 0, -380)
			case 180:
				ui.Scroll(1000, 300, 0, 120)
			case 240:
				d := v.Model.Details["s"]
				d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", Kind: "questions", State: "pending", Title: "遮挡检查", Questions: []domain.Question{{ID: "q", Text: "检查内容边界", Custom: true}}}}
				v.Model.Details["s"] = d
			case 300:
				v.Model.Drafts["session:s"] = "第一行\n第二行\n第三行\n第四行\n第五行"
			case 360:
				d := v.Model.Details["s"]
				d.Requests = nil
				v.Model.Details["s"] = d
				v.Model.Drafts["session:s"] = ""
			case 420:
				if err := ui.Click("回到最新"); err != nil {
					t.Fatal(err)
				}
			}
			ui.Move(0, 0)
			ui.Frame()
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			ui.Advance(time.Second / 60)
		}
	}
}

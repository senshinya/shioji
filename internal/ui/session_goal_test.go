package ui

import (
	"fmt"
	"image"
	"math"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func goalLayout(approval, history bool) (*View, *n.Tester) {
	m := layoutModel()
	m.WorkerMinor = protocol.GoalMinor
	m.Readiness.Capabilities.Goals = true
	m.Snapshot.Workspaces[0].Name = "Shioji"
	s := m.Snapshot.Sessions[0]
	s.Title = "验证 Tailnet 重连"
	s.ThreadID = "source"
	s.GitBranch = "fix/tailnet-reconnect"
	s.Goal = &domain.Goal{State: domain.GoalPaused, Objective: "让 Tailnet 重连测试连续通过三轮，确认队列恢复后每条消息只发送一次，并留下可重复执行的验证命令。", ElapsedSeconds: 382, CreatedAt: 200}
	m.Snapshot.Sessions[0] = s
	d := m.Details[s.ID]
	d.Session = s
	d.Items = nil
	d.Turns = nil
	if history {
		corpus := []string{
			"先核对已接纳的执行目录与分支，再用隔离的本地服务复现重连。普通草稿继续保留。",
			"断开之前的连接后，旧连接回执仍有机会迟到。订阅现在使用连接代次判断归属。",
			"检查队列状态后，确认旧回执不能触发新一轮输入。接下来验证重连期间的待处理请求。",
			"定向验证通过。先用相同参数重复运行三次，再把可复跑的命令写进验收记录。",
		}
		for i := 0; i < 11; i++ {
			id := fmt.Sprintf("turn-%d", i)
			d.Items = append(d.Items, domain.Item{ID: id + "/input", TurnID: id, Kind: "user", Text: "先看连接恢复，再检查待发送队列。", Final: true, Order: int64(2*i + 1)})
			d.Items = append(d.Items, domain.Item{ID: id + "/reply", TurnID: id, Kind: "assistant", Text: corpus[i%len(corpus)] + "\n\n" + corpus[(i+1)%len(corpus)], Final: true, Order: int64(2*i + 2)})
			var ms int64 = 18000
			d.Turns = append(d.Turns, domain.Turn{ID: id, Order: int64(i + 1), Status: domain.ExecutionCompleted, DurationMS: &ms})
		}
	}
	if approval {
		d.Requests = []domain.PendingRequest{{ID: "permission", SessionID: s.ID, Kind: "approval", State: "pending", Title: "允许执行此命令？", Text: "go test ./internal/transport -run TestReconnect -count=3"}}
	}
	m.Details[s.ID] = d
	m.Drafts[m.DraftKey()] = "先确认重连后是否还会重复发送。"
	v := New(m, app.NewController(""), "")
	tester := n.NewTester(v.View, 1240, 820)
	tester.Advance(time.Second)
	return v, tester
}

func TestGoalDockKeepsScrollAndApprovalClearOfItsRow(t *testing.T) {
	dir := os.Getenv("SHIOJI_GOAL_RECORDING")
	if dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	}
	capture := func(t *testing.T, tester *n.Tester, name string) {
		t.Helper()
		if dir != "" {
			tester.SetScale(2)
			saveNativeSkillImage(t, tester, name)
			tester.SetScale(1)
		}
	}
	find := func(t *testing.T, tester *n.Tester, name string) n.Rect {
		t.Helper()
		r, ok := tester.Find(name)
		if !ok {
			t.Fatalf("missing %q", name)
		}
		return r
	}
	t.Run("scroll", func(t *testing.T) {
		v, tester := goalLayout(false, true)
		_, empty := goalLayout(false, false)
		goal := find(t, tester, "目标状态行")
		editor := find(t, tester, "消息编辑器")
		resume := find(t, tester, "恢复")
		if _, ok := tester.Find("清除"); ok {
			t.Fatal("clear appeared without hover")
		}
		tester.Scroll(900, 450, 0, -525)
		tester.Move(300, 48)
		tester.Advance(time.Second)
		if v.lists["s"].FollowingEnd() || find(t, tester, "目标状态行") != goal || find(t, tester, "消息编辑器") != editor || find(t, tester, "恢复") != resume {
			t.Fatal("scrolling changed the fixed dock or failed to leave the end")
		}
		rect := image.Rect(int(goal.X)+2, int(goal.Y)+2, int(goal.X+goal.W)-2, int(goal.Y+goal.H)-2)
		if differentPixels(tester.Image(), empty.Image(), rect) != 0 {
			t.Fatal("history painted through the goal row")
		}
		latest := find(t, tester, "回到最新")
		for _, beside := range []image.Rectangle{
			image.Rect(int(goal.X)+8, int(latest.Y)+1, int(latest.X)-8, int(latest.Y+latest.H)-1),
			image.Rect(int(latest.X+latest.W)+8, int(latest.Y)+1, int(goal.X+goal.W)-8, int(latest.Y+latest.H)-1),
		} {
			if differentPixels(tester.Image(), empty.Image(), beside) == 0 {
				t.Fatalf("history is hidden beside the latest control: %v", beside)
			}
		}
		dock := find(t, tester, "会话底部面板")
		for _, clearance := range []image.Rectangle{
			image.Rect(int(goal.X)+8, int(dock.Y), int(goal.X+goal.W)-8, int(goal.Y)),
			image.Rect(int(goal.X)+8, int(goal.Y+goal.H), int(goal.X+goal.W)-8, int(editor.Y)),
		} {
			if differentPixels(tester.Image(), empty.Image(), clearance) != 0 {
				t.Fatalf("history painted into the Goal dock: %v", clearance)
			}
		}
		capture(t, tester, "goal-scroll-up.png")
		summary := find(t, tester, "目标摘要")
		tester.Move(summary.X+summary.W/2, summary.Y+summary.H/2)
		tester.Advance(time.Second)
		if info, ok := tester.AccessByRole(n.RoleTooltip, ""); !ok || info.Bounds.Y+info.Bounds.H > goal.Y {
			t.Fatalf("goal hover hint overlaps its row: %v", info.Bounds)
		}
		if find(t, tester, "清除").X > resume.X || find(t, tester, "恢复") != resume {
			t.Fatal("the Clear disclosure displaced Resume")
		}
	})
	t.Run("approval", func(t *testing.T) {
		_, tester := goalLayout(true, true)
		approval := find(t, tester, "待处理请求卡片")
		goal := find(t, tester, "目标状态行")
		editor := find(t, tester, "消息编辑器")
		if approval.X != goal.X || approval.W != goal.W || approval.Y+approval.H > goal.Y || goal.Y+goal.H > editor.Y {
			t.Fatalf("approval, goal, and composer overlap or have different columns: %v %v %v", approval, goal, editor)
		}
		if above, below := goal.Y-approval.Y-approval.H, editor.Y-goal.Y-goal.H; above != 9 || below != 2 {
			t.Fatalf("goal needs 9 DIP to the approval card and 2 DIP to the editor: got %g DIP above and %g DIP below", above, below)
		}
		find(t, tester, "允许执行此命令？")
		capture(t, tester, "goal-pending-approval.png")
	})
}

func TestGoalResumeUsesTheOwningSessionAndKeepsTheDraft(t *testing.T) {
	v, tester := goalLayout(false, false)
	for len(v.Controller.Jobs) > 0 {
		<-v.Controller.Jobs
	}
	if err := tester.Click("恢复"); err != nil {
		t.Fatal(err)
	}
	job := feedbackJob(t, v, "session.goal.control")
	in, ok := job.Input.(domain.GoalRequest)
	if !ok || in.Action != domain.ResumeGoal || in.SessionID != "s" || in.Target.State != domain.GoalPaused || in.OperationID == "" {
		t.Fatal("Resume did not address the owning goal", job.Input)
	}
	tester.Frame()
	if button, ok := tester.AccessByRole(n.RoleButton, "恢复"); !ok || !button.Disabled {
		t.Fatal("Resume remains actionable while its receipt is pending")
	}
	if v.Model.Drafts[v.Model.DraftKey()] != "先确认重连后是否还会重复发送。" {
		t.Fatal("goal action changed the normal draft")
	}
}

func TestGoalActiveActionsSlideInOnHover(t *testing.T) {
	v, tester := goalLayout(false, false)
	s := &v.Model.Snapshot.Sessions[0]
	goal := *s.Goal
	goal.State = domain.GoalActive
	s.Goal = &goal
	detail := v.Model.Details[s.ID]
	detail.Session = *s
	v.Model.Details[s.ID] = detail
	tester.Frame()
	has := func(label string) bool { _, shown := tester.Find(label); return shown }
	elapsed, shown := tester.Find("6 分 22 秒")
	if !shown || has("清除") || has("暂停") || has("恢复") {
		t.Fatal("the running goal must show only elapsed time at rest", tester.Texts())
	}
	tester.Move(elapsed.X+elapsed.W/2, elapsed.Y+elapsed.H/2)
	tester.Advance(80 * time.Millisecond)
	middle, _ := tester.Find("6 分 22 秒")
	tester.Advance(200 * time.Millisecond)
	shifted, _ := tester.Find("6 分 22 秒")
	clear, clearShown := tester.Find("清除")
	pause, pauseShown := tester.Find("暂停")
	if !clearShown || !pauseShown || shifted.X >= middle.X || middle.X >= elapsed.X || shifted.X >= clear.X || clear.X >= pause.X {
		t.Fatalf("the running goal actions did not slide to the right of elapsed time: %v %v %v %v %v", elapsed, middle, shifted, clear, pause)
	}
	tester.Move(300, 48)
	tester.Advance(time.Second)
	if final, _ := tester.Find("6 分 22 秒"); final != elapsed || has("清除") || has("暂停") {
		t.Fatal("the running goal did not return to its resting alignment")
	}
}

func TestGoalVisibleEdgesMatchComposer(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{850, 1240} {
			t.Run(fmt.Sprintf("dark=%v/width=%d", dark, width), func(t *testing.T) {
				_, tester := goalLayout(true, false)
				tester.SetDark(dark)
				tester.SetSize(width, 820)
				tester.SetScale(2)
				tester.Advance(time.Second)
				find := func(label string) n.Rect {
					t.Helper()
					r, ok := tester.Find(label)
					if !ok {
						t.Fatalf("missing %q", label)
					}
					return r
				}
				row, summary, paused, resume := find("目标状态行"), find("目标摘要"), find("已暂停"), find("恢复")
				pixels := tester.Image()
				ink := func(box n.Rect, bg byte) image.Rectangle {
					t.Helper()
					var bounds image.Rectangle
					for y := int(2 * box.Y); y < int(2*(box.Y+box.H)); y++ {
						for x := int(2 * box.X); x < int(2*(box.X+box.W)); x++ {
							if math.Abs(float64(int(pixels.RGBAAt(x, y).R)-int(bg))) > 70 {
								bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
							}
						}
					}
					if bounds.Empty() {
						t.Fatalf("no visible pixels in %v", box)
					}
					return bounds
				}
				goalBG := pixels.RGBAAt(int(2*(row.X+6)), int(2*(row.Y+6))).R
				icon := ink(n.Rect{X: summary.X, Y: row.Y, W: 16, H: row.H}, goalBG)
				titleX := summary.X + goalIconSize
				title := ink(n.Rect{X: titleX, Y: row.Y, W: paused.X - 1 - titleX, H: row.H}, goalBG)
				state := ink(paused, goalBG)
				if math.Abs(float64(title.Min.Y+title.Max.Y-state.Min.Y-state.Max.Y)) > 2 {
					t.Errorf("title and status have different visible centers: %v %v", title, state)
				}
				action := ink(resume, goalBG)
				editor, input, send := find("消息编辑器"), find("消息输入"), find("发送")
				composerBG := pixels.RGBAAt(int(2*(editor.X+8)), int(2*(input.Y+10))).R
				draft := ink(n.Rect{X: input.X, Y: input.Y, W: 36, H: 25}, composerBG)
				// The primary button may be disabled by the approval above; use its
				// native 28-DIP circular surface as the right reference.
				if math.Abs(float64(icon.Min.X-draft.Min.X)) > 2 || math.Abs(float64(action.Max.X-int(2*(send.X+send.W)))) > 2 {
					t.Errorf("goal ink misses the composer's visible edges: goal %v..%v, composer %v..%.1f", icon.Min.X, action.Max.X, draft.Min.X, 2*(send.X+send.W))
				}
				approval, heading, count := find("待处理请求卡片"), find("允许执行此命令？"), find("1 项")
				cardBG := pixels.RGBAAt(int(2*(approval.X+8)), int(2*(heading.Y+3))).R
				shield := ink(n.Rect{X: approval.X + 10, Y: heading.Y, W: 24, H: heading.H}, cardBG)
				total := ink(count, cardBG)
				if math.Abs(float64(shield.Min.X-icon.Min.X)) > 2 || math.Abs(float64(total.Max.X-action.Max.X)) > 2 {
					t.Errorf("goal and approval ink differ at the edges: goal %v..%v, approval %v..%v", icon.Min.X, action.Max.X, shield.Min.X, total.Max.X)
				}
			})
		}
	}
}

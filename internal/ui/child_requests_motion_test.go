package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func completeChildQuestion(v *View) {
	d := v.Model.Details["s"]
	d.Requests[0].State = "answered"
	d.Requests[0].Revision++
	d.Items[1].Status = "answered"
	receipt := domain.Item{ID: "request-result:child-q", WorkID: "agent-a", TurnID: "t", Kind: "request_result", Presentation: "questions", Final: true, Order: 4, Parts: []domain.ContentPart{{Text: "同时覆盖根节点与空子树"}}}
	d.Items = append(d.Items, receipt)
	v.Model.Details["s"] = d
	key := v.Model.WorkKey("s", "agent-a")
	child := v.Model.WorkDetails[key]
	child.Items[1].Item.Status = "answered"
	child.Items = append(child.Items, domain.WorkItem{Item: receipt, Revision: 4}, domain.WorkItem{Item: domain.Item{ID: "child-continuation", Kind: "assistant", Text: "收到，我会继续检查根节点与空子树。", Final: true, Order: 5}, Revision: 5})
	v.Model.WorkDetails[key] = child
}
func TestChildReceiptCompletesBeforeItsContinuationReveals(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		v, ui := childRequestsFixture(t)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduce})
		ui.Click("查看来源：检查边界条件")
		ui.Advance(time.Second)
		ui.Click("消息输入")
		completeChildQuestion(v)
		ui.Frame()
		ui.Advance(90 * time.Millisecond)
		if !reduce && ui.HasText("收到，我会继续检查根节点与空子树。") {
			t.Fatal("child output mounted before its receipt entrance completed")
		}
		if !ui.HasText("已回答 · 同时覆盖根节点与空子树") {
			t.Fatal("receipt missing in child detail")
		}
		ui.Advance(time.Second)
		if !ui.HasText("收到，我会继续检查根节点与空子树。") {
			t.Fatal("child continuation never mounted")
		}
		if !ui.Focused("消息输入") || v.Model.Drafts["session:s"] != "keep draft" {
			t.Fatal("receipt sequence steals composer focus/draft")
		}
	}
}
func TestChildRequestSourceNavigationAndPagerKeepRealMotion(t *testing.T) {
	v, ui := childRequestsFixture(t)
	ui.Click("查看来源：检查边界条件")
	ui.Advance(70 * time.Millisecond)
	middle, ok := ui.Find("检查根节点与空子树")
	if !ok {
		t.Fatal("child detail does not enter")
	}
	ui.Advance(time.Second)
	final, _ := ui.Find("检查根节点与空子树")
	if middle.X <= final.X {
		t.Fatal("origin navigation lacks a horizontal intermediate position", middle, final)
	}
	d := v.Model.Details["s"]
	d.Requests[1].Questions[0].Text = strings.Repeat("较长的主会话问题。", 24)
	v.Model.Details["s"] = d
	ui.Frame()
	before, _ := ui.Find("请求内容区域")
	ui.Click("下一个请求")
	ui.Advance(70 * time.Millisecond)
	during, _ := ui.Find("请求内容区域")
	moving, _ := ui.Find(d.Requests[1].Questions[0].Text)
	ui.Advance(time.Second)
	after, _ := ui.Find("请求内容区域")
	settled, _ := ui.Find(d.Requests[1].Questions[0].Text)
	if !(during.H > min(before.H, after.H) && during.H < max(before.H, after.H)) || moving.X <= settled.X {
		t.Fatal("sourced pager loses synchronized position/height", before, during, after, moving, settled)
	}
	if v.Model.Drafts["session:s"] != "keep draft" {
		t.Fatal("pager loses ordinary draft")
	}
}
func TestChildRequestsNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		v, ui := childRequestsFixture(t)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "child-requests-"+name+".png")
		head, _ := ui.Find("需要回答")
		origin, _ := ui.Find("查看来源：检查边界条件")
		pager, _ := ui.Find("下一个请求")
		if math.Abs(float64(head.Y-origin.Y)) > 1 || origin.X+origin.W > pager.X {
			t.Fatal("request origin/header geometry is not aligned", head, origin, pager)
		}
		ui.Click("查看来源：检查边界条件")
		ui.Advance(time.Second)
		ui.Click("查看后台任务：运行回归测试")
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "child-requests-"+name+"-detail.png")
		d := v.Model.Details["s"]
		d.Requests[0].Kind = "approval"
		d.Requests[0].Title = "允许执行此命令？"
		d.Requests[0].Questions = nil
		d.Items[1].Text = "允许执行此命令？"
		key := v.Model.WorkKey("s", "agent-a")
		child := v.Model.WorkDetails[key]
		child.Items[1].Item.Text = "允许执行此命令？"
		v.Model.WorkDetails[key] = child
		d.Requests[0].Text = "go test -race ./..."
		d.Requests[0].Choices = []domain.RequestChoice{{ID: "accept", Label: "允许这一次"}, {ID: "decline", Label: "拒绝"}}
		d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
		d.Session.Status = "completed"
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "child-requests-"+name+"-approval.png")
		if !ui.HasText("等待授权") || !ui.HasText("允许这一次") {
			t.Fatal("parent completion hides child approval")
		}
		ui.SetSize(1050, 720)
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "child-requests-"+name+"-narrow.png")
		ui.SetSize(1440, 900)
		v.Model.Connected = false
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "child-requests-"+name+"-offline.png")
	}
}
func TestChildRequestNativeMotionCapture(t *testing.T) {
	dir := os.Getenv("SHIOJI_CHILD_MOTION")
	if dir == "" {
		t.Skip("set SHIOJI_CHILD_MOTION to capture native frames")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v, ui := childRequestsFixture(t)
	frame := 0
	capture := func(count int) {
		for i := 0; i < count; i++ {
			ui.Frame()
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			frame++
			ui.Advance(time.Second / 60)
		}
	}
	capture(30)
	ui.Click("查看来源：检查边界条件")
	capture(45)
	ui.Click("下一个请求")
	capture(30)
	ui.Click("上一个请求")
	capture(30)
	v.Model.Collapsed["s"] = true
	capture(30)
	v.Model.Collapsed["s"] = false
	capture(30)
	completeChildQuestion(v)
	capture(75)
	// Ensure captures stay in a single output location on retries.
	if _, err := os.Stat(filepath.Join(dir, "frame-0269.png")); err != nil {
		t.Fatal(err)
	}
}

func TestRequestOriginWithdrawsWithWidthWhenPagingToRoot(t *testing.T) {
	_, ui := childRequestsFixture(t)
	before, ok := ui.Find("请求来源区域")
	if !ok {
		t.Fatal("no stable origin layout slot")
	}
	ui.Click("下一个请求")
	ui.Advance(60 * time.Millisecond)
	middle, ok := ui.Find("请求来源区域")
	if !ok || middle.W <= 0 || middle.W >= before.W {
		t.Fatal("origin disappears without a width transition", before, middle)
	}
	ui.Advance(time.Second)
	after, _ := ui.Find("请求来源区域")
	if after.W != 0 {
		t.Fatal("root request retains child origin", after)
	}
}

func TestAsyncChildHistorySeedsSettledAndPreservesConcurrentArrival(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprint(concurrent), func(t *testing.T) {
			v, ui := sessionWorkFixture(t)
			key := v.Model.WorkKey("s", "agent-a")
			delete(v.Model.WorkDetails, key)
			v.Model.Sequence = 10
			controller := app.NewController("")
			v.Controller = controller
			if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			var read app.Job
			for len(controller.Jobs) > 0 {
				job := <-controller.Jobs
				if job.Method == "session.work" {
					read = job
				}
			}
			if read.Apply == nil {
				t.Fatal("no async child read")
			}
			// Observe actual live content while the historical snapshot is in flight.
			live := domain.Item{ID: "concurrent", Kind: "assistant", Text: "实际并发追加", Order: 11}
			if concurrent {
				if err := v.Model.ApplyEvent(domain.Event{Sequence: 11, SessionID: "s", Type: "work.item", WorkID: "agent-a", WorkItem: &live}); err != nil {
					t.Fatal(err)
				}
				ui.Frame()
			}
			old := domain.Item{ID: "old", Kind: "assistant", Text: "已经完成的历史正文", Final: true, Order: 2}
			receipt := domain.Item{ID: "old-receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 3, Parts: []domain.ContentPart{{Text: "历史答案"}}}
			snapshot := domain.WorkDetail{SessionID: "s", Work: v.Model.Details["s"].Works[0], Sequence: 10, Items: []domain.WorkItem{{Item: old, Revision: 2}, {Item: receipt, Revision: 3}}}
			b, _ := json.Marshal(snapshot)
			read.Apply(v.Model, b, nil)
			ui.Frame()
			state := v.workPanel("agents").detail("agent-a")
			if state.streams["old"].live() || state.streams["old-receipt"].live() {
				t.Fatal("asynchronously loaded history is replayed as live content")
			}
			if stream := state.streams["concurrent"]; concurrent && (stream == nil || !stream.live()) {
				t.Fatal("history snapshot suppresses actual concurrent arrival")
			}
			if !ui.HasText("已经完成的历史正文") || !ui.HasText("历史答案") {
				t.Fatal("settled snapshot content is missing")
			}
		})
	}
}

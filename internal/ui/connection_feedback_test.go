package ui

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestConnectionFeedbackUsesCurrentPhase(t *testing.T) {
	t.Run("first connection", func(t *testing.T) {
		v, _ := sessionLoadingFixture()
		v.Model.Connected, v.Model.ConnectionEpoch = false, ""
		ui := n.NewTester(New(v.Model, v.Controller, "").View, 1100, 680)
		ui.Advance(time.Second)
		if dir := os.Getenv("SHIOJI_CONNECTION_CAPTURE"); dir != "" {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
			saveNativeSkillImage(t, ui, "startup.png")
		}
		if !ui.HasText("正在连接") || ui.HasText("设备连接已断开") || ui.HasText("弹出消息") {
			t.Fatalf("initial connection shown as a failure: %v", ui.Texts())
		}
	})
	t.Run("backup path", func(t *testing.T) {
		m := layoutModel()
		v := New(m, app.NewController(""), "")
		ui := n.NewTester(v.View, 1100, 680)
		ui.Advance(time.Second)
		m.Connected = false
		m.DevicePaths = []app.DevicePathStatus{{ID: "ssh", Phase: app.PathUnavailable}, {ID: "tailnet", Phase: app.PathSwitching}}
		ui.Frame()
		ui.Advance(6 * time.Second)
		if dir := os.Getenv("SHIOJI_CONNECTION_CAPTURE"); dir != "" {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
			saveNativeSkillImage(t, ui, "switching.png")
		}
		if !ui.HasText("正在切换线路") || ui.HasText("无法连接执行设备") || ui.HasText("弹出消息") {
			t.Fatalf("path switch presented as an outage: %v", ui.Texts())
		}
		m.Connected = true
		m.DevicePaths = []app.DevicePathStatus{{ID: "tailnet", Phase: app.PathActive}}
		ui.Frame()
		ui.Advance(time.Second)
		if ui.HasText("已重新连接") || ui.HasText("正在切换线路") || len(v.feedbackState().messages) != 0 {
			t.Fatal("successful handoff created a notification")
		}
	})
	t.Run("agent detection", func(t *testing.T) {
		m := layoutModel()
		m.WorkerMinor, m.ConnectionEpoch = 49, "epoch"
		m.Readiness = domain.Readiness{State: domain.RuntimeRefreshing}
		ui := n.NewTester(New(m, app.NewController(""), "").View, 1100, 680)
		ui.Advance(time.Second)
		if dir := os.Getenv("SHIOJI_CONNECTION_CAPTURE"); dir != "" {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
			saveNativeSkillImage(t, ui, "agent-detect.png")
		}
		if ui.HasText("当前 Agent 尚未就绪") || ui.HasText("设备详情") {
			t.Fatal("Agent detection asked for configuration")
		}
	})
}

func TestStopFeedbackDoesNotClaimTheRequestFailed(t *testing.T) {
	m := layoutModel()
	m.Readiness.Capabilities.Interrupt = true
	d := m.Details["s"]
	d.Session.Status = domain.ExecutionRunning
	d.Turns = []domain.Turn{{ID: "turn", Status: domain.ExecutionRunning}}
	m.Details["s"], m.Snapshot.Sessions[0] = d, d.Session
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1100, 680)
	v.interrupt()
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "turn.interrupt" {
			job.Apply(m, json.RawMessage(nil), errors.New("EOF"))
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("停止结果待确认") || ui.HasText("弹出消息") {
				t.Fatalf("ambiguous stop result is missing from the conversation: %v", ui.Texts())
			}
			return
		}
	}
	t.Fatal("stop command was not sent")
}

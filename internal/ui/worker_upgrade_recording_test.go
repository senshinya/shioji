package ui

import (
	"fmt"
	"os"
	"shioji/internal/app"
	"testing"
	"time"
)

// Native production View rendered by MyGo with deterministic device responses.
// This is UI evidence; signed process migration is verified separately.
func TestWorkerUpgradeNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_UPGRADE_RECORDING")
	if dir == "" {
		t.Skip("opt-in native worker upgrade evidence")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v, ui, c := upgradeUIFixture(t)
	ui.SetDark(os.Getenv("SHIOJI_UPGRADE_DARK") != "")
	ui.Advance(time.Second)
	state := v.Model.DeviceStates["air"].WorkerService
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(label, err)
		}
	}
	for frame := 0; frame < 270; frame++ {
		switch frame {
		case 15:
			saveNativeSkillImage(t, ui, "waiting.png")
		case 30:
			click("立即更新执行服务")
		case 55:
			saveNativeSkillImage(t, ui, "confirmation.png")
		case 70:
			click("取消")
		case 90:
			click("立即更新执行服务")
		case 115:
			click("确认立即更新")
			job := <-c.Jobs
			if job.Method != "$worker.update.immediate" {
				t.Fatal(job)
			}
			state.Phase, state.OperationPhase = app.WorkerUpdating, "switching"
			state.Message = "正在切换执行服务"
			ui.Frame()
		case 155:
			state.Phase, state.OperationPhase = app.WorkerUpdateFailed, "blocked"
			state.Message = "执行服务连续异常退出，已停止自动重启；可重试兼容安装"
			ui.Frame()
		case 180:
			saveNativeSkillImage(t, ui, "retry.png")
		case 200:
			state.Phase, state.OperationPhase = app.WorkerWaiting, "waiting"
			state.Message = "等待实际任务完成，空闲工具会自动回收"
			ui.Frame()
		case 230:
			click("取消执行服务更新")
			job := <-c.Jobs
			if job.Method != "$worker.update.cancel" {
				t.Fatal(job)
			}
			state.Phase, state.OperationPhase = app.WorkerAvailable, "failed"
			state.Message = "更新已取消，原服务保持运行"
			ui.Frame()
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
}

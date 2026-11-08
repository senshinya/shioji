package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestLocalWorkerUpdateActionKeepsDeviceScopeAndAnimatesStatus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := deviceUIModel()
		state := m.DeviceStates["local"]
		state.WorkerService = &app.WorkerService{Build: &domain.WorkerBuild{BuildID: "old-build", SafeUpdate: true}, Available: &domain.WorkerBuild{BuildID: "new-build"}, Phase: app.WorkerAvailable}
		controller := app.NewController("")
		v := New(m, controller, "")
		v.devices = true
		v.deviceSelected = "local"
		ui := n.NewTester(v.View, 1240, 900)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		if !ui.HasText("old-buil") || !ui.HasText("有更新") {
			t.Fatal("actual worker version unavailable", ui.Texts())
		}
		if err := ui.Click("更新执行服务"); err != nil {
			t.Fatal(err)
		}
		found := false
		for len(controller.Jobs) > 0 {
			job := <-controller.Jobs
			if job.Method == "$worker.update" {
				found = true
				if job.ConnectionID != "local" || job.ConnectionEpoch != "a" {
					t.Fatal("update lost device identity", job)
				}
			}
		}
		if !found {
			t.Fatal("update command was not scheduled")
		}
		state.WorkerService = &app.WorkerService{Build: state.WorkerService.Build, Available: state.WorkerService.Available, Phase: app.WorkerUpdating, Message: "正在更新并重新连接，会话和草稿已保留"}
		ui.Frame()
		ui.Advance(250 * time.Millisecond)
		if !ui.HasText("正在更新") || !ui.HasText("正在更新并重新连接，会话和草稿已保留") {
			t.Fatal("update progress missing", ui.Texts())
		}
	}
}

func TestWorkerVersionDetailsAnimateTheirActualHeight(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	build := &domain.WorkerBuild{BuildID: "worker-build", SafeUpdate: true}
	state.WorkerService = &app.WorkerService{Build: build, Phase: app.WorkerCurrent}
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("执行服务版本详情")
	state.WorkerService = &app.WorkerService{Build: build, Available: &domain.WorkerBuild{BuildID: "new-worker"}, Phase: app.WorkerWaiting, Message: "有任务正在运行，空闲后更新"}
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	moving, _ := ui.Find("执行服务版本详情")
	ui.Advance(500 * time.Millisecond)
	open, _ := ui.Find("执行服务版本详情")
	if moving.H <= before.H+1 || moving.H >= open.H-1 {
		t.Fatal("version/message/action area jumped height", before, moving, open)
	}
	state.WorkerService = &app.WorkerService{Build: build, Phase: app.WorkerCurrent}
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	closing, _ := ui.Find("执行服务版本详情")
	if closing.H <= before.H+1 || closing.H >= open.H-1 {
		t.Fatal("update details disappeared before height transition", before, closing, open)
	}
}

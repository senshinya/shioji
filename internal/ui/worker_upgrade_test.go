package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func upgradeUIFixture(t *testing.T) (*View, *n.Tester, *app.Controller) {
	t.Helper()
	m := deviceUIModel()
	m.DeviceStates["air"].WorkerService = &app.WorkerService{Build: &domain.WorkerBuild{BuildID: "old", Lifecycle: 1, Maintenance: true, ForceRecovery: true}, Available: &domain.WorkerBuild{BuildID: "next", Lifecycle: 1}, Phase: app.WorkerWaiting, OperationID: "upgrade", OperationPhase: "waiting", Message: "等待实际任务完成"}
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.deviceSelected = "air"
	u := n.NewTester(v.View, 1240, 820)
	for len(c.Jobs) > 0 {
		<-c.Jobs
	}
	return v, u, c
}
func TestNativeUpgradeWaitingCanCancelWithoutEnvironmentPage(t *testing.T) {
	_, u, c := upgradeUIFixture(t)
	if err := u.Click("取消执行服务更新"); err != nil {
		t.Fatal("waiting upgrade has no direct cancellation", err)
	}
	select {
	case job := <-c.Jobs:
		if job.Method != "$worker.update.cancel" || job.ConnectionID != "air" {
			t.Fatal(job)
		}
	default:
		t.Fatal("cancel did not target owning device")
	}
	if u.HasText("选择运行环境") || u.HasText("查看阻塞") {
		t.Fatal("normal update opened old environment workflow")
	}
}
func TestNativeImmediateUpgradeRequiresOnlyOneImpactConfirmation(t *testing.T) {
	_, u, c := upgradeUIFixture(t)
	if err := u.Click("立即更新执行服务"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if !u.HasText("立即更新执行服务？") {
		t.Fatal("missing concise impact confirmation")
	}
	select {
	case job := <-c.Jobs:
		t.Fatal("update started before confirmation", job)
	default:
	}
	if err := u.Click("确认立即更新"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-c.Jobs:
		if job.Method != "$worker.update.immediate" || job.ConnectionID != "air" {
			t.Fatal(job)
		}
	default:
		t.Fatal("confirmed update did not start")
	}
}

package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func nextMaintenanceUIJob(t *testing.T, c *app.Controller) app.Job {
	t.Helper()
	for {
		select {
		case job := <-c.Jobs:
			if strings.Contains(job.Method, "maintenance") {
				return job
			}
		default:
			t.Fatal("maintenance job missing")
			return app.Job{}
		}
	}
}
func maintenanceUIFixture(t *testing.T) (*View, *n.Tester, *app.Controller) {
	t.Helper()
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerService = &app.WorkerService{Build: &domain.WorkerBuild{BuildID: "old", Maintenance: true}, Available: &domain.WorkerBuild{BuildID: "new"}, Phase: app.WorkerWaiting, Message: "后台环境仍在运行"}
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.deviceSelected = "air"
	u := n.NewTester(v.View, 1240, 820)
	if err := u.Click("查看更新阻塞"); err != nil {
		t.Fatal(err)
	}
	job := nextMaintenanceUIJob(t, c)
	targets := []domain.WorkerUpdateBlocker{}
	for i, title := range []string{"排查 worker 更新等待", "验证局域网网页预览"} {
		sid := fmt.Sprintf("s%d", i)
		target := domain.WorkerMaintenanceTarget{SessionID: sid, Generation: "g", ProcessRef: "p", Revision: 1}
		targets = append(targets, domain.WorkerUpdateBlocker{ID: sid, SessionID: sid, WorkspaceName: "Shioji", SessionName: title, Kind: domain.WorkerBlockerEnvironment, Status: "completed", Reason: "后台环境仍在运行，后台活动无法确认", Target: &target, Processes: []domain.WorkerProcessSummary{{PID: 18324 + i, Name: "node"}}})
	}
	b, _ := json.Marshal(domain.WorkerUpdateDiagnosis{DeviceID: "B", BuildID: "old", Blockers: targets})
	job.Apply(m, b, nil)
	u.Advance(time.Second)
	return v, u, c
}
func TestNativeMaintenanceRequiresSelectionAndSeparateForceConfirmation(t *testing.T) {
	v, u, c := maintenanceUIFixture(t)
	if _, ok := u.Find("设备列表"); !ok {
		t.Fatal("device collection removed")
	}
	if err := u.Click("结束所选环境并更新"); err != nil {
		t.Fatal(err)
	}
	if v.maintenanceUIFor("air").confirm != "" {
		t.Fatal("empty selection opened confirmation")
	}
	if err := u.Click("选择 排查 worker 更新等待"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("结束所选环境并更新"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if !u.HasText("结束 1 个会话的后台环境并更新？") || !u.HasText("尚未写入的内存状态可能丢失") {
		t.Fatal("missing scope or consequence")
	}
	select {
	case <-c.Jobs:
		t.Fatal("opening confirmation sent a stop")
	default:
	}
	if err := u.Click("确认正常结束"); err != nil {
		t.Fatal(err)
	}
	job := nextMaintenanceUIJob(t, c)
	in := job.Input.(domain.WorkerMaintenanceInput)
	if in.Action != "stop" || len(in.Targets) != 1 || job.ConnectionID != "air" {
		t.Fatal(in, job.ConnectionID)
	}
	op := domain.WorkerMaintenance{ID: in.ID, DeviceID: "B", ExpectedBuildID: "old", BuildID: "new", Phase: domain.WorkerMaintenanceNeedsForce, Revision: 2, Targets: []domain.WorkerMaintenanceResult{{Target: in.Targets[0], SessionName: "排查 worker 更新等待", WorkspaceName: "Shioji", State: "awaitingForce", ProcessRef: "p", Processes: []domain.WorkerProcessSummary{{PID: 18324, Name: "node"}}}}}
	b, _ := json.Marshal(op)
	job.Apply(v.Model, b, nil)
	v.maintenanceUIFor("air").next = v.frameTime.Add(time.Hour)
	u.Advance(time.Second)
	if err := u.Click("强制结束所选环境"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if !u.HasText("这些进程将被立即终止") {
		t.Fatal("force risk missing")
	}
	select {
	case <-c.Jobs:
		t.Fatal("force sent without separate confirmation")
	default:
	}
	if err := u.Click("取消强制结束"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if v.Model.Drafts["session:s"] != "" {
		t.Fatal("unexpected draft mutation")
	}
}
func TestOldWorkerDoesNotOfferMaintenance(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["air"].WorkerService = &app.WorkerService{Build: &domain.WorkerBuild{BuildID: "old"}, Phase: app.WorkerWaiting, Message: "实际返回的阻塞原因"}
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "air"
	u := n.NewTester(v.View, 1240, 820)
	if _, ok := u.Find("查看更新阻塞"); ok {
		t.Fatal("old worker offered unsupported action")
	}
	if !u.HasText("实际返回的阻塞原因") {
		t.Fatal("lost real wait reason")
	}
}
func TestWorkerMaintenanceNativeEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_MAINTENANCE_EVIDENCE")
	if dir == "" {
		t.Skip("native renderer evidence")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v, u, c := maintenanceUIFixture(t)
	_ = c
	u.SetDark(false)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "blockers.png")
	if err := u.Click("选择 排查 worker 更新等待"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("结束所选环境并更新"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "confirm.png")
	u.SetDark(true)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "dark-confirm.png")
	u.Key(0, n.KeyEscape)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "dark-list.png")
	u.SetSize(980, 820)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "narrow.png")
	u.SetSize(1240, 820)
	u.SetDark(false)
	var op domain.WorkerMaintenance
	click := func(label string) {
		t.Helper()
		if err := u.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	for frame := 0; frame < 240; frame++ {
		switch frame {
		case 20:
			click("结束所选环境并更新")
		case 60:
			click("确认正常结束")
			job := nextMaintenanceUIJob(t, c)
			in := job.Input.(domain.WorkerMaintenanceInput)
			op = domain.WorkerMaintenance{ID: in.ID, DeviceID: "B", ExpectedBuildID: "old", BuildID: "new", Revision: 1, Phase: domain.WorkerMaintenanceStopping, Targets: []domain.WorkerMaintenanceResult{{Target: in.Targets[0], SessionName: "排查 worker 更新等待", WorkspaceName: "Shioji", State: "stopping", ProcessRef: "p", Processes: []domain.WorkerProcessSummary{{PID: 18324, Name: "node"}}}}}
			data, _ := json.Marshal(op)
			job.Apply(v.Model, data, nil)
			v.maintenanceUIFor("air").next = v.frameTime.Add(time.Hour)
		case 90:
			op.Phase = domain.WorkerMaintenanceNeedsForce
			op.Revision++
			op.Targets[0].State = "awaitingForce"
			v.Model.MaintenanceClient("B").Operation = &op
		case 120:
			click("强制结束所选环境")
		case 140:
			saveNativeSkillImage(t, u, "force.png")
		case 165:
			click("取消强制结束")
		case 195:
			click("取消本次处理")
			job := nextMaintenanceUIJob(t, c)
			op.Phase = domain.WorkerMaintenanceCancelled
			op.Revision++
			data, _ := json.Marshal(op)
			job.Apply(v.Model, data, nil)
		case 220:
			saveNativeSkillImage(t, u, "cancelled.png")
		}
		u.Advance(time.Second / 24)
		saveNativeSkillImage(t, u, fmt.Sprintf("frame-%04d.png", frame))
	}
	op.Phase = domain.WorkerMaintenanceWaiting
	op.Targets[0].State = "stopped"
	op.Targets[0].Processes = nil
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "waiting.png")
	op.Phase = domain.WorkerMaintenanceUpdating
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "progress.png")
	op.Phase = domain.WorkerMaintenanceCompleted
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "completed.png")
	v.Model.MaintenanceClient("B").Operation = nil
	v.Model.MaintenanceClient("B").Diagnosis.Blockers = []domain.WorkerUpdateBlocker{{ID: "unknown", Kind: domain.WorkerBlockerUnknown, Reason: "后台进程的归属记录不完整，无法安全结束"}}
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "unknown.png")

	if v.maintenanceUIFor("air").confirm != "" {
		t.Fatal("Escape did not cancel confirmation")
	}
}

func TestUnknownMaintenanceAlwaysOffersCurrentDiagnosticExport(t *testing.T) {
	v, u, _ := maintenanceUIFixture(t)
	client := v.Model.MaintenanceClient("B")
	client.Diagnosis.Blockers = append(client.Diagnosis.Blockers, domain.WorkerUpdateBlocker{ID: "unknown", Kind: domain.WorkerBlockerUnknown, Reason: "进程归属记录不完整"})
	u.Frame()
	if _, ok := u.Find("导出诊断"); !ok {
		t.Fatal("mixed unknown blocker lost diagnostic export")
	}
	saveNativeSkillImage(t, u, "mixed-unknown.png")
	client.Operation = &domain.WorkerMaintenance{ID: "active", DeviceID: "B", Phase: domain.WorkerMaintenanceNeedsForce, Targets: []domain.WorkerMaintenanceResult{{Target: domain.WorkerMaintenanceTarget{SessionID: "s0"}, State: "unknown", SessionName: "后台环境的运行实例待核对", Message: "运行实例的归属记录已变化"}}}
	v.maintenanceUIFor("air").next = v.frameTime.Add(time.Hour)
	u.Frame()
	if _, ok := u.Find("导出诊断"); !ok {
		t.Fatal("active unknown result lost diagnostic export")
	}
	saveNativeSkillImage(t, u, "active-unknown.png")
	report := maintenanceDiagnostic(client)
	text := string(report)
	if !strings.Contains(text, "运行实例的归属记录已变化") || !strings.Contains(text, "后台环境的运行实例待核对") {
		t.Fatal("export uses stale preview", text)
	}
}

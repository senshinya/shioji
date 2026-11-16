package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func nextRecoveryUIJob(t *testing.T, c *app.Controller) app.Job {
	t.Helper()
	for {
		select {
		case job := <-c.Jobs:
			if job.Method == "$worker.recovery" || job.Method == "worker.update.diagnose" || job.Method == "$worker.maintenance.inspect" || job.Method == "worker.maintenance.apply" {
				return job
			}
		default:
			t.Fatal("recovery job missing")
			return app.Job{}
		}
	}
}
func forceRecoveryFixture(t *testing.T) (*View, *n.Tester, *app.Controller) {
	t.Helper()
	m := deviceUIModel()
	s := m.DeviceStates["air"]
	s.WorkerService = &app.WorkerService{Build: &domain.WorkerBuild{BuildID: "old", ForceRecovery: true}, Phase: app.WorkerWaiting}
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.deviceSelected = "air"
	u := n.NewTester(v.View, 1240, 820)
	v.openWorkerRecovery("air", "", false)
	job := nextRecoveryUIJob(t, c)
	target := domain.WorkerMaintenanceTarget{SessionID: "s1", Generation: "g1", ProcessRef: "exact-observation", Revision: 1}
	data, _ := json.Marshal(domain.WorkerUpdateDiagnosis{DeviceID: "B", BuildID: "old", CheckedAt: time.Now(), Environments: []domain.WorkerUpdateBlocker{{ID: "s1", SessionID: "s1", SessionName: "核查 B 站视频封面", WorkspaceName: "xagent", Status: "running", Reason: "存在无法确认归属的脱离进程", ForceTarget: &target}}})
	job.Apply(m, data, nil)
	u.Frame()
	u.Advance(time.Second)
	return v, u, c
}
func TestNativeForceRecoveryRequiresSelectionRiskAndExplicitClick(t *testing.T) {
	v, u, c := forceRecoveryFixture(t)
	if err := u.Click("选择 核查 B 站视频封面"); err != nil {
		t.Fatal(err, u.Texts())
	}
	u.Frame()
	if err := u.Click("强制关闭所选环境"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if !u.HasText("尚未写入的内存状态可能丢失") {
		t.Fatal("risk missing", u.Texts())
	}
	if err := u.Click("确认强制关闭"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	select {
	case j := <-c.Jobs:
		t.Fatal("unchecked risk sent force", j)
	default:
	}
	if err := u.Click("我已了解强制关闭的影响"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("确认强制关闭"); err != nil {
		t.Fatal(err)
	}
	job := nextRecoveryUIJob(t, c)
	in := job.Input.(domain.WorkerMaintenanceInput)
	if in.Action != "force-close" || !in.RiskAccepted || !in.Standalone || len(in.Targets) != 1 {
		t.Fatal(in)
	}
	if v.Model.MaintenanceClient("B").PendingID == "" {
		t.Fatal("receipt not retained")
	}
}
func TestNativeDisconnectedWorkerAlwaysOffersIndependentRecovery(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["air"].Connected = false
	m.DeviceStates["air"].WorkerService = nil
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.deviceSelected = "air"
	u := n.NewTester(v.View, 1240, 820)
	if err := u.Click("执行服务更多操作"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if err := u.ChooseMenuItem("强制重启执行服务…"); err != nil {
		t.Fatal(err)
	}
	job := nextRecoveryUIJob(t, c)
	if job.Method != "$worker.recovery" {
		t.Fatal("dead RPC dependency", job.Method)
	}
}

func TestNativeProcessPickerIncludesExplicitUnknownAndProtectsController(t *testing.T) {
	v, u, c := forceRecoveryFixture(t)
	if err := u.Click("选择其他进程…"); err != nil {
		t.Fatal(err)
	}
	job := nextRecoveryUIJob(t, c)
	preview := domain.WorkerRecoveryPreview{DeviceID: "B", Ref: "process-preview", Processes: []domain.WorkerRecoveryProcess{{Ref: "other", PID: 22001, Name: "custom-helper", Evidence: "unknown", StartedAt: time.Now()}, {Ref: "control", PID: 22002, Name: "shioji-runtime", Protected: true, Reason: "恢复控制连接"}}}
	data, _ := json.Marshal(preview)
	job.Apply(v.Model, data, nil)
	u.Frame()
	u.Advance(time.Second)
	if u.HasText("custom-helper · PID 22001") {
		t.Fatal("unknown process selected as candidate by default")
	}
	if err := u.Click("显示此用户的所有进程"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("选择进程 control"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if v.recoveryUIFor("air").processes["control"] {
		t.Fatal("control process selectable")
	}
	if err := u.Click("选择进程 other"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("完成选择"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("强制关闭所选环境"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	u.Advance(time.Second)
	if !u.HasText("custom-helper · PID 22001 · 来源未确认") {
		t.Fatal("explicit unknown not in frozen confirmation", u.Texts())
	}
	u.Key(0, n.KeyEnter)
	u.Frame()
	select {
	case j := <-c.Jobs:
		t.Fatal("Enter sent force", j)
	default:
	}
	if !v.recoveryUIFor("air").risk {
		if err := u.Click("我已了解强制关闭的影响"); err != nil {
			t.Fatal(err)
		}
	}
	u.Frame()
	if err := u.Click("确认强制关闭"); err != nil {
		t.Fatal(err)
	}
	if v.recoveryUIFor("air").error != "" {
		t.Fatal(v.recoveryUIFor("air").error, u.Texts())
	}
	t.Logf("before receipt risk=%v confirm=%s clientBusy=%v helperBusy=%v", v.recoveryUIFor("air").risk, v.recoveryUIFor("air").confirm, v.Model.MaintenanceClient("B").Busy, v.Model.RecoveryClient("B").Busy)
	input := nextRecoveryUIJob(t, c).Input.(domain.WorkerMaintenanceInput)
	if len(input.ProcessRefs) != 1 || input.ProcessRefs[0] != "other" || input.ProcessPreviewRef != "process-preview" {
		t.Fatal(input)
	}
}

func TestForceRecoveryNativeEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_FORCE_RECOVERY_EVIDENCE")
	if dir == "" {
		t.Skip("native evidence")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v, u, c := forceRecoveryFixture(t)
	u.Move(520, 320)
	u.SetDark(false)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "force-list.png")
	click := func(label string) {
		t.Helper()
		if err := u.Click(label); err != nil {
			t.Fatal(err)
		}
		u.Frame()
	}
	click("选择 核查 B 站视频封面")
	click("强制关闭所选环境")
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "force-confirm.png")
	u.SetDark(true)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "force-dark.png")
	u.SetSize(860, 760)
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "force-narrow.png")
	u.SetSize(1240, 820)
	u.SetDark(false)
	click("取消恢复确认")
	u.Advance(time.Second)
	for frame := 0; frame < 144; frame++ {
		switch frame {
		case 12:
			click("强制关闭所选环境")
		case 45:
			click("我已了解强制关闭的影响")
		case 65:
			click("确认强制关闭")
			job := nextRecoveryUIJob(t, c)
			in := job.Input.(domain.WorkerMaintenanceInput)
			op := domain.WorkerMaintenance{ID: in.ID, DeviceID: "B", Standalone: true, Explicit: true, ExpectedBuildID: "old", Phase: domain.WorkerMaintenanceNeedsForce, Revision: 2, Targets: []domain.WorkerMaintenanceResult{{Target: in.Targets[0], SessionName: "核查 B 站视频封面", WorkspaceName: "xagent", State: "awaitingForce", ProcessRef: "latest", Message: "部分进程尚未退出，可重新确认剩余对象"}}}
			data, _ := json.Marshal(op)
			job.Apply(v.Model, data, nil)
			v.recoveryUIFor("air").next = v.frameTime.Add(time.Hour)
		case 100:
			saveNativeSkillImage(t, u, "force-partial.png")
		}
		u.Advance(time.Second / 24)
		saveNativeSkillImage(t, u, fmt.Sprintf("force-frame-%04d.png", frame))
	}
	// Separate helper preview through its actual application callback.
	v.Model.MaintenanceClient("B").Operation = nil
	click("强制重启执行服务入口")
	job := nextRecoveryUIJob(t, c)
	preview := domain.WorkerRecoveryPreview{DeviceID: "B", Ref: "device-preview", CandidateBuildID: "current-app-build", RestoreBuildID: "original-worker-build", Sessions: []domain.Session{{ID: "s1", Title: "核查 B 站视频封面", Status: "running"}}, Warnings: []string{"旧执行服务未响应，使用独立恢复程序"}}
	data, _ := json.Marshal(preview)
	job.Apply(v.Model, data, nil)
	u.Frame()
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "force-device.png")
	click("取消恢复确认")
	click("选择其他进程…")
	job = nextRecoveryUIJob(t, c)
	preview.Processes = []domain.WorkerRecoveryProcess{{Ref: "unknown", PID: 18324, Name: "node", Evidence: "unknown", StartedAt: time.Now()}, {Ref: "controller", PID: 18200, Name: "sshd", Protected: true, Reason: "恢复控制连接", StartedAt: time.Now()}}
	data, _ = json.Marshal(preview)
	job.Apply(v.Model, data, nil)
	u.Frame()
	u.Advance(time.Second)
	click("显示此用户的所有进程")
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "force-picker.png")
}

func TestNativeNormalCloseUsesExplicitObservation(t *testing.T) {
	v, u, c := forceRecoveryFixture(t)
	mc := v.Model.MaintenanceClient("B")
	b := &mc.Diagnosis.Environments[0]
	normal := *b.ForceTarget
	normal.ProcessRef = "ordinary-ref"
	b.Target = &normal
	u.Frame()
	if err := u.Click("选择 核查 B 站视频封面"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("正常结束所选环境"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if err := u.Click("确认正常结束"); err != nil {
		t.Fatal(err)
	}
	if f := v.recoveryUIFor("air"); f.error != "" {
		t.Fatal(f.error)
	}
	in := nextRecoveryUIJob(t, c).Input.(domain.WorkerMaintenanceInput)
	if in.Action != "close" || in.Targets[0].ProcessRef != "exact-observation" {
		t.Fatal(in)
	}
}

func TestUnknownSessionCanEndOldRecordWithoutSendingQueuedInput(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 43
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", CanEnd: true}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	m.Drafts["session:s"] = "保留普通草稿"
	c := app.NewController("")
	v := New(m, c, "")
	u := n.NewTester(v.View, 1240, 820)
	u.Advance(time.Second)
	if dir := os.Getenv("SHIOJI_FORCE_RECOVERY_EVIDENCE"); dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	}
	saveNativeSkillImage(t, u, "recovery-record.png")
	for len(c.Jobs) > 0 {
		<-c.Jobs
	}
	if err := u.Click("结束旧轮记录…"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	u.Advance(time.Second)
	saveNativeSkillImage(t, u, "recovery-record-confirm.png")
	select {
	case j := <-c.Jobs:
		t.Fatal("end sent before confirmation", j)
	default:
	}
	if err := u.Click("确认任务已停止"); err != nil {
		t.Fatal(err)
	}
	select {
	case j := <-c.Jobs:
		in, ok := j.Input.(protocol.RecoveryInput)
		if j.Method != "session.reconcile" || !ok || in.Action != "end" || in.SessionID != "s" {
			t.Fatal(j)
		}
	default:
		t.Fatal("explicit end missing")
	}
	if m.Drafts["session:s"] != "保留普通草稿" {
		t.Fatal("draft changed")
	}
	select {
	case j := <-c.Jobs:
		t.Fatal("unexpected automatic input replay", j)
	default:
	}
}

func TestDeviceRecoveryReplacementRequiresSeparateChoice(t *testing.T) {
	v, u, c := forceRecoveryFixture(t)
	if err := u.Click("强制重启执行服务入口"); err != nil {
		t.Fatal(err)
	}
	job := nextRecoveryUIJob(t, c)
	preview := domain.WorkerRecoveryPreview{DeviceID: "B", Ref: "replacement-preview", RestoreBuildID: "old-build", CandidateBuildID: "new-build", ScopeUnknown: true}
	data, _ := json.Marshal(preview)
	job.Apply(v.Model, data, nil)
	u.Frame()
	u.Advance(time.Second)
	if !u.HasText("任务数量和范围无法核对，确认将关闭此设备全部执行环境") {
		t.Fatal("unknown impact shown as zero tasks", u.Texts())
	}
	if err := u.Click("使用当前应用构建恢复执行服务"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("我已了解强制关闭的影响"); err != nil {
		t.Fatal(err)
	}
	u.Frame()
	if err := u.Click("确认强制重启"); err != nil {
		t.Fatal(err)
	}
	in := nextRecoveryUIJob(t, c).Input.(app.WorkerRecoveryAction).Input
	if !in.Update || in.BuildID != "new-build" {
		t.Fatal("explicit replacement intent lost", in)
	}
}

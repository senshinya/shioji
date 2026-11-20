package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func popoverWorkerFixture(t *testing.T, phase app.WorkerUpdatePhase) (*View, *n.Tester, *app.Controller) {
	t.Helper()
	m := deviceUIModel()
	m.SessionID, m.WorkspaceID, m.ActiveConnection = "", "wb", "air"
	m.DeviceStates["air"].WorkerService = &app.WorkerService{Build: &domain.WorkerBuild{BuildID: "old", SafeUpdate: true, Lifecycle: 1}, Available: &domain.WorkerBuild{BuildID: "next", Lifecycle: 1}, Phase: phase}
	m.RefreshDevices()
	c := app.NewController("")
	v := New(m, c, "")
	u := n.NewTester(v.View, 1000, 700)
	u.Advance(time.Second)
	if err := u.Click("当前设备"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	for len(c.Jobs) > 0 {
		<-c.Jobs
	}
	return v, u, c
}

func TestDevicePopoverWorkerUpdateSharesActionRhythmAndDisappearsWhenCurrent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, u, _ := popoverWorkerFixture(t, app.WorkerAvailable)
		u.SetDark(dark)
		u.Frame()
		update, ok := u.Find("浮层执行服务更新")
		if !ok {
			t.Fatal("missing integrated update action", u.Texts())
		}
		details, _ := u.Find("设备详情")
		word, _ := u.Find("更新执行服务")
		if math.Abs(float64(word.X-(details.X+6))) > 1 {
			t.Fatal("update content is not aligned with other action icons", word, details)
		}
		power, _ := u.Find("保持唤醒开关")
		panel, _ := u.Find("当前设备状态")
		if update.X != details.X || update.W != details.W || update.H != details.H || update.H != 26 {
			t.Fatal("update does not use the existing action geometry", update, details)
		}
		center := func(r n.Rect) float32 { return r.Y + r.H/2 }
		if math.Abs(float64((center(update)-center(power))-(center(details)-center(update)))) > 1 {
			t.Fatal("update introduced a different row gap", power, update, details)
		}
		for _, phase := range []app.WorkerUpdatePhase{app.WorkerCurrent, app.WorkerNewer} {
			v.Model.DeviceStates["air"].WorkerService.Phase = phase
			u.Frame()
			u.Advance(time.Second)
			if _, ok := u.Find("浮层执行服务更新"); ok {
				t.Fatal("current/newer worker still has update action", phase)
			}
			if u.HasText("已是最新") || u.HasText("已就绪") {
				t.Fatal("current worker left a status placeholder", u.Texts())
			}
			now, _ := u.Find("当前设备状态")
			if math.Abs(float64(panel.H-now.H-28)) > 1 {
				t.Fatal("hidden update kept its space", panel, now)
			}
		}
	}
}

func TestDevicePopoverWorkerUpdateTargetsOwningDeviceAndPreservesDraft(t *testing.T) {
	v, u, c := popoverWorkerFixture(t, app.WorkerAvailable)
	v.Model.Drafts["home"] = "preserve draft"
	if err := u.Click("浮层执行服务更新"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-c.Jobs:
		if job.Method != "$worker.update" || job.ConnectionID != "air" || job.ConnectionEpoch != "b" {
			t.Fatal("wrong update destination", job)
		}
	default:
		t.Fatal("no update dispatched")
	}
	if v.devices || v.Model.Drafts["home"] != "preserve draft" || v.Model.WorkspaceID != "wb" {
		t.Fatal("update navigated or changed draft")
	}
}

func TestDisconnectedDevicePopoverRetriesThroughServiceControl(t *testing.T) {
	v, u, c := popoverWorkerFixture(t, app.WorkerUpdateFailed)
	state := v.Model.DeviceStates["air"]
	state.Connected, state.ConnectionEpoch = false, ""
	state.WorkerService.OperationID = "blocked-update"
	u.Frame()
	u.Advance(time.Second)
	for len(c.Jobs) > 0 {
		<-c.Jobs
	}
	if err := u.Click("浮层执行服务更新"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-c.Jobs:
		if job.Method != "$worker.repair" || job.ConnectionID != "air" || job.Input != "blocked-update" {
			t.Fatal("offline recovery lost its device or blocked receipt", job)
		}
	default:
		t.Fatal("offline recovery did not dispatch")
	}
}

func TestDevicePopoverWaitingUpdateCanCancelAndConfirmWithoutLeavingConversation(t *testing.T) {
	v, u, c := popoverWorkerFixture(t, app.WorkerWaiting)
	service := v.Model.DeviceStates["air"].WorkerService
	service.OperationID, service.OperationPhase = "upgrade", "waiting"
	u.Frame()
	if err := u.Click("浮层执行服务更新"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if !u.HasText("正在等待 MacBook Air 空闲") {
		t.Fatal("waiting update did not open device-scoped management", u.Texts())
	}
	if len(c.Jobs) != 0 {
		t.Fatal("opening management dispatched update")
	}
	if err := u.Click("取消执行服务更新"); err != nil {
		t.Fatal(err)
	}
	job := <-c.Jobs
	if job.Method != "$worker.update.cancel" || job.ConnectionID != "air" {
		t.Fatal(job)
	}
	u.Frame()
	u.Advance(time.Second)
	if err := u.Click("当前设备"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if err := u.Click("浮层执行服务更新"); err != nil {
		t.Fatal(err)
	}
	u.Advance(time.Second)
	if err := u.Click("确认立即更新"); err != nil {
		t.Fatal(err)
	}
	job = <-c.Jobs
	if job.Method != "$worker.update.immediate" || job.ConnectionID != "air" {
		t.Fatal(job)
	}
	if v.devices {
		t.Fatal("waiting update left conversation")
	}
}

func TestDevicePopoverBusyUpdateDoesNotDispatchAgain(t *testing.T) {
	v, u, c := popoverWorkerFixture(t, app.WorkerUpdating)
	s := v.Model.DeviceStates["air"].WorkerService
	s.OperationID = "upgrade"
	for _, phase := range []string{"preparing", "switching", "applied", ""} {
		s.OperationPhase = phase
		u.Frame()
		u.Advance(time.Second)
		if _, ok := u.Find("浮层执行服务更新"); !ok {
			t.Fatal("missing busy update", phase)
		}
		u.Click("浮层执行服务更新")
		if len(c.Jobs) != 0 {
			t.Fatal("busy status resubmitted update", phase)
		}
	}
}

func TestDevicePopoverUpdateDialogDropsStaleOperationAndConnection(t *testing.T) {
	for _, change := range []string{"operation", "connection", "build", "device", "completed"} {
		t.Run(change, func(t *testing.T) {
			v, u, c := popoverWorkerFixture(t, app.WorkerWaiting)
			state := v.Model.DeviceStates["air"]
			state.WorkerService.OperationID = "original"
			u.Frame()
			u.Click("浮层执行服务更新")
			u.Advance(time.Second)
			switch change {
			case "operation":
				state.WorkerService.OperationID = "replacement"
			case "connection":
				state.ConnectionEpoch = "replacement"
			case "build":
				state.WorkerService.Available = &domain.WorkerBuild{BuildID: "other"}
			case "device":
				state.Snapshot.DeviceID = "other"
			case "completed":
				state.WorkerService.Phase = app.WorkerCurrent
			}
			u.Frame()
			u.Advance(time.Second)
			if _, ok := u.Find("确认立即更新"); ok {
				t.Fatal("stale update dialog still open", change)
			}
			if len(c.Jobs) != 0 {
				for len(c.Jobs) > 0 {
					job := <-c.Jobs
					if strings.HasPrefix(job.Method, "$worker.update") {
						t.Fatal("state change dispatched an update", change, job)
					}
				}
			}
		})
	}
}

func TestDevicePopoverWorkerUpdateVisibilityAtDisconnectedAndLegacyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		phase     app.WorkerUpdatePhase
		connected bool
		operation string
		show      bool
	}{
		{"offline-unstarted", app.WorkerAvailable, false, "", false},
		{"offline-query", app.WorkerUpdating, false, "original", true},
		{"offline-failure", app.WorkerUpdateFailed, false, "original", true},
		{"legacy-update", app.WorkerLegacy, true, "", true},
		{"current", app.WorkerCurrent, true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, u, c := popoverWorkerFixture(t, tc.phase)
			state := v.Model.DeviceStates["air"]
			state.Connected = tc.connected
			state.WorkerService.OperationID = tc.operation
			u.Frame()
			u.Advance(time.Second)
			_, shown := u.Find("浮层执行服务更新")
			if shown != tc.show {
				t.Fatal("wrong update visibility", tc, shown)
			}
			if tc.phase == app.WorkerLegacy {
				u.Click("浮层执行服务更新")
				u.Frame()
				if !v.devices || v.deviceSelected != "air" || len(c.Jobs) != 0 {
					t.Fatal("legacy update bypassed device migration path")
				}
			}
		})
	}
}

func TestSingleWorkerDevicePopoverReadsLocalUpdateState(t *testing.T) {
	m := deviceHealthModel()
	m.WorkerService = &app.WorkerService{Phase: app.WorkerAvailable, Build: &domain.WorkerBuild{BuildID: "old", SafeUpdate: true}, Available: &domain.WorkerBuild{BuildID: "next"}}
	c := app.NewController("")
	v := New(m, c, "")
	u := n.NewTester(v.View, 1000, 700)
	u.Click("当前设备")
	u.Advance(time.Second)
	if _, ok := u.Find("浮层执行服务更新"); !ok {
		t.Fatal("single-worker local state lost update action")
	}
	for len(c.Jobs) > 0 {
		<-c.Jobs
	}
	u.Click("浮层执行服务更新")
	select {
	case job := <-c.Jobs:
		if job.ConnectionID != "local" || job.ConnectionEpoch != m.ConnectionEpoch {
			t.Fatal(job)
		}
	default:
		t.Fatal("missing local update")
	}
}

// Render the production native View; update receipts and telemetry are fixtures.
// No worker update, network registration, or process termination is performed.
func TestDevicePopoverWorkerUpdateNativeCapture(t *testing.T) {
	dir := os.Getenv("SHIOJI_DEVICE_UPDATE_CAPTURE")
	if dir == "" {
		t.Skip("opt-in native device popover evidence")
	}
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1240, 680} {
			name := fmt.Sprintf("light-%d", int(width))
			if dark {
				name = fmt.Sprintf("dark-%d", int(width))
			}
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
			v, _, controller := popoverWorkerFixture(t, app.WorkerAvailable)
			session := domain.Session{ID: "update-session", WorkspaceID: "wb", Title: "检查设备重连", Status: "completed"}
			v.Model.DeviceStates["air"].Snapshot.Sessions = []domain.Session{session}
			v.Model.SessionID = session.ID
			v.Model.RefreshDevices()
			v.Model.Details[session.ID] = domain.SessionDetail{Session: session, Items: []domain.Item{{ID: "user", Kind: "user", Text: "检查设备重连后的状态同步，保留会话与草稿。", Final: true}, {ID: "reply", Kind: "assistant", Text: "连接恢复后会核对执行服务和会话状态。更新不会更改设备身份，未发送的草稿也会保留。", Final: true}}}
			v.Model.Drafts["session:"+session.ID] = "保留这份未发送的草稿。"
			u := n.NewTester(v.View, width, 820)
			u.SetDark(dark)
			u.Advance(time.Second)
			u.Click("当前设备")
			u.Advance(time.Second)
			state := v.Model.DeviceStates["air"]
			cpu := 24.
			state.WorkerMinor = 38
			state.Health = &domain.DeviceHealth{ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Available: true}, Samples: []domain.DeviceLoad{{At: time.Now().Add(-2 * time.Second), CPU: &cpu, Memory: &domain.DeviceMemory{UsedBytes: 42, TotalBytes: 100}}, {At: time.Now(), CPU: &cpu, Memory: &domain.DeviceMemory{UsedBytes: 42, TotalBytes: 100}}}}
			for _, phase := range []app.WorkerUpdatePhase{app.WorkerAvailable, app.WorkerUpdating, app.WorkerWaiting, app.WorkerUpdateFailed, app.WorkerCurrent} {
				state.WorkerService.Phase = phase
				state.WorkerService.OperationID = "capture-update"
				state.WorkerService.OperationPhase = "preparing"
				state.WorkerService.Message = ""
				if phase == app.WorkerUpdateFailed {
					state.WorkerService.Message = "候选签名校验失败，原服务保持运行"
				}
				u.Frame()
				u.Advance(300 * time.Millisecond)
				saveNativeSkillImage(t, u, string(phase)+".png")
			}
			state.WorkerService.Phase = app.WorkerWaiting
			state.WorkerService.OperationPhase = "waiting"
			u.Frame()
			u.Click("浮层执行服务更新")
			u.Advance(time.Second)
			saveNativeSkillImage(t, u, "confirmation.png")
			for len(controller.Jobs) > 0 {
				job := <-controller.Jobs
				if strings.HasPrefix(job.Method, "$worker.update") {
					t.Fatal("capture dispatched a worker update", job)
				}
			}
			if !dark && width == 1240 {
				u.Click("继续等待")
				u.Advance(time.Second)
				u.Click("当前设备")
				u.Advance(time.Second)
				state.WorkerService.Phase = app.WorkerAvailable
				for frame := 0; frame < 150; frame++ {
					switch frame {
					case 15:
						u.Click("浮层执行服务更新")
					case 30:
						state.WorkerService.Phase = app.WorkerUpdating
						state.WorkerService.OperationPhase = "preparing"
					case 55:
						state.WorkerService.Phase = app.WorkerWaiting
						state.WorkerService.OperationPhase = "waiting"
					case 70:
						u.Click("浮层执行服务更新")
					case 105:
						u.Click("确认立即更新")
						state.WorkerService.Phase = app.WorkerUpdating
						state.WorkerService.OperationPhase = "switching"
						u.Advance(time.Second)
						u.Click("当前设备")
					case 125:
						state.WorkerService.Phase = app.WorkerCurrent
					}
					u.Frame()
					u.Advance(time.Second / 30)
					saveNativeSkillImage(t, u, fmt.Sprintf("frame-%04d.png", frame))
				}
				// This controller is never run: queued commands are captured, not sent.
			}
		}
	}
}

func TestDevicePopoverUpdateGlyphInkStaysAlignedWithDetails(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, phase := range []app.WorkerUpdatePhase{app.WorkerAvailable, app.WorkerWaiting, app.WorkerUpdating} {
			v, u, _ := popoverWorkerFixture(t, phase)
			u.SetDark(dark)
			u.SetPreferences(n.Preferences{ReduceMotion: true})
			v.Model.DeviceStates["air"].WorkerService.OperationPhase = "preparing"
			u.Frame()
			update, _ := u.Find("浮层执行服务更新")
			details, _ := u.Find("设备详情")
			im := u.Image()
			ink := func(r n.Rect) (float64, int) {
				bg := im.RGBAAt(int(r.X+r.W-30), int(r.Y+r.H/2))
				top, bottom := im.Bounds().Max.Y, -1
				for y := int(math.Ceil(float64(r.Y))); y < int(r.Y+r.H); y++ {
					for x := int(r.X + 6); x < int(r.X+20); x++ {
						p := im.RGBAAt(x, y)
						d := abs(int(p.R)-int(bg.R)) + abs(int(p.G)-int(bg.G)) + abs(int(p.B)-int(bg.B))
						if d > 90 {
							top = min(top, y)
							bottom = max(bottom, y)
						}
					}
				}
				if bottom < 0 {
					t.Fatal("missing visible icon", phase, dark, r)
				}
				return float64(top+bottom)/2 - float64(r.Y), bottom - top + 1
			}
			a, ah := ink(update)
			b, bh := ink(details)
			if math.Abs(a-b) > 1 || abs(ah-bh) > 1 {
				t.Fatal("update/details visible icon alignment differs", phase, dark, a, b, ah, bh)
			}
		}
	}
}

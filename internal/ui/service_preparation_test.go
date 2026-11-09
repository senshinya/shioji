package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/transport"
	"strings"
	"testing"
	"time"
)

func servicePreparationFixture() *app.ServicePreparation {
	return &app.ServicePreparation{ID: "prepare-id", Name: "MacBook Air", SSH: transport.SSH{Target: "user@example.test"}, Info: transport.WorkerInfo{DeviceID: "remote", Directory: "/remote/worker", Worker: "/remote/helper", Build: &domain.WorkerBuild{BuildID: "old", SafeUpdate: true}}, Candidate: domain.WorkerBuild{BuildID: "candidate", SafeUpdate: true}, Phase: app.ServiceStaging}
}
func TestServicePreparationLostUpdateRestoresAsObservation(t *testing.T) {
	v, ui := sshSetupUI(t)
	p := servicePreparationFixture()
	p.Phase = app.ServiceDispatching
	p.RequestedAt = time.Now()
	v.Model.ServicePreparations = map[string]*app.ServicePreparation{p.Info.DeviceID: p}
	v.Model.Drafts["home"] = "preserve draft"
	ui.Frame()
	ui.Advance(1200 * time.Millisecond)
	job := welcomeJob(t, v.Controller, "$device.prepare-service")
	in := job.Input.(app.PrepareService)
	if in.Action != "observe" || in.Preparation.ID != p.ID {
		t.Fatal("restored update was submitted again", in)
	}
	job.Apply(v.Model, nil, errors.New("connection lost"))
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("重新连接") || v.currentServicePreparation().Phase != app.ServiceInterrupted {
		t.Fatal(ui.Texts())
	}
	if v.Model.Drafts["home"] != "preserve draft" {
		t.Fatal("lost ordinary draft")
	}
}
func TestServicePreparationWaitsThenRechecksCatalogBeforeAdvancing(t *testing.T) {
	v, ui := sshSetupUI(t)
	p := servicePreparationFixture()
	p.Phase = app.ServiceWaiting
	v.acceptServicePreparation(p)
	ui.Frame()
	ui.Advance(1200 * time.Millisecond)
	job := welcomeJob(t, v.Controller, "$device.prepare-service")
	in := job.Input.(app.PrepareService)
	if in.Action != "update" || in.Preparation.Phase != app.ServiceDispatching {
		t.Fatal(in)
	}
	out := app.ServicePreparationResult{Preparation: in.Preparation}
	out.Preparation.Phase = app.ServiceReconnecting
	b, _ := json.Marshal(out)
	job.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(1200 * time.Millisecond)
	job = welcomeJob(t, v.Controller, "$device.prepare-service")
	if job.Input.(app.PrepareService).Action != "observe" {
		t.Fatal(job)
	}
	out.Preparation.Phase = app.ServiceReady
	out.Check = &app.DeviceSetupCheck{Info: out.Preparation.Info, Catalog: []domain.HarnessInfo{{ID: "codex", Name: "Codex", State: "ready"}}}
	out.Check.Info.Minor = 20
	out.Check.Info.Build = &out.Preparation.Candidate
	b, _ = json.Marshal(out)
	job.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if v.welcome.step != welcomeAgents || v.currentServicePreparation() != nil || !ui.HasText("选择 Agent") {
		t.Fatal(ui.Texts())
	}
}

func TestServiceSpinnerKeepsClockAcrossStatusChangesAndAlignsInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, text := range []string{"国国国", "HHH", "国 H 国"} {
			label := text
			var clock time.Time
			ui := n.NewTester(func(c *n.Context) {
				font := n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}
				n.Column(c).Fill().Background(c.Theme().Background).Padding(20).Children(func() {
					n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
						e := serviceSpinner(c.Key("spinner"), label, font, c.Theme().Text)
						clock = *n.Local(e, "service-spin-start", func() time.Time { return time.Time{} })
						e.Label("旋转图标")
						n.Text(c, label).FontSize(13).FixedLineHeight(20).Label("状态文字")
					})
				})
			}, 300, 90)
			ui.SetDark(dark)
			ui.Frame()
			started := clock
			ui.Advance(340 * time.Millisecond)
			label = text + " 继续"
			ui.Frame()
			ui.Advance(40 * time.Millisecond)
			if clock != started || started.IsZero() {
				t.Fatal("caption restarted spinner", started, clock)
			}
			// A static angle has symmetric ink. Check visible pixels, not line boxes.
			ui.SetPreferences(n.Preferences{ReduceMotion: true})
			ui.Frame()
			center := func(r n.Rect) float64 {
				im := ui.Image()
				bg := im.RGBAAt(0, 0)
				top, bottom := im.Bounds().Max.Y, -1
				for y := int(r.Y); y < int(r.Y+r.H); y++ {
					for x := int(r.X); x < int(r.X+r.W); x++ {
						px := im.RGBAAt(x, y)
						d := int(px.R) - int(bg.R)
						if d > 35 || d < -35 {
							top = min(top, y)
							bottom = max(bottom, y)
						}
					}
				}
				if bottom < 0 {
					t.Fatal("missing ink", r)
				}
				return float64(top+bottom) / 2
			}
			// Keep the initial cap/CJK reference for the pixel comparison.
			label = text
			ui.Frame()
			icon, _ := ui.Find("旋转图标")
			word, _ := ui.Find("状态文字")
			if d := center(icon) - center(word); math.Abs(d) > 1 {
				t.Fatal("spinner/text optical centers differ", d, icon, word)
			}
		}
	}
}

func TestWaitingAdmissionPollKeepsCaptionAndCancelDoesNotNavigate(t *testing.T) {
	v, ui := sshSetupUI(t)
	p := servicePreparationFixture()
	p.Phase = app.ServiceWaiting
	v.acceptServicePreparation(p)
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	job := welcomeJob(t, v.Controller, "$device.prepare-service")
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("设备仍有任务或待处理请求") {
		t.Fatal("poll flashed dispatching caption", ui.Texts())
	}
	v.cancelSSHCheck()
	v.welcomeStep(welcomeDevice, -1)
	out := app.ServicePreparationResult{Preparation: job.Input.(app.PrepareService).Preparation}
	out.Preparation.Phase = app.ServiceReconnecting
	b, _ := json.Marshal(out)
	job.Apply(v.Model, b, nil)
	ui.Frame()
	if v.welcome.step != welcomeDevice || v.sshCheck.open() {
		t.Fatal("late preparation stole navigation")
	}
	if v.currentServicePreparation() == nil {
		t.Fatal("cancel discarded uncertain update")
	}
}

func TestPreparationKnownFailureRetriesWithFreshDeviceCheck(t *testing.T) {
	v, ui := sshSetupUI(t)
	p := servicePreparationFixture()
	p.Phase = app.ServiceFailed
	v.acceptServicePreparation(p)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if e := ui.Click("重试准备"); e != nil {
		t.Fatal(e)
	}
	welcomeJob(t, v.Controller, "$device.check")
	if v.currentServicePreparation() != nil {
		t.Fatal("retry retained stale source build")
	}
}

func TestPreparingSecondDeviceCannotEraseUncertainFirstDevice(t *testing.T) {
	v, ui := sshSetupUI(t)
	a := servicePreparationFixture()
	a.ID = "unknown-A"
	a.Phase = app.ServiceInterrupted
	v.acceptServicePreparation(a)
	b := servicePreparationFixture()
	b.ID = "new-B"
	b.Info.DeviceID = "second"
	b.SSH.Target = "user@second.test"
	v.sshTarget = b.SSH.Target
	v.acceptServicePreparation(b)
	state, e := v.Model.EncodeState()
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(state), a.ID) || !strings.Contains(string(state), b.ID) {
		t.Fatal("second target erased unresolved first intent", string(state))
	}
	v.cancelSSHCheck()
	v.sshCheck = sshEditing
	v.sshTarget = a.SSH.Target
	v.checkSSHDevice()
	ui.Frame()
	ui.Advance(1200 * time.Millisecond)
	job := welcomeJob(t, v.Controller, "$device.prepare-service")
	in := job.Input.(app.PrepareService)
	if in.Action != "observe" || in.Preparation.ID != a.ID {
		t.Fatal("returning to A created a new update", in)
	}
}

func TestFullObservationQueueNeverEnablesUpdateRetry(t *testing.T) {
	v, _ := sshSetupUI(t)
	p := servicePreparationFixture()
	p.Phase = app.ServiceObserving
	v.acceptServicePreparation(p)
	for len(v.Controller.Jobs) < cap(v.Controller.Jobs) {
		v.Controller.Jobs <- app.Job{Method: "occupied"}
	}
	v.requestServicePreparation(p, "observe")
	if v.currentServicePreparation().Phase != app.ServiceInterrupted {
		t.Fatal("queue failure erased uncertainty", v.currentServicePreparation().Phase)
	}
	label, _ := servicePreparationAction(v.currentServicePreparation())
	if label != "重新连接" {
		t.Fatal(label)
	}
}

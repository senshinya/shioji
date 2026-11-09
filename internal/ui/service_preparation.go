package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/protocol"
	"strconv"
	"strings"
	"time"
)

var serviceSpinnerIcon = svg("M10 3a7 7 0 1 1-7 7")

// The element key and clock belong to the loading run, never to its caption.
// Paint rotates around the actual inline glyph center rather than rotating a
// line-height box (which also moves the icon up and down relative to CJK ink).
func serviceSpinner(c *n.Context, label string, font n.Font, color n.Color) n.Element {
	m := inlineFontMetrics(label, font)
	e := n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: 14, ObjectHeight: m.ascent, ObjectDescent: m.descent}).Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).Role(n.RoleNone)
	started := *n.Local(e, "service-spin-start", func() time.Time { return c.Now() })
	reduced := c.Preferences().ReduceMotion
	return e.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		r.Y += m.ascent - (7 + m.center)
		r.H = 14
		angle := float32(0)
		if !reduced {
			angle = float32(math.Mod(p.Now().Sub(started).Seconds()/1.3, 1) * 360)
			p.AnimationFrameRate(60)
		}
		p.Transform(n.Rotation(angle), r.X+r.W/2, r.Y+r.H/2, func() { p.Icon(serviceSpinnerIcon, r, color) })
	})
}

func (v *View) servicePreparationVisible(p *app.ServicePreparation) bool {
	return p != nil && v.welcome != nil && v.welcome.step == welcomeSSH && v.welcome.key == "" && strings.TrimSpace(v.sshTarget) == p.SSH.Target && migrationPort(v.sshPort) == p.SSH.Port && v.sshCheck.open()
}

type servicePreparationUI struct {
	pending bool
	from    app.ServicePreparationPhase
	next    time.Time
}

func (v *View) preparationUI(p *app.ServicePreparation) *servicePreparationUI {
	if v.servicePreparations == nil {
		v.servicePreparations = map[string]*servicePreparationUI{}
	}
	state := v.servicePreparations[p.Info.DeviceID]
	if state == nil {
		state = &servicePreparationUI{}
		v.servicePreparations[p.Info.DeviceID] = state
	}
	return state
}
func (v *View) currentServicePreparation() *app.ServicePreparation {
	for _, p := range v.Model.ServicePreparations {
		if p.SSH.Target == strings.TrimSpace(v.sshTarget) && p.SSH.Port == migrationPort(v.sshPort) {
			return p
		}
	}
	return nil
}
func (v *View) acceptServicePreparation(p *app.ServicePreparation) {
	if v.Model.ServicePreparations == nil {
		v.Model.ServicePreparations = map[string]*app.ServicePreparation{}
	}
	if previous := v.Model.ServicePreparations[p.Info.DeviceID]; previous != nil && previous.ID != p.ID && previous.Phase != app.ServiceFailed {
		// A checked alias may resume observation, but never replace the old build
		// pair, operation ID or dispatch intent for the same authoritative device.
		previous.SSH = p.SSH
		previous.Name = p.Name
		p = previous
		if p.Phase == app.ServiceInterrupted {
			p.Phase = app.ServiceObserving
		}
	}
	v.Model.ServicePreparations[p.Info.DeviceID] = p
	v.Model.ServicePreparationDeviceID = p.Info.DeviceID
	v.sshSetup.Info = p.Info
	v.sshError = ""
	v.sshCheck = sshPreparing
	v.retireSSHSetupTerminal()
	v.preparationUI(p).next = v.frameTime
	if v.Saver != nil {
		v.queueState()
	}
}

func servicePreparationCaption(p *app.ServicePreparation) string {
	switch p.Phase {
	case app.ServiceStaging, app.ServiceReadyToUpdate:
		return "正在准备更新"
	case app.ServiceWaiting:
		return "设备仍有任务或待处理请求"
	case app.ServiceDispatching:
		return "正在更新设备服务"
	case app.ServiceReconnecting:
		return "正在重新连接"
	case app.ServiceObserving:
		return "正在核对更新结果"
	case app.ServiceInterrupted:
		return "连接已中断，尚未确认更新结果"
	case app.ServiceFailed:
		return "服务准备未完成"
	}
	return "正在检查设备服务"
}
func servicePreparationAction(p *app.ServicePreparation) (string, bool) {
	if p.Phase == app.ServiceInterrupted {
		return "重新连接", false
	}
	if p.Phase == app.ServiceFailed {
		return "重试准备", false
	}
	if p.Phase == app.ServiceWaiting {
		return "等待空闲", true
	}
	return "正在准备", true
}
func (v *View) retryServicePreparation() {
	p := v.currentServicePreparation()
	if p == nil || v.preparationUI(p).pending {
		return
	}
	if p.Phase == app.ServiceInterrupted {
		p.Phase = app.ServiceObserving
	} else if p.Phase == app.ServiceFailed {
		delete(v.Model.ServicePreparations, p.Info.DeviceID)
		if v.Saver != nil {
			v.queueState()
		}
		v.checkSSHDevice()
		return
	}
	p.Message = ""
	v.preparationUI(p).next = v.frameTime
	if v.Saver != nil {
		v.queueState()
	}
}
func (v *View) requestServicePreparation(p *app.ServicePreparation, action string) {
	if p == nil || v.preparationUI(p).pending || v.Controller == nil {
		return
	}
	u := v.preparationUI(p)
	u.from = p.Phase
	if action == "update" {
		p.Phase = app.ServiceDispatching
		p.RequestedAt = time.Now()
	}
	in := *p
	var prepare func() error
	if action == "update" && v.Saver != nil {
		v.queueState()
		prepare = v.Saver.FlushLatest
	}
	u.pending = true
	job := app.Job{Method: "$device.prepare-service", Input: app.PrepareService{Action: action, Preparation: in}, Prepare: prepare, Apply: func(m *app.Model, b json.RawMessage, err error) {
		u.pending = false
		u.next = v.frameTime.Add(time.Second)
		current := m.ServicePreparations[in.Info.DeviceID]
		if current == nil || current.ID != in.ID {
			return
		}
		if err != nil {
			var remote *protocol.RemoteError
			if action != "observe" && errors.As(err, &remote) {
				current.Phase = app.ServiceFailed
			} else {
				current.Phase = app.ServiceInterrupted
			}
			current.Message = err.Error()
		} else {
			var result app.ServicePreparationResult
			if json.Unmarshal(b, &result) != nil || result.Preparation.ID != in.ID || result.Preparation.Info.DeviceID != in.Info.DeviceID || result.Preparation.Candidate.BuildID != in.Candidate.BuildID {
				current.Phase = app.ServiceInterrupted
				current.Message = "设备准备回执身份不一致"
			} else if result.Preparation.Phase == app.ServiceReady {
				if result.Check == nil || result.Check.Info.DeviceID != in.Info.DeviceID {
					current.Phase = app.ServiceInterrupted
					current.Message = "Agent 目录回执身份不一致"
				} else {
					visible := v.servicePreparationVisible(current)
					delete(m.ServicePreparations, in.Info.DeviceID)
					if m.ServicePreparationDeviceID == in.Info.DeviceID {
						m.ServicePreparationDeviceID = ""
					}
					if visible {
						v.sshSetup = *result.Check
						v.finishSSHSetup(*result.Check)
					}
				}
			} else {
				current.Phase = result.Preparation.Phase
				current.Message = result.Preparation.Message
			}
		}
		if v.Saver != nil {
			v.queueState()
		}
	}}
	if !v.Controller.Do(job) {
		u.pending = false
		if action == "observe" {
			p.Phase = app.ServiceInterrupted
		} else {
			p.Phase = app.ServiceFailed
		}
		p.Message = "操作队列繁忙，请重试"
		if v.Saver != nil {
			v.queueState()
		}
	}
}

func (v *View) syncServicePreparation(c *n.Context) {
	if !v.servicePreparationRestored {
		v.servicePreparationRestored = true
		if p := v.Model.ServicePreparations[v.Model.ServicePreparationDeviceID]; p != nil && v.Model.FirstRun && v.setupFlow().step == welcomeDevice {
			f := v.setupFlow()
			f.key = ""
			f.step = welcomeSSH
			v.sshTarget = p.SSH.Target
			v.sshName = p.Name
			v.sshPort = ""
			if p.SSH.Port != 0 {
				v.sshPort = strconv.Itoa(p.SSH.Port)
			}
			v.sshSetup.Info = p.Info
			v.sshCheck = sshPreparing
		}
	}
	for _, p := range v.Model.ServicePreparations {
		u := v.preparationUI(p)
		if u.pending {
			continue
		}
		if v.servicePreparationVisible(p) && v.sshCheck != sshChecking {
			v.sshCheck = sshPreparing
		}
		action := p.NextAction()
		if action == "" {
			continue
		}
		if c.Now().Before(u.next) {
			c.After(u.next.Sub(c.Now()))
			continue
		}
		v.requestServicePreparation(p, action)
	}
}

func (v *View) sshServiceContent(c *n.Context, t *n.Theme) {
	caption, message := "", ""
	loading := false
	failed := false
	if p := v.currentServicePreparation(); v.servicePreparationVisible(p) && v.sshCheck == sshPreparing {
		caption = servicePreparationCaption(p)
		if v.preparationUI(p).pending && v.preparationUI(p).from == app.ServiceWaiting {
			caption = "设备仍有任务或待处理请求"
		}
		message = p.Message
		loading = p.NextAction() != ""
		failed = p.Phase == app.ServiceFailed
	} else if v.sshCheck == sshChecking {
		caption = "正在检查设备服务"
		loading = true
	} else if flow := v.setupMigration(); flow != nil && (v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating) {
		caption = migrationCaption(flow)
		message = migrationMessage(flow)
		loading = flow.phase == migrationViewInspecting || flow.phase == migrationViewFollowing || flow.phase == migrationViewRechecking
		if flow.target.Status != nil && !flow.target.Status.Phase.Active() {
			loading = false
		}
	}
	frame := n.Column(c.Key("ssh-service-progress")).FillWidth().Clip()
	// Keep the row alive while height closes, without replaying its entrance
	// when another loading phase follows the current one.
	retained := n.Local(frame, "caption", func() string { return caption })
	if caption != "" {
		*retained = caption
	}
	animatedContentHeight(c, frame, caption != "", 240*time.Millisecond, func() float32 {
		color := t.TextMuted
		if failed {
			color = n.Hex("#b4433b")
			if t.Dark {
				color = n.Hex("#ed938a")
			}
		}
		n.Column(c).FillWidth().Padding(12, 0, 0).Children(func() {
			n.Row(c.Key("progress-line")).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
				font := n.Font{Family: t.Font, Size: 13, Weight: 400}
				if loading {
					serviceSpinner(c.Key("spinner"), *retained, font, color).Label("服务准备进度图标")
				}
				n.Text(c.Key("progress-caption"), *retained).FontSize(13).FixedLineHeight(20).TextColor(color).MinWidth(0).Grow(1).Tooltip(message).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			})
		})
		return 0
	})
}

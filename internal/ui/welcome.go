package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

type welcomeStep uint8

const (
	welcomeDevice welcomeStep = iota
	welcomeLocal
	welcomeSSH
	welcomeAgents
	welcomeAgentOptions
	welcomeWorkspace
	welcomeDone
)

type welcomeFlow struct {
	changed                time.Time
	step                   welcomeStep
	direction              float32
	key, deviceName, error string
	agents                 *deviceAgentEditor
	name, directory        string
	directories            []welcomeDirectory
	nextDirectory          int
	busy                   bool
	width                  float32
}
type welcomeDirectory struct {
	id      int
	path    string
	removed time.Time
	added   bool
}

func (v *View) setupFlow() *welcomeFlow {
	if v.welcome == nil {
		v.welcome = &welcomeFlow{key: "local", deviceName: v.Model.LocalName(), width: 560}
	}
	return v.welcome
}
func (v *View) welcomeStep(step welcomeStep, direction float32) {
	f := v.setupFlow()
	if f.step == step {
		return
	}
	f.step, f.direction, f.changed = step, direction, v.frameTime
	f.error = ""
}
func (v *View) welcomeView(c *n.Context, t *n.Theme) {
	f := v.setupFlow()
	width, height := c.Size()
	top := max(float32(112), min(height/2-208, 256))
	distance := width
	duration := 260 * time.Millisecond
	if c.Preferences().ReduceMotion {
		distance = 0
		duration = 0
	}
	n.Column(c.Key("welcome")).Fill().Background(t.Background).Transition(n.ElementTransition{Duration: duration, Ease: n.EaseOut, Exit: &n.Motion{X: -distance, Opacity: 1}}).Children(func() {
		n.Row(c).FillWidth().Height(56).DragWindow()
		n.Scroll(c).FillWidth().Grow(1).Padding(top-56, 0, 24).Children(func() {
			n.Column(c).Width(min(560, width-40)).AlignSelf(n.Center).Label("欢迎内容").Children(func() {
				v.welcomeSteps(c, t, f, false)
				v.welcomeStage(c, t, f, false)
				v.welcomeError(c, v.Model.Error)
			})
		})
	})
}
func (v *View) welcomeSteps(c *n.Context, t *n.Theme, f *welcomeFlow, add bool) {
	index := 0
	if f.step == welcomeAgents || f.step == welcomeAgentOptions {
		index = 1
	}
	if f.step == welcomeWorkspace {
		index = 2
	}
	labels := []string{"设备", "Agent", "工作区"}
	if add {
		labels = []string{"连接", "Agent"}
		if v.sshCheck == sshSelecting || v.sshCheck == sshSaving {
			index = 1
		}
	}
	n.Row(c).FillWidth().Height(20).Gap(12).AlignItems(n.Baseline).Margin(0, 0, 28).Children(func() {
		for i, label := range labels {
			if i > 0 {
				inlineSymbol(c, disclosureRight, label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 10, t.TextMuted)
			}
			color := t.TextMuted
			if i == index {
				color = t.Text
			}
			n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(color)
		}
	})
}
func (v *View) welcomeHeading(c *n.Context, t *n.Theme, title, key string, device bool) {
	n.Row(c).FillWidth().MinHeight(36).Margin(0, 0, 28).AlignItems(n.Baseline).Gap(16).Children(func() {
		n.Text(c, title).FontSize(28).FontWeight(400).FixedLineHeight(36).Grow(1)
		if device {
			name := v.Model.LocalName()
			if key != "local" {
				name = v.sshName
				if name == "" {
					name = v.sshTarget
				}
				for _, d := range v.Model.Connections {
					if d.ID == key {
						name = d.Name
					}
				}
			}
			inlineIconText(c, monitor, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 8, t.TextMuted)
		}
	})
}
func setupRow(c *n.Context, label string, body func()) {
	n.Row(c).FillWidth().MinHeight(40).PaddingY(6).Gap(24).AlignItems(n.Start).Children(func() {
		n.Text(c, label).FontSize(13).FixedLineHeight(20).PaddingY(4).Grow(1)
		n.Column(c).Width(320).Children(body)
	})
}
func setupField(c *n.Context, label string, value *string, placeholder string, disabled bool) n.Element {
	return textField(c, value).FillWidth().Height(28).Padding(4, 8).FontSize(12).FixedLineHeight(20).Label(label).Placeholder(placeholder).Background(c.Theme().Background).Disabled(disabled)
}
func (v *View) welcomeFooter(c *n.Context, back, next string, disabled bool, onBack, onNext func()) {
	n.Row(c.Key("welcome-footer")).FillWidth().MinHeight(32).Margin(32, 0, 0).Justify(n.SpaceBetween).AlignItems(n.Center).Children(func() {
		if back != "" {
			if deviceAction(c, back).MarginStart(-8).Disabled(v.setupFlow().busy || v.setupFlow().agents != nil && v.setupFlow().agents.busy).Clicked() {
				onBack()
			}
		} else {
			n.Spacer(c).Grow(1)
		}
		button := n.PrimaryButton(c, "").Height(32).PaddingX(14).Radius(6).Label(next).Disabled(disabled).Children(func() {
			n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
				n.Text(c.Key("action-label"), next).FontSize(13).FontWeight(400).FixedLineHeight(20).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				if next == "继续" || next == "开始使用" || next == "连接并准备服务" || next == "添加设备" {
					inlineSymbol(c, disclosureRight, next, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, c.Theme().AccentText).Transition(fadeMotion(c, 140*time.Millisecond))
				}
			})
		})
		button.Transition(n.ElementTransition{Position: true, Size: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut})
		if button.Clicked() {
			onNext()
		}
	})
}
func (v *View) welcomeError(c *n.Context, message string) {
	box := n.Column(c.Key("setup-error")).FillWidth().Clip()
	last := n.Local(box, "text", func() string { return message })
	if message != "" {
		*last = message
	}
	animatedContentHeight(c, box, message != "", 180*time.Millisecond, func() float32 {
		n.Text(c, *last).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(c.Theme().TextMuted).PaddingY(8).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		return 0
	})
}
func (v *View) welcomePage(c *n.Context, t *n.Theme, f *welcomeFlow, step welcomeStep, add bool) {
	switch step {
	case welcomeDevice:
		v.welcomeHeading(c, t, "选择设备", f.key, false)
		n.Column(c).FillWidth().Gap(4).Children(func() {
			row := n.Row(c).Width(f.width + 16).MarginX(-8).Height(44).PaddingX(8).Gap(8).AlignItems(n.Center).Radius(7).Background(t.Surface)
			controlFeedback(c, row, 7)
			row.Children(func() {
				n.Row(c).Grow(1).AlignItems(n.Baseline).Children(func() {
					inlineIconText(c, monitor, v.Model.LocalName(), n.Font{Family: t.Font, Size: 14, Weight: 400}, 16, 8, t.Text)
				})
				n.Icon(c, check).Size(16, 16).TextColor(t.TextMuted)
				if deviceAction(c, "配置").Label("配置本机").MarginEnd(-8).Clicked() {
					f.deviceName = v.Model.LocalName()
					v.welcomeStep(welcomeLocal, 1)
				}
			})
			if control(c, 7).Width(f.width + 16).MarginX(-8).Height(44).PaddingX(8).Label("通过 SSH 连接").Children(func() {
				n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
					inlineIconText(c, terminalIcon, "通过 SSH 连接", n.Font{Family: t.Font, Size: 14, Weight: 400}, 16, 8, t.Text)
					n.Spacer(c).Grow(1)
					inlineSymbol(c, disclosureRight, "通过 SSH 连接", n.Font{Family: t.Font, Size: 14, Weight: 400}, 16, t.TextMuted)
				})
			}).Clicked() {
				v.sshCheck = sshEditing
				f.key = ""
				v.welcomeStep(welcomeSSH, 1)
			}
		})
		v.welcomeFooter(c, "稍后设置", "继续", v.Controller == nil || !v.deviceState("local").Connected, func() { v.finishWelcome() }, func() {
			f.key = "local"
			f.agents = agentEditor(v.deviceState("local").Readiness.Harnesses)
			f.agents.epoch = v.deviceState("local").ConnectionEpoch
			v.loadDeviceAgents("local", v.deviceState("local"), f.agents)
			v.welcomeStep(welcomeAgents, 1)
		})
	case welcomeLocal:
		v.welcomeHeading(c, t, "配置设备", "local", false)
		setupRow(c, "设备名称", func() { setupField(c, "设备显示名称", &f.deviceName, "这台 Mac", f.busy) })
		v.backgroundSwitch(c, t, "local", v.deviceState("local"), true)
		v.welcomeError(c, f.error)
		v.welcomeFooter(c, "取消", "保存", strings.TrimSpace(f.deviceName) == "", func() { f.deviceName = v.Model.LocalName(); v.welcomeStep(welcomeDevice, -1) }, func() {
			if err := v.Model.RenameDevice("local", f.deviceName); err != nil {
				f.error = err.Error()
				return
			}
			v.welcomeStep(welcomeDevice, -1)
		})
	case welcomeSSH:
		v.welcomeHeading(c, t, "连接设备", f.key, true)
		v.welcomeSSHFields(c, t, f)
	case welcomeAgents:
		v.welcomeHeading(c, t, "选择 Agent", f.key, true)
		v.welcomeAgents(c, t, f, add)
	case welcomeAgentOptions:
		v.welcomeAgentOptions(c, t, f, add)
	case welcomeWorkspace:
		v.welcomeWorkspace(c, t, f)
	}
}
func (v *View) finishWelcome() {
	f := v.setupFlow()
	f.step = welcomeDone
	f.changed = v.frameTime
	v.Model.FirstRun = false
	v.home()
	v.focusComposer = true
}
func (v *View) welcomeSSHFields(c *n.Context, t *n.Theme, f *welcomeFlow) {
	v.syncSSHSetupTerminal()
	disabled := v.sshCheck.busy() || v.sshCheck == sshAwaitingTrust || v.sshCheck == sshConfiguring || v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating
	setupRow(c, "SSH 地址", func() { setupField(c, "SSH 地址", &v.sshTarget, "用户名@主机或别名", disabled) })
	scope := n.Column(c.Key("connection-state"))
	open := n.Local(scope, "options", func() bool { return false })
	toggle := control(c).Width(f.width + 16).MarginX(-8).Height(40).PaddingX(8).Label("连接选项").Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
			n.Text(c, "连接选项").FontSize(13).FixedLineHeight(20).Grow(1).TextColor(t.TextMuted)
			angle := float32(-90)
			if *open {
				angle = 0
			}
			inlineSymbol(c, chevron, "连接选项", n.Font{Family: t.Font, Size: 13, Weight: 400}, 16, t.TextMuted, angle)
		})
	})
	if toggle.Disabled(disabled).Clicked() {
		*open = !*open
	}
	frame := n.Column(c.Key("connection-options")).FillWidth().Clip()
	animatedContentHeight(c, frame, *open, 240*time.Millisecond, func() float32 {
		setupRow(c, "设备名称", func() { setupField(c, "远程设备名称", &v.sshName, "自动识别", disabled) })
		setupRow(c, "SSH 端口", func() { setupField(c, "SSH 端口", &v.sshPort, "默认", disabled) })
		return 0
	})
	v.sshSetupContent(c, t)
	v.sshMigrationContent(c, t)
	v.welcomeError(c, v.sshError)
	label := "连接并准备服务"
	if disabled {
		label = "正在连接"
	}
	if v.sshCheck == sshAwaitingTrust {
		label = "信任并继续"
	}
	if v.sshCheck == sshNeedsKey {
		label = "设置 SSH 密钥"
	}
	if v.sshCheck == sshConfiguring {
		label = "正在设置密钥"
	}
	migrationDisabled := false
	if v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating {
		label, migrationDisabled = migrationAction(v.setupMigration())
	}
	back := "返回"
	if !v.Model.FirstRun {
		back = "取消"
	}
	v.welcomeFooter(c, back, label, disabled && v.sshCheck != sshAwaitingTrust && v.sshCheck != sshLegacyUpgrade && v.sshCheck != sshMigrating || migrationDisabled || v.Controller == nil || strings.TrimSpace(v.sshTarget) == "", func() { v.cancelSSHCheck(); f.key = "local"; v.welcomeStep(welcomeDevice, -1) }, func() {
		if v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating {
			v.performMigrationAction(v.setupMigration())
		} else if v.sshCheck == sshNeedsKey {
			v.startSSHKeySetup()
		} else {
			v.checkSSHDevice()
		}
		if v.sshCheck.busy() || v.sshCheck == sshConfiguring {
			*open = false
		}
	})
}
func (v *View) welcomeAgents(c *n.Context, t *n.Theme, f *welcomeFlow, add bool) {
	e := f.agents
	if e == nil {
		n.Text(c, "正在检测").FontSize(12).TextColor(t.TextMuted)
		return
	}
	connected := f.key != "" && v.deviceState(f.key).Connected || v.sshCheck == sshSelecting || v.sshCheck == sshSaving
	catalogFrame := n.Column(c.Key("agent-catalog")).Width(f.width + 16).MarginX(-8).Clip()
	animatedContentHeight(c, catalogFrame, true, 200*time.Millisecond, func() float32 {
		n.Column(c).FillWidth().PaddingX(8).Children(func() {
			for i := range e.catalog {
				info := &e.catalog[i]
				header := n.Row(c.Key(info.ID)).Width(f.width + 16).MarginX(-8).PaddingX(8).Height(56).Gap(12).AlignItems(n.Center).Radius(6).Transition(fadeMotion(c, 160*time.Millisecond))
				controlFeedback(c, header, 6)
				header.Children(func() {
					n.Row(c).Grow(1).AlignItems(n.Baseline).Children(func() {
						inlineIconText(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: 14, Weight: 400}, 24, 8, t.Text)
					})
					label := runtimeSetupLabel(*info, connected)
					if e.loading {
						label = "正在检测"
					}
					n.Text(c.Key("runtime-status"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
					if deviceAction(c, "配置").Label("配置 " + info.Name).Disabled(e.busy || e.loading || info.Installation == nil).Clicked() {
						e.edit(*info)
						e.setup = true
						e.tab = "常规"
						v.welcomeStep(welcomeAgentOptions, 1)
					}
					enabled := info.Configured
					if settingsSwitch(c, &enabled).Label("启用 " + info.Name).Disabled(e.busy || e.loading || !connected).Changed() {
						info.Configured = enabled
					}
				})
			}
			if len(e.catalog) == 0 {
				label := "暂无可用 Agent"
				if e.loading {
					label = "正在检测"
				}
				n.Text(c.Key("empty-catalog"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transition(fadeMotion(c, 160*time.Millisecond))
			}
		})
		return 0
	})
	message := e.error
	if f.key == "" && v.sshError != "" {
		message = v.sshError
	}
	v.welcomeError(c, message)
	back := "返回"
	next := "继续"
	if add {
		next = "添加设备"
	}
	if e.busy {
		next = "正在检查"
	}
	v.welcomeFooter(c, back, next, e.loading || e.busy || !connected || len(e.input().Agents) == 0 || v.Controller == nil, func() {
		if f.key == "" {
			v.sshCheck = sshEditing
			v.welcomeStep(welcomeSSH, -1)
		} else {
			v.welcomeStep(welcomeDevice, -1)
		}
	}, func() {
		if f.key == "" {
			v.completeSSHDevice()
		} else {
			v.admitWelcomeAgents(f)
		}
	})
}
func (v *View) admitWelcomeAgents(f *welcomeFlow) {
	e := f.agents
	if e == nil || e.busy {
		return
	}
	e.busy = true
	key, epoch := f.key, v.deviceState(f.key).ConnectionEpoch
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.agents", Input: e.input(), Apply: func(m *app.Model, b json.RawMessage, err error) {
		e.busy = false
		if v.welcome != f || f.step != welcomeAgents || v.deviceState(key).ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			e.error = err.Error()
			return
		}
		var result protocol.AgentSetupResult
		if json.Unmarshal(b, &result) != nil {
			e.error = "设备返回的配置结果无效"
			return
		}
		if !result.Ready {
			e.error = result.Error
			return
		}
		state := v.deviceState(key)
		state.Readiness = result.Readiness
		m.RefreshDevices()
		v.welcomeStep(welcomeWorkspace, 1)
	}}) {
		e.busy = false
		e.error = "设备操作繁忙，请重试"
	}
}

func (v *View) welcomeStage(c *n.Context, t *n.Theme, f *welcomeFlow, add bool) {
	stage := n.Column(c.Key("stage")).Width(f.width + 16).MarginX(-8).Clip()
	stage.Children(func() {
		current := n.Column(c.Key(f.step)).FillWidth().PaddingX(8)
		current.Children(func() {
			if add && f.step == welcomeSSH {
				v.welcomeSSHFields(c, t, f)
			} else if add && f.step == welcomeAgents {
				v.welcomeAgents(c, t, f, true)
			} else {
				v.welcomePage(c, t, f, f.step, add)
			}
		})
		direction := f.direction
		if direction == 0 {
			direction = 1
		}
		distance := f.width + 16
		if c.Preferences().ReduceMotion {
			distance = 0
		}
		if !f.changed.IsZero() && c.Now().Sub(f.changed) < 260*time.Millisecond {
			stage.Transition(n.ElementTransition{Size: true, Duration: motionAnimation(c, 260*time.Millisecond).Duration, Ease: n.EaseOut})
		}
		current.Transition(n.ElementTransition{Duration: motionAnimation(c, 260*time.Millisecond).Duration, Ease: n.EaseOut, Enter: &n.Motion{X: direction * distance, Opacity: 1}, Exit: &n.Motion{X: -direction * distance, Opacity: 1}})
	})
}

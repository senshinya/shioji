package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strconv"
	"strings"
	"time"
)

type welcomeStep uint8

const (
	welcomeDevice welcomeStep = iota
	welcomeSSH
	welcomeAgents
	welcomeAgentOptions
	welcomeWorkspace
	welcomeImport
	welcomeDone
	welcomeTailnetJoin
	welcomeTailnetPeers
)

type welcomeRoute uint8

const (
	welcomeRouteLocal welcomeRoute = iota
	welcomeRouteSSH
	welcomeRouteTailnet
)

type welcomeFlow struct {
	icon            app.DeviceIcon
	footer          func()
	collectFooter   bool
	workspace       domain.Workspace
	contentTop      float32
	changed         time.Time
	step            welcomeStep
	key, error      string
	route           welcomeRoute
	agents          *deviceAgentEditor
	localAgents     *deviceAgentEditor
	remoteAgents    map[string]*deviceAgentEditor
	directSSH       welcomeSSHInput
	tailnetSSH      map[string]welcomeSSHInput
	sshOptions      bool
	workspaceOwner  string
	workspaceDrafts map[string]welcomeWorkspaceInput
	agentDrafts     map[welcomeAgentKey]welcomeAgentInput
	name, directory string
	directories     []welcomeDirectory
	nextDirectory   int
	busy            bool
	width           float32
}

type welcomeAgentKey struct{ device, harness string }
type welcomeAgentInput struct {
	path, tab string
	draft     runtimeOptionDraft
}

// These are only connection form drafts. Verified host keys, authentication,
// service preparation and requests remain owned by the shared SSH flow.
type welcomeSSHInput struct {
	icon               app.DeviceIcon
	target, name, port string
	options            bool
}

type welcomeWorkspaceInput struct {
	workspace       domain.Workspace
	name, directory string
	directories     []welcomeDirectory
	nextDirectory   int
}
type welcomeDirectory struct {
	id      int
	path    string
	removed time.Time
	added   bool
}

func (v *View) setupFlow() *welcomeFlow {
	if v.welcome == nil {
		v.welcome = &welcomeFlow{key: "local", width: 560}
	}
	return v.welcome
}
func (v *View) welcomeStep(step welcomeStep, direction float32) {
	f := v.setupFlow()
	if f.step == step {
		return
	}
	f.step, f.changed = step, v.frameTime
	f.error = ""
}
func (v *View) welcomeView(c *n.Context, t *n.Theme) {
	f := v.setupFlow()
	if f.localAgents != nil && f.key == "local" && (f.step == welcomeAgents || f.step == welcomeAgentOptions) {
		v.syncWelcomeLocalAgents()
	}
	width, height := c.Size()
	f.width = min(560, max(0, width-40))
	top := max(float32(112), min(height/2-208, 256))
	if f.step == welcomeImport {
		top = 112
	}
	// Preserve room for the form and footer in short or zoomed windows.
	top = min(top, max(56, height-300))
	distance := width
	duration := 260 * time.Millisecond
	if c.Preferences().ReduceMotion {
		distance = 0
		duration = -1
	}
	n.Column(c.Key("welcome")).Fill().Background(t.Background).Transition(n.ElementTransition{Duration: duration, Ease: n.EaseOut, Exit: &n.Motion{X: -distance, Opacity: 1}}).Children(func() {
		n.Row(c).FillWidth().Height(56).DragWindow()
		scroll := n.Scroll(c.Key("welcome-scroll")).FillWidth().Grow(1)
		if c.Preferences().ReduceMotion {
			top = scroll.Jump("content-top", top)
		} else {
			top = scroll.AnimateTo("content-top", top, motionAnimation(c, configurationTabDuration))
		}
		f.contentTop = top
		scroll.Padding(top-56, 0, 24).Children(func() {
			n.Column(c).Width(f.width).AlignSelf(n.Center).Label("欢迎内容").Children(func() {
				v.welcomeSteps(c, t, f, false)
				v.welcomeStage(c, t, f, false)
				message := v.Model.ConnectionError
				if message == "" {
					message = v.Model.Error
				}
				v.welcomeError(c, message)
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
	if f.step == welcomeImport {
		index = 3
	}
	labels := []string{"设备", "Agent", "工作区", "导入"}
	if add {
		labels = []string{"连接", "Agent"}
		if v.sshCheck == sshSelecting || v.sshCheck == sshSaving {
			index = 1
		}
	}
	gap := float32(20)
	if !add || f.step == welcomeTailnetJoin || f.step == welcomeTailnetPeers {
		gap = 12
	}
	n.Row(c).FillWidth().Height(20).Gap(12).AlignItems(n.Baseline).Margin(0, 0, gap).Children(func() {
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
	gap := float32(configurationHeadingGap)
	if v.Model.FirstRun {
		gap = 12
	}
	n.Row(c).FillWidth().MinHeight(configurationTitleLine).Margin(0, 0, gap).AlignItems(n.Baseline).Gap(16).Children(func() {
		n.Text(c, title).FontSize(func() float32 {
			if !v.Model.FirstRun {
				return 14
			}
			return configurationTitleSize
		}()).FontWeight(400).FixedLineHeight(configurationTitleLine).Grow(1)
		if device {
			v.welcomeDeviceIcon(c, t, key, "设备图标", true)
		}
	})
}
func setupRow(c *n.Context, label string, body func()) {
	configurationFormRow(c, label, configurationSetupForm, body)
}
func setupField(c *n.Context, label string, value *string, placeholder string, disabled bool) n.Element {
	return configurationTextField(c, value).Label(label).Placeholder(placeholder).Background(c.Theme().Background).Disabled(disabled)
}
func (v *View) welcomeFooter(c *n.Context, back, next string, disabled bool, onBack, onNext func()) {
	f := v.setupFlow()
	if f.collectFooter {
		f.footer = func() { v.welcomeFooter(c, back, next, disabled, onBack, onNext) }
		return
	}
	n.Row(c.Key("welcome-footer")).FillWidth().MinHeight(32).Margin(configurationSectionGap, 0, 0).Justify(n.SpaceBetween).AlignItems(n.Center).Children(func() {
		if back != "" {
			if configurationAction(c, back).MarginStart(-8).Disabled(v.setupFlow().busy || v.setupFlow().agents != nil && v.setupFlow().agents.busy).Clicked() {
				onBack()
			}
		} else {
			n.Spacer(c).Grow(1)
		}
		button := n.PrimaryButton(c, "").Height(32).PaddingX(14).Radius(6).Label(next).Disabled(disabled).Children(func() {
			n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
				n.Text(c.Key("action-label"), next).FontSize(13).FontWeight(400).FixedLineHeight(20).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				if next == "继续" || next == "下一步" || next == "开始使用" || next == "连接并准备服务" || next == "添加设备" {
					inlineSymbol(c, disclosureRight, next, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, c.Theme().AccentText).Transition(fadeMotion(c, 140*time.Millisecond))
				}
			})
		})
		// Configuration actions have fixed labels and follow the form's real height.
		// Navigation actions retain their existing right-anchored label resize.
		changingPage := !f.changed.IsZero() && c.Now().Sub(f.changed) < configurationTabDuration
		button.Transition(n.ElementTransition{Position: f.step != welcomeAgentOptions && !changingPage, Size: !changingPage, Colors: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut})
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
		v.welcomeHeading(c, t, "选择执行位置", "", false)
		n.Column(c).FillWidth().Children(func() {
			for _, entry := range []struct {
				name string
				icon *n.SVG
				open func()
			}{
				{"这台 Mac", v.deviceGlyph("local"), v.beginWelcomeLocal},
				{"通过 SSH 连接", terminalIcon, v.beginWelcomeSSH},
				{"从 Tailnet 添加", tailscaleMark, v.beginTailnet},
			} {
				if control(c.Key(entry.name), 6).Width(f.width + 16).MarginX(-8).Height(48).PaddingX(8).Label(entry.name).Children(func() {
					n.Row(c).FillWidth().Gap(12).AlignItems(n.Baseline).Children(func() {
						inlineIconText(c, entry.icon, entry.name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 16, 12, t.Text)
						n.Spacer(c).Grow(1)
						inlineSymbol(c, disclosureRight, entry.name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, t.TextMuted)
					})
				}).Clicked() {
					entry.open()
				}
			}
		})
	case welcomeTailnetJoin:
		v.tailnetJoinPage(c, t)
	case welcomeTailnetPeers:
		v.tailnetPeerPage(c, t)
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
	case welcomeImport:
		v.welcomeHeading(c, t, "导入会话", f.key, true)
		_, height := c.Size()
		n.Column(c).FillWidth().Height(max(240, height-f.contentTop-112)).Children(func() {
			if v.conversationImport != nil {
				v.conversationImportBody(c, t, v.conversationImport)
			}
		})
	}
}

func (v *View) beginWelcomeLocal() {
	f := v.setupFlow()
	v.saveWelcomeSSHInput()
	v.cancelSSHCheck()
	v.sshTailnetPeer = ""
	f.route, f.key = welcomeRouteLocal, "local"
	v.syncWelcomeLocalAgents()
	f.agents = f.localAgents
	v.welcomeStep(welcomeAgents, 1)
}

func carryWelcomeAgentSelections(next, previous *deviceAgentEditor) {
	if previous == nil {
		return
	}
	for i := range next.catalog {
		info := &next.catalog[i]
		for _, old := range previous.catalog {
			if old.ID == info.ID {
				info.Configured = old.Configured
				next.paths[info.ID] = previous.paths[info.ID]
				next.options[info.ID] = previous.options[info.ID]
				break
			}
		}
	}
}

func (v *View) syncWelcomeLocalAgents() {
	f, state := v.setupFlow(), v.deviceState("local")
	if f.localAgents != nil && f.localAgents.epoch == state.ConnectionEpoch {
		return
	}
	previous := f.localAgents
	if previous != nil && previous.openID != "" {
		v.keepWelcomeAgentDraft(previous, previous.openID)
	}
	catalog := state.Readiness.Harnesses
	if len(catalog) == 0 && previous != nil {
		catalog = previous.catalog
	}
	e := agentEditor(append([]domain.HarnessInfo(nil), catalog...))
	carryWelcomeAgentSelections(e, previous)
	e.epoch = state.ConnectionEpoch
	f.localAgents, f.agents = e, e
	if previous != nil && previous.openID != "" {
		for _, info := range e.catalog {
			if info.ID == previous.openID {
				v.openWelcomeAgent(info)
				break
			}
		}
	}
	if v.Controller == nil || !state.Connected || state.ConnectionPhase == app.ConnectionSyncing {
		return
	}
	e.loading = true
	epoch := state.ConnectionEpoch
	if !v.do(app.Job{ConnectionID: "local", Method: "runtime.catalog", Apply: func(m *app.Model, b json.RawMessage, err error) {
		if v.welcome != f || f.localAgents != e || v.deviceState("local").ConnectionEpoch != epoch {
			return
		}
		e.loading = false
		if err != nil {
			e.error = err.Error()
			return
		}
		var ready domain.Readiness
		if json.Unmarshal(b, &ready) != nil {
			e.error = "设备返回的 Agent 列表无效"
			return
		}
		next := agentEditor(ready.Harnesses)
		carryWelcomeAgentSelections(next, e)
		e.catalog, e.paths, e.options, e.optionSupport = next.catalog, next.paths, next.options, next.optionSupport
		if f.key == "local" && f.agents == e && f.step == welcomeAgentOptions {
			for _, info := range e.catalog {
				if info.ID == e.openID {
					v.openWelcomeAgent(info)
					return
				}
			}
			v.welcomeStep(welcomeAgents, -1)
		}
	}}) {
		e.loading = false
		e.error = "设备操作繁忙，请重试"
	}
}

func (v *View) welcomeAgentDraftKey(id string) welcomeAgentKey {
	device := v.Model.ConnectionDeviceID(v.setupFlow().key)
	if v.setupFlow().key == "" {
		device = v.sshSetup.Info.DeviceID
	}
	return welcomeAgentKey{device, id}
}

func (v *View) keepWelcomeAgentDraft(e *deviceAgentEditor, id string) {
	f := v.setupFlow()
	if f.agentDrafts == nil {
		f.agentDrafts = map[welcomeAgentKey]welcomeAgentInput{}
	}
	draft := e.draft
	draft.environment = append([]runtimeEnvironmentDraft(nil), draft.environment...)
	f.agentDrafts[v.welcomeAgentDraftKey(id)] = welcomeAgentInput{e.path, e.tab, draft}
}

func (v *View) openWelcomeAgent(info domain.HarnessInfo) {
	f := v.setupFlow()
	e := f.agents
	e.edit(info)
	e.setup, e.tab = true, "常规"
	if draft, ok := f.agentDrafts[v.welcomeAgentDraftKey(info.ID)]; ok {
		e.path, e.tab, e.draft = draft.path, draft.tab, draft.draft
	}
	v.welcomeStep(welcomeAgentOptions, 1)
}

func (v *View) saveWelcomeSSHInput() {
	f := v.setupFlow()
	draft := welcomeSSHInput{icon: f.icon, target: v.sshTarget, name: v.sshName, port: v.sshPort, options: f.sshOptions}
	if f.route == welcomeRouteSSH {
		f.directSSH = draft
	} else if f.route == welcomeRouteTailnet && v.sshTailnetPeer != "" {
		if f.tailnetSSH == nil {
			f.tailnetSSH = map[string]welcomeSSHInput{}
		}
		f.tailnetSSH[v.sshTailnetPeer] = draft
	}
}

func (v *View) beginWelcomeSSH() {
	f := v.setupFlow()
	v.saveWelcomeSSHInput()
	v.cancelSSHCheck()
	f.route, f.key = welcomeRouteSSH, ""
	v.sshTailnetPeer, v.sshError, v.sshCheck = "", "", sshEditing
	draft := f.directSSH
	f.icon = draft.icon
	v.sshTarget, v.sshName, v.sshPort, f.sshOptions = draft.target, draft.name, draft.port, draft.options
	v.welcomeStep(welcomeSSH, 1)
}
func (v *View) finishWelcome() {
	v.conversationImport = nil
	f := v.setupFlow()
	f.step = welcomeDone
	f.changed = v.frameTime
	v.Model.FirstRun = false
	v.home()
	v.focusComposer = true
}
func (v *View) welcomeSSHFields(c *n.Context, t *n.Theme, f *welcomeFlow) {
	v.syncSSHSetupTerminal()
	disabled := v.sshCheck.busy() || v.sshCheck == sshAwaitingTrust || v.sshCheck == sshConfiguring || v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating || v.sshCheck == sshPreparing
	setupRow(c, "SSH 地址", func() { setupField(c, "SSH 地址", &v.sshTarget, "用户名@主机或别名", disabled) })
	open := &f.sshOptions
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
	v.sshServiceContent(c, t)
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
	if v.sshCheck == sshPreparing && v.currentServicePreparation() != nil {
		label, migrationDisabled = servicePreparationAction(v.currentServicePreparation())
		if v.preparationUI(v.currentServicePreparation()).pending && v.preparationUI(v.currentServicePreparation()).from == app.ServiceWaiting {
			label = "等待空闲"
			migrationDisabled = true
		}
	}
	back := "返回"
	if !v.Model.FirstRun {
		back = "取消"
	}
	v.welcomeFooter(c, back, label, disabled && v.sshCheck != sshAwaitingTrust && v.sshCheck != sshLegacyUpgrade && v.sshCheck != sshMigrating && v.sshCheck != sshPreparing || migrationDisabled || v.Controller == nil || strings.TrimSpace(v.sshTarget) == "", func() {
		if !v.Model.FirstRun {
			v.dismissDeviceSetup()
			return
		}
		v.saveWelcomeSSHInput()
		v.cancelSSHCheck()
		if f.route == welcomeRouteTailnet || v.sshTailnetPeer != "" {
			v.welcomeStep(welcomeTailnetPeers, -1)
		} else if v.Model.FirstRun {
			f.key = "local"
			v.welcomeStep(welcomeDevice, -1)
		} else {
			v.welcome = nil
		}
	}, func() {
		if v.sshCheck == sshPreparing {
			v.retryServicePreparation()
		} else if v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating {
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
	state := v.deviceState(f.key)
	phase := connectionPhase(state.Connected, state.ConnectionPhase, state.Paths)
	connected := f.key != "" && state.Connected && phase == app.ConnectionIdle || v.sshCheck == sshSelecting || v.sshCheck == sshSaving
	catalogFrame := n.Column(c.Key("agent-catalog")).Width(f.width + 16).MarginX(-8).Clip()
	animatedContentHeight(c, catalogFrame, true, 200*time.Millisecond, func() float32 {
		n.Column(c).FillWidth().PaddingX(8).Children(func() {
			shown := 0
			for i := range e.catalog {
				info := &e.catalog[i]
				if info.IsCustomProfileProvider() {
					continue
				}
				shown++
				header := n.Row(c.Key(info.ID)).Width(f.width + 16).MarginX(-8).PaddingX(8).Height(56).Gap(12).AlignItems(n.Center).Radius(6).Transition(fadeMotion(c, 160*time.Millisecond))
				controlFeedback(c, header, 6)
				header.Children(func() {
					label := runtimeSetupLabel(*info, connected)
					if e.loading {
						label = "正在检测"
					} else if !connected && phase != app.ConnectionIdle {
						label = connectionPhaseLabel(phase)
					}
					if n.ButtonBase(c.Key("open")).Height(48).Grow(1).Label("配置 " + info.Name).Disabled(e.busy || e.loading || info.Installation == nil).Children(func() {
						n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
							inlineIconText(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 20, 12, t.Text).
								MinWidth(0).Shrink(1).TooltipWhenTruncated(info.Name)
							systemProfileBadge(c, *info)
							n.Spacer(c).Grow(1)
							n.Text(c.Key("runtime-status"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
							inlineSymbol(c, disclosureRight, info.Name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, t.TextMuted)
						})
					}).Clicked() {
						v.openWelcomeAgent(*info)
					}
					enabled := info.Configured
					if configurationSwitch(c, &enabled).Label("启用 " + info.Name).Disabled(e.busy || e.loading || !connected).Changed() {
						info.Configured = enabled
					}
				})
			}
			if shown == 0 {
				label := "暂无可用 Agent"
				if e.loading {
					label = "正在检测 Agent"
				} else if !connected && phase != app.ConnectionIdle {
					label = connectionPhaseLabel(phase)
				} else if e.error != "" {
					return
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
		if f.key == "" || v.Model.FirstRun && f.key != "local" {
			f.key = ""
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
	f.collectFooter = true
	f.footer = nil
	n.Column(c.Key("stage")).Width(f.width + 16).MarginX(-8).Children(func() {
		// Both branches converge before Agent setup. A route depth determines
		// direction, while the shared panel owns actual height and exit copies.
		depth := 0
		switch f.step {
		case welcomeTailnetJoin:
			depth = 1
		case welcomeTailnetPeers:
			depth = 2
		case welcomeSSH:
			depth = 3
		case welcomeAgents:
			depth = 4
		case welcomeAgentOptions, welcomeWorkspace:
			depth = 5
		case welcomeImport:
			depth = 6
		}
		configurationTabPanel(c.Key("welcome-page"), strconv.Itoa(int(f.step)), depth, configurationPanelFit, func() {
			n.Column(c).FillWidth().PaddingX(8).Children(func() {
				if add && f.step == welcomeSSH {
					v.welcomeSSHFields(c, t, f)
				} else if add && f.step == welcomeAgents {
					v.welcomeAgents(c, t, f, true)
				} else {
					v.welcomePage(c, t, f, f.step, add)
				}
			})
		}, false)
	})
	f.collectFooter = false
	if footer := f.footer; footer != nil {
		f.footer = nil
		footer()
	}
}

func (v *View) welcomeDeviceName(key string) string {
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
	return name
}

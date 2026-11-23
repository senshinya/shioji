package ui

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

var toolHTTPIcon = svg("M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0M2 10h16M10 2c4 5 4 11 0 16M10 2c-4 5-4 11 0 16")
var toolLockIcon = svg("M5 9h10v9H5zM7 9V6a3 3 0 0 1 6 0v3")

type toolArgumentDraft struct {
	id      int
	value   string
	removed time.Time
	added   bool
}
type toolValueDraft struct {
	domain.ToolServiceValue
	id      int
	removed time.Time
	added   bool
}
type toolConfigurationDraft struct {
	config                           domain.ToolServiceConfiguration
	original                         domain.ToolServiceConfiguration
	id, revision, timeout, inherited string
	arguments                        []toolArgumentDraft
	environment, headers             []toolValueDraft
	nextID                           int
	advanced, focus, readOnly, basic bool
	reason                           string
}

func (u *toolServicesUI) editService(service *domain.ToolService) {
	d := &toolConfigurationDraft{config: domain.ToolServiceConfiguration{Transport: "command", Enabled: true}, revision: u.catalog.Revision, timeout: "10", focus: true, basic: u.catalog.BasicEditing}
	if service != nil && service.Configuration != nil {
		d.config = *service.Configuration
		d.id = service.ID
		d.readOnly = !service.Editable
		d.reason = service.ReadOnlyReason
		if d.config.StartupSeconds > 0 {
			d.timeout = strconv.FormatFloat(d.config.StartupSeconds, 'f', -1, 64)
		}
	}
	d.inherited = strings.Join(d.config.InheritedEnvironment, " ")
	d.original = d.config
	if !d.basic && d.original.StartupSeconds == 0 {
		d.original.StartupSeconds = 10
	}
	for _, arg := range d.config.Arguments {
		d.nextID++
		d.arguments = append(d.arguments, toolArgumentDraft{id: d.nextID, value: arg})
	}
	for _, value := range d.config.Environment {
		d.nextID++
		d.environment = append(d.environment, toolValueDraft{ToolServiceValue: value, id: d.nextID})
	}
	for _, value := range d.config.Headers {
		d.nextID++
		d.headers = append(d.headers, toolValueDraft{ToolServiceValue: value, id: d.nextID})
	}
	u.draft = d
	u.message = ""
}
func (d *toolConfigurationDraft) changed() bool {
	current, err := d.value()
	if err != nil {
		return true
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(d.original)
	return string(a) != string(b)
}
func (d *toolConfigurationDraft) value() (domain.ToolServiceConfiguration, error) {
	result := d.config
	result.Arguments = nil
	result.Environment = nil
	result.Headers = nil
	for _, arg := range d.arguments {
		if arg.removed.IsZero() {
			result.Arguments = append(result.Arguments, arg.value)
		}
	}
	for _, entry := range d.environment {
		if entry.removed.IsZero() {
			result.Environment = append(result.Environment, entry.ToolServiceValue)
		}
	}
	for _, entry := range d.headers {
		if entry.removed.IsZero() {
			result.Headers = append(result.Headers, entry.ToolServiceValue)
		}
	}
	result.InheritedEnvironment = strings.Fields(d.inherited)
	if strings.TrimSpace(result.Name) == "" {
		return result, errors.New("请输入服务名称")
	}
	if result.Transport == "command" && strings.TrimSpace(result.Command) == "" {
		return result, errors.New("请输入执行命令")
	}
	if result.Transport == "http" && !strings.HasPrefix(result.URL, "http://") && !strings.HasPrefix(result.URL, "https://") {
		return result, errors.New("请输入 HTTP 或 HTTPS 地址")
	}
	if d.basic {
		return result, nil
	}
	var err error
	result.StartupSeconds, err = strconv.ParseFloat(d.timeout, 64)
	if err != nil || result.StartupSeconds < 0 || result.StartupSeconds > 600 {
		return result, errors.New("启动超时须在 0 到 600 秒之间")
	}
	return result, nil
}
func (v *View) changeToolConfiguration(u *toolServicesUI, change domain.ToolServiceChange) {
	if u.saving || u.saved || v.Controller == nil {
		return
	}
	u.saving = true
	u.mutation++
	mutation := u.mutation
	u.message = ""
	u.mutationID = change.ServiceID
	if !v.toolServiceJob(u, "runtime.tools.change", protocol.ToolServiceInput{HarnessID: u.harness, Change: &change}, func(_ *app.Model, b json.RawMessage, err error) {
		if !v.toolServiceCurrent(u) || mutation != u.mutation || !u.saving {
			return
		}
		u.saving = false
		if err != nil {
			u.message = "保存结果未确认，请刷新后核对"
			var remote *protocol.RemoteError
			if errors.As(err, &remote) {
				u.message = remote.Message
			}
			return
		}
		var result domain.ToolServiceChangeResult
		if json.Unmarshal(b, &result) != nil || !result.Saved {
			u.message = "保存结果未确认，请刷新后核对"
			return
		}
		u.saved = true
		u.draft, u.removingID = nil, ""
		if result.ServiceID != "" {
			u.mutationID = result.ServiceID
		}
		// A refresh started in the editor may have read the old configuration.
		// Replace its request version with a check that starts after this save.
		u.phase = toolServiceIdle
		v.queryToolServices(u, u.catalog.CanRefresh)
	}) {
		u.saving = false
		u.message = "设备操作繁忙，请重试"
	}
}
func toolConfigurationFeedback(c *n.Context, t *n.Theme, message string) {
	frame := n.Column(c.Key("feedback")).FillWidth().Clip()
	text := n.Local(frame, "retained", func() string { return message })
	if message != "" {
		*text = message
	}
	animatedContentHeight(c, frame, message != "", 180*time.Millisecond, func() float32 {
		n.Text(c, *text).FillWidth().FontSize(12).FixedLineHeight(20).PaddingY(8).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		return 0
	})
}

func toolServiceRefreshButton(c *n.Context, label string, loading, disabled bool) n.Element {
	button := control(c, 6).Size(28, 28).Radius(6).Label(label).Tooltip(label).TextColor(c.Theme().TextMuted).Disabled(disabled || loading)
	started := n.Local(button, "refresh-start", func() time.Time { return time.Time{} })
	if !loading {
		*started = time.Time{}
	} else if started.IsZero() {
		*started = c.Now()
	}
	return button.Children(func() {
		glyph := refreshIcon
		if loading {
			glyph = serviceSpinnerIcon
		}
		icon := n.Icon(c.Key("glyph"), glyph).Size(16, 16)
		if loading {
			icon.Label("MCP 刷新进度")
			if !c.Preferences().ReduceMotion {
				icon.Rotate(float32(math.Mod(c.Now().Sub(*started).Seconds()/1.3, 1) * 360))
				c.After(time.Second / 60)
			}
		}
	})
}

func (v *View) toolServiceList(c *n.Context, t *n.Theme, u *toolServicesUI, disabled bool) {
	pending := u.saving || u.phase == toolServiceLoading || u.authorization != nil && u.authorization.Phase.Active()
	busy := pending || u.saved
	disabled = disabled || v.Controller == nil
	n.Row(c).FillWidth().Height(configurationSectionHeader).AlignItems(n.Start).Gap(6).Children(func() {
		configurationSectionTitle(c, "MCP").Grow(1)
		font := n.Font{Family: t.Font, Size: configurationSectionSize, Weight: 500}
		n.Row(c.Key("actions")).Gap(6).Transform(n.Translation(0, -inlineCenterOffset("MCP", font))).Children(func() {
			if toolServiceRefreshButton(c.Key("refresh"), "刷新 MCP", u.phase == toolServiceLoading, disabled || pending).Clicked() {
				v.queryToolServices(u, u.catalog.CanRefresh)
			}
			if u.catalog.CanAdd && v.deviceState(u.key).WorkerMinor >= 24 {
				if iconButton(c, plus, "添加 MCP 服务").Disabled(disabled || busy).Clicked() {
					u.editService(nil)
				}
			}
		})
	})
	n.Scroll(c.Key("service-list")).FillWidth().FillHeight().Grow(1).MinHeight(0).Label("MCP 服务列表").Children(func() {
		n.Column(c).FillWidth().Children(func() {
			for _, service := range u.catalog.Services {
				row := n.Row(c.Key(service.ID)).FillWidth().Height(configurationRowHeight).PaddingY(6).Gap(8).AlignItems(n.Baseline).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Label("MCP 服务：" + service.Name).Transition(n.ElementTransition{Position: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
				row.Children(func() {
					if u.removingID == service.ID {
						n.Text(c, "删除 "+service.Name+"？").FontSize(12).FixedLineHeight(28).Grow(1)
						if configurationAction(c, "取消").Label("取消删除 MCP").Disabled(u.saving).Clicked() {
							u.removingID = ""
						}
						if configurationAction(c, "删除").Label("确认删除 MCP").TextColor(t.Danger).Disabled(disabled || busy).Clicked() {
							v.changeToolConfiguration(u, domain.ToolServiceChange{Action: "delete", ServiceID: service.ID, Revision: u.catalog.Revision})
						}
						return
					}
					glyph := terminalIcon
					if service.Transport == "http" {
						glyph = toolHTTPIcon
					}
					inlineSymbol(c, glyph, service.Name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 16, t.TextMuted)
					if service.Configuration != nil {
						if control(c, 5).Height(28).Grow(1).MinWidth(0).Label("编辑 MCP：" + service.Name).Disabled(disabled || busy).Children(func() { n.Text(c, service.Name).FontSize(13).FixedLineHeight(28).SingleLine().Ellipsis("…").Grow(1) }).Clicked() {
							u.editService(&service)
						}
					} else {
						n.Text(c, service.Name).FontSize(13).FixedLineHeight(28).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(service.Origin)
					}
					active := u.authorization != nil && u.authorization.ServiceID == service.ID && u.authorization.Phase.Active()
					caption := toolServiceLabel(service)
					if active {
						caption = "等待授权"
						if u.authorization.Phase == domain.SignInStarting {
							caption = "正在准备授权"
						}
					}
					if u.saving && u.mutationID == service.ID {
						caption = "正在保存"
					}
					checking := u.saved && u.phase == toolServiceLoading && u.mutationID == service.ID
					if checking {
						caption = "已保存，正在检查连接"
					}
					n.Row(c.Key("status")).Gap(6).AlignItems(n.Baseline).Children(func() {
						if active || service.State == domain.ToolServiceConnecting || u.saving && u.mutationID == service.ID || checking {
							serviceSpinner(c.Key("spinner"), caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
						}
						n.Text(c.Key("caption"), caption).FontSize(12).FixedLineHeight(28).TextColor(t.TextMuted).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
					})
					if active && u.authorization.Challenge.URL != "" {
						if configurationAction(c, "打开授权页").Disabled(disabled).Clicked() {
							if openToolServiceLink(u.authorization.Challenge.URL) != nil {
								u.authorization.Message = "无法打开授权页"
							}
						}
					} else if service.CanAuthorize && (service.State == domain.ToolServiceAuthorizationRequired || service.State == domain.ToolServiceFailed) {
						if configurationAction(c, "授权").Label("授权 " + service.Name).Disabled(disabled || busy).Clicked() {
							v.startToolAuthorization(u, service)
						}
					}
					if service.Editable && v.deviceState(u.key).WorkerMinor >= 24 {
						enabled := service.State != domain.ToolServiceDisabled
						if confirmedConfigurationSwitch(c, &enabled, service.State != domain.ToolServiceDisabled).Label("启用 MCP：" + service.Name).AlignSelf(n.Center).Disabled(disabled || busy).Changed() {
							v.changeToolConfiguration(u, domain.ToolServiceChange{Action: "setEnabled", ServiceID: service.ID, Revision: u.catalog.Revision, Enabled: enabled})
						}
						trigger := iconButton(c, moreActionsIcon, service.Name+" 更多操作").AlignSelf(n.Center).Disabled(disabled || busy)
						check := "检查连接"
						if u.catalog.BasicEditing {
							check = "核对设备配置"
						}
						v.choiceMenu(c, t, "mcp:"+u.device+":"+u.harness+":"+service.ID, trigger, []menuChoice{{id: "edit", name: "编辑", choose: func() { u.editService(&service) }}, {id: "check", name: check, choose: func() { v.queryToolServices(u, u.catalog.CanRefresh) }}, {id: "delete", name: "删除", choose: func() { u.removingID = service.ID }}})
					} else if service.ReadOnlyReason != "" {
						inlineSymbol(c, toolLockIcon, service.Name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, t.TextMuted).Tooltip(service.ReadOnlyReason)
					}
				})
			}
			if len(u.catalog.Services) == 0 && u.phase != toolServiceLoading && u.message == "" {
				n.Text(c, "暂无 MCP 服务").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(8)
			}
		})
	})
	message := u.message
	if u.authorization != nil && u.authorization.Message != "" {
		message = u.authorization.Message
	}
	toolConfigurationFeedback(c, t, message)
}

func (v *View) toolConfigurationEditor(c *n.Context, t *n.Theme, u *toolServicesUI, disabled bool) {
	d := u.draft
	authorizing := u.authorization != nil && u.authorization.Phase.Active()
	disabled = disabled || d.readOnly || u.saving || u.saved || authorizing
	n.Row(c).FillWidth().Height(28).Gap(6).AlignItems(n.Baseline).Children(func() {
		if control(c, 5).Height(28).Label("返回 MCP 列表").Disabled(u.saving).Children(func() {
			inlineIconText(c, deviceBackIcon, "MCP", n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, 6, t.TextMuted)
		}).Clicked() {
			u.draft = nil
			if !u.saved {
				u.message = ""
			}
		}
		label := "添加服务"
		if d.id != "" {
			label = "编辑服务"
		}
		n.Text(c, label).FontSize(14).FontWeight(500).FixedLineHeight(20)
		n.Spacer(c).Grow(1)
		if toolServiceRefreshButton(c.Key("refresh"), "刷新 MCP 配置", u.phase == toolServiceLoading, u.saving || authorizing || v.Controller == nil).Clicked() {
			v.queryToolServices(u, u.catalog.CanRefresh)
		}
	})
	n.Scroll(c.Key("editor-scroll")).FillWidth().FillHeight().Grow(1).MinHeight(0).Padding(12, 0, 0).Label("MCP 配置内容").Children(func() {
		n.Column(c).FillWidth().Children(func() {
			deviceConfigRow(c, "名称", func() {
				field := configurationTextField(c, &d.config.Name).Label("MCP 名称").Disabled(disabled)
				if d.focus && !disabled {
					field.Focus()
					d.focus = false
				}
			})
			deviceConfigRow(c, "连接方式", func() {
				d.config.Transport = configurationTabs(c, d.config.Transport, []configurationTabItem{{ID: "command", Label: "本地命令", Icon: terminalIcon}, {ID: "http", Label: "HTTP", Icon: toolHTTPIcon}}, configurationSegmented, disabled)
			})
			configurationTabPanel(c.Key("transport-fields"), d.config.Transport, func() int {
				if d.config.Transport == "http" {
					return 1
				}
				return 0
			}(), configurationPanelFit, func() {
				if d.config.Transport == "command" {
					deviceConfigRow(c, "命令", func() {
						configurationTextField(c, &d.config.Command).Label("MCP 执行命令").Placeholder("npx").Disabled(disabled)
					})
					configurationFormRow(c, "参数", configurationDeviceForm, func() { v.toolArgumentFields(c, t, d, disabled) }, true)
				} else {
					deviceConfigRow(c, "服务地址", func() {
						configurationTextField(c, &d.config.URL).Label("MCP 服务地址").Placeholder("https://…/mcp").Disabled(disabled)
					})
					if !d.basic {
						deviceConfigRow(c, "令牌环境变量", func() {
							configurationTextField(c, &d.config.TokenEnvironment).Label("MCP 令牌环境变量").Placeholder("GITHUB_TOKEN").Disabled(disabled)
						})
					}
				}
			}, false)
			deviceDisclosure(c.Key("advanced"), "高级选项", &d.advanced, func() {
				if d.config.Transport == "command" {
					configurationFormRow(c, "环境变量", configurationDeviceForm, func() { v.toolValueFields(c, t, d, &d.environment, disabled) }, true)
					if !d.basic {
						deviceConfigRow(c, "继承环境变量", func() {
							configurationTextField(c, &d.inherited).Label("MCP 继承环境变量").Placeholder("PATH HOME").Disabled(disabled)
						})
					}
					deviceConfigRow(c, "工作目录", func() {
						configurationTextField(c, &d.config.Directory).Label("MCP 工作目录").Placeholder("Agent 默认目录").Disabled(disabled)
					})
				} else {
					configurationFormRow(c, "请求头", configurationDeviceForm, func() { v.toolValueFields(c, t, d, &d.headers, disabled) }, true)
				}
				if !d.basic {
					deviceConfigRow(c, "启动超时（秒）", func() { configurationTextField(c, &d.timeout).Label("MCP 启动超时").Disabled(disabled) })
				}
			})
			deviceConfigRow(c, "启用", func() {
				n.Row(c).FillWidth().Justify(n.End).Children(func() { configurationSwitch(c, &d.config.Enabled).Label("启用 MCP 服务").Disabled(disabled) })
			})
			if d.readOnly {
				toolConfigurationFeedback(c, t, d.reason)
			}
			v.toolConfigurationAuthorization(c, t, u, d, disabled)
			message := u.message
			if u.authorization != nil && u.authorization.ServiceID == d.id && u.authorization.Message != "" {
				message = u.authorization.Message
			}
			toolConfigurationFeedback(c.Key("edit-feedback"), t, message)
		})
	})
	n.Row(c.Key("footer")).FillWidth().Height(48).Gap(6).AlignItems(n.Baseline).Children(func() {
		caption := ""
		if u.saving {
			caption = "正在保存"
		}
		if caption != "" {
			serviceSpinner(c.Key("spinner"), caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
			n.Text(c.Key("caption"), caption).FontSize(12).FixedLineHeight(28).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		}
		n.Spacer(c).Grow(1)
		if configurationAction(c, "取消").Label("取消编辑 MCP").Disabled(u.saving).Clicked() {
			u.draft = nil
			if !u.saved {
				u.message = ""
			}
		}
		if !d.readOnly {
			label := "添加"
			if d.id != "" {
				label = "保存"
			}
			if configurationAction(c, label).Label("保存 MCP 服务").Disabled(disabled || v.Controller == nil).Clicked() {
				config, err := d.value()
				if err != nil {
					u.message = err.Error()
				} else {
					v.changeToolConfiguration(u, domain.ToolServiceChange{Action: "save", ServiceID: d.id, Revision: d.revision, Configuration: &config})
				}
			}
		}
	})
}

func (v *View) toolConfigurationAuthorization(c *n.Context, t *n.Theme, u *toolServicesUI, d *toolConfigurationDraft, disabled bool) {
	if d.id == "" || d.config.Transport != "http" {
		return
	}
	for _, service := range u.catalog.Services {
		if service.ID != d.id || !service.CanAuthorize {
			continue
		}
		deviceConfigRow(c, "授权", func() {
			n.Row(c).FillWidth().Height(28).Gap(6).AlignItems(n.Baseline).Children(func() {
				caption := toolServiceLabel(service)
				dirty := d.changed()
				if dirty {
					caption = "保存后授权"
				}
				active := u.authorization != nil && u.authorization.ServiceID == d.id && u.authorization.Phase.Active()
				if active {
					caption = "等待浏览器授权"
					serviceSpinner(c.Key("authorization-spinner"), caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
				}
				n.Text(c.Key("authorization-caption"), caption).FontSize(12).FixedLineHeight(28).Grow(1).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				if active {
					if u.authorization.Challenge.URL != "" && configurationAction(c, "重新打开").Clicked() {
						if openToolServiceLink(u.authorization.Challenge.URL) != nil {
							u.authorization.Message = "无法打开授权页"
						}
					}
				} else if configurationAction(c, "浏览器授权").Disabled(disabled || dirty || u.phase == toolServiceLoading).Clicked() {
					v.startToolAuthorization(u, service)
				}
			})
		})
	}
}
func toolDraftRow(c *n.Context, id int, added *bool, removed time.Time, build func()) {
	frame := n.Column(c.Key(id)).FillWidth().Clip()
	if *added {
		frame.Jump("height", 0)
		*added = false
	}
	h := float32(36)
	if !removed.IsZero() {
		h = 0
	}
	frame.Height(frame.AnimateTo("height", h, motionAnimation(c, 180*time.Millisecond)))
	n.Inert(frame, !removed.IsZero())
	frame.Children(func() {
		n.Row(c).Absolute().Top(0).Left(0).Right(0).Height(36).Gap(6).AlignItems(n.Center).Children(build)
	})
}
func (v *View) toolArgumentFields(c *n.Context, t *n.Theme, d *toolConfigurationDraft, disabled bool) {
	n.Column(c).FillWidth().Children(func() {
		for i := range d.arguments {
			entry := &d.arguments[i]
			if !entry.removed.IsZero() && c.Now().Sub(entry.removed) > 240*time.Millisecond {
				continue
			}
			toolDraftRow(c, entry.id, &entry.added, entry.removed, func() {
				configurationTextField(c, &entry.value).Grow(1).MinWidth(0).Label("MCP 参数 " + strconv.Itoa(i+1)).Disabled(disabled)
				if iconButton(c, closeIcon, "删除 MCP 参数 "+strconv.Itoa(i+1)).Disabled(disabled).Clicked() {
					entry.removed = c.Now()
				}
			})
		}
		if toolAddAction(c, "添加参数").Label("添加 MCP 参数").Disabled(disabled).Clicked() {
			d.nextID++
			d.arguments = append(d.arguments, toolArgumentDraft{id: d.nextID, added: true})
		}
	})
}
func (v *View) toolValueFields(c *n.Context, t *n.Theme, d *toolConfigurationDraft, entries *[]toolValueDraft, disabled bool) {
	n.Column(c).FillWidth().Children(func() {
		for i := range *entries {
			entry := &(*entries)[i]
			if !entry.removed.IsZero() && c.Now().Sub(entry.removed) > 240*time.Millisecond {
				continue
			}
			toolDraftRow(c, entry.id, &entry.added, entry.removed, func() {
				configurationTextField(c, &entry.Name).Grow(1).MinWidth(0).Label("MCP 变量名 " + strconv.Itoa(i+1)).Placeholder("名称").Disabled(disabled || entry.Retained)
				field := configurationTextField(c, &entry.Value).Grow(1).MinWidth(0).Label("MCP 变量值 " + strconv.Itoa(i+1)).Placeholder("值").Password().Disabled(disabled)
				if entry.Retained {
					field.Placeholder("已配置，留空保留")
				}
				if field.Changed() {
					entry.Retained = false
				}
				if iconButton(c, closeIcon, "删除 MCP 变量 "+strconv.Itoa(i+1)).Disabled(disabled).Clicked() {
					entry.removed = c.Now()
				}
			})
		}
		if toolAddAction(c, "添加一项").Label("添加 MCP 变量").Disabled(disabled).Clicked() {
			d.nextID++
			*entries = append(*entries, toolValueDraft{id: d.nextID, added: true})
		}
	})
}

func toolAddAction(c *n.Context, label string) n.Element {
	font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
	width, _ := n.ShapeText(label, font, 0).Size()
	return control(c, 5).Width(width + 20).Height(28).Justify(n.Start).Children(func() { inlineIconText(c, plus, label, font, 14, 6, c.Theme().TextMuted) })
}

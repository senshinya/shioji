package ui

import (
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var modelEye = svg("M2 10c4-7 12-7 16 0-4 7-12 7-16 0zM12 10a2 2 0 1 1-4 0 2 2 0 0 1 4 0")

type runtimeOptionDraft struct {
	arguments, directory, instructions, titleModel string
	environment                                    []runtimeEnvironmentDraft
	nextID                                         int
	advanced, login                                bool
	copiedUntil                                    time.Time
}

type runtimeEnvironmentDraft struct {
	domain.EnvironmentVariable
	id            int
	reveal, added bool
	removed       time.Time
}

func newRuntimeOptionDraft(o domain.RuntimeOptions) runtimeOptionDraft {
	args := []string{}
	for _, arg := range o.Arguments {
		if strings.ContainsAny(arg, " \t\"'\\") {
			arg = strconv.Quote(arg)
		}
		args = append(args, arg)
	}
	d := runtimeOptionDraft{arguments: strings.Join(args, " "), directory: o.Directory, instructions: o.Instructions, titleModel: o.TitleModel}
	for _, entry := range o.Environment {
		d.nextID++
		d.environment = append(d.environment, runtimeEnvironmentDraft{EnvironmentVariable: entry, id: d.nextID})
	}
	return d
}

func parseRuntimeArguments(text string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range text {
		if escaped {
			word.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if unicode.IsSpace(r) {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(r)
		started = true
	}
	if quote != 0 || escaped {
		return nil, errors.New("启动参数的引号或转义尚未结束")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}
func (d runtimeOptionDraft) options() (domain.RuntimeOptions, error) {
	args, err := parseRuntimeArguments(d.arguments)
	environment := []domain.EnvironmentVariable{}
	for _, entry := range d.environment {
		if entry.removed.IsZero() {
			environment = append(environment, entry.EnvironmentVariable)
		}
	}
	return domain.RuntimeOptions{Arguments: args, Directory: d.directory, Instructions: d.instructions, TitleModel: d.titleModel, Environment: environment}, err
}
func deviceConfigRow(c *n.Context, label string, content func()) {
	width, _ := c.Size()
	controlWidth := min(float32(320), (width-296)*.55)
	if width < 900 {
		controlWidth = min(320, (width-64)*.55)
	}
	n.Row(c).FillWidth().MinHeight(40).Gap(24).AlignItems(n.Center).Children(func() {
		n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(c.Theme().TextMuted).Grow(1)
		n.Column(c).Width(controlWidth).Children(content)
	})
}
func deviceConfigTitle(c *n.Context, title string) {
	n.Text(c, title).FontSize(14).FontWeight(500).FixedLineHeight(28).Padding(0, 0, 8)
}
func (e *deviceAgentEditor) edit(info domain.HarnessInfo) {
	e.openID = info.ID
	e.path = e.paths[info.ID]
	e.draft = newRuntimeOptionDraft(e.options[info.ID])
	e.error = ""
}
func (v *View) agentConfiguration(c *n.Context, t *n.Theme, key string, connected bool, e *deviceAgentEditor, info domain.HarnessInfo, onboarding bool, save func()) {
	info = v.signInProfileInfo(key, e, info)
	install := info.Installation
	row := deviceConfigRow
	if e.setup {
		row = setupRow
	}
	minor := v.deviceState(key).WorkerMinor
	if e.setup {
		minor = e.workerMinor
	}
	background := t.Surface
	if e.setup {
		background = t.Background
	}
	sectionInset := float32(24)
	if e.setup {
		sectionInset = 0
	}
	disabled := e.readOnly || e.busy || e.loading || !connected
	n.Column(c).FillWidth().Gap(0).Children(func() {
		if !e.setup || e.tab == "常规" {
			if !e.setup {
				deviceConfigTitle(c, "常规")
			}
			row(c, "账号", func() {
				label, status := "登录状态未报告", ""
				if account := info.Account; account != nil {
					label = account.Label
					if account.State == "authenticated" {
						status = account.Subscription
					}
				}
				if install != nil && (install.Issue == domain.RuntimeSignInRequired || install.Issue == domain.RuntimeAuthenticationExpired) {
					label = runtimeSetupLabel(info, connected)
				}
				trigger := control(c.Key("account"), 5).FillWidth().Height(28).PaddingX(8).Radius(5).Label("管理账号").Disabled(install == nil || len(install.SignInCommand) == 0 && len(install.SignInMethods) == 0 || info.Account != nil && info.Account.State == "notRequired").Children(func() {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						n.Text(c, label).FontSize(12).FixedLineHeight(20).SingleLine().Shrink(1).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						if status != "" {
							size := float32(11)
							if e.setup {
								size = 12
							}
							n.Text(c, status).SingleLine().MaxWidth(120).Tooltip(status).FontSize(size).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						}
						n.Spacer(c).Grow(1)
						angle := float32(-90)
						if e.draft.login {
							angle = 0
						}
						inlineSymbol(c, chevron, label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.TextMuted, angle)
					})
				})
				if trigger.Clicked() {
					e.draft.login = !e.draft.login
				}
			})
			frame := n.Column(c.Key("login")).FillWidth().Clip()
			animatedContentHeight(c, frame, e.draft.login, 240*time.Millisecond, func() float32 {
				row(c, "", func() {
					top, bottom := float32(4), float32(8)
					if e.setup {
						top, bottom = 0, 0
					}
					n.Column(c).FillWidth().Padding(top, 0, bottom, 8).Children(func() {
						v.agentAuthentication(c, t, key, e, info, disabled)
					})
				})
				return 0
			})

			row(c, "可执行文件", func() {
				placeholder := "自动检测"
				if install != nil && install.Executable != "" {
					placeholder = install.Executable
				}
				field := textField(c.Key("executable"), &e.path).Background(t.Background).FillWidth().Height(28).FontSize(12).Label(info.Name + " 可执行文件").Placeholder(placeholder).Tooltip("留空时在所属设备自动检测").Disabled(disabled)
				if e.setup {
					field.Padding(4, 8).Background(t.Background)
				}
			})
			if install != nil {
				guidance := n.Column(c.Key("login-guidance")).FillWidth().Clip()
				visible := install.Issue != "" && !e.draft.login
				last := n.Local(guidance, "info", func() domain.HarnessInfo { return info })
				if visible {
					*last = info
				}
				animatedContentHeight(c, guidance, visible, 240*time.Millisecond, func() float32 {
					previous := *last
					installation := previous.Installation
					if (installation.Issue == domain.RuntimeSignInRequired || installation.Issue == domain.RuntimeAuthenticationExpired) && len(installation.SignInMethods) > 0 && minor >= 19 {
						n.Row(c).FillWidth().Justify(n.End).PaddingY(8).Children(func() {
							if deviceAction(c, "登录账号").Disabled(disabled).Clicked() {
								e.draft.login = true
							}
						})
					} else {
						n.Column(c).FillWidth().PaddingY(8).Children(func() {
							n.Text(c, installation.Guide).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
							v.agentGuidance(c, key, previous)
						})
					}
					return 0
				})
			}
		}
		if (!onboarding || e.setup) && (!e.setup || e.tab == "模型") && len(info.Models) > 0 {
			n.Column(c).FillWidth().Padding(sectionInset, 0, 0).Children(func() {
				if !e.setup {
					v.deviceModels(c.Key("models"), t, key, info)
				}
				if install != nil && install.OptionsSupported.TitleModel && minor >= 18 {
					row(c, "标题生成", func() {
						name := "自动"
						for _, model := range info.Models {
							if model.ID == e.draft.titleModel {
								name = modelName(model)
							}
						}
						if e.draft.titleModel != "" && name == "自动" {
							name = e.draft.titleModel
						}
						trigger := control(c, 5).FillWidth().Height(28).PaddingX(8).Radius(5).Border(1, t.Border).Label("标题生成").Disabled(disabled).Children(func() {
							n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
								n.Text(c, name).SingleLine().FontSize(12).FixedLineHeight(20).Grow(1).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
								inlineSymbol(c, chevron, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.TextMuted)
							})
						})
						choices := []menuChoice{{id: "", name: "自动", choose: func() { e.draft.titleModel = "" }}}
						for _, model := range info.Models {
							choices = append(choices, menuChoice{id: model.ID, name: modelName(model), choose: func() { e.draft.titleModel = model.ID }})
						}
						v.choiceMenu(c, t, "title-model:"+key+":"+info.ID, trigger, choices)
					})
				}
				if e.setup {
					deviceID := v.Model.ConnectionDeviceID(key)
					if key == "" {
						deviceID = v.sshSetup.Info.DeviceID
					}
					v.deviceModels(c.Key("models"), t, key, info, deviceID)
				}
			})
		}
		supported := domain.RuntimeOptionSupport{}
		if install != nil {
			supported = install.OptionsSupported
		}
		if (!e.setup || e.tab == "高级") && minor >= 18 && (supported.Arguments || supported.Directory || supported.Environment || supported.Instructions) {
			n.Column(c).FillWidth().Padding(sectionInset, 0, 0).Children(func() {
				if !e.setup {
					heading := control(c.Key("advanced-toggle"), 5).FillWidth().Height(28).Label("高级").Children(func() {
						n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
							n.Text(c, "高级").FontSize(14).FontWeight(500).FixedLineHeight(20).Grow(1)
							angle := float32(-90)
							if e.draft.advanced {
								angle = 0
							}
							inlineSymbol(c, chevron, "高级", n.Font{Family: t.Font, Size: 14, Weight: 500}, 12, t.TextMuted, angle)
						})
					})
					if heading.Clicked() {
						e.draft.advanced = !e.draft.advanced
					}
				}
				body := n.Column(c.Key("advanced-body")).FillWidth().Clip()
				animatedContentHeight(c, body, e.setup || e.draft.advanced, 240*time.Millisecond, func() float32 {
					padding := float32(8)
					if e.setup {
						padding = 0
					}
					n.Column(c).FillWidth().Padding(padding, 0, 0).Children(func() {
						if supported.Arguments {
							row(c, "启动参数", func() {
								label := "启动参数"
								if e.setup {
									label = "启动参数输入"
								}
								field := textField(c.Key("arguments"), &e.draft.arguments).FillWidth().Height(28).FontSize(12).Label(label).Placeholder("默认").Disabled(disabled)
								if e.setup {
									field.Padding(4, 8).Background(t.Background)
								}
							})
						}
						if supported.Directory {
							row(c, "配置目录", func() {
								field := textField(c.Key("config-directory"), &e.draft.directory).FillWidth().Height(28).FontSize(12).Label("配置目录").Placeholder("默认").Disabled(disabled)
								if e.setup {
									field.Padding(4, 8).Background(t.Background)
								}
							})
						}
						if supported.Environment {
							row(c, "环境变量", func() { v.runtimeEnvironmentFields(c, t, e, disabled) })
						}
						if supported.Instructions {
							row(c, "自定义提示词", func() {
								n.TextAreaBase(c.Key("instructions"), &e.draft.instructions).FillWidth().Lines(3, 6).FontSize(12).FixedLineHeight(18).Padding(6, 9).Radius(5).Background(background).Border(1, t.Border).Label("自定义提示词").Placeholder("可选的默认提示词").Disabled(disabled)
							})
						}
					})
					return 0
				})
			})
		}
		if e.setup {
			return
		}
		changed := e.path != e.paths[info.ID]
		options, err := e.draft.options()
		saved := e.options[info.ID]
		if err != nil || !runtimeOptionsEqual(options, saved) {
			changed = true
		}
		actions := n.Column(c.Key("save-actions")).FillWidth().Clip()
		animatedContentHeight(c, actions, changed, 180*time.Millisecond, func() float32 {
			n.Row(c).FillWidth().Height(36).Justify(n.End).Gap(6).Children(func() {
				if deviceAction(c, "取消").Disabled(e.busy).Clicked() {
					advanced, login := e.draft.advanced, e.draft.login
					e.edit(info)
					e.draft.advanced, e.draft.login = advanced, login
				}
				if deviceAction(c, "保存").Label("保存 " + info.Name + " 路径").Disabled(disabled).Clicked() {
					if err != nil {
						e.error = err.Error()
						return
					}
					e.paths[info.ID] = e.path
					if install != nil && minor >= 18 {
						e.options[info.ID] = options
					}
					if onboarding {
						e.openID = ""
					} else {
						if info.Configured {
							save()
						} else {
							v.saveDisabledAgentSettings(key, e, info.ID)
						}
					}
				}
			})
			return 0
		})
	})
}
func runtimeOptionsEqual(a, b domain.RuntimeOptions) bool {
	if a.Directory != b.Directory || a.Instructions != b.Instructions || a.TitleModel != b.TitleModel || len(a.Arguments) != len(b.Arguments) || len(a.Environment) != len(b.Environment) {
		return false
	}
	for i := range a.Arguments {
		if a.Arguments[i] != b.Arguments[i] {
			return false
		}
	}
	for i := range a.Environment {
		if a.Environment[i] != b.Environment[i] {
			return false
		}
	}
	return true
}

func (v *View) runtimeEnvironmentFields(c *n.Context, t *n.Theme, e *deviceAgentEditor, disabled bool) {
	fontSize := float32(11)
	background := t.Surface
	if e.setup {
		fontSize = 12
		background = t.Background
	}
	n.Column(c).FillWidth().Children(func() {
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			if deviceAction(c, "添加变量").PaddingX(0).Disabled(disabled).Clicked() {
				e.draft.nextID++
				e.draft.environment = append(e.draft.environment, runtimeEnvironmentDraft{id: e.draft.nextID, added: true})
			}
		})
		for index := range e.draft.environment {
			entry := &e.draft.environment[index]
			if !entry.removed.IsZero() && c.Now().Sub(entry.removed) > 240*time.Millisecond {
				continue
			}
			frame := n.Column(c.Key(entry.id)).FillWidth().Clip()
			if entry.added {
				frame.Jump("row-height", 0)
				entry.added = false
			}
			target := float32(36)
			if !entry.removed.IsZero() {
				target = 0
			}
			frame.Height(frame.AnimateTo("row-height", target, motionAnimation(c, 180*time.Millisecond)))
			n.Inert(frame, !entry.removed.IsZero())
			frame.Children(func() {
				n.Row(c).Absolute().Top(0).Left(0).Right(0).Height(36).Gap(4).AlignItems(n.Center).Children(func() {
					textField(c, &entry.Name).MinWidth(0).Grow(1).Height(28).FontSize(fontSize).Background(background).Label("环境变量名").Placeholder("变量名").Disabled(disabled)
					value := textField(c, &entry.Value).MinWidth(0).Grow(1).Height(28).FontSize(fontSize).Background(background).Label("环境变量值").Placeholder("值").Disabled(disabled)
					if !entry.reveal {
						value.Password()
					}
					if iconButton(c, modelEye, "显示环境变量值").Size(24, 28).Disabled(disabled).Clicked() {
						entry.reveal = !entry.reveal
					}
					if iconButton(c, closeIcon, "删除环境变量").Size(24, 28).Disabled(disabled).Clicked() {
						entry.removed = c.Now()
					}
				})
			})
		}
	})
}

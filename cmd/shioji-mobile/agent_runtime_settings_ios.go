package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileRuntimeVariable struct {
	id         int
	name       string
	previous   string
	value      *string
	needsValue bool
}

type mobileAgentSettingsDraft struct {
	base      protocol.RuntimeSettings
	options   domain.RuntimeOptions
	path      string
	arguments string
	variables []*mobileRuntimeVariable
	nextID    int
	stale     bool
	saved     bool
}

func (d *mobileAgentSettingsDraft) accept(settings protocol.RuntimeSettings) {
	*d = mobileAgentSettingsDraft{base: settings, path: settings.Override, options: settings.Options,
		arguments: app.FormatRuntimeArguments(settings.Options.Arguments)}
	for _, name := range settings.Environment {
		d.nextID++
		d.variables = append(d.variables, &mobileRuntimeVariable{id: d.nextID, name: name, previous: name})
	}
}

func (d *mobileAgentSettingsDraft) changed() bool {
	if strings.TrimSpace(d.path) != d.base.Override || d.arguments != app.FormatRuntimeArguments(d.base.Options.Arguments) ||
		d.options.Directory != d.base.Options.Directory || d.options.Instructions != d.base.Options.Instructions ||
		d.options.TitleModel != d.base.Options.TitleModel || len(d.variables) != len(d.base.Environment) {
		return true
	}
	for i, v := range d.variables {
		if v.name != d.base.Environment[i] || v.previous != d.base.Environment[i] || v.value != nil || v.needsValue {
			return true
		}
	}
	return false
}

func (d *mobileAgentSettingsDraft) clearPrivateValues() {
	for _, v := range d.variables {
		if v.value != nil {
			v.value = nil
			v.needsValue = true
		}
	}
}

func (d *mobileAgentSettingsDraft) input(harnessID string) (protocol.RuntimeSettingsChange, error) {
	options := d.options
	if d.base.Support.Arguments {
		args, err := app.ParseRuntimeArguments(d.arguments)
		if err != nil {
			return protocol.RuntimeSettingsChange{}, err
		}
		options.Arguments = args
	}
	in := protocol.RuntimeSettingsChange{HarnessID: harnessID, Revision: d.base.Revision, Executable: strings.TrimSpace(d.path), Options: options}
	for _, v := range d.variables {
		if v.needsValue {
			return protocol.RuntimeSettingsChange{}, fmt.Errorf("请重新填写 %s 的值", v.name)
		}
		in.Environment = append(in.Environment, protocol.RuntimeVariableChange{Name: v.name, PreviousName: v.previous, Value: v.value})
	}
	return in, nil
}

func (d *mobileAgentSettingsDraft) titleModel(c *ui.Context, models []domain.Model, disabled bool) {
	value := "自动"
	if d.options.TitleModel != "" {
		value = d.options.TitleModel
		for _, model := range models {
			if model.ID == d.options.TitleModel && model.Name != "" {
				value = model.Name
			}
		}
	}
	formChoice(c, "标题生成", value, func(menu *ui.Menu) {
		menuChoice(menu, "自动", d.options.TitleModel == "", func() { d.options.TitleModel = "" })
		for _, model := range models {
			name := model.Name
			if name == "" {
				name = model.ID
			}
			menuChoice(menu, name, d.options.TitleModel == model.ID, func() { d.options.TitleModel = model.ID })
		}
	}).Disabled(disabled)
}

func (a *mobileApp) runtimeSettingsFields(c *ui.Context, w *mygo.Window, harnessID string, d *mobileAgentSettingsDraft, disabled bool) {
	supported := d.base.Support
	if supported.Arguments {
		formField(c, "启动参数", "默认", &d.arguments).Disabled(disabled)
	}
	if supported.Directory {
		formField(c, "配置目录", "默认", &d.options.Directory).Disabled(disabled)
	}
	if supported.Instructions {
		mobileGroupHeading(c, "自定义提示词")
		mobileTextArea(c, &d.options.Instructions, "自定义提示词输入").Placeholder("可选的默认提示词").MinHeight(96).Disabled(disabled)
	}
	if supported.Environment {
		ui.Row(c.Key("environment-heading")).Margin(17, 0, 0).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Children(func() { mobileSection(c, "环境变量") })
			action(c, "添加变量", func() { a.openRuntimeVariable(w, harnessID, d, nil) }).PaddingX(0).Disabled(disabled)
		})
		for _, variable := range d.variables {
			status := "已保存"
			if variable.needsValue {
				status = "请重新填写值"
			} else if variable.value != nil || variable.previous != variable.name {
				status = "待保存"
			}
			rowDivider(mobileDetailRow(c.Key(variable.id), variable.name, "", variable.name, nil,
				func() { a.openRuntimeVariable(w, harnessID, d, variable) }, func() {
					oneLine(c, status, captionTextSize, palette(c).secondary).PassThrough()
				}), palette(c).line).Disabled(disabled)
		}
	}
}

func (a *mobileApp) openRuntimeVariable(parent *mygo.Window, harnessID string, draft *mobileAgentSettingsDraft, target *mobileRuntimeVariable) {
	if !a.pageCurrent(parent) {
		return
	}
	var edit mobileRuntimeVariable
	if target != nil {
		edit = *target
	}
	value, revealed := "", ""
	editingValue, showValue := edit.value != nil, false
	if editingValue {
		value = *edit.value
	}
	req := mobileRequest{}
	var w *mygo.Window
	complete := func() {
		name := strings.TrimSpace(edit.name)
		if name == "" {
			req.err = "请填写变量名"
			w.Invalidate()
			return
		}
		if slices.ContainsFunc(draft.variables, func(other *mobileRuntimeVariable) bool { return other != target && other.name == name }) {
			req.err = "环境变量名称重复"
			w.Invalidate()
			return
		}
		edit.name = name
		if editingValue || target == nil {
			next := value
			edit.value, edit.needsValue = &next, false
		}
		if target == nil {
			draft.nextID++
			edit.id = draft.nextID
			draft.variables = append(draft.variables, &edit)
		} else {
			*target = edit
		}
		parent.Invalidate()
		w.Pop()
	}
	w = a.page("环境变量", func(c *ui.Context) {
		mobilePageBody(c, func() {
			formField(c, "变量名", "", &edit.name).Disabled(req.busy)
			formRow(c, "变量值", func() {
				placeholder := "可留空"
				if !editingValue && target != nil {
					placeholder = "保留已保存的值"
					if edit.needsValue {
						placeholder = "请重新填写值"
					}
				}
				visible := &value
				if showValue && !editingValue && edit.previous != "" {
					visible = &revealed
				}
				field := formInput(c, "变量值", placeholder, visible).Disabled(req.busy)
				if !showValue {
					field.Password()
				}
				if field.Changed() {
					editingValue = true
					if visible == &revealed {
						value, revealed = revealed, ""
					}
				}
			})
			ui.Row(c).Wrap().Gap(4).Children(func() {
				title := "显示变量值"
				if showValue {
					title = "隐藏变量值"
				}
				action(c, title, func() {
					if showValue || editingValue || edit.previous == "" {
						showValue = !showValue
						revealed = ""
						return
					}
					input := protocol.RuntimeSettingsQuery{HarnessID: harnessID, Revision: draft.base.Revision, Name: edit.previous}
					a.pageJob(w, &req, "runtime.settings.environment", input, decodePage(func(result protocol.RuntimePrivateValue) error {
						revealed, showValue = result.Value, true
						return nil
					}))
				}).Disabled(req.busy || !showValue && !editingValue && edit.previous != "" && !a.model.Connected)
				if target != nil {
					action(c, "清空变量值", func() {
						value, revealed, editingValue, showValue = "", "", true, false
					}).Disabled(req.busy)
					action(c, "删除变量", func() {
						draft.variables = slices.DeleteFunc(draft.variables, func(other *mobileRuntimeVariable) bool { return other == target })
						parent.Invalidate()
						w.Pop()
					}).Disabled(req.busy)
				}
			})
			req.status(c)
		})
	})
	w.OnClosed(func() { value, revealed = "", "" })
	a.pushWithCommit(parent, w, "完成", func() {
		if !req.busy && a.pageCurrent(w) {
			complete()
		}
	})
}

func (a *mobileApp) saveRuntimeSettings(w *mygo.Window, req *mobileRequest, harnessID string, draft *mobileAgentSettingsDraft, accepted func(protocol.RuntimeSettings)) {
	in, err := draft.input(harnessID)
	if err != nil {
		req.err = err.Error()
		w.Invalidate()
		return
	}
	epoch := a.model.ConnectionEpoch
	a.runPageJobResult(nil, w, req, "runtime.settings.change", in, nil, func(raw json.RawMessage, err error) error {
		if a.model.ConnectionEpoch != epoch {
			return nil
		}
		var remote *protocol.RemoteError
		if errors.As(err, &remote) && remote.Code == "runtimeSettingsChanged" {
			draft.stale = true
		}
		if err != nil {
			return err
		}
		var next protocol.RuntimeSettings
		if err = json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if next.Revision == "" {
			return fmt.Errorf("设备未返回保存后的运行设置")
		}
		accepted(next)
		draft.saved = true
		return nil
	})
}

func redactRuntimeEnvironment(info domain.HarnessInfo) domain.HarnessInfo {
	if info.Installation != nil && len(info.Installation.Options.Environment) > 0 {
		install := *info.Installation
		install.Options.Environment = nil
		info.Installation = &install
	}
	return info
}

func redactRuntimeReadiness(ready domain.Readiness) domain.Readiness {
	for _, info := range ready.Harnesses {
		if info.Installation != nil && len(info.Installation.Options.Environment) > 0 {
			ready.Harnesses = slices.Clone(ready.Harnesses)
			for i, agent := range ready.Harnesses {
				ready.Harnesses[i] = redactRuntimeEnvironment(agent)
			}
			break
		}
	}
	for _, info := range ready.Sessions {
		if info.Installation != nil && len(info.Installation.Options.Environment) > 0 {
			ready.Sessions = maps.Clone(ready.Sessions)
			for sid, agent := range ready.Sessions {
				ready.Sessions[sid] = redactRuntimeEnvironment(agent)
			}
			break
		}
	}
	return ready
}

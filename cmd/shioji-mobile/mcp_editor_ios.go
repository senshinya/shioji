package main

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

type mobileToolEditor struct {
	id, revision, reason  string
	message, timeout      string
	inherited             string
	config                domain.ToolServiceConfiguration
	original              *domain.ToolServiceConfiguration
	args                  []*string
	env, headers          []*mobileToolValue
	readOnly, basic       bool
	advanced, confirmDrop bool
}

type mobileToolValue struct {
	domain.ToolServiceValue
	replace bool
}

func newMobileToolEditor(catalog domain.ToolServiceCatalog, service *domain.ToolService) *mobileToolEditor {
	d := &mobileToolEditor{revision: catalog.Revision, basic: catalog.BasicEditing, config: domain.ToolServiceConfiguration{Transport: "command", Enabled: true}}
	if service != nil {
		d.id, d.readOnly, d.reason = service.ID, !service.Editable, service.ReadOnlyReason
		d.config = *service.Configuration
		for _, arg := range d.config.Arguments {
			d.args = append(d.args, new(string(arg)))
		}
		copyPublicNames := func(values []domain.ToolServiceValue) []*mobileToolValue {
			var rows []*mobileToolValue
			for _, value := range values {
				rows = append(rows, &mobileToolValue{ToolServiceValue: domain.ToolServiceValue{Name: value.Name, Retained: true}})
			}
			return rows
		}
		d.env, d.headers = copyPublicNames(d.config.Environment), copyPublicNames(d.config.Headers)
		d.inherited = strings.Join(d.config.InheritedEnvironment, " ")
		d.config.Arguments, d.config.Environment, d.config.Headers = nil, nil, nil
	}
	if d.config.StartupSeconds > 0 {
		d.timeout = strconv.FormatFloat(d.config.StartupSeconds, 'f', -1, 64)
	}
	if change, err := d.change(); err == nil {
		d.original = change.Configuration
	}
	return d
}

func (s *mobileToolServices) discardDraft() {
	if s.draft != nil {
		s.draft.clearSecrets()
	}
	s.draft = nil
}

func (d *mobileToolEditor) clearSecrets() {
	for _, rows := range [][]*mobileToolValue{d.env, d.headers} {
		for _, row := range rows {
			row.Value = ""
		}
	}
	d.confirmDrop = false
}

func (d *mobileToolEditor) change() (domain.ToolServiceChange, error) {
	config := d.config
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return domain.ToolServiceChange{}, errors.New("请输入服务名称")
	}
	if d.timeout != "" {
		timeout, err := strconv.ParseFloat(strings.TrimSpace(d.timeout), 64)
		if err != nil || math.IsNaN(timeout) || timeout < 0 || timeout > 600 {
			return domain.ToolServiceChange{}, errors.New("启动超时须在 0 到 600 秒之间")
		}
		config.StartupSeconds = timeout
	} else {
		config.StartupSeconds = 0
	}
	values := func(rows []*mobileToolValue) []domain.ToolServiceValue {
		var out []domain.ToolServiceValue
		for _, entry := range rows {
			value := entry.ToolServiceValue
			if entry.replace && value.Value != "" {
				value.Retained = false
			}
			out = append(out, value)
		}
		return out
	}
	if config.Transport == "command" {
		if strings.TrimSpace(config.Command) == "" {
			return domain.ToolServiceChange{}, errors.New("请输入执行命令")
		}
		for _, arg := range d.args {
			config.Arguments = append(config.Arguments, *arg)
		}
		config.Environment, config.InheritedEnvironment = values(d.env), strings.Fields(d.inherited)
		config.URL, config.TokenEnvironment = "", ""
	} else {
		config.URL = strings.TrimSpace(config.URL)
		u, err := url.Parse(config.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "https" && u.Scheme != "http") {
			return domain.ToolServiceChange{}, errors.New("请输入不含凭据的 HTTP 或 HTTPS 地址")
		}
		config.Headers = values(d.headers)
		config.Command, config.Directory, config.InheritedEnvironment = "", "", nil
	}
	return domain.ToolServiceChange{Action: "save", ServiceID: d.id, Revision: d.revision, Configuration: &config}, nil
}

func (s *mobileToolServices) editor(a *mobileApp, c *ui.Context, w *mygo.Window) {
	d := s.draft
	if d == nil {
		return
	}
	p := palette(c)
	stale := s.loaded && d.revision != s.catalog.Revision
	writable := !d.readOnly && !stale && s.authorizedWrite(a, w)
	title := "添加服务"
	if d.id != "" {
		title = "编辑服务"
	}
	mobileContent(c.Key("mcp-editor"), contentMaxWidth).FillHeight().Padding(0, pageInset, 0).
		Transition(mobileHorizontalTransition(100)).
		Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				iconButton(c, "返回 MCP 列表", backSymbol, 16, s.discardDraft).KeepFocus().Disabled(s.write.busy)
				label(c, title, 15, p.ink).FontWeight(620).Grow(1)
				if d.id != "" && !d.readOnly {
					action(c, "删除 MCP 服务", func() { d.confirmDrop = true }).KeepFocus().Disabled(!writable)
				}
			})
			ui.Scroll(c.Key("mcp-editor-scroll")).Grow(1).Basis(0).MinHeight(0).Padding(0, 0, pageInset).Label("MCP 配置内容").Children(func() {
				editable := !d.readOnly && !s.write.busy
				selection := "远程命令"
				if d.config.Transport == "http" {
					selection = "HTTP"
				}
				ui.Column(c).Padding(8, 0).Children(func() {
					mobileSegments(c, []mobileSegment{{title: "远程命令", disabled: !editable}, {title: "HTTP", disabled: !editable}}, selection, func(value string) {
						d.config.Transport = "command"
						if value == "HTTP" {
							d.config.Transport = "http"
						}
					})
				})
				toolField(c, "名称", "MCP 名称", "", &d.config.Name, editable)
				if d.config.Transport == "command" {
					toolField(c, "命令", "MCP 执行命令", "", &d.config.Command, editable)
					d.arguments(c, editable)
				} else {
					toolField(c, "服务地址", "MCP 服务地址", "https://…/mcp", &d.config.URL, editable)
					if !d.basic {
						toolField(c, "令牌环境变量", "MCP 令牌环境变量", "可选", &d.config.TokenEnvironment, editable)
					}
				}
				arrow := rightSymbol
				if d.advanced {
					arrow = downSymbol
				}
				iconAction(c, "高级选项", "高级选项", nil, 0, arrow, 12, 14, 400, func() { d.advanced = !d.advanced }).
					AlignSelf(ui.Start).PaddingX(0).KeepFocus()
				if d.advanced {
					ui.Column(c.Key("mcp-advanced")).Transition(mobilePanelTransition).Children(func() {
						if d.config.Transport == "command" {
							toolValueRows(c.Key("env"), "环境变量", &d.env, editable)
							if !d.basic {
								toolField(c, "继承环境变量", "MCP 继承环境变量", "PATH HOME", &d.inherited, editable)
							}
							toolField(c, "工作目录", "MCP 工作目录", "Agent 默认目录", &d.config.Directory, editable)
						} else {
							toolValueRows(c.Key("headers"), "请求头", &d.headers, editable)
						}
						if !d.basic {
							toolField(c, "启动超时（秒）", "MCP 启动超时", "默认", &d.timeout, editable)
						}
					})
				}
				formRow(c, "启用", func() { mobileSwitch(c, "启用 MCP 服务", &d.config.Enabled, nil).Disabled(!editable) })
				if d.readOnly && d.reason != "" {
					mobileMessage(c, d.reason)
				}
				if d.id != "" && d.config.Transport == "http" {
					s.editorAuthorization(a, c, w, d)
				}
			})
			if d.message != "" {
				label(c, d.message, 13, p.warning).Padding(4, 0)
			}
			if stale {
				mobileMessage(c, "配置已变化，请返回列表后重新打开")
			}
			s.feedback(c, a.model.Connected)
			ui.Row(c.Key("mcp-footer")).Wrap().MinHeight(mobileTapSize(c)).Padding(4, 0).AlignItems(ui.Center).Gap(8).Children(func() {
				if d.confirmDrop && writable {
					label(c, "删除 "+d.config.Name+"？", 13, p.ink).Grow(1)
					action(c, "取消", func() { d.confirmDrop = false }).KeepFocus().PaddingX(6)
					primaryAction(c, "确认删除 MCP", func() {
						s.change(a, w, domain.ToolServiceChange{Action: "delete", ServiceID: d.id, Revision: d.revision})
					}).KeepFocus()
					return
				}
				if s.needsCheck || s.read.err != "" {
					action(c, "刷新核对", func() { s.load(a, w, false) }).Disabled(!s.available(a, w) || s.write.busy || s.read.busy)
				}
				ui.Spacer(c).Grow(1)
				action(c, "取消编辑 MCP", s.discardDraft).KeepFocus().Disabled(s.write.busy)
				if !d.readOnly {
					primaryAction(c, "保存 MCP 服务", func() {
						change, err := d.change()
						if err != nil {
							d.message = err.Error()
							return
						}
						d.message = ""
						s.change(a, w, change)
					}).KeepFocus().Disabled(!writable)
				}
			})
		})
}

func (s *mobileToolServices) editorAuthorization(a *mobileApp, c *ui.Context, w *mygo.Window, d *mobileToolEditor) {
	for _, service := range s.catalog.Services {
		if service.ID != d.id || !service.CanAuthorize || service.State == domain.ToolServiceDisabled {
			continue
		}
		status := s.catalog.Authorization
		if status != nil && status.ServiceID == d.id && (status.Phase.Active() || status.Message != "") {
			s.authorizationView(a, c, w, status)
		}
		if s.authorizationActive() {
			return
		}
		change, err := d.change()
		dirty := err != nil || !reflect.DeepEqual(change.Configuration, d.original)
		mobileRow(c, "授权", "", mobileToolLabel(service), nil, nil)
		if dirty {
			mobileMessage(c, "保存后授权")
		} else {
			action(c, "授权 "+service.Name, func() { s.startAuthorization(a, w, d.id) }).AlignSelf(ui.Start).
				Disabled(!s.available(a, w) || s.read.busy || s.write.busy || s.needsCheck || s.authRequest.busy)
		}
	}
}

func toolField(c *ui.Context, title, access, placeholder string, value *string, editable bool) {
	formRow(c, title, func() {
		formInput(c, access, placeholder, value).Autocorrect(false).Capitalize(ui.CapitalizeNone).
			ReturnKey(ui.ReturnNext).Disabled(!editable)
	})
}

func (d *mobileToolEditor) arguments(c *ui.Context, editable bool) {
	if len(d.args) > 0 {
		mobileSection(c, "参数")
	}
	for i, arg := range d.args {
		row := c.Key(arg)
		ui.Row(row).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(6).Children(func() {
			toolRowField(row, "MCP 参数 "+strconv.Itoa(i+1), "", arg, editable)
			iconButton(row, "移除参数 "+strconv.Itoa(i+1), closeSymbol, 14, func() { d.args = slices.Delete(d.args, i, i+1) }).
				Disabled(!editable).KeepFocus()
		})
	}
	action(c, "添加参数", func() { d.args = append(d.args, new(string)) }).AlignSelf(ui.Start).PaddingX(0).KeepFocus().Disabled(!editable)
}

func toolValueRows(c *ui.Context, title string, rows *[]*mobileToolValue, editable bool) {
	p := palette(c)
	if len(*rows) > 0 {
		mobileSection(c, title)
	}
	for i, item := range *rows {
		row := c.Key(item)
		ui.Row(row).Wrap().MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(6).Children(func() {
			name := fmt.Sprintf("%s %d", title, i+1)
			if item.Retained {
				oneLine(row, item.Name, 14, p.ink).Grow(1)
			} else {
				toolRowField(row, name+" 名称", "名称", &item.Name, editable).MinWidth(130)
			}
			if item.Retained && !item.replace {
				oneLine(row, "已保留", 12, p.secondary)
				action(row, "替换", func() { item.replace = true }).KeepFocus().PaddingX(6).Disabled(!editable)
			} else {
				placeholder := "私密值"
				if item.Retained {
					placeholder = "留空保留"
				}
				toolRowField(row, name+" 私密值", placeholder, &item.Value, editable).MinWidth(130).Password()
			}
			iconButton(row, "移除 "+name, closeSymbol, 14, func() {
				item.Value = ""
				*rows = slices.Delete(*rows, i, i+1)
			}).KeepFocus().Disabled(!editable)
		})
	}
	action(c, "添加"+title, func() { *rows = append(*rows, &mobileToolValue{}) }).AlignSelf(ui.Start).PaddingX(0).KeepFocus().Disabled(!editable)
}

func toolRowField(c *ui.Context, title, placeholder string, value *string, editable bool) ui.Element {
	p := palette(c)
	return ui.TextInputBase(c, value).Label(title).Placeholder(placeholder).FontSize(controlTextSize * c.Preferences().TextScale).
		PlaceholderColor(p.secondary).TextColor(p.ink).PaddingX(7).Radius(7).Background(p.inset).Grow(1).Basis(0).MinWidth(0).MinHeight(mobileTapSize(c)).
		Autocorrect(false).Capitalize(ui.CapitalizeNone).ReturnKey(ui.ReturnNext).Disabled(!editable)
}

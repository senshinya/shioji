package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/portforward"
)

var openForwardURL = mygo.Shell.OpenExternal

type portForwardDraft struct {
	id                                     string
	connection, host, remote, local, error string
	open, busy                             bool
	cancel                                 context.CancelFunc
}

func (v *View) beginPortForward() {
	if v.parent != nil {
		v.parent.beginPortForward()
		return
	}
	key := v.Model.ConnectionID()
	if key == "local" || key == "" {
		if len(v.Model.Connections) > 0 {
			key = v.Model.Connections[0].ID
		} else {
			key = ""
		}
	}
	v.portForward = &portForwardDraft{id: domain.NewID(), connection: key, host: "localhost", remote: "3000", local: "3000", open: true}
}
func (v *View) cancelPortForward(d *portForwardDraft) {
	if d.cancel != nil {
		d.cancel()
		if v.Controller != nil {
			v.Controller.Do(app.Job{Method: "$forward.close", Input: app.ForwardInput{ID: d.id}})
		}
	}
	if v.portForward == d {
		v.portForward = nil
	}
}
func (v *View) submitPortForward(d *portForwardDraft) {
	if d.busy || v.Controller == nil {
		return
	}
	remote, err := strconv.Atoi(strings.TrimSpace(d.remote))
	if err != nil {
		d.error = "目标端口应在 1–65535 之间"
		return
	}
	local, err := strconv.Atoi(strings.TrimSpace(d.local))
	if err != nil || local < 1 || local > 65535 {
		d.error = "本机端口应在 1–65535 之间"
		return
	}
	target, err := (portforward.Target{Host: strings.TrimSpace(d.host), Port: remote}).Normalize()
	if err != nil {
		d.error = err.Error()
		return
	}
	var device, name string
	for _, c := range v.Model.Connections {
		if c.ID == d.connection {
			device, name = c.DeviceID, c.Name
		}
	}
	if device == "" {
		d.error = "请选择已添加的远程设备"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.busy = true
	d.error = ""
	req := portforward.Request{ID: d.id, DeviceID: device, ConnectionID: d.connection, Name: name, Target: target, LocalPort: local}
	if !v.Controller.Do(app.Job{Context: ctx, ConnectionID: d.connection, Method: "$forward.create", Input: app.ForwardInput{Request: req}, Apply: func(m *app.Model, body json.RawMessage, err error) {
		if v.portForward != d {
			return
		}
		d.busy = false
		d.cancel = nil
		cancel()
		if err != nil {
			d.error = err.Error()
			return
		}
		v.portForward = nil
		var item portforward.Mapping
		json.Unmarshal(body, &item)
		v.revealForward(item.ID)
	}}) {
		cancel()
		d.busy = false
		d.error = "操作繁忙，请重试"
	}
}
func (v *View) revealForward(id string) {
	root := v
	if root.parent != nil {
		root = root.parent
	}
	if root.quickUI().selected != "ports" {
		root.selectQuick("ports")
	}
	for i, item := range root.Model.PortForwards {
		if item.ID == id {
			root.quickUI().ports.ScrollIntoView(i)
			break
		}
	}
}
func (v *View) portForwardDialog(c *n.Context, t *n.Theme) {
	d := v.portForward
	if d == nil {
		return
	}
	dialog := compactDialog(c.Key("create-port-forward"), t, &d.open, 380, func(panel n.Element) {
		panel.Children(func() {
			compactDialogContent(c, "创建端口转发", func() {
				n.Column(c).FillWidth().Children(func() {
					configurationFormRow(c, "设备", configurationSettingsForm, func() {
						name := "选择设备"
						var choices []menuChoice
						for _, conn := range v.Model.Connections {
							if conn.ID == d.connection {
								name = conn.Name
							}
							id := conn.ID
							choices = append(choices, menuChoice{id: id, name: conn.Name, checked: id == d.connection, choose: func() { d.connection = id }})
						}
						trigger := configurationChoiceField(c, "转发设备", name, d.busy || len(choices) == 0).Width(210)
						v.choiceMenu(c, t, "port-forward-device", trigger, choices)
					})
					for _, row := range []struct {
						label string
						value *string
					}{{"目标地址", &d.host}, {"目标端口", &d.remote}, {"本机端口", &d.local}} {
						configurationFormRow(c, row.label, configurationSettingsForm, func() {
							before := d.remote
							input := configurationTextField(c, row.value).Width(210).Label("转发" + row.label).Disabled(d.busy)
							if row.label == "目标地址" {
								input.AutoFocus()
							}
							if input.Changed() && row.label == "目标端口" && (d.local == before || d.local == "") {
								d.local = d.remote
							}
						})
					}
					if d.error != "" {
						n.Text(c, d.error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Margin(8, 0, 0, 0)
					}
				})
			}, func() {
				n.Spacer(c).Grow(1)
				if configurationAction(c, "取消").Clicked() {
					v.cancelPortForward(d)
				}
				if compactDialogPrimary(c, "创建").Disabled(d.busy || v.Controller == nil || len(v.Model.Connections) == 0).Clicked() {
					v.submitPortForward(d)
				}
			})
		})
	})
	if dialog.Dismissed() || !d.open {
		v.cancelPortForward(d)
	}
}
func (v *View) forwardAction(method string, item portforward.Mapping, open func(string) error) {
	if v.Controller == nil {
		return
	}
	if !v.Controller.Do(app.Job{Method: "$forward." + method, Input: app.ForwardInput{ID: item.ID, OpenURL: open}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
		}
	}}) {
		v.persistenceModel().Error = "操作繁忙，请重试"
	}
}
func (v *View) quickPorts(c *n.Context, t *n.Theme) {
	items := v.persistenceModel().PortForwards
	if len(items) == 0 {
		n.Text(c, "暂无端口转发").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Padding(6, 12).Height(32)
		return
	}
	v.quickUI().ports.Key = func(i int) any { return items[i].ID }
	n.List(c.Key("ports-list"), &v.quickUI().ports, len(items), func(i int) {
		item := items[i]
		title := fmt.Sprintf("%d → %s", item.LocalPort, item.Target.Address())
		if item.LocalPort == 0 {
			title = "— → " + item.Target.Address()
		}
		detail := item.Name
		switch item.State {
		case "connecting":
			detail += " · 连接中"
		case "unavailable":
			detail += " · 无法连接"
		case "closing":
			detail += " · 正在关闭"
		}
		n.Row(c).Height(44).FillWidth().Padding(4, 8, 4, 12).Gap(4).AlignItems(n.Start).Children(func() {
			copy := control(c.Key("open"), 4).Grow(1).MinWidth(0).Height(36).Label("打开转发：" + item.LocalAddress()).Tooltip(title + "\n" + item.Name + "\n" + item.Error).Children(func() {
				n.Column(c).FillWidth().Children(func() {
					n.Text(c, title).FontSize(12).FixedLineHeight(18).SingleLine().Ellipsis("…").FillWidth()
					n.Text(c, detail).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).SingleLine().Ellipsis("…").FillWidth()
				})
			})
			if copy.Clicked() && item.State == "available" {
				v.forwardAction("open", item, openForwardURL)
			}
			copy.ContextMenu(func(menu *n.Menu) {
				if menu.Item("打开").Disabled(item.State != "available").Chosen() {
					v.forwardAction("open", item, openForwardURL)
				}
				if menu.Item("复制本机地址").Chosen() {
					c.WriteClipboard(item.LocalAddress())
				}
				if menu.Item("复制目标地址").Chosen() {
					c.WriteClipboard(item.Target.Address())
				}
				if item.State == "unavailable" && menu.Item("重试").Chosen() {
					v.forwardAction("retry", item, nil)
				}
				menu.Separator()
				if menu.Item("关闭").Chosen() {
					v.forwardAction("close", item, nil)
				}
			})
			if iconButton(c, closeIcon, "关闭转发："+item.Name+" · "+strconv.Itoa(item.LocalPort)).Disabled(item.State == "closing").Clicked() {
				v.forwardAction("close", item, nil)
			}
		})
	}).FillWidth().Grow(1).MinHeight(0).Label("端口转发列表")
}

package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

type workspaceCreateDraft struct{ name, path, extra string }

// This state belongs to the global creation entry, not to the visible session.
// Reconnection itself remains owned by the independent device controller.
type workspaceCreation struct {
	open, adding, localPending            bool
	waiting, device, error, previousError string
	started                               time.Time
	targetDevice, targetEpoch             string
	drafts                                map[string]workspaceCreateDraft
}

func (v *View) workspaceCreationUI() *workspaceCreation {
	if v.workspaceCreation == nil {
		v.workspaceCreation = &workspaceCreation{drafts: map[string]workspaceCreateDraft{}}
	}
	return v.workspaceCreation
}

func (v *View) createWorkspace() {
	f := v.workspaceCreationUI()
	f.open, f.waiting, f.error = true, "", ""
	f.localPending = false
	v.remoteWorkspaceRequest = domain.NewID()
}

func (v *View) workspaceCreationChoices() []app.DeviceConnection {
	choices := []app.DeviceConnection{{ID: "local", Name: v.Model.LocalName(), DeviceID: v.Model.LocalDeviceID}}
	seen := map[string]bool{v.Model.LocalDeviceID: true}
	for _, connection := range v.Model.Connections {
		if connection.DeviceID != "" && seen[connection.DeviceID] {
			continue
		}
		seen[connection.DeviceID] = true
		choices = append(choices, connection)
	}
	return choices
}

func (v *View) selectWorkspaceDevice(key string) {
	f := v.workspaceCreationUI()
	f.waiting, f.error = "", ""
	if v.deviceState(key).Connected {
		f.open = false
		v.beginWorkspace(key)
		return
	}
	f.waiting, f.device = key, v.Model.ConnectionDeviceID(key)
	f.previousError = v.deviceState(key).Error
	f.started = v.frameTime
	if v.Controller == nil || !v.Controller.Do(app.Job{ConnectionID: key, Method: "$reconnect"}) {
		f.error = "设备操作繁忙，请重试"
	}
}

func (v *View) syncWorkspaceCreation(c *n.Context) {
	f := v.workspaceCreation
	if f == nil || !f.open || f.waiting == "" || f.error != "" {
		return
	}
	state := v.deviceState(f.waiting)
	if v.Model.ConnectionDeviceID(f.waiting) != f.device {
		f.error = "设备已改变，请重新选择"
		return
	}
	if state.Connected {
		key := f.waiting
		f.waiting, f.open = "", false
		v.beginWorkspace(key)
		return
	}
	if state.Error != "" && state.Error != f.previousError || c.Now().Sub(f.started) >= 10*time.Second {
		f.error = state.Error
		if f.error == "" {
			f.error = "连接超时，请检查设备是否在线"
		}
		return
	}
	c.After(100 * time.Millisecond)
}

func (v *View) workspaceCreationMenu(c *n.Context, t *n.Theme, trigger n.Element) {
	f := v.workspaceCreationUI()
	if trigger.Clicked() {
		if f.open {
			f.open, f.waiting, f.error = false, "", ""
		} else {
			v.createWorkspace()
		}
	}
	v.syncWorkspaceCreation(c)
	popover(c.Key("workspace-create-devices"), trigger, &f.open, func(panel n.Element) {
		_, height := c.Size()
		panel.Role(n.RoleMenu).Label("新建工作区设备菜单").Width(236).Padding(3).Radius(7).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			n.Text(c, "新建工作区到").FontSize(11).FixedLineHeight(26).TextColor(t.TextMuted).PaddingX(7)
			n.Scroll(c).FillWidth().MaxHeight(min(360, height-180)).Label("创建工作区设备列表").Children(func() {
				n.Column(c).FillWidth().FocusGroup(n.Vertical).Children(func() {
					choices := v.workspaceCreationChoices()
					focus := v.Model.ConnectionID()
					found := false
					for _, choice := range choices {
						if choice.ID == focus {
							found = true
						}
					}
					if !found {
						focus = "local"
					}
					for _, choice := range choices {
						key := choice.ID
						row := n.ButtonBase(c.Key(key)).Role(n.RoleMenuItem).FillWidth().Height(26).PaddingX(7).Radius(4).Label("在 " + choice.Name + " 新建工作区").Disabled(v.Controller == nil)
						background := raised(t)
						if row.FocusVisible() || row.Hovered() {
							background = menuSelectionSurface(t)
						}
						row.Background(row.AnimateColor("menu-selection", background, motionAnimation(c, 120*time.Millisecond)))
						if key == focus {
							row.AutoFocus()
						}
						row.Children(func() {
							n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
								inlineSymbol(c, v.deviceGlyph(key), choice.Name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, t.TextMuted)
								n.Text(c, choice.Name).FontSize(13).FixedLineHeight(18).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").TooltipWhenTruncated(choice.Name)
								status := "未连接"
								if v.deviceState(key).Connected {
									status = "已连接"
									if key == "local" {
										status = "本机"
									}
								}
								n.Text(c, status).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Shrink(0)
							})
						})
						if row.Clicked() {
							v.selectWorkspaceDevice(key)
						}
					}
					deviceDivider(c)
					add := n.ButtonBase(c.Key("add")).Role(n.RoleMenuItem).FillWidth().Height(26).PaddingX(7).Radius(4).Label("添加设备…").Disabled(v.Controller == nil)
					if add.Hovered() || add.FocusVisible() {
						add.Background(menuSelectionSurface(t))
					}
					add.Children(func() {
						n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
							inlineIconText(c, plus, "添加设备…", n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, 8, t.Text)
						})
					})
					if add.Clicked() {
						v.dismissDeviceSetup()
						f.open, f.waiting, f.adding = false, "", true
						v.sshCheck = sshEditing
					}
				})
			})
			if f.waiting != "" {
				deviceDivider(c)
				n.Column(c).FillWidth().Padding(4, 7).Gap(4).Children(func() {
					text := "正在连接设备…"
					if f.error != "" {
						text = f.error
					}
					n.Text(c, text).FontSize(11).FixedLineHeight(18).FillWidth().TextColor(t.TextMuted)
					if f.error != "" && ghost(c, "重试").Height(24).FontSize(11).AlignSelf(n.End).Label("重试连接工作区设备").Clicked() {
						v.selectWorkspaceDevice(f.waiting)
					}
				})
			}
		})
	})
	if !f.open {
		f.waiting, f.error = "", ""
	}
}

// Called only after the explicit, validated device onboarding was saved.
func (v *View) continueWorkspaceAfterDeviceSave(connection app.DeviceConnection) {
	f := v.workspaceCreation
	if f == nil || !f.adding {
		return
	}
	f.adding, f.open = false, true
	f.waiting, f.device = connection.ID, connection.DeviceID
	f.error, f.previousError = "", v.deviceState(connection.ID).Error
	f.started = v.frameTime
}

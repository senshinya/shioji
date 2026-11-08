package ui

import (
	"encoding/json"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"path"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

func (v *View) welcomeWorkspace(c *n.Context, t *n.Theme, f *welcomeFlow) {
	v.welcomeHeading(c, t, "添加工作区", f.key, true)
	setupRow(c, "名称", func() { setupField(c, "工作区名称", &f.name, "名称", f.busy) })
	setupRow(c, "主目录", func() {
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			setupField(c, "工作区主目录", &f.directory, "文件夹路径", f.busy).MinWidth(0).Grow(1)
			if f.key == "local" && iconButton(c, workspaceIcon, "选择主目录").MarginEnd(-6).Disabled(f.busy).Clicked() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择工作区主目录", Directory: true})
				if err != nil {
					f.error = err.Error()
				} else if len(paths) > 0 {
					f.directory = paths[0]
					if strings.TrimSpace(f.name) == "" {
						f.name = path.Base(paths[0])
					}
				}
			}
		})
	})
	setupRow(c, "附加目录", func() {
		for i := range f.directories {
			entry := &f.directories[i]
			if !entry.removed.IsZero() && c.Now().Sub(entry.removed) >= 240*time.Millisecond {
				continue
			}
			frame := n.Column(c.Key(entry.id)).FillWidth().Clip()
			if entry.added {
				frame.Jump("row-height", 0)
				entry.added = false
			}
			target := float32(32)
			if !entry.removed.IsZero() {
				target = 0
			}
			frame.Height(frame.AnimateTo("row-height", target, motionAnimation(c, 180*time.Millisecond)))
			n.Inert(frame, !entry.removed.IsZero())
			frame.Children(func() {
				n.Row(c).Absolute().Top(0).Left(0).Right(0).Height(32).Gap(8).AlignItems(n.Center).Children(func() {
					setupField(c, "附加目录路径", &entry.path, "文件夹路径", f.busy).MinWidth(0).Grow(1)
					if iconButton(c, closeIcon, "移除附加目录").MarginEnd(-6).Disabled(f.busy).Clicked() {
						entry.removed = c.Now()
					}
				})
			})
		}
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			if deviceAction(c, "添加文件夹").MarginEnd(-8).Disabled(f.busy).Clicked() {
				f.nextDirectory++
				f.directories = append(f.directories, welcomeDirectory{id: f.nextDirectory, added: true})
			}
		})
	})
	v.welcomeError(c, f.error)
	valid := strings.TrimSpace(f.name) != "" && path.IsAbs(strings.TrimSpace(f.directory))
	for _, entry := range f.directories {
		if entry.removed.IsZero() && !path.IsAbs(strings.TrimSpace(entry.path)) {
			valid = false
		}
	}
	label := "开始使用"
	if f.busy {
		label = "正在创建"
	}
	v.welcomeFooter(c, "返回", label, !valid || f.busy || v.Controller == nil || !v.deviceState(f.key).Connected, func() { v.welcomeStep(welcomeAgents, -1) }, func() { v.submitWelcomeWorkspace(f) })
}
func (v *View) submitWelcomeWorkspace(f *welcomeFlow) {
	if f.busy || v.welcome != f || f.step != welcomeWorkspace {
		return
	}
	folders := []string{strings.TrimSpace(f.directory)}
	for _, entry := range f.directories {
		if entry.removed.IsZero() {
			folders = append(folders, strings.TrimSpace(entry.path))
		}
	}
	for _, folder := range folders {
		if !path.IsAbs(folder) {
			f.error = "请填写所属设备上的绝对路径"
			return
		}
	}
	if strings.TrimSpace(f.name) == "" {
		f.error = "请填写工作区名称"
		return
	}
	state := v.deviceState(f.key)
	if !state.Connected {
		f.error = "设备已断开，请重新连接"
		return
	}
	key, epoch, device := f.key, state.ConnectionEpoch, v.Model.ConnectionDeviceID(f.key)
	f.busy = true
	f.error = ""
	if !v.do(app.Job{ConnectionID: key, Method: "workspace.create", Input: protocol.WorkspaceInput{Name: strings.TrimSpace(f.name), Folders: folders}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		f.busy = false
		if v.welcome != f || f.step != welcomeWorkspace || v.deviceState(key).ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			f.error = err.Error()
			return
		}
		var w domain.Workspace
		if json.Unmarshal(b, &w) != nil || w.ID == "" || w.DeviceID != device {
			f.error = "设备返回的工作区无效"
			return
		}
		state := v.deviceState(key)
		found := false
		for i, old := range state.Snapshot.Workspaces {
			if old.ID == w.ID {
				state.Snapshot.Workspaces[i] = w
				found = true
				break
			}
		}
		if !found {
			state.Snapshot.Workspaces = append(state.Snapshot.Workspaces, w)
		}
		m.WorkspaceID = w.ID
		m.SessionID = ""
		m.ActiveConnection = key
		m.RefreshDevices()
		v.finishWelcome()
	}}) {
		f.busy = false
		f.error = "设备操作繁忙，请重试"
	}
}

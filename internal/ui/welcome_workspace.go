package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"path"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"
	"time"
)

func (v *View) welcomeWorkspace(c *n.Context, t *n.Theme, f *welcomeFlow) {
	v.welcomeHeading(c, t, "添加工作区", f.key, true)
	setupRow(c, "名称", func() { setupField(c, "工作区名称", &f.name, "名称", f.busy) })
	setupRow(c, "主目录", func() {
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			setupField(c, "工作区主目录", &f.directory, "文件夹路径", f.busy).MinWidth(0).Grow(1)
			choose := iconButton(c, workspaceIcon, "选择主目录").MarginEnd(-6).Disabled(f.busy)
			if choose.Clicked() {
				v.chooseWelcomeDirectory(f, 0)
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
				n.Row(c).Absolute().Top(0).Left(0).Right(0).Height(32).Gap(8).AlignItems(n.Start).Children(func() {
					setupField(c, "附加目录路径", &entry.path, "文件夹路径", f.busy).MinWidth(0).Grow(1)
					choose := iconButton(c, workspaceIcon, "选择附加目录").Disabled(f.busy)
					if choose.Clicked() {
						v.chooseWelcomeDirectory(f, entry.id)
					}
					if iconButton(c, closeIcon, "移除附加目录").MarginEnd(-6).Disabled(f.busy).Clicked() {
						entry.removed = c.Now()
					}
				})
			})
		}
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			choose := iconButton(c, plus, "添加文件夹").Tooltip("添加文件夹").MarginEnd(-6).Disabled(f.busy)
			if choose.Clicked() {
				v.chooseWelcomeDirectory(f, -1)
			}
		})
	})
	v.welcomeWorkspaceFeedback(c, t, f)
	valid := strings.TrimSpace(f.name) != "" && path.IsAbs(strings.TrimSpace(f.directory))
	for _, entry := range f.directories {
		if entry.removed.IsZero() && !path.IsAbs(strings.TrimSpace(entry.path)) {
			valid = false
		}
	}
	label := "下一步"
	if f.busy {
		label = "正在创建"
	}
	v.welcomeFooter(c, "返回", label, !valid || f.busy || v.Controller == nil || !v.deviceState(f.key).Connected, func() { v.welcomeStep(welcomeAgents, -1) }, func() { v.submitWelcomeWorkspace(f) })
}

// The row ID survives appending and removing other directory drafts while a picker is open.
func (v *View) chooseWelcomeDirectory(f *welcomeFlow, id int) {
	key := f.key
	valid := func() bool {
		if v.welcome != f || f.step != welcomeWorkspace || f.key != key || f.busy {
			return false
		}
		if id > 0 {
			for _, e := range f.directories {
				if e.id == id {
					return e.removed.IsZero()
				}
			}
			return false
		}
		return true
	}
	title, current := "选择附加目录", f.directory
	if id == 0 {
		title = "选择主目录"
	} else if id > 0 {
		for _, e := range f.directories {
			if e.id == id && e.path != "" {
				current = e.path
			}
		}
	}
	v.chooseWorkspaceDirectory(key, title, current, id < 0, valid, func(paths []string) {
		if id == 0 {
			f.directory = paths[0]
			if strings.TrimSpace(f.name) == "" {
				f.name = path.Base(paths[0])
			}
			return
		}
		if id > 0 {
			for i := range f.directories {
				if f.directories[i].id == id {
					f.directories[i].path = paths[0]
					return
				}
			}
			return
		}
		for _, folder := range paths {
			duplicate := folder == f.directory
			for _, entry := range f.directories {
				if entry.removed.IsZero() && entry.path == folder {
					duplicate = true
				}
			}
			if !duplicate {
				f.nextDirectory++
				f.directories = append(f.directories, welcomeDirectory{id: f.nextDirectory, path: folder, added: true})
			}
		}
	}, func(err error) { f.error = err.Error() })
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
	if f.workspace.ID != "" {
		var current domain.Workspace
		for _, w := range state.Snapshot.Workspaces {
			if w.ID == f.workspace.ID && w.DeviceID == device {
				current = w
				break
			}
		}
		if f.workspace.DeviceID != device || current.ID == "" {
			f.workspace = domain.Workspace{}
		} else if current.Revision != f.workspace.Revision {
			f.error = "工作区已由其他位置修改，请重新载入"
			return
		}
	}
	if f.workspace.ID != "" && f.workspace.Name == strings.TrimSpace(f.name) && slices.Equal(f.workspace.Folders, folders) {
		v.openConversationImport(key, f.workspace, true)
		v.welcomeStep(welcomeImport, 1)
		return
	}
	f.busy = true
	f.error = ""
	method := "workspace.create"
	var input any = protocol.WorkspaceInput{Name: strings.TrimSpace(f.name), Folders: folders}
	if f.workspace.ID != "" {
		method = "workspace.update"
		next := f.workspace
		next.Name = strings.TrimSpace(f.name)
		next.Folders = folders
		input = next
	}
	if !v.do(app.Job{ConnectionID: key, Method: method, Input: input, Apply: func(m *app.Model, b json.RawMessage, err error) {
		f.busy = false
		if v.welcome != f || f.step != welcomeWorkspace || v.deviceState(key).ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			f.error = err.Error()
			return
		}
		var w domain.Workspace
		if json.Unmarshal(b, &w) != nil || w.ID == "" || w.DeviceID != device || len(w.Folders) == 0 {
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
		f.workspace = w
		f.directory = w.Folders[0]
		v.openConversationImport(key, w, true)
		v.welcomeStep(welcomeImport, 1)
	}}) {
		f.busy = false
		f.error = "设备操作繁忙，请重试"
	}
}

func (v *View) welcomeWorkspaceFeedback(c *n.Context, t *n.Theme, f *welcomeFlow) {
	var current domain.Workspace
	for _, w := range v.deviceState(f.key).Snapshot.Workspaces {
		if w.ID == f.workspace.ID && w.DeviceID == v.Model.ConnectionDeviceID(f.key) && w.Revision != f.workspace.Revision {
			current = w
			break
		}
	}
	box := n.Column(c.Key("workspace-feedback")).FillWidth().Clip()
	lastText := n.Local(box, "text", func() string { return f.error })
	lastWorkspace := n.Local(box, "reload", func() domain.Workspace { return current })
	if f.error != "" {
		*lastText = f.error
		*lastWorkspace = current
	}
	n.Inert(box, f.error == "")
	animatedContentHeight(c, box, f.error != "", 180*time.Millisecond, func() float32 {
		n.Row(c).FillWidth().Gap(12).AlignItems(n.Baseline).PaddingY(8).Children(func() {
			n.Text(c, *lastText).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Grow(1).MinWidth(0).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			if lastWorkspace.ID != "" {
				if configurationAction(c, "重新载入").Label("重新载入工作区").Disabled(f.busy).Clicked() {
					current := *lastWorkspace
					f.workspace = current
					f.name = current.Name
					f.directory = current.Folders[0]
					f.directories = nil
					for _, folder := range current.Folders[1:] {
						f.nextDirectory++
						f.directories = append(f.directories, welcomeDirectory{id: f.nextDirectory, path: folder, added: true})
					}
					f.error = ""
				}
			}
		})
		return 0
	})
}

package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

type changesState struct {
	Files    []domain.ChangedFile
	Loaded   bool
	Loading  bool
	Error    string
	Updated  time.Time
	Path     string
	Versions domain.FileVersions
	Diff     editor.DiffState
	List     n.ListState
}

var refreshIcon = svg("M16 7a7 7 0 1 0 1 5M16 3v4h-4")

func (v *View) changesFor(id string) *changesState {
	if v.changes == nil {
		v.changes = map[string]*changesState{}
	}
	state := v.changes[id]
	if state == nil {
		state = &changesState{}
		v.changes[id] = state
	}
	return state
}
func (v *View) requestChanges(force bool) {
	m := v.Model
	if v.Controller == nil || !m.Connected || m.SessionID == "" {
		return
	}
	state := v.changesFor(m.SessionID)
	if state.Loading || (!force && time.Since(state.Updated) < 3*time.Second) {
		return
	}
	state.Loading = true
	_, q := v.gitQuery()
	if !v.do(app.Job{Method: "git.changes", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		state.Loading = false
		state.Updated = time.Now()
		if err != nil {
			state.Error = err.Error()
			return
		}
		state.Error = ""
		if err = json.Unmarshal(b, &state.Files); err != nil {
			state.Error = err.Error()
			return
		}
		state.Loaded = true
	}}) {
		state.Loading = false
	}
}
func (v *View) openChangedFile(path string) {
	if v.Controller == nil || !v.Model.Connected {
		return
	}
	state := v.changesFor(v.Model.SessionID)
	state.Path = path
	state.Error = ""
	_, q := v.gitQuery()
	v.do(app.Job{Method: "git.file", Input: protocol.FileQuery{GitQuery: q, Path: path}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if state.Path != path {
			return
		}
		if err != nil {
			state.Error = err.Error()
			return
		}
		var versions domain.FileVersions
		if err = json.Unmarshal(b, &versions); err != nil {
			state.Error = err.Error()
			return
		}
		previousPath := state.Versions.Path
		state.Versions = versions
		if state.Diff.Old == nil || previousPath != versions.Path {
			state.Diff = editor.DiffState{Old: editor.NewBuffer(versions.Old), New: editor.NewBuffer(versions.New), Layout: editor.DiffUnified, Wrap: true}
		} else {
			state.Diff.Old.Set(versions.Old)
			state.Diff.New.Set(versions.New)
		}
	}})
}
func (v *View) inspector(c *n.Context, t *n.Theme, drawer bool) {
	id := v.Model.SessionID
	state := v.changesFor(id)
	v.requestChanges(false)
	c.After(3 * time.Second)
	panel := n.Column(c.Key("changes-inspector:" + id)).Width(280).FillHeight().Background(t.Background).Label("更改侧栏")
	if drawer {
		width, _ := c.Size()
		panel.Absolute().Right(0).Top(0).Bottom(0).Shadow(-4, 0, 14, 0, n.RGBA(0, 0, 0, .12))
		if state.Path != "" {
			navigation := float32(236)
			if width <= 980 {
				navigation = 224
			}
			panel.Width(min(700, width-navigation-16))
		}
	}
	panel.Children(func() {
		n.Column(c).Width(1).Absolute().Left(0).Top(0).Bottom(0).Background(t.Border)
		n.Row(c).FillWidth().Height(48).PaddingX(12).Gap(6).AlignItems(n.Center).Children(func() {
			if drawer && state.Path != "" {
				if ghost(c, "‹ 更改").FontSize(12).Clicked() {
					state.Path = ""
				}
			} else {
				n.Text(c, "更改").FontSize(12).Grow(1)
			}
			if drawer && state.Path != "" {
				n.Text(c, state.Path).SingleLine().Grow(1).FontSize(12).Tooltip(state.Path)
			}
			if iconButton(c, refreshIcon, "刷新更改").Disabled(!v.Model.Connected).Clicked() {
				v.requestChanges(true)
				if state.Path != "" {
					v.openChangedFile(state.Path)
				}
			}
			if iconButton(c, closeIcon, "关闭更改侧栏").Clicked() {
				v.Model.Inspectors[id] = false
			}
		})
		if drawer && state.Path != "" {
			v.diffBody(c, t, state)
			return
		}
		if state.Error != "" {
			n.Text(c, state.Error).FontSize(12).TextColor(t.TextMuted).Padding(12)
		}
		if !state.Loaded {
			n.Text(c, "读取更改…").FontSize(12).TextColor(t.TextMuted).Padding(12)
		} else if len(state.Files) == 0 {
			n.Text(c, "没有文件更改").FontSize(12).TextColor(t.TextMuted).Padding(12)
		}
		state.List.Key = func(i int) any { return state.Files[i].Path }
		n.List(c, &state.List, len(state.Files), func(i int) {
			file := state.Files[i]
			row := n.ButtonBase(c).FillWidth().Height(28).PaddingX(12).Label("查看文件:" + file.Path).Children(func() {
				n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
					n.Text(c, file.Path).SingleLine().Grow(1).FontSize(12).Tooltip(file.Path)
					a, d := "—", "—"
					if file.Added != nil {
						a = fmt.Sprint(*file.Added)
					}
					if file.Removed != nil {
						d = fmt.Sprint(*file.Removed)
					}
					n.Text(c, "+"+a).Font("Menlo").FontSize(11).Width(34).TextAlign(n.End).TextColor(t.Success)
					removed := n.Text(c, "−"+d).Font("Menlo").FontSize(11).Width(34).TextAlign(n.End).TextColor(t.TextMuted)
					if file.Removed != nil && *file.Removed == 0 {
						removed.Opacity(0)
					}
				})
			})
			if row.Clicked() {
				v.openChangedFile(file.Path)
			}
		}).FillWidth().FillHeight().Grow(1)
	})
}
func (v *View) diffReader(c *n.Context, t *n.Theme, width float32) {
	state := v.changesFor(v.Model.SessionID)
	n.Column(c.Key("diff-reader:" + v.Model.SessionID)).Width(width).FillHeight().Background(t.Background).Label("文件差异阅读器").Children(func() {
		n.Column(c).Width(1).Absolute().Left(0).Top(0).Bottom(0).Background(t.Border)
		n.Row(c).Height(48).FillWidth().PaddingX(14).Gap(8).AlignItems(n.Center).Children(func() {
			n.Text(c, state.Path).SingleLine().Grow(1).FontSize(12).Tooltip(state.Path)
			if iconButton(c, closeIcon, "关闭文件差异").Clicked() {
				state.Path = ""
			}
		})
		v.diffBody(c, t, state)
	})
}
func (v *View) diffBody(c *n.Context, t *n.Theme, state *changesState) {
	if state.Error != "" {
		n.Text(c, state.Error).FontSize(12).TextColor(t.TextMuted).Padding(14)
		return
	}
	if state.Versions.Path != state.Path {
		n.Text(c, "读取文件…").FontSize(12).TextColor(t.TextMuted).Padding(14)
		return
	}
	editor.DiffView(c.Key("diff"), &state.Diff).FillWidth().FillHeight().Grow(1).Label("原生文件差异")
}

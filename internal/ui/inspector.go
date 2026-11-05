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
func (v *View) inspector(c *n.Context, t *n.Theme, drawer bool, width, limit float32) {
	id := v.Model.SessionID
	state := v.changesFor(id)
	v.requestChanges(false)
	c.After(3 * time.Second)
	panel := n.Column(c.Key("changes-inspector:" + id)).Width(width).Shrink(0).FillHeight().Clip().Background(t.Background).Label("更改侧栏")
	if drawer {
		panel.Absolute().Right(0).Top(0).Bottom(0).Shadow(-4, 0, 14, 0, n.RGBA(0, 0, 0, .12))
		if !v.resizingInspector {
			panel.Transition(drawerMotion(c))
		}
	}
	goBack := false
	fileOpen := state.Path != ""
	header := func(file bool) {
		n.Row(c).FillWidth().Height(48).Padding(0, 80, 0, 12).Gap(6).AlignItems(n.Center).Children(func() {
			if file {
				back := control(c, 5).Size(24, 26).Label("返回更改列表").Tooltip("返回更改列表").Children(func() { n.Icon(c, disclosureRight).Size(14, 14).Transform(n.Rotation(180)).TextColor(t.TextMuted) })
				goBack = back.Clicked()
				n.Text(c, state.Path).SingleLine().Grow(1).FontSize(12).Tooltip(state.Path)
			} else {
				n.Text(c, "更改").FontSize(12).Grow(1)
			}
		})
	}
	panel.Children(func() {
		n.Column(c).Width(1).Absolute().Left(0).Top(0).Bottom(0).Background(t.Border)
		n.Column(c.Key("inspector-list")).FillWidth().FillHeight().IgnoreInput(fileOpen).Children(func() {
			header(false)
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
				row := control(c, 0).FillWidth().Height(28).PaddingX(12).Label("查看文件:" + file.Path).Children(func() {
					n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
						n.Text(c, file.Path).SingleLine().Grow(1).FontSize(12).Tooltip(file.Path)
						added, deleted, _, _ := gitColors(t)
						a, d := "—", "—"
						if file.Added != nil {
							a = fmt.Sprint(*file.Added)
						}
						if file.Removed != nil {
							d = fmt.Sprint(*file.Removed)
						}
						n.Text(c, "+"+a).Font("Menlo").FontSize(11).Width(34).TextAlign(n.End).TextColor(added)
						removed := n.Text(c, "−"+d).Font("Menlo").FontSize(11).Width(34).TextAlign(n.End).TextColor(deleted)
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
		// Keep the exiting file above the restored list, including its header.
		n.Column(c.Key("inspector-file-layer")).Absolute().Left(0).Right(0).Top(0).Bottom(0).Clip().PassThrough().Children(func() {
			if !fileOpen {
				return
			}
			pose := n.Motion{X: width, Opacity: 1}
			if c.Preferences().ReduceMotion {
				pose.X = 0
			}
			n.Column(c.Key("inspector-file")).FillWidth().FillHeight().Background(t.Background).
				Transition(n.ElementTransition{Duration: motionAnimation(c, 260*time.Millisecond).Duration, Ease: n.EaseOut, Colors: true, Enter: &pose, Exit: &pose}).
				Children(func() {
					header(true)
					v.diffBody(c, t, state)
				})
		})
		n.Row(c.Key("inspector-actions")).Absolute().Right(12).Top(10).Height(28).Gap(6).AlignItems(n.Center).Background(t.Background).Children(func() {
			if iconButton(c, refreshIcon, "刷新更改").Disabled(!v.Model.Connected).Clicked() {
				v.requestChanges(true)
				if fileOpen {
					v.openChangedFile(state.Path)
				}
			}
			if iconButton(c, closeIcon, "关闭更改侧栏").Clicked() {
				v.Model.Inspectors[id] = false
			}
		})

		grip := n.Column(c.Key("inspector-resize")).Absolute().Left(0).Top(0).Bottom(0).Width(6).Cursor(n.CursorResizeEW).Focusable().Role(n.RoleSplitter).Label("调整右侧栏宽度")
		dx, _, held := grip.Dragged()
		v.resizingInspector = held
		change := float32(0)
		if held {
			change = -dx
		}
		if grip.Shortcut(0, n.KeyLeft) {
			change = 10
		}
		if grip.Shortcut(0, n.KeyRight) {
			change = -10
		}
		if change != 0 {
			v.Model.InspectorWidth = min(max(width+change, 280), min(limit, 960))
			c.AnimationFrame()
		}
	})
	if goBack {
		state.Path = ""
	}
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
	added, deleted, addedFill, deletedFill := gitColors(t)
	state.Diff.Font = n.Font{Family: "Menlo", Size: 11, Weight: 400, Antialiased: true}
	state.Diff.LineHeight = 23.0 / 11
	state.Diff.Inline = editor.DiffInlineNone
	state.Diff.Indicators = editor.DiffClassic
	state.Diff.Theme = &editor.Theme{Background: t.Background, Text: t.Text, LineNumber: subtle(t), Added: added, Deleted: deleted, AddedBackground: addedFill, DeletedBackground: deletedFill}
	editor.DiffView(c.Key("diff"), &state.Diff).FillWidth().FillHeight().Grow(1).Label("原生文件差异")
}

package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"time"
)

var backgroundWorkIcon = svg("M3 5l5 5-5 5M11 15h6")

func inspectorWorkName(kind string) string {
	switch kind {
	case "agents":
		return "子 Agent"
	case "background":
		return "后台任务"
	case "tasks":
		return "任务"
	}
	return "更改"
}
func inspectorWorkIcon(kind string) *n.SVG {
	switch kind {
	case "agents":
		return agentActivityIcon
	case "background":
		return backgroundWorkIcon
	case "tasks":
		return tasksIcon
	}
	return changesIcon
}
func workDockGroups(layout app.InspectorLayout, prior, hasTasks bool) []app.InspectorGroup {
	if len(layout.Groups) > 0 {
		return app.CloneInspectorGroups(layout.Groups)
	}
	if layout.Joined {
		first := layout.First
		if first == "" {
			first = "changes"
		}
		other := otherInspectorView(first)
		selected := layout.Selected
		if selected == "" {
			selected = first
		}
		return []app.InspectorGroup{{Views: []string{first, other, "agents", "background"}, Selected: selected}}
	}
	top, bottom := "agents", "background"
	if prior || layout.First != "" || layout.Selected != "" {
		top = "changes"
		if hasTasks {
			bottom = "tasks"
		}
	}
	groups := []app.InspectorGroup{{Views: []string{"changes", "agents"}, Selected: top}, {Views: []string{"tasks", "background"}, Selected: bottom}}
	if layout.First == "tasks" {
		groups[0], groups[1] = groups[1], groups[0]
	}
	return groups
}

type visibleWorkGroup struct {
	index    int
	views    []string
	selected string
}

func visibleWorkGroups(groups []app.InspectorGroup, hasTasks bool) []visibleWorkGroup {
	out := []visibleWorkGroup{}
	for i, g := range groups {
		visible := []string{}
		for _, kind := range g.Views {
			if kind != "tasks" || hasTasks {
				visible = append(visible, kind)
			}
		}
		if len(visible) == 0 {
			continue
		}
		selected := g.Selected
		if !slices.Contains(visible, selected) {
			selected = visible[0]
		}
		out = append(out, visibleWorkGroup{i, visible, selected})
	}
	return out
}
func (v *View) workInspector(c *n.Context, t *n.Theme, drawer bool, width, limit float32, state *changesState, works []domain.WorkActivity, prior bool) {
	id := v.Model.SessionID
	plan := v.Model.Details[id].Plan
	hasTasks := plan != nil && (len(plan.Steps) > 0 || plan.Narrative != "")
	if state.WorkGroups == nil {
		state.WorkGroups = workDockGroups(v.Model.InspectorLayout, prior, hasTasks)
	}
	groups := state.WorkGroups
	if len(v.Model.InspectorLayout.Groups) > 0 {
		groups = v.Model.InspectorLayout.Groups
	}
	groups = app.CloneInspectorGroups(groups)
	visible := visibleWorkGroups(groups, hasTasks)
	split := len(visible) > 1
	_, height := c.Size()
	bottomHeight := v.Model.InspectorSplitHeight
	if bottomHeight == 0 {
		bottomHeight = height / 2
	}
	splitLimit := max(float32(120), height-240)
	bottomHeight = min(max(bottomHeight, 120), splitLimit)
	v.requestChanges(false)
	c.After(3 * time.Second)
	panel := n.Column(c.Key("changes-inspector:" + id)).Width(width).Shrink(0).FillHeight().Clip().Background(t.Background).Label("更改侧栏")
	if drawer {
		panel.Absolute().Right(0).Top(0).Bottom(0).Shadow(-4, 0, 14, 0, n.RGBA(0, 0, 0, .12))
		if !v.splitRoot().resizingInspector {
			panel.Transition(drawerMotion(c))
		}
	}
	var next []app.InspectorGroup
	panel.Children(func() {
		v.workInspectorPane(c, t, state, plan, works, "top", visible[0], groups, 48, width, &next)
		bottom := n.Column(c.Key("inspector-bottom")).FillWidth().Shrink(0).Clip()
		target := float32(0)
		if split {
			target = 1
		}
		extent := bottom.AnimateTo("split-presence", target, motionAnimation(c, 200*time.Millisecond))
		bottom.Height(bottomHeight * extent)
		bottom.Children(func() {
			if !split {
				return
			}
			v.workInspectorPane(c, t, state, plan, works, "bottom", visible[1], groups, 36, width, &next)
			n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(1).Background(t.Border).PassThrough()
			grip := n.Column(c.Key("task-panel-resize")).Absolute().Left(0).Right(0).Top(0).Height(6).Cursor(n.CursorResizeNS).Focusable().Role(n.RoleSplitter).Label("调整侧栏分区高度")
			_, dy, held := grip.Dragged()
			change := float32(0)
			if held {
				change = -dy
			}
			if grip.Shortcut(0, n.KeyUp) {
				change = 10
			}
			if grip.Shortcut(0, n.KeyDown) {
				change = -10
			}
			if change != 0 {
				v.Model.InspectorSplitHeight = min(max(bottomHeight+change, 120), splitLimit)
				c.AnimationFrame()
			}
		})
		n.Row(c.Key("inspector-actions")).Absolute().Right(12).Top(10).Height(28).Gap(6).AlignItems(n.Center).Background(t.Background).Children(func() {
			if iconButton(c, refreshIcon, "刷新更改").Disabled(!v.Model.Connected).Clicked() {
				v.requestChanges(true)
				if state.Path != "" {
					v.openChangedFile(state.Path)
				}
				v.refreshSelectedWork()
			}
			if iconButton(c, closeIcon, "关闭更改侧栏").Clicked() {
				v.Model.Inspectors[id] = false
			}
		})
		if !split {
			dragged, over := n.DragOver[inspectorTabDrag](panel)
			dropped, released := n.Drop[inspectorTabDrag](panel)
			_, y, inside := panel.PointerPosition()
			valid := over && dragged.session == id
			zone := inside && y > height*.6
			if valid && zone {
				n.Column(c.Key("inspector-split-preview")).Absolute().Left(6).Right(6).Bottom(6).Height(height/2-12).Radius(6).Background(t.Text.Alpha(.07)).Border(1, t.TextMuted.Alpha(.4)).PassThrough().Label("下方分屏预览")
			}
			if released && zone && dropped.session == id {
				next = app.SplitInspectorView(groups, dropped.kind)
				v.Model.InspectorSplitHeight = height / 2
			}
		}
		n.Column(c).Width(1).Absolute().Left(0).Top(0).Bottom(0).Background(t.Border).PassThrough()
		grip := n.Column(c.Key("inspector-resize")).Absolute().Left(0).Top(0).Bottom(0).Width(6).Cursor(n.CursorResizeEW).Focusable().Role(n.RoleSplitter).Label("调整右侧栏宽度")
		dx, _, held := grip.Dragged()
		v.splitRoot().resizingInspector = held
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
	if next != nil {
		v.Model.InspectorLayout.Groups = next
		state.WorkGroups = app.CloneInspectorGroups(next)
		c.AnimationFrame()
	}
}
func (v *View) workInspectorPane(c *n.Context, t *n.Theme, state *changesState, plan *domain.Plan, works []domain.WorkActivity, slot string, g visibleWorkGroup, groups []app.InspectorGroup, headerHeight, width float32, next *[]app.InspectorGroup) {
	pane := n.Column(c.Key("inspector-pane:" + slot)).FillWidth().FillHeight().Grow(1).Clip().Background(t.Background)
	pane.Transition(n.ElementTransition{Duration: motionAnimation(c, 200*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 1}, Exit: &n.Motion{Opacity: 1}})
	switch g.selected {
	case "agents":
		pane.Label("子 Agent 面板")
	case "background":
		pane.Label("后台任务面板")
	case "tasks":
		pane.Label("任务面板")
	default:
		pane.Label("文件更改区域")
	}
	pane.Children(func() {
		header := n.Row(c.Key("work-group-header")).FillWidth().Height(headerHeight).Shrink(0).Padding(0, 12).Gap(2).AlignItems(n.Center)
		header.Children(func() {
			selected := slices.Index(g.views, g.selected)
			tabs := n.TabsBase(c.Key("work-tabs"), &selected, len(g.views))
			tabs.List.Gap(2).Children(func() {
				for i, kind := range g.views {
					button := controlFeedback(c, tabs.Tab(i), 6).Size(28, 28).AlignItems(n.Center).Justify(n.Center).Radius(6).Label(inspectorWorkName(kind)).Tooltip(inspectorWorkName(kind)).Cursor(n.CursorGrab).Drag(inspectorTabDrag{v.Model.SessionID, kind})
					if kind == g.selected {
						button.Background(t.SurfacePressed)
					}
					if button.Dragging() {
						button.Opacity(0)
					}
					button.Children(func() { n.Icon(c, inspectorWorkIcon(kind)).Size(16, 16).TextColor(t.TextMuted) })
				}
			})
			n.Text(c, inspectorWorkName(g.selected)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).MarginStart(6).Grow(1)
			if slot == "top" {
				n.Spacer(c).Width(68)
			}
			if selected >= 0 && g.views[selected] != g.selected {
				*next = app.CloneInspectorGroups(groups)
				(*next)[g.index].Selected = g.views[selected]
			}
		})
		dragged, over := n.DragOver[inspectorTabDrag](header)
		dropped, released := n.Drop[inspectorTabDrag](header)
		valid := over && dragged.session == v.Model.SessionID && !slices.Contains(g.views, dragged.kind)
		if valid {
			n.Column(c.Key("work-merge-preview")).Absolute().Left(10).Top((headerHeight-32)/2).Width(float32(len(g.views)*30+30)).Height(32).Radius(7).Background(t.Text.Alpha(.07)).Border(1, t.TextMuted.Alpha(.4)).PassThrough().Label("合并标签预览")
		}
		if released && dropped.session == v.Model.SessionID && !slices.Contains(g.views, dropped.kind) {
			*next = app.MoveInspectorView(groups, dropped.kind, g.index)
		}
		index := slices.Index(g.views, g.selected)
		configurationTabPanel(c.Key("work-group-content"), g.selected, index, configurationPanelFill, func() {
			switch g.selected {
			case "agents", "background":
				v.sessionWorkBody(c, t, g.selected, works, width)
			default:
				var unused *app.InspectorLayout
				v.inspectorPane(c, t, state, plan, "body-"+slot, g.selected, nil, false, 0, width, &unused)
			}
		}, false)
	})
}

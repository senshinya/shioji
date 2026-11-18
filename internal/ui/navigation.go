package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"time"

	n "github.com/egoist/mygo/ui"
)

var navigationPinIcon = designIcon("m16 3 5 5-4 2-3 5-5-5 5-3 2-4ZM9 10l-3 3 5 5 3-3M8 16l-5 5")

type navigationRowKind uint8

const (
	navigationSession navigationRowKind = iota
	navigationWorkspace
	navigationPinnedHeading
	navigationWorkspaceHeading
)

type navigationKey struct {
	kind navigationRowKind
	ref  app.SessionLocation
}
type navigationRow struct {
	kind      navigationRowKind
	workspace domain.Workspace
	session   domain.Session
	ref       app.SessionLocation
	pinned    bool
	pending   int
	presence  float32
}

func (r navigationRow) key() navigationKey { return navigationKey{r.kind, r.ref} }

type navigationSave struct {
	before, after app.NavigationPreferences
}
type navigationOffset struct {
	from    float32
	started time.Time
}
type navigationGeometry struct {
	keys    []navigationKey
	tops    map[navigationKey]float32
	offsets map[navigationKey]navigationOffset
	bounds  map[navigationKey]n.Rect
}

func navigationRowHeight(r navigationRow) float32 {
	height := float32(30)
	if r.kind == navigationPinnedHeading {
		height = 28
	}
	if r.kind == navigationWorkspaceHeading {
		height = 32
	}
	return height * r.presence
}
func (v *View) navigationOffset(c *n.Context, key navigationKey) float32 {
	o, ok := v.navigationGeometry.offsets[key]
	if !ok || c.Preferences().ReduceMotion {
		return 0
	}
	p := min(1, float32(c.Now().Sub(o.started))/float32(180*time.Millisecond))
	if p >= 1 {
		return 0
	}
	c.AnimationFrame()
	return o.from * (1 - n.EaseOut(p))
}

// Fixed row heights give content coordinates independently of list virtualization
// and scrolling. Height reveals own their motion; only order changes retarget shifts.
func (v *View) prepareNavigationMotion(c *n.Context, rows []navigationRow) {
	g := &v.navigationGeometry
	keys := make([]navigationKey, 0, len(rows))
	tops := make(map[navigationKey]float32, len(rows))
	var y float32
	for _, r := range rows {
		key := r.key()
		keys = append(keys, key)
		tops[key] = y
		y += navigationRowHeight(r)
	}
	if g.tops != nil && !slices.Equal(g.keys, keys) && !c.Preferences().ReduceMotion {
		next := map[navigationKey]navigationOffset{}
		for _, key := range keys {
			if old, ok := g.tops[key]; ok {
				delta := old + v.navigationOffset(c, key) - tops[key]
				if math.Abs(float64(delta)) > .1 {
					next[key] = navigationOffset{delta, c.Now()}
				}
			}
		}
		g.offsets = next
	}
	if c.Preferences().ReduceMotion {
		g.offsets = nil
	}
	g.keys, g.tops = keys, tops
	g.bounds = make(map[navigationKey]n.Rect)
}

func (v *View) navigationChange(change func(*app.Model) error) {
	v = v.splitRoot()
	m := v.Model
	before := m.Navigation.Clone()
	if err := change(m); err != nil {
		m.Error = err.Error()
		return
	}
	if reflect.DeepEqual(before, m.Navigation) {
		return
	}
	if v.Saver != nil {
		if v.navigationSave == nil {
			v.navigationSave = &navigationSave{before: before}
		}
		v.navigationSave.after = m.Navigation.Clone()
	}
}
func (v *View) syncNavigationSave(saved []byte, err error) bool {
	if pending := v.navigationSave; pending != nil && len(saved) > 0 {
		var state struct {
			Navigation app.NavigationPreferences `json:"navigation"`
		}
		if json.Unmarshal(saved, &state) == nil {
			pending.before = state.Navigation.Clone()
			if err == nil && (reflect.DeepEqual(state.Navigation, pending.after) || reflect.DeepEqual(state.Navigation, v.Model.Navigation)) {
				v.navigationSave = nil
			}
		}
	}
	if err != nil && (v.navigationSave != nil || v.navigationSaveError != "") {
		if pending := v.navigationSave; pending != nil {
			v.Model.Navigation = pending.before.Clone()
			v.Model.ObserveNavigation()
			v.navigationSave = nil
		}
		v.navigationSaveError = "无法保存排列：" + err.Error()
		v.Model.Error = v.navigationSaveError
		return true
	}
	if err == nil && v.navigationSaveError != "" {
		if v.Model.Error == v.navigationSaveError {
			v.Model.Error = ""
		}
		v.navigationSaveError = ""
	}

	return false
}
func (v *View) navigationMove(ref app.SessionLocation, delta int) {
	m := v.persistenceModel()
	var items []domain.Session
	if m.SessionPinned(ref) {
		items = m.NavigationPins()
	} else {
		for _, w := range m.Snapshot.Workspaces {
			if w.DeviceID == ref.DeviceID && w.ID == ref.WorkspaceID {
				items = m.NavigationSessions(w)
				break
			}
		}
	}
	items = slices.DeleteFunc(items, func(s domain.Session) bool { return s.Archived })
	at := -1
	for i, s := range items {
		if s.ID == ref.SessionID {
			at = i
			break
		}
	}
	if at < 0 || at+delta < 0 || at+delta >= len(items) {
		return
	}
	before := app.SessionLocation{}
	if delta < 0 {
		before, _ = m.NavigationSessionLocation(items[at-1].ID)
	} else if at+2 < len(items) {
		before, _ = m.NavigationSessionLocation(items[at+2].ID)
	}
	pinned := m.SessionPinned(ref)
	v.navigationChange(func(m *app.Model) error { return m.MoveNavigationSession(ref, before, pinned) })
}
func (v *View) navigationMenu(menu *n.Menu, s domain.Session, ref app.SessionLocation) {
	m := v.persistenceModel()
	pinned := m.SessionPinned(ref)
	label := "置顶会话"
	if pinned {
		label = "取消置顶"
	}
	if menu.Item(label).Disabled(s.Archived && !pinned).Chosen() {
		v.navigationChange(func(m *app.Model) error { return m.SetSessionPinned(ref, !pinned) })
	}
	if menu.Item("上移一位").Disabled(s.Archived).Chosen() {
		v.navigationMove(ref, -1)
	}
	if menu.Item("下移一位").Disabled(s.Archived).Chosen() {
		v.navigationMove(ref, 1)
	}
	menu.Separator()
}
func (v *View) navigationRows(c *n.Context) []navigationRow {
	m := v.navigationPreview()
	workspaces := m.NavigationWorkspaces()
	byWorkspace := map[string]domain.Workspace{}
	for _, w := range workspaces {
		byWorkspace[w.ID] = w
	}
	sessions := map[app.SessionLocation]domain.Session{}
	presence := map[app.SessionLocation]float32{}
	pending := map[string]int{}
	for _, s := range m.Snapshot.Sessions {
		w, ok := byWorkspace[s.WorkspaceID]
		if !ok {
			continue
		}
		ref := app.SessionLocation{DeviceID: w.DeviceID, WorkspaceID: w.ID, SessionID: s.ID}
		sessions[ref] = s
		presence[ref] = v.navigationPresence(c, "session:"+s.ID, !w.Archived && !s.Archived)
		if v.collapsedWorkspaces[w.ID] && !w.Archived && !s.Archived {
			pending[w.ID] += v.pendingCount(s.ID)
		}
	}
	rows := []navigationRow{}
	heading := v.navigationPresence(c, "global-pinned-heading", len(m.NavigationPins()) > 0)
	if heading > 0 || len(m.NavigationPins()) > 0 {
		rows = append(rows, navigationRow{kind: navigationPinnedHeading, presence: heading})
	}
	global := map[app.SessionLocation]bool{}
	for _, ref := range m.Navigation.Pinned {
		s, ok := sessions[ref]
		if !ok || global[ref] {
			continue
		}
		w := byWorkspace[s.WorkspaceID]
		if presence[ref] <= 0 {
			continue
		}
		global[ref] = true
		rows = append(rows, navigationRow{kind: navigationSession, workspace: w, session: s, ref: ref, pinned: true, presence: presence[ref]})
	}
	rows = append(rows, navigationRow{kind: navigationWorkspaceHeading, presence: 1})
	for _, w := range workspaces {
		p := v.navigationPresence(c, "workspace:"+w.ID, !w.Archived)
		if p <= 0 {
			continue
		}
		rows = append(rows, navigationRow{kind: navigationWorkspace, workspace: w, ref: app.SessionLocation{DeviceID: w.DeviceID, WorkspaceID: w.ID}, presence: p, pending: pending[w.ID]})
		if v.collapsedWorkspaces[w.ID] {
			continue
		}
		for _, s := range m.NavigationSessions(w) {
			ref := app.SessionLocation{DeviceID: w.DeviceID, WorkspaceID: w.ID, SessionID: s.ID}
			if global[ref] || presence[ref] <= 0 {
				continue
			}
			rows = append(rows, navigationRow{kind: navigationSession, workspace: w, session: s, ref: ref, presence: min(p, presence[ref])})
		}
	}
	return rows
}

func (v *View) sidebar(c *n.Context, t *n.Theme) {
	m := v.Model
	width, _ := c.Size()
	sidebarWidth := float32(256)
	if width <= 980 {
		sidebarWidth = 224
	} else if width <= 1160 {
		sidebarWidth = 236
	}
	color := t.Surface
	if t.Dark {
		color = n.Hex("#161616")
	}
	n.Column(c).Width(sidebarWidth).FillHeight().Padding(8, 0, 8, 8).Children(func() {
		card := n.Column(c).Fill().Radius(14).Clip().Background(color).Border(1, t.Border)
		card.Children(func() {
			n.Column(c.Key("nav-top")).FillWidth().Padding(sidebarHeaderTopInset, 8, 0, 8).Gap(sidebarHeaderGap).Children(func() {
				n.Row(c).Height(sidebarTitlebarHeight).AlignItems(n.Start).DragWindow().Children(func() { n.Spacer(c).Grow(1); v.searchControl(c, t) })
				if navButton(c, plus, "新建会话").Background(func() n.Color {
					if m.SessionID == "" && !v.devices && (v.archive == nil || !v.archive.open) {
						return t.SurfacePressed
					}
					return n.Transparent
				}()).Clicked() {
					v.home()
				}
				v.attentionControl(c, t)
			})
			rows := v.navigationRows(c)
			for i := range rows {
				if rows[i].kind == navigationSession {
					rows[i].presence = min(rows[i].presence, v.sessionEntryProgress(c, rows[i].session.ID, false))
				}
			}
			v.prepareNavigationMotion(c, rows)
			v.nav.Key = func(i int) any { return rows[i].key() }
			v.nav.KeepOffset = true
			v.nav.Reveals = v.nav.Reveals[:0]
			for i, r := range rows {
				p := r.presence
				if r.kind == navigationSession {
					p = min(p, v.sessionEntryProgress(c, r.session.ID, false))
				}
				if p < 1 {
					v.nav.Reveals = append(v.nav.Reveals, n.ListReveal{First: i, End: i + 1, Progress: p})
				}
			}
			list := n.List(c, &v.nav, len(rows), func(i int) {
				r := rows[i]
				box := n.Column(c.Key("navigation-row-bounds")).FillWidth()
				if bounds := box.LayoutBounds(); bounds.H > 0 {
					v.navigationGeometry.bounds[r.key()] = bounds
				}
				box.Children(func() { v.navigationRow(c, t, r) })
			}).Grow(1).FillHeight().Margin(0, 0, 12, 8).
				Padding(0, 16, 0, 0).ScrollbarInsets(4, 1, 4, 0).
				Label("工作区与会话").TrackScroll(&v.navigationScroll)
			v.navigationListGestures(c, list, rows)
			v.quickFooter(c, t)
			available := card.LayoutBounds().H
			if available == 0 {
				_, available = c.Size()
				available -= 16
			}
			v.quickPanel(c, t, color, available)

		})
		if v.quickUI().selected != "" && v.portForward == nil {
			if card.PressedOutside() {
				v.quickUI().selected = ""
			}
			if c.Shortcut(0, n.KeyEscape) {
				v.quickUI().focus = v.quickUI().selected
				v.quickUI().selected = ""
			}
		}
	})
}
func (v *View) navigationRow(c *n.Context, t *n.Theme, r navigationRow) {
	m := v.Model
	shift := n.Translation(0, v.navigationOffset(c, r.key()))
	switch r.kind {
	case navigationPinnedHeading:
		n.Row(c).Height(28).Padding(8, 8, 0, 8).Opacity(r.presence).Children(func() { n.Text(c, "置顶").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted) })
	case navigationWorkspaceHeading:
		n.Row(c).Height(32).Padding(8, 0, 0, 6).AlignItems(n.Center).Transform(shift).Children(func() {
			n.Text(c, "工作区").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Grow(1)
			iconButton(c, moreActionsIcon, "工作区菜单").Menu(func(menu *n.Menu) {
				if menu.Item("已归档…").Chosen() {
					v.openArchive()
				}
			})
			v.workspaceCreationMenu(c, t, iconButton(c, plus, "创建工作区").Size(28, 28).MarginEnd(-2))
		})
	case navigationWorkspace:
		w := r.workspace
		expanded := !v.collapsedWorkspaces[w.ID]
		description := m.DeviceName(w.DeviceID)
		if r.pending > 0 {
			description += fmt.Sprintf("，%d 项待处理", r.pending)
		}
		group := n.Row(c).Height(30).Gap(3).AlignItems(n.Center).Opacity(r.presence).Transform(shift)
		v.navigationDragSource(c, group, r)
		group.Children(func() {
			if control(c.Key("workspace:" + w.ID)).Height(24).Grow(1).Gap(3).Justify(n.Start).Label(w.Name).Description(description).Expanded(expanded).Children(func() {
				n.Row(c).Grow(1).Gap(3).AlignItems(n.Baseline).Children(func() {
					angle := float32(0)
					if v.collapsedWorkspaces[w.ID] {
						angle = -90
					}
					inlineSymbol(c, disclosureDown, w.Name, n.Font{Family: t.Font, Size: 12, Weight: 500}, 11, t.TextMuted, angle).Width(20).TextAlign(n.Center)
					n.Text(c, w.Name).SingleLine().Grow(1).FontSize(12).FontWeight(500).FixedLineHeight(20).TooltipWhenTruncated(w.Name)
				})
				n.Text(c, m.DeviceName(w.DeviceID)).FontSize(11).TextColor(t.TextMuted).PaddingX(6)
				if r.pending > 0 {
					n.Text(c, fmt.Sprint(r.pending)).Font("Menlo").FontSize(11).Width(16).Shrink(0).TextAlign(n.End).
						TextColor(t.Warning).Label("工作区待处理：" + w.ID)
				}
			}).ContextMenu(func(menu *n.Menu) { v.workspaceMenu(menu, w) }).Clicked() {
				v.collapsedWorkspaces[w.ID] = !v.collapsedWorkspaces[w.ID]
			}
		})
	case navigationSession:
		v.navigationSessionRow(c, t, r)
	}
}
func (v *View) navigationSessionRow(c *n.Context, t *n.Theme, r navigationRow) {
	m, s := v.Model, r.session
	selected := s.ID == v.focusedSessionID() && !v.devices && v.workspaceEditor == nil
	finishing := s.Status == "completed" && v.sessionOutputAnimating(s.ID)
	if finishing {
		c.AnimationFrame()
	}
	pending := v.pendingCount(s.ID)
	description := sessionNavigationDescription(s, pending, selected, finishing)
	row := control(c, 8).FillWidth().Height(28).MarginY(1).Padding(4, 0, 4, 20).Radius(8).Label(s.Title).Description(description)
	shift := v.navigationOffset(c, r.key())
	row.Transform(n.Translation(0, shift))
	if p := min(r.presence, v.sessionEntryProgress(c, s.ID, false)); p < 1 {
		row.Opacity(p).Transform(n.Translation(-6*(1-p), shift))
	}
	if selected {
		row.Background(t.SurfacePressed)
	}
	moving := v.navigationDragSource(c, row, r)
	row.ContextMenu(func(menu *n.Menu) { v.sessionMenu(menu, s, r.ref) })
	row.Children(func() {
		n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
			if s.ReadOnlyHistory() {
				inlineSymbol(c, modelEye, s.Title, n.Font{Family: t.Font, Size: 13}, 10, t.TextMuted).Label("只读会话：" + s.ID).Tooltip("只读会话")
			} else if pending > 0 {
				statusDotSize(c, s.Title, n.Font{Family: t.Font, Size: 13}, t.Warning, 4, false).Width(10).TextAlign(n.Center).Label("会话等待状态：" + s.ID)
			} else if s.Status.Active() || finishing {
				statusDotSize(c, s.Title, n.Font{Family: t.Font, Size: 13}, t.Success, 4, true).Width(10).TextAlign(n.Center).Label("会话运行状态：" + s.ID)
			} else if s.Status == "completed" {
				color := t.TextMuted
				if m.HasCompletedReply(s.ID) {
					color = t.Success
				}
				inlineSymbol(c, check, s.Title, n.Font{Family: t.Font, Size: 13}, 10, color)
			} else if s.Status == "interrupted" || s.Status == "abandoned" {
				inlineSymbol(c, check, s.Title, n.Font{Family: t.Font, Size: 13}, 10, t.TextMuted).Label("已停止")
			} else {
				glyph, label := "Ⅱ", "已暂停"
				if s.Status == "failed" {
					glyph, label = "×", "执行失败"
				}
				if s.Status == "unknown" {
					glyph, label = "?", "执行状态待核实"
				}
				n.Text(c, glyph).Width(10).FontSize(11).TextColor(t.TextMuted).Label(label)
			}
			color := t.TextMuted
			if selected {
				color = t.Text
			}
			animation := motionAnimation(c, 160*time.Millisecond)
			title := n.Text(c, s.Title).SingleLine().Grow(1).MinWidth(0).FontSize(13).FixedLineHeight(20).ContentTransition(n.ContentTransition{Animation: &animation}).TextColor(color)
			title.TooltipWhenTruncated(s.Title)
			if pending > 0 {
				n.Text(c, fmt.Sprint(pending)).Font("Menlo").FontSize(11).Width(16).TextAlign(n.End)
			}
			// The action slot has no text baseline when the pin is hidden. Keep
			// it out of baseline alignment so its bottom cannot lower the title.
			n.Column(c).Width(28).Height(20).AlignSelf(n.Center).Children(func() {
				if r.pinned && !moving && !row.Hovered() && !row.FocusWithin() && !row.MenuOpen() {
					inlineSymbol(c.Key("pin-mark"), navigationPinIcon, s.Title, n.Font{Family: t.Font, Size: 13}, 13, t.TextMuted).Width(28).TextAlign(n.Center).Transition(fadeMotion(c, 120*time.Millisecond))
				}
				// Match the title's visible glyph center while retaining a 28px hit area.
				top := (float32(20)-28)/2 - inlineCenterOffset(s.Title, n.Font{Family: t.Font, Size: 13})
				more := iconButton(c.Key("actions"), moreActionsIcon, "会话菜单："+s.Title).Absolute().Top(top).Size(28, 28).Menu(func(menu *n.Menu) { v.sessionMenu(menu, s, r.ref) })
				if moving || !row.Hovered() && !row.FocusWithin() && !row.MenuOpen() {
					more.Opacity(0)
				}
			})
		})
	})
	if row.FocusWithin() {
		if c.Shortcut(n.Alt, n.KeyUp) {
			v.navigationMove(r.ref, -1)
		}
		if c.Shortcut(n.Alt, n.KeyDown) {
			v.navigationMove(r.ref, 1)
		}
	}
	if v.canManage(m.WorkspaceConnection(s.WorkspaceID)) && (row.DoubleClicked() || row.Focused() && c.Shortcut(0, n.KeyF2)) {
		v.rename = &sessionRename{Session: s, Title: s.Title, Connection: m.WorkspaceConnection(s.WorkspaceID), Open: true}
	}
	if row.Clicked() {
		row.Focus()
		v.workspaceEditor = nil
		v.skillReturn = nil
		v.navigateSession(s.ID, s.WorkspaceID)
		v.devices = false
	}
}

func sessionNavigationDescription(s domain.Session, pending int, selected, finishing bool) string {
	status := "已暂停"
	switch {
	case s.ReadOnlyHistory():
		status = "只读会话"
	case pending > 0:
		status = fmt.Sprintf("%d 项待处理", pending)
	case s.Status.Active() || finishing:
		status = "正在执行"
	case s.Status == domain.ExecutionCompleted:
		status = "已完成"
	case s.Status == domain.ExecutionInterrupted || s.Status == domain.ExecutionAbandoned:
		status = "已停止"
	case s.Status == domain.ExecutionFailed:
		status = "执行失败"
	case s.Status == domain.ExecutionUnknown:
		status = "执行状态待核实"
	}
	if selected {
		status = "已选中，" + status
	}
	return status
}

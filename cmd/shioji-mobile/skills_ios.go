package main

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileSkillScope struct {
	device, epoch, harness, installation, directory string
	revision                                        int64
}

type mobileSkillRequests struct {
	list, change mobileRequest
	detail       *mobileRequest
}

type mobileManagedSkills struct {
	workspace, harness string
	scope              mobileSkillScope
	requests           *mobileSkillRequests
	catalog            domain.SkillInventory
	document           domain.SkillDocument
	detail, search     string
	loaded             bool
	listScroll         ui.ScrollState
	detailScroll       ui.ScrollState
}

func (s *mobileManagedSkills) currentScope(a *mobileApp, agent domain.HarnessInfo) mobileSkillScope {
	for _, entry := range a.model.Readiness.Harnesses {
		if entry.ID == agent.ID {
			agent = entry
			break
		}
	}
	if agent.ID == "" || !agent.Capabilities.SkillManagement {
		return mobileSkillScope{}
	}
	raw, _ := json.Marshal(agent.Installation)
	scope := mobileSkillScope{device: a.model.Snapshot.DeviceID, epoch: a.model.ConnectionEpoch, harness: agent.ID, installation: string(raw)}
	if s.workspace != "" {
		ws := a.workspace(s.workspace)
		if ws.ID == "" || ws.Archived || ws.DeviceID != scope.device || len(ws.Folders) == 0 {
			return mobileSkillScope{}
		}
		scope.revision, scope.directory = ws.Revision, ws.Folders[0]
	}
	return scope
}

func (s *mobileManagedSkills) ready(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo) bool {
	return a.pageCurrent(w) && a.model.Connected && a.model.WorkerMinor >= 25 &&
		s.harness == agent.ID && s.scope.harness != "" && s.currentScope(a, agent) == s.scope
}

func (s *mobileManagedSkills) load(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo) {
	if !s.ready(a, w, agent) || s.requests.list.busy || s.requests.change.busy {
		return
	}
	req := s.requests
	req.detail = &mobileRequest{}
	s.loaded = false
	receive := func(next domain.SkillInventory) error {
		if s.requests != req || !s.ready(a, w, agent) {
			return nil
		}
		sort.SliceStable(next.Entries, func(i, j int) bool {
			a, b := next.Entries[i], next.Entries[j]
			if a.Scope != b.Scope {
				return mobileSkillScopeOrder(a.Scope) < mobileSkillScopeOrder(b.Scope)
			}
			if a.Directory != b.Directory {
				return a.Directory < b.Directory
			}
			return a.Name < b.Name
		})
		req.change.err = ""
		s.catalog, s.loaded = next, true
		s.read(a, w, agent)
		return nil
	}
	in := protocol.SkillManagementInput{WorkspaceID: s.workspace, HarnessID: agent.ID}
	if a.preview {
		req.list.err = ""
		_ = receive(a.previewSkillInventory(in))
		w.Invalidate()
		return
	}
	a.pageJob(w, &req.list, "runtime.skills.list", in, decodePage(receive))
}

func (s *mobileManagedSkills) read(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo) {
	req, id := s.requests, s.detail
	if id == "" || !s.ready(a, w, agent) || !s.loaded || req.detail.busy {
		return
	}
	if !slices.ContainsFunc(s.catalog.Entries, func(e domain.ManagedSkill) bool { return e.ID == id }) {
		s.document = domain.SkillDocument{}
		req.detail.err = "此 Skill 已不在当前来源中"
		return
	}
	detail := req.detail
	receive := func(doc domain.SkillDocument) error {
		if s.requests != req || req.detail != detail || s.detail != id || !s.ready(a, w, agent) {
			return nil
		}
		if doc.ID != id {
			return errors.New("Skill 内容与当前来源不一致")
		}
		s.document = doc
		return nil
	}
	if a.preview {
		_ = receive(previewSkillDocument(id))
		w.Invalidate()
		return
	}
	in := protocol.SkillManagementInput{WorkspaceID: s.workspace, HarnessID: agent.ID, SkillChange: domain.SkillChange{ID: id}}
	a.pageJob(w, detail, "runtime.skills.read", in, decodePage(receive))
}

func (s *mobileManagedSkills) writable(entry domain.ManagedSkill) bool {
	return entry.CanEnable && (s.workspace != "" && entry.Scope == domain.SkillProject || s.workspace == "" && entry.Scope == domain.SkillUser)
}

func (s *mobileManagedSkills) change(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo, entry domain.ManagedSkill, enabled bool) {
	req := s.requests
	if !s.ready(a, w, agent) || !s.writable(entry) || !s.loaded || s.catalog.Revision == "" || req.change.busy || req.change.err != "" {
		return
	}
	in := protocol.SkillManagementInput{WorkspaceID: s.workspace, HarnessID: agent.ID,
		SkillChange: domain.SkillChange{ID: entry.ID, Revision: s.catalog.Revision, Enabled: enabled}}
	receive := func(result domain.SkillChangeResult) error {
		if s.requests != req || !s.ready(a, w, agent) {
			return nil
		}
		if !result.Saved || result.Enabled != enabled {
			return errors.New("Skill 配置未确认")
		}
		s.load(a, w, agent)
		return nil
	}
	if a.preview {
		if err := receive(a.previewChangeSkill(in)); err != nil {
			req.change.err = err.Error()
			w.Invalidate()
		}
		return
	}
	a.pageJob(w, &req.change, "runtime.skills.change", in, decodePage(receive))
}

func (s *mobileManagedSkills) workspaceView(a *mobileApp, c *ui.Context, w *mygo.Window) {
	var agents []domain.HarnessInfo
	for _, entry := range a.model.Readiness.Harnesses {
		if entry.Configured && entry.Capabilities.SkillManagement {
			agents = append(agents, entry)
		}
	}
	var selected domain.HarnessInfo
	if len(agents) > 0 {
		selected = agents[0]
		for _, agent := range agents {
			if agent.ID == s.harness {
				selected = agent
				break
			}
		}
	}
	s.harness = selected.ID
	s.view(a, c, w, selected, agents, false)
}

func (s *mobileManagedSkills) view(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, agents []domain.HarnessInfo, unsaved bool) {
	scope := s.currentScope(a, agent)
	if scope != s.scope || s.requests == nil {
		previous := s.scope
		previous.epoch = scope.epoch
		if s.requests != nil && previous != scope {
			s.catalog, s.document = domain.SkillInventory{}, domain.SkillDocument{}
			s.detail = ""
			s.listScroll, s.detailScroll = ui.ScrollState{}, ui.ScrollState{}
		}
		s.scope, s.requests, s.loaded = scope, &mobileSkillRequests{detail: &mobileRequest{}}, false
	}
	mobileContent(c, contentMaxWidth).Grow(1).Basis(0).MinHeight(0).Padding(0, pageInset).Children(func() {
		switch {
		case unsaved:
			mobileMessage(c, "保存 Agent 配置后查看 Skills")
		case !a.model.Connected && len(s.catalog.Entries) == 0:
			mobileMessage(c, "连接执行设备后查看 Skills")
		case a.model.Connected && a.model.WorkerMinor < 25:
			mobileMessage(c, "更新设备执行服务后才能管理 Skills")
		case scope.harness == "":
			message := "此 Agent 不支持 Skills 管理"
			if s.workspace != "" {
				message = "此工作区没有可检查的 Skills 来源"
			}
			mobileMessage(c, message)
		default:
			if !s.loaded && s.requests.list.err == "" && s.requests.change.err == "" {
				s.load(a, w, agent)
			}
			page, index := "list", 0
			if s.detail != "" {
				page, index = "detail", 1
			}
			mobileSettingsPanel(c.Key("skills-panel"), page, index, func() {
				if s.detail == "" {
					s.list(a, c, w, agent, agents)
				} else {
					s.showDetail(a, c, w, agent)
				}
			})
		}
	})
}

func (s *mobileManagedSkills) refreshButton(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo) {
	action(c, "刷新", func() { s.load(a, w, agent) }).Label("刷新 Skills").KeepFocus().PaddingX(4).
		Disabled(!a.model.Connected || s.requests.list.busy || s.requests.change.busy)
}

func (s *mobileManagedSkills) notice(a *mobileApp, c *ui.Context) {
	req := s.requests
	message := ""
	switch {
	case !a.model.Connected:
		message = "设备未连接，显示上次结果"
	case req.change.busy:
		message = "正在保存…"
	case req.list.busy:
		message = "正在读取 Skills…"
	case req.change.err != "":
		message = "保存未确认，显示上次状态。请刷新核对"
	case req.list.err != "":
		message = "无法读取 Skills，显示上次结果。请刷新重试"
	case s.detail != "" && req.detail.busy:
		message = "正在读取 Skill 内容…"
	case s.detail != "" && req.detail.err != "":
		message = req.detail.err
	}
	if message != "" {
		ui.Column(c.Key("skill-notice")).Transition(mobilePanelTransition).Children(func() { mobileMessage(c, message) })
	}
}

func (s *mobileManagedSkills) list(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, agents []domain.HarnessInfo) {
	p := palette(c)
	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.TextInputBase(c.Key("search"), &s.search).Label("搜索 Skills").Placeholder("搜索 Skills").
			TextColor(p.ink).PlaceholderColor(p.secondary).FontSize(controlTextSize * c.Preferences().TextScale).
			MinHeight(mobileTapSize(c)).Grow(1).MinWidth(0).PaddingX(10).Radius(10).Background(p.inset)
		if len(agents) > 1 {
			iconAction(c.Key("agent"), agent.Name, "Skills Agent", nil, 0, pickerSymbol, 12, 13, 400, nil).
				PaddingX(3).MaxWidthPercent(30).Menu(func(m *ui.Menu) {
				for _, item := range agents {
					menuChoice(m, item.Name, item.ID == agent.ID, func() { s.harness = item.ID })
				}
			})
		}
		s.refreshButton(a, c.Key("refresh"), w, agent)
	})
	s.notice(a, c)
	ui.Scroll(c.Key("inventory")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.listScroll).Label("Skills 列表").Children(func() {
		query := strings.ToLower(strings.TrimSpace(s.search))
		group := [2]string{}
		count := 0
		for _, entry := range s.catalog.Entries {
			if s.workspace == "" && entry.Scope == domain.SkillProject ||
				!strings.Contains(strings.ToLower(entry.Name+" "+entry.Description+" "+entry.Directory+" "+mobileSkillScopeTitle(entry.Scope)), query) {
				continue
			}
			key := [2]string{string(entry.Scope), entry.Directory}
			if count == 0 || key != group {
				group = key
				title := mobileSkillScopeTitle(entry.Scope)
				if s.workspace != "" && entry.Scope != domain.SkillProject {
					title += " · 继承"
				}
				ui.Row(c.Key(group)).MinHeight(32).AlignItems(ui.Center).Gap(8).Children(func() {
					oneLine(c, title, 12, p.secondary).FontWeight(600).Shrink(0)
					if entry.Directory != "" {
						oneLine(c, entry.Directory, 11, p.secondary).Grow(1).MinWidth(0).TextAlign(ui.End)
					}
				})
			}
			count++
			row := c.Key(entry.ID)
			rowDivider(ui.Row(row).MinHeight(max(contentRowHeight, mobileTapSize(c))).AlignItems(ui.Center).Gap(6), p.line).Children(func() {
				state := "已停用"
				if entry.Enabled {
					state = "已启用"
				}
				mobileButton(row, "查看 Skill："+entry.Name, func() {
					s.detail, s.document, s.detailScroll = entry.ID, domain.SkillDocument{}, ui.ScrollState{}
					s.requests.detail = &mobileRequest{}
					s.read(a, w, agent)
				}).Value(state).MinHeight(max(contentRowHeight, mobileTapSize(c))).Grow(1).MinWidth(0).AlignItems(ui.Center).KeepFocus().Gap(6).Children(func() {
					ui.Column(row).Grow(1).MinWidth(0).Gap(3).PassThrough().Children(func() {
						oneLine(row, entry.Name, bodyTextSize, p.ink).PassThrough()
						oneLine(row, entry.Description, 12, p.secondary).PassThrough()
					})
					if !s.writable(entry) {
						oneLine(row, state, 12, p.secondary).Shrink(0).PassThrough()
					}
					ui.Icon(row, rightSymbol).Size(13, 13).TextColor(p.secondary).PassThrough()
				})
				if s.writable(entry) {
					s.toggle(a, row.Key("toggle"), w, agent, entry)
				}
			})
		}
		if count == 0 && s.loaded {
			message := "未发现 Skills"
			if query != "" {
				message = "没有匹配的 Skills"
			}
			mobileMessage(c, message)
		}
		for i, warning := range s.catalog.Warnings {
			mobileMessage(c.Key(i), warning)
		}
	})
}

func (s *mobileManagedSkills) toggle(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, entry domain.ManagedSkill) {
	enabled := entry.Enabled
	mobileSwitch(c, "启停 Skill："+entry.Name, &enabled, func() { s.change(a, w, agent, entry, enabled) }).
		Disabled(!a.model.Connected || !s.loaded || s.catalog.Revision == "" || s.requests.change.busy || s.requests.change.err != "")
}

func (s *mobileManagedSkills) showDetail(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo) {
	p := palette(c)
	index := slices.IndexFunc(s.catalog.Entries, func(entry domain.ManagedSkill) bool { return entry.ID == s.detail })
	entry := domain.ManagedSkill{}
	if index >= 0 {
		entry = s.catalog.Entries[index]
	}
	title := entry.Name
	if title == "" {
		title = "Skill"
	}
	rowDivider(ui.Row(c).MinHeight(mobileTapSize(c)).Gap(4).AlignItems(ui.Center), p.line).Children(func() {
		iconButton(c.Key("back"), "返回 Skills 列表", backSymbol, 18, func() {
			s.detail = ""
			s.requests.detail = &mobileRequest{}
		})
		oneLine(c, title, 16, p.ink).FontWeight(550).Grow(1).MinWidth(0)
		if s.workspace != "" && entry.ID != "" && entry.Scope != domain.SkillProject {
			action(c.Key("manage"), "设备管理", func() {
				a.openAgentSettingsAt(w, agent, "Skills", entry.ID, func() { s.load(a, w, agent) })
			}).Label("管理设备 Skills").PaddingX(3).Disabled(!a.model.Connected || s.requests.change.busy)
		} else if s.writable(entry) {
			s.toggle(a, c.Key("toggle"), w, agent, entry)
		}
		s.refreshButton(a, c.Key("refresh"), w, agent)
	})
	s.notice(a, c)
	ui.Scroll(c.Key("document")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.detailScroll).Label("Skill 说明").Children(func() {
		if entry.ID != "" {
			mobileMetadataField(c.Key("scope"), "来源", mobileSkillScopeTitle(entry.Scope), "")
			mobileMetadataField(c.Key("path"), "文件", entry.Path, "复制 Skill 路径")
			if entry.ReadOnlyReason != "" && (s.workspace == "" || entry.Scope == domain.SkillProject) {
				mobileMessage(c, entry.ReadOnlyReason)
			}
		}
		if s.document.ID == s.detail && s.document.Text != "" {
			ui.Row(c.Key("document-actions")).Justify(ui.End).Children(func() {
				mobileCopyButton(c, "复制 Skill 说明", s.document.Text)
			})
			markdown.View(c.Key(s.detail), s.document.Text, &markdown.Options{WrapCode: true, ShareSelection: true,
				Theme: &markdown.Theme{FontSize: bodyTextSize * c.Preferences().TextScale, CodeFont: a.codeFont()},
			}).FillWidth().Selectable().Margin(0, 0, 24)
		}
	})
}

func mobileSkillScopeOrder(scope domain.SkillScope) int {
	switch scope {
	case domain.SkillProject:
		return 0
	case domain.SkillUser:
		return 1
	case domain.SkillBuiltIn:
		return 2
	case domain.SkillManaged:
		return 3
	default:
		return 4
	}
}

func mobileSkillScopeTitle(scope domain.SkillScope) string {
	return [...]string{"项目级", "用户级", "内置", "受管理", "其他来源"}[mobileSkillScopeOrder(scope)]
}

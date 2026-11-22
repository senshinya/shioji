package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (s *mobileHooks) view(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, agents []domain.HarnessInfo, unsaved bool) {
	binding := s.currentBinding(a, agent)
	mobileContent(c, contentMaxWidth).Grow(1).Basis(0).MinHeight(0).PaddingX(pageInset).Children(func() {
		switch {
		case unsaved:
			mobileMessage(c, "保存 Agent 配置后查看 Hooks")
		case a.model.Connected && a.model.WorkerMinor < mobileHookMinor:
			mobileMessage(c, "更新设备执行服务后可管理 Hooks")
		case binding.slot.device == "":
			message := "此范围没有可检查的 Hooks"
			if !a.model.Connected {
				message = "连接执行设备后查看 Hooks"
			}
			mobileMessage(c, message)
		default:
			s.bind(a, binding)
			if !s.visited && a.model.Connected {
				s.refresh(a, w, agent)
			}
			page, index := "list", 0
			if s.review {
				page, index = "review", 1
			} else if s.detail != "" {
				page, index = "detail", 1
			}
			mobileSettingsPanel(c.Key("hooks-panels"), page, index, func() {
				switch page {
				case "review":
					s.showReview(a, c, w, agent)
				case "detail":
					s.showDetail(a, c, w, agent)
				default:
					s.showList(a, c, w, agent, agents)
				}
			})
		}
	})
}

func (s *mobileHooks) feedback(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo) {
	pending := s.receipts.pending
	message := s.message
	switch {
	case !a.model.Connected:
		message = "设备未连接，显示上次结果"
	case s.request == "runtime.hooks.change":
		message = "正在保存…"
	case s.request == "runtime.hooks.operation":
		message = "正在查询操作回执…"
	case s.request == "runtime.hooks.list" && message == "":
		message = "正在读取 Hooks…"
	}
	if message != "" {
		ui.Column(c.Key("hook-notice")).Transition(mobilePanelTransition).Children(func() { mobileMessage(c, message) })
	}
	if pending != nil && s.request == "" {
		ui.Row(c.Key("hook-recovery")).Gap(6).Children(func() {
			action(c, "查询操作回执", func() {
				s.operation(a, w, agent, "runtime.hooks.operation", *pending)
			}).Disabled(!a.model.Connected).PaddingX(4)
			action(c, "核对最新定义", func() { s.load(a, w, agent, true) }).Disabled(!a.model.Connected).PaddingX(4)
		})
	} else if len(s.receipts.unconfirmed) > 0 && s.request == "" {
		action(c.Key("hook-prior"), "查询未确认回执", nil).AlignSelf(ui.Start).PaddingX(4).
			Disabled(!a.model.Connected).Menu(func(m *ui.Menu) {
			for i, in := range s.receipts.unconfirmed {
				title := fmt.Sprintf("未确认的操作 %d", i+1)
				if m.Item(title).Chosen() {
					s.operation(a, w, agent, "runtime.hooks.operation", in)
				}
			}
		})
	}
}

func (s *mobileHooks) refreshButton(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo) {
	action(c, "刷新", func() { s.refresh(a, w, agent) }).Label("刷新 Hooks").KeepFocus().PaddingX(4).
		Disabled(!a.model.Connected || s.request != "")
}

func (s *mobileHooks) entries() []domain.HookEntry {
	var entries []domain.HookEntry
	for _, h := range s.catalog.Entries {
		if s.workspace != "" || h.Scope != domain.HookProject {
			entries = append(entries, h)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Scope != b.Scope {
			return mobileHookScopeOrder(a.Scope) < mobileHookScopeOrder(b.Scope)
		}
		if a.SourcePath != b.SourcePath {
			return a.SourcePath < b.SourcePath
		}
		return a.Event < b.Event
	})
	return entries
}

func (s *mobileHooks) reviewable() []domain.HookEntry {
	if s.binding.archived {
		return nil
	}
	var list []domain.HookEntry
	for _, h := range s.entries() {
		if s.owned(h) && h.CanTrust && h.DefinitionRevision != "" {
			list = append(list, h)
		}
	}
	return list
}

func (s *mobileHooks) showList(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, agents []domain.HarnessInfo) {
	p := palette(c)
	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.TextInputBase(c.Key("search"), &s.query).Label("搜索 Hooks").Placeholder("搜索 Hooks").
			TextColor(p.ink).PlaceholderColor(p.secondary).FontSize(controlTextSize * c.Preferences().TextScale).
			MinHeight(mobileTapSize(c)).Grow(1).MinWidth(0).PaddingX(10).Radius(10).Background(p.inset)
		if count := len(s.reviewable()); count != 0 && a.model.WorkerMinor >= protocol.HookBatchMinor {
			action(c.Key("review"), fmt.Sprintf("待审核 %d", count), func() {
				s.review = true
				s.selected = make(map[string]bool)
				s.reviewScroll = ui.ScrollState{}
			}).Label("审核待审批 Hooks").PaddingX(4)
		}
		s.refreshButton(a, c.Key("refresh"), w, agent)
	})
	if len(agents) > 1 {
		iconAction(c.Key("agent"), agent.Name, "Hooks Agent", nil, 0, pickerSymbol, 12, 13, 400, nil).
			AlignSelf(ui.End).PaddingX(3).Menu(func(m *ui.Menu) {
			for _, item := range agents {
				menuChoice(m, item.Name, item.ID == agent.ID, func() { s.harness = item.ID })
			}
		})
	}
	s.feedback(a, c, w, agent)
	if s.session != "" {
		status := "会话加载状态未知"
		if s.loaded && a.model.Connected && s.catalog.RuntimeKnown {
			status = "已核对当前会话"
		}
		label(c, status, 12, p.secondary).PaddingY(6)
	}
	ui.Scroll(c.Key("list")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.listScroll).Label("Hooks 列表").Children(func() {
		query := strings.ToLower(strings.TrimSpace(s.query))
		count := 0
		var last [2]string
		for _, h := range s.entries() {
			if !strings.Contains(strings.ToLower(h.Event+" "+h.SourcePath+" "+h.Source+" "+mobileHookDefinition(h)+" "+h.Matcher), query) {
				continue
			}
			group := [2]string{string(h.Scope), h.SourcePath}
			if count == 0 || group != last {
				last = group
				ui.Row(c.Key(group)).MinHeight(32).AlignItems(ui.Center).Gap(7).Children(func() {
					name := mobileHookScopeName(h.Scope)
					if s.inherited(h) {
						name += " · 继承"
					}
					oneLine(c, name, 12, p.secondary).FontWeight(600).Shrink(0)
					oneLine(c, h.SourcePath, 11, p.secondary).Grow(1).MinWidth(0).TextAlign(ui.End)
					if s.inherited(h) {
						action(c, "管理", func() { s.manageDevice(a, w, agent, "") }).Label("管理继承的 Hooks").
							Disabled(!a.model.Connected).PaddingX(2)
					}
				})
			}
			count++
			enabled := "已停用"
			if h.Enabled {
				enabled = "已启用"
			}
			status := mobileHookTrust(h.Trust)
			row := c.Key(h.ID)
			rowDivider(mobileButton(row, "查看 Hook："+h.Event, func() {
				s.detail, s.metadata, s.detailScroll = h.ID, false, ui.ScrollState{}
			}).KeepFocus().MinHeight(64).Gap(6).AlignItems(ui.Center), p.line).
				Value(status + " · " + enabled).Children(func() {
				ui.Column(row).Grow(1).MinWidth(0).Gap(3).PassThrough().Children(func() {
					oneLine(row, h.Event, bodyTextSize, p.ink).PassThrough()
					oneLine(row, mobileHookDefinition(h), 12, p.secondary).Font("monospace").PassThrough()
				})
				ui.Column(row).AlignItems(ui.End).Gap(3).Shrink(0).PassThrough().Children(func() {
					oneLine(row, status, 12, p.secondary).PassThrough()
					oneLine(row, enabled, 11, p.secondary).PassThrough()
				})
				ui.Icon(row, rightSymbol).Size(13, 13).TextColor(p.secondary).PassThrough()
			})
		}
		if count == 0 && s.loaded {
			message := "没有 Hook"
			if query != "" {
				message = "没有匹配的 Hook"
			}
			mobileMessage(c, message)
		}
		for i, warning := range s.catalog.Warnings {
			mobileMessage(c.Key(i), warning)
		}
	})
}

func (s *mobileHooks) subpageHeader(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, title string, back func()) {
	p := palette(c)
	rowDivider(ui.Row(c).MinHeight(mobileTapSize(c)).Gap(4).AlignItems(ui.Center), p.line).Children(func() {
		iconButton(c.Key("back"), "返回 Hooks 列表", backSymbol, 18, back)
		oneLine(c, title, 16, p.ink).FontWeight(550).Grow(1).MinWidth(0)
		s.refreshButton(a, c.Key("refresh"), w, agent)
	})
}

func (s *mobileHooks) showDetail(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo) {
	p := palette(c)
	h := domain.HookEntry{}
	if i := slices.IndexFunc(s.catalog.Entries, func(entry domain.HookEntry) bool { return entry.ID == s.detail }); i >= 0 {
		h = s.catalog.Entries[i]
	}
	title := h.Event
	if title == "" {
		title = "Hook"
	}
	s.subpageHeader(a, c, w, agent, title, func() { s.detail = "" })
	s.feedback(a, c, w, agent)
	ui.Scroll(c.Key("detail")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.detailScroll).Label("Hook 定义").Children(func() {
		if h.ID == "" {
			if s.loaded {
				mobileMessage(c, "定义已变化，请返回列表重新审核")
			}
			return
		}
		label(c, mobileHookTrust(h.Trust), 13, p.secondary).PaddingY(9)
		a.codeResult(c, mobileHookDefinition(h))
		mobileMetadataField(c.Key("source"), "来源", h.SourcePath, "复制 Hook 来源路径")
		mobileMetadataField(c.Key("matcher"), "匹配", h.Matcher, "")
		if h.StatusMessage != "" {
			mobileMessage(c, h.StatusMessage)
		}
		iconAction(c.Key("metadata"), "详细信息", "Hook 详细信息", nil, 0, pickerSymbol, 12, 13, 500,
			func() { s.metadata = !s.metadata }).Expanded(s.metadata).AlignSelf(ui.Start).PaddingX(0)
		if s.metadata {
			ui.Column(c.Key("details")).Transition(mobilePanelTransition).Children(func() {
				mobileMetadataField(c.Key("device"), "设备", a.deviceName(), "")
				mobileMetadataField(c.Key("cwd"), "执行目录", s.catalog.Directory, "")
				mobileMetadataField(c.Key("kind"), "类型", h.Kind, "")
				if h.TimeoutSeconds != 0 {
					mobileMetadataField(c.Key("timeout"), "超时", fmt.Sprintf("%d 秒", h.TimeoutSeconds), "")
				}
				mode := "同步"
				if h.Async {
					mode = "异步"
				}
				mobileMetadataField(c.Key("mode"), "执行方式", mode, "")
				mobileMetadataField(c.Key("plugin"), "插件", h.Plugin, "")
				mobileMetadataField(c.Key("fingerprint"), "指纹", h.Fingerprint, "")
			})
		}
		if h.ReadOnlyReason != "" && !s.inherited(h) {
			mobileMessage(c, h.ReadOnlyReason)
		}
	})
	revision := s.catalog.Revision
	if h.ID != "" && (s.inherited(h) || s.owned(h) && !s.binding.archived && (h.CanEnable || h.CanTrust)) {
		ui.Row(c.Key("actions")).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(8).Shrink(0).
			BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
			if s.inherited(h) {
				ui.Spacer(c).Grow(1)
				action(c, "在设备设置中管理", func() { s.manageDevice(a, w, agent, h.ID) }).
					Label("管理设备 Hooks").Disabled(!a.model.Connected)
				return
			}
			if h.CanEnable {
				enabled := h.Enabled
				label(c, "启用", 13, p.secondary)
				mobileSwitch(c, "启停 Hook："+h.Event, &enabled, func() {
					s.change(a, w, agent, revision, domain.HookChange{Action: domain.HookSetEnabled, ID: h.ID,
						DefinitionRevision: h.DefinitionRevision, Enabled: enabled})
				}).Disabled(!s.writable(a))
			}
			ui.Spacer(c).Grow(1)
			if h.CanTrust {
				action(c, "信任此版本", func() {
					s.change(a, w, agent, revision, domain.HookChange{Action: domain.HookTrustDefinition,
						ID: h.ID, DefinitionRevision: h.DefinitionRevision})
				}).Disabled(!s.writable(a))
			}
		})
	}
}

func (s *mobileHooks) manageDevice(a *mobileApp, w *mygo.Window, agent domain.HarnessInfo, id string) {
	a.openAgentSettingsAt(w, agent, "Hooks", id, func() { s.refresh(a, w, agent) })
}

func (s *mobileHooks) showReview(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo) {
	p := palette(c)
	hooks := s.reviewable()
	var all, selected []domain.HookDefinitionRef
	writable := s.writable(a) && a.model.WorkerMinor >= protocol.HookBatchMinor
	revision := s.catalog.Revision
	s.subpageHeader(a, c, w, agent, "待审核", func() { s.review = false; clear(s.selected) })
	s.feedback(a, c, w, agent)
	ui.Scroll(c.Key("review")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.reviewScroll).Label("待审核的 Hook 定义").Children(func() {
		for _, h := range hooks {
			definition := domain.HookDefinitionRef{ID: h.ID, DefinitionRevision: h.DefinitionRevision}
			all = append(all, definition)
			if s.selected[h.ID] {
				selected = append(selected, definition)
			}
			ui.Column(c.Key(h.ID)).Gap(3).PaddingY(7).Children(func() {
				remoteChoiceRow(c, h.Event, mobileHookTrust(h.Trust), s.selected[h.ID], func() {
					if s.selected == nil {
						s.selected = make(map[string]bool)
					}
					s.selected[h.ID] = !s.selected[h.ID]
				}).Label("选择 Hook：" + h.Event).Disabled(!writable)
				a.codeResult(c, mobileHookDefinition(h))
				if h.SourcePath != "" {
					label(c.Key("source"), h.SourcePath, 12, p.secondary).Font("monospace").PaddingY(3).Selectable()
				}
				if h.Matcher != "" {
					label(c.Key("matcher"), "匹配 "+h.Matcher, 12, p.secondary).Selectable()
				}
			})
		}
		if len(hooks) == 0 && s.loaded {
			mobileMessage(c, "没有待审核的 Hooks")
		}
	})
	ui.Row(c).MinHeight(mobileTapSize(c)).Gap(8).Justify(ui.End).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
		allTitle, selectedTitle := "信任全部", "信任所选"
		if len(all) > 0 {
			allTitle = fmt.Sprintf("信任全部（%d）", len(all))
		}
		if len(selected) > 0 {
			selectedTitle = fmt.Sprintf("信任所选（%d）", len(selected))
		}
		action(c.Key("all"), allTitle, func() {
			s.change(a, w, agent, revision, domain.HookChange{Action: domain.HookTrustDefinitions, Definitions: all})
		}).Label("信任全部").Disabled(!writable || len(all) == 0)
		action(c.Key("selected"), selectedTitle, func() {
			s.change(a, w, agent, revision, domain.HookChange{Action: domain.HookTrustDefinitions, Definitions: selected})
		}).Label("信任所选").Disabled(!writable || len(selected) == 0)
	})
}

func mobileHookScopeOrder(scope domain.HookScope) int {
	switch scope {
	case domain.HookProject:
		return 0
	case domain.HookUser:
		return 1
	case domain.HookPlugin:
		return 2
	case domain.HookManaged:
		return 3
	default:
		return 4
	}
}

func mobileHookScopeName(scope domain.HookScope) string {
	return [...]string{"项目级", "用户级", "插件", "受管理", "来源未知"}[mobileHookScopeOrder(scope)]
}

func mobileHookTrust(trust domain.HookTrust) string {
	switch trust {
	case domain.HookTrusted:
		return "已信任"
	case domain.HookModified:
		return "已修改"
	case domain.HookPolicy:
		return "策略管理"
	case domain.HookUntrusted:
		return "待审核"
	default:
		return "信任状态未知"
	}
}

func mobileHookDefinition(h domain.HookEntry) string {
	switch {
	case h.Kind == "command" && h.Command != "":
		return h.Command
	case h.Kind == "tool" && h.Service != "" && h.Tool != "":
		return h.Service + " / " + h.Tool
	default:
		return "无法读取完整定义"
	}
}

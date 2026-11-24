package main

import (
	"encoding/json"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type workspaceToolQuery struct {
	input         protocol.ToolInspectionInput
	device, epoch string
	context       string
	directory     string
	revision      int64
}

type mobileWorkspaceTools struct {
	workspaceID, harness, folder, session string
	search, detail                        string
	query                                 workspaceToolQuery
	result                                domain.ToolServiceInspection
	request                               *mobileRequest
	loaded                                bool
	listScroll, detailScroll              ui.ScrollState
}

func (s *mobileWorkspaceTools) sessions(a *mobileApp) []domain.Session {
	var sessions []domain.Session
	for _, entry := range a.model.Snapshot.Sessions {
		if !entry.Archived && entry.WorkspaceID == s.workspaceID && entry.HarnessID == s.harness && entry.Context.CWD != "" {
			sessions = append(sessions, entry)
		}
	}
	return sessions
}

func (s *mobileWorkspaceTools) currentQuery(a *mobileApp) workspaceToolQuery {
	ws := a.workspace(s.workspaceID)
	q := workspaceToolQuery{
		input:  protocol.ToolInspectionInput{WorkspaceID: s.workspaceID, HarnessID: s.harness, Directory: s.folder, SessionID: s.session},
		device: a.model.Snapshot.DeviceID, epoch: a.model.ConnectionEpoch, revision: ws.Revision, directory: s.folder,
	}
	if ws.ID == "" || ws.Archived || ws.DeviceID != q.device || !slices.Contains(ws.Folders, s.folder) {
		return workspaceToolQuery{}
	}
	for _, agent := range a.model.Readiness.Harnesses {
		if agent.ID != s.harness || !agent.Configured || !agent.Capabilities.ToolServiceInspection {
			continue
		}
		if s.session != "" {
			session := a.sessionByID(s.session)
			if session.Archived || session.WorkspaceID != s.workspaceID || session.HarnessID != s.harness || session.Context.CWD == "" {
				return workspaceToolQuery{}
			}
			q.input.Directory, q.directory = "", session.Context.CWD
			raw, _ := json.Marshal(session.Context)
			q.context = session.ThreadID + "\x00" + string(raw)
		}
		raw, _ := json.Marshal(agent.Installation)
		q.context += "\x00" + string(raw)
		return q
	}
	return workspaceToolQuery{}
}

func (s *mobileWorkspaceTools) load(a *mobileApp, w *mygo.Window) {
	q := s.currentQuery(a)
	req := s.request
	if q.input.WorkspaceID == "" || s.query != q || req == nil || !a.model.Connected || a.model.WorkerMinor < 26 || req.busy {
		return
	}
	if a.preview {
		s.result = previewWorkspaceTools(q, a.workspace(s.workspaceID).Folders[0])
		s.loaded = true
		return
	}
	a.pageJob(w, req, "runtime.tools.inspect", q.input, decodePage(func(result domain.ToolServiceInspection) error {
		if s.request != req || s.currentQuery(a) != q || !a.model.Connected {
			return nil
		}
		sort.SliceStable(result.Entries, func(i, j int) bool {
			a, b := result.Entries[i], result.Entries[j]
			if a.ConfigurationPath != b.ConfigurationPath {
				return a.ConfigurationPath < b.ConfigurationPath
			}
			return a.Name < b.Name
		})
		s.result, s.loaded = result, true
		return nil
	}))
}

func (s *mobileWorkspaceTools) selectScope(harness, folder, session string) {
	if s.harness == harness && s.folder == folder && s.session == session {
		return
	}
	s.harness, s.folder, s.session = harness, folder, session
	s.detail = ""
	s.listScroll, s.detailScroll = ui.ScrollState{}, ui.ScrollState{}
}

func (s *mobileWorkspaceTools) view(a *mobileApp, c *ui.Context, w *mygo.Window) {
	var choices []domain.HarnessInfo
	var agent domain.HarnessInfo
	for _, info := range a.model.Readiness.Harnesses {
		if info.Configured && info.Capabilities.ToolServiceInspection {
			choices = append(choices, info)
			if info.ID == s.harness || agent.ID == "" {
				agent = info
			}
		}
	}
	ws := a.workspace(s.workspaceID)
	switch {
	case a.model.WorkerMinor < 26 && a.model.Connected:
		mobileContent(c, contentMaxWidth).PaddingX(pageInset).Children(func() {
			mobileMessage(c, "更新设备执行服务后才能查看工作区 MCP")
		})
		return
	case agent.ID == "" || ws.Archived || len(ws.Folders) == 0:
		message := "此设备暂无支持 MCP 来源检查的 Agent"
		if !a.model.Connected {
			message = "连接执行设备后查看 MCP 来源"
		} else if ws.Archived || len(ws.Folders) == 0 {
			message = "此工作区没有可检查的目录"
		}
		mobileContent(c, contentMaxWidth).PaddingX(pageInset).Children(func() { mobileMessage(c, message) })
		return
	}
	if s.harness != agent.ID {
		s.selectScope(agent.ID, s.folder, "")
	}
	if !slices.Contains(ws.Folders, s.folder) {
		s.selectScope(agent.ID, ws.Folders[0], s.session)
	}
	if s.session != "" && !slices.ContainsFunc(s.sessions(a), func(entry domain.Session) bool { return entry.ID == s.session }) {
		s.selectScope(agent.ID, s.folder, "")
	}
	if q := s.currentQuery(a); s.query != q || s.request == nil {
		s.query, s.loaded = q, false
		s.result = domain.ToolServiceInspection{}
		s.request = &mobileRequest{}
	}
	if !s.loaded && !s.request.busy && s.request.err == "" {
		s.load(a, w)
	}
	live := a.model.Connected && s.loaded && !s.request.busy && s.request.err == "" && s.result.RuntimeKnown && s.session != ""
	mobileContent(c, contentMaxWidth).Grow(1).Basis(0).MinHeight(0).Padding(0, pageInset).Clip().Children(func() {
		if s.detail == "" {
			ui.Column(c.Key("source-list")).Fill().Transition(mobileHorizontalTransition(-100)).Children(func() {
				s.list(a, c, w, ws, agent, choices, live)
			})
		} else {
			ui.Column(c.Key("source-detail")).Fill().Transition(mobileHorizontalTransition(100)).Children(func() {
				s.showDetail(a, c, w, agent, live)
			})
		}
	})
}

func (s *mobileWorkspaceTools) feedback(a *mobileApp, c *ui.Context) {
	switch {
	case !a.model.Connected:
		mobileMessage(c, "设备未连接")
	case s.request.err != "":
		s.request.status(c)
	case s.request.busy:
		mobileMessage(c, "正在检查 MCP 来源…")
	default:
		for _, warning := range s.result.Warnings {
			mobileMessage(c, warning)
		}
	}
}

func (s *mobileWorkspaceTools) list(a *mobileApp, c *ui.Context, w *mygo.Window, ws domain.Workspace, agent domain.HarnessInfo, agents []domain.HarnessInfo, live bool) {
	p := palette(c)
	ui.Row(c).AlignItems(ui.Center).Gap(6).Margin(0, 0, 8).Children(func() {
		ui.TextInputBase(c.Key("search"), &s.search).Label("搜索 MCP").Placeholder("搜索 MCP").
			TextColor(p.ink).PlaceholderColor(p.secondary).FontSize(controlTextSize * c.Preferences().TextScale).
			MinHeight(mobileTapSize(c)).Grow(1).MinWidth(0).PaddingX(10).Radius(10).Background(p.inset)
		if len(agents) > 1 {
			iconAction(c.Key("agent"), agent.Name, "MCP Agent", nil, 0, pickerSymbol, 12, 13, 400, nil).
				PaddingX(3).MaxWidthPercent(31).Menu(func(m *ui.Menu) {
				for _, next := range agents {
					menuChoice(m, next.Name, next.ID == agent.ID, func() { s.selectScope(next.ID, s.folder, "") })
				}
			})
		}
		action(c.Key("refresh"), "刷新", func() { s.load(a, w) }).Label("刷新工作区 MCP").
			Disabled(!a.model.Connected || s.request.busy).KeepFocus().PaddingX(5)
	})
	sessions := s.sessions(a)
	scope := "配置来源"
	if s.session != "" {
		scope = a.sessionByID(s.session).Title
	}
	rowDivider(ui.Row(c).Gap(4).AlignItems(ui.Center), p.line).Children(func() {
		iconAction(c.Key("scope"), scope, "MCP 查看范围", nil, 0, pickerSymbol, 12, 13, 500, nil).
			PaddingX(0).Grow(1).MinWidth(0).Menu(func(m *ui.Menu) {
			menuChoice(m, "配置来源", s.session == "", func() { s.selectScope(s.harness, s.folder, "") })
			for _, entry := range sessions {
				menuChoice(m, entry.Title, entry.ID == s.session, func() { s.selectScope(s.harness, s.folder, entry.ID) })
			}
		})
		if s.session == "" && len(ws.Folders) > 1 {
			iconAction(c.Key("folder"), path.Base(s.folder), "MCP 查询目录", folderSymbol, 15, pickerSymbol, 12, 13, 400, nil).
				PaddingX(0).MaxWidthPercent(55).Value(s.folder).Menu(func(m *ui.Menu) {
				for _, dir := range ws.Folders {
					menuChoice(m, dir, dir == s.folder, func() { s.selectScope(s.harness, dir, "") })
				}
			})
		} else {
			oneLine(c, path.Base(s.query.directory), 13, p.secondary).MaxWidthPercent(43).Label(s.query.directory)
		}
	})
	query := strings.ToLower(strings.TrimSpace(s.search))
	ui.Scroll(c.Key("entries")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.listScroll).Label("工作区 MCP 列表").Children(func() {
		s.feedback(a, c)
		count := 0
		for _, scope := range []domain.ToolServiceScope{domain.ToolServiceProject, domain.ToolServiceUser, domain.ToolServiceManaged, domain.ToolServiceOther} {
			lastSource := ""
			showHeading := true
			for _, entry := range s.result.Entries {
				if entry.Scope != scope || (s.session != "" && !entry.PrimarySource) ||
					!strings.Contains(strings.ToLower(entry.Name+" "+entry.ConfigurationPath+" "+workspaceToolConnection(entry)), query) {
					continue
				}
				count++
				if s.session == "" && (showHeading || lastSource != entry.ConfigurationPath) {
					showHeading, lastSource = false, entry.ConfigurationPath
					ui.Row(c.Key([2]string{string(scope), lastSource})).MinHeight(32).AlignItems(ui.Center).Gap(8).Children(func() {
						oneLine(c, workspaceToolScopeTitle(scope), 12, p.secondary).FontWeight(600).Shrink(0)
						if lastSource != "" {
							oneLine(c, lastSource, 11, p.secondary).Grow(1).MinWidth(0).TextAlign(ui.End)
						}
					})
				}
				secondary := workspaceToolConnection(entry)
				if live && entry.State == domain.ToolServiceReady {
					secondary = strconv.Itoa(entry.Tools) + " 个工具"
				}
				status := workspaceToolDisplayState(entry, s.session != "", live)
				row := c.Key(entry.EntryID)
				rowDivider(mobileButton(row, "查看 MCP："+entry.Name, func() {
					s.detail = entry.EntryID
					s.detailScroll = ui.ScrollState{}
				}).MinHeight(max(contentRowHeight, mobileTapSize(c))).AlignItems(ui.Center).Gap(8).PaddingY(6), p.line).
					Value(status).KeepFocus().Children(func() {
					ui.Column(row).Grow(1).MinWidth(0).Gap(3).PassThrough().Children(func() {
						oneLine(row, entry.Name, bodyTextSize, p.ink).PassThrough()
						if secondary != "" {
							oneLine(row, secondary, 12, p.secondary).PassThrough()
						}
					})
					oneLine(row, status, 12, p.secondary).Shrink(0).PassThrough()
					ui.Icon(row, rightSymbol).Size(13, 13).TextColor(p.secondary).PassThrough()
				})
			}
		}
		if count == 0 && s.loaded && !s.request.busy {
			message := "此目录没有 MCP 配置"
			if query != "" {
				message = "没有匹配的 MCP"
			}
			mobileMessage(c, message)
		}
	})
}

func (s *mobileWorkspaceTools) showDetail(a *mobileApp, c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, live bool) {
	p := palette(c)
	var entry domain.InspectedToolService
	for _, candidate := range s.result.Entries {
		if candidate.EntryID == s.detail {
			entry = candidate
			break
		}
	}
	title := entry.Name
	if title == "" {
		title = "MCP 来源"
	}
	rowDivider(ui.Row(c).Gap(4).MinHeight(mobileTapSize(c)).AlignItems(ui.Center), p.line).Children(func() {
		iconButton(c.Key("back"), "返回 MCP 列表", backSymbol, 18, func() { s.detail = "" })
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Baseline).Gap(7).Children(func() {
			oneLine(c, title, 16, p.ink).FontWeight(550).MinWidth(0).Shrink(1)
			if entry.EntryID != "" {
				oneLine(c, workspaceToolDisplayState(entry, s.session != "", live), 12, p.secondary).Shrink(0)
			}
		})
		if entry.Scope == domain.ToolServiceUser && agent.Capabilities.ToolServices {
			action(c.Key("manage"), "设备管理", func() {
				a.openAgentSettingsAt(w, agent, "MCP", "", func() {
					s.request = &mobileRequest{}
					s.loaded = false
					s.load(a, w)
					w.Invalidate()
				})
			}).Label("管理设备 MCP").PaddingX(3).Disabled(!a.model.Connected)
		}
	})
	ui.Scroll(c.Key("metadata")).Grow(1).Basis(0).MinHeight(0).TrackScroll(&s.detailScroll).
		Label("MCP 来源详情").PaddingY(8).Children(func() {
		s.feedback(a, c)
		if entry.EntryID == "" {
			if !s.request.busy {
				mobileMessage(c, "此服务已不在当前来源中")
			}
			return
		}
		if s.session == "" {
			mobileMetadataField(c.Key("scope"), "来源", workspaceToolScopeTitle(entry.Scope), "")
			mobileMetadataField(c.Key("path"), "配置文件", entry.ConfigurationPath, "复制配置路径")
			mobileMetadataField(c.Key("connection"), "连接", workspaceToolConnection(entry), "")
			if cfg := entry.Configuration; cfg != nil && cfg.StartupSeconds > 0 {
				mobileMetadataField(c.Key("startup"), "启动超时", strconv.FormatFloat(cfg.StartupSeconds, 'f', -1, 64)+" 秒", "")
			}
		} else {
			for _, source := range s.result.Entries {
				if source.ID == entry.ID && source.IgnoredReason == "" {
					mobileMetadataField(c.Key(source.EntryID), "当前来源", source.ConfigurationPath, "复制配置路径")
				}
			}
		}
		mobileMetadataField(c.Key("ignored"), "未加载原因", entry.IgnoredReason, "")
		if live && entry.State == domain.ToolServiceReady {
			mobileGroupHeading(c, "工具 · "+strconv.Itoa(entry.Tools))
			for _, tool := range entry.Definitions {
				toolRow := c.Key(tool.Name)
				rowDivider(ui.Column(toolRow).Gap(4).PaddingY(8), p.line).Children(func() {
					label(toolRow, tool.Name, 13, p.ink).Font("monospace").Selectable()
					if tool.Description != "" {
						label(toolRow, tool.Description, 12, p.secondary).Selectable()
					}
				})
			}
		}
	})
}

func workspaceToolScopeTitle(scope domain.ToolServiceScope) string {
	switch scope {
	case domain.ToolServiceProject:
		return "项目级"
	case domain.ToolServiceUser:
		return "用户级 · 继承"
	case domain.ToolServiceManaged:
		return "受管理 · 继承"
	default:
		return "其他来源"
	}
}

func workspaceToolDisplayState(entry domain.InspectedToolService, session, live bool) string {
	if entry.IgnoredReason != "" {
		return "未加载"
	}
	if !session {
		if entry.Configuration != nil && !entry.Configuration.Enabled {
			return "已停用"
		}
		return "已配置"
	}
	if !live {
		return "状态未知"
	}
	switch entry.State {
	case domain.ToolServiceReady:
		return "已连接"
	case domain.ToolServiceDisabled, domain.ToolServiceNotLoaded:
		return "未加载"
	case domain.ToolServiceAuthorizationRequired, domain.ToolServiceConnecting, domain.ToolServiceFailed:
		return mobileToolLabel(entry.ToolService)
	default:
		return "状态未知"
	}
}

func workspaceToolConnection(entry domain.InspectedToolService) string {
	if cfg := entry.Configuration; cfg != nil {
		switch cfg.Transport {
		case "http":
			if cfg.URL == "" {
				return "HTTP"
			}
			return "HTTP · " + cfg.URL
		case "command":
			parts := []string{"stdio"}
			if entry.CommandHidden {
				parts = append(parts, "命令值已隐藏")
			} else if cfg.Command != "" {
				parts = append(parts, cfg.Command)
			}
			if entry.ArgumentCount > 0 {
				parts = append(parts, strconv.Itoa(entry.ArgumentCount)+" 个参数（值已隐藏）")
			}
			return strings.Join(parts, " · ")
		}
		return "连接方式未设置"
	}
	return ""
}

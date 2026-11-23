package main

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileToolServices struct {
	harness, epoch            string
	catalog                   domain.ToolServiceCatalog
	read, write, authRequest  mobileRequest
	draft                     *mobileToolEditor
	loaded, saved, needsCheck bool
	nextAuth                  time.Time
	stopForward               func()
	forwardRoute              string
	opening, visible          bool
}

func (s *mobileToolServices) available(a *mobileApp, w *mygo.Window) bool {
	return a.pageCurrent(w) && a.model.Connected && a.model.WorkerMinor >= 22
}

func (s *mobileToolServices) authorizedWrite(a *mobileApp, w *mygo.Window) bool {
	auth := s.catalog.Authorization
	return s.available(a, w) && a.model.WorkerMinor >= 24 && s.epoch == a.model.ConnectionEpoch &&
		s.loaded && s.catalog.Revision != "" && !s.needsCheck && !s.read.busy && !s.write.busy &&
		!s.authRequest.busy && (auth == nil || !auth.Phase.Active())
}

func (s *mobileToolServices) load(a *mobileApp, w *mygo.Window, refresh bool) {
	if !s.available(a, w) || s.read.busy || s.write.busy {
		return
	}
	if a.preview {
		if s.catalog.Revision == "" {
			s.catalog = previewToolServices()
		}
		s.loaded, s.needsCheck = true, false
		s.read.err, s.write.err = "", ""
		return
	}
	method := "runtime.tools.list"
	if refresh && s.catalog.CanRefresh {
		method = "runtime.tools.refresh"
	}
	epoch := s.epoch
	a.pageJob(w, &s.read, method, protocol.ToolServiceInput{HarnessID: s.harness}, decodePage(func(catalog domain.ToolServiceCatalog) error {
		if a.model.ConnectionEpoch != epoch {
			return nil
		}
		if auth := catalog.Authorization; auth != nil && (auth.HarnessID != s.harness || auth.ID == "" || auth.ServiceID == "") {
			return fmt.Errorf("设备返回的 MCP 授权记录无效")
		}
		previous, current := s.catalog.Authorization, catalog.Authorization
		if previous != nil && (current == nil || current.ID != previous.ID || !current.Phase.Active()) {
			s.closeForward()
		}
		s.catalog, s.loaded, s.needsCheck = catalog, true, false
		s.write.err = ""
		return nil
	}))
}

func (s *mobileToolServices) change(a *mobileApp, w *mygo.Window, change domain.ToolServiceChange) {
	if !s.authorizedWrite(a, w) || change.Revision != s.catalog.Revision {
		return
	}
	for _, service := range s.catalog.Services {
		if service.ID == change.ServiceID && !service.Editable {
			return
		}
	}
	if change.ServiceID == "" && !s.catalog.CanAdd {
		return
	}
	s.needsCheck, s.saved = true, false
	accepted := func() {
		s.discardDraft()
		s.saved = true
		s.load(a, w, true)
		w.Invalidate()
	}
	if a.preview {
		s.previewChange(change)
		accepted()
		return
	}
	epoch := s.epoch
	a.pageJob(w, &s.write, "runtime.tools.change", protocol.ToolServiceInput{HarnessID: s.harness, Change: &change},
		decodePage(func(result domain.ToolServiceChangeResult) error {
			if a.model.ConnectionEpoch != epoch {
				return nil
			}
			if !result.Saved {
				return fmt.Errorf("保存结果待核对，请刷新 MCP 配置")
			}
			accepted()
			return nil
		}))
}

func (s *mobileToolServices) view(a *mobileApp, c *ui.Context, w *mygo.Window, unsaved bool) {
	p := palette(c)
	if s.epoch != a.model.ConnectionEpoch {
		s.closeForward()
		s.epoch = a.model.ConnectionEpoch
		s.loaded = false
		s.read.err = ""
	}
	if s.available(a, w) && !unsaved {
		if !s.loaded && !s.read.busy && s.read.err == "" {
			s.load(a, w, false)
		}
		s.pollAuthorization(a, c, w)
	}
	ui.Column(c.Key("mcp")).Fill().Background(p.canvas).Children(func() {
		if a.model.WorkerMinor < 22 || unsaved {
			message := "请先升级执行服务以管理 MCP"
			if unsaved {
				message = "保存 Agent 配置后查看 MCP 服务"
			} else if !a.model.Connected {
				message = "连接执行设备后查看 MCP 服务"
			}
			mobileContent(c, contentMaxWidth).PaddingX(pageInset).Children(func() { mobileMessage(c, message) })
			return
		}
		ui.Column(c.Key("mcp-tab-content")).Grow(1).Basis(0).MinHeight(0).Clip().Children(func() {
			if s.draft == nil {
				s.list(a, c.Key("list"), w)
			} else {
				s.editor(a, c.Key("editor"), w)
			}
		})
	})
}

func (s *mobileToolServices) list(a *mobileApp, c *ui.Context, w *mygo.Window) {
	p := palette(c)
	mobileContent(c.Key("mcp-list"), contentMaxWidth).FillHeight().Padding(0, pageInset, pageInset).
		Transition(mobileHorizontalTransition(-100)).
		Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
				label(c, "工具服务", 13, p.secondary).FontWeight(600).Grow(1)
				action(c, "刷新 MCP", func() { s.load(a, w, true) }).
					Disabled(!s.available(a, w) || s.read.busy || s.write.busy || s.authorizationActive())
				if s.catalog.CanAdd && a.model.WorkerMinor >= 24 {
					iconButton(c, "添加 MCP 服务", plusSymbol, 17, func() {
						s.saved = false
						s.draft = newMobileToolEditor(s.catalog, nil)
					}).Margin(0, -13, 0, 0).Disabled(!s.authorizedWrite(a, w))
				}
			})
			ui.Scroll(c.Key("service-scroll")).Grow(1).Basis(0).MinHeight(0).Label("MCP 服务列表").Children(func() {
				for _, service := range s.catalog.Services {
					s.serviceRow(a, c.Key(service.ID), w, service)
				}
				if s.loaded && len(s.catalog.Services) == 0 {
					mobileMessage(c, "暂无 MCP 服务")
				}
			})
			s.feedback(c, a.model.Connected)
		})
}

func (s *mobileToolServices) serviceRow(a *mobileApp, c *ui.Context, w *mygo.Window, service domain.ToolService) {
	p := palette(c)
	glyph := terminalSymbol
	if service.Transport == "http" {
		glyph = linkSymbol
	}
	var edit func()
	if service.Configuration != nil {
		edit = func() {
			s.saved = false
			s.draft = newMobileToolEditor(s.catalog, &service)
		}
	}
	rowDivider(ui.Row(c).AlignItems(ui.Center).MinHeight(max(contentRowHeight, mobileTapSize(c))).Gap(6), p.line).Children(func() {
		status := mobileToolLabel(service)
		mobileDetailRow(c, service.Name, status, "编辑 MCP："+service.Name, glyph, edit, nil).
			Grow(1).MinWidth(0).Value(status)
		if service.Editable && service.Configuration != nil && a.model.WorkerMinor >= 24 {
			enabled := service.Configuration.Enabled
			mobileSwitch(c, "启用 MCP："+service.Name, &enabled, func() {
				s.change(a, w, domain.ToolServiceChange{
					ServiceID: service.ID, Revision: s.catalog.Revision, Action: "setEnabled", Enabled: enabled,
				})
			}).Disabled(!s.authorizedWrite(a, w))
		}
	})
	if status := s.catalog.Authorization; status != nil && status.ServiceID == service.ID && (status.Phase.Active() || status.Message != "") {
		s.authorizationView(a, c, w, status)
	}
	if service.CanAuthorize && service.State != domain.ToolServiceDisabled && (s.catalog.Authorization == nil ||
		s.catalog.Authorization.ServiceID != service.ID || !s.authorizationActive()) &&
		(service.State == domain.ToolServiceAuthorizationRequired || service.State == domain.ToolServiceFailed) {
		action(c, "授权 "+service.Name, func() { s.startAuthorization(a, w, service.ID) }).AlignSelf(ui.Start).
			Disabled(!s.available(a, w) || !s.loaded || s.needsCheck || s.read.busy || s.write.busy || s.authRequest.busy || s.authorizationActive())
	}
}

func (s *mobileToolServices) feedback(c *ui.Context, connected bool) {
	switch {
	case s.write.busy:
		mobileMessage(c, "正在保存…")
	case s.write.err != "":
		s.write.status(c)
	case s.read.err != "":
		if s.saved {
			message := "配置已保存，连接检查未完成"
			if s.catalog.BasicEditing {
				message = "配置已保存，读取未完成，请刷新核对"
			}
			mobileMessage(c, message)
		}
		s.read.status(c)
	case s.read.busy:
		message := "正在检查 MCP…"
		if s.catalog.BasicEditing {
			message = "正在读取 MCP 配置…"
		}
		mobileMessage(c, message)
	case !connected:
		mobileMessage(c, "连接执行设备后再试")
	case s.saved:
		message := "已保存，下轮生效"
		if s.catalog.BasicEditing {
			message = "已保存到设备配置，当前会话生效状态未知"
		}
		mobileMessage(c, message)
	}
}

func mobileToolLabel(service domain.ToolService) string {
	switch service.State {
	case domain.ToolServiceConnecting:
		return "连接中"
	case domain.ToolServiceReady:
		if service.Tools > 0 {
			return strconv.Itoa(service.Tools) + " 个工具"
		}
		return "可用"
	case domain.ToolServiceAuthorizationRequired:
		return "需要授权"
	case domain.ToolServiceFailed:
		return "连接失败"
	case domain.ToolServiceDisabled:
		return "已停用"
	}
	return "待检查"
}

func previewToolServices() domain.ToolServiceCatalog {
	return domain.ToolServiceCatalog{
		Revision: domain.NewID(), CanAdd: true, CanRefresh: true,
		Services: []domain.ToolService{
			{ID: "Documentation", Name: "Documentation", Transport: "http", Editable: true, State: domain.ToolServiceReady, Tools: 3,
				Configuration: &domain.ToolServiceConfiguration{Name: "Documentation", Enabled: true, Transport: "http", URL: "https://docs.example.test/mcp",
					Headers: []domain.ToolServiceValue{{Name: "X-Service-Key", Retained: true}}}},
			{ID: "Calendar", Name: "Calendar", Transport: "http", State: domain.ToolServiceAuthorizationRequired, CanAuthorize: true},
		},
	}
}

func (s *mobileToolServices) previewChange(change domain.ToolServiceChange) {
	index := slices.IndexFunc(s.catalog.Services, func(service domain.ToolService) bool { return service.ID == change.ServiceID })
	switch change.Action {
	case "save":
		config := *change.Configuration
		for _, values := range [][]domain.ToolServiceValue{config.Environment, config.Headers} {
			for i := range values {
				values[i].Value, values[i].Retained = "", true
			}
		}
		service := domain.ToolService{ID: config.Name, Name: config.Name, Editable: true, Transport: config.Transport, Configuration: &config, State: domain.ToolServiceReady}
		if !config.Enabled {
			service.State = domain.ToolServiceDisabled
		}
		if index >= 0 {
			s.catalog.Services[index] = service
		} else {
			s.catalog.Services = append(s.catalog.Services, service)
		}
	case "setEnabled":
		service := &s.catalog.Services[index]
		service.Configuration.Enabled = change.Enabled
		service.State = domain.ToolServiceReady
		if !change.Enabled {
			service.State = domain.ToolServiceDisabled
		}
	case "delete":
		s.catalog.Services = slices.Delete(s.catalog.Services, index, index+1)
	}
	s.catalog.Revision = domain.NewID()
}

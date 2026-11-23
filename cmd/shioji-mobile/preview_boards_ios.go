package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/editor"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
	"shioji/internal/tailnet"
)

func previewWorkspaceTools(q workspaceToolQuery, primaryFolder string) domain.ToolServiceInspection {
	documentation := previewToolServices().Services[0]
	documentation.Editable = false
	result := domain.ToolServiceInspection{
		Directory: q.directory,
		Entries: []domain.InspectedToolService{{
			ToolService: documentation, EntryID: "user-documentation", PrimarySource: true, Scope: domain.ToolServiceUser,
			ConfigurationPath: "/Users/developer/.codex/config.toml",
		}},
	}
	if q.input.SessionID == "review" {
		result.RuntimeKnown = true
		result.Entries[0].Definitions = []domain.ToolServiceDefinition{
			{Name: "find_docs", Description: "在项目文档中查找相关说明。"},
		}
	} else if q.input.SessionID != "" {
		result.Warnings = []string{"此会话没有可查询的运行实例，连接状态未知"}
	}
	if q.directory == primaryFolder {
		result.Entries = append(result.Entries, domain.InspectedToolService{
			ToolService: domain.ToolService{ID: "build-tools", Name: "build-tools", State: domain.ToolServiceUnknown,
				Configuration: &domain.ToolServiceConfiguration{Transport: "command", Enabled: true, StartupSeconds: 20}},
			EntryID: "project-build-tools", PrimarySource: true, ArgumentCount: 2, CommandHidden: true,
			Scope: domain.ToolServiceProject, ConfigurationPath: path.Join(primaryFolder, ".codex/config.toml"),
		})
	}
	return result
}

func (a *mobileApp) previewSkillInventory(in protocol.SkillManagementInput) domain.SkillInventory {
	catalog := domain.SkillInventory{Revision: fmt.Sprint(a.previewSkillRevision)}
	skill := func(id, description, dir string, scope domain.SkillScope, editable bool) {
		catalog.Entries = append(catalog.Entries, domain.ManagedSkill{
			SkillReference: domain.SkillReference{ID: id, Name: id}, Directory: dir,
			Path: path.Join(dir, id, "SKILL.md"), Description: description, Scope: scope,
			Enabled: !a.previewDisabledSkills[id], CanEnable: editable,
		})
	}
	if ws := a.workspace(in.WorkspaceID); len(ws.Folders) != 0 {
		skill("build-check", "检查项目构建和回归。", path.Join(ws.Folders[0], ".agents/skills"), domain.SkillProject, true)
	}
	skill("release-notes", "按实际变更编写发布说明。", "/Users/developer/.codex/skills", domain.SkillUser, in.WorkspaceID == "")
	skill("image-tools", "读取项目使用的视觉资源。", "/Users/developer/.codex/skills/.system", domain.SkillBuiltIn, false)
	return catalog
}

func previewSkillDocument(id string) domain.SkillDocument {
	text := map[string]string{
		"build-check":   "# 项目构建检查\n\n先确认工作区和分支，再运行项目的构建与回归。",
		"release-notes": "# 发布说明\n\n整理发布说明前先核对变更范围。",
		"image-tools":   "# 视觉资源\n\n保留当前项目的素材和图标规范。",
	}[id]
	return domain.SkillDocument{ID: id, Text: text}
}

func (a *mobileApp) previewChangeSkill(in protocol.SkillManagementInput) domain.SkillChangeResult {
	catalog := a.previewSkillInventory(in)
	if a.previewSkillFailure {
		a.previewSkillFailure = false
		return domain.SkillChangeResult{}
	}
	if in.Revision == catalog.Revision && slices.ContainsFunc(catalog.Entries, func(entry domain.ManagedSkill) bool {
		return entry.ID == in.ID && entry.CanEnable
	}) {
		if a.previewDisabledSkills == nil {
			a.previewDisabledSkills = make(map[string]bool)
		}
		a.previewDisabledSkills[in.ID] = !in.Enabled
		a.previewSkillRevision++
		return domain.SkillChangeResult{Saved: true, Enabled: in.Enabled}
	}
	return domain.SkillChangeResult{}
}

// The geometry fixtures use the same text and states as the frozen boards.
// The general navigation and IME tests keep using seedPreview.
func (a *mobileApp) seedArtboardConversation(longQueue bool) {
	a.previewDeviceName = "Mac mini"
	m := a.model
	workspace := &m.Snapshot.Workspaces[0]
	workspace.Name = "Shioji"
	a.filter = mobileclient.WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID}
	session := m.Snapshot.Sessions[0]
	session.Title = "测试断线重连"
	session.GitBranch = "feat/reconnect"
	m.Snapshot.Sessions = []domain.Session{session}
	for index, row := range []struct {
		title, branch string
		status        domain.ExecutionState
	}{
		{"核对 Worker 配置", "main", domain.ExecutionRunning},
		{"检查会话列表刷新", "main", domain.ExecutionRunning},
		{"修正 iPad 文件检查器", "fix/inspector", domain.ExecutionCompleted},
		{"验证多设备切换", "main", domain.ExecutionCompleted},
		{"校对原生菜单文案", "main", domain.ExecutionCompleted},
		{"调整队列与输入焦点", "main", domain.ExecutionIdle},
		{"整理远程设备就绪检查", "main", domain.ExecutionInterrupted},
	} {
		item := domain.Session{ID: fmt.Sprintf("artboard-%d", index), WorkspaceID: workspace.ID,
			Title: row.title, GitBranch: row.branch, Status: row.status, HarnessID: session.HarnessID}
		m.Snapshot.Sessions = append(m.Snapshot.Sessions, item)
	}
	m.Snapshot.PendingRequests = []domain.PendingRequest{{ID: "artboard-request", SessionID: "artboard-0", State: domain.RequestPending}}
	m.Readiness.Capabilities.Steer = true
	m.Readiness.Capabilities.Fork = true
	m.Readiness.Capabilities.Restart = true
	detail := m.Details[session.ID]
	detail.Session = session
	first := time.Date(2026, 11, 19, 9, 36, 0, 0, time.Local)
	followup := first.Add(3 * time.Minute)
	completed := first.Add(2*time.Minute + 18*time.Second)
	elapsed := (2*time.Minute + 18*time.Second).Milliseconds()
	detail.Turns = []domain.Turn{
		{ID: "artboard-turn-1", Status: domain.ExecutionCompleted, CompletedAt: &completed, DurationMS: &elapsed,
			InputTokens: new(int64(2840)), OutputTokens: new(int64(620)), CachedInputTokens: new(int64(1024))},
		{ID: "artboard-turn-2", Status: domain.ExecutionRunning},
	}
	detail.Items = []domain.Item{
		{ID: "prompt", Kind: "user", Status: "sent", TurnID: detail.Turns[0].ID, SentAt: &first,
			Text: "帮我测试 Worker 断线重连。恢复观看时保留原会话和正在写的草稿。"},
		{ID: "tool-read", Kind: "tool", TurnID: detail.Turns[0].ID, Name: "读取 connection.go", Final: true},
		{ID: "tool-test", Kind: "tool", TurnID: detail.Turns[0].ID, Name: "go test ./internal/transport", Final: true},
		{ID: "tool-edit", Kind: "tool", TurnID: detail.Turns[0].ID, Name: "修改 reconnect_test.go", Final: true},
		{ID: "reply", Kind: "assistant", TurnID: detail.Turns[0].ID, Final: true,
			Text: "已把重连和执行状态分开处理。连接恢复后，界面会先跟 Worker 核对当前轮次，再继续接收输出。\n\n我补了断线前有未确认请求和编辑中草稿的场景。接下来验证恢复连接后的状态交接。"},
		{ID: "followup", Kind: "user", Status: "sent", TurnID: detail.Turns[1].ID, SentAt: &followup,
			Text: "也确认一下，重复连线时会不会把原来的任务再发一次。"},
	}
	detail.Plan = &domain.Plan{Steps: []domain.PlanStep{
		{Text: "梳理离线恢复路径", Status: "completed"},
		{Text: "补充接纳回执测试", Status: "running"},
		{Text: "验证历史位置", Status: "pending"},
	}}
	detail.Works = []domain.WorkActivity{
		{ID: "agent", Kind: "agent", Title: "连接验证", State: "running", CanSteer: true, Brief: "核对断线前后的事件游标与回执一致性。", Latest: "已复现连接中断，正在比对重连返回的轮次。"},
		{ID: "task", Kind: "background", Title: "重连回归", Brief: "go test ./internal/transport -run 'Test(ResumeCursor|VerifyAcceptedInput|ReconnectAfterTimeout)' -v", State: "running", ParentID: "agent", DurationMS: new(int64(42000)), Latest: "PASS  TestResumeCursor\nPASS  TestVerifyAcceptedInput\nRUN   TestReconnectAfterTimeout"},
	}
	queue := []string{"补上连接超时测试"}
	if longQueue {
		queue = append(queue, "检查历史折叠状态", "把验证结果写进发布记录",
			"也验证一下移动端重新进入原会话时，草稿和已经接纳的操作是否保持原样。")
	}
	detail.Queue.Items = nil
	for index, value := range queue {
		input := domain.Submission{Text: value, WorkspaceID: workspace.ID}
		if index == 1 {
			input.Attachments = []domain.Attachment{{Name: "reconnect_test.go"}}
		}
		detail.Queue.Items = append(detail.Queue.Items, domain.QueueItem{ID: fmt.Sprint(index), Input: input})
	}
	m.Details[session.ID] = detail
}

func (a *mobileApp) seedArtboardRequest(request domain.PendingRequest, title, prompt, device, workspace, branch string) {
	m := a.model
	a.previewDeviceName = device
	m.Snapshot.Workspaces[0].Name = workspace
	session := &m.Snapshot.Sessions[0]
	session.Title, session.GitBranch = title, branch
	request.TurnID, request.Revision = "preview-request-turn", 1
	now := time.Date(2026, 11, 19, 9, 36, 0, 0, time.Local)
	detail := domain.SessionDetail{Session: *session, HistoryLoaded: true,
		Turns:    []domain.Turn{{ID: request.TurnID, Status: domain.ExecutionRunning}},
		Requests: []domain.PendingRequest{request},
		Items: []domain.Item{
			{ID: "prompt", Kind: "user", TurnID: request.TurnID, Text: prompt, SentAt: &now},
			{ID: "source-tool", Kind: "tool", TurnID: request.TurnID, Name: strings.SplitN(request.Text, "\n", 2)[0], Final: true},
			{ID: "request:" + request.ID, Kind: "request", TurnID: request.TurnID, Name: request.Title, Status: "pending"},
		},
	}
	m.Snapshot.PendingRequests = []domain.PendingRequest{request}
	m.Details[session.ID] = detail
	a.openSession(session.ID)
}

func (a *mobileApp) openPreviewBoard(board string) {
	switch board {
	case "ssh-auth", "worker-setup", "worker-upgrade", "worker-schema-upgrade", "agent-config", "directory-picker", "device-health", "hook-review", "port-forwards", "import-session", "device-paths":
		a.seedArtboardDevices()
	case "message-search", "history-match", "session-actions", "session-archive", "session-archive-progress", "fork-session", "branch-switch", "attachment-transfer",
		"schedule-list", "schedule-editor", "schedule-detail", "schedule-run":
		a.seedArtboardConversation(false)
	}
	review := a.sessionByID("review")
	workspace := a.workspace(review.WorkspaceID)
	today := time.Date(2026, 11, 19, 1, 36, 0, 0, time.UTC)
	approval := domain.PendingRequest{
		ID: "approval", SessionID: review.ID, Kind: "approval", Title: "允许执行此命令？", State: domain.RequestPending,
		Text: "go test ./internal/transport -run TestReconnect\n~/work/Shioji",
		Choices: []domain.RequestChoice{{ID: "accept", Label: "允许这一次"}, {ID: "acceptForSession", Label: "本会话允许"},
			{ID: "decline", Label: "拒绝"}, {ID: "cancel", Label: "取消"}},
	}
	question := domain.PendingRequest{
		ID: "question", SessionID: review.ID, Kind: "questions", Title: "回答问题", State: domain.RequestPending,
		Questions: []domain.Question{{ID: "continuation", Header: "如何处理未确认的提交？", Custom: true,
			Options: []domain.Option{{Label: "保留现有状态", Description: "先按当前协议核对回执。"}, {Label: "记录并重试", Description: "只重试读取操作。"}}}},
	}
	access := domain.PendingRequest{
		ID: "access", SessionID: review.ID, Kind: "file-approval", Title: "文件与权限审批", State: domain.RequestPending,
		Choices: []domain.RequestChoice{{ID: "decline", Label: "拒绝变更"}, {ID: "accept", Label: "允许此次变更"}},
	}
	switch board {
	case "usage-limit", "usage-limit-timed":
		a.seedArtboardConversation(false)
		now := time.Now()
		detail := a.model.Details[review.ID]
		detail.Session.Status = domain.ExecutionFailed
		detail.Queue.Items, detail.Works, detail.Plan = nil, nil, nil
		limit := &domain.UsageLimit{}
		if board == "usage-limit-timed" {
			reset := now.Add(2 * time.Hour)
			limit.ResetAt = &reset
		}
		failed := &detail.Turns[len(detail.Turns)-1]
		failed.Status, failed.CompletedAt, failed.UsageLimit = domain.ExecutionFailed, &now, limit
		a.model.Details[review.ID] = detail
		a.model.Snapshot.Sessions[0] = detail.Session
		a.model.Drafts["session:"+review.ID] = "再检查一下连续展开两个工具组的情况。"
		a.openSession(review.ID)
	case "motion-send", "motion-send-delayed", "motion-send-uncertain", "motion-steer", "motion-steer-delayed", "motion-steer-tool", "motion-queue", "motion-queue-auto":
		a.seedArtboardConversation(false)
		a.seedConversationMotion(board, review.ID)
		a.openSession(review.ID)
	case "motion-home-preparing", "motion-home-handoff":
		a.seedArtboardConversation(false)
		a.previewConversationMotion = board
		a.presentDraft(a.home, false)
		a.model.Drafts[a.model.DraftKey()] = "核对新会话的准备状态"
	case "stopped-before-output":
		a.seedArtboardConversation(false)
		a.seedStoppedBeforeOutput(review.ID)
		a.openSession(review.ID)
	case "motion-reply-height":
		a.seedArtboardConversation(false)
		a.seedReplyHeightMotion(review.ID)
		a.openSession(review.ID)
	case "inbox":
		a.seedArtboardConversation(false)
	case "connected-device-catalogs", "session-list-opening", "session-list-cached", "session-list-fresh", "session-list-refresh":
		a.previewSessionOpening = board == "session-list-opening"
		now := time.Now()
		first := mobileclient.Endpoint{DeviceID: a.model.Snapshot.DeviceID, Address: "192.0.2.10:22"}
		other := mobileclient.Endpoint{DeviceID: "preview-other-device", Address: "192.0.2.20:22"}
		m := a.model
		a.endpoint = first
		a.savedDevices.Devices = []savedMobileDevice{
			{Endpoint: first, Name: "工作室 Mac", Icon: app.DeviceIconLaptop},
			{Endpoint: other, Name: "家里 Mac mini", Icon: app.DeviceIconServer},
		}
		timing := &app.DeviceTiming{Milliseconds: 12, ObservedAt: now}
		m.DevicePaths = []app.DevicePathStatus{{ID: first.RouteID(), Phase: app.PathActive, Timing: timing}}
		a.sessionCatalogs = map[string]mobileclient.DeviceCatalog{other.DeviceID: {
			Address: other.Address, RouteID: other.RouteID(), Timing: timing, RequestedAt: now, CheckedAt: now,
			Snapshot: domain.Snapshot{DeviceID: other.DeviceID,
				Workspaces: []domain.Workspace{{ID: "house", DeviceID: other.DeviceID, Name: "家庭项目"}},
				Sessions:   []domain.Session{{ID: "house-reply", WorkspaceID: "house", Title: "家庭设备巡检", Status: domain.ExecutionCompleted}},
			},
		}}
		if board == "session-list-cached" || board == "session-list-fresh" {
			a.sessionGroupings.Global = groupByWorkspace
			if board == "session-list-cached" {
				m.ConnectionPhase = app.ConnectionOpening
				catalog := a.sessionCatalogs[other.DeviceID]
				catalog.RequestedAt = time.Time{}
				a.sessionCatalogs[other.DeviceID] = catalog
			}
		}
		if board == "session-list-refresh" {
			a.seedSessionCatalogRefresh(other.DeviceID)
		}
		a.home.Invalidate()
		a.devices.Invalidate()
	case "recent-operations":
		a.seedArtboardConversation(false)
		a.endpoint.DeviceID = workspace.DeviceID
		const remoteDevice = "preview-receipt-remote"
		a.savedDevices.Devices = append(a.savedDevices.Devices, savedMobileDevice{
			Endpoint: mobileclient.Endpoint{DeviceID: remoteDevice}, Name: "备用 Mac",
		})
		a.sessionCatalogs = map[string]mobileclient.DeviceCatalog{remoteDevice: {Snapshot: domain.Snapshot{
			DeviceID:   remoteDevice,
			Workspaces: []domain.Workspace{{ID: workspace.ID, DeviceID: remoteDevice, Name: "工具仓库"}},
			Sessions:   []domain.Session{{ID: "artboard-1", WorkspaceID: workspace.ID, Title: "编译检查"}},
		}}}
		const pending = "recent-pending"
		a.model.Pending[pending] = domain.Submission{OperationID: pending, WorkspaceID: workspace.ID, SessionID: review.ID}
		a.model.UncertainSubmissions = map[string]bool{pending: true}
		a.model.ReportFeedback(app.Feedback{ID: pending, WorkspaceID: workspace.ID, SessionID: review.ID,
			Kind: app.FeedbackSubmissionUncertain, Title: "提交结果待核对"})
		a.model.ReportFeedback(app.Feedback{ID: "recent-operation", WorkspaceID: workspace.ID, SessionID: "artboard-1",
			Kind: app.FeedbackFailure, Title: "Git 操作未完成", Details: "等待设备响应超时。 token=fixture-secret"})
		a.model.ReportFeedback(app.Feedback{ID: "new-session-failure", WorkspaceID: workspace.ID,
			Kind: app.FeedbackSubmissionFailed, Title: "消息未发送", Details: "执行设备未能准备会话。"})
		a.collectOperationFeedback()
		a.operationHistory.Collect(remoteDevice, []app.Feedback{{ID: "recent-operation", WorkspaceID: workspace.ID,
			SessionID: "artboard-1", Kind: app.FeedbackSuccess, Title: "备用 Mac 已核对"}}, nil)
	case "chat", "queue-manager", "pad-chat", "pad-portrait", "work-rosters":
		a.seedArtboardConversation(board == "queue-manager")
		if board == "work-rosters" {
			detail := a.model.Details[review.ID]
			task := &detail.Works[1]
			task.Brief = "go test ./internal/transport ./internal/mobileclient ./internal/app ./internal/worker -run 'Test(ResumeCursor|VerifyAcceptedInput|ReconnectAfterTimeout|RecoverFromInterruptedHistory|RestoreTheInitialPosition)' -count=1 -v"
			task.Title = task.Brief
			a.model.Details[review.ID] = detail
			a.model.WorkDetails[a.model.WorkKey(review.ID, task.ID)] = domain.WorkDetail{SessionID: review.ID, Work: *task}
		}
		a.openSession(review.ID)
	case "context-compaction", "context-compaction-receipt", "context-compaction-auto",
		"context-compaction-history", "context-compaction-order":
		a.seedArtboardConversation(false)
		a.seedPreviewCompaction(review.ID, board)
		a.openSession(review.ID)
	case "session-goal", "session-goal-legacy":
		a.seedArtboardConversation(false)
		a.seedPreviewGoal(review.ID, board == "session-goal-legacy")
		a.openSession(review.ID)
	case "session-reload", "session-reload-receipt", "session-reload-busy", "session-reload-legacy":
		a.seedPreviewReload(review.ID, board)
		a.openSession(review.ID)
	case "session-reload-list":
		a.seedPreviewReload(review.ID, board)
	case "transcript-command-output":
		a.seedPreviewCommandOutput(review.ID)
		a.openSession(review.ID)
	case "transcript-tool-tail", "transcript-tool-tail-completed", "transcript-tool-tail-settled", "running-session-entry", "running-session-cold-entry":
		a.seedArtboardConversation(false)
		a.model.Preferences.CollapseLatestTools = false
		detail := a.model.Details[review.ID]
		detail.Session.Status = domain.ExecutionRunning
		detail.Session.Title = "核对会话工具状态"
		detail.Items, detail.Turns, detail.Works, detail.Queue.Items = nil, nil, nil, nil
		for i := 1; i <= 12; i++ {
			id := fmt.Sprintf("history-%d", i)
			detail.Items = append(detail.Items, domain.Item{ID: id, TurnID: id, Kind: "assistant", Final: true,
				Text: fmt.Sprintf("第 %d 次核对已完成。已确认历史记录、回执、草稿和文件不会因设备重新连接而改变。", i)})
			detail.Turns = append(detail.Turns, domain.Turn{ID: id, Status: domain.ExecutionCompleted})
		}
		started := time.Now().Add(-12 * time.Second)
		detail.Turns = append(detail.Turns, domain.Turn{ID: "tool-tail", Status: domain.ExecutionRunning, StartedAt: &started})
		detail.Items = append(detail.Items,
			domain.Item{ID: "tool-tail-prompt", TurnID: "tool-tail", Kind: "user", Text: "再核对一次连接记录。", SentAt: &started},
			domain.Item{ID: "tool-tail-read", TurnID: "tool-tail", Kind: "tool", Name: "读取连接记录", ActivityOrder: 1, Final: true},
			domain.Item{ID: "tool-tail-check", TurnID: "tool-tail", Kind: "tool", Presentation: "command", Name: "核对连接恢复", ActivityOrder: 2, Status: "running"},
		)
		if board == "transcript-tool-tail-settled" {
			for i := len(detail.Items) - 2; i < len(detail.Items); i++ {
				detail.Items[i].Status, detail.Items[i].Final = "completed", true
			}
			detail.Items = append(detail.Items, domain.Item{ID: "tool-tail-review", TurnID: "tool-tail", Kind: "tool",
				Name: "核对执行回执", ActivityOrder: 3, Status: "completed", Final: true})
		}
		if board == "transcript-tool-tail-completed" {
			detail.Session.Status = domain.ExecutionCompleted
			turn := &detail.Turns[len(detail.Turns)-1]
			turn.Status = domain.ExecutionCompleted
			finished := started.Add(2 * time.Second)
			duration := int64(2000)
			turn.CompletedAt, turn.DurationMS = &finished, &duration
			tool := &detail.Items[len(detail.Items)-1]
			tool.Status, tool.Final = "completed", true
			detail.Items = append(detail.Items, domain.Item{ID: "tool-tail-reply", TurnID: turn.ID, Kind: "assistant", Final: true,
				Text: "连接恢复已核对。"})
		}
		a.model.Details[review.ID] = detail
		a.model.Snapshot.Sessions[0] = detail.Session
		if board == "running-session-cold-entry" {
			partial := detail
			partial.Items, partial.Turns = detail.Items[12:], detail.Turns[12:]
			partial.History, partial.HistoryLoaded = nil, false
			a.model.Details[review.ID] = partial
			a.model.BeginHistoryRead(review.ID)
			a.openSession(review.ID)
			time.AfterFunc(20*time.Second, func() {
				mygo.RunOnMain(func() {
					delete(a.model.HistoryReads, review.ID)
					a.model.Details[review.ID] = detail
					a.refresh()
				})
			})
		} else if board == "running-session-entry" {
			a.previewLatestHistory = &detail
			cached := detail
			cached.Items, cached.Turns = detail.Items[:8], detail.Turns[:8]
			cached.History = &domain.HistoryWindow{First: 1, Last: 8, Newer: true}
			a.model.Details[review.ID] = cached
			a.home.Invalidate()
		} else {
			a.openSession(review.ID)
		}
	case "draft-references", "draft-references-history", "draft-references-keyboard", "draft-references-keyboard-long-queue":
		a.seedArtboardConversation(board == "draft-references-keyboard-long-queue")
		a.previewReferenceKeyboard = strings.HasPrefix(board, "draft-references-keyboard")
		a.model.Readiness.Capabilities.Skills = true
		detail := a.model.Details[review.ID]
		detail.Queue.Items[0].Input.Text = "按  检查  的边界"
		detail.Queue.Items[0].Input.Attachments = []domain.Attachment{{ID: "queued-source", Name: "connection.go",
			SourcePath: "/work/internal/transport/connection.go"}}
		detail.Queue.Items[0].Input.Skills = []domain.SkillReference{{ID: "review-check", Name: "review-check"}}
		detail.Queue.Items[0].Input.ReferencePositions = []domain.ReferencePosition{
			{SkillID: "review-check", Position: 3}, {AttachmentID: "queued-source", Position: 8},
		}
		if board == "draft-references-history" {
			detail.Session.Status = domain.ExecutionCompleted
			detail.Session.ThreadID = "history-reference-thread"
			a.model.Snapshot.Sessions[0] = detail.Session
			a.model.Drafts["session:"+review.ID] = "ordinary draft"
			input := detail.Queue.Items[0].Input
			detail.Items, detail.Turns = detail.Items[:5], detail.Turns[:1]
			detail.Items[0].Text, detail.Items[0].Attachments = input.Text, input.Attachments
			detail.Items[0].Skills, detail.Items[0].ReferencePositions = input.Skills, input.ReferencePositions
		}
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
	case "transcript-wait":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		detail.Turns = detail.Turns[:1]
		prompt := detail.Items[0]
		prompt.Text = "核对等待步骤的时长和结果。"
		detail.Items = []domain.Item{
			prompt,
			{ID: "wait-done", TurnID: prompt.TurnID, Kind: "tool", Presentation: "wait", Name: "等待", Status: "completed", Final: true,
				Parts: []domain.ContentPart{{Kind: "text", Label: "时长", Text: "45.0 秒"}}},
			{ID: "wait-stopped", TurnID: prompt.TurnID, Kind: "tool", Presentation: "wait", Name: "等待", Status: "interrupted", Final: true,
				Parts: []domain.ContentPart{{Kind: "text", Label: "时长", Text: "30.0 秒"}}},
		}
		detail.Queue.Items = nil
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
	case "transcript-legacy":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		detail.Session.Status = domain.ExecutionCompleted
		detail.Turns, detail.Queue.Items = nil, nil
		detail.Items = []domain.Item{
			{ID: "legacy-input", Kind: "user", Text: "读一下之前的记录。", Final: true},
			{ID: "legacy-reply", Kind: "assistant", Text: "已经过核对。", Final: true},
		}
		a.model.Details[review.ID], a.model.Snapshot.Sessions[0] = detail, detail.Session
		a.openSession(review.ID)
	case "transcript-turns", "transcript-offline":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		detail.Session.Status = domain.ExecutionFailed
		a.model.Snapshot.Sessions[0].Status = domain.ExecutionFailed
		begin := time.Date(2026, 11, 19, 9, 36, 0, 0, time.Local)
		ended := begin.Add(18 * time.Second)
		detail.Turns = []domain.Turn{
			{ID: "tools-only", Status: domain.ExecutionCompleted, StartedAt: &begin, CompletedAt: &ended,
				DurationMS: new(int64(18000)), InputTokens: new(int64(560)), OutputTokens: new(int64(0))},
			{ID: "tools-failed", Status: domain.ExecutionFailed, Error: "工作区校验失败。"},
		}
		detail.Items = []domain.Item{
			{ID: "sent-references", Kind: "user", TurnID: "tools-only", Status: "sent", SentAt: &begin,
				Text:               "用  检查 。",
				Skills:             []domain.SkillReference{{ID: "audit", Name: "review-check"}},
				Attachments:        []domain.Attachment{{ID: "connection-snapshot", Name: "connection.go", SourcePath: "/work/internal/transport/connection.go"}},
				ReferencePositions: []domain.ReferencePosition{{SkillID: "audit", Position: 3}, {AttachmentID: "connection-snapshot", Position: 8}}},
			{ID: "finished-tool", Kind: "tool", TurnID: "tools-only", Name: "核对重连逻辑", Text: "校验通过", Presentation: "command", Status: "completed", Final: true},
			{ID: "followup", Kind: "user", TurnID: "tools-failed", Status: "sent", Text: "再执行一次完整校验。"},
			{ID: "failed-tool", Kind: "tool", TurnID: "tools-failed", Name: "运行完整校验", Status: "failed", Final: true},
		}
		detail.Queue.Items = nil
		if board == "transcript-offline" {
			latest := time.Now().Add(-12 * time.Second)
			detail.Session.Status = domain.ExecutionRunning
			a.model.Snapshot.Sessions[0].Status = domain.ExecutionRunning
			detail.Turns[1] = domain.Turn{ID: "tools-failed", Status: domain.ExecutionRunning, StartedAt: &latest}
			last := &detail.Items[len(detail.Items)-1]
			last.Final, last.Status, last.Presentation = false, "running", "command"
			last.Text = "正在比对重连前后的事件游标"
			a.model.Connected = false
		}
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
	case "transcript-links":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		detail.Turns = nil
		detail.Items = []domain.Item{
			{ID: "linked-reply", Kind: "assistant", Text: "检查了连接实现。\n\n[查看连接源码](internal/transport/connection.go#L17-L19)\n\n[查看运维手册](docs/runbook.md)", Final: true},
		}
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
	case "transcript-content":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		detail.Turns = nil
		detail.Items = []domain.Item{
			{ID: "question-receipt", Kind: "request_result", Text: "已提交回答", Final: true,
				Parts: []domain.ContentPart{{Kind: "text", Label: "采用哪种缓存策略？", Text: "保留现有缓存"}}},
			{ID: "reasoning", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Final: true,
				Parts: []domain.ContentPart{{Kind: "text", Text: "对照重连前后的历史事件。"}}},
			{ID: "tool-results", Kind: "tool", Presentation: "mcp", Name: "检查工作区", Final: true, Status: "failed", ExitCode: new(3),
				Parts: []domain.ContentPart{
					{Kind: "markdown", Label: "检查结果", Text: "**已核对事件顺序**"},
					{Kind: "code", Label: "命令输出", Text: "worker ping: ok"},
					{Kind: "image", Attachment: &domain.Attachment{ID: "result-image", Name: "connection.png", MIME: "image/png"}},
					{Kind: "file", Attachment: &domain.Attachment{ID: "result-file", Name: "diagnostics.txt", MIME: "text/plain", Size: int64(len(previewDiagnostics))}},
				}, Files: []domain.FileChange{{Path: "reconnect.go", Kind: "update", Patch: "@@ -1 +1 @@\n-useOld()\n+resume()\n"}}, Truncated: true},
		}
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
	case "transcript-images", "system-file-preview":
		a.seedArtboardConversation(false)
		if board == "system-file-preview" {
			a.seedSystemFilePreview(review.ID)
		} else {
			a.seedPreviewImages(review.ID)
		}
		a.openSession(review.ID)
	case "transcript-mermaid", "transcript-mermaid-fallback":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		detail.Queue.Items, detail.Turns = nil, nil
		reply := domain.Item{ID: "diagram-reply", Kind: "assistant", Final: true}
		if board == "transcript-mermaid" {
			reply.Text = "[打开重连流程说明](docs/reconnect-diagram.md)\n\n```mermaid\nflowchart LR\n  A[收到请求] --> B[恢复连接]\n```"
		} else {
			reply.Text = "```mermaid\ninvalid diagram\n```\n\n```go\nvar normalCode = true\n```\n\n```mermaid\nflowchart LR\n  A[正在接收]"
			reply.Final = false
		}
		detail.Items = []domain.Item{reply}
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
	case "transcript-history", "transcript-outline", "transcript-find":
		a.seedArtboardConversation(false)
		detail := a.model.Details[review.ID]
		a.previewFind = board == "transcript-find"
		if a.previewFind {
			detail = previewFindWindow(detail, "")
		} else {
			detail = previewRecentTranscript(detail)
		}
		a.model.Details[review.ID] = detail
		a.openSession(review.ID)
		if board == "transcript-outline" || a.previewFind {
			a.model.Drafts[a.model.DraftKey()] = "保留这条提问草稿"
		}
	case "approval":
		a.seedArtboardRequest(approval, "核对 Worker 配置", "核对 Mac mini 上的执行服务配置，验证变更前的检查步骤。", "Mac mini", "Shioji", "main")
	case "request-question":
		a.seedArtboardRequest(question, "核对 Worker 配置", "请验证断线恢复后的接纳状态。", "Mac mini", "Shioji", "main")
	case "request-access":
		access.ItemID = "source-tool"
		a.seedArtboardRequest(access, "检查共享渲染缓存", "检查缓存失效前后各端的渲染状态。", "MacBook Pro", "Design tools", "fix/render-cache")
		detail := a.model.Details[review.ID]
		detail.Items[1].Name = "更新缓存配置"
		detail.Items[1].Files = []domain.FileChange{{Path: "internal/cache/settings.go"}}
		a.model.Details[review.ID] = detail
	case "create":
		a.model.Snapshot.Workspaces[0].Name = "Shioji"
		a.model.GitDrafts[workspace.ID] = domain.GitChoice{Branch: "feat/reconnect", NewWorktree: true}
		a.model.GitStatuses["workspace:"+workspace.ID] = domain.GitStatus{Applicable: true, Branch: "main",
			DefaultBranch: "main", Branches: []string{"main", "feat/reconnect"}}
		a.model.Readiness.Models[0].Efforts = []string{"high"}
		a.model.Readiness.Models[0].DefaultEffort = "high"
		a.model.Configs[a.model.DraftKey()] = domain.RunConfig{Model: "gpt-5.4", Effort: "high", Permission: "default"}
		a.model.Readiness.Harnesses = []domain.HarnessInfo{{ID: "codex", Name: "Codex",
			State: domain.RuntimeReady, Models: a.model.Readiness.Models, PermissionChoices: a.model.Readiness.PermissionChoices}}
		a.openDraft(a.home)
	case "diff":
		a.previewReviewBoard = true
		a.openReview(a.home, review, nil, nil)
	case "file":
		a.previewReviewBoard = true
		r := &reviewState{session: review, parent: a.home}
		a.loadReview(r)
		a.showReviewFile(r, "internal/transport/connection.go")
	case "pad-diff":
		a.previewReviewBoard = true
		a.openSession(review.ID)
		a.openReview(a.detail, review, nil, nil)
	case "diff-compare":
		a.previewReviewBoard = true
		a.openDiffComparison(a.openReview(a.home, review, nil, nil))
	case "diff-annotation":
		a.previewReviewBoard = true
		a.selectSession(review.ID)
		r := &reviewState{session: review, parent: a.home}
		a.loadReview(r)
		file := a.showReviewFile(r, "internal/transport/connection.go")
		file.versions.Snapshot = domain.Attachment{ID: "preview-snapshot"}
		file.diff.TextSelection, file.diff.LineSelection = true, true
		buffer := file.diff.New.Snapshot()
		file.diff.SetTextSelection(editor.DiffTextPoint{Side: editor.DiffNew, Offset: buffer.LineStart(2)},
			editor.DiffTextPoint{Side: editor.DiffNew, Offset: buffer.LineEnd(2)})
		selected, _ := file.diff.SelectedText()
		file.note = &diffCommentDraft{selection: selected, versions: file.versions}
	case "device-list", "tailnet-route-derp":
		a.seedArtboardDevices()
		if board == "tailnet-route-derp" {
			a.tailnetStatus.Peers[0].Measurement = &tailnet.Measurement{Path: "derp", RegionName: "Taipei", Region: "tpe", Milliseconds: 32, ObservedAt: time.Now()}
		}
		a.callUIKit("selectTab", map[string]int{"index": 1}, nil)
	case "pair":
		a.openPair(a.home, mobileclient.Endpoint{Address: "mac-mini.local:22", User: "shinya"})
	case "verify":
		a.previewDeviceName = "Mac mini"
		a.openHostVerification(a.home, mobileclient.Endpoint{Address: "mac-mini.local:22", User: "shinya", WorkerPath: mobileclient.DefaultWorkerPath})
	case "device-detail", "device-scroll":
		a.seedArtboardDevices()
		if board == "device-scroll" {
			for i := 1; i <= 24; i++ {
				a.model.Snapshot.Workspaces = append(a.model.Snapshot.Workspaces, domain.Workspace{
					ID: fmt.Sprintf("scroll-%d", i), DeviceID: a.endpoint.DeviceID, Name: fmt.Sprintf("测试工作区 %02d", i),
				})
			}
		}
		a.openDevice(a.home)
	case "tailnet-route-last":
		a.seedArtboardDevices()
		a.tailnetStatus.Peers[0].Measurement = &tailnet.Measurement{Path: "derp", RegionName: "Taipei", Region: "tpe", Milliseconds: 32, ObservedAt: time.Now()}
		a.tailnetLastPeers = a.tailnetStatus.Peers
		a.tailnetStatus = tailnet.Snapshot{State: "Stopped"}
		a.model.Connected = false
		a.openDevice(a.home)
	case "device-paths":
		direct := mobileclient.Endpoint{DeviceID: a.endpoint.DeviceID, Address: "mac-mini.local:22", User: "shinya",
			HostFingerprint: "SHA256:mS6bQ1EiR4uJvWRbUZYqASfMJgzeoLaM98ib5qEqOg8", WorkerPath: mobileclient.DefaultWorkerPath}
		tail := direct
		tail.Address, tail.User, tail.TailnetPeerID = "100.64.7.18:22", "remote", "preview-tailnet-peer"
		a.endpoint = direct
		a.savedDevices.Devices[0] = savedMobileDevice{Endpoint: direct, Routes: []mobileclient.Endpoint{direct, tail}}
		a.model.DevicePaths = []app.DevicePathStatus{{ID: direct.RouteID(), Phase: app.PathActive}, {ID: tail.RouteID(), Phase: app.PathReady}}
		a.openDevice(a.home)
	case "offline", "connection-syncing", "connection-switching", "connection-sync-failed":
		a.seedArtboardConversation(false)
		a.model.ConnectionPhase = map[string]app.ConnectionPhase{
			"offline": app.ConnectionUnavailable, "connection-syncing": app.ConnectionSyncing,
			"connection-switching": app.ConnectionSwitching, "connection-sync-failed": app.ConnectionSyncFailed,
		}[board]
		a.model.Connected = board != "offline"
		a.model.Snapshot.ObservedAt = time.Date(2026, 11, 19, 9, 40, 0, 0, time.Local)
		a.model.Drafts["session:"+review.ID] = "再核对下恢复连接后未确认请求的状态。"
		if board == "offline" {
			a.endpoint.DeviceID = workspace.DeviceID
			a.model.ReportFeedback(app.Feedback{ID: "git-error", SessionID: review.ID, Title: "Git 操作未完成",
				Details: "等待设备响应超时。 token=fixture-secret"})
			a.model.ReportFeedback(app.Feedback{ID: "other-error", SessionID: "different-session", WorkspaceID: review.WorkspaceID, Title: "另一会话的回执"})
			a.collectOperationFeedback()
		}
		a.openSession(review.ID)
	case "ssh-auth":
		a.endpoint.HostFingerprint = "SHA256:mS6bQ1EiR4uJvWRbUZYqASfMJgzeoLaM98ib5qEqOg8"
		a.openSSHCredentials(a.home, a.endpoint, "ssh-ed25519")
	case "worker-setup":
		a.endpoint.HostFingerprint = "SHA256:mS6bQ1EiR4uJvWRbUZYqASfMJgzeoLaM98ib5qEqOg8"
		a.openVerifiedWorker(a.home, a.endpoint, nil)
	case "worker-upgrade", "worker-schema-upgrade":
		a.seedWorkerUpgrade()
		if board == "worker-schema-upgrade" {
			a.model.WorkerService.Build.Schema = 15
			a.model.WorkerService.Available.Schema, a.model.WorkerService.Available.MigrationFrom = 16, 15
		}
		a.endpoint.HostFingerprint = "SHA256:mS6bQ1EiR4uJvWRbUZYqASfMJgzeoLaM98ib5qEqOg8"
		a.callUIKit("selectTab", map[string]int{"index": 1}, func(err error) {
			if err == nil {
				a.openWorker(a.devices)
			}
		})
	case "agent-config", "agent-config-linked":
		a.model.WorkerMinor = protocol.Minor
		account := &domain.RuntimeAccount{Mode: "chatgpt", Label: "尚未登录", State: "required"}
		if board == "agent-config-linked" {
			account = &domain.RuntimeAccount{Mode: "account", Label: "ChatGPT", Identity: "developer@example.com", Subscription: "Pro", State: "authenticated"}
		}
		a.openAgentSettings(a.home, domain.HarnessInfo{ID: "codex", Name: "Codex", State: domain.RuntimeReady,
			Capabilities: domain.HarnessCapabilities{SkillManagement: true, HookManagement: true},
			Models:       a.model.Readiness.Models, Account: account,
			Installation: &domain.RuntimeInstallation{Executable: "/Applications/Codex.app/Contents/Resources/codex", Configurable: true,
				SignInMethods: []domain.SignInMethod{domain.SignInDeviceCode, domain.SignInBrowser}, APISetup: true}})
	case "workspace-mcp", "workspace-skills-uncertain", "workspace-hooks-receipt":
		a.previewSkillFailure = board == "workspace-skills-uncertain"
		a.seedPreviewHooks(board == "workspace-hooks-receipt")
		a.model.Readiness.Harnesses = []domain.HarnessInfo{{ID: "codex", Name: "Codex", Configured: true, State: domain.RuntimeReady,
			Capabilities: domain.HarnessCapabilities{ToolServices: true, ToolServiceInspection: true, SkillManagement: true, HookManagement: true}}}
		workspace.Folders = append(slices.Clone(workspace.Folders), workspace.Folders[0]+"/docs")
		for i, saved := range a.model.Snapshot.Workspaces {
			if saved.ID == workspace.ID {
				a.model.Snapshot.Workspaces[i] = workspace
			}
		}
		for i, saved := range a.model.Snapshot.Sessions {
			if saved.WorkspaceID == workspace.ID && !saved.Archived {
				a.model.Snapshot.Sessions[i].Context.CWD = workspace.Folders[0] + "-worktrees/" + saved.ID
			}
		}
		a.openWorkspace(a.home, workspace)
	case "workspace-config":
		a.openWorkspace(a.home, workspace)
	case "directory-picker":
		a.openDirectory(a.home, "", func(string) {})
	case "tailnet-setup":
		a.openTailnet(a.home)
	case "tailnet-peers", "tailnet-pair-draft":
		now := time.Now()
		a.tailnetStatus = tailnet.Snapshot{State: "Running", Network: "example.ts.net", Hostname: "shioji-mobile", Address: "100.64.0.10", Peers: []tailnet.Peer{
			{ID: "online", Name: "Mac mini", DNSName: "build-mac.example.ts.net", Address: "100.64.0.20", OS: "macOS", Online: true,
				Measurement: &tailnet.Measurement{Path: "derp", Region: "tpe", RegionName: "Taipei", Milliseconds: 32, ObservedAt: now}},
			{ID: "offline", Name: "旧 Mac", Address: "100.64.0.21", OS: "macOS"},
		}}
		if board == "tailnet-pair-draft" {
			a.openPair(a.home, mobileclient.Endpoint{Address: "pair.test:2222", User: "remote-operator"})
		} else {
			a.callUIKit("selectTab", map[string]int{"index": 1}, nil)
		}
	case "device-health":
		a.openDeviceHealth(a.home)
	case "hook-review":
		a.seedPreviewHooks(false)
		a.openHooks(a.home, workspace.ID, "", true)
	case "hook-session-archived":
		a.seedPreviewHooks(false)
		for i, item := range a.model.Snapshot.Sessions {
			if item.ID == review.ID {
				a.model.Snapshot.Sessions[i].Archived = true
				a.model.Snapshot.Sessions[i].Context.CWD = workspace.Folders[0] + "-worktrees/review"
			}
		}
		a.openHooks(a.home, workspace.ID, review.ID, false)
	case "port-forwards":
		a.openPortForwards(a.home)
	case "import-session":
		a.openConversationImport(a.home, workspace)
	case "attention":
		a.seedArtboardConversation(false)
		question.SessionID, access.SessionID = "artboard-0", "artboard-1"
		a.model.Snapshot.Sessions[2].Title = "检查共享渲染缓存"
		a.model.Snapshot.PendingRequests = []domain.PendingRequest{approval, question, access}
		a.openAttention(a.home)
	case "attention-shortcut", "attention-shortcut-unread", "attention-shortcut-draft":
		m := a.model
		unread := &m.Snapshot.Sessions[1]
		unread.Completion = &domain.ReplyCompletion{ID: "preview-unread", Unread: true}
		m.CompletedReplies = map[string]app.CompletedReply{unread.ID: {CompletionID: unread.Completion.ID, Unread: true}}
		m.Details[unread.ID] = domain.SessionDetail{Session: *unread, HistoryLoaded: true,
			Items: []domain.Item{{ID: "unread", Kind: "assistant", Text: "输入与阅读位置已校对。", Final: true}}}
		m.Snapshot.Sessions[0], m.Snapshot.Sessions[1] = m.Snapshot.Sessions[1], m.Snapshot.Sessions[0]
		if board != "attention-shortcut-unread" {
			m.Snapshot.PendingRequests = []domain.PendingRequest{question}
			detail := m.Details[review.ID]
			detail.Requests = []domain.PendingRequest{question}
			m.Details[review.ID] = detail
		}
		if board == "attention-shortcut-draft" {
			a.openDraft(a.home)
		}
	case "operation-receipt", "operation-receipt-long":
		a.seedArtboardConversation(false)
		body := "再核对恢复连接后未确认请求的状态。"
		if board == "operation-receipt-long" {
			body += "\n\n第一项：比对发送前后保存的消息与执行上下文。\n\n第二项：确认未提交的草稿保留，并检查同一个请求有没有被重复发送。\n\n第三项：核对断线期间设备没有接纳其他操作。\n\n清单末尾：确认重连后仍可查看完整内容。"
		}
		a.model.Pending["receipt-preview"] = domain.Submission{OperationID: "receipt-preview", SessionID: review.ID,
			WorkspaceID: workspace.ID, Text: body}
		a.model.UncertainSubmissions = map[string]bool{"receipt-preview": true}
		a.openSession(review.ID)
	case "steer":
		a.seedArtboardConversation(false)
		a.model.SessionID = review.ID
		a.model.Drafts[a.model.DraftKey()] = "先检查断线前已接纳的操作，再补连接超时测试。"
		a.openSession(review.ID)
	case "plan":
		a.seedArtboardConversation(false)
		a.openPlan(a.home, review.ID)
	case "child-agent", "background-task":
		a.seedArtboardConversation(false)
		work := a.model.Details[review.ID].Works[0]
		if board == "child-agent" {
			sent := time.Date(2026, 11, 19, 9, 40, 0, 0, time.Local)
			prompt := work.Brief
			work.Brief = ""
			a.model.WorkDetails[a.model.WorkKey(review.ID, work.ID)] = domain.WorkDetail{
				SessionID: review.ID, Work: work, Items: []domain.WorkItem{
					{Item: domain.Item{ID: "child-prompt", Kind: "user", Text: prompt, SentAt: &sent, Final: true}},
					{Item: domain.Item{ID: "child-reply", Kind: "assistant", Text: work.Latest, Final: true}},
				},
			}

		}
		if board == "background-task" {
			work = a.model.Details[review.ID].Works[1]
		}
		parent := a.home
		if a.wide {
			a.selectSession(review.ID)
			parent = a.detail
		}
		a.openWork(parent, review.ID, work)
	case "message-search":
		a.openContentSearch(a.home, "重连")
	case "history-match":
		a.openHistoryMatch(a.home, domain.SearchResult{ItemID: "reply", SessionID: review.ID, WorkspaceID: workspace.ID})
	case "history-match-content":
		a.seedArtboardConversation(false)
		a.seedPreviewImages(review.ID)
		detail := a.model.Details[review.ID]
		prompt, tool, reply := detail.Items[0], detail.Items[1], detail.Items[2]
		prompt.Text, prompt.TurnID = "请核对历史索引中的延迟记录。", "verify-index"
		prompt.Attachments = append(prompt.Attachments, domain.Attachment{ID: "index-file", Name: "index.md", Kind: domain.PathAttachmentResource})
		prompt.ReferencePositions = []domain.ReferencePosition{{AttachmentID: "index-file", Position: 3}}
		tool.Text, tool.TurnID = "确认历史索引已生成。", prompt.TurnID
		reply.Text, reply.TurnID = "**历史索引**已核对。", prompt.TurnID
		ended := time.Date(2026, 11, 20, 9, 42, 0, 0, time.Local)
		elapsed := int64(14000)
		detail.Turns = []domain.Turn{{ID: prompt.TurnID, Status: domain.ExecutionCompleted, CompletedAt: &ended, DurationMS: &elapsed}}
		detail.Items = []domain.Item{prompt, tool,
			{ID: "history-approval", Kind: "request", Name: "已处理索引审批", TurnID: prompt.TurnID, Final: true},
			{ID: "history-receipt", Kind: "request_result", TurnID: prompt.TurnID, Final: true,
				Parts: []domain.ContentPart{{Kind: "text", Label: "核对范围", Text: "读取延迟记录"}}},
			{ID: "hidden-plan", Kind: "plan", Text: "历史索引内部计划"},
			{ID: "hidden-child", Kind: "tool", Text: "历史索引子 Agent 活动", WorkID: "agent"},
			reply,
		}
		a.model.Details[review.ID] = detail
		a.openHistoryMatch(a.home, domain.SearchResult{ItemID: prompt.ID, SessionID: review.ID, WorkspaceID: workspace.ID})
	case "session-actions":
		a.openSessionActions(a.home, review.ID)
	case "session-archive", "session-archive-progress":
		a.model.Snapshot.ArchivePolicy = &domain.ArchivePolicy{DeviceID: a.model.Snapshot.DeviceID, Days: 30}
		for i, title := range []string{"旧版 SSH 配置迁移", "整理运行日志"} {
			s := domain.Session{ID: fmt.Sprintf("archive-%d", i),
				Title: title, WorkspaceID: workspace.ID, Archived: true, Status: domain.ExecutionCompleted}
			a.model.Snapshot.Sessions = append(a.model.Snapshot.Sessions, s)
			if i == 0 {
				duration := int64(2800)
				a.model.Details[s.ID] = domain.SessionDetail{Session: s, HistoryLoaded: true, Items: []domain.Item{
					{ID: "archive-old", Kind: "user", Text: "开始迁移前检查"},
					{ID: "archive-check", Kind: "user", TurnID: "verify-host", Text: "核对新配置的 SSH 主机。"},
					{ID: "archive-pass", Kind: "assistant", TurnID: "verify-host", Final: true,
						Text: "**SSH 主机核验通过。**\n\n主机指纹与预期一致。"},
					{ID: "archive-ask", Kind: "user", TurnID: "backup-config", Text: "旧配置可以清理了吗？"},
					{ID: "archive-backup", Kind: "assistant", TurnID: "backup-config", Final: true, Text: "已保留旧配置的备份。"},
				}, Turns: []domain.Turn{
					{ID: "verify-host", Order: 1, Status: domain.ExecutionCompleted, DurationMS: &duration},
					{ID: "backup-config", Order: 2, Status: domain.ExecutionCompleted, DurationMS: &duration},
				}}
			} else {
				a.model.Details[s.ID] = domain.SessionDetail{Session: s, HistoryLoaded: true}
			}
		}
		archived := domain.Workspace{ID: "archived-build", DeviceID: a.model.Snapshot.DeviceID, Name: "旧版构建工具", Archived: true}
		a.model.Snapshot.Workspaces = append(a.model.Snapshot.Workspaces, archived)
		for i, title := range []string{"保留 iOS 构建记录", "macOS 测试历史"} {
			s := domain.Session{ID: fmt.Sprintf("archived-build-%d", i),
				Title: title, WorkspaceID: archived.ID, Status: domain.ExecutionCompleted}
			a.model.Snapshot.Sessions = append(a.model.Snapshot.Sessions, s)
			if i == 0 {
				detail := domain.SessionDetail{Session: s, HistoryLoaded: true}
				for number := 1; number <= 12; number++ {
					detail.Items = append(detail.Items,
						domain.Item{ID: fmt.Sprintf("build-request-%d", number), Kind: "user", Text: fmt.Sprintf("保留第 %d 次构建的记录。", number)},
						domain.Item{ID: fmt.Sprintf("build-reply-%d", number), Kind: "assistant", Final: true, Text: fmt.Sprintf("构建记录 %d 已保留。", number)},
					)
				}
				a.model.Details[s.ID] = detail
			}
		}
		if board == "session-archive-progress" {
			a.archiveDeletion = &mobileArchiveDeletion{running: true, batch: true, next: 1, targets: make([]mobileArchiveTarget, 2)}
		}
		a.openArchive(a.home)
	case "fork-session":
		review.Status, review.ThreadID = domain.ExecutionCompleted, "preview-fork-source"
		a.model.Snapshot.Sessions[0] = review
		detail := a.model.Details[review.ID]
		detail.Session = review
		detail.Turns[0].Order, detail.Turns[1].Order = 1, 2
		detail.Turns[1].Status = domain.ExecutionCompleted
		detail.Queue.Items = nil
		a.model.Details[review.ID] = detail
		a.model.Drafts["session:"+review.ID] = "保留原会话草稿"
		a.openSession(review.ID)
	case "branch-switch":
		source := a.sessionByID(review.ID)
		source.Status = domain.ExecutionIdle
		a.model.GitStatuses["workspace:"+workspace.ID] = domain.GitStatus{Applicable: true, Branch: source.GitBranch, Branches: []string{"main", source.GitBranch, "fix/inspector"}}
		a.openBranch(a.home, source)
	case "reference-picker":
		a.seedArtboardConversation(false)
		a.model.SessionID = review.ID
		a.openReferences(a.home, a.draftTarget(), a.model.RunReadiness().Capabilities.Skills, nil)
	case "attachment-transfer":
		a.selectSession(review.ID)
		a.openAttachment(a.home, review, domain.Attachment{ID: "preview-attachment", Name: "reconnect-report.md", MIME: "text/plain", Size: 6144})
	case "schedule-list":
		a.openSchedules(a.home, "")
	case "schedule-editor":
		a.model.Readiness.Models[0].Efforts = []string{"medium", "high"}
		a.model.Readiness.Models[0].DefaultEffort = "medium"
		a.model.SetGitStatus("workspace:"+workspace.ID, domain.GitStatus{Applicable: true, Branches: []string{"main", "feat/reconnect"}})
		a.openScheduleEditor(a.home, a.previewSchedule().Schedule, func() {})
	case "schedule-detail":
		a.openSchedule(a.home, a.previewSchedule().Schedule, "", func() {}, nil)
	case "schedule-run":
		schedule := a.previewSchedule()
		a.openScheduleReceipt(a.home, schedule.Schedule, schedule.Runs[0])
	case "notification-prefs":
		a.openNotificationSettings(a.home, "")
	case "input-prefs":
		a.openInputSettings(a.home, "")
	case "pad-parallel":
		a.model.Details["keyboard"] = domain.SessionDetail{Session: a.sessionByID("keyboard"), HistoryLoaded: true, Items: []domain.Item{
			{ID: "parallel-user", Kind: "user", Text: "核对窗口恢复宽度后，选中文件和草稿仍保留。", SentAt: &today},
			{ID: "parallel-response", Kind: "assistant", Text: "我已检查分栏折叠，正在核对恢复后的会话位置。", Final: true},
		}}
		a.openSession(review.ID)
		a.openParallel("keyboard")
	}
}

func (a *mobileApp) previewReferenceFiles(needle string) domain.FileSearchResult {
	paths := []string{"internal/transport/connection.go"}
	if a.previewReferenceKeyboard {
		paths = append(paths, "one/reference.go", "two/reference.go", "three/reference.go",
			"four/reference_four.go", "five/reference_five.go", "six/reference_six.go")
	}
	var result domain.FileSearchResult
	for _, path := range paths {
		if strings.Contains(path, needle) {
			result.Files = append(result.Files, domain.WorkspaceFile{Root: "/Users/shinya/work/Shioji", Path: path})
		}
	}
	return result
}

func (a *mobileApp) previewReferenceSkills() domain.SkillCatalog {
	skills := []domain.Skill{{SkillReference: domain.SkillReference{ID: "review", Name: "review-check"}}}
	if a.previewReferenceKeyboard {
		skills = append(skills, domain.Skill{
			SkillReference: domain.SkillReference{ID: "release", Name: "release-check"},
			Description:    "Review a deployment plan",
		})
	}
	return domain.SkillCatalog{Skills: skills}
}

func previewAgentSignInResult(status domain.SignInStatus, api *domain.APIConfiguration) domain.SignInStatus {
	account := &domain.RuntimeAccount{Mode: "chatgpt", State: "authenticated", Label: "已完成授权"}
	if api != nil {
		credential := "notRequired"
		if api.Credential != "" {
			credential = "configured"
		}
		account = &domain.RuntimeAccount{Mode: "api", State: "authenticated", Label: "Team API", Endpoint: api.Endpoint,
			Model: "fixture-model", Credential: credential}
	}
	status.Phase = domain.SignInCompleted
	status.Challenge = domain.SignInChallenge{}
	status.Profile = &domain.HarnessInfo{ID: status.HarnessID, Name: "Codex", State: domain.RuntimeReady, Account: account,
		Installation: &domain.RuntimeInstallation{Configurable: true, SignInMethods: []domain.SignInMethod{domain.SignInDeviceCode, domain.SignInBrowser}}}
	return status
}

const previewDiagnostics = "worker ping: ok\n"

func previewLinkedFile(target string) (app.LinkedFile, string, error) {
	const root = "/Users/shioji/work/Shioji"
	if !path.IsAbs(target) {
		target = path.Join(root, target)
	}
	var text string
	switch target {
	case root + "/internal/transport/connection.go":
		text = "package transport\n\n"
		for i := 3; i <= 32; i++ {
			if i == 17 {
				text += "func reconnect() error {\n"
			} else if i == 18 {
				text += "\treturn resumeWorker()\n"
			} else if i == 19 {
				text += "}\n"
			} else {
				text += "// 恢复执行服务连接。\n"
			}
		}
	case root + "/docs/runbook.md":
		text = "# 运维手册\n\n[阅读故障恢复](recovery.md)"
	case root + "/docs/recovery.md":
		text = "# 恢复步骤\n\n重新连接执行服务，然后查询同一轮次的回执。"
	case root + "/docs/reconnect-diagram.md":
		text = "# 重连流程说明\n\n```mermaid\nsequenceDiagram\n  手机->>Worker: 查询回执\n  Worker-->>手机: <b>已接纳</b>\n```"
	case root + "/diagnostics.txt":
		text = previewDiagnostics
	default:
		return app.LinkedFile{}, "", fmt.Errorf("文件不在当前工作区：%s", target)
	}
	return app.LinkedFile{Resource: domain.FileResource{Path: target, Name: path.Base(target)}}, text, nil
}

func previewRecentTranscript(detail domain.SessionDetail) domain.SessionDetail {
	detail.History = &domain.HistoryWindow{First: 8, Last: 25, Older: true}
	detail.Turns = nil
	detail.Items = []domain.Item{
		{ID: "history-recent-input", Kind: "user", Text: "这条消息留在原来的阅读位置。", Order: 8},
		{ID: "history-recent-reply", Kind: "assistant", Text: "这是最近一轮的回复。", Order: 9},
	}
	for order := int64(10); order <= detail.History.Last; order++ {
		id := fmt.Sprintf("history-recent-%d", order)
		detail.Items = append(detail.Items, domain.Item{ID: id, Kind: "user", Order: order, Text: fmt.Sprintf("稍后的消息 %d", order)})
	}
	return detail
}

func previewOlderTranscript(detail domain.SessionDetail) domain.SessionDetail {
	return domain.SessionDetail{Session: detail.Session,
		History: &domain.HistoryWindow{First: 5, Last: 7},
		Items: []domain.Item{
			{ID: "older-input", Kind: "user", Text: "排查断线前的记录。", Order: 5},
			{ID: "older-tool", Kind: "tool", Name: "检查事件顺序", Order: 6, Final: true},
			{ID: "older-reply", Kind: "assistant", Text: "这是前一轮的回复。", Order: 7},
		},
	}
}

func previewFindWindow(detail domain.SessionDetail, item string) domain.SessionDetail {
	if item == "older-input" || item == "older-reply" || item == "older-tool" {
		history := previewOlderTranscript(detail)
		history.History.Newer = true
		history.Items[1] = domain.Item{ID: "older-reply", Kind: "assistant", Order: 6, Final: true,
			Text: "断线日志：**reconnect** 重试成功。\n\n恢复前再次读取 `reconnect` 记录。"}
		history.Items[2] = domain.Item{ID: "older-tool", Kind: "tool", Order: 7, Final: true,
			Name: "检查事件顺序", Presentation: "command", Text: "工具输出 reconnect"}
		return history
	}
	detail = previewRecentTranscript(detail)
	detail.Items[len(detail.Items)-1].Text = "恢复结果：reconnect 完成。"
	return detail
}

func (a *mobileApp) previewSessionDeviceOpening(endpoint mobileclient.Endpoint) {
	catalog := a.sessionCatalogs[endpoint.DeviceID]
	a.endpoint = endpoint
	a.model.Connected, a.model.ConnectionPhase = false, app.ConnectionOpening
	time.AfterFunc(4*time.Second, func() {
		mygo.RunOnMain(func() {
			if a.endpoint.DeviceID != endpoint.DeviceID {
				return
			}
			a.model.Connected, a.model.ConnectionPhase = true, app.ConnectionIdle
			a.model.Snapshot = catalog.Snapshot
			session := catalog.Snapshot.Sessions[0]
			a.model.HistoryReads[session.ID] = &app.HistoryRead{Pending: true}
			a.completeSessionListOpening()
			a.refresh()
			time.AfterFunc(4*time.Second, func() {
				mygo.RunOnMain(func() {
					if a.model.Snapshot.DeviceID != endpoint.DeviceID {
						return
					}
					delete(a.model.HistoryReads, session.ID)
					a.model.Details[session.ID] = domain.SessionDetail{Session: session, HistoryLoaded: true,
						Items: []domain.Item{{ID: "loaded-remote", Kind: "assistant", Text: "远端历史已加载。", Final: true}}}
					a.refresh()
				})
			})
		})
	})
}

func (a *mobileApp) seedArtboardDevices() {
	a.previewDeviceName = "Mac mini"
	a.endpoint = mobileclient.Endpoint{Address: "mac-mini.local:22", User: "shinya",
		DeviceID: a.model.Snapshot.DeviceID, TailnetPeerID: "preview-tailnet-peer"}
	a.savedDevices.Devices = []savedMobileDevice{
		{Endpoint: a.endpoint},
		{Endpoint: mobileclient.Endpoint{DeviceID: "preview-laptop", Address: "macbook-pro.local:22", User: "shinya"}},
		{Endpoint: mobileclient.Endpoint{DeviceID: "preview-builder", Address: "build-mac.local:22", User: "shinya"}},
	}
	a.tailnetStatus = tailnet.Snapshot{State: "Running", Peers: []tailnet.Peer{
		{ID: a.endpoint.TailnetPeerID, Address: "mac-mini.local", Online: true,
			Measurement: &tailnet.Measurement{Path: "direct", Milliseconds: 12, ObservedAt: time.Now()}},
	}}
	a.model.Connected = true
	a.model.Snapshot.Workspaces[0].Name = "Shioji"
}

// Simulated worker receipt for the native preview. The source and completed
// fork go through the same navigation and history projection as live receipts.
func (a *mobileApp) previewFork(parent *mygo.Window, source domain.Session, input domain.HistoryChange) {
	history := a.model.Details[source.ID]
	var keep int64
	for _, turn := range history.Turns {
		keep = turn.Order
		if turn.ID == input.TurnID {
			break
		}
	}
	target := source
	target.ID, target.ThreadID = "fork:"+input.OperationID, "thread:"+input.OperationID
	target.Title, target.ForkedFrom = source.Title+"（分叉）", source.ID
	a.finishFork(parent, source, domain.HistoryOperation{ID: input.OperationID, Input: input, Source: source, Session: target,
		State: domain.HistoryComplete, KeepOrder: keep}, history)
}

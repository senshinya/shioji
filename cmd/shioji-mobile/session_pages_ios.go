package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

func (a *mobileApp) openSessionActions(parent *mygo.Window, sid string) {
	opened := a.sessionByID(sid)
	name, savedName, titleRevision := opened.Title, opened.Title, opened.TitleRevision
	req := mobileRequest{}
	pinError := ""
	var w *mygo.Window
	canEditTitle := func(s domain.Session) bool {
		workspace := a.workspace(s.WorkspaceID)
		return s.ID != "" && !s.Archived && !workspace.Archived && workspace.DeviceID == a.model.Snapshot.DeviceID &&
			!req.busy && a.model.Connected && (a.preview || a.model.ConnectionEpoch != "")
	}
	saveTitle := func(useSource bool) {
		s := a.sessionByID(sid)
		if !a.pageCurrent(w) || !canEditTitle(s) {
			return
		}
		method := "session.rename"
		input := protocol.SessionChangeInput{SessionID: sid, TitleRevision: titleRevision, Title: strings.TrimSpace(name)}
		if useSource {
			if !s.TitleOverride || s.SourceTitle == "" {
				return
			}
			method = "session.reset-title"
			input.TitleRevision, input.Title = s.TitleRevision, ""
		} else if input.Title == "" {
			req.err = "请输入会话名称"
			w.Invalidate()
			return
		}
		submittedName := name
		a.pageJob(w, &req, method, input, decodePage(func(updated domain.Session) error {
			if updated.ID != sid || updated.WorkspaceID != s.WorkspaceID || updated.TitleRevision <= input.TitleRevision {
				return fmt.Errorf("名称回执不属于当前会话")
			}
			if live := a.sessionByID(sid); live.TitleRevision > updated.TitleRevision {
				updated = live
			}
			savedName, titleRevision = updated.Title, updated.TitleRevision
			if name == submittedName {
				name = savedName
			}
			return nil
		}))
	}
	w = a.page("会话操作", func(c *ui.Context) {
		mobilePageBody(c, func() {
			s := a.sessionByID(sid)
			if s.ID == "" {
				mobileMessage(c, "会话已不可用")
				return
			}
			if !req.busy && name == savedName && s.TitleRevision >= titleRevision {
				name, savedName, titleRevision = s.Title, s.Title, s.TitleRevision
			}
			mobilePageContext(c, a.workspace(s.WorkspaceID).Name, a.deviceName())
			mobileSection(c, "整理")
			formField(c, "会话名称", "", &name)
			edited := strings.TrimSpace(name) != savedName
			mobileDisclosureBody(c.Key("save-name"), edited, func(body *ui.Context) {
				action(body, "保存名称", func() { saveTitle(false) }).AlignSelf(ui.End).PaddingX(0).
					Disabled(!canEditTitle(s) || strings.TrimSpace(name) == "")
			})
			if s.TitleOverride && s.SourceTitle != "" {
				const restoreTitle = "使用来源名称"
				mobileDetailRow(c.Key("source-name"), restoreTitle, s.SourceTitle, "", nil, nil, nil).
					Role(ui.RoleButton).Label(restoreTitle).Value(s.SourceTitle).
					OnClick(func() { saveTitle(true) }).Disabled(!canEditTitle(s))
			}
			req.status(c)
			ref := app.SessionLocation{SessionID: s.ID, WorkspaceID: s.WorkspaceID, DeviceID: a.model.Snapshot.DeviceID}
			if !s.Archived && !a.workspace(s.WorkspaceID).Archived {
				pinned := slices.Contains(a.sessionPins, ref)
				mobileToggle(c, "置顶会话", &pinned, func() {
					pinError = ""
					if err := a.setSessionPin(ref, pinned); err != nil {
						pinError = err.Error()
					}
				})
				if pinError != "" {
					label(c, pinError, captionTextSize, palette(c).warning)
				}
			}
			workspace := a.workspace(s.WorkspaceID)
			if !workspace.Archived {
				archiveTitle := "归档会话"
				if s.Archived {
					archiveTitle = "恢复会话"
				}
				mobileRow(c, archiveTitle, "", "", filesSymbol, func() {
					a.confirmSessionArchive(w, s)
				})
			}
			if s.Archived || workspace.Archived {
				catalog := a.sessionCatalog(workspace.DeviceID)
				device := catalog.ArchiveDevice()
				row := mobileclient.ArchivedConversation{SessionCatalogEntry: mobileclient.SessionCatalogEntry{Session: s, Workspace: workspace}, Device: device}
				action(c, "彻底删除会话", func() {
					a.confirmArchiveDeletion(w, []mobileArchiveTarget{archiveSessionTarget(row)}, false, "")
				}).AlignSelf(ui.Start).PaddingX(0).Disabled(req.busy || !a.canUseArchiveDevice(device, 40))
			}
			mobileGroupHeading(c, "历史与执行")
			mobileRow(c, "搜索对话内容", "", "", searchSymbol, func() { a.openConversationFindFromActions(w, parent, sid) })
			mobileDetailRow(c, "分叉会话", "", "分叉会话", branchSymbol, func() { a.openFork(w, s, "") }, nil).Disabled(!a.forkAvailable(s))
			if s.ForkedFrom != "" {
				origin := a.forkOrigin(s)
				subtitle := origin.Title
				if origin.ID == "" {
					subtitle = "来源会话已不可用"
				}
				const originTitle = "查看来源会话"
				mobileDetailRow(c.Key("fork-origin"), originTitle, subtitle, originTitle, branchSymbol, func() {
					if !a.pageCurrent(w) {
						return
					}
					current := a.forkOrigin(a.sessionByID(sid))
					if current.ID == "" {
						return
					}
					a.openSession(current.ID)
					if a.wide && a.detail != nil {
						root := a.detail
						if parent == a.home {
							root = a.home
						}
						a.callUIKit("showPage", map[string]uintptr{"window": root.NativeHandle()}, nil)
					}
				}, nil).Disabled(origin.ID == "").Value(subtitle)
			}
			a.historyStatus(c, w, s)
			mobileRow(c, "切换分支", s.GitBranch, "", branchSymbol, func() { a.openBranch(w, s) })
			mobileRow(c, "会话 Hooks", "", "", permissionSymbol, func() { a.openHooks(w, s.WorkspaceID, s.ID, false) })
			a.sessionReloadRow(c, s)
		})
	})
	a.pushNative(parent, w)
	a.querySessionReload(sid)
}

func (a *mobileApp) forkOrigin(fork domain.Session) domain.Session {
	source := a.sessionByID(fork.ForkedFrom)
	ws := a.workspace(fork.WorkspaceID)
	device := a.model.Snapshot.DeviceID
	if source.ID == "" || source.ID == fork.ID || source.WorkspaceID != fork.WorkspaceID ||
		device == "" || ws.DeviceID != device || source.Context.DeviceID != "" && source.Context.DeviceID != device {
		return domain.Session{}
	}
	return source
}

func (a *mobileApp) confirmSessionArchive(parent *mygo.Window, s domain.Session) {
	title := "归档会话"
	if s.Archived {
		title = "恢复会话"
	}
	req := mobileRequest{}
	var w *mygo.Window
	w = a.page(title, func(c *ui.Context) {
		mobilePageBody(c, func() {
			mobileRow(c, s.Title, a.workspace(s.WorkspaceID).Name, "", filesSymbol, nil)
			if !s.Archived {
				mobileMessage(c, "归档会话会结束其运行环境。历史、草稿和文件仍可保留。")
			}
			action(c, title, func() {
				if a.preview {
					if i := slices.IndexFunc(a.model.Snapshot.Sessions, func(session domain.Session) bool { return session.ID == s.ID }); i >= 0 {
						a.model.Snapshot.Sessions[i].Archived = !s.Archived
						w.Pop()
						a.refresh()
					}
					return
				}
				a.pageJob(w, &req, "session.archive", protocol.SessionChangeInput{SessionID: s.ID, Revision: s.Revision,
					Archived: !s.Archived}, func(_ json.RawMessage) error { w.Pop(); return nil })
			}).Disabled(req.busy || !a.model.Connected)
			req.status(c)
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openBranch(parent *mygo.Window, s domain.Session) {
	var w *mygo.Window
	var status domain.GitStatus
	var statusEpoch, selected string
	var choseBranch bool
	req := mobileRequest{}
	q := protocol.GitQuery{SessionID: s.ID, WorkspaceID: s.WorkspaceID}
	read := func() {
		epoch := a.model.ConnectionEpoch
		a.pageJob(w, &req, "git.status", q, decodePage(func(in domain.GitStatus) error {
			if a.model.ConnectionEpoch == epoch {
				status, statusEpoch = in, epoch
			}
			return nil
		}))
	}
	w = a.page("分支", func(c *ui.Context) {
		m := a.model
		current := a.sessionByID(s.ID)
		workspace := a.workspace(s.WorkspaceID)
		observed := status
		fresh := status.Applicable && statusEpoch == m.ConnectionEpoch
		branch := current.GitBranch
		switch {
		case choseBranch:
			branch = selected
		case current.ContextChanged && fresh:
			branch = status.Branch
		}
		confirming := current.ContextChanged && (!choseBranch || branch == status.Branch)
		supported := m.WorkerMinor >= protocol.GitConfirmationMinor
		disabled := !fresh || !m.Connected || req.busy || current.ID == "" || current.Archived || workspace.Archived ||
			current.Status.BlocksCheckout() || current.Goal.Active() || current.ExecutionGuard != "" || m.HasPendingInputForSession(s.ID)
		for _, request := range m.Snapshot.PendingRequests {
			if request.SessionID == s.ID && request.State.AwaitingResolution() {
				disabled = true
				break
			}
		}
		mobilePageWithFooter(c, func() {
			mobilePageContext(c, s.Title, workspace.Name, a.deviceName())
			mobileSection(c, "当前分支")
			actual := current.GitBranch
			head := ""
			if status.Applicable {
				actual = status.Branch
				if actual == "" {
					actual = "分离 HEAD"
				}
				head = status.Head[:min(7, len(status.Head))]
			}
			mobileRow(c, actual, "", head, branchSymbol, nil)
			if current.ContextChanged && current.GitBranch != "" {
				mobileMessage(c, "此会话原先使用 "+current.GitBranch+"。")
			}
			if fresh && len(status.Branches) != 0 {
				target := branch
				if confirming {
					target = "选择其他分支"
				}
				formRow(c, "切换到", func() {
					iconAction(c, target, "切换到", nil, 0, pickerSymbol, pickerIconSize, bodyTextSize, 400, func() {
						epoch := statusEpoch
						choices := observed.Branches
						detached := observed.Branch == "" && current.ContextChanged
						if detached {
							choices = append([]string{""}, choices...)
						}
						a.openBranchPicker(w, choices, branch, func(name string) bool {
							if a.model != m || !m.Connected || m.ConnectionEpoch != epoch || statusEpoch != epoch ||
								status.Repository != observed.Repository ||
								!(name == "" && detached && status.Branch == "" || slices.Contains(status.Branches, name)) {
								return false
							}
							selected, choseBranch = name, true
							w.Invalidate()
							return true
						})
					}).PaddingX(0).MaxWidthPercent(62).Value(branch).Disabled(req.busy || !m.Connected)
				})
			}
			switch {
			case confirming && !supported:
				mobileMessage(c, "更新执行服务后可确认当前分支。")
			case confirming && status.Dirty:
				mobileMessage(c, "确认后可沿用当前分支，未提交改动会保留。")
			case !confirming && status.Dirty:
				mobileMessage(c, "工作目录有未提交改动。处理改动后再切换。")
			case !confirming && fresh:
				mobileMessage(c, "切换会更新此会话所在设备的共享工作目录。")
			}
			req.status(c)
			if !req.busy && (req.err != "" || status.Applicable && !fresh) {
				action(c, "刷新分支", read)
			}
		}, func() {
			if confirming {
				primaryAction(c, "确认当前分支", func() {
					a.pageJob(w, &req, "git.confirm", protocol.ConfirmCheckoutInput{
						CheckoutInput: protocol.CheckoutInput{GitQuery: q, Branch: observed.Branch}, Head: observed.Head,
					}, func(_ json.RawMessage) error { w.Pop(); return nil })
				}).Disabled(disabled || !supported || req.err != "")
			} else {
				primaryAction(c, "检查并切换", func() {
					a.pageJob(w, &req, "git.checkout", protocol.CheckoutInput{GitQuery: q, Branch: branch},
						func(_ json.RawMessage) error { w.Pop(); return nil })
				}).Disabled(disabled || status.Dirty || branch == "" || branch == status.Branch)
			}
		})
	})
	if a.preview {
		status, statusEpoch = a.model.GitStatuses["workspace:"+s.WorkspaceID], a.model.ConnectionEpoch
	} else {
		read()
	}
	a.pushNative(parent, w)
}

func (a *mobileApp) branchContextNotice(c *ui.Context, parent *mygo.Window, s domain.Session, editor *mobileDraftEditor, right, connected bool) {
	mobileInlinePanel(c).PaddingX(panelInset).Shrink(0).Children(func() {
		ui.Row(c).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Wrap().Children(func() {
			label(c, "执行分支已改变", captionTextSize, palette(c).warning).Grow(1).Role(ui.RoleStatus)
			button := action(c.Key("review"), "查看分支", func() {
				if editor.current(a, parent) {
					a.openBranch(parent, s)
				}
			}).PaddingX(6).Disabled(!connected)
			if right {
				button.Label("查看右侧分支")
			}
		})
	})
}

func (a *mobileApp) historyRequest(key app.WorkKey) *mobileHistoryRequest {
	if req := a.historyRequests[key]; req != nil {
		return req
	}
	if a.historyRequests == nil {
		a.historyRequests = make(map[app.WorkKey]*mobileHistoryRequest)
	}
	req := &mobileHistoryRequest{}
	a.historyRequests[key] = req
	return req
}

func (a *mobileApp) forkAvailable(s domain.Session) bool {
	req := a.historyRequests[a.model.WorkKey(s.ID, "")]
	return (req == nil || !req.busy && req.visual == nil) && a.model.HistoryActions[s.ID].OperationID == "" &&
		a.model.HistoryEdits[s.ID].ItemID == "" && a.historyActionAvailable(s) &&
		a.model.SessionReadiness(s).Capabilities.Fork
}

func (a *mobileApp) historyStatus(c *ui.Context, parent *mygo.Window, s domain.Session) {
	m := a.model
	pending := m.HistoryActions[s.ID]
	if pending.OperationID == "" {
		a.historyDraftRecovery(c.Key("history-draft"), parent, s)
	}
	req := a.historyRequests[m.WorkKey(s.ID, "")]
	showComplete := req != nil && c.Now().Before(req.completedUntil)
	if pending.OperationID == "" && (req == nil || !req.busy && req.err == "" && !showComplete) {
		return
	}
	if showComplete {
		c.After(req.completedUntil.Sub(c.Now()))
	}
	query, resume := "查询分叉回执", "继续完成分叉"
	run := func(intent historyIntent) { a.submitFork(parent, s, "", intent) }
	if pending.Restart {
		query, resume = "核对重开回执", "继续完成重开"
		run = func(intent historyIntent) { a.submitHistoryEdit(parent, s, nil, intent) }
	}
	panel := mobileContent(c.Key("history-status"), contentMaxWidth).Padding(2, pageInset, 4)
	panel.Children(func() {
		inset := c.Key("history-panel")
		mobileInlinePanel(inset).Padding(4, panelInset).Children(func() {
			if req != nil && req.busy {
				oneLine(inset, "正在处理…", captionTextSize, palette(c).secondary).Role(ui.RoleStatus)
				return
			}
			if req != nil && req.err != "" {
				label(inset, req.err, captionTextSize, palette(c).warning)
			}
			if showComplete {
				label(inset, "操作已完成", captionTextSize, palette(c).secondary).Role(ui.RoleStatus)
			}
			if pending.OperationID != "" {
				ui.Row(inset).Wrap().Gap(4).Children(func() {
					action(inset, query, func() { run(historyLookup) }).KeepFocus().Disabled(!m.Connected)
					if req != nil && req.canContinue(pending.OperationID, m.ConnectionEpoch) {
						action(inset, resume, func() { run(historyContinue) }).KeepFocus().Disabled(!m.Connected)
					}
					action(inset, "取消本次操作", func() { a.cancelHistory(parent, s) }).KeepFocus().Disabled(!m.Connected)
				})
			}
		})
	})
}

func (a *mobileApp) detachedHistoryEdit(sid string) app.HistoryEdit {
	edit := a.model.HistoryEdits[sid]
	if edit.ItemID == "" || edit.OperationID != "" {
		return app.HistoryEdit{}
	}
	for _, item := range a.model.Details[sid].Items {
		if item.Kind == "user" && item.ID == edit.ItemID {
			return app.HistoryEdit{}
		}
	}
	return edit
}

func (a *mobileApp) historyDraftRecovery(c *ui.Context, parent *mygo.Window, s domain.Session) {
	m := a.model
	edit := a.detachedHistoryEdit(s.ID)
	if edit.ItemID == "" {
		return
	}
	current := func() bool {
		return a.model == m && a.visibleLinkSource(parent, s) && m.HistoryActions[s.ID].OperationID == "" &&
			a.detachedHistoryEdit(s.ID).ItemID == edit.ItemID
	}
	key := m.SessionHistoryDraftKey(s.ID)
	mobileContent(c, contentMaxWidth).Padding(2, pageInset, 4).Children(func() {
		mobileInlinePanel(c).Padding(4, panelInset).Children(func() {
			ui.Row(c).Wrap().Gap(panelContentGap).AlignItems(ui.Center).Children(func() {
				label(c, "修改稿已保留", captionTextSize, palette(c).secondary).Role(ui.RoleStatus).Grow(1)
				action(c.Key("copy"), "复制修改稿", func() {
					if !current() {
						return
					}
					item := domain.Item{Text: m.Drafts[key], ReferencePositions: m.DraftReferencePositions(key)}
					for _, file := range m.Attachments[key] {
						item.Attachments = append(item.Attachments, file.Attachment)
					}
					for _, skill := range m.DraftSkills[key] {
						item.Skills = append(item.Skills, skill.Reference)
					}
					mygo.Clipboard.WriteText(sentMessageText(mobileclient.SentMessageRuns(item)))
					c.AddToast(ui.Toast{ID: key, Title: "修改稿已复制"})
				}).PaddingX(4).KeepFocus().Disabled(!m.HasDraftContent(key))
				action(c.Key("discard"), "放弃编辑", func() {
					if current() {
						m.CancelSessionHistoryEdit(s.ID)
						a.save()
						a.refresh()
					}
				}).PaddingX(4).Label("放弃历史编辑").KeepFocus()
			})
		})
	})
}

func (a *mobileApp) openFork(parent *mygo.Window, s domain.Session, turn string) {
	if !a.forkAvailable(s) {
		return
	}
	req := a.historyRequest(a.model.WorkKey(s.ID, ""))
	if turn == "" {
		req.hasAnchor = false
	}
	detail := a.model.Details[s.ID]
	in := domain.HistoryChange{SessionID: s.ID, Revision: s.Revision, TurnID: turn}
	if detail.Session.ID == "" {
		a.submitFork(parent, s, turn, historySubmit)
		return
	}
	a.animateHistoryExit(parent, s, req, in, func() { a.submitFork(parent, s, turn, historySubmit) })
}

func (a *mobileApp) submitFork(parent *mygo.Window, s domain.Session, turn string, intent historyIntent) {
	m := a.model
	if !m.Connected || !a.visibleLinkSource(parent, s) || !a.preview && a.controller == nil {
		return
	}
	s = a.sessionByID(s.ID)
	key := m.WorkKey(s.ID, "")
	req := a.historyRequest(key)
	in := m.HistoryActions[s.ID]
	if req.busy || in.Restart {
		return
	}

	method := "session.fork"
	var input any
	var prepare func() error
	switch intent {
	case historyLookup:
		if in.OperationID == "" {
			return
		}
		method = "session.history-operation"
		input = protocol.HistoryOperationQuery{ID: in.OperationID, SessionID: s.ID}
	case historyContinue:
		if a.stateWriter == nil || !req.canContinue(in.OperationID, m.ConnectionEpoch) {
			return
		}
		req.prepared = ""
		input, prepare = in, a.stateWriter.FlushLatest
	case historySubmit:
		if !a.forkAvailable(s) && (req.visual == nil || !req.visual.sent) {
			return
		}
		req.completedUntil = time.Time{}
		in = domain.HistoryChange{OperationID: domain.NewID(), SessionID: s.ID, Revision: s.Revision, TurnID: turn}
		if a.preview {
			a.previewFork(parent, s, in)
			delete(a.historyRequests, key)
			return
		}
		if a.stateWriter == nil {
			req.err = "当前无法保存分叉回执"
			a.refresh()
			return
		}
		input, prepare = in, a.stateWriter.FlushLatest
		m.HistoryActions[s.ID] = in
		a.save()
	}
	clear := func() { a.clearHistoryAction(in, req, false) }
	clearOnMissing := clear
	if intent == historyContinue {
		clearOnMissing = nil
	}
	sourceDetail := m.Details[s.ID]
	sourceDetail.Items = slices.Clone(sourceDetail.Items)
	sourceDetail.Turns = slices.Clone(sourceDetail.Turns)
	sourceDetail.Requests = slices.Clone(sourceDetail.Requests)
	epoch := m.ConnectionEpoch
	a.historyPageJob(parent, &req.mobileRequest, method, input, prepare, clearOnMissing, decodePage(func(op domain.HistoryOperation) error {
		if !historyReceiptMatches(op, in, s, m.Snapshot.DeviceID) {
			return fmt.Errorf("分叉回执与源会话不一致")
		}
		switch op.State {
		case domain.HistoryFailed:
			clear()
		case domain.HistoryForked:
			req.prepared, req.epoch = op.ID, epoch
		case domain.HistoryComplete:
			clear()
			a.finishFork(parent, s, op, sourceDetail)
		}
		a.refresh()
		if op.Error != "" {
			return fmt.Errorf("%s", op.Error)
		}
		if op.State == domain.HistoryFailed {
			return fmt.Errorf("分叉未能完成")
		}
		return nil
	}))
	a.refresh()
}

func (a *mobileApp) finishFork(parent *mygo.Window, source domain.Session, op domain.HistoryOperation, history domain.SessionDetail) {
	if a.sessionByID(op.Session.ID).ID == "" {
		a.model.Snapshot.Sessions = append(a.model.Snapshot.Sessions, op.Session)
	}
	if !a.visibleLinkSource(parent, source) {
		return
	}
	a.seedForkHistory(op, history)
	if req := a.historyRequests[a.model.WorkKey(source.ID, "")]; req != nil && req.visual != nil && req.visual.placed {
		a.forkMotionTarget = mobileForkPosition{session: op.Session.ID, thread: op.Session.ThreadID,
			turn: req.visual.input.TurnID, y: req.visual.y}
		req.visual = nil
	}
	delete(a.historyRequests, a.model.WorkKey(source.ID, ""))
	if parent == a.detail {
		if a.parallelOwner == nil && source.ID != a.model.SessionID && source.ID == a.parallel {
			a.openParallel(op.Session.ID)
		} else {
			a.selectSession(op.Session.ID)
		}
		return
	}
	if a.wide && a.chat == nil && a.detail != nil {
		a.selectSession(op.Session.ID)
		a.callUIKit("showPage", map[string]uintptr{"window": a.detail.NativeHandle()}, nil)
		return
	}
	a.openSessionFrom(parent, op.Session.ID)
}

func (a *mobileApp) seedForkHistory(op domain.HistoryOperation, source domain.SessionDetail) {
	if a.model.Details[op.Session.ID].HasLoadedHistory() ||
		source.Session.ID != op.Source.ID || source.Session.ThreadID != op.Source.ThreadID ||
		source.Session.Revision != op.Source.Revision || !source.HasLoadedHistory() || source.HasMore ||
		source.History != nil && (source.History.Invalidated || source.History.Older || source.History.Newer) {
		return
	}
	target := a.sessionByID(op.Session.ID)
	if target.ThreadID != op.Session.ThreadID || target.Revision != op.Session.Revision {
		return
	}
	detail := domain.SessionDetail{Session: target, Queue: domain.QueueState{SessionID: target.ID}}
	kept := make(map[string]bool)
	for _, turn := range source.Turns {
		if turn.Order <= op.KeepOrder {
			kept[turn.ID] = true
			detail.Turns = append(detail.Turns, turn)
		}
	}
	for _, item := range source.Items {
		if kept[item.TurnID] {
			detail.Items = append(detail.Items, item)
		}
	}
	if len(detail.Items) == 0 {
		return
	}
	detail.HistoryLoaded = true
	for _, request := range source.Requests {
		if kept[request.TurnID] && !request.State.AwaitingResolution() {
			request.SessionID = target.ID
			detail.Requests = append(detail.Requests, request)
		}
	}
	a.model.Details[target.ID] = detail
}

func (a *mobileApp) openHistoryMatch(parent *mygo.Window, hit domain.SearchResult) {
	source := a.sessionByID(hit.SessionID)
	if source.ID == "" || source.WorkspaceID != hit.WorkspaceID {
		return
	}
	a.openConversationFrom(parent, source.ID, transcriptJump{
		session: source.ID, thread: source.ThreadID, item: hit.ItemID, center: true,
	})
}

func (a *mobileApp) operationReceipt(c *ui.Context, parent *mygo.Window, submission domain.Submission) {
	m, id := a.model, submission.OperationID
	if !mobileclient.CanCheckSubmission(m, id, submission.WorkspaceID, submission.SessionID) {
		return
	}
	req := ui.Local(c.Root(), "operation-receipt:"+id, func() mobileRequest { return mobileRequest{} })
	originalOpen := ui.Local(c.Root(), m.WorkKey(submission.SessionID, "receipt-original:"+id), func() bool { return false })
	showOriginal := *originalOpen && submission.Text != ""
	p := palette(c)
	mobileInlinePanel(c).Padding(panelInset).Gap(2 * panelContentGap).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, clockSymbol).Size(15, 15).TextColor(p.secondary)
			title := "消息是否送达仍待确认"
			if submission.QuestionReply != nil {
				title = "答案待核对"
			}
			oneLine(c, title, 13, p.ink).FontWeight(620).Grow(1)
		})
		if submission.Text != "" && !showOriginal {
			label(c, submission.Text, 13, p.secondary).MaxLines(2).Ellipsis("…")
		}
		ui.Row(c.Key("receipt-actions")).Wrap().Gap(panelContentGap).AlignItems(ui.Center).Children(func() {
			primaryAction(c.Key("check-receipt"), "查询接纳结果", func() {
				if a.model != m || !mobileclient.CanCheckSubmission(m, id, submission.WorkspaceID, submission.SessionID) {
					return
				}
				a.pageJob(parent, req, "operation", m.SubmissionReceiptQuery(id), decodePage(func(op domain.Operation) error {
					missing := op.Deleted && op.State == domain.OperationRejected && op.SessionID == ""
					if op.ID != id || !missing && submission.SessionID != "" && op.SessionID != submission.SessionID {
						return fmt.Errorf("接纳回执不属于当前提交")
					}
					m.ApplyOperation(op)
					a.save()
					return nil
				}))
			}).Grow(1).Disabled(parent == nil || req.busy || !m.Connected)
			if submission.Text != "" {
				title := "查看原文"
				if showOriginal {
					title = "收起原文"
				}
				action(c.Key("toggle-original"), title, func() { *originalOpen = !*originalOpen }).
					KeepFocus().Expanded(showOriginal)
			}
		})
		if submission.Text != "" {
			mobileDisclosureBody(c.Key("receipt-original"), showOriginal, func(body *ui.Context) {
				ui.Scroll(body).FillWidth().MaxHeight(112).Clip().Label("待核对消息原文").Children(func() {
					label(body, submission.Text, 13, p.secondary).FillWidth().Selectable()
				})
			})
		}
		req.status(c)
	})
}

func (a *mobileApp) openParallelPicker(parent *mygo.Window, sid string) {
	var w *mygo.Window
	message := ""
	w = a.page("并排查看会话", func(c *ui.Context) {
		mobilePageBody(c, func() {
			if message != "" {
				label(c, message, captionTextSize, palette(c).warning)
			}
			for _, catalog := range a.browseCatalogs() {
				for _, entry := range mobileclient.BrowseSessionCatalog(catalog.Snapshot, mobileclient.WorkspaceLocation{}, false) {
					id, other := catalog.Snapshot.DeviceID, entry.Session
					if id == a.model.Snapshot.DeviceID && other.ID == sid {
						continue
					}
					ref := app.SessionLocation{DeviceID: id, WorkspaceID: entry.Workspace.ID, SessionID: other.ID}
					scope := entry.Workspace.Name + " · " + catalog.Label()
					mobileRow(c.Key(ref), other.Title, scope, "", layersSymbol, func() {
						if err := a.openParallelSession(ref); err != nil {
							message = err.Error()
							w.Invalidate()
							return
						}
						a.callUIKit("showPage", map[string]uintptr{"window": a.detail.NativeHandle()}, nil)
					})
				}
			}
		})
	})
	a.refreshSessionCatalogs()
	a.pushNative(parent, w)
}

func (a *mobileApp) openParallel(id string) {
	source := a.sessionByID(id)
	if !a.wide || id == a.model.SessionID || source.ID == "" || a.detail == nil {
		return
	}
	a.setParallel(id)
	a.refresh()
}

func (a *mobileApp) setParallel(id string) {
	if a.parallel != id {
		a.suspendParallelFind()
		if a.reviewBelongsToParallel(a.review) {
			a.replaceReview(nil)
		}
	}
	a.parallel = id
	if a.controller != nil {
		var visible []string
		if id != "" {
			visible = append(visible, id)
		}
		a.controller.Do(app.Job{Method: "$visible", Input: visible})
	}
}

func (a *mobileApp) parallelContent(c *ui.Context) {
	pane, id := a, a.parallel
	if a.parallelDevice != nil {
		pane = a.parallelDevice
		pane.model.Preferences = a.model.Preferences
		id = pane.model.SessionID
	}
	pane.drawParallelPane(c, id, func() {
		if err := a.closeParallelDevice(); err == nil {
			a.setParallel("")
		}
		a.refresh()
	})
}

func (a *mobileApp) drawParallelPane(c *ui.Context, id string, closePane func()) {
	s := a.sessionByID(id)
	p := palette(c)
	c = c.Key(a.model.WorkKey(id, "parallel"))
	var draft *mobileDraftEditor
	if s.ID != "" && !s.Archived && !a.workspace(s.WorkspaceID).Archived {
		target := a.parallelDraftTarget()
		if a.parallelOwner != nil {
			target = a.draftTarget()
		}
		draft = a.bindDraftEditor(c, target)
	}
	title := s.Title
	if s.ID == "" {
		title = "会话已不可用"
	}
	pane := ui.Column(c).Grow(1).Basis(0).MinWidth(0).HeightPercent(100).BorderStartWidth(1).BorderColor(p.line).
		Bind(&a.splitRoot().parallelFindHandle)
	pane.Children(func() {
		ui.Row(c).MinHeight(55).PaddingX(12).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				oneLine(c, title, 16, p.ink).FontWeight(650)
				scope := a.conversationWorkspaceScope(s)
				if a.parallelOwner != nil {
					scope = a.conversationScope(s)
				}
				oneLine(c, scope, 12, p.secondary)
			})
			if s.ID != "" {
				toolbarButton(c, inParallelFind, searchSymbol, func() { a.openConversationFind(a.detail, s, true) })
				toolbarButton(c, "右栏文件改动", filesSymbol, func() {
					reviewDraft := draft
					if a.model.ActiveQueueEdit(s.ID).ID != "" {
						reviewDraft = nil
					}
					a.openReview(a.detail, s, a.detail, reviewDraft)
				})
			}
			iconButton(c, "关闭并排会话", closeSymbol, 17, closePane)
		})
		if a.parallelOwner != nil {
			a.connectionNotice(c.Key("connection"), a.detail)
		}
		if s.ID == "" {
			return
		}
		detail := a.model.Details[s.ID]
		if read := a.model.HistoryReads[s.ID]; !detail.HasLoadedHistory() && read != nil {
			if read.Pending {
				oneLine(c, "正在读取历史…", captionTextSize, p.secondary).Padding(4, pageInset)
			} else if read.Error != "" {
				label(c, read.Error, captionTextSize, p.warning).Padding(4, pageInset)
			}
		}
		find := a.conversationFind(c, a.detail, s, detail, true, pane.Shortcut)
		a.conversationBody(c, a.detail, s, detail, find, draft, true)
	})
}

type mobileHistoryRequest struct {
	mobileRequest
	prepared, epoch string
	completedUntil  time.Time
	visual          *mobileHistoryVisual
	anchorY         float32
	hasAnchor       bool
}

func (r *mobileHistoryRequest) canContinue(id, epoch string) bool {
	return id != "" && r.prepared == id && r.epoch == epoch
}

type historyIntent uint8

const (
	historySubmit historyIntent = iota
	historyLookup
	historyContinue
)

// History edits bind to the original item and have a separate persisted draft.
func (a *mobileApp) historyEditor(c *ui.Context, parent *mygo.Window, s domain.Session, item domain.Item) bool {
	m := a.model
	edit := m.HistoryEdits[s.ID]
	if edit.ItemID != item.ID || !a.visibleLinkSource(parent, s) {
		return false
	}
	target := a.draftTarget()
	if target.session != s.ID && parent == a.detail {
		target = a.parallelDraftTarget()
	}
	if target.session != s.ID || target.workspace != s.WorkspaceID {
		return false
	}
	key := m.SessionHistoryDraftKey(s.ID)
	target.key, target.history = key, true
	editor := a.bindDraftEditor(c, target)
	req := a.historyRequests[m.WorkKey(s.ID, "")]
	busy := req != nil && req.busy
	p := palette(c)
	ui.Column(c.Key("user-surface")).WidthPercent(92).AlignSelf(ui.End).Padding(8).Gap(4).Background(p.inset).Radius(16).Transition(mobileHistoryBubbleMotion).Children(func() {
		ui.Column(c).Height(80).Padding(8).Children(func() {
			editor.field(a, c, parent, "编辑已发送消息", "", edit.OperationID != "", 0, nil)
		})
		editor.referencePanel(a, c, parent)
		if edit.OperationID == "" {
			ui.Row(c).Justify(ui.End).Gap(4).Children(func() {
				action(c, "取消", func() {
					delete(a.historyRequests, m.WorkKey(s.ID, ""))
					m.CancelSessionHistoryEdit(s.ID)
					a.save()
				}).KeepFocus().Disabled(busy)
				iconButton(c, "确认修改并重开", checkSymbol, 16, func() { a.submitHistoryEdit(parent, s, editor, historySubmit) }).
					KeepFocus().Disabled(busy || editor.pending() || !m.Connected || !m.HasDraftContent(key))
			})
		}
	})
	return true
}

func (a *mobileApp) submitHistoryEdit(parent *mygo.Window, s domain.Session, editor *mobileDraftEditor, intent historyIntent) {
	m := a.model
	edit := m.HistoryEdits[s.ID]
	if !m.Connected || a.controller == nil || !a.visibleLinkSource(parent, s) {
		return
	}
	req := a.historyRequest(m.WorkKey(s.ID, ""))
	if req.busy || req.visual != nil && !req.visual.sent {
		return
	}
	in := m.HistoryActions[s.ID]
	var prepare func() error
	method := "session.restart"
	var input any
	switch intent {
	case historyLookup:
		if in.OperationID == "" || !in.Restart {
			return
		}
		method = "session.history-operation"
		input = protocol.HistoryOperationQuery{ID: in.OperationID, SessionID: s.ID}
	case historyContinue:
		if a.stateWriter == nil || !in.Restart || !req.canContinue(in.OperationID, m.ConnectionEpoch) {
			return
		}
		req.prepared = ""
		input, prepare = in, a.stateWriter.FlushLatest
	case historySubmit:
		s = a.sessionByID(s.ID)
		if edit.ItemID == "" || a.stateWriter == nil || !a.historyActionAvailable(s) || !m.SessionReadiness(s).Capabilities.Restart ||
			in.OperationID != "" || !editor.current(a, parent) || editor.pending() || editor.Doc.Composing() {
			req.err = "当前会话无法修改这条历史消息"
			return
		}
		key := editor.target.key
		editor.Publish(m, key)
		if !m.HasDraftContent(key) {
			return
		}
		refs, err := m.SessionHistoryReferences(s)
		if err != nil {
			req.err = err.Error()
			return
		}
		req.completedUntil = time.Time{}
		in = domain.HistoryChange{OperationID: domain.NewID(), SessionID: s.ID, Revision: s.Revision, ItemID: edit.ItemID,
			Restart: true, Text: m.Drafts[key], Config: m.SessionConfig(s), References: refs}
		input, prepare = in, a.stateWriter.FlushLatest
	}
	dispatch := func() {
		if intent == historySubmit {
			edit.OperationID, edit.SubmittedText, edit.SubmittedReferences = in.OperationID, in.Text, in.References
			m.HistoryEdits[s.ID], m.HistoryActions[s.ID] = edit, in
			a.save()
		}
		clear := func() {
			req.visual = nil
			a.clearHistoryAction(in, req, true)
		}
		clearOnMissing := clear
		if intent == historyContinue {
			clearOnMissing = nil
		}
		a.historyPageJob(parent, &req.mobileRequest, method, input, prepare, clearOnMissing, decodePage(func(op domain.HistoryOperation) error {
			if !historyReceiptMatches(op, in, s, m.Snapshot.DeviceID) {
				return fmt.Errorf("历史修改回执与当前会话不一致")
			}
			if op.State == domain.HistoryFailed {
				clear()
				return fmt.Errorf("%s", op.Error)
			}
			if op.State == domain.HistoryForked {
				req.prepared, req.epoch = op.ID, m.ConnectionEpoch
			}
			if op.State == domain.HistoryComplete && m.SessionID == s.ID {
				a.controller.Select(s.ID)
			}
			return nil
		}))
	}
	if intent == historySubmit {
		a.animateHistoryExit(parent, s, req, in, dispatch)
	} else {
		dispatch()
	}
}

func historyReceiptMatches(op domain.HistoryOperation, in domain.HistoryChange, source domain.Session, device string) bool {
	if op.ID != in.OperationID || !op.Input.Equal(in) || op.Source.ID != source.ID || op.Source.Context.DeviceID != device ||
		op.Source.WorkspaceID != source.WorkspaceID || op.Session.WorkspaceID != source.WorkspaceID ||
		op.Session.Context.DeviceID != device || op.Session.ID == "" {
		return false
	}
	if in.Restart {
		return op.Session.ID == source.ID
	}
	return op.Session.ID != source.ID && op.Session.ForkedFrom == source.ID
}

func (a *mobileApp) clearHistoryAction(in domain.HistoryChange, req *mobileHistoryRequest, unlock bool) {
	m := a.model
	if m.HistoryActions[in.SessionID].OperationID != in.OperationID {
		return
	}
	delete(m.HistoryActions, in.SessionID)
	if edit := m.HistoryEdits[in.SessionID]; unlock && edit.OperationID == in.OperationID {
		edit.OperationID = ""
		m.HistoryEdits[in.SessionID] = edit
	}
	req.prepared = ""
	a.save()
}

func (a *mobileApp) cancelHistory(parent *mygo.Window, s domain.Session) {
	m := a.model
	in := m.HistoryActions[s.ID]
	if in.OperationID == "" || !m.Connected || a.controller == nil || !a.visibleLinkSource(parent, s) {
		return
	}
	req := a.historyRequest(m.WorkKey(s.ID, ""))
	input := protocol.HistoryOperationQuery{ID: in.OperationID, SessionID: s.ID}
	a.pageJob(parent, &req.mobileRequest, "session.cancel-history", input, decodePage(func(op domain.HistoryOperation) error {
		if !historyReceiptMatches(op, in, s, m.Snapshot.DeviceID) {
			return fmt.Errorf("历史操作回执与当前会话不一致")
		}
		if pending := m.HistoryActions[s.ID].OperationID; pending != "" && pending != in.OperationID {
			return nil
		}
		switch op.State {
		case domain.HistoryFailed:
			a.clearHistoryAction(in, req, true)
		case domain.HistoryComplete:
			// A committed restart is reconciled by the ordinary input receipt.
			a.clearHistoryAction(in, req, false)
			req.completedUntil = time.Now().Add(6 * time.Second)
			if in.Restart && m.SessionID == s.ID {
				a.controller.Select(s.ID)
			}
		default:
			return fmt.Errorf("还未确认取消，请核对操作回执")
		}
		a.refresh()
		return nil
	}))
}

func (a *mobileApp) historyPageJob(page *mygo.Window, req *mobileRequest, method string, input any,
	prepare func() error, clear func(), receipt func(json.RawMessage) error) {
	a.runPageJobResult(nil, page, req, method, input, prepare, func(data json.RawMessage, err error) error {
		if err == nil {
			return receipt(data)
		}
		var remote *protocol.RemoteError
		query := method == "session.history-operation"
		if clear != nil && errors.As(err, &remote) && (query && remote.Code == "notFound" || !query && remote.Code == "notSent") {
			clear()
		}
		return err
	})
}

func (a *mobileApp) historyActionAvailable(s domain.Session) bool {
	return a.model.Connected && s.ID != "" && s.ThreadID != "" && s.Status.CanSubmit() && !s.ReadOnlyHistory() &&
		!s.Archived && !a.workspace(s.WorkspaceID).Archived && !s.ContextChanged && s.ExecutionGuard == "" &&
		!a.model.HasPendingInputForSession(s.ID)
}

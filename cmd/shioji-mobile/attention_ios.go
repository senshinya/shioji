package main

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type attentionTarget struct {
	device, workspace, session, thread string
	request, work, completion          string
	revision                           int64
}

type attentionEntries struct {
	requests []mobileclient.DeviceRequest
	replies  []mobileclient.DeviceReply
}

func requestAttentionTarget(entry mobileclient.DeviceRequest) attentionTarget {
	return attentionTarget{device: entry.DeviceID, workspace: entry.WorkspaceID, session: entry.Request.SessionID,
		thread: entry.ThreadID, request: entry.Request.ID, work: entry.Request.WorkID, revision: entry.Request.Revision}
}

func replyAttentionTarget(entry mobileclient.DeviceReply) attentionTarget {
	return attentionTarget{device: entry.DeviceID, workspace: entry.Session.WorkspaceID, session: entry.Session.ID,
		thread: entry.Session.ThreadID, completion: entry.CompletionID}
}

func (a *mobileApp) currentAttentionEntries() attentionEntries {
	address := a.deviceLabel(a.endpoint.DeviceID, a.activeEndpoint().Address)
	if a.preview {
		address = a.deviceName()
	}
	catalog := a.model.Snapshot
	return attentionEntries{
		requests: mobileclient.DeviceRequests(catalog, address),
		replies: mobileclient.DeviceReplies(catalog, address, func(s domain.Session) bool {
			return (a.preview || a.model.WorkerMinor > 0) && a.model.HasCompletedReply(s.ID)
		}),
	}
}

func (a *mobileApp) remoteAttentionEntries(catalog mobileclient.DeviceCatalog) attentionEntries {
	cutoff := a.model.CompletionCutoffForSnapshot(catalog.Snapshot, catalog.RequestedAt)
	return attentionEntries{
		requests: mobileclient.DeviceRequests(catalog.Snapshot, catalog.Label()),
		replies: mobileclient.DeviceReplies(catalog.Snapshot, catalog.Label(), func(s domain.Session) bool {
			return mobileclient.UnreadReplySince(s, cutoff)
		}),
	}
}

func (a *mobileApp) openAttentionTarget(parent *mygo.Window, target attentionTarget) string {
	session := a.sessionByID(target.session)
	if !a.model.Connected || a.model.Snapshot.DeviceID != target.device ||
		session.ID == "" || session.WorkspaceID != target.workspace || session.ThreadID != target.thread ||
		session.Context.DeviceID != "" && session.Context.DeviceID != target.device {
		return "会话已改变，请重试"
	}
	if target.request == "" {
		if !a.model.HasCompletedReply(session.ID) ||
			target.completion != "" && (session.Completion == nil || session.Completion.ID != target.completion || !session.Completion.Unread) {
			return "回复已处理或更新"
		}
		a.openAttentionReply(parent, session.ID)
		return ""
	}
	for _, record := range a.model.Snapshot.PendingRequests {
		if record.ID == target.request && record.SessionID == target.session && record.WorkID == target.work &&
			record.Revision == target.revision && pendingRequest(record) {
			if a.wide && parent == a.home && target.work != "" {
				a.selectSession(session.ID)
				parent = a.detail
			}
			a.showAttentionRequest(parent, record)
			return ""
		}
	}
	return "请求已处理或失效"
}

func (a *mobileApp) openAttention(parent *mygo.Window) {
	if a.wide && parent == a.home {
		parent = a.detail
	}
	var w *mygo.Window
	var chosen *attentionTarget
	var deadline time.Time
	var stop context.CancelFunc
	pending, failed := 0, 0
	notice := ""
	remotes := map[string]attentionEntries{}
	readOthers := func() {
		if a.preview || pending > 0 || chosen != nil {
			return
		}
		notice = ""
		var devices []savedMobileDevice
		for _, device := range a.savedDevices.Devices {
			if device.Endpoint.DeviceID != a.endpoint.DeviceID {
				devices = append(devices, device)
			}
		}
		if len(devices) == 0 {
			return
		}
		defer w.Invalidate()
		signer, err := a.identity()
		if err != nil {
			notice = "无法读取设备连接身份"
			return
		}
		if stop != nil {
			stop()
		}
		ctx, cancel := context.WithCancel(context.Background())
		stop = cancel
		route := a.routeDial()
		pending, failed = len(devices), 0
		for _, device := range devices {
			go func() {
				read, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				catalog, err := mobileclient.ReadDeviceCatalog(read, device, signer, route)
				mygo.RunOnMain(func() {
					if ctx.Err() != nil || !a.pageCurrent(w) {
						return
					}
					pending--
					id := device.Endpoint.DeviceID
					if err != nil {
						failed++
						delete(remotes, id)
					} else {
						remotes[id] = a.remoteAttentionEntries(catalog)
					}
					w.Invalidate()
				})
			}()
		}
	}
	focus := func(target attentionTarget) {
		defer w.Invalidate()
		notice = ""
		if !a.preview && target.device != a.endpoint.DeviceID {
			remotes[a.model.Snapshot.DeviceID] = a.currentAttentionEntries()
			if err := a.openDeviceFromResults(w, target.device); err != nil {
				notice = err.Error()
				return
			}
		}
		chosen, deadline = &target, time.Now().Add(15*time.Second)
	}
	w = a.page("待我处理", func(c *ui.Context) {
		if chosen != nil {
			target := *chosen
			switch {
			case a.model.Connected && a.model.Snapshot.DeviceID == target.device:
				chosen = nil
				notice = a.openAttentionTarget(w, target)
			case !c.Now().Before(deadline):
				chosen, notice = nil, "无法连接会话所属的设备"
			default:
				c.After(deadline.Sub(c.Now()))
			}
		}
		current := a.model.Snapshot.DeviceID
		items := a.currentAttentionEntries()
		for _, device := range a.savedDevices.Devices {
			if id := device.Endpoint.DeviceID; id != current && id != a.endpoint.DeviceID {
				items.requests = append(items.requests, remotes[id].requests...)
				items.replies = append(items.replies, remotes[id].replies...)
			}
		}
		mobilePageBody(c, func() {
			p := palette(c)
			mobileSection(c, "需要行动")
			status := notice
			switch {
			case chosen != nil:
				status = "正在连接会话所属的设备…"
			case status == "" && pending > 0:
				status = "正在检查其他设备…"
			case status == "" && (failed > 0 || !a.model.Connected && a.endpoint.DeviceID != ""):
				status = "部分设备暂不可用，列表可能不完整"
			}
			if status != "" {
				label(c.Key("status"), status, captionTextSize, p.secondary).Role(ui.RoleStatus).Padding(2, 0, 6)
			}
			for _, entry := range items.requests {
				request := entry.Request
				title := requestName(request)
				state := "待审批"
				if len(request.Questions) != 0 || len(request.Fields) != 0 {
					state = "待答问题"
				}
				scope := strings.Join(slices.DeleteFunc([]string{entry.SessionTitle, entry.Workspace, entry.Address}, func(part string) bool {
					return part == "" || part == title
				}), " · ")
				row := c.Key([4]string{entry.DeviceID, request.SessionID, request.WorkID, request.ID})
				mobileDetailRow(row, title, scope, title, infoSymbol,
					func() {
						focus(requestAttentionTarget(entry))
					}, func() {
						oneLine(row, state, captionTextSize, p.warning)
					}).Value(scope).Disabled(chosen != nil)
			}
			found := len(items.requests)+len(items.replies) != 0
			for id, pending := range a.model.Pending {
				session := a.sessionByID(pending.SessionID)
				if session.ID != "" {
					found = true
					var open func()
					if chosen == nil {
						open = func() { a.openSessionFrom(w, session.ID) }
					}
					mobileRow(c.Key(id), session.Title, a.workspace(session.WorkspaceID).Name+" · "+a.deviceName(),
						"待核对", clockSymbol, open)
				}
			}
			for _, reply := range items.replies {
				session := reply.Session
				scope := strings.Trim(reply.Workspace+" · "+reply.Address, " ·")
				row := c.Key([3]string{"completion", reply.DeviceID, session.ID})
				mobileDetailRow(row, session.Title, scope, "查看未读回复："+session.Title, checkSymbol, func() {
					focus(replyAttentionTarget(reply))
				}, func() { oneLine(row, "未读回复", captionTextSize, p.secondary) }).Value(scope).Disabled(chosen != nil)
			}
			if !found && status == "" {
				mobileMessage(c, "当前没有待处理事项")
			}
		})
	})
	w.OnClosed(func() {
		if stop != nil {
			stop()
		}
	})
	if len(a.savedDevices.Devices) > 1 && !a.preview {
		mygo.SetBarItems(w, mygo.BarItem{Title: "刷新待处理请求", Symbol: "arrow.clockwise", OnClick: readOthers})
	}
	readOthers()
	a.pushNative(parent, w)
}

func (a *mobileApp) openAttentionReply(w *mygo.Window, session string) {
	delete(a.model.HistoryAnchors, session)
	a.openSessionFrom(w, session)
	detail := a.model.Details[session]
	if detail.History != nil && detail.History.Newer {
		a.readTranscriptHistory(session, detail, domain.HistoryQuery{})
	} else if state := a.history[session]; state != nil {
		state.ScrollToEnd()
	}
}

func (a *mobileApp) showAttentionRequest(w *mygo.Window, request domain.PendingRequest) {
	a.focusRequest(request)
	if request.WorkID == "" {
		a.openSessionFrom(w, request.SessionID)
		return
	}
	for _, work := range domain.SessionWorks(a.model.Details[request.SessionID]) {
		if work.ID == request.WorkID {
			a.openWork(w, request.SessionID, work)
			return
		}
	}
	a.openWork(w, request.SessionID, domain.WorkActivity{ID: request.WorkID, Kind: "agent"})
}

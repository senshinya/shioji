package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type archiveConversationPreview struct {
	window  *mygo.Window
	cancel  context.CancelFunc
	request string
}

type archiveTranscript struct {
	list        ui.ListState
	timeline    mobileToolTimeline
	compaction  mobileCompaction
	fillEarlier func()
	fitHeight   func(float32)
}

func (p *archiveTranscript) fitTail(c *ui.Context, row ui.Element) {
	zoom := c.Zoom()
	row.DrawOver(func(_ *ui.Painter, r ui.Rect) {
		before, _ := p.list.ScrollEdges()
		if before < .5 && p.list.AtEnd() {
			p.fitHeight((r.Y + r.H) * zoom)
		}
	})
}

func (p *archiveConversationPreview) close() {
	if p != nil {
		p.cancel()
		p.window.Close()
	}
}

func (a *mobileApp) startArchiveConversationPreview(owner *mygo.Window, location app.SessionLocation, session domain.Session, request string) *archiveConversationPreview {
	m := a.model
	ctx, cancel := context.WithCancel(context.Background())
	preview := &archiveConversationPreview{cancel: cancel, request: request}
	transcript := archiveTranscript{
		list:       ui.ListState{FollowEnd: true},
		timeline:   mobileToolTimeline{thread: session.ThreadID, bodies: make(map[string]*bool)},
		compaction: mobileCompaction{thread: session.ThreadID},
	}
	history := app.NewModel()
	status := "正在读取会话…"
	local := location.DeviceID != "" && location.DeviceID == m.Snapshot.DeviceID
	renderer := a
	device, signer, route := a.savedDevice(location.DeviceID), a.signer, a.routeDial()
	catalog := a.sessionCatalog(location.DeviceID)
	epoch, generation := m.ConnectionEpoch, catalog.Snapshot.Generation
	remote := !local && device.Endpoint.DeviceID == location.DeviceID && signer != nil
	if remote {
		directory, _ := a.deviceDirectory(device)
		renderer = &mobileApp{model: history, directory: directory, windows: make(map[*mygo.Window]bool),
			previewResources: &previewImageResources{context: ctx, dial: func(ctx context.Context) (net.Conn, error) {
				last := errors.New("没有已验证的连接路径")
				for _, endpoint := range device.Endpoints() {
					if endpoint.DeviceID != location.DeviceID {
						continue
					}
					dial, cancel := context.WithTimeout(ctx, 5*time.Second)
					stream, err := endpoint.DialRoute(dial, signer, route)
					cancel()
					if err == nil {
						return stream, nil
					}
					last = err
				}
				return nil, last
			}}}
		history.Preferences = m.Preferences
		history.Snapshot.DeviceID = location.DeviceID
	}
	var pending, failed bool
	canRead := func() bool {
		if failed || a.preview || a.model != m || ctx.Err() != nil || !a.pageCurrent(owner) {
			return false
		}
		if remote {
			return a.savedDevice(location.DeviceID).Endpoint.DeviceID == location.DeviceID
		}
		return local && m.Connected && m.ConnectionEpoch == epoch && m.Snapshot.Generation == generation
	}
	var lastHeight float32
	transcript.fitHeight = func(height float32) {
		h := history.Details[session.ID].History
		if pending || ctx.Err() != nil || h != nil && h.Older && h.First > 0 && canRead() {
			return
		}
		if height >= lastHeight-1 && height <= lastHeight+1 {
			return
		}
		lastHeight = height
		a.callUIKit("fitContextPreview", map[string]any{
			"window": owner.NativeHandle(), "request": request, "height": height,
		}, nil)
	}
	preview.window = a.page(session.Title, func(c *ui.Context) {
		p := palette(c)
		zoom := c.Zoom()
		ui.Column(c).Fill().Background(p.canvas).Children(func() {
			label(c.Key("title"), session.Title, bodyTextSize, p.ink).FontWeight(640).MaxLines(2).Padding(14, pageInset, 8).
				Role(ui.RoleHeading)
			if detail, loaded := history.Details[session.ID]; loaded {
				renderer.renderTranscript(c, preview.window, session, detail, false, nil, &transcript, nil, ui.Element{})
			} else {
				label(c.Key("status"), status, controlTextSize, p.secondary).Padding(4, pageInset, transcriptItemGap).Role(ui.RoleStatus).
					DrawOver(func(_ *ui.Painter, r ui.Rect) { transcript.fitHeight((r.Y + r.H) * zoom) })
			}
		})
	})
	if remote {
		renderer.windows[preview.window] = true
		preview.window.OnClosed(func() { delete(renderer.windows, preview.window) })
	}
	a.callUIKit("mountContextPreview", map[string]any{
		"window": owner.NativeHandle(), "preview": preview.window.NativeHandle(), "request": request,
	}, nil)

	belongs := func(detail domain.SessionDetail) bool {
		return detail.Session.ID == session.ID && detail.Session.WorkspaceID == session.WorkspaceID &&
			detail.Session.ThreadID == session.ThreadID &&
			(detail.Session.Context.DeviceID == "" || detail.Session.Context.DeviceID == location.DeviceID)
	}
	read := func(before int64) {
		if pending || !canRead() {
			return
		}
		pending = true
		query := domain.HistoryQuery{SessionID: session.ID, ThreadID: session.ThreadID, Generation: generation,
			Before: before, Rounds: m.Preferences.Normalized().HistoryPageRounds}
		apply := func(page domain.SessionDetail, err error) {
			if !canRead() {
				return
			}
			pending = false
			failed = err != nil || !belongs(page) || page.History == nil || before > 0 && page.History.First >= before
			if !failed {
				failed = history.MergeHistoryPage(page, history.BeginHistoryRead(session.ID), before == 0, query) != nil
			}
			if failed {
				status = "无法读取记录，轻点可打开会话"
			} else {
				history.Connected = true
				transcript.list.ScrollToEnd()
			}
			preview.window.Invalidate()
		}
		if remote {
			go func() {
				read, cancel := context.WithTimeout(ctx, sessionCatalogTimeout)
				defer cancel()
				page, err := mobileclient.ReadDeviceSessionHistory(read, device, signer, route, location, query)
				mygo.RunOnMain(func() { apply(page, err) })
			}()
			return
		}
		if a.controller == nil || !a.controller.Do(app.Job{Context: ctx, Method: "session.history", Input: query, ConnectionEpoch: epoch,
			Apply: func(current *app.Model, data json.RawMessage, err error) {
				if current != m {
					return
				}
				var page domain.SessionDetail
				if err == nil {
					err = json.Unmarshal(data, &page)
				}
				apply(page, err)
			},
		}) {
			apply(domain.SessionDetail{}, errors.New("连接执行设备后读取会话"))
		}
	}
	transcript.fillEarlier = func() {
		if h := history.Details[session.ID].History; h != nil && h.Older && h.First > 0 {
			read(h.First)
		}
	}
	detail := m.Details[session.ID]
	switch {
	case local && belongs(detail) && detail.HasLoadedHistory() &&
		(detail.History == nil || !detail.History.Newer && !detail.History.Invalidated):
		history.Details[session.ID] = detail
		transcript.list.ScrollToEnd()
		preview.window.Invalidate()
	case !canRead():
		status = "连接设备后可预览会话记录"
		preview.window.Invalidate()
	default:
		read(0)
	}
	return preview
}

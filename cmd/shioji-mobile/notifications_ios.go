package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"

	"shioji/internal/app"
	"shioji/internal/domain"
)

const retainedNotifications = 64

// Keep device and conversation identifiers in the app sandbox. The system
// notification identifier is an opaque key, also usable after a relaunch.
type mobileNotificationRoute struct {
	Location app.SessionLocation `json:"location"`
	ID       string              `json:"id"`
	Thread   string              `json:"thread,omitempty"`
	Request  string              `json:"request,omitempty"`
	Work     string              `json:"work,omitempty"`
	Revision int64               `json:"revision,omitempty"`
}

type mobileNotifications struct {
	routes   []mobileNotificationRoute
	inFlight map[string]context.CancelFunc
	toOpen   string
	opening  uint64
}

func (a *mobileApp) loadNotificationRoutes() {
	data, err := os.ReadFile(filepath.Join(a.rootDirectory, "notification-routes.json"))
	if err != nil {
		return
	}
	var routes []mobileNotificationRoute
	if json.Unmarshal(data, &routes) == nil {
		a.notifications.routes = slices.Clone(routes[max(0, len(routes)-retainedNotifications):])
	}
}

func (a *mobileApp) deliverNotifications() {
	events := a.model.Notifications
	a.model.Notifications = nil
	prefs := a.model.Preferences.Notifications
	if len(events) == 0 || a.preview || !prefs.Enabled || !prefs.Foreground && mygo.App.IsActive() {
		return
	}
	root := a
	if a.parallelOwner != nil {
		root = a.parallelOwner
	}
	state := &root.notifications
	var notices []*mygo.Notification
	for _, event := range events {
		location := event.SessionLocation
		id := fmt.Sprintf("shioji-%x", sha256.Sum256([]byte(a.model.Snapshot.DeviceID+"\x00"+event.ID)))
		if slices.ContainsFunc(state.routes, func(route mobileNotificationRoute) bool { return route.ID == id }) {
			continue
		}
		group := ""
		if location.SessionID != "" {
			group = fmt.Sprintf("%x", sha256.Sum256([]byte(location.DeviceID+"\x00"+location.SessionID)))
		}
		state.routes = append(state.routes, mobileNotificationRoute{
			ID: id, Location: location, Thread: event.ThreadID, Request: event.RequestID,
			Work: event.WorkID, Revision: event.RequestRevision,
		})
		notices = append(notices, mygo.NewNotification(mygo.NotificationOptions{
			ID: id, Title: event.Title, Body: event.Body, Group: group, Silent: !prefs.Sound,
		}))
	}
	if len(notices) == 0 {
		return
	}
	pruned := max(0, len(state.routes)-retainedNotifications)
	for _, route := range state.routes[:pruned] {
		state.close(route.ID)
	}
	if pruned != 0 {
		state.routes = slices.Clone(state.routes[pruned:])
	}
	if root.rootDirectory != "" {
		data, err := json.Marshal(state.routes)
		if err == nil {
			err = app.WriteState(filepath.Join(root.rootDirectory, "notification-routes.json"), data)
		}
		if err != nil {
			log.Printf("save notification routes: %v", err)
		}
	}
	if state.inFlight == nil {
		state.inFlight = make(map[string]context.CancelFunc)
	}
	for _, notice := range notices[max(0, len(notices)-retainedNotifications):] {
		ctx, cancel := context.WithCancel(context.Background())
		state.inFlight[notice.ID()] = cancel
		go func() {
			_ = notice.ShowContext(ctx)
			mygo.RunOnMain(func() {
				if ctx.Err() == nil {
					delete(state.inFlight, notice.ID())
					cancel()
				}
			})
		}()
	}
}

func (n *mobileNotifications) close(id string) {
	if cancel := n.inFlight[id]; cancel != nil {
		cancel()
		delete(n.inFlight, id)
	}
	mygo.NewNotification(mygo.NotificationOptions{ID: id}).Close()
}

func (a *mobileApp) openQueuedNotification() {
	n := &a.notifications
	if n.toOpen == "" || !a.uiConfigured || isTablet() && !a.widthReported {
		return
	}
	id := n.toOpen
	n.toOpen = ""
	index := slices.IndexFunc(n.routes, func(route mobileNotificationRoute) bool { return route.ID == id })
	if index < 0 {
		return
	}
	route := n.routes[index]
	n.close(id)
	ref := route.Location
	if ref.DeviceID == "" || ref.WorkspaceID == "" || ref.SessionID == "" {
		return
	}
	n.opening++
	opening := n.opening
	open := func() {
		if n.opening != opening {
			return
		}
		a.callUIKit("selectTab", map[string]int{"index": 0}, func(err error) {
			if err != nil || n.opening != opening {
				return
			}
			roots := []uintptr{a.home.NativeHandle()}
			if a.detail != nil {
				roots = append(roots, a.detail.NativeHandle())
			}
			a.callUIKit("resetDeviceNavigation", map[string][]uintptr{"roots": roots}, func(err error) {
				if err == nil && n.opening == opening {
					a.openSessionListTarget(sessionListTarget{location: ref, notification: &route})
				}
			})
		})
	}
	if draft := a.draftPage; draft != nil {
		draft.OnClosed(open)
		if isTablet() {
			a.callUIKit("dismissForm", map[string]uintptr{"window": draft.NativeHandle()}, nil)
		} else {
			draft.Pop()
		}
		return
	}
	open()
}

func (a *mobileApp) presentNotificationTarget(route mobileNotificationRoute, session domain.Session, archived bool) {
	if route.Thread != "" && session.ThreadID != route.Thread {
		a.sessionListNotice = "会话历史已改变"
		return
	}
	if route.Request == "" {
		a.openSession(session.ID)
		return
	}
	for _, request := range a.model.Snapshot.PendingRequests {
		if !archived && request.ID == route.Request && request.SessionID == session.ID &&
			request.WorkID == route.Work && request.Revision == route.Revision && pendingRequest(request) {
			parent := a.home
			if a.wide && request.WorkID != "" {
				a.selectSession(session.ID)
				parent = a.detail
			}
			a.showAttentionRequest(parent, request)
			return
		}
	}
	a.sessionListNotice = "请求已处理或失效"
}

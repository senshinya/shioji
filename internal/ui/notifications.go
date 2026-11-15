package ui

import (
	"context"

	"github.com/egoist/mygo"
)

type nativeNotificationDelivery struct {
	note   *mygo.Notification
	cancel context.CancelFunc
}

// AttachWindow pumps worker updates on the application thread even while the
// window is minimized. Native notifications do not depend on a rendered frame.
func (v *View) AttachWindow(window *mygo.Window, settings *Settings) {
	v.nativeWindow = window
	v.settings = settings
	v.notificationContext, v.cancelNotifications = context.WithCancel(context.Background())
	window.OnClosed(func() { v.cancelNotifications() })
	if v.Controller != nil {
		v.Controller.SetInvalidate(func() {
			window.Update(func() {
				if !window.IsDestroyed() {
					v.pumpUpdates()
				}
			})
		})
	}
}

func (v *View) pumpUpdates() {
	updated := false
	if v.Controller != nil {
		for {
			select {
			case update := <-v.Controller.Updates:
				update(v.Model)
				updated = true
			default:
				goto drained
			}
		}
	}
drained:
	v.syncArchivePolicies()
	if updated && v.settings != nil {
		v.settings.services.Invalidate()
	}
	if v.nativeWindow != nil && !v.nativeWindow.IsDestroyed() {
		v.deliverNotifications()
	}
	if updated && v.Saver != nil {
		v.queueState()
	}
}

func (v *View) foregroundNotificationSuppressed(id string, foreground bool) bool {
	if p := v.Model.Preferences.Notifications; p != nil && p.Foreground {
		return false
	}
	if !foreground || v.devices || v.workspaceEditor != nil || v.Model.SessionID == "" || v.archive != nil && v.archive.open {
		return false
	}
	m := v.Model
	wide := v.viewportWidth >= 1180
	return id == m.SessionID && (wide || !m.FocusSecondary || m.SecondarySessionID == "") || id == m.SecondarySessionID && (wide || m.FocusSecondary)
}

func (v *View) deliverNotifications() {
	queue := v.Model.Notifications
	v.Model.Notifications = nil
	options := v.Model.Preferences.Notifications
	if options == nil || !options.Enabled {
		return
	}
	foreground := v.nativeWindow.IsFocused() && v.nativeWindow.IsVisible() && !v.nativeWindow.IsMinimized()
	for _, event := range queue {
		if v.foregroundNotificationSuppressed(event.SessionID, foreground) {
			continue
		}
		if event.Kind == "completed" && !options.Completed || event.Kind == "attention" && !options.Attention || event.Kind == "failed" && !options.Failed {
			continue
		}
		if !mygo.NotificationsSupported() {
			continue
		}
		id := "shioji:" + event.ConnectionID + ":" + event.SessionID + ":" + event.Kind
		for i, previous := range v.notificationDeliveries {
			if previous.note.ID() == id {
				previous.cancel()
				previous.note.Close()
				v.notificationDeliveries = append(v.notificationDeliveries[:i], v.notificationDeliveries[i+1:]...)
				break
			}
		}
		note := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: event.Title, Body: event.Body, Group: event.ConnectionID + ":" + event.SessionID, Silent: !options.Sound})
		deliveryContext, cancel := context.WithCancel(v.notificationContext)
		v.notificationDeliveries = append(v.notificationDeliveries, nativeNotificationDelivery{note: note, cancel: cancel})
		if len(v.notificationDeliveries) > 64 {
			old := v.notificationDeliveries[0]
			old.cancel()
			old.note.Close()
			v.notificationDeliveries = append([]nativeNotificationDelivery(nil), v.notificationDeliveries[1:]...)
		}
		note.OnClick(func() {
			if v.nativeWindow.IsDestroyed() {
				return
			}
			for _, session := range v.Model.Snapshot.Sessions {
				if session.ID == event.SessionID {
					v.navigateSession(session.ID, session.WorkspaceID)
					if event.Kind == "attention" {
						v.Model.Collapsed[session.ID] = false
						v.collapsedWorkspaces[session.WorkspaceID] = false
					}
					break
				}
			}
			v.nativeWindow.Show()
			v.nativeWindow.Focus()
			v.nativeWindow.Invalidate()
			note.Close()
		})
		go func() {
			if err := note.ShowContext(deliveryContext); err != nil && deliveryContext.Err() == nil && v.settings != nil {
				v.nativeWindow.Update(func() {
					if !v.nativeWindow.IsDestroyed() {
						v.settings.reportNotificationError(err)
					}
				})
			}
		}()
	}
}

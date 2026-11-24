package ui

import (
	"errors"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
)

func (s *Settings) testNotification(c *n.Context, enable bool) {
	if s.window == nil || !mygo.NotificationsSupported() {
		s.error = "请启动 Shioji.app 后开启系统通知"
		return
	}
	if s.notificationPending {
		return
	}
	s.notificationPending = true
	s.notificationMessage = "等待 macOS 通知授权…"
	window := s.window
	silent := !s.owner.Model.Preferences.Notifications.Sound
	go func() {
		err := mygo.NewNotification(mygo.NotificationOptions{ID: "shioji-settings-test", Title: "Shioji", Body: "通知已准备就绪", Silent: silent}).Show()
		window.Update(func() { s.finishNotificationTest(enable, err) })
	}()
}

func (s *Settings) finishNotificationTest(enable bool, err error) {
	s.notificationPending = false
	if err != nil {
		s.notificationMessage = ""
		s.reportNotificationError(err)
		return
	}
	if enable {
		s.owner.Model.Preferences.Notifications.Enabled = true
		s.changed()
	}
	s.notificationMessage = "测试通知已发送"
	s.services.Invalidate()
}

func (s *Settings) reportNotificationError(err error) {
	if errors.Is(err, mygo.ErrNotificationsDenied) {
		s.error = "macOS 未允许通知，请在系统通知设置中允许 Shioji"
	} else {
		s.error = "无法发送通知：" + err.Error()
	}
	if s.window != nil {
		s.window.Invalidate()
	}
}

func (s *Settings) notifications(c *n.Context, t *n.Theme) {
	p := s.owner.Model.Preferences.Notifications
	settingsGroup(c, t, func() {
		s.row(c, "系统通知", func() {
			enabled := p.Enabled || s.notificationPending
			if configurationSwitch(c, &enabled).Label("启用系统通知").Disabled(s.notificationPending).Changed() {
				if enabled {
					s.testNotification(c, true)
				} else {
					p.Enabled = false
					s.changed()
				}
			}
		})
	})
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsHeading(c, t, "提醒时机")
		settingsGroup(c, t, func() {
			for _, item := range []struct {
				label string
				value *bool
			}{{"回复完成", &p.Completed}, {"需要回答或授权", &p.Attention}, {"执行失败", &p.Failed}} {
				s.row(c, item.label, func() {
					if configurationSwitch(c, item.value).Disabled(!p.Enabled).Label("通知：" + item.label).Changed() {
						s.changed()
					}
				})
			}
		})
	})
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsHeading(c, t, "显示与声音")
		settingsGroup(c, t, func() {
			s.row(c, "前台也通知", func() {
				if configurationSwitch(c, &p.Foreground).Label("通知：前台也通知").Tooltip("包含正在前台查看的会话").Changed() {
					s.changed()
				}
			})
			s.row(c, "提示音", func() {
				if configurationSwitch(c, &p.Sound).Disabled(!p.Enabled).Label("通知提示音").Changed() {
					s.changed()
				}
			})
		})
	})
	n.Row(c).FillWidth().Gap(4).Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		if configurationAction(c, "发送测试通知").MarginStart(-8).Disabled(!p.Enabled || s.notificationPending).Clicked() {
			s.testNotification(c, false)
		}
		if configurationAction(c, "系统通知设置…").Clicked() {
			if err := mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.notifications"); err != nil {
				s.error = err.Error()
			}
		}
	})
	if s.notificationMessage != "" {
		n.Text(c, s.notificationMessage).FontSize(11).TextColor(t.TextMuted).Margin(8, 0, 0, 0)
	}
}

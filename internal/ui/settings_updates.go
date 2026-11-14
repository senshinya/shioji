package ui

import (
	n "github.com/egoist/mygo/ui"
)

// AppUpdateControls keeps updater implementation and preference ownership out
// of presentation and the workspace/worker model.
type AppUpdateControls struct {
	Enabled            func() bool
	Check              func()
	AutomaticChecks    func() bool
	SetAutomaticChecks func(bool) error
}

func (s *Settings) Invalidate() {
	s.services.Invalidate()
	if s.window != nil {
		s.window.Invalidate()
	}
}

func (s *Settings) appUpdateSettings(c *n.Context, t *n.Theme) {
	u := s.AppUpdates
	if u == nil {
		return
	}
	n.Column(c.Key("app-updates")).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsHeading(c, t, "App 更新")
		button := configurationRowControl(c, 6).Height(configurationRowHeight).Label("检查 App 更新").Children(func() { settingsLinkContent(c, t, "检查更新", "检查") })
		n.Column(c).FillWidth().Height(.5).Background(t.Border)
		if button.Clicked() {
			u.Check()
		}
		settingsRow(c, t, "自动检查更新", func() {
			enabled := u.Enabled()
			on := enabled && u.AutomaticChecks()
			if configurationSwitch(c, &on).Label("自动检查 App 更新").Disabled(!enabled).Changed() {
				s.error = ""
				if err := u.SetAutomaticChecks(on); err != nil {
					s.error = "无法保存更新设置：" + err.Error()
				}
			}
		})
	})
}

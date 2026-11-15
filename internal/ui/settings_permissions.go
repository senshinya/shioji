package ui

import (
	"fmt"

	n "github.com/egoist/mygo/ui"
)

const fullDiskAccessSettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"

func (s *Settings) refreshSystemPermissions() {
	s.fullDiskAccessGranted = s.FullDiskAccessGranted != nil && s.FullDiskAccessGranted()
}

func (s *Settings) fullDiskAccessSetting(c *n.Context, t *n.Theme) {
	settingsHeading(c, t, "系统权限")
	if s.fullDiskAccessGranted {
		settingsRow(c, t, "完全磁盘访问权限", func() {
			n.Text(c, "已授予").FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted)
		})
	} else {
		s.fullDiskAccessAction(c, t, "授予完全磁盘访问权限", false)
	}
	if s.BackgroundRuntimeAvailable != nil && s.BackgroundRuntimeAvailable() {
		s.fullDiskAccessAction(c, t, "为 Shioji Runtime 授权", true)
	}
	n.Text(c, "本机 App 与后台服务分别授权；远程设备请在对应 Mac 设置。").FillWidth().FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted).Margin(8, 0, 0, 0)
}

func (s *Settings) fullDiskAccessAction(c *n.Context, t *n.Theme, title string, background bool) {
	button := configurationRowControl(c, 6).Height(configurationRowHeight).Label(title).Children(func() {
		settingsLinkContent(c, t, title, "打开系统设置")
	})
	n.Column(c).FillWidth().Height(.5).Background(t.Border)
	if !button.Clicked() {
		return
	}
	s.error = ""
	result := func(err error) {
		if err != nil {
			s.error = fmt.Sprintf("无法打开完全磁盘访问权限设置：%v", err)
		}
	}
	if s.AuthorizeFullDiskAccess != nil {
		result(s.AuthorizeFullDiskAccess(background))
	} else {
		c.OpenURLThen(fullDiskAccessSettingsURL, result)
	}
}

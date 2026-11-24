package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	settingsAppearance = "输入与外观"
	settingsNotices    = "通知"
	settingsArchive    = "已归档"
	settingsAbout      = "关于"
)

type mobileSetting struct{ group, key, title string }

var mobileSettings = []mobileSetting{
	{settingsAppearance, "theme", "主题"},
	{settingsAppearance, "interface-zoom", "界面缩放"},
	{settingsAppearance, "interface-font", "界面字体"},
	{settingsAppearance, "reply-size", "会话字号"},
	{settingsAppearance, "code-font", "代码字体"},
	{settingsAppearance, "motion", "减少动态效果"},
	{settingsAppearance, "appearance-preview", "对话预览"},
	{settingsAppearance, "history-rounds", "每次加载轮次"},
	{settingsAppearance, "tools", "自动展开最新工具调用"},
	{settingsAppearance, "send", "发送或加入队列"},
	{settingsAppearance, "newline", "换行"},
	{settingsAppearance, "steer", "立即介入当前轮"},
	{settingsAppearance, "restore-shortcuts", "恢复默认按键"},
	{settingsShortcuts, "shortcuts", settingsShortcuts},
	{settingsNotices, "allow", "允许通知"},
	{settingsNotices, "foreground", "前台也通知"},
	{settingsNotices, "sound", "提示音"},
	{settingsNotices, "completed", "回复完成"},
	{settingsNotices, "attention", "需要回答或授权"},
	{settingsNotices, "failed", "执行失败"},
	{settingsNotices, "test-notice", "发送测试通知"},
	{settingsNotices, "system-settings", "打开系统通知设置"},
	{settingsArchive, "retention", "归档保留期"},
	{settingsAbout, "version", "版本"},
	{settingsAbout, "licenses", "第三方许可"},
}

func (a *mobileApp) settingsResults(c *ui.Context, query []string, version string) {
	found := false
	result := func(setting mobileSetting, value string) {
		text := strings.ToLower(setting.title + " " + setting.group + " " + value)
		if setting.key == "reply-size" {
			text += " 回复字号"
		}
		for _, word := range query {
			if !strings.Contains(text, word) {
				return
			}
		}
		found = true
		row := c.Key(setting.key)
		if setting.key == "version" {
			mobileRow(row, setting.title, setting.group, version, infoSymbol, nil)
			return
		}
		icon := infoSymbol
		switch setting.group {
		case settingsAppearance:
			icon = appearanceSymbol
		case settingsShortcuts:
			icon = keyboardSymbol
		case settingsArchive, settingsAbout:
			icon = filesSymbol
		}
		mobileRow(row, setting.title, setting.group, value, icon, func() {
			switch setting.group {
			case settingsAppearance:
				a.openInputSettings(a.settings, setting.key)
			case settingsNotices:
				a.openNotificationSettings(a.settings, setting.key)
			case settingsShortcuts:
				a.openKeyboardSettings(a.settings, setting.key)
			case settingsArchive:
				a.openArchivePage(a.settings, true)
			case settingsAbout:
				a.openThirdPartyLicenses(a.settings)
			}
		}).Value(value)
	}
	for _, setting := range mobileSettings {
		result(setting, "")
	}
	for _, shortcut := range slices.Concat(conversationShortcuts, interfaceShortcuts) {
		result(mobileSetting{settingsShortcuts, shortcut.Action, shortcut.Title}, shortcut.binding())
	}
	if !found {
		label(c, "没有匹配的设置", controlTextSize, palette(c).secondary).Padding(10, 0).Role(ui.RoleStatus)
	}
}

func revealSetting(target *string, key string, row ui.Element) {
	if *target == key {
		row.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter})
		*target = ""
	}
}

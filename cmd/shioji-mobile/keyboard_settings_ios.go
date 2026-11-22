package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const settingsShortcuts = "快捷键"

type mobileNavigationShortcut struct {
	Action string `json:"action"`
	Title  string `json:"title"`
	Key    string `json:"key"`
	Shift  bool   `json:"shift"`
}

var conversationShortcuts = []mobileNavigationShortcut{
	{Action: "compose", Title: "新建会话", Key: "n"},
	{Action: "search", Title: "搜索会话", Key: "k"},
	{Action: "attention", Title: "下一个待处理", Key: "j", Shift: true},
	{Action: "previous", Title: "返回上一个会话", Key: "h", Shift: true},
	{Action: "conversation-find", Title: inConversationFind, Key: "f"},
}

var interfaceShortcuts = []mobileNavigationShortcut{
	{Action: "settings", Title: "打开设置", Key: ","},
	{Action: "zoom-in", Title: "放大界面", Key: "+"},
	{Action: "zoom-out", Title: "缩小界面", Key: "-"},
	{Action: "zoom-reset", Title: "恢复默认缩放", Key: "0"},
}

func (s mobileNavigationShortcut) binding() string {
	prefix := "⌘ "
	if s.Shift {
		prefix += "⇧ "
	}
	return prefix + strings.ToUpper(s.Key)
}

func (a *mobileApp) openKeyboardSettings(parent *mygo.Window, target string) {
	var w *mygo.Window
	w = a.page(settingsShortcuts, func(c *ui.Context) {
		mobilePageBody(c, func() {
			mobileSection(c, "会话导航")
			for _, shortcut := range conversationShortcuts {
				row := mobileRow(c.Key(shortcut.Action), shortcut.Title, "", shortcut.binding(), nil, nil)
				revealSetting(&target, shortcut.Action, row)
			}
			mobileGroupHeading(c, "消息输入")
			mobileRow(c.Key("message-bindings"), "设置消息输入按键", "", "", nil, func() { a.openInputSettings(w, "send") })
			mobileGroupHeading(c, "界面")
			for _, shortcut := range interfaceShortcuts {
				row := mobileRow(c.Key(shortcut.Action), shortcut.Title, "", shortcut.binding(), nil, nil)
				revealSetting(&target, shortcut.Action, row)
			}
		})
	})
	a.pushNative(parent, w)
}

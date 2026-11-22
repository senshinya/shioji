package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) openWorkSource(page *mygo.Window, source domain.Session, work domain.WorkActivity) {
	current := a.sessionByID(source.ID)
	if current.ID == "" || current.ThreadID != source.ThreadID ||
		current.WorkspaceID != source.WorkspaceID || !a.pageCurrent(page) {
		return
	}
	detail := a.model.Details[source.ID]
	cached := !a.model.Connected || a.controller == nil
	if cached && mobileclient.WorkSourceAnchor(detail, work) == "" {
		return
	}
	jump := transcriptJump{session: source.ID, thread: source.ThreadID, item: work.SourceItemID,
		center: true, ready: cached, workSource: &work}
	if a.wide && a.chat == nil && a.pageCurrent(a.detail) {
		a.selectConversation(source.ID, jump)
		a.callUIKit("showPage", map[string]uintptr{"window": a.detail.NativeHandle()}, nil)
		return
	}
	a.openConversationFrom(page, source.ID, jump)
}

func (a *mobileApp) workDetailActions(c *ui.Context, page *mygo.Window, source domain.Session, work domain.WorkActivity, latest func(), output string) {
	if work.SourceItemID == "" && latest == nil && output == "" {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).MinHeight(mobileTapSize(c)).Gap(4).Children(func() {
		if work.SourceItemID != "" {
			available := a.model.Connected && a.controller != nil ||
				mobileclient.WorkSourceAnchor(a.model.Details[source.ID], work) != ""
			action(c.Key("source"), "定位来源", func() {
				a.openWorkSource(page, source, work)
			}).PaddingX(2).Disabled(!available)
		}
		ui.Spacer(c)
		if latest != nil {
			action(c.Key("latest"), "最新输出", latest).PaddingX(4).Label("跟随最新输出")
		}
		if output != "" {
			iconButton(c.Key("copy-output"), "复制后台输出", copySymbol, footerIconSize, func() {
				mygo.Clipboard.WriteText(output)
				c.AddToast(ui.Toast{ID: "work-output-copied", Title: "已复制后台输出", Timeout: 1500 * time.Millisecond})
			})
		}
	})
}

func workScrollMotion(c *ui.Context) ui.Animation {
	if c.Preferences().ReduceMotion {
		return ui.Animation{}
	}
	return ui.Smooth
}

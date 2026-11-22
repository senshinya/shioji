package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func (a *mobileApp) supportsPathAttachments(target mobileDraftTarget) bool {
	m := a.model
	if m.WorkerMinor < 47 {
		return false
	}
	if target.session != "" {
		session := a.sessionByID(target.session)
		return session.ID != "" && m.SessionReadiness(session).Capabilities.PathAttachments
	}
	return m.RunReadiness().Capabilities.PathAttachments
}

func (a *mobileApp) pathAttachmentPanel(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor) {
	m, target := a.model, editor.target
	pending := func(entry app.DraftAttachment) bool {
		return entry.Attachment.Kind == domain.PathAttachmentResource && !entry.Ready && draftAttachmentOwner(entry, target)
	}
	var files []app.DraftAttachment
	waitingConsent := false
	for _, entry := range m.Attachments[target.key] {
		if pending(entry) {
			files = append(files, entry)
			waitingConsent = waitingConsent || !entry.UploadApproved
		}
	}
	if len(files) == 0 {
		editor.rememberPathUpload = false
		return
	}
	canUpload := m.Connected && a.controller != nil && a.supportsPathAttachments(target)
	p := palette(c)
	title := "上传到 " + a.deviceName()
	if waitingConsent {
		title = "上传文件到 " + a.deviceName() + "？"
	}
	mobileInlinePanel(c.Key("path-upload")).FillWidth().Shrink(0).Padding(8, 10).Gap(2).Label("远程附件上传").Children(func() {
		label(c.Key("title"), title, controlTextSize, p.ink).FontWeight(600)
		if waitingConsent {
			label(c.Key("explanation"), "Agent 将收到文件在该设备上的路径。", captionTextSize, p.secondary)
		}
		ui.Scroll(c.Key("files")).FillWidth().MaxHeight(112).Clip().Label("待上传文件").Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				for _, entry := range files {
					a.pathAttachmentRow(c.Key(entry.Attachment.ID), parent, editor, entry, canUpload)
				}
			})
		})
		if !canUpload {
			hint := "连接执行设备后继续上传"
			if m.Connected {
				hint = "当前执行服务或 Agent 不支持远程路径引用"
			}
			label(c.Key("unavailable"), hint, captionTextSize, p.secondary).Role(ui.RoleStatus)
		}
		if waitingConsent {
			mobileToggle(c.Key("remember"), "对此设备不再提示", &editor.rememberPathUpload, a.refresh)
		}
		ui.Row(c.Key("actions")).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spacer(c)
			cancel := "取消上传"
			if waitingConsent {
				cancel = "取消"
			}
			action(c.Key("cancel"), cancel, func() {
				if !editor.current(a, parent) {
					return
				}
				for _, entry := range files {
					a.removeDraftAttachment(parent, editor, entry)
				}
			}).Label("取消远程附件上传").KeepFocus()
			if waitingConsent {
				primaryAction(c.Key("approve"), "上传并引用", func() {
					if !editor.current(a, parent) || !m.Connected || !a.supportsPathAttachments(target) {
						return
					}
					var approved []domain.Attachment
					for i, entry := range m.Attachments[target.key] {
						if pending(entry) && !entry.UploadApproved {
							m.Attachments[target.key][i].UploadApproved = true
							approved = append(approved, entry.Attachment)
						}
					}
					if len(approved) == 0 {
						return
					}
					if editor.rememberPathUpload {
						m.RemotePathUploads[target.device] = true
					}
					editor.rememberPathUpload = false
					a.save()
					for _, item := range approved {
						a.uploadDraftAttachment(target, item)
					}
					a.refresh()
				}).MinHeight(mobileTapSize(c)).KeepFocus().Disabled(!canUpload)
			}
		})
	})
}

func (a *mobileApp) pathAttachmentRow(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, entry app.DraftAttachment, canUpload bool) {
	item, target := entry.Attachment, editor.target
	sending := a.draftAttachmentUploads[draftUploadKey(target, item.ID)] != nil
	status := snapshotSize(item.Size)
	switch {
	case sending:
		status = snapshotSize(entry.Transferred) + " / " + status
	case entry.Error != "":
		status = "上传失败 · " + status
	case entry.UploadApproved:
		status = "等待上传 · " + status
	}
	p := palette(c)
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(5).Children(func() {
			mobileButton(c.Key("preview"), "预览草稿附件 "+item.Name,
				func() { a.previewDraftAttachment(parent, editor, entry) }).Value(status).
				MinHeight(mobileTapSize(c)).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(8).Children(func() {
				alignedSymbol(c, filesSymbol, item.Name, ui.Font{Size: captionTextSize * c.Preferences().TextScale}, 14, p.secondary)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					oneLine(c, item.Name, controlTextSize, p.ink)
					oneLine(c, status, captionTextSize, p.secondary)
				})
			})
			if entry.UploadApproved && !sending {
				action(c.Key("retry"), "重试", func() {
					if editor.current(a, parent) {
						a.uploadDraftAttachment(target, item)
					}
				}).KeepFocus().Label("重试上传 " + item.Name).Disabled(!canUpload)
			}
			iconButton(c.Key("remove"), "移除 "+item.Name, closeSymbol, 12,
				func() { a.removeDraftAttachment(parent, editor, entry) }).KeepFocus()
		})
		if sending {
			ui.Progress(c.Key("progress"), float64(entry.Transferred)/float64(item.Size)).
				FillWidth().Height(3).TextColor(p.secondary).Label("上传 " + item.Name)
		}
	})
}

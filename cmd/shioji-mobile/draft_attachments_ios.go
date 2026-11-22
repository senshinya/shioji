package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

type mobileDraftUploadKey struct {
	target mobileDraftTarget
	id     string
}

type mobileDraftUpload struct {
	cancel context.CancelFunc
}

func (a *mobileApp) visibleAttachmentDraft(parent *mygo.Window, target mobileDraftTarget) bool {
	return target.key != "" && a.pageCurrent(parent) && (target == a.draftTarget() || target == a.parallelDraftTarget())
}

func draftAttachmentOwner(entry app.DraftAttachment, target mobileDraftTarget) bool {
	return entry.DeviceID == target.device && entry.WorkspaceID == target.workspace
}

func draftUploadKey(target mobileDraftTarget, id string) mobileDraftUploadKey {
	target.harness = ""
	return mobileDraftUploadKey{target, id}
}

func (a *mobileApp) cancelDraftUpload(key mobileDraftUploadKey) {
	if upload := a.draftAttachmentUploads[key]; upload != nil {
		delete(a.draftAttachmentUploads, key)
		upload.cancel()
	}
}

func (a *mobileApp) rebindHomeDraftAttachments() {
	m, target := a.model, a.draftTarget()
	workspace := a.workspace(target.workspace)
	if a.preview || target.key != "home" || m.HasPendingInputForSession("") ||
		workspace.ID == "" || workspace.Archived || workspace.DeviceID != target.device {
		return
	}
	files := m.Attachments[target.key]
	var moved []domain.Attachment
	for i, entry := range files {
		if entry.DeviceID != target.device || entry.WorkspaceID == target.workspace || !localDraftAttachment(entry) {
			continue
		}
		source := target
		source.workspace = entry.WorkspaceID
		a.cancelDraftUpload(draftUploadKey(source, entry.Attachment.ID))
		entry.WorkspaceID = target.workspace
		entry.Ready, entry.Error, entry.Transferred = false, "", 0
		files[i] = entry
		moved = append(moved, entry.Attachment)
	}
	if len(moved) == 0 {
		return
	}
	a.save()
	for _, item := range moved {
		a.uploadDraftAttachment(target, item)
	}
}

func (a *mobileApp) draftAttachmentsReady(target mobileDraftTarget) bool {
	for _, entry := range a.model.Attachments[target.key] {
		if !entry.Ready || !draftAttachmentOwner(entry, target) ||
			entry.Attachment.Kind == domain.PathAttachmentResource && entry.UploadApproved && entry.InlinePosition == 0 {
			return false
		}
	}
	return true
}

func (a *mobileApp) draftReferences(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor) {
	m, target := a.model, editor.target
	visible := func(entry app.DraftAttachment) bool {
		if entry.Attachment.Kind == domain.PathAttachmentResource && draftAttachmentOwner(entry, target) {
			return false
		}
		return !entry.Attachment.InlineReference() || !entry.Ready || !draftAttachmentOwner(entry, target)
	}
	entries := slices.DeleteFunc(slices.Clone(m.Attachments[target.key]), func(entry app.DraftAttachment) bool { return !visible(entry) })
	frame := ui.Column(c.Key("draft-attachment-presence")).FillWidth().Clip().Shrink(0)
	retained := ui.Local(frame, "attachments", func() []app.DraftAttachment { return nil })
	present := len(entries) > 0
	if present {
		*retained = entries
	}
	p := palette(c)
	mobilePanelPresence(c, frame, present, mobileReplyFade, func(c *ui.Context) {
		ui.Row(c.Key("draft-attachments")).Wrap().Gap(4).Children(func() {
			for _, entry := range *retained {
				item := entry.Attachment
				key := draftUploadKey(target, item.ID)
				busy := a.draftAttachmentUploads[key] != nil
				owned := draftAttachmentOwner(entry, target)
				state := "已上传"
				switch {
				case !owned:
					state = "属于其他工作区"
				case busy:
					state = "上传中"
				case entry.Error != "":
					state = "上传失败"
				case !entry.Ready:
					state = "等待上传"
				}
				row := c.Key(item.ID)
				ui.Row(row).MaxWidthPercent(100).MaxWidth(290).AlignItems(ui.Center).Radius(10).Background(p.inset).Transition(mobileFeedbackFade(mobileReplyFade)).Children(func() {
					var open func()
					if !item.InlineReference() && !a.preview {
						open = func() { a.previewDraftAttachment(parent, editor, entry) }
					}
					mobileButton(row.Key("preview"), "预览草稿附件 "+item.Name, open).Value(state).
						Disabled(open == nil).DimDisabled(false).
						MinHeight(mobileTapSize(c)).MinWidth(0).Shrink(1).PaddingX(8).Gap(7).AlignItems(ui.Center).Children(func() {
						alignedSymbol(row, filesSymbol, item.Name, ui.Font{Size: captionTextSize * c.Preferences().TextScale}, 14, p.secondary)
						ui.Column(row).MinWidth(0).Shrink(1).Children(func() {
							oneLine(row, item.Name, captionTextSize, p.ink)
							if state != "已上传" {
								ink := p.secondary
								if entry.Error != "" && owned {
									ink = p.warning
								}
								oneLine(row, state, captionTextSize, ink)
							}
						})
					})
					if canRetryDraftAttachment(entry, target) && !busy {
						action(row.Key("retry"), "重试", func() {
							if editor.current(a, parent) {
								a.uploadDraftAttachment(target, item)
							}
						}).Label("重试上传 " + item.Name).KeepFocus().Shrink(0).PaddingX(4).
							MinWidth(mobileTapSize(c)).Disabled(!m.Connected || m.WorkerMinor < 6 || a.controller == nil)
					}
					iconButton(row.Key("remove"), "移除 "+item.Name, closeSymbol, 12,
						func() { a.removeDraftAttachment(parent, editor, entry) }).KeepFocus()
				})
			}
		})
	})
}

func (a *mobileApp) removeDraftAttachment(parent *mygo.Window, editor *mobileDraftEditor, entry app.DraftAttachment) {
	if !editor.current(a, parent) {
		return
	}
	m, target := a.model, editor.target
	files := m.Attachments[target.key]
	index := slices.IndexFunc(files, func(current app.DraftAttachment) bool {
		return current.Attachment == entry.Attachment && current.WorkspaceID == entry.WorkspaceID && current.DeviceID == entry.DeviceID
	})
	if index >= 0 {
		owner := target
		owner.workspace, owner.device = entry.WorkspaceID, entry.DeviceID
		a.cancelDraftUpload(draftUploadKey(owner, entry.Attachment.ID))
		m.Attachments[target.key] = slices.Delete(files, index, index+1)
		a.save()
		a.refresh()
	}
}

func localDraftAttachment(entry app.DraftAttachment) bool {
	return entry.UploadApproved && entry.Attachment.Kind == "" && !entry.Attachment.InlineReference()
}

func canRetryDraftAttachment(entry app.DraftAttachment, target mobileDraftTarget) bool {
	return !entry.Ready && localDraftAttachment(entry) && draftAttachmentOwner(entry, target)
}

func (a *mobileApp) uploadDraftAttachment(target mobileDraftTarget, item domain.Attachment) {
	m, controller := a.model, a.controller
	key := draftUploadKey(target, item.ID)
	if a.preview || controller == nil || !m.Connected || m.WorkerMinor < 6 || a.directory == "" ||
		a.draftAttachmentUploads[key] != nil {
		return
	}
	isPath := item.Kind == domain.PathAttachmentResource
	if isPath && !a.supportsPathAttachments(target) {
		return
	}
	match := func(entry app.DraftAttachment) bool {
		return entry.Attachment == item && !entry.Ready && entry.UploadApproved &&
			draftAttachmentOwner(entry, target) && (isPath || localDraftAttachment(entry))
	}
	files := m.Attachments[target.key]
	index := slices.IndexFunc(files, match)
	if index < 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	job := &mobileDraftUpload{cancel: cancel}
	if a.draftAttachmentUploads == nil {
		a.draftAttachmentUploads = make(map[mobileDraftUploadKey]*mobileDraftUpload)
	}
	a.draftAttachmentUploads[key] = job
	files[index].Error = ""
	a.save()
	a.refresh()
	staging := attachments.New(filepath.Join(a.directory, "attachments"))
	worker := app.RemoteAttachmentTarget(target.device, target.workspace, controller.DialDeviceStream)
	activeFiles := func() []app.DraftAttachment {
		if a.draftAttachmentUploads[key] != job || a.model != m {
			return nil
		}
		// Ordinary drafts can be displaced while a queued message is being edited.
		edit := m.ActiveQueueEdit(target.session)
		if edit.Token != target.queueEdit {
			if target.queueEdit != "" || edit.ID == "" {
				return nil
			}
			return edit.DisplacedAttachments
		}
		return m.Attachments[target.key]
	}
	var progress func(int64)
	if isPath {
		progress = func(offset int64) {
			mygo.RunOnMain(func() {
				current := activeFiles()
				if i := slices.IndexFunc(current, match); i >= 0 {
					current[i].Transferred = offset
					a.refresh()
				}
			})
		}
	}
	go func() {
		defer cancel()
		err := worker.UploadProgress(ctx, staging, item, progress)
		mygo.RunOnMain(func() {
			if a.draftAttachmentUploads[key] != job {
				return
			}
			current := activeFiles()
			delete(a.draftAttachmentUploads, key)
			if i := slices.IndexFunc(current, match); i >= 0 {
				current[i].Ready = err == nil
				if err != nil {
					current[i].Error = err.Error()
				}
				a.save()
				a.refresh()
			}
		})
	}()
}

func (a *mobileApp) previewDraftAttachment(parent *mygo.Window, editor *mobileDraftEditor, entry app.DraftAttachment) {
	m, target := a.model, editor.target
	current := func() bool {
		return a.model == m && editor.current(a, parent) &&
			slices.ContainsFunc(m.Attachments[target.key], func(active app.DraftAttachment) bool {
				return active.Attachment == entry.Attachment
			})
	}
	if !current() || a.directory == "" {
		return
	}
	item := entry.Attachment
	isMarkdown := mobileMarkdownFile(item.Name, item.MIME)
	controller, connected := a.controller, m.Connected && entry.DeviceID == m.Snapshot.DeviceID
	staging := attachments.New(filepath.Join(a.directory, "attachments"))
	go func() {
		path, err := staging.Path(attachments.StagingWorkspace, item)
		if err != nil && entry.Ready && connected && controller != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			worker := app.RemoteAttachmentTarget(entry.DeviceID, entry.WorkspaceID, controller.DialDeviceStream)
			path, err = worker.Download(ctx, staging, item)
		}
		var text []byte
		if err == nil && isMarkdown {
			text, err = os.ReadFile(path)
		}
		mygo.RunOnMain(func() {
			if !current() {
				return
			}
			if err != nil {
				editor.attachmentError = "无法读取此附件，请重新添加或连接执行设备后重试"
				a.refresh()
				return
			}
			editor.attachmentError = ""
			if isMarkdown {
				preview := &mobileLinkedPreview{target: item.Name}
				preview.apply(app.LinkedFile{
					Path: path, Resource: domain.FileResource{Name: item.Name, MIME: item.MIME, Size: item.Size},
				}, string(text), true)
				a.showLinkedFile(&reviewState{parent: parent, session: a.sessionByID(target.session), linked: preview}, "")
			} else {
				a.openNativeFilePreview(parent, path, item.Name, item.MIME)
			}
			a.refresh()
		})
	}()
}

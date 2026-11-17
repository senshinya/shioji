package ui

import (
	"context"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"time"
)

type remoteUploadJob struct {
	key, preparation, device, workspace, id string
	cancel                                  context.CancelFunc
}

func (v *View) remoteUploadKey(key string, a app.DraftAttachment) string {
	return key + ":" + a.DeviceID + ":" + a.WorkspaceID + ":" + a.Attachment.ID
}

func (v *View) remoteRememberKey(key string) string {
	return v.attachmentPreparationKey(key) + ":" + v.Model.WorkspaceID + ":" + v.referenceDeviceID()
}

func (v *View) ownsRemoteAttachment(a app.DraftAttachment) bool {
	return a.WorkspaceID == v.Model.WorkspaceID && a.DeviceID != "" && a.DeviceID == v.referenceDeviceID()
}

func (v *View) pendingRemoteAttachments(key string) []app.DraftAttachment {
	var files []app.DraftAttachment
	for _, a := range v.Model.Attachments[key] {
		if a.Attachment.Kind == domain.PathAttachmentResource && (!a.Ready || !v.ownsRemoteAttachment(a)) {
			files = append(files, a)
		}
	}
	return files
}

func (v *View) remoteDeviceName(id string) string {
	for _, device := range v.Model.Connections {
		if device.DeviceID == id && device.Name != "" {
			return device.Name
		}
	}
	return "远程设备"
}

func (v *View) confirmRemoteAttachments(key string, remember bool) {
	m := v.Model
	if !m.Connected || m.WorkerMinor < 47 || !m.RunReadiness().Capabilities.PathAttachments {
		return
	}
	for i, a := range m.Attachments[key] {
		if a.Attachment.Kind != domain.PathAttachmentResource || a.Ready || !v.ownsRemoteAttachment(a) {
			continue
		}
		a.UploadApproved, a.Error = true, ""
		m.Attachments[key][i] = a
		if remember {
			root := v.persistenceModel()
			if root.RemotePathUploads == nil {
				root.RemotePathUploads = map[string]bool{}
			}
			root.RemotePathUploads[a.DeviceID] = true
			m.RemotePathUploads = root.RemotePathUploads
		}
	}
	delete(v.remoteUploadRemember, v.remoteRememberKey(key))
}

func (v *View) removeRemoteAttachment(key, id string) {
	for _, a := range v.Model.Attachments[key] {
		if a.Attachment.Kind == domain.PathAttachmentResource && a.Attachment.ID == id {
			jobKey := v.remoteUploadKey(key, a)
			if job := v.remoteUploadJobs[jobKey]; job != nil {
				job.cancel()
				delete(v.remoteUploadJobs, jobKey)
			}
		}
	}
	v.Model.Attachments[key] = slices.DeleteFunc(slices.Clone(v.Model.Attachments[key]), func(a app.DraftAttachment) bool {
		return a.Attachment.Kind == domain.PathAttachmentResource && a.Attachment.ID == id
	})
}

func (v *View) cancelRemoteAttachments(key string) {
	for _, a := range v.pendingRemoteAttachments(key) {
		v.removeRemoteAttachment(key, a.Attachment.ID)
	}
	delete(v.remoteUploadRemember, v.remoteRememberKey(key))
}

// A job can only update the exact staged resource and edit generation that
// authorized it. Removing/retrying a resource invalidates the old job pointer.
func (v *View) remoteUploadDraft(m *app.Model, jobKey string, job *remoteUploadJob) int {
	if v.remoteUploadJobs[jobKey] != job || v.attachmentPreparationKey(job.key) != job.preparation {
		return -1
	}
	for i, a := range m.Attachments[job.key] {
		if a.Attachment.ID == job.id && a.WorkspaceID == job.workspace && a.DeviceID == job.device && a.UploadApproved && !a.Ready {
			return i
		}
	}
	return -1
}

func (v *View) startRemoteUpload(key string, draft app.DraftAttachment) {
	m := v.Model
	index := slices.IndexFunc(m.Attachments[key], func(current app.DraftAttachment) bool {
		return current.Attachment == draft.Attachment && current.WorkspaceID == draft.WorkspaceID && current.DeviceID == draft.DeviceID
	})
	if index < 0 {
		return
	}
	draft = m.Attachments[key][index]
	if v.Controller == nil || !m.Connected || !draft.UploadApproved || draft.Ready || draft.Error != "" || !v.ownsRemoteAttachment(draft) || m.WorkerMinor < 47 || !m.RunReadiness().Capabilities.PathAttachments {
		return
	}
	if v.remoteUploadJobs == nil {
		v.remoteUploadJobs = map[string]*remoteUploadJob{}
	}
	jobKey := v.remoteUploadKey(key, draft)
	if v.remoteUploadJobs[jobKey] != nil {
		return
	}
	target, err := m.AttachmentTarget(v.ControllerDirectory(), draft.WorkspaceID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	job := &remoteUploadJob{key: key, preparation: v.attachmentPreparationKey(key), device: draft.DeviceID, workspace: draft.WorkspaceID, id: draft.Attachment.ID, cancel: cancel}
	v.remoteUploadJobs[jobKey] = job
	store := v.staging()
	v.Controller.AttachmentTransfer(func(report func(app.Update)) app.Update {
		defer cancel()
		err := target.UploadProgress(ctx, store, draft.Attachment, func(offset int64) {
			report(func(m *app.Model) {
				if i := v.remoteUploadDraft(m, jobKey, job); i >= 0 {
					m.Attachments[key][i].Transferred = offset
				}
			})
		})
		return func(m *app.Model) {
			i := v.remoteUploadDraft(m, jobKey, job)
			if v.remoteUploadJobs[jobKey] == job {
				delete(v.remoteUploadJobs, jobKey)
			}
			if i < 0 {
				return
			}
			a := m.Attachments[key][i]
			a.Ready = err == nil
			if err != nil {
				a.Error = feedbackDetails(err.Error())
			} else {
				a.Transferred = a.Attachment.Size
			}
			m.Attachments[key][i] = a
		}
	})
}

func (v *View) remoteAttachmentsPanel(c *n.Context, t *n.Theme, key string) {
	for jobKey, job := range v.remoteUploadJobs {
		if v.remoteUploadDraft(v.Model, jobKey, job) < 0 {
			job.cancel()
			delete(v.remoteUploadJobs, jobKey)
		}
	}
	files := v.pendingRemoteAttachments(key)
	frame := n.Column(c.Key("remote-attachment-panel:" + key)).FillWidth().Clip()
	retained := n.Local(frame, "files", func() []app.DraftAttachment { return nil })
	closingAt := n.Local(frame, "closing-at", func() time.Time { return time.Time{} })
	if len(files) > 0 {
		*retained = slices.Clone(files)
		*closingAt = time.Time{}
	} else if len(*retained) > 0 {
		if closingAt.IsZero() {
			*closingAt = c.Now()
			c.After(240 * time.Millisecond)
		}
		if c.Preferences().ReduceMotion || c.Now().Sub(*closingAt) >= 240*time.Millisecond {
			*retained = nil
		}
	}
	animatedContentHeight(c, frame, len(files) > 0, 240*time.Millisecond, func() float32 {
		shown := *retained
		if len(shown) == 0 {
			return 0
		}
		unapproved, failed, foreign := false, false, false
		for _, a := range shown {
			unapproved = unapproved || !a.UploadApproved
			failed = failed || a.Error != ""
			foreign = foreign || !v.ownsRemoteAttachment(a)
		}
		name := v.remoteDeviceName(shown[0].DeviceID)
		title := "正在上传到 " + name
		if unapproved {
			title = "上传文件到 " + name + "？"
		} else if failed {
			title = "有文件未上传"
		}
		if foreign {
			title = "附件属于其他工作区"
		}
		panel := n.Column(c.Key("panel")).FillWidth().Padding(12, 14).Gap(6).Radius(12).Background(processingPanelColor(t)).Border(1, t.Border).Margin(0, 0, 8, 0).Label("远程附件上传")
		panel.Children(func() {
			n.Text(c, title).FontSize(13).FontWeight(500).FixedLineHeight(20)
			if foreign {
				n.Text(c, "请返回原工作区继续，或移除这些附件。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			} else if unapproved {
				n.Text(c, "将上传文件内容，并将远程路径提供给 Agent。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			n.Scroll(c.Key("files")).FillWidth().MaxHeight(192).Label("待上传文件列表").Children(func() {
				n.Column(c).FillWidth().Gap(6).Children(func() {
					for _, a := range shown {
						a := a
						row := n.Column(c.Key(a.Attachment.ID)).FillWidth().Gap(3)
						row.Children(func() {
							n.Row(c).FillWidth().Height(28).Gap(8).AlignItems(n.Center).Children(func() {
								inlineIconText(c, fileAttachmentIcon, a.Attachment.Name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 8, t.Text).Grow(1).MinWidth(0).Ellipsis("…").Tooltip(a.Attachment.Name)
								label := attachmentSize(a.Attachment.Size)
								if a.UploadApproved && !a.Ready && a.Error == "" {
									label = attachmentSize(a.Transferred) + " / " + label
								}
								n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).FontFeatures("tnum")
								if iconButton(c, queueRemoveIcon, "移除远程附件："+a.Attachment.Name).Size(28, 28).Tooltip("取消上传并移除此文件").Clicked() {
									v.removeRemoteAttachment(key, a.Attachment.ID)
								}
							})
							if a.Error != "" {
								n.Text(c, "上传失败，请重试").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Tooltip(a.Error).PaddingStart(22)
							} else if a.UploadApproved && !foreign {
								progress := n.Box(c.Key("progress")).FillWidth().Height(2).Margin(0, 0, 5, 22)
								value := progress.AnimateTo("progress", float32(a.Transferred)/float32(a.Attachment.Size), motionAnimation(c, 240*time.Millisecond))
								progress.Draw(func(p *n.Painter, r n.Rect) {
									p.Fill(r, t.Border, 1)
									r.W *= value
									p.Fill(r, t.TextMuted, 1)
								})
							}
						})
						if !foreign {
							v.startRemoteUpload(key, a)
						}
					}
				})
			})
			n.Row(c).FillWidth().Wrap().Gap(8).AlignItems(n.Center).Children(func() {
				rememberKey := v.remoteRememberKey(key)
				if v.remoteUploadRemember == nil {
					v.remoteUploadRemember = map[string]bool{}
				}
				remember := v.remoteUploadRemember[rememberKey]
				if unapproved && !foreign {
					configurationCheckbox(c, &remember, "对此设备不再提示", n.Font{Family: t.Font, Size: 12, Weight: 400})
					v.remoteUploadRemember[rememberKey] = remember
				}
				n.Row(c).Grow(1)
				label := "取消上传"
				if unapproved || foreign {
					label = "取消"
				}
				if ghost(c, label).FontSize(12).Height(28).Label("取消远程附件上传").Clicked() {
					v.cancelRemoteAttachments(key)
				}
				if unapproved && !foreign {
					if configurationAction(c, "上传并引用").Background(t.Text).TextColor(t.Background).Disabled(!v.Model.Connected || v.Model.WorkerMinor < 47 || !v.Model.RunReadiness().Capabilities.PathAttachments).Clicked() {
						v.confirmRemoteAttachments(key, remember)
					}
				} else if failed && !foreign {
					if configurationAction(c, "重试上传").Border(1, t.Border).Disabled(!v.Model.Connected).Clicked() {
						for i, a := range v.Model.Attachments[key] {
							if a.Attachment.Kind == domain.PathAttachmentResource && !a.Ready && v.ownsRemoteAttachment(a) && a.UploadApproved {
								v.Model.Attachments[key][i].Error = ""
							}
						}
					}
				}
			})
		})
		return 0
	})
	// A zero-height column still contributes its parent's gap. Hide it once
	// its own exit is complete, including the initial empty history editor.
	frame.Hidden(len(files) == 0 && len(*retained) == 0)
}

func remoteUploadMetadata(name string, a domain.Attachment) string {
	return fmt.Sprintf("已上传至 %s · %s\n发送时将向 Agent 提供远程路径。", name, attachmentSize(a.Size))
}

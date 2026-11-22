package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

type mobileDraftTarget struct {
	key, session, workspace, device, harness, queueEdit string
	history                                             bool
}

func (a *mobileApp) draftTarget() mobileDraftTarget {
	return mobileDraftTarget{
		key: a.model.DraftKey(), session: a.model.SessionID, workspace: a.model.WorkspaceID,
		device: a.model.Snapshot.DeviceID, harness: a.model.HarnessID(),
		queueEdit: a.model.ActiveQueueEdit(a.model.SessionID).Token,
	}
}

func (a *mobileApp) parallelDraftTarget() mobileDraftTarget {
	if !a.wide || a.review != nil && !a.reviewBelongsToParallel(a.review) || a.parallel == "" || a.parallel == a.model.SessionID {
		return mobileDraftTarget{}
	}
	s := a.sessionByID(a.parallel)
	if s.ID == "" {
		return mobileDraftTarget{}
	}
	return mobileDraftTarget{key: "session:" + s.ID, session: s.ID, workspace: s.WorkspaceID,
		device: a.model.Snapshot.DeviceID, harness: s.HarnessID, queueEdit: a.model.ActiveQueueEdit(s.ID).Token}
}

func (a *mobileApp) openReferences(parent *mygo.Window, target mobileDraftTarget, skillsAvailable bool, choose func(mobileclient.DraftReference) error) {
	if parent == nil {
		return
	}
	req := mobileRequest{}
	var files []domain.WorkspaceFile
	var skills []domain.Skill
	var w *mygo.Window
	query := ""
	showSkills := false
	q := protocol.GitQuery{WorkspaceID: target.workspace, SessionID: target.session}
	load := func() {
		if showSkills {
			a.pageJob(w, &req, "skills.list", protocol.SkillQuery{GitQuery: q, HarnessID: target.harness},
				decodePage(func(catalog domain.SkillCatalog) error {
					skills = catalog.Skills
					return nil
				}))
		} else {
			a.pageJob(w, &req, "file.search", protocol.FileSearchQuery{GitQuery: q, Query: strings.TrimSpace(query)},
				decodePage(func(result domain.FileSearchResult) error {
					files = result.Files
					return nil
				}))
		}
	}
	insert := func(ref mobileclient.DraftReference) error {
		if choose != nil {
			if err := choose(ref); err != nil {
				return err
			}
		}
		w.Pop()
		return nil
	}
	attach := func(in domain.Attachment) error {
		return insert(mobileclient.DraftReference{File: app.DraftAttachment{
			Attachment: in, DeviceID: target.device, WorkspaceID: target.workspace, Ready: true,
		}})
	}
	w = a.page("插入引用", func(c *ui.Context) {
		p := palette(c)
		ui.Column(c).Fill().Background(p.canvas).Children(func() {
			mobilePageBody(c, func() {
				if session := a.sessionByID(target.session); session.ID != "" {
					ui.Row(c).MinHeight(48).AlignItems(ui.Center).Gap(10).Children(func() {
						ui.Icon(c, layersSymbol).Size(17, 17).TextColor(p.secondary)
						ui.Column(c).MinWidth(0).Grow(1).Children(func() {
							oneLine(c, session.Title, 14, p.ink).FontWeight(600)
							oneLine(c, a.workspace(target.workspace).Name+" · "+a.deviceName(), 12, p.secondary)
						})
						action(c, "编辑草稿", func() { w.Pop() }).PaddingX(0)
					})
				}
				active := "文件"
				if showSkills {
					active = "Skill"
				}
				mobileSegments(c, []mobileSegment{
					{title: "文件", disabled: req.busy},
					{title: "Skill", disabled: req.busy || !skillsAvailable},
				}, active, func(title string) {
					if title != active {
						showSkills, query = title == "Skill", ""
						load()
					}
				})
				ui.Row(c).MinHeight(mobileTapSize(c)).Margin(12, 0, 5).Radius(22).Background(p.inset).
					AlignItems(ui.Center).Children(func() {
					iconButton(c, "搜索", searchSymbol, 16, load).Disabled(req.busy)
					placeholder := "搜索远程文件"
					if showSkills {
						placeholder = "搜索 Skill"
					}
					ui.TextInputBase(c, &query).Grow(1).MinWidth(0).MinHeight(mobileTapSize(c)).PaddingX(0).
						FontSize(14 * c.Preferences().TextScale).Label("搜索引用").Placeholder(placeholder).
						TextColor(p.ink).PlaceholderColor(p.secondary).OnTextKey(func(key ui.TextKey) bool {
						if key.Key != ui.KeyEnter || req.busy {
							return false
						}
						query = key.Text()
						load()
						return true
					})
				})
				if showSkills {
					for _, skill := range skills {
						if query != "" && !strings.Contains(strings.ToLower(skill.Name), strings.ToLower(query)) {
							continue
						}
						mobileRow(c.Key(skill.ID), skill.Name, skill.Description, "", terminalSymbol, func() {
							if err := insert(mobileclient.DraftReference{Skill: app.DraftSkill{
								Reference: skill.SkillReference, WorkspaceID: target.workspace, DeviceID: target.device, HarnessID: target.harness,
							}}); err != nil {
								req.err = err.Error()
							}
						})
					}
				} else {
					mobileSection(c, "工作区文件")
					for _, file := range files {
						mobileRow(c.Key(file.Root+"/"+file.Path), filepath.Base(file.Path), file.Path, "", filesSymbol, func() {
							if a.preview {
								if err := attach(domain.Attachment{ID: "preview:" + file.Path, Name: filepath.Base(file.Path), SourcePath: file.Path}); err != nil {
									req.err = err.Error()
								}
								return
							}
							a.pageJob(w, &req, "file.snapshot", protocol.FileSnapshotQuery{GitQuery: q, Root: file.Root, Path: file.Path}, decodePage(attach))
						})
					}
				}
				if !req.busy && req.err == "" && (showSkills && len(skills) == 0 || !showSkills && len(files) == 0) {
					mobileMessage(c, "没有找到引用")
				}
				if !showSkills {
					mobileRow(c, "选择本机附件", "", "", plusSymbol, func() { a.localAttachment(w, target, nil) })
				}
				req.status(c)
			})
			ui.Column(c).Padding(7, 16, 0).Children(func() {
				primaryAction(c, "返回草稿", func() { w.Pop() })
			})
		})
	})
	if a.preview {
		files = []domain.WorkspaceFile{
			{Root: "/Users/shinya/work/Shioji", Path: "internal/transport/connection.go"},
			{Root: "/Users/shinya/work/Shioji", Path: "internal/worker/worker.go"},
		}
	} else {
		load()
	}
	a.pushNative(parent, w)
}

func (a *mobileApp) localAttachment(parent *mygo.Window, target mobileDraftTarget, editor *mobileDraftEditor) {
	a.stageLocalAttachments(parent, target, editor, func(staging *attachments.Repository) ([]domain.Attachment, error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: "添加附件", Multiple: true,
		})
		if err != nil {
			return nil, err
		}
		if len(paths) > 8 {
			return nil, errors.New("每条消息最多添加 8 个附件")
		}
		var files []domain.Attachment
		for _, path := range paths {
			item, err := stageAttachmentFile(staging, path)
			if err != nil {
				return nil, fmt.Errorf("%s：%w", filepath.Base(path), err)
			}
			files = append(files, item)
		}
		return files, nil
	})
}

func stageAttachmentFile(staging *attachments.Repository, path string) (domain.Attachment, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.Attachment{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil {
		return domain.Attachment{}, err
	}
	name := filepath.Base(path)
	item, err := staging.Save(name, data)
	if errors.Is(err, attachments.ErrUnsupportedType) {
		return staging.SavePathResource(name, data)
	}
	return item, err
}

func (a *mobileApp) stageLocalAttachment(parent *mygo.Window, target mobileDraftTarget, editor *mobileDraftEditor, read func() (string, []byte, error)) bool {
	return a.stageLocalAttachments(parent, target, editor, func(staging *attachments.Repository) ([]domain.Attachment, error) {
		name, data, err := read()
		if err != nil || name == "" {
			return nil, err
		}
		item, err := staging.Save(name, data)
		return []domain.Attachment{item}, err
	})
}

func (a *mobileApp) stageLocalAttachments(parent *mygo.Window, target mobileDraftTarget, editor *mobileDraftEditor,
	prepare func(*attachments.Repository) ([]domain.Attachment, error)) bool {
	m, owner := a.model, a.controller
	currentDraft := func() bool {
		return a.model == m && a.controller == owner && a.visibleAttachmentDraft(parent, target)
	}
	if a.preview || target.key == "" || !currentDraft() || editor != nil && editor.pending() {
		return false
	}
	report := func(message string) {
		if editor != nil {
			editor.attachmentError = message
		} else if message != "" {
			m.Error = message
		}
		a.refresh()
	}
	if owner == nil || !m.Connected || a.directory == "" || target.workspace == "" {
		report("连接工作区所属设备后可添加附件")
		return false
	}
	if m.WorkerMinor < 6 {
		report("请升级执行服务后添加附件")
		return false
	}
	if editor != nil {
		editor.preparingAttachment = true
	}
	report("")
	staging := attachments.New(filepath.Join(a.directory, "attachments"))
	go func() {
		staged, err := prepare(staging)
		mygo.RunOnMain(func() {
			if editor != nil {
				editor.preparingAttachment = false
			}
			if !currentDraft() {
				a.refresh()
				return
			}
			if err != nil {
				report(err.Error())
				return
			}
			if len(staged) == 0 {
				a.refresh()
				return
			}
			current := m.Attachments[target.key]
			files := make([]domain.Attachment, 0, len(current)+len(staged))
			for _, entry := range current {
				files = append(files, entry.Attachment)
			}
			if err := (domain.Submission{Attachments: append(files, staged...)}).ValidateContent(); err != nil {
				report(err.Error())
				return
			}
			canUploadPaths := a.supportsPathAttachments(target)
			for _, item := range staged {
				if item.Kind == domain.PathAttachmentResource && !canUploadPaths {
					report("当前执行服务或 Agent 不支持远程路径引用，请升级或更换 Agent")
					return
				}
			}
			for _, item := range staged {
				entry := app.DraftAttachment{Attachment: item, WorkspaceID: target.workspace, DeviceID: target.device}
				entry.UploadApproved = item.Kind != domain.PathAttachmentResource || m.RemotePathUploads[target.device]
				m.Attachments[target.key] = append(m.Attachments[target.key], entry)
			}
			a.save()
			for _, item := range staged {
				a.uploadDraftAttachment(target, item)
			}
			a.refresh()
		})
	}()
	return true
}

func (a *mobileApp) openAttachment(parent *mygo.Window, session domain.Session, item domain.Attachment) {
	a.attachmentPage(parent, session, item, true)
}

func (a *mobileApp) attachmentPage(parent *mygo.Window, session domain.Session, item domain.Attachment, showLocal bool) {
	if item.Kind == domain.DiffCommentResource {
		a.openSavedDiffComment(parent, session, item, nil)
		return
	}
	if !mobileMarkdownFile(item.Name, item.MIME) {
		a.previewAttachment(parent, session, item, func() { a.attachmentDetailsPage(parent, session, item, showLocal) })
		return
	}
	a.attachmentDetailsPage(parent, session, item, showLocal)
}

func (a *mobileApp) attachmentDetailsPage(parent *mygo.Window, session domain.Session, item domain.Attachment, showLocal bool) {
	req := mobileRequest{}
	text := ""
	isMarkdown := mobileMarkdownFile(item.Name, item.MIME)
	options := markdown.Options{WrapCode: true}
	var w *mygo.Window
	var localPath string
	var load func()
	target := a.draftTarget()
	title := "附件预览"
	if showLocal {
		title = "附件"
	}
	w = a.page(title, func(c *ui.Context) {
		read := localPath != "" || text != ""
		mobilePageWithFooter(c, func() {
			mobilePageContext(c, session.Title, a.workspace(session.WorkspaceID).Name, a.deviceName())
			if showLocal {
				mobileSection(c, "远程结果")
			}
			status := "待读取"
			switch {
			case req.busy:
				status = "读取中"
			case req.err != "":
				status = "读取失败"
			case read:
				status = "已读取"
			}
			mobileRow(c.Key("remote-file"), item.Name, fmt.Sprintf("%d KB", (item.Size+1023)/1024), status, filesSymbol, nil)
			if text != "" && isMarkdown {
				root := ui.Column(c).Margin(8, 0, 0)
				root.Children(func() {
					options.Theme = &markdown.Theme{FontSize: bodyTextSize * c.Preferences().TextScale, CodeFont: a.codeFont()}
					a.bindMobileDiagrams(c, root, item.ID, &options)
					markdown.View(c, text, &options).Selectable()
				})
			} else if text != "" {
				mobilePanel(c.Key("attachment-text")).Margin(8, 0, 0).Children(func() {
					label(c, text, controlTextSize, palette(c).ink).Selectable().Padding(12)
				})
			}
			if req.err != "" {
				req.status(c)
			}
			if showLocal {
				mobileGroupHeading(c, "草稿附件")
				if a.preview {
					mobileRow(c, "reconnect.png", "196 KB · PNG", "已上传", fileCodeSymbol, nil)
				}
				for _, staged := range a.model.Attachments[target.key] {
					if staged.Attachment.ID == item.ID || staged.Attachment.Kind != "" {
						continue
					}
					status := "待上传"
					if staged.Ready {
						status = "已上传"
					} else if staged.Error != "" {
						status = "上传失败"
					}
					mobileRow(c.Key(staged.Attachment.ID), staged.Attachment.Name, "", status, fileCodeSymbol, nil)
				}
				mobileDetailRow(c.Key("pick-local-file"), "选择本机文件", "", "选择本机文件", plusSymbol,
					func() { a.localAttachment(w, target, nil) }, nil).
					Disabled(!a.preview && (!a.model.Connected || target.session != session.ID))
			}
		}, func() {
			if localPath != "" {
				ui.Row(c).Gap(8).Children(func() {
					secondaryAction(c, "另存为…", func() {
						go func() { _, _ = mygo.Dialog.SaveCopy(localPath, item.Name) }()
					}).Grow(1).Basis(0)
					if !isMarkdown {
						primaryAction(c, "原生预览", func() {
							a.openNativeFilePreview(w, localPath, item.Name, item.MIME)
						}).Grow(1).Basis(0)
					}
				})
			} else if !read {
				button := "读取附件"
				if req.err != "" {
					button = "重试读取"
				}
				primaryAction(c, button, load).Disabled(req.busy)
			}
		})
	})
	load = func() {
		if a.preview {
			text = "重连验证\n4 组用例通过。恢复连接后，消息顺序与授权状态保持一致。"
			w.Invalidate()
			return
		}
		if req.busy || a.directory == "" || !a.model.Connected || a.controller == nil {
			req.err = "连接执行设备后读取附件"
			w.Invalidate()
			return
		}
		owner := a.controller
		device := a.model.Snapshot.DeviceID
		staging := attachments.New(filepath.Join(a.directory, "attachments"))
		req.busy, req.err = true, ""
		w.Invalidate()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			worker := app.RemoteAttachmentTarget(device, session.WorkspaceID, owner.DialDeviceStream)
			path, err := worker.Download(ctx, staging, item)
			if err == nil && (item.MIME == "text/plain" || isMarkdown) {
				if bytes, readErr := os.ReadFile(path); readErr == nil {
					mygo.RunOnMain(func() {
						if a.windows[w] && owner == a.controller {
							text = string(bytes)
							w.Invalidate()
						}
					})
				}
			}
			mygo.RunOnMain(func() {
				if !a.windows[w] || owner != a.controller {
					return
				}
				req.busy = false
				if err != nil {
					req.err = err.Error()
				} else {
					localPath = path
				}
				w.Invalidate()
			})
		}()
	}
	if !a.preview {
		load()
	}
	a.pushNative(parent, w)
}

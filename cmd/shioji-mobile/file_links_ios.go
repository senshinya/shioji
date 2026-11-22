package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

type mobileLinkedPreview struct {
	target      string
	position    app.FileLinkPosition
	snapshot    *domain.Attachment
	file        app.LinkedFile
	req         mobileRequest
	cancel      context.CancelFunc
	options     markdown.Options
	reader      *editor.State
	image       mobileImageReader
	text, mode  string
	first, last int
	markdown    bool
}

func (a *mobileApp) bindMobileFileLinks(parent *mygo.Window, source domain.Session, sourcePath string, options *markdown.Options) {
	options.LinkContextMenu = func(menu *ui.Menu, dest string) {
		a.conversationLinkMenu(menu, parent, source, sourcePath, dest)
	}
	options.OnLink = func(dest string) {
		if strings.HasPrefix(dest, "#") {
			options.Anchor = dest
			options.AnchorVersion++
			a.refresh()
			return
		}
		a.activateConversationLink(parent, source, sourcePath, dest)
	}
}

func mobileFileLinkTarget(sourcePath, dest string) (string, app.FileLinkPosition, error) {
	target, position, err := app.FileLinkTarget(dest)
	if err == nil && sourcePath != "" && !path.IsAbs(target) {
		target = path.Join(path.Dir(sourcePath), target)
	}
	return target, position, err
}

func (a *mobileApp) replaceReview(r *reviewState) {
	if a.review == r {
		return
	}
	if r != nil && a.parallelOwner == nil && !a.reviewBelongsToParallel(r) {
		a.suspendRightPaneFind()
		if pane := a.parallelDevice; pane != nil {
			pane.replaceReview(nil)
		}
	}
	if a.review != nil && a.review.linked != nil {
		a.review.linked.stop()
	}
	if a.review != nil && a.review.comment != nil {
		a.review.comment.stop()
	}
	a.review = r
	if r != nil && r.linked != nil {
		a.openLinkedImage(r)
	}
	if home := a.splitRoot().home; home != nil {
		home.Invalidate()
	}
}

func (p *mobileLinkedPreview) stop() {
	p.image.close()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
		p.req.busy = false
	}
}

func (a *mobileApp) openLinkedFile(parent *mygo.Window, source domain.Session, sourcePath string, p *mobileLinkedPreview) {
	if !a.pageCurrent(parent) || source.ID == "" || a.sessionByID(source.ID).WorkspaceID != source.WorkspaceID {
		return
	}
	r := &reviewState{parent: parent, session: source, linked: p}
	if a.wide && parent == a.detail {
		if current := a.review; current != nil && current.session.ID == source.ID &&
			current.linked != nil && current.linked.file.Resource.Path == sourcePath && sourcePath != "" {
			r.previous = current
		}
		a.replaceReview(r)
	} else {
		window := a.page(path.Base(p.target), func(c *ui.Context) { a.linkedFileView(c, r, nil) })
		r.parent = window
		window.OnClosed(p.stop)
		a.pushNative(parent, window)
		a.openLinkedImage(r)
	}
	if p.file.Path == "" {
		a.loadLinkedFile(r)
	}
	a.refresh()
}

func (a *mobileApp) openLinkedImage(r *reviewState) {
	p := r.linked
	if p.file.Path != "" && strings.HasPrefix(p.file.Resource.MIME, "image/") {
		p.image.open(r.parent, p.file.Path, p.file.Resource.Name, a.model.Preferences.ReduceMotion, false)
	}
}

func (a *mobileApp) loadLinkedFile(r *reviewState) {
	p := r.linked
	if p.req.busy {
		return
	}
	if a.preview {
		file, text, err := previewLinkedFile(p.target)
		if err != nil {
			p.req.err = err.Error()
		} else {
			p.apply(file, text, true)
		}
		a.refresh()
		return
	}
	if !a.model.Connected || a.controller == nil || a.directory == "" {
		p.req.err = "连接所属设备后再打开文件"
		a.refresh()
		return
	}
	device, owner, epoch := a.endpoint.DeviceID, a.controller, a.model.ConnectionEpoch
	dir := filepath.Join(a.directory, "cache", "linked-files")
	staging := filepath.Join(a.directory, "attachments")
	name, snapshot := p.target, p.snapshot
	target := app.RemoteAttachmentTarget(device, r.session.WorkspaceID, owner.DialDeviceStream)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	p.cancel = cancel
	p.req.busy, p.req.err = true, ""
	a.refresh()
	go func() {
		var file app.LinkedFile
		var err error
		if snapshot == nil {
			file, err = target.FetchFile(ctx, dir, r.session.ID, name, nil)
		} else {
			// Resolve by the saved attachment identity. The original file may
			// have moved or changed since the tool completed.
			file.Path, err = target.Download(ctx, attachments.New(staging), *snapshot)
			file.Resource = domain.FileResource{Name: snapshot.Name, MIME: snapshot.MIME, Size: snapshot.Size}
		}
		var text string
		var isText bool
		if err == nil && file.Resource.Size <= 2<<20 && !strings.HasPrefix(file.Resource.MIME, "image/") {
			data, readErr := os.ReadFile(file.Path)
			if readErr == nil && utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
				text, isText = string(data), true
			} else if snapshot != nil {
				err = fmt.Errorf("无法读取已保存的文本文件 %s", name)
			}
		}
		mygo.RunOnMain(func() {
			defer cancel()
			if ctx.Err() == context.Canceled || p.cancel == nil || !a.pageCurrent(r.parent) || r.parent == a.detail && a.review != r {
				return
			}
			p.cancel = nil
			p.req.busy = false
			if owner != a.controller || epoch != a.model.ConnectionEpoch {
				p.req.err = "连接已改变，请重试"
			} else if err != nil {
				p.req.err = err.Error()
			} else {
				p.apply(file, text, isText)
				a.openLinkedImage(r)
				if r.parent != a.detail {
					r.parent.SetTitle(file.Resource.Name)
				}
			}
			a.refresh()
		})
	}()
}

func (p *mobileLinkedPreview) apply(file app.LinkedFile, text string, isText bool) {
	p.file, p.text = file, text
	if !isText {
		return
	}
	extension := strings.ToLower(path.Ext(file.Resource.Name))
	// The tool snapshot owns its bytes, not a workspace-relative path for
	// resolving Markdown links. Show its source with the file's own syntax.
	p.markdown = p.snapshot == nil && (extension == ".md" || extension == ".markdown")
	p.mode = "源码"
	if p.markdown && p.position.Line == 0 {
		p.mode = "预览"
	}
	buffer := editor.NewBuffer(text)
	buffer.SetLanguage(editor.LanguageFor(file.Resource.Name))
	p.reader = &editor.State{
		Buffer: buffer, ReadOnly: true, SoftWrap: true, NoActiveLine: true, NoScrollPastEnd: true,
		NoIndentGuides: true, Label: "文件内容：" + file.Resource.Name,
	}
	if p.position.Line > 0 {
		p.first = min(p.position.Line, buffer.LineCount())
		p.last = min(max(p.position.LastLine, p.first), buffer.LineCount())
		p.reader.GoTo(editor.Pos{Line: p.first - 1, Col: max(p.position.Column-1, 0)})
	}
}

func (a *mobileApp) linkedFileView(c *ui.Context, r *reviewState, close func()) {
	p := r.linked
	ink := palette(c)
	root := ui.Column(c).Fill().MinWidth(0).Background(ink.canvas)
	root.Children(func() {
		if close != nil {
			ui.Row(c).MinHeight(55).PaddingX(8).Gap(5).AlignItems(ui.Center).Children(func() {
				if r.previous != nil {
					iconButton(c, "返回上一个文件", backSymbol, toolbarIconSize, func() { a.replaceReview(r.previous); a.refresh() })
				}
				oneLine(c, path.Base(p.target), conversationTitle, ink.ink).Grow(1).PaddingX(8).FontWeight(650)
				iconButton(c, "关闭文件预览", closeSymbol, toolbarIconSize, close)
			})
		}
		mobilePageContent(c, 4).Gap(5).Children(func() {
			if p.snapshot != nil {
				oneLine(c, snapshotSize(p.snapshot.Size), captionTextSize, ink.secondary)
			} else {
				oneLine(c, p.target, captionTextSize, ink.secondary).Font("monospace")
			}
			if p.first != 0 {
				location := fmt.Sprintf("第 %d 行", p.first)
				if p.last > p.first {
					location = fmt.Sprintf("第 %d–%d 行", p.first, p.last)
				} else if p.position.Column > 0 {
					location = fmt.Sprintf("第 %d 行，第 %d 列", p.first, p.position.Column)
				}
				label(c, location, captionTextSize, ink.secondary)
			}
			if p.markdown {
				mobileSegments(c, []mobileSegment{{title: "预览"}, {title: "源码"}},
					p.mode, func(title string) { p.mode = title; a.refresh() })
			}
			if p.req.busy {
				mobileMessage(c, "正在读取文件…")
			} else if p.req.err != "" {
				p.req.status(c)
				action(c, "重新读取", func() { a.loadLinkedFile(r) }).AlignSelf(ui.Start)
			}
		})
		switch {
		case p.file.Path != "" && strings.HasPrefix(p.file.Resource.MIME, "image/"):
			ui.Column(c).Grow(1).Basis(0).MinHeight(0).Padding(4, pageInset).Children(func() {
				p.image.render(c)
			})
		case p.mode == "预览":
			ui.Scroll(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
				mobileContent(c, contentMaxWidth).Padding(8, pageInset, 24).Children(func() {
					p.options.WrapCode = true
					p.options.Theme = &markdown.Theme{FontSize: bodyTextSize * c.Preferences().TextScale, CodeFont: a.codeFont()}
					a.bindMobileFileLinks(r.parent, r.session, p.file.Resource.Path, &p.options)
					a.bindMobileMarkdownImages(r.parent, r.session, p.file.Resource.Path, &p.options)
					a.bindMobileDiagrams(c, root, p.target, &p.options)
					markdown.View(c, p.text, &p.options).Selectable()
				})
			})
		case p.reader != nil:
			p.reader.Font = ui.Font{Family: a.codeFont(), Size: captionTextSize * c.Preferences().TextScale}
			p.reader.Theme = editor.DefaultTheme(c.Theme())
			p.reader.Theme.Background, p.reader.Theme.Text = ink.canvas, ink.ink
			if p.first != 0 {
				p.reader.LineBackgrounds = []editor.LineBackground{{First: p.first - 1, Last: p.last - 1, Color: ink.inset}}
			}
			editor.View(c, p.reader).Grow(1).Basis(0).MinHeight(0)
		default:
			ui.Spacer(c)
		}
		if p.file.Path != "" {
			mobileContent(c, contentMaxWidth).Padding(4, pageInset, 8).Children(func() {
				ui.Row(c).Wrap().Gap(8).Children(func() {
					action(c, "原生预览", func() {
						a.openNativeFilePreview(r.parent, p.file.Path, p.file.Resource.Name, p.file.Resource.MIME)
					})
					action(c, "另存为…", func() {
						go func() { _, _ = mygo.Dialog.SaveCopy(p.file.Path, p.file.Resource.Name) }()
					})
				})
			})
		}
	})
}

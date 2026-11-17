package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"shioji/internal/domain"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

type markdownPreview struct {
	mode     string
	options  markdown.Options
	diagrams *mermaidCache
	scroll   n.ScrollState
	error    string
	cancel   context.CancelFunc
}

func (p *attachmentPreview) isMarkdown() bool {
	if strings.HasPrefix(p.Attachment.MIME, "image/") || p.Attachment.Kind == domain.PathAttachmentResource {
		return false
	}
	return markdownFileName(p.Attachment.Name)
}

func markdownFileName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

func (p *attachmentPreview) markdownTabs(c *n.Context) {
	if p.Markdown == nil {
		p.Markdown = &markdownPreview{mode: "preview"}
	}
	n.Column(c.Key("markdown-modes")).Width(108).Children(func() {
		p.Markdown.mode = configurationTabs(c, p.Markdown.mode, []configurationTabItem{{ID: "preview", Label: "预览"}, {ID: "source", Label: "源码"}}, configurationSegmented, p.Loading || p.Error != "")
	})
}

func (v *View) previewSource(c *n.Context, t *n.Theme, p *attachmentPreview) n.Element {
	if p.Reader == nil {
		p.Reader = &editor.State{Buffer: editor.NewBuffer(p.Text), ReadOnly: true, SoftWrap: true, NoActiveLine: true, NoScrollPastEnd: true, NoIndentGuides: true, Label: "文件内容：" + p.Attachment.Name}
		p.Reader.Buffer.SetLanguage(editor.LanguageFor(p.Attachment.Name))
		if p.Position.Line > 0 {
			line := min(p.Position.Line-1, p.Reader.Buffer.LineCount()-1)
			p.Reader.GoTo(editor.Pos{Line: line, Col: max(0, p.Position.Column-1)})
		}
	}
	p.Reader.Font = n.Font{Family: v.Model.Preferences.CodeFont, Size: 12}
	p.Reader.Theme = editor.DefaultTheme(t)
	p.Reader.Theme.Background = raised(t)
	if p.Position.Line > 0 {
		line := min(p.Position.Line-1, p.Reader.Buffer.LineCount()-1)
		last := min(max(line, p.Position.LastLine-1), p.Reader.Buffer.LineCount()-1)
		p.Reader.LineBackgrounds = []editor.LineBackground{{First: line, Last: last, Color: raised(t).Mix(t.Text, .08)}}
	}
	return editor.View(c.Key("file-reader"), p.Reader).FillWidth()
}

func (v *View) markdownPreviewContent(c *n.Context, t *n.Theme, p *attachmentPreview, height float32) {
	m := p.Markdown
	th := markdown.DefaultTheme(t)
	th.Font, th.CodeFont = v.Model.Preferences.InterfaceFont, v.Model.Preferences.CodeFont
	th.FontSize, th.LineHeight, th.BlockSpacing, th.CodeSize = 14, 24.0/14, 1, 12.0/14
	th.Link, th.Checkbox, th.LinkWeight, th.LinkUnderline = t.Text, t.Text, 500, true
	th.Headings[0] = markdown.HeadingStyle{Size: 26.0 / 14, Weight: 600, Top: 0, Bottom: 16.0 / 14}
	th.Headings[1] = markdown.HeadingStyle{Size: 18.0 / 14, Weight: 600, Top: 2, Bottom: 10.0 / 14}
	m.options.Theme = th
	v.bindPreviewMarkdown(c, p)
	if m.error != "" {
		n.Text(c, m.error).FontSize(12).TextColor(t.TextMuted)
	}
	n.Column(c.Key("markdown-body")).FillWidth().Height(height).Children(func() {
		index := 0
		if m.mode == "source" {
			index = 1
		}
		configurationTabPanel(c.Key("markdown-panel"), m.mode, index, configurationPanelFill, func() {
			if m.mode == "source" {
				v.previewSource(c, t, p).FillHeight().Grow(1)
				return
			}
			n.Scroll(c.Key("markdown-scroll")).FillWidth().FillHeight().MinHeight(0).TrackScroll(&m.scroll).Label("Markdown 预览").Children(func() {
				n.Column(c).FillWidth().MaxWidth(688).AlignSelf(n.Center).Padding(14, 0, 44, 0).Children(func() {
					if strings.TrimSpace(p.Text) == "" {
						n.Text(c, "文件为空").FontSize(13).TextColor(t.TextMuted)
						return
					}
					markdown.View(c.Key("markdown"), p.Text, &m.options).FillWidth()
				})
			})
		}, false)
	})
}

func (p *attachmentPreview) cancelMarkdown() {
	if p.Markdown != nil && p.Markdown.cancel != nil {
		p.Markdown.cancel()
		p.Markdown.cancel = nil
	}
}

func (p *attachmentPreview) releaseMarkdown() {
	p.cancelMarkdown()
	if p.Markdown != nil && p.Markdown.diagrams != nil {
		p.Markdown.diagrams.close()
	}
	p.Markdown = nil
}

// Paths remain device paths. Only the authenticated worker may open them.
func (p *attachmentPreview) markdownPath(dest string) (string, error) {
	path, _, err := p.markdownTarget(dest)
	return path, err
}

func (p *attachmentPreview) markdownTarget(dest string) (string, fileLinkPosition, error) {
	path, position, err := fileLinkTarget(dest)
	if err != nil {
		return "", position, err
	}
	if !filepath.IsAbs(path) {
		if !filepath.IsAbs(p.SourcePath) {
			return "", position, errors.New("附件没有原始目录，无法解析相对路径")
		}
		path = filepath.Join(filepath.Dir(p.SourcePath), path)
	}
	return path, position, nil
}

func (v *View) bindPreviewMarkdown(c *n.Context, p *attachmentPreview) {
	m := p.Markdown
	bindMermaid(c, &m.options, &m.diagrams, p.SourceSessionID+"|"+p.SourceEpoch, false)
	services := c.Services()
	session := v.Model.Details[p.SourceSessionID].Session
	m.options.OnLink = func(dest string) {
		if n.SafeToOpen(dest) {
			services.OpenURL(dest)
			return
		}
		if strings.HasPrefix(dest, "#") {
			m.options.Anchor = dest
			m.options.AnchorVersion++
			services.Invalidate()
			return
		}
		path, position, err := p.markdownTarget(dest)
		if err == nil && session.ID == "" {
			err = errors.New("当前文件没有所属会话，无法读取链接")
		}
		if err != nil {
			m.error = err.Error()
			services.Invalidate()
			return
		}
		v.startFileLink(session, path, services, position)
	}
	menu := v.forwardLinkMenu(c, session)
	m.options.LinkContextMenu = func(menuView *n.Menu, dest string) {
		if menu == nil {
			return
		}
		if !n.SafeToOpen(dest) && !strings.HasPrefix(dest, "#") {
			path, err := p.markdownPath(dest)
			if err != nil {
				return
			}
			dest = path
		}
		menu(menuView, dest)
	}
	if m.options.LoadImage != nil || v.Controller == nil || v.StatePath == "" || session.ID == "" || !v.Model.Connected {
		return
	}
	target, err := v.Model.AttachmentTarget(v.Controller.Directory, p.WorkspaceID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	controller, directory := v.Controller, v.linkedFileDirectory()
	// Match the existing preview cache's image count, with two transfers at a
	// time. Each image is bounded by the attachment size and decoder limits.
	var mu sync.Mutex
	count := 0
	slots := make(chan struct{}, 2)
	m.options.LoadImage = func(dest string) (n.ImageSource, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := p.markdownPath(dest)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		if count >= 32 {
			mu.Unlock()
			return nil, errors.New("图片数量超出预览范围")
		}
		count++
		mu.Unlock()
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-slots }()
		call, stop := context.WithTimeout(ctx, time.Minute)
		defer stop()
		routed := target
		if target.SSH != nil {
			ssh, e := controller.TailnetSSH(*target.SSH)
			if e != nil {
				return nil, e
			}
			routed.SSH = &ssh
		}
		result, err := routed.FetchFile(call, directory, session.ID, path, func(meta domain.FileResource, _ int64) {
			if meta.Size > 8<<20 {
				stop()
			}
		})
		if err != nil {
			return nil, err
		}
		image, err := decodeAttachmentImage(result.Path, 1600)
		if call.Err() != nil {
			return nil, call.Err()
		}
		return image, err
	}
}

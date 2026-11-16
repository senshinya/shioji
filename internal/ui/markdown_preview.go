package ui

import (
	"path/filepath"
	"strings"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

type markdownPreview struct {
	mode    string
	options markdown.Options
	scroll  n.ScrollState
}

func (p *attachmentPreview) isMarkdown() bool {
	if strings.HasPrefix(p.Attachment.MIME, "image/") {
		return false
	}
	switch strings.ToLower(filepath.Ext(p.Attachment.Name)) {
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
	}
	p.Reader.Font = n.Font{Family: v.Model.Preferences.CodeFont, Size: 12}
	p.Reader.Theme = editor.DefaultTheme(t)
	p.Reader.Theme.Background = raised(t)
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

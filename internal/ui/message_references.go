package ui

import (
	"github.com/egoist/mygo/transfer"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
)

func (v *View) messageReferenceContent(item domain.Item) composerContent {
	content := composerContent{text: item.Text}
	positions := map[string]int{}
	for _, p := range item.ReferencePositions {
		if p.SkillID != "" {
			positions["skill:"+p.SkillID] = p.Position
		} else {
			positions["file:"+p.AttachmentID] = p.Position
		}
	}
	for _, file := range item.Attachments {
		if file.SourcePath != "" {
			content.files = append(content.files, app.DraftAttachment{Attachment: file, Ready: true, DeviceID: v.referenceDeviceID(), WorkspaceID: v.Model.WorkspaceID, InlinePosition: positions["file:"+file.ID]})
		}
	}
	for _, skill := range item.Skills {
		content.skills = append(content.skills, app.DraftSkill{Reference: skill, DeviceID: v.referenceDeviceID(), WorkspaceID: v.Model.WorkspaceID, HarnessID: v.Model.HarnessID(), InlinePosition: positions["skill:"+skill.ID]})
	}
	return content
}
func (v *View) messageCopyText(item domain.Item) string {
	if item.Kind == "command" {
		return item.Name
	}
	return referencePlainText(referenceDocumentText(v.messageReferenceContent(item)))
}

func (v *View) commandTagBody(c *n.Context, item domain.Item) {
	a := inlineTagAttachment(c, item.Name, compactContextIcon, 14)
	n.Column(c).Selectable().CopyData(func([]n.SelectedText) transfer.Data { return transfer.TextData(item.Name) }).Children(func() {
		n.RichText(c, n.Span{Text: string(richtext.ObjectReplacement), ObjectWidth: a.Width, ObjectHeight: a.Height - a.Descent, ObjectDescent: a.Descent}).
			FontSize(14).FixedLineHeight(24).Selectable().Label("操作：" + item.Name).
			DrawObjects(func(p *n.Painter, _ int, r n.Rect) { a.Draw(p, r) })
	})
}

func (v *View) messageReferenceBody(c *n.Context, t *n.Theme, item domain.Item) n.Element {
	text := v.referenceText(c, v.messageReferenceContent(item))
	objects := map[int]*richtext.Attachment{}
	var body n.Element
	scope := n.Column(c).Selectable()
	scope.Children(func() {
		body = n.RichText(c).FontSize(14).FixedLineHeight(24).Selectable().Label("用户消息正文")
		body.Children(func() {
			for at, run := range text.Runs(0, text.Len()) {
				a := run.Attributes.Attachment
				if a == nil {
					n.Text(c, run.Text)
					continue
				}
				objects[at] = a
				ref := a.Data.(composerReference)
				label := "Skill：" + ref.name()
				if ref.file.Attachment.ID != "" {
					label = "预览引用：" + ref.name()
				}
				tag := n.RichText(c, n.Span{Text: string(richtext.ObjectReplacement), ObjectWidth: a.Width, ObjectHeight: a.Height - a.Descent, ObjectDescent: a.Descent}).Label(label).Tooltip(a.Name)
				if a.Tooltip != "" {
					tag.Tooltip(a.Tooltip)
				}
				if a.OnClick != nil {
					tag.Cursor(n.CursorPointer).Role(n.RoleButton)
					if tag.Clicked() {
						a.OnClick(tag.Bounds())
					}
				}
			}
		})
		body.DrawObjects(func(p *n.Painter, at int, r n.Rect) {
			if a := objects[at]; a != nil {
				a.Draw(p, r)
			}
		})
	})
	scope.CopyData(func(parts []n.SelectedText) transfer.Data {
		var out strings.Builder
		for i, part := range parts {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(referencePlainText(text.Slice(part.Start, part.End)))
		}
		return transfer.TextData(out.String())
	})
	// Find offsets refer to ordinary text; map them past the object runes.
	v.decoratePlainFind(c, body, item, nil, func(offset int, end bool) int {
		plain, raw := 0, 0
		for _, run := range text.Runs(0, text.Len()) {
			if run.Attributes.Attachment != nil {
				if plain < offset || plain == offset && !end {
					raw++
				}
				continue
			}
			for range []rune(run.Text) {
				if plain < offset {
					raw++
				}
				plain++
			}
		}
		return raw
	})
	return scope
}

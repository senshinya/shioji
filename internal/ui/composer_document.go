package ui

import (
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"shioji/internal/app"
	"shioji/internal/domain"
)

// Reference positions belong to Shioji input presentation, never to a harness's
// wire input. The native document owns atomic selection and undo, including
// the typed reference metadata carried by each attachment.
type composerReference struct {
	file  app.DraftAttachment
	skill app.DraftSkill
}

func (r composerReference) position() int {
	if r.skill.Reference.ID != "" {
		return r.skill.InlinePosition
	}
	return r.file.InlinePosition
}
func (r composerReference) name() string {
	if r.skill.Reference.ID != "" {
		return r.skill.Reference.Name
	}
	return r.file.Attachment.Name
}

type composerContent struct {
	text   string
	files  []app.DraftAttachment
	skills []app.DraftSkill
}

func (v *View) composerContent(key string) composerContent {
	files := []app.DraftAttachment{}
	for _, file := range v.Model.Attachments[key] {
		if file.Attachment.InlineReference() {
			files = append(files, file)
		}
	}
	return composerContent{v.Model.Drafts[key], files, slices.Clone(v.Model.DraftSkills[key])}
}
func (a composerContent) equal(b composerContent) bool {
	return a.text == b.text && slices.EqualFunc(a.files, b.files, func(a, b app.DraftAttachment) bool {
		a.Ready, b.Ready = false, false
		a.Error, b.Error = "", ""
		return a == b
	}) && slices.Equal(a.skills, b.skills)
}

type composerDocument struct {
	doc            *richtext.Document
	last           composerContent
	version        uint64
	key, editToken string
}
type composerInput struct {
	n.Element
	binding *composerDocument
}

func (i composerInput) TextAnchorCaret() (int, int) { return i.binding.doc.Selection() }
func (i composerInput) TextSelection() (int, int) {
	a, b := i.TextAnchorCaret()
	return min(a, b), max(a, b)
}
func (i composerInput) SetTextSelection(a, b int) { i.binding.doc.SetSelection(a, b) }
func (i composerInput) Composing() bool           { return i.binding.doc.Composing() }

type composerKey struct {
	Key     n.Key
	Mods    n.Modifiers
	binding *composerDocument
}

func (k composerKey) Text() string { return k.binding.doc.Text().String() }
func (k composerKey) Selection() (int, int) {
	a, b := k.binding.doc.Selection()
	return min(a, b), max(a, b)
}

func (v *View) composerDocument(c *n.Context, key string) *composerDocument {
	if v.composerDocuments == nil {
		v.composerDocuments = map[string]*composerDocument{}
	}
	identity := key
	editToken := ""
	if edit := v.Model.QueueEdits[v.Model.SessionID]; edit.ID != "" && key == v.Model.DraftKey() {
		editToken = edit.Token
		identity += ":edit:" + edit.Token
	}
	for id, b := range v.composerDocuments {
		if b.key == key && b.editToken != "" && id != identity {
			delete(v.composerDocuments, id)
		}
	}
	b := v.composerDocuments[identity]
	content := v.composerContent(key)
	if b == nil {
		b = &composerDocument{doc: richtext.NewDocument(richtext.NewText("")), key: key, editToken: editToken}
		v.composerDocuments[identity] = b
	}
	if b.version == 0 || !content.equal(b.last) {
		b.doc.SetText(v.referenceText(c, content))
		b.last, b.version = content, b.doc.Version()
	}
	return b
}

// Publish only edits, so an external acceptance/scope change cannot be undone by
// an idle document. Ordinary pasted attachments retain their existing strip.
func (v *View) publishComposer(key string, b *composerDocument) {
	if b.doc.Version() == b.version || !b.last.equal(v.composerContent(key)) {
		return
	}
	text := b.doc.Text()
	var plain strings.Builder
	files := []app.DraftAttachment{}
	for _, file := range v.Model.Attachments[key] {
		if !file.Attachment.InlineReference() {
			files = append(files, file)
		}
	}
	skills := []app.DraftSkill{}
	for at, run := range text.Runs(0, text.Len()) {
		if attachment := run.Attributes.Attachment; attachment != nil {
			ref, ok := attachment.Data.(composerReference)
			if !ok {
				continue
			}
			if ref.skill.Reference.ID != "" {
				ref.skill.InlinePosition = at + 1
				if !slices.ContainsFunc(skills, func(s app.DraftSkill) bool {
					return s.Reference == ref.skill.Reference && s.DeviceID == ref.skill.DeviceID && s.WorkspaceID == ref.skill.WorkspaceID && s.HarnessID == ref.skill.HarnessID
				}) {
					skills = append(skills, ref.skill)
				}
			} else {
				for _, current := range v.Model.Attachments[key] {
					if current.Attachment == ref.file.Attachment && current.DeviceID == ref.file.DeviceID && current.WorkspaceID == ref.file.WorkspaceID {
						ref.file = current
						break
					}
				}
				ref.file.InlinePosition = at + 1
				if !slices.ContainsFunc(files, func(a app.DraftAttachment) bool { return a.Attachment == ref.file.Attachment }) {
					files = append(files, ref.file)
				}
			}
		} else {
			plain.WriteString(strings.ReplaceAll(run.Text, string(richtext.ObjectReplacement), ""))
		}
	}
	v.Model.Drafts[key] = plain.String()
	v.Model.Attachments[key] = files
	v.Model.DraftSkills[key] = skills
	b.last, b.version = v.composerContent(key), b.doc.Version()
}

func (v *View) insertReference(c *n.Context, b *composerDocument, state *fileMention, ref composerReference) bool {
	text := b.doc.Text().String()
	current, original := []rune(text), []rune(state.text)
	if state.end > len(current) || state.end > len(original) || string(current[state.start:state.end]) != string(original[state.start:state.end]) {
		return false
	}
	anchor, caret := b.doc.Selection()
	addSpace := state.end == len(current) || current[state.end] != ' '
	inserted := 1
	if addSpace {
		inserted++
	}
	adjust := func(p int) int {
		if p <= state.start {
			return p
		}
		if p <= state.end {
			return state.start + 2
		}
		return p - (state.end - state.start) + inserted
	}
	b.doc.Edit(func(e *richtext.Edit) {
		e.ReplaceString(state.start, state.end, string(richtext.ObjectReplacement), richtext.Attributes{Attachment: v.referenceAttachment(c, ref)})
		if addSpace {
			e.ReplaceString(state.start+1, state.start+1, " ", richtext.Attributes{})
		}
		e.Select(adjust(anchor), adjust(caret))
	})

	v.publishComposer(b.key, b)
	return true
}

func (v *View) referenceAttachment(c *n.Context, ref composerReference) *richtext.Attachment {
	iconSize := float32(14)
	// The book has a wider visible footprint than the file glyph.
	if ref.skill.Reference.ID != "" {
		iconSize = 12
	}
	entry := []time.Time{}
	if born, ok := v.commentTagEntries[ref.file.Attachment.ID]; ok {
		entry = append(entry, born)
	}
	display := ref.name()
	if ref.file.Attachment.Kind == domain.DiffCommentResource {
		display = compactDiffCommentTag(display, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400})
	}
	a := inlineTagAttachmentDisplay(c, ref.name(), display, fileOrSkillIcon(ref), iconSize, entry...)
	a.Data = ref
	if ref.file.Attachment.Kind == domain.DiffCommentResource {
		draw := a.Draw
		a.Draw = func(p *n.Painter, r n.Rect) {
			draw(p, r)
			if v.nextCommentTagBounds == nil {
				v.nextCommentTagBounds = map[*richtext.Attachment]n.Rect{}
			}
			v.nextCommentTagBounds[a] = r
		}
		a.OnClick = func(n.Rect) { v.locateDiffComment(ref.file) }
		return a
	}
	surface := codeSurface(c.Theme())
	if ref.file.Attachment.ID != "" {
		a.Tooltip = ref.file.Attachment.SourcePath
		a.OnClick = func(r n.Rect) {
			if ref.file.Ready && ref.file.DeviceID == v.referenceDeviceID() && ref.file.WorkspaceID == v.Model.WorkspaceID {
				paint := a.Draw
				v.openAttachment(ref.file.Attachment, ref.file.WorkspaceID, r, attachmentPreviewOrigin{Surface: surface, Radius: 5, Content: func(c *n.Context) { n.Column(c).Fill().Draw(paint) }})
			}
		}
	}
	return a
}

func inlineTagAttachment(c *n.Context, name string, icon *n.SVG, iconSize float32, entry ...time.Time) *richtext.Attachment {
	return inlineTagAttachmentDisplay(c, name, name, icon, iconSize, entry...)
}
func inlineTagAttachmentDisplay(c *n.Context, name, display string, icon *n.SVG, iconSize float32, entry ...time.Time) *richtext.Attachment {
	font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
	layout := n.ShapeText(display, font, 0)
	width, _ := layout.Size()
	width = min(width, 180)
	height := float32(20)
	// Reserve the same cap-to-descender body for every Latin name. Centering
	// each word's ink would move its baseline whenever a g/p/y appears.
	reference := "Hg"
	for _, r := range name {
		if unicode.Is(unicode.Han, r) {
			reference = "国"
			break
		}
	}
	body := font.InkBounds(reference)
	textTop := height/2 - (body.Y + body.H/2) - layout.Lines()[0].Ascent
	outer := inlineFontMetrics(name, n.Font{Family: c.Theme().Font, Size: 14, Weight: 400})
	a := &richtext.Attachment{Name: name, Width: width + 33, Height: height, Descent: height/2 - outer.center}

	animate := len(entry) > 0 && !c.Preferences().ReduceMotion
	a.Draw = func(p *n.Painter, r n.Rect) {
		progress := float32(1)
		if animate {
			progress = n.EaseOut(min(1, max(0, float32(p.Now().Sub(entry[0]))/float32(180*time.Millisecond))))
			if progress < 1 {
				p.AnimationFrame()
			}
		}
		t := p.Theme()
		p.Fill(r, codeSurface(t).Alpha(progress), 5)
		p.Stroke(r, t.Border.Alpha(progress), 5, 1)

		p.Icon(icon, n.Rect{X: r.X + 6 + (14-iconSize)/2, Y: r.Y + (r.H-iconSize)/2, W: iconSize, H: iconSize}, t.TextMuted.Alpha(progress))
		p.Clip(n.Rect{X: r.X + 25, Y: r.Y, W: max(0, r.W-30), H: r.H}, 0, func() { p.TextLayout(layout, r.X+25, r.Y+textTop, t.Text.Alpha(progress)) })
	}
	return a
}
func fileOrSkillIcon(ref composerReference) *n.SVG {
	if ref.file.Attachment.Kind == domain.DiffCommentResource {
		return diffCommentIcon
	}
	if ref.skill.Reference.ID != "" {
		return skillIcon
	}
	return fileAttachmentIcon
}

// The same attributed content feeds both editable drafts and selectable sent
// messages. One object rune retains each reference's original text position.
func referenceDocumentText(content composerContent) richtext.Text {
	text := richtext.NewText(content.text)
	var refs []composerReference
	for _, file := range content.files {
		if file.Attachment.InlineReference() {
			refs = append(refs, composerReference{file: file})
		}
	}
	for _, skill := range content.skills {
		refs = append(refs, composerReference{skill: skill})
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].position() < refs[j].position() })
	for _, ref := range refs {
		at := ref.position() - 1
		if at < 0 {
			at = text.Len()
		}
		at = min(at, text.Len())
		text = text.ReplaceString(at, at, string(richtext.ObjectReplacement), richtext.Attributes{Attachment: &richtext.Attachment{Name: ref.name(), Data: ref}})
	}
	return text
}
func (v *View) referenceText(c *n.Context, content composerContent) richtext.Text {
	text := referenceDocumentText(content)
	out := richtext.NewText("")
	for _, run := range text.Runs(0, text.Len()) {
		attrs := run.Attributes
		if a := attrs.Attachment; a != nil {
			attrs.Attachment = v.referenceAttachment(c, a.Data.(composerReference))
		}
		out = out.Append(run.Text, attrs)
	}
	return out
}
func referencePlainText(text richtext.Text) string {
	var out strings.Builder
	for _, run := range text.Runs(0, text.Len()) {
		if a := run.Attributes.Attachment; a != nil {
			out.WriteString(a.Name)
		} else {
			out.WriteString(run.Text)
		}
	}
	return out.String()
}

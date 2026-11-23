package mobileclient

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type DraftReference struct {
	File  app.DraftAttachment
	Skill app.DraftSkill
}

func (r DraftReference) ID() string {
	if r.Skill.Reference.ID != "" {
		return "skill:" + r.Skill.Reference.ID
	}
	return "file:" + r.File.Attachment.ID
}

type draftContent struct {
	text string
	refs []DraftReference
}

func readDraft(m *app.Model, key string) draftContent {
	d := draftContent{text: m.Drafts[key]}
	for _, file := range m.Attachments[key] {
		if file.Attachment.InlineReference() && file.Ready && !unplacedPathUpload(file) {
			file.Transferred = 0
			d.refs = append(d.refs, DraftReference{File: file})
		}
	}
	for _, skill := range m.DraftSkills[key] {
		d.refs = append(d.refs, DraftReference{Skill: skill})
	}
	return d
}

// A newly uploaded path has a verified copy before it has a document position.
func unplacedPathUpload(file app.DraftAttachment) bool {
	return file.Attachment.Kind == domain.PathAttachmentResource && file.UploadApproved && file.InlinePosition == 0
}

func (d draftContent) equal(other draftContent) bool {
	return d.text == other.text && slices.Equal(d.refs, other.refs)
}

type DraftDocument struct {
	Doc      *richtext.Document
	GoalChip func() *richtext.Attachment
	last     draftContent
	version  uint64
	departed string
}

// Depart clears the editor while the original input remains in the model and
// its pending submission. A new edit can now start an independent draft.
func (d *DraftDocument) Depart(operation string) {
	d.Doc.SetText(richtext.NewText(""))
	d.version, d.departed = d.Doc.Version(), operation
}

func (d *DraftDocument) departure(m *app.Model) domain.Submission {
	if m.UncertainSubmissions[d.departed] {
		return domain.Submission{}
	}
	return m.Pending[d.departed]
}

func (d *DraftDocument) VisibleText(m *app.Model, key string) string {
	text := m.Drafts[key]
	if in := d.departure(m); in.OperationID != "" && text == in.Text {
		return ""
	}
	return text
}

func (d *DraftDocument) VisibleAttachments(m *app.Model, key string) []app.DraftAttachment {
	files := m.Attachments[key]
	if in := d.departure(m); len(in.Attachments) != 0 {
		files = slices.DeleteFunc(slices.Clone(files), func(file app.DraftAttachment) bool {
			return slices.Contains(in.Attachments, file.Attachment)
		})
	}
	return files
}

const GoalCommandText = "/goal"

type goalDraftTag struct{}

func GoalTagRange(text string) (int, int, bool) {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	_, command := domain.GoalCommand(trimmed)
	if !command || len(trimmed) == len(GoalCommandText) {
		return 0, 0, false
	}
	start := utf8.RuneCountInString(text[:len(text)-len(trimmed)])
	return start, start + len(GoalCommandText), true
}

func (d *DraftDocument) goalTag() richtext.Attributes {
	chip := d.GoalChip()
	chip.Data = goalDraftTag{}
	return richtext.Attributes{Attachment: chip}
}

func (d *DraftDocument) InsertGoal(start, end int) {
	runes := []rune(d.Doc.Text().String())
	d.Doc.Edit(func(edit *richtext.Edit) {
		edit.ReplaceString(start, end, string(richtext.ObjectReplacement), d.goalTag())
		if end == len(runes) || !unicode.IsSpace(runes[end]) {
			edit.ReplaceString(start+1, start+1, " ", richtext.Attributes{})
		}
		edit.Select(start+2, start+2)
	})
}

func (d *DraftDocument) Sync(m *app.Model, key string, chip func(DraftReference) *richtext.Attachment) {
	current := readDraft(m, key)
	in := d.departure(m)
	restore := d.departed != "" && in.OperationID == ""
	if restore {
		d.departed = ""
	}
	if !restore && d.Doc != nil && current.equal(d.last) {
		return
	}
	input := domain.Item{Text: d.VisibleText(m, key)}
	byID := make(map[string]DraftReference, len(current.refs))
	for _, ref := range current.refs {
		if slices.Contains(in.Skills, ref.Skill.Reference) || slices.Contains(in.Attachments, ref.File.Attachment) {
			continue
		}
		byID[ref.ID()] = ref
		if ref.Skill.Reference.ID != "" {
			input.Skills = append(input.Skills, ref.Skill.Reference)
			input.ReferencePositions = append(input.ReferencePositions, domain.ReferencePosition{SkillID: ref.Skill.Reference.ID, Position: ref.Skill.InlinePosition})
		} else {
			input.Attachments = append(input.Attachments, ref.File.Attachment)
			input.ReferencePositions = append(input.ReferencePositions, domain.ReferencePosition{AttachmentID: ref.File.Attachment.ID, Position: ref.File.InlinePosition})
		}
	}
	text := richtext.NewText("")
	if start, end, goal := GoalTagRange(input.Text); goal && len(byID) == 0 && d.GoalChip != nil {
		runes := []rune(input.Text)
		text = text.Append(string(runes[:start]), richtext.Attributes{}).
			Append(string(richtext.ObjectReplacement), d.goalTag()).
			Append(string(runes[end:]), richtext.Attributes{})
	} else {
		for _, run := range messageRuns(input, false) {
			switch {
			case run.Skill != nil:
				text = text.Append(string(richtext.ObjectReplacement), richtext.Attributes{Attachment: chip(byID["skill:"+run.Skill.ID])})
			case run.Attachment != nil:
				text = text.Append(string(richtext.ObjectReplacement), richtext.Attributes{Attachment: chip(byID["file:"+run.Attachment.ID])})
			default:
				text = text.Append(run.Text, richtext.Attributes{})
			}
		}
	}
	if d.Doc == nil {
		d.Doc = richtext.NewDocument(text)
		d.Doc.SetSelection(text.Len(), text.Len())
	} else {
		d.Doc.SetText(text)
	}
	d.last, d.version = current, d.Doc.Version()
}

// Publish commits only edits from the current binding. In-progress uploads
// and ordinary attachments belong to the model, outside the text document.
func (d *DraftDocument) Publish(m *app.Model, key string) bool {
	if d.Doc == nil || d.version == d.Doc.Version() || !readDraft(m, key).equal(d.last) {
		return false
	}
	var plain strings.Builder
	var files []app.DraftAttachment
	var skills []app.DraftSkill
	for _, file := range d.VisibleAttachments(m, key) {
		if !file.Attachment.InlineReference() || !file.Ready || unplacedPathUpload(file) {
			files = append(files, file)
		}
	}
	text := d.Doc.Text()
	legacy := m.WorkerMinor > 0 && m.WorkerMinor < 12 && len(m.DraftReferencePositions(key)) == 0
	extraRunes := 0
	for at, run := range text.Runs(0, text.Len()) {
		if tag := run.Attributes.Attachment; tag != nil {
			if _, goal := tag.Data.(goalDraftTag); goal {
				plain.WriteString(GoalCommandText)
				extraRunes += len(GoalCommandText) - 1
				continue
			}
			ref, ok := tag.Data.(DraftReference)
			if !ok {
				continue
			}
			position := at + 1 + extraRunes
			if legacy {
				position = 0
			}
			if ref.Skill.Reference.ID != "" {
				ref.Skill.InlinePosition = position
				skills = append(skills, ref.Skill)
			} else {
				ref.File.InlinePosition = position
				files = append(files, ref.File)
			}
		} else {
			plain.WriteString(strings.ReplaceAll(run.Text, string(richtext.ObjectReplacement), ""))
		}
	}
	m.Drafts[key], m.Attachments[key], m.DraftSkills[key] = plain.String(), files, skills
	d.last, d.version = readDraft(m, key), d.Doc.Version()
	d.departed = ""
	return true
}

// AdoptPathUploads inserts completed uploads as native undoable edits. An upload
// belonging to another workspace waits for that workspace's editor.
func (d *DraftDocument) AdoptPathUploads(m *app.Model, key, device, workspace string, chip func(DraftReference) *richtext.Attachment) bool {
	if d.Doc == nil || d.Doc.Composing() || !readDraft(m, key).equal(d.last) {
		return false
	}
	matching := func(file app.DraftAttachment) bool {
		return file.Ready && unplacedPathUpload(file) && file.DeviceID == device && file.WorkspaceID == workspace
	}
	if !slices.ContainsFunc(m.Attachments[key], matching) {
		return false
	}
	d.Publish(m, key)
	var ready []app.DraftAttachment
	files := m.Attachments[key]
	m.Attachments[key] = slices.DeleteFunc(files, func(file app.DraftAttachment) bool {
		if matching(file) {
			ready = append(ready, file)
			return true
		}
		return false
	})
	for _, file := range ready {
		file.Transferred = 0
		_, caret := d.Doc.Selection()
		d.Insert(caret, caret, chip(DraftReference{File: file}))
		d.Publish(m, key)
	}
	return true
}

func (d *DraftDocument) Contains(ref DraftReference) bool {
	return slices.ContainsFunc(d.last.refs, func(old DraftReference) bool { return old.ID() == ref.ID() })
}

func (d *DraftDocument) Insert(start, end int, chip *richtext.Attachment) {
	d.Doc.Edit(func(edit *richtext.Edit) {
		edit.ReplaceString(start, end, string(richtext.ObjectReplacement), richtext.Attributes{Attachment: chip})
		edit.Select(start+1, start+1)
	})
}

// InsertAttachment adds an inline reference on the active side of the selection,
// preserving the selected text and the document's undo history.
func (d *DraftDocument) InsertAttachment(m *app.Model, key string, ref DraftReference, chip func(DraftReference) *richtext.Attachment) bool {
	if d.Doc == nil || d.Doc.Composing() || !readDraft(m, key).equal(d.last) || d.Contains(ref) {
		return false
	}
	_, caret := d.Doc.Selection()
	d.Insert(caret, caret, chip(ref))
	return d.Publish(m, key)
}

// ReplaceAttachment updates one live inline reference with an immutable
// replacement. It keeps the cursor and other draft content in the same
// native document so the change can be undone.
func (d *DraftDocument) ReplaceAttachment(m *app.Model, key string, original, replacement domain.Attachment, chip func(DraftReference) *richtext.Attachment) bool {
	if d.Doc == nil || d.Doc.Composing() || !readDraft(m, key).equal(d.last) {
		return false
	}
	text := d.Doc.Text()
	for at, run := range text.Runs(0, text.Len()) {
		old := run.Attributes.Attachment
		if old == nil {
			continue
		}
		ref, ok := old.Data.(DraftReference)
		if !ok || ref.File.Attachment != original {
			continue
		}
		ref.File.Attachment = replacement
		attributes := run.Attributes
		attributes.Attachment = chip(ref)
		anchor, caret := d.Doc.Selection()
		d.Doc.Edit(func(edit *richtext.Edit) {
			edit.ReplaceString(at, at+1, string(richtext.ObjectReplacement), attributes)
			edit.Select(anchor, caret)
		})
		return d.Publish(m, key)
	}
	return false
}

type ReferenceQuery struct {
	Start, End int
	Marker     rune
	Needle     string
}

// ReadReferenceQuery detects a trigger at the current caret. Whitespace and
// existing reference objects delimit an input token, while email @ and URLs
// remain ordinary text.
func ReadReferenceQuery(text string, caret int, skills bool) (ReferenceQuery, bool) {
	runes := []rune(text)
	if caret <= 0 || caret > len(runes) {
		return ReferenceQuery{}, false
	}
	start := caret
	for start > 0 && !unicode.IsSpace(runes[start-1]) && runes[start-1] != richtext.ObjectReplacement {
		start--
	}
	if start == caret || (runes[start] != '@' && !(skills && runes[start] == '/')) {
		return ReferenceQuery{}, false
	}
	return ReferenceQuery{Start: start, End: caret, Marker: runes[start], Needle: string(runes[start+1 : caret])}, true
}

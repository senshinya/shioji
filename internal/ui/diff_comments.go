package ui

import (
	"context"
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
	"math"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"
	"time"
)

var diffCommentIcon = svg("M17.5 9.5c0 3.6-3.1 6.5-7 6.5H6l-3.5 1.5v-8C2.5 5.9 5.6 3 9.5 3h1c3.9 0 7 2.9 7 6.5Z")

// Header, version, two input lines and actions, including gaps and border.
const diffCommentEditorPanelHeight = 20 + 18 + 44 + 28 + 3*4 + 2*6 + 2

type diffCommentKey struct {
	Comparison domain.DiffComparison
	Path       string
}
type diffCommentDraft struct {
	Versions domain.FileVersions
	Closing  bool

	Selection           editor.DiffTextSelection
	Snapshot            domain.Attachment
	Text, Error         string
	Open, Saving, Focus bool
	Retaining           bool
	Editing             domain.Attachment
}
type diffCommentInsertion struct {
	key, token, session, epoch string
	ref                        app.DraftAttachment
	draft                      *diffCommentDraft
}
type diffCommentNote struct {
	Ref    app.DraftAttachment
	Record domain.DiffCommentRecord
}
type diffCommentResource struct {
	Record  domain.DiffCommentRecord
	Loading bool
	Error   string
}

func (s *changesState) commentDraft() *diffCommentDraft {
	if s.CommentDrafts == nil {
		s.CommentDrafts = map[diffCommentKey]*diffCommentDraft{}
	}
	return s.CommentDrafts[diffCommentKey{s.Comparison, s.Path}]
}
func (v *View) configureDiffComments(c *n.Context, t *n.Theme, state *changesState) {
	if !state.Diff.TextSelection {
		return
	}
	state.Diff.FullWidthAnnotations = true
	state.Diff.OnTextSelection = func(editor.DiffTextSelection) { state.SuppressSelectionAction = false }
	state.Diff.Annotations = nil
	draft := state.commentDraft()
	clock := n.Column(c.Key("diff-comment-height")).Absolute().Size(0, 0).PassThrough()
	target := float32(0)
	if draft != nil && draft.Open {
		target = diffCommentEditorPanelHeight + 16
		if draft.Error != "" {
			target += 24
		}
	}
	height := clock.AnimateTo("height", target, motionAnimation(c, 220*time.Millisecond))
	if draft != nil && draft.Closing && height == 0 {
		draft.Closing = false
		state.PinnedSnapshot = false
		state.Updated = time.Time{}
	}
	if draft != nil && len(draft.Selection.Fragments) > 0 {
		last := draft.Selection.Fragments[len(draft.Selection.Fragments)-1]
		if height > 0 {
			state.Diff.Annotations = append(state.Diff.Annotations, editor.DiffAnnotation{Side: last.Side, Line: last.EndLine - 1, Height: height, Key: "editor"})
		}
	}
	present := map[string]bool{}
	for _, ref := range v.Model.Attachments[v.Model.DraftKey()] {
		if ref.Attachment.Kind != domain.DiffCommentResource || ref.DeviceID != v.referenceDeviceID() || ref.WorkspaceID != v.Model.WorkspaceID {
			continue
		}
		if draft != nil && draft.Open && draft.Editing.ID == ref.Attachment.ID {
			continue
		}
		resource := v.loadCommentResource(ref)
		if resource.Loading || resource.Error != "" || resource.Record.Content.Comparison != state.Comparison || resource.Record.Content.Path != state.Path || resource.Record.Snapshot.SHA256 != state.Versions.Snapshot.SHA256 {
			continue
		}
		present[ref.Attachment.ID] = true
		if !slices.ContainsFunc(state.CommentNotes, func(note diffCommentNote) bool { return note.Ref.Attachment.ID == ref.Attachment.ID }) {
			state.CommentNotes = append(state.CommentNotes, diffCommentNote{ref, resource.Record})
		}
	}
	keep := state.CommentNotes[:0]
	for _, note := range state.CommentNotes {
		if note.Record.Content.Comparison != state.Comparison || note.Record.Content.Path != state.Path {
			keep = append(keep, note)
			continue
		}
		parts := note.Record.Content.Fragments
		if len(parts) == 0 {
			continue
		}
		clock := n.Column(c.Key("comment-note-height:"+note.Ref.Attachment.ID)).Absolute().Size(0, 0).PassThrough()
		initialized := n.Local(clock, "initialized", func() bool { return false })
		if !*initialized {
			clock.Jump("height", 0)
			*initialized = true
		}
		target := float32(0)
		if present[note.Ref.Attachment.ID] {
			target = 32
		}
		height := clock.AnimateTo("height", target, motionAnimation(c, 220*time.Millisecond))
		if height <= 0 && !present[note.Ref.Attachment.ID] {
			continue
		}
		keep = append(keep, note)
		last := parts[len(parts)-1]
		side := editor.DiffNew
		if last.Side == "base" {
			side = editor.DiffOld
		}
		state.Diff.Annotations = append(state.Diff.Annotations, editor.DiffAnnotation{Side: side, Line: last.EndLine - 1, Height: height, Key: note.Ref.Attachment.ID})
	}
	state.CommentNotes = keep

	state.Diff.RenderAnnotation = func(c *n.Context, annotation editor.DiffAnnotation) {
		if annotation.Key == "editor" {
			v.diffCommentEditor(c, t, state, draft)
			return
		}
		for _, entry := range state.CommentNotes {
			ref := entry.Ref
			if ref.Attachment.ID != annotation.Key {
				continue
			}
			resource := &diffCommentResource{Record: entry.Record}

			// Icon-only actions have no text baseline. Center their hit slots
			// independently so they cannot push the inline comment text down.
			note := n.Row(c).Fill().Clip().Padding(5, 12).Gap(6).AlignItems(n.Center).Background(t.Background).Transition(fadeMotion(c, 140*time.Millisecond))
			note.Children(func() {
				font := n.Font{Family: t.Font, Size: 12, Weight: 400}
				inlineIconText(c, diffCommentIcon, resource.Record.Content.Comment, font, 13, 6, t.TextMuted).Grow(1).MinWidth(0).TextColor(t.Text).Transform(n.Translation(0, inlineCenterOffset(resource.Record.Content.Comment, font))).Ellipsis("…").Tooltip(resource.Record.Content.Comment)
				if iconButton(c, queueEditIcon, "编辑评论").Size(22, 22).Clicked() {
					v.editDiffComment(state, ref, resource.Record)
				}
				if iconButton(c, closeIcon, "移除评论").Size(22, 22).Clicked() {
					v.removeCommentReference(c, ref.Attachment.ID)
				}
			})
		}
	}
}
func (v *View) diffSelectionAction(c *n.Context, t *n.Theme, state *changesState, viewer n.Element) {
	if state.Locating {
		if target, ok := state.Diff.TextSelectionScrollTarget(); ok {
			clock := n.Column(c.Key(fmt.Sprint("locate-comment-", state.LocateSequence))).Absolute().Size(0, 0).PassThrough()
			initialized := n.Local(clock, "initialized", func() bool { return false })
			if !*initialized {
				clock.Jump("scroll", float32(state.Diff.ScrollY()))
				*initialized = true
				c.AnimationFrame()
			}
			shown := clock.AnimateTo("scroll", float32(target), motionAnimation(c, 220*time.Millisecond))
			state.Diff.SetScrollY(float64(shown))
			if math.Abs(float64(shown)-target) < .5 {
				state.Locating = false
			} else {
				c.AnimationFrame()
			}
		} else {
			c.AnimationFrame()
		}
	}
	selection, ok := state.Diff.SelectedText()
	draft := state.commentDraft()
	if !ok || selection.Dragging || state.SuppressSelectionAction || draft != nil && draft.Open || state.Versions.Snapshot.ID == "" {
		return
	}
	bounds, area := selection.Bounds, viewer.Bounds()
	if bounds.H <= 0 || bounds.Y < area.Y || bounds.Y+bounds.H > area.Y+area.H {
		return
	}
	n.Overlay(c.Key("diff-selection-action"), func() {
		x := min(max(bounds.X+bounds.W+6, area.X+8), area.X+area.W-36)
		y := bounds.Y + bounds.H + 6
		if y+28 > area.Y+area.H {
			y = bounds.Y - 34
		}
		button := iconButton(c, diffCommentIcon, "添加代码评论").Absolute().Left(x).Top(y).Size(28, 28).Background(raised(t)).Border(1, t.Border).Radius(6).Shadow(0, 2, 8, 0, n.RGBA(0, 0, 0, .10)).Transition(fadeMotion(c, 140*time.Millisecond))
		if button.Clicked() {
			selection.Fragments = slices.Clone(selection.Fragments)
			key := diffCommentKey{state.Comparison, state.Path}
			draft := &diffCommentDraft{Selection: selection, Snapshot: state.Versions.Snapshot, Versions: state.Versions, Open: true, Focus: true}
			state.CommentDrafts[key] = draft
			v.retainCommentDraft(state, key, draft)
		}
	})
}
func (v *View) diffCommentEditor(c *n.Context, t *n.Theme, state *changesState, draft *diffCommentDraft) {
	if draft == nil {
		return
	}
	panelHeight := float32(diffCommentEditorPanelHeight)
	if draft.Error != "" {
		panelHeight += 24
	}
	n.Column(c).Fill().Padding(8, 12).Clip().Children(func() {
		panel := n.Column(c).FillWidth().Height(panelHeight).Shrink(0).Padding(6, 12).Gap(4).Clip().Radius(8).Background(codeSurface(t)).Border(1, t.Border).Label("代码评论编辑器")
		panel.Children(func() {
			name := state.Path
			if len(draft.Selection.Fragments) > 0 {
				part := draft.Selection.Fragments[0]
				name += fmt.Sprintf(":%d", part.StartLine)
			}
			inlineIconText(c, diffCommentIcon, name, n.Font{Family: t.Font, Size: 11}, 13, 6, t.TextMuted).Height(20)
			endpoint := state.Comparison.Target
			side := "目标侧"
			if draft.Selection.Fragments[0].Side == editor.DiffOld {
				endpoint = state.Comparison.Base
				side = "基准侧"
			}
			origin := side + " · " + diffEndpointShortDescription(endpoint)
			for _, part := range draft.Selection.Fragments {
				if part.Side != draft.Selection.Fragments[0].Side {
					origin = "基准与目标 · " + diffEndpointName(state.Comparison.Base) + " → " + diffEndpointName(state.Comparison.Target)
					break
				}
			}
			n.Text(c, origin).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).SingleLine().Ellipsis("…").FillWidth()
			field := n.TextAreaBase(c.Key("comment-text"), &draft.Text).FillWidth().Height(44).Lines(2, 2).FontSize(13).FixedLineHeight(20).Label("代码评论内容").Placeholder("添加评论…").Disabled(draft.Saving).FocusRing(false)
			if draft.Focus {
				field.Focus()
				draft.Focus = false
			}
			field.OnTextKey(func(key n.TextKey) bool {
				if key.Key == n.KeyEscape && !draft.Saving {
					draft.Open = false
					draft.Closing = true
					draft.Text = ""
					return true
				}
				if key.Key == n.KeyEnter && key.Mods == n.Cmd {
					v.saveDiffComment(c, state, draft)
					return true
				}
				return false
			})
			field.Changed()
			if draft.Error != "" {
				n.Text(c, draft.Error).FontSize(11).FixedLineHeight(20).TextColor(t.Danger)
			}
			n.Row(c).FillWidth().Height(28).Gap(6).AlignItems(n.Center).Justify(n.End).Children(func() {
				if ghost(c, "取消").Label("取消评论").Height(28).Disabled(draft.Saving).Clicked() {
					draft.Open = false
					draft.Closing = true
					draft.Text = ""
					draft.Error = ""
				}
				label := "加入消息"
				if draft.Editing.ID != "" {
					label = "保存评论"
				}
				if ghost(c, label).Height(28).PaddingX(8).Background(t.Text).TextColor(t.Background).Radius(5).Disabled(draft.Saving || draft.Retaining || strings.TrimSpace(draft.Text) == "" || v.Controller == nil).Clicked() {
					v.saveDiffComment(c, state, draft)
				}
			})
		})

	})
}

func (v *View) retainCommentDraft(state *changesState, key diffCommentKey, draft *diffCommentDraft) {
	if v.Controller == nil {
		return
	}
	draft.Retaining = true
	_, q := v.gitQuery()
	epoch := v.Model.ConnectionEpoch
	if !v.do(app.Job{Method: "diff.snapshot.retain", Input: protocol.DiffSnapshotInput{GitQuery: q, Snapshot: draft.Snapshot}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if m.ConnectionEpoch != epoch || state.CommentDrafts[key] != draft {
			return
		}
		draft.Retaining = false
		if err != nil {
			draft.Error = err.Error()
		}
	}}) {
		draft.Retaining = false
		draft.Error = "连接不可用，请重试"
	}
}

func (v *View) saveDiffComment(c *n.Context, state *changesState, draft *diffCommentDraft) {
	if draft.Saving || draft.Retaining || strings.TrimSpace(draft.Text) == "" || v.Controller == nil {
		return
	}
	count := 0
	for _, ref := range v.Model.Attachments[v.Model.DraftKey()] {
		if ref.Attachment.Kind == domain.DiffCommentResource {
			count++
		}
	}
	if count >= 8 && draft.Editing.ID == "" {
		draft.Error = "最多添加 8 条代码评论"
		return
	}
	key, session, epoch := v.Model.DraftKey(), v.Model.SessionID, v.Model.ConnectionEpoch
	token := v.attachmentPreparationKey(key)
	workspace := v.Model.WorkspaceID
	comparison, path := state.Comparison, state.Path
	v.collectBindings()
	_, q := v.gitQuery()
	in := protocol.DiffCommentInput{GitQuery: q, Snapshot: draft.Snapshot, Comment: draft.Text}
	for _, part := range draft.Selection.Fragments {
		side := "target"
		if part.Side == editor.DiffOld {
			side = "base"
		}
		in.Fragments = append(in.Fragments, domain.CodeSelection{Side: side, Start: part.Start, End: part.End})
	}
	draft.Saving = true
	draft.Error = ""
	if !v.do(app.Job{Method: "diff.comment", Input: in, Apply: func(m *app.Model, data json.RawMessage, err error) {
		draft.Saving = false
		if err != nil {
			draft.Error = err.Error()
			return
		}
		var attachment domain.Attachment
		if err = json.Unmarshal(data, &attachment); err != nil {
			draft.Error = err.Error()
			return
		}
		if m.SessionID != session || m.ConnectionEpoch != epoch || token != v.attachmentPreparationKey(key) {
			draft.Error = "已保留评论，请返回原消息后重试"
			return
		}
		ref := app.DraftAttachment{Attachment: attachment, Ready: true, WorkspaceID: workspace, DeviceID: v.referenceDeviceID()}
		content := domain.CodeCommentContent{Comparison: comparison, Path: path, Comment: in.Comment, FileSnapshot: in.Snapshot.SHA256}
		for i, part := range draft.Selection.Fragments {
			raw := in.Fragments[i]
			raw.StartLine, raw.EndLine, raw.Text = part.StartLine, part.EndLine, part.Text
			content.Fragments = append(content.Fragments, raw)
		}
		if v.commentResources == nil {
			v.commentResources = map[string]*diffCommentResource{}
		}
		v.commentResources[v.commentResourceKey(ref)] = &diffCommentResource{Record: domain.DiffCommentRecord{Content: content, Snapshot: in.Snapshot}}

		v.pendingCommentInsertions = append(v.pendingCommentInsertions, diffCommentInsertion{key: key, token: token, session: session, epoch: epoch, ref: ref, draft: draft})

	}}) {
		draft.Saving = false
	}
}
func (v *View) removeCommentReference(c *n.Context, id string) {
	key := v.Model.DraftKey()
	binding := v.composerDocument(c, key)
	for at, run := range binding.doc.Text().Runs(0, binding.doc.Text().Len()) {
		if run.Attributes.Attachment == nil {
			continue
		}
		ref, ok := run.Attributes.Attachment.Data.(composerReference)
		if ok && ref.file.Attachment.ID == id {
			binding.doc.Edit(func(edit *richtext.Edit) { edit.ReplaceString(at, at+1, "", richtext.Attributes{}) })
			v.publishComposer(key, binding)
			return
		}
	}
}
func (v *View) editDiffComment(state *changesState, ref app.DraftAttachment, record domain.DiffCommentRecord) {
	selection := editor.DiffTextSelection{}
	for _, part := range record.Content.Fragments {
		side := editor.DiffNew
		if part.Side == "base" {
			side = editor.DiffOld
		}
		selection.Fragments = append(selection.Fragments, editor.DiffTextFragment{Side: side, Start: part.Start, End: part.End, StartLine: part.StartLine, EndLine: part.EndLine, Text: part.Text})
	}
	state.CommentDrafts[diffCommentKey{state.Comparison, state.Path}] = &diffCommentDraft{Selection: selection, Snapshot: record.Snapshot, Versions: state.Versions, Text: record.Content.Comment, Open: true, Focus: true, Editing: ref.Attachment}
}
func (v *View) commentResourceKey(ref app.DraftAttachment) string {
	return ref.DeviceID + ":" + ref.WorkspaceID + ":" + ref.Attachment.ID
}
func (v *View) loadCommentResource(ref app.DraftAttachment) *diffCommentResource {
	if v.commentResources == nil {
		v.commentResources = map[string]*diffCommentResource{}
	}
	key := v.commentResourceKey(ref)
	if existing := v.commentResources[key]; existing != nil {
		return existing
	}
	resource := &diffCommentResource{Loading: true}
	v.commentResources[key] = resource
	if len(v.commentResources) > 64 {
		for old := range v.commentResources {
			if old != key {
				delete(v.commentResources, old)
				break
			}
		}
	}
	if v.Controller == nil || v.StatePath == "" {
		resource.Loading = false
		resource.Error = "评论预览不可用"
		return resource
	}
	target, err := v.Model.AttachmentTarget(v.ControllerDirectory(), ref.WorkspaceID)
	if err != nil {
		resource.Loading = false
		resource.Error = err.Error()
		return resource
	}
	cache, store := v.displayCache(), v.staging()
	v.Controller.AttachmentTask(func() app.Update {
		var record domain.DiffCommentRecord
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := cache.Read(ctx, target, store, ref.Attachment, func(path string) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &record)
		})
		return func(*app.Model) {
			resource.Loading = false
			if err != nil {
				resource.Error = err.Error()
				return
			}
			resource.Record = record
		}
	})
	return resource
}
func (v *View) diffCommentPopover(c *n.Context, t *n.Theme, ref app.DraftAttachment, rect n.Rect) {
	resource := v.loadCommentResource(ref)
	n.Overlay(c.Key("comment-preview:"+ref.Attachment.ID), func() {
		width, height := c.Size()
		panel := n.Column(c).Absolute().Left(max(12, min(rect.X, width-312))).Top(max(12, rect.Y-220)).Width(300).MaxHeight(min(300, height-24)).Padding(10).Gap(4).Radius(8).Background(raised(t)).Border(1, t.Border).Shadow(0, 4, 16, 0, n.RGBA(0, 0, 0, .10)).PassThrough().Transition(fadeMotion(c, 140*time.Millisecond)).Label("代码评论预览")
		panel.Children(func() {
			inlineIconText(c, diffCommentIcon, ref.Attachment.Name, n.Font{Family: t.Font, Size: 11}, 13, 6, t.TextMuted)
			if resource.Loading {
				n.Text(c, "读取评论…").FontSize(12).TextColor(t.TextMuted)
				return
			}
			if resource.Error != "" {
				n.Text(c, resource.Error).FontSize(12).TextColor(t.TextMuted)
				return
			}
			content := resource.Record.Content
			n.Text(c, diffEndpointShortDescription(content.Comparison.Base)+" → "+diffEndpointShortDescription(content.Comparison.Target)).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).FillWidth().MaxHeight(54).Clip()
			side := "目标侧"
			if len(content.Fragments) > 0 && content.Fragments[0].Side == "base" {
				side = "基准侧"
			}
			for _, part := range content.Fragments {
				if part.Side != content.Fragments[0].Side {
					side = "基准侧与目标侧"
					break
				}
			}
			n.Text(c, side).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			n.Text(c, content.Comment).FontSize(12).FixedLineHeight(20).FillWidth().MaxHeight(60).Clip()
			texts := []string{}
			for _, part := range content.Fragments {
				texts = append(texts, part.Text)
			}
			n.Text(c, strings.Join(texts, "\n")).Font("Menlo").FontSize(11).FixedLineHeight(18).FillWidth().MaxHeight(72).Padding(6, 8).Radius(4).Background(codeSurface(t)).Clip()
		})
	})
}
func (v *View) diffCommentInputHover(c *n.Context, t *n.Theme, input composerInput) {
	x, y, over := input.PointerPosition()
	if !over {
		return
	}
	box := input.Bounds()
	x += box.X
	y += box.Y
	for _, run := range input.binding.doc.Text().Runs(0, input.binding.doc.Text().Len()) {
		attachment := run.Attributes.Attachment
		if attachment == nil {
			continue
		}
		ref, ok := attachment.Data.(composerReference)
		if !ok || ref.file.Attachment.Kind != domain.DiffCommentResource {
			continue
		}
		rect, known := v.commentTagBounds[attachment]
		if known && x >= rect.X && x <= rect.X+rect.W && y >= rect.Y && y <= rect.Y+rect.H {
			v.diffCommentPopover(c, t, ref.file, rect)
			return
		}
	}
}
func (v *View) locateDiffComment(ref app.DraftAttachment) {
	if v.Controller == nil || v.StatePath == "" || ref.WorkspaceID != v.Model.WorkspaceID || ref.DeviceID != v.referenceDeviceID() {
		return
	}
	v.diffLocateSequence++
	sequence, session, epoch := v.diffLocateSequence, v.Model.SessionID, v.Model.ConnectionEpoch
	target, err := v.Model.AttachmentTarget(v.ControllerDirectory(), ref.WorkspaceID)
	if err != nil {
		v.Model.Error = err.Error()
		return
	}
	cache, store := v.displayCache(), v.staging()
	v.Controller.AttachmentTask(func() app.Update {
		var record domain.DiffCommentRecord
		var versions domain.FileVersions
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		read := func(a domain.Attachment, value any) error {
			return cache.Read(ctx, target, store, a, func(path string) error {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return json.Unmarshal(data, value)
			})
		}
		err := read(ref.Attachment, &record)
		if err == nil {
			err = read(record.Snapshot, &versions)
		}
		return func(m *app.Model) {
			if sequence != v.diffLocateSequence || m.SessionID != session || m.ConnectionEpoch != epoch {
				return
			}
			if err != nil {
				m.Error = err.Error()
				return
			}
			state := v.changesFor(session)
			state.ComparisonSequence++
			state.FileSequence++
			state.PendingComparison = nil
			versions.Snapshot = record.Snapshot
			state.Path = versions.Path
			state.Comparison = versions.Comparison
			state.installDiff(versions)
			state.PinnedSnapshot = true
			state.Updated = time.Now()
			state.endLoad(true)
			state.Error = ""
			state.Files = []domain.ChangedFile{{Path: versions.Path}}
			state.Diff.ExpandAll()
			parts := make([]editor.DiffTextFragment, 0, len(record.Content.Fragments))
			for _, part := range record.Content.Fragments {
				side := editor.DiffNew
				if part.Side == "base" {
					side = editor.DiffOld
				}
				parts = append(parts, editor.DiffTextFragment{Side: side, Start: part.Start, End: part.End})
			}
			state.Diff.SetTextSelectionFragments(parts)
			state.Locating = true
			state.LocateSequence = sequence
			state.SuppressSelectionAction = true
			m.Inspectors[session] = true
			m.InspectorLayout.Selected = "changes"
			for i := range m.InspectorLayout.Groups {
				if slices.Contains(m.InspectorLayout.Groups[i].Views, "changes") {
					m.InspectorLayout.Groups[i].Selected = "changes"
				}
			}
			for i := range state.WorkGroups {
				if slices.Contains(state.WorkGroups[i].Views, "changes") {
					state.WorkGroups[i].Selected = "changes"
				}
			}

		}
	})
}

func (v *View) flushDiffCommentInsertions(c *n.Context) {
	pending := v.pendingCommentInsertions
	v.pendingCommentInsertions = nil
	for _, in := range pending {
		if v.Model.SessionID != in.session || v.Model.ConnectionEpoch != in.epoch || v.attachmentPreparationKey(in.key) != in.token {
			in.draft.Error = "已保留评论，请返回原消息后重试"
			continue
		}
		binding := v.composerDocument(c, in.key)
		text := binding.doc.Text()
		anchor, caret := binding.doc.Selection()
		replacement := -1
		if in.draft.Editing.ID != "" {
			for at, run := range text.Runs(0, text.Len()) {
				if run.Attributes.Attachment != nil {
					ref, ok := run.Attributes.Attachment.Data.(composerReference)
					if ok && ref.file.Attachment.ID == in.draft.Editing.ID {
						replacement = at
						break
					}
				}
			}
		}
		if v.commentTagEntries == nil {
			v.commentTagEntries = map[string]time.Time{}
		}
		v.commentTagEntries[in.ref.Attachment.ID] = c.Now()
		attachment := v.referenceAttachment(c, composerReference{file: in.ref})
		binding.doc.Edit(func(edit *richtext.Edit) {
			if replacement >= 0 {
				edit.ReplaceString(replacement, replacement+1, string(richtext.ObjectReplacement), richtext.Attributes{Attachment: attachment})
				edit.Select(anchor, caret)
				return
			}
			// Adding a comment inserts at the caret without consuming an ordinary
			// text selection that was left in the independent composer.
			edit.ReplaceString(caret, caret, string(richtext.ObjectReplacement), richtext.Attributes{Attachment: attachment})
			edit.Select(caret+1, caret+1)
		})
		v.publishComposer(in.key, binding)
		v.changesFor(in.session).SuppressSelectionAction = true
		in.draft.Open = false
		in.draft.Closing = true
		in.draft.Text = ""
		in.draft.Error = ""
		in.draft.Editing = domain.Attachment{}
	}
}

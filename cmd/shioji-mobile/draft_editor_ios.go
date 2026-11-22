package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

type mobileDraftEditor struct {
	mobileclient.DraftDocument
	model               *app.Model
	target              mobileDraftTarget
	focused             bool
	focusRequested      bool
	sendScroll          mobileSendScroll
	query               mobileclient.ReferenceQuery
	dismissed           mobileclient.ReferenceQuery
	queryAt             time.Time
	search              *mobileRequest
	insertion           *mobileRequest
	choices             []mobileReferenceChoice
	skillCatalog        []domain.Skill
	active              int
	referenceList       ui.ListState
	referenceListHeight float32
	connection          string
	chipColors          colors
	openComment         func(mobileclient.DraftReference)
	openFile            func(mobileclient.DraftReference)
	attachmentMenuOpen  bool
	preparingAttachment bool
	attachmentError     string
	rememberPathUpload  bool
}

type mobileReferenceChoice struct {
	file  domain.WorkspaceFile
	skill domain.Skill
}

func (r mobileReferenceChoice) display() (name, role string, symbol *ui.SVG) {
	if r.skill.ID != "" {
		return r.skill.Name, "引用 Skill ", terminalSymbol
	}
	return filepath.Base(r.file.Path), "引用文件 ", filesSymbol
}

func (r mobileReferenceChoice) detail() string {
	if r.skill.ID != "" {
		return r.skill.Description
	}
	return filepath.ToSlash(filepath.Join(filepath.Base(r.file.Root), filepath.Dir(r.file.Path)))
}

func (a *mobileApp) bindDraftEditor(c *ui.Context, target mobileDraftTarget) *mobileDraftEditor {
	bindingKey := "draft-editor:" + target.device + ":" + target.key
	if target.queueEdit != "" {
		bindingKey += ":queue"
	}
	slot := ui.Local(c.Root(), bindingKey, func() *mobileDraftEditor { return nil })
	if *slot == nil || (*slot).target != target || (*slot).model != a.model {
		*slot = &mobileDraftEditor{model: a.model, target: target}
	}
	b := *slot
	b.chipColors = palette(c)
	b.chipColors.canvas = b.chipColors.inset
	scale := c.Preferences().TextScale
	b.GoalChip = func() *richtext.Attachment { return b.draftChip(scale, mobileclient.GoalCommandText, goalSymbol) }
	chip := b.chipMaker(c)
	b.Sync(a.model, target.key, chip)
	if b.AdoptPathUploads(a.model, target.key, target.device, target.workspace, chip) {
		a.save()
	}
	return b
}

func (b *mobileDraftEditor) chipMaker(c *ui.Context) func(mobileclient.DraftReference) *richtext.Attachment {
	scale := c.Preferences().TextScale
	return func(ref mobileclient.DraftReference) *richtext.Attachment {
		name, symbol := ref.File.Attachment.Name, filesSymbol
		if ref.Skill.Reference.ID != "" {
			name, symbol = ref.Skill.Reference.Name, terminalSymbol
		} else if ref.File.Attachment.Kind == domain.DiffCommentResource {
			symbol = conversationsSymbol
		}
		chip := b.draftChip(scale, name, symbol)
		if ref.File.Attachment.Kind == domain.DiffCommentResource {
			chip.OnClick = func(ui.Rect) {
				if b.openComment != nil {
					b.openComment(ref)
				}
			}
		} else if ref.File.Attachment.Kind == domain.PathAttachmentResource {
			chip.OnClick = func(ui.Rect) {
				if b.openFile != nil {
					b.openFile(ref)
				}
			}
		}
		chip.Data = ref
		return chip
	}
}

func (b *mobileDraftEditor) draftChip(scale float32, name string, symbol *ui.SVG) *richtext.Attachment {
	font := ui.Font{Size: bodyTextSize * scale}
	tagFont := ui.Font{Size: (controlTextSize - 1) * scale}
	span, draw := sentReferenceChip(name, font, tagFont, symbol)
	return &richtext.Attachment{
		Name: name, Width: span.ObjectWidth, Height: span.ObjectHeight + span.ObjectDescent, Descent: span.ObjectDescent,
		Draw: func(p *ui.Painter, rect ui.Rect) { draw(p, rect, b.chipColors) },
	}
}

func (b *mobileDraftEditor) current(a *mobileApp, parent *mygo.Window) bool {
	if a.model != b.model || !a.pageCurrent(parent) {
		return false
	}
	current := a.draftTarget()
	if b.target.session != current.session && parent == a.detail {
		current = a.parallelDraftTarget()
	}
	if b.target.history {
		if edit := a.model.HistoryEdits[current.session]; edit.ItemID == "" || edit.OperationID != "" {
			return false
		}
		current.key, current.history = a.model.SessionHistoryDraftKey(current.session), true
	}
	return current == b.target
}

func (b *mobileDraftEditor) field(a *mobileApp, c *ui.Context, parent *mygo.Window, title, placeholder string, disabled bool, maxLines int, send func(steer bool)) ui.Element {
	b.openFile = func(ref mobileclient.DraftReference) {
		if !a.preview && b.current(a, parent) && draftAttachmentOwner(ref.File, b.target) {
			a.previewDraftAttachment(parent, b, ref.File)
		}
	}
	b.openComment = func(ref mobileclient.DraftReference) {
		if disabled || !b.current(a, parent) || b.pending() || ref.File.DeviceID != b.target.device ||
			ref.File.WorkspaceID != b.target.workspace {
			return
		}
		if b.Publish(a.model, b.target.key) {
			a.save()
		}
		if b.Contains(ref) {
			a.openSavedDiffComment(parent, a.sessionByID(b.target.session), ref.File.Attachment, b)
		}
	}
	if b.current(a, parent) && b.Publish(a.model, b.target.key) {
		a.model.Error = ""
		a.save()
	}
	p, chip := palette(c), b.chipMaker(c)
	size := bodyTextSize * c.Preferences().TextScale
	e := richtext.EditorBase(c.Key("native-draft"), b.Doc, richtext.EditorOptions{
		PlainText: true, NoMarkdown: true, NoScrollbar: true, ReadOnly: disabled, MaxLines: maxLines,
		Label: title, Placeholder: placeholder, Font: ui.Font{Family: c.Theme().Font, Size: size}, LineHeight: composerLineHeight(c),
		Theme: &richtext.Theme{Text: p.ink, Muted: p.secondary},
		OnPaste: func(text string) bool {
			if text != "" || b.target.history || disabled || b.pending() || !b.current(a, parent) {
				return false
			}
			data := mygo.Clipboard.ReadImage()
			if len(data) == 0 {
				return false
			}
			a.stageLocalAttachment(parent, b.target, b, func() (string, []byte, error) {
				return "粘贴图片.png", data, nil
			})
			return true
		},
		OnKey: func(key ui.Key, mods ui.Modifiers) bool {
			if !b.current(a, parent) {
				return false
			}
			if !disabled && b.referenceKey(a, parent, key, mods, chip) {
				return true
			}
			if key != ui.KeyEnter || send == nil || disabled {
				return false
			}
			// ReturnNewLine routes the on-screen Return straight to the document.
			switch action := b.model.Preferences.MobileShortcutAction(mobileComposerBinding(mods)); action {
			case "newline":
				if mods == 0 || mods == ui.Shift {
					return false
				}
				b.Doc.InsertText("\n")
				if b.Publish(a.model, b.target.key) {
					a.save()
				}
				parent.Invalidate()
			case "send", "steer":
				if mods == 0 {
					if query := b.referenceQuery(); query.Marker != 0 && query != b.dismissed {
						return true
					}
				}
				if !b.pending() {
					if b.Publish(a.model, b.target.key) {
						a.save()
					}
					send(action == "steer")
				}
			}
			return true
		},
	}).ReturnKey(ui.ReturnNewLine).FillWidth()
	if maxLines == 0 {
		e.FillHeight()
	}
	if b.focusRequested && !disabled {
		e.Focus(ui.FocusOptions{PreventScroll: true})
		b.focusRequested = false
	}
	if now := e.Focused(); now != b.focused {
		b.focused = now
		c.AnimationFrame()
	}
	b.trackQuery(a, c, parent, disabled)
	return e
}

func (b *mobileDraftEditor) pending() bool {
	return b.preparingAttachment || b.Doc.Composing() || b.insertion != nil && b.insertion.busy
}

func (a *mobileApp) draftSaveRecovery(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor) {
	composing := editor.Doc.Composing()
	mobileInlinePanel(c).PaddingX(panelInset).Shrink(0).Children(func() {
		ui.Row(c).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Wrap().Children(func() {
			label(c, "草稿尚未保存", captionTextSize, palette(c).warning).Grow(1).Role(ui.RoleStatus)
			action(c.Key("retry"), "重试保存", func() {
				if editor.current(a, parent) && !editor.Doc.Composing() {
					editor.Publish(a.model, editor.target.key)
					a.save()
				}
			}).PaddingX(6).KeepFocus().Disabled(composing)
			action(c.Key("copy"), "复制草稿", func() {
				if !editor.current(a, parent) || editor.Doc.Composing() {
					return
				}
				text := editor.Doc.Text()
				var clipboard strings.Builder
				for _, run := range text.Runs(0, text.Len()) {
					if tag := run.Attributes.Attachment; tag != nil {
						clipboard.WriteString(tag.Name)
					} else {
						clipboard.WriteString(run.Text)
					}
				}
				mygo.Clipboard.WriteText(clipboard.String())
			}).PaddingX(6).KeepFocus().Disabled(composing || editor.Doc.Text().Len() == 0)
		})
	})
}

func (b *mobileDraftEditor) openReferences(a *mobileApp, parent *mygo.Window, chip func(mobileclient.DraftReference) *richtext.Attachment) {
	if !b.current(a, parent) || b.pending() {
		return
	}
	start, end := b.Doc.Selection()
	if !b.focused || a.model.WorkerMinor < 12 {
		start, end = b.Doc.Text().Len(), b.Doc.Text().Len()
	}
	version, connection := b.Doc.Version(), a.model.ConnectionEpoch
	a.openReferences(parent, b.target, b.readiness().Capabilities.Skills, func(ref mobileclient.DraftReference) error {
		if !b.current(a, parent) || version != b.Doc.Version() || connection != a.model.ConnectionEpoch || b.Doc.Composing() {
			return fmt.Errorf("草稿或设备已改变，请重新选择")
		}
		if b.Contains(ref) {
			return fmt.Errorf("已添加此引用")
		}
		b.Insert(min(start, end), max(start, end), chip(ref))
		if b.Publish(a.model, b.target.key) {
			a.save()
		}
		return nil
	})
}

func (b *mobileDraftEditor) trackQuery(a *mobileApp, c *ui.Context, parent *mygo.Window, disabled bool) {
	var query mobileclient.ReferenceQuery
	m := a.model
	if b.focused && !disabled && b.current(a, parent) && !b.Doc.Composing() {
		query = b.referenceQuery()
	}
	if query != b.query || b.connection != m.ConnectionEpoch {
		refilter := query.Marker == '/' && b.query.Marker == '/' && query.Start == b.query.Start &&
			b.query != b.dismissed && b.connection == m.ConnectionEpoch && b.search != nil && b.search.err == ""
		b.insertion = nil
		if query.Marker == 0 || query.Marker != b.query.Marker || b.connection != m.ConnectionEpoch {
			b.choices = nil
		}
		if query != b.dismissed {
			b.dismissed = mobileclient.ReferenceQuery{}
		}
		b.query, b.connection, b.active = query, m.ConnectionEpoch, 0
		b.referenceList.ScrollTo(0, ui.Start)
		if refilter {
			b.filterReferenceSkills()
		} else {
			b.search, b.skillCatalog = nil, nil
		}
		b.queryAt = c.Now().Add(120 * time.Millisecond)
		c.AnimationFrame()
	}
	if b.query.Marker == 0 || b.query == b.dismissed || b.search != nil {
		return
	}
	if delay := b.queryAt.Sub(c.Now()); delay > 0 {
		c.After(delay)
		return
	}
	b.loadReferences(a, parent)
}

func (b *mobileDraftEditor) referenceQuery() mobileclient.ReferenceQuery {
	anchor, caret := b.Doc.Selection()
	if anchor != caret {
		return mobileclient.ReferenceQuery{}
	}
	query, found := mobileclient.ReadReferenceQuery(b.Doc.Text().String(), caret, true)
	if !found {
		return mobileclient.ReferenceQuery{}
	}
	m := b.model
	canSearch := m.Connected && m.WorkerMinor >= 12 && b.target.workspace != "" &&
		(query.Marker == '@' || b.readiness().Capabilities.Skills)
	if !canSearch && len(b.slashCommands(query)) == 0 {
		return mobileclient.ReferenceQuery{}
	}
	return query
}

func (b *mobileDraftEditor) loadReferences(a *mobileApp, parent *mygo.Window) {
	req := &mobileRequest{}
	b.search = req
	if b.query.Marker == '/' && (!a.model.Connected || a.model.WorkerMinor < 12 ||
		b.target.workspace == "" || !b.readiness().Capabilities.Skills) {
		b.choices = nil
		return
	}
	needle := strings.ToLower(b.query.Needle)
	current := func() bool {
		return b.search == req && b.query != b.dismissed && b.current(a, parent) && b.connection == a.model.ConnectionEpoch
	}
	apply := func(results []mobileReferenceChoice) {
		if current() {
			b.choices = results
			count := len(results) + len(b.slashCommands(b.query))
			b.active = min(b.active, max(0, count-1))
			b.referenceList.ScrollIntoView(b.active)
		}
	}
	files := func(catalog domain.FileSearchResult) error {
		choices := make([]mobileReferenceChoice, 0, len(catalog.Files))
		for _, file := range catalog.Files {
			choices = append(choices, mobileReferenceChoice{file: file})
		}
		apply(choices)
		return nil
	}
	skills := func(catalog domain.SkillCatalog) error {
		if current() {
			b.skillCatalog = catalog.Skills
			b.filterReferenceSkills()
		}
		return nil
	}
	if a.preview {
		if b.query.Marker == '/' {
			_ = skills(a.previewReferenceSkills())
		} else {
			_ = files(a.previewReferenceFiles(needle))
		}
		parent.Invalidate()
		return
	}
	git := protocol.GitQuery{WorkspaceID: b.target.workspace, SessionID: b.target.session}
	if b.query.Marker == '/' {
		a.pageJob(parent, req, "skills.list", protocol.SkillQuery{GitQuery: git, HarnessID: b.target.harness}, decodePage(skills))
	} else {
		a.pageJob(parent, req, "file.search", protocol.FileSearchQuery{GitQuery: git, Query: b.query.Needle}, decodePage(files))
	}
}

func (b *mobileDraftEditor) filterReferenceSkills() {
	needle := strings.ToLower(b.query.Needle)
	b.choices = nil
	for _, skill := range b.skillCatalog {
		if strings.Contains(strings.ToLower(skill.Name+" "+skill.Description), needle) {
			b.choices = append(b.choices, mobileReferenceChoice{skill: skill})
			if len(b.choices) == 40 {
				break
			}
		}
	}
	b.active = min(b.active, max(0, len(b.choices)+len(b.slashCommands(b.query))-1))
	b.referenceList.ScrollIntoView(b.active)
}

func (b *mobileDraftEditor) readiness() domain.Readiness {
	if b.target.session != "" && b.target.session != b.model.SessionID {
		for _, session := range b.model.Snapshot.Sessions {
			if session.ID == b.target.session {
				return b.model.SessionReadiness(session)
			}
		}
		return domain.Readiness{}
	}
	return b.model.RunReadiness()
}

func (b *mobileDraftEditor) referenceKey(a *mobileApp, parent *mygo.Window, key ui.Key, mods ui.Modifiers, chip func(mobileclient.DraftReference) *richtext.Attachment) bool {
	if mods != 0 {
		return false
	}
	switch key {
	case ui.KeyEscape, ui.KeyUp, ui.KeyDown, ui.KeyEnter, ui.KeyTab:
	default:
		return false
	}
	query := b.referenceQuery()
	if query.Marker == 0 || query == b.dismissed {
		return false
	}
	if key == ui.KeyEscape {
		b.dismissed, b.insertion = query, nil
		parent.Invalidate()
		return true
	}
	if query != b.query {
		return true
	}
	commands := b.slashCommands(b.query)
	offset := len(commands)
	count := len(b.choices) + offset
	switch key {
	case ui.KeyUp, ui.KeyDown:
		if count == 0 {
			return true
		}
		delta := 1
		if key == ui.KeyUp {
			delta = -1
		}
		b.active = min(count-1, max(0, b.active+delta))
		b.referenceList.ScrollIntoView(b.active)
	case ui.KeyEnter, ui.KeyTab:
		if b.active < offset {
			b.chooseSlashCommand(a, parent, commands[b.active], key == ui.KeyEnter)
			return true
		}
		if b.active-offset < len(b.choices) && b.search != nil && !b.search.busy && b.search.err == "" {
			b.chooseReference(a, parent, b.choices[b.active-offset], chip)
		}
	}
	parent.Invalidate()
	return true
}

func (b *mobileDraftEditor) referencePanel(a *mobileApp, c *ui.Context, parent *mygo.Window) {
	if b.query.Marker == 0 || b.query == b.dismissed {
		return
	}
	p, chip := palette(c), b.chipMaker(c)
	commands := b.slashCommands(b.query)
	choices := b.choices
	offset := len(commands)
	count, rowHeight := len(choices)+offset, mobileTapSize(c)
	searching := b.search == nil || b.search.busy
	err := ""
	if b.search != nil {
		err = b.search.err
	}
	if b.insertion != nil && b.insertion.err != "" {
		err = b.insertion.err
	}
	panel := ui.Column(c.Key("reference-candidates")).Radius(14).Border(1, p.line).Background(p.panel).
		Clip().Transition(mobilePanelTransition)
	if count > 0 {
		panel.MinHeight(rowHeight + 2)
	}
	panel.Children(func() {
		if err != "" {
			label(c, err, 13, p.warning).Padding(9, 12)
		}
		if count == 0 {
			status := "没有找到引用"
			if searching {
				status = "正在查找…"
			}
			if err == "" {
				label(c, status, 13, p.secondary).Padding(9, 12)
			}
			return
		}
		b.referenceList.Key = func(i int) any {
			if i < offset {
				return commands[i]
			}
			choice := choices[i-offset]
			if choice.skill.ID != "" {
				return choice.skill.SkillReference
			}
			return choice.file
		}
		ui.List(c.Key("candidates"), &b.referenceList, count, func(i int) {
			if i < offset {
				command := commands[i]
				title, access, symbol, reason := "设置目标", "设置目标", goalSymbol, ""
				if command == "/compact" {
					session := a.sessionByID(b.target.session)
					title, access, symbol = "压缩上下文", "压缩上下文命令", compactSymbol
					reason = a.compactionReason(session, a.compaction(session))
				}
				row := rowDivider(mobileButton(c.Key(command), access, func() {
					b.chooseSlashCommand(a, parent, command, true)
				}).FillWidth().Height(rowHeight).AlignItems(ui.Center).Gap(8).KeepFocus().PaddingX(12), p.line).
					Disabled(reason != "" || b.pending()).Value(reason)
				b.referenceHighlight(row, i, p)
				row.Children(func() {
					ui.Icon(c, symbol).Size(15, 15).TextColor(p.secondary).PassThrough()
					oneLine(c, title, 14, p.ink).Grow(1).MinWidth(0).PassThrough()
					hint := command
					if reason != "" {
						hint = reason
					}
					oneLine(c, hint, 11, p.secondary).MaxWidthPercent(55).PassThrough()
				})
				return
			}
			choice := choices[i-offset]
			part := c.Key(b.referenceList.Key(i))
			name, role, symbol := choice.display()
			detail := choice.detail()
			row := ui.Row(part).Role(ui.RoleButton).Label(role + name).Height(rowHeight).
				Shrink(0).AlignItems(ui.Center).PaddingX(12).Gap(8).KeepFocus().
				Disabled(searching || b.pending()).Value(detail).OnClick(func() { b.chooseReference(a, parent, choice, chip) })
			b.referenceHighlight(row, i, p)
			row.Children(func() {
				ui.Icon(part, symbol).Size(15, 15).Shrink(0).TextColor(p.secondary).PassThrough()
				oneLine(part, name, 14, p.ink).Grow(1).MinWidth(0).Ellipsis("…").PassThrough()
				if detail != "" {
					oneLine(part, detail, captionTextSize, p.secondary).MaxWidthPercent(48).MinWidth(0).Ellipsis("…").PassThrough()
				}
			})
		}).FillWidth().Height(float32(min(count, 3)) * rowHeight).MinHeight(rowHeight).
			DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
				if bounds.H != b.referenceListHeight {
					// The keyboard and the other dock panels can resize the
					// viewport after an arrow key requested the selected row.
					b.referenceListHeight = bounds.H
					b.referenceList.ScrollIntoView(b.active)
					c.Services().Invalidate()
				}
			})
	})
}

func (b *mobileDraftEditor) referenceHighlight(row ui.Element, index int, p colors) {
	selected := index == b.active
	row.AccessSelected(selected)
	if selected {
		row.Background(p.selected)
	}
}

func (b *mobileDraftEditor) slashCommands(q mobileclient.ReferenceQuery) []string {
	if q.Marker != '/' || b.target.history || b.target.queueEdit != "" {
		return nil
	}
	runes := []rune(b.Doc.Text().String())
	if q.Start < 0 || q.End > len(runes) || strings.TrimSpace(string(runes[:q.Start])) != "" {
		return nil
	}
	prefix := "/" + strings.ToLower(q.Needle)
	var commands []string
	if strings.HasPrefix("/compact", prefix) {
		commands = append(commands, "/compact")
	}
	if strings.HasPrefix(mobileclient.GoalCommandText, prefix) && b.model.Connected && b.target.workspace != "" &&
		b.model.WorkerMinor >= protocol.GoalMinor && b.readiness().Capabilities.Goals {
		commands = append(commands, mobileclient.GoalCommandText)
	}
	return commands
}

func (b *mobileDraftEditor) chooseSlashCommand(a *mobileApp, parent *mygo.Window, command string, run bool) {
	if !b.current(a, parent) || b.pending() || !slices.Contains(b.slashCommands(b.query), command) {
		return
	}
	if command == mobileclient.GoalCommandText {
		b.InsertGoal(b.query.Start, b.query.End)
		b.query, b.search, b.choices = mobileclient.ReferenceQuery{}, nil, nil
		if b.Publish(a.model, b.target.key) {
			a.model.Error = ""
			a.save()
		}
		parent.Invalidate()
		return
	}
	s := a.sessionByID(b.target.session)
	if reason := a.compactionReason(s, a.compaction(s)); reason != "" {
		a.model.Error = reason
		parent.Invalidate()
		return
	}
	q := b.query
	b.Doc.Edit(func(edit *richtext.Edit) { edit.Replace(q.Start, q.End, richtext.NewText(command)) })
	b.Doc.SetSelection(q.Start+len(command), q.Start+len(command))
	b.query, b.search, b.choices = mobileclient.ReferenceQuery{}, nil, nil
	b.Publish(a.model, b.target.key)
	a.save()
	if run {
		b.dismissed, _ = mobileclient.ReadReferenceQuery(b.Doc.Text().String(), q.Start+len(command), true)
		a.tryCompactionCommand(b)
	}
	parent.Invalidate()
}

func (b *mobileDraftEditor) chooseReference(a *mobileApp, parent *mygo.Window, choice mobileReferenceChoice, chip func(mobileclient.DraftReference) *richtext.Attachment) {
	if !b.current(a, parent) || b.pending() || b.search == nil || b.search.busy {
		return
	}
	query, version, epoch, target := b.query, b.Doc.Version(), b.connection, b.target
	req := &mobileRequest{}
	b.insertion = req
	insert := func(ref mobileclient.DraftReference) error {
		anchor, caret := b.Doc.Selection()
		if b.insertion != req || b.query != query || !b.current(a, parent) || a.model.ConnectionEpoch != epoch ||
			b.Doc.Version() != version || b.Doc.Composing() || anchor != query.End || caret != query.End {
			return nil
		}
		if b.Contains(ref) {
			return fmt.Errorf("已添加此引用")
		}
		b.Insert(query.Start, query.End, chip(ref))
		if b.Publish(a.model, target.key) {
			a.save()
		}
		b.query, b.insertion, b.search, b.choices = mobileclient.ReferenceQuery{}, nil, nil, nil
		parent.Invalidate()
		return nil
	}
	file := func(item domain.Attachment) error {
		return insert(mobileclient.DraftReference{File: app.DraftAttachment{
			Attachment: item, DeviceID: target.device, WorkspaceID: target.workspace, Ready: true,
		}})
	}
	if choice.skill.ID != "" {
		if err := insert(mobileclient.DraftReference{Skill: app.DraftSkill{Reference: choice.skill.SkillReference, WorkspaceID: target.workspace,
			DeviceID: target.device, HarnessID: target.harness}}); err != nil {
			req.err = err.Error()
		}
	} else if a.preview {
		_ = file(domain.Attachment{ID: "preview-draft-connection", Name: filepath.Base(choice.file.Path), SourcePath: choice.file.Path})
	} else {
		git := protocol.GitQuery{WorkspaceID: target.workspace, SessionID: target.session}
		a.pageJob(parent, req, "file.snapshot", protocol.FileSnapshotQuery{GitQuery: git, Root: choice.file.Root, Path: choice.file.Path}, decodePage(file))
	}
}

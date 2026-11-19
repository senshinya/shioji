package ui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mentionPhase uint8

const (
	mentionWaiting mentionPhase = iota
	mentionSearching
	mentionShowing
	mentionCapturing
	mentionCaptured
	mentionDismissed
)

type slashCommand string

const (
	slashCompact slashCommand = "/compact"
	slashGoal    slashCommand = "/goal"
)

type fileMention struct {
	context, text, query string
	key                  string
	start, end           int
	phase                mentionPhase
	results              domain.FileSearchResult
	skills               []domain.Skill
	captured             app.DraftAttachment
	trigger              rune
	selected             int
	commands             []slashCommand
	reveal               bool
	present              bool
	revision             uint64
	due                  time.Time
	err                  string
	cancel               context.CancelFunc
}

// Offsets match the native editor's rune positions, not bytes. A trigger starts
// a token: email addresses and paths containing @ remain ordinary text.
func mentionToken(text string, start, end int) (from, to int, query string, ok bool) {
	runes := []rune(text)
	if start != end || start < 0 || start > len(runes) {
		return
	}
	from = start
	for from > 0 && !unicode.IsSpace(runes[from-1]) {
		from--
	}
	if from >= start || (runes[from] != '@' && runes[from] != '/') {
		return
	}
	for _, r := range runes[from+1 : start] {
		if r == '@' {
			return
		}
	}
	// Only the trigger and query before the caret belong to this completion.
	// Text already following an insertion point is not part of the query,
	// even when no whitespace separates it from the newly typed trigger.
	return from, start, string(runes[from+1 : start]), true
}
func (v *View) referenceEditorActive(key string) bool {
	if key == v.Model.DraftKey() {
		return true
	}
	edit := v.Model.HistoryEdits[v.Model.SessionID]
	return edit.ItemID != "" && edit.OperationID == "" && key == v.Model.HistoryDraftKey() && v.historyExit == nil
}
func (v *View) mentionContext(key string) string {
	if !v.referenceEditorActive(key) {
		return "inactive:" + key
	}
	return v.Model.ConnectionID() + "/" + v.Model.WorkspaceID + "/" + v.Model.HarnessID() + "/" + v.attachmentPreparationKey(key)
}
func (v *View) clearMention() {
	if v.fileMention != nil && v.fileMention.cancel != nil {
		v.fileMention.cancel()
	}
	v.fileMention = nil
}
func (v *View) syncMention(c *n.Context, input composerInput, text string) {
	m := v.Model
	key := input.binding.key
	if v.fileMention != nil && !v.referenceEditorActive(v.fileMention.key) {
		v.clearMention()
	}
	if v.fileMention != nil && v.fileMention.key != key && !input.Focused() {
		return
	}
	if !v.referenceEditorActive(key) || strings.HasPrefix(key, "history:") && m.WorkerMinor < 12 {
		return
	}
	if v.Controller == nil || !m.Connected || m.WorkerMinor < 11 || m.WorkspaceID == "" {
		v.clearMention()
		return
	}
	context := v.mentionContext(key)
	if v.fileMention != nil && v.fileMention.context != context {
		v.clearMention()
	}
	if v.fileMention != nil && v.fileMention.phase == mentionCaptured {
		if !input.Composing() {
			if v.fileMention.trigger == '/' {
				v.applySelectedSkill(c, input.binding, v.fileMention)
				return
			}
			v.applyCapturedMention(c, input, text, v.fileMention)
		}
		return
	}
	if v.fileMention != nil && v.fileMention.phase == mentionCapturing {
		return
	}
	if input.Composing() {
		return
	}
	a, b := input.TextSelection()
	from, to, query, ok := mentionToken(text, a, b)
	if !ok {
		v.clearMention()
		return
	}
	trigger := []rune(text)[from]
	var commands []slashCommand
	if trigger == '/' {
		commands = v.slashCommands(key, from, query)
		if !m.RunReadiness().Capabilities.Skills && len(commands) == 0 {
			v.clearMention()
			return
		}
	}
	state := v.fileMention
	if state == nil || state.text != text || state.start != from || state.end != to || state.query != query || !slices.Equal(state.commands, commands) {
		if !input.Focused() {
			v.clearMention()
			return
		}
		if state == nil || state.start != from || state.trigger != trigger {
			v.clearMention()
			state = &fileMention{key: key, context: context, start: from, trigger: trigger}
			v.fileMention = state
		} else if state.cancel != nil {
			state.cancel()
		}
		state.revision++
		state.text, state.query, state.end = text, query, to
		if !slices.Equal(state.commands, commands) {
			state.selected = 0
		}
		state.commands = commands
		if len(commands) != 0 {
			state.present = true
		}
		state.phase, state.err = mentionWaiting, ""
		state.due = c.Now().Add(120 * time.Millisecond)
	}
	if state.phase == mentionWaiting {
		if c.Now().Before(state.due) {
			c.After(state.due.Sub(c.Now()))
			return
		}
		v.searchMention(state)
	}
}
func (v *View) searchMention(state *fileMention) {
	if state.trigger == '/' && !v.Model.RunReadiness().Capabilities.Skills {
		state.phase = mentionShowing
		state.present = true
		return
	}
	target, err := v.Model.AttachmentTarget(v.ControllerDirectory(), v.Model.WorkspaceID)
	if err != nil {
		state.err = err.Error()
		state.phase = mentionShowing
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	state.cancel = cancel
	state.phase = mentionSearching
	state.present = true
	query, revision, trigger := state.query, state.revision, state.trigger
	session := v.Model.SessionID
	harnessID := v.Model.HarnessID()
	v.Controller.AttachmentTask(func() app.Update {
		defer cancel()
		var results domain.FileSearchResult
		var skills []domain.Skill
		var err error
		if trigger == '/' {
			var catalog domain.SkillCatalog
			catalog, err = target.Skills(ctx, session, harnessID)
			if err == nil {
				for _, skill := range catalog.Skills {
					if strings.Contains(strings.ToLower(skill.Name+" "+skill.Description), strings.ToLower(query)) {
						skills = append(skills, skill)
						if len(skills) == 40 {
							break
						}
					}
				}
			}
		} else {
			results, err = target.SearchFiles(ctx, session, query)
		}
		return func(*app.Model) {
			if v.fileMention != state || state.revision != revision || state.phase == mentionDismissed {
				return
			}
			state.phase = mentionShowing
			state.results = results
			state.skills = skills
			state.selected = min(state.selected, max(0, state.count()-1))
			state.reveal = true
			if err != nil {
				state.err = err.Error()
			}
		}
	})
}
func (v *View) mentionKey(k composerKey) bool {
	key := k.binding.key
	if !v.referenceEditorActive(key) || strings.HasPrefix(key, "history:") && v.Model.WorkerMinor < 12 {
		return false
	}
	state := v.fileMention
	if state != nil && state.key != key {
		v.clearMention()
		state = nil
	}
	a, b := k.Selection()
	text := k.Text()
	from, to, query, token := mentionToken(text, a, b)
	if !token {
		return false
	}
	trigger := []rune(text)[from]
	var commands []slashCommand
	if trigger == '/' {
		commands = v.slashCommands(key, from, query)
		if !v.Model.RunReadiness().Capabilities.Skills && len(commands) == 0 {
			return false
		}
	}
	if state == nil && (k.Key == n.KeyEnter || k.Key == n.KeyTab) && k.Mods == 0 && v.Controller != nil && v.Model.Connected && v.Model.WorkerMinor >= 11 {
		state = &fileMention{key: key, context: v.mentionContext(key), text: text, query: query, start: from, end: to, trigger: trigger, phase: mentionWaiting, due: v.frameTime, commands: commands}
		v.fileMention = state
		if len(commands) != 0 {
			v.chooseSlashCommand(state, k.binding, k.Key == n.KeyTab)
		}
		return true
	}
	if state == nil || state.phase == mentionDismissed {
		return false
	}
	if k.Mods != 0 {
		return false
	}
	switch k.Key {
	case n.KeyEscape:
		if state.cancel != nil {
			state.cancel()
		}
		state.phase = mentionDismissed
		return true
	case n.KeyUp, n.KeyDown:
		if text != state.text || query != state.query {
			return true
		}
		direction := 1
		if k.Key == n.KeyUp {
			direction = -1
		}
		state.selected = max(0, min(state.count()-1, state.selected+direction))
		state.reveal = true
		return true
	case n.KeyEnter, n.KeyTab:
		if text != state.text || from != state.start || to != state.end || query != state.query || !slices.Equal(state.commands, commands) {
			return true
		}
		if state.selected < len(state.commands) {
			v.chooseSlashCommand(state, k.binding, k.Key == n.KeyTab)
			return true
		}
		// Enter while a lookup is pending belongs to the picker, never to send.
		if state.phase == mentionShowing && state.err == "" && state.count() > 0 {
			v.captureMention(state)
		}
		return true
	}
	return false
}
func (v *View) captureMention(state *fileMention) {
	if state.phase != mentionShowing || state.selected >= state.count() {
		return
	}
	if state.trigger == '/' {
		v.selectSkill(state)
		return
	}
	m := v.Model
	key := state.key
	token := v.attachmentPreparationKey(key)
	workspace := m.WorkspaceID
	session := m.SessionID
	fileCount := 0
	for _, file := range m.Attachments[key] {
		if file.Attachment.Kind != domain.DiffCommentResource {
			fileCount++
		}
	}
	if fileCount >= 8 {
		state.err = "最多添加 8 个附件"
		return
	}
	target, err := m.AttachmentTarget(v.ControllerDirectory(), workspace)
	if err != nil {
		state.err = err.Error()
		return
	}
	file := state.results.Files[state.selected]
	state.phase = mentionCapturing
	if v.attachmentPreparing == nil {
		v.attachmentPreparing = map[string]int{}
	}
	v.attachmentPreparing[token]++
	v.Controller.AttachmentTask(func() app.Update {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		a, err := target.SnapshotFile(ctx, session, file)
		return func(model *app.Model) {
			v.attachmentPreparing[token]--
			if v.fileMention != state || state.phase == mentionDismissed || v.mentionContext(state.key) != state.context || v.attachmentPreparationKey(key) != token {
				return
			}
			if err != nil {
				if state.phase != mentionDismissed {
					state.phase = mentionShowing
				}
				state.err = err.Error()
				v.Model.Error = err.Error()
				return
			}
			state.captured = app.DraftAttachment{Attachment: a, Ready: true, WorkspaceID: workspace, DeviceID: target.DeviceID}
			state.phase = mentionCaptured
		}
	})
}
func (v *View) mentionPopup(c *n.Context, t *n.Theme, anchor n.Element, input composerInput) {
	state := v.fileMention
	if state != nil && state.key != input.binding.key {
		return
	}
	open := state != nil && state.phase != mentionDismissed && (state.present || state.phase != mentionWaiting)
	panel := popover(c.Key("file-mention-popup"), anchor, &open, func(panel n.Element) {
		commands := len(state.commands)
		count := min(8, state.count())
		const expandedHeight = 8*compactMenuRowHeight + 2*compactMenuInset
		height := float32(count*compactMenuRowHeight + 2*compactMenuInset)
		if commands > 0 && len(state.skills) > 0 {
			height += 7
		}
		if count == 0 || state.phase == mentionCapturing || state.err != "" && commands == 0 {
			height = 32
		}
		windowW, windowH := c.Size()
		height = min(height, max(32, windowH-80))
		// Fit the expanded list before the short loading row appears. Using
		// the animated height would flip the popup when results arrive.
		bounds := anchor.Bounds()
		above, below := max(float32(0), bounds.Y-12), max(float32(0), windowH-bounds.Y-bounds.H-12)
		if above < min(expandedHeight, max(32, windowH-80)) && below > above {
			panel.AttachTo(anchor, n.AnchorBottomStart, n.AnchorTopStart).Margin(8, 0, 0, 0)
			height = min(height, max(32, below))
		} else {
			panel.AttachTo(anchor, n.AnchorTopStart, n.AnchorBottomStart).Margin(0, 0, 8, 0)
			height = min(height, max(32, above))
		}
		compactMenuSurface(panel, t).Width(min(380, windowW-40)).
			Height(panel.AnimateTo("height", height, motionAnimation(c, 180*time.Millisecond))).Clip().Label(state.title()).Children(func() {
			message := ""
			if (state.phase == mentionSearching || state.phase == mentionWaiting) && state.count() == 0 {
				message = "正在查找文件…"
			} else if state.phase == mentionCapturing {
				message = "正在读取文件…"
			} else if state.err != "" && commands == 0 {
				message = state.err
			} else if state.count() == 0 {
				message = "没有匹配的文件"
				if state.trigger == '/' {
					message = "没有匹配的 Skill"
				}
			}
			if message != "" {
				n.Text(c, message).FontSize(12).FixedLineHeight(20).Padding(3, 7).TextColor(t.TextMuted)
				return
			}
			n.Scroll(c.Key("files")).Fill().Children(func() {
				n.Column(c).FillWidth().Children(func() {
					for i := 0; i < state.count(); i++ {
						if commands > 0 && i == commands {
							n.Column(c).FillWidth().Height(1).Margin(3, 0).Background(t.Border)
						}
						var file domain.WorkspaceFile
						label, detail, identity, accessible := "", "", "", ""
						icon := fileAttachmentIcon
						disabled := state.phase != mentionShowing
						if i < commands {
							command := state.commands[i]
							detail, identity, disabled = string(command), "builtin:"+strings.TrimPrefix(string(command), "/"), false
							if command == slashCompact {
								label, icon = "压缩上下文", compactContextIcon
								if reason := v.compactReason(); reason != "" {
									detail, disabled = reason, true
								}
							} else {
								label, icon = "设置目标", goalTargetIcon
							}
							accessible = label + "命令"
						} else if state.trigger == '/' {
							icon = skillIcon
							skill := state.skills[i-commands]
							label = skill.Name
							detail = skill.Description
							identity = skill.ID
							accessible = "选择 Skill：" + skill.Name
						} else {
							file = state.results.Files[i]
							label = filepath.Base(file.Path)
							detail = filepath.ToSlash(filepath.Join(filepath.Base(file.Root), filepath.Dir(file.Path)))
							identity = file.Root + "/" + file.Path
							accessible = "引用文件：" + file.Path
						}
						row := control(c.Key(identity), compactMenuRowRadius).FillWidth().Height(compactMenuRowHeight).
							PaddingX(compactMenuRowInset).Radius(compactMenuRowRadius).Label(accessible).Tooltip(detail).Disabled(disabled)
						background := raised(t)
						if i == state.selected {
							background = menuSelectionSurface(t)
						}
						row.Background(row.AnimateColor("reference-selection", background, motionAnimation(c, 120*time.Millisecond)))
						row.Children(func() {
							n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
								referenceSymbol(c, icon, label, n.Font{Family: c.Theme().Font, Size: compactMenuFontSize, Weight: 400}, compactMenuIconSize, t.TextMuted)
								n.Text(c, label).SingleLine().Ellipsis("…").FontSize(compactMenuFontSize).FixedLineHeight(18).Grow(1).MinWidth(0)
								n.Text(c, detail).SingleLine().Ellipsis("…").FontSize(11).TextColor(t.TextMuted).MaxWidth(160)
							})
						})
						if state.reveal && i == state.selected {
							row.ScrollIntoView()
						}
						if row.Clicked() {
							state.selected = i
							if i < commands {
								v.chooseSlashCommand(state, input.binding, false)
							} else {
								v.captureMention(state)
							}
							input.Focus()
						}
					}
					state.reveal = false
				})
			})
		})
	})
	if panel.Dismissed() || !open && state != nil && state.phase != mentionWaiting {
		if state != nil {
			if state.cancel != nil {
				state.cancel()
			}
			state.phase = mentionDismissed
		}
	}
}

func (v *View) referenceDeviceID() string {
	target, err := v.Model.AttachmentTarget(v.ControllerDirectory(), v.Model.WorkspaceID)
	if err != nil {
		return ""
	}
	return target.DeviceID
}

// Lucide book-open, ISC; attribution in third_party/lucide/LICENSE.
var skillIcon = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/></svg>`))

func (s *fileMention) count() int {
	if s.trigger == '/' {
		return len(s.commands) + len(s.skills)
	}
	return len(s.results.Files)
}

func (v *View) slashCommands(key string, from int, query string) []slashCommand {
	m := v.Model
	if from != 0 || key != m.DraftKey() || m.ActiveQueueEdit(m.SessionID).ID != "" {
		return nil
	}
	query = "/" + strings.ToLower(query)
	capabilities := m.RunReadiness().Capabilities
	var commands []slashCommand
	if m.WorkerMinor >= 30 && capabilities.Compact && strings.HasPrefix(string(slashCompact), query) {
		commands = append(commands, slashCompact)
	}
	if m.WorkerMinor >= protocol.GoalMinor && capabilities.Goals && strings.HasPrefix(string(slashGoal), query) {
		commands = append(commands, slashGoal)
	}
	return commands
}
func (s *fileMention) skillIndex() int {
	return s.selected - len(s.commands)
}
func (v *View) chooseSlashCommand(state *fileMention, binding *composerDocument, complete bool) {
	command := state.commands[state.selected]
	runes := []rune(state.text)
	if command == slashCompact && !complete {
		if len(runes) != state.end {
			s := v.contextInput()
			s.command = true
			s.error = "/compact 需要单独输入"
			return
		}
		v.beginContextCompaction(true)
		state.phase = mentionDismissed
		return
	}
	replacement := string(command)
	caret := state.start + len(replacement)
	if command == slashGoal {
		binding.doc.Edit(func(e *richtext.Edit) {
			e.ReplaceString(state.start, state.end, string(richtext.ObjectReplacement), binding.goalTag)
			if state.end == len(runes) || !unicode.IsSpace(runes[state.end]) {
				e.ReplaceString(state.start+1, state.start+1, " ", richtext.Attributes{})
			}
			e.Select(state.start+2, state.start+2)
		})
		v.publishComposer(state.key, binding)
		state.phase = mentionDismissed
		return
	}
	binding.doc.Edit(func(e *richtext.Edit) {
		e.ReplaceString(state.start, state.end, replacement, richtext.Attributes{})
		e.Select(caret, caret)
	})
	v.publishComposer(state.key, binding)
	state.text = binding.doc.Text().String()
	state.query = strings.TrimPrefix(string(command), "/")
	state.end = caret
	state.phase = mentionDismissed
}
func (s *fileMention) title() string {
	if len(s.commands) > 0 {
		return "命令与 Skill"
	}
	if s.trigger == '/' {
		return "选择 Skill"
	}
	return "引用工作区文件"
}
func (v *View) selectSkill(state *fileMention) {
	if len(v.Model.DraftSkills[state.key]) >= 8 {
		state.err = "最多引用 8 个 Skill"
		return
	}
	for _, ref := range v.Model.DraftSkills[state.key] {
		if ref.Reference == state.skills[state.skillIndex()].SkillReference && ref.HarnessID == v.Model.HarnessID() {
			state.err = "已引用此 Skill"
			return
		}
	}
	state.phase = mentionCaptured
}
func (v *View) applySelectedSkill(c *n.Context, b *composerDocument, state *fileMention) {
	if state.phase != mentionCaptured {
		return
	}
	skill := state.skills[state.skillIndex()]
	ref := app.DraftSkill{Reference: skill.SkillReference, WorkspaceID: v.Model.WorkspaceID, DeviceID: v.referenceDeviceID(), HarnessID: v.Model.HarnessID()}
	v.insertReference(c, b, state, composerReference{skill: ref})
	v.clearMention()
	c.AnimationFrame()
}

// Apply network results on the native frame, after the editor has published
// current text and selection. Never restore focus from a network callback.
func (v *View) applyCapturedMention(c *n.Context, input composerInput, text string, state *fileMention) {
	if err := v.validateAddedAttachments(state.key, []app.DraftAttachment{state.captured}); err != nil {
		state.phase = mentionShowing
		state.err = err.Error()
		v.Model.Error = err.Error()
		return
	}
	v.insertReference(c, input.binding, state, composerReference{file: state.captured})
	v.clearMention()
	c.AnimationFrame()
}

// Reference names can be entirely lowercase, including descenders. Measure the
// actual name's ink while retaining the font baseline and the shared 20px slot.
func referenceSymbol(c *n.Context, icon *n.SVG, label string, font n.Font, size float32, color n.Color) n.Element {
	m := inlineFontMetrics(label, font)
	ink := font.InkBounds(label)
	center := -(ink.Y + ink.H/2)
	return n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: size, ObjectHeight: m.ascent, ObjectDescent: m.descent}).Font(font.Family).FontSize(font.Size).FixedLineHeight(20).Role(n.RoleNone).DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		r.Y += m.ascent - size/2 - center
		r.H = size
		p.Icon(icon, r, color)
	})
}

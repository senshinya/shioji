package mobileclient

import (
	"sort"
	"strings"
	"unicode"

	"shioji/internal/domain"
)

// SentMessageRun is either ordinary message text or one displayed reference.
type SentMessageRun struct {
	Text       string
	Attachment *domain.Attachment
	Skill      *domain.SkillReference
	Goal       bool
}

func (run SentMessageRun) Inline() bool { return run.Goal || run.Attachment != nil || run.Skill != nil }

// SentMessageRuns reads the worker's one-based positions in the original
// text-with-objects. An older item without positions keeps its references
// readable at the end of the message.
func SentMessageRuns(item domain.Item) []SentMessageRun {
	if item.Kind == "user" && len(item.Attachments) == 0 && len(item.Skills) == 0 {
		if start, end, goal := GoalTagRange(item.Text); goal {
			text := []rune(item.Text)
			return []SentMessageRun{{Text: string(text[:start])}, {Text: GoalCommandText, Goal: true}, {Text: string(text[end:])}}
		}
	}
	return messageRuns(item, true)
}

func messageRuns(item domain.Item, separateUnpositioned bool) []SentMessageRun {
	attachments, skills := make(map[string]int), make(map[string]int)
	for _, position := range item.ReferencePositions {
		if position.SkillID != "" {
			skills[position.SkillID] = position.Position
		} else {
			attachments[position.AttachmentID] = position.Position
		}
	}
	type positioned struct {
		SentMessageRun
		position int
	}
	refs := make([]positioned, 0, len(item.Attachments)+len(item.Skills))
	for i := range item.Attachments {
		file := &item.Attachments[i]
		if file.InlineReference() {
			refs = append(refs, positioned{SentMessageRun{Text: file.Name, Attachment: file}, attachments[file.ID]})
		}
	}
	for i := range item.Skills {
		skill := &item.Skills[i]
		refs = append(refs, positioned{SentMessageRun{Text: skill.Name, Skill: skill}, skills[skill.ID]})
	}
	if len(refs) == 0 {
		return []SentMessageRun{{Text: item.Text}}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].position > 0 && (refs[j].position <= 0 || refs[i].position < refs[j].position)
	})
	text := []rune(item.Text)
	runs := make([]SentMessageRun, 0, 2*len(refs)+1)
	start := 0
	for i, ref := range refs {
		end := len(text)
		if ref.position > 0 {
			end = min(len(text), max(start, ref.position-1-i))
		}
		if end > start {
			runs = append(runs, SentMessageRun{Text: string(text[start:end])})
		}
		if separateUnpositioned && ref.position <= 0 && len(runs) != 0 && strings.TrimRightFunc(runs[len(runs)-1].Text, unicode.IsSpace) == runs[len(runs)-1].Text {
			runs = append(runs, SentMessageRun{Text: " "})
		}
		runs = append(runs, ref.SentMessageRun)
		start = end
	}
	if start < len(text) {
		runs = append(runs, SentMessageRun{Text: string(text[start:])})
	}
	return runs
}

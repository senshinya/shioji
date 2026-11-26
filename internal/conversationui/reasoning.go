package conversationui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// ThinkingIconPath is shared by the native tool timelines.
const ThinkingIconPath = "M10 6.7C9.4 5.4 8.1 4.5 6.7 4.5C4.9 4.5 3.8 5.7 3.9 7.4C2.2 8.1 2 10.8 3.6 11.8C3.4 13.8 5 15.5 7 15.5C8.3 15.5 9.3 15 10 13.9C10.7 15 11.7 15.5 13 15.5C15 15.5 16.6 13.8 16.4 11.8C18 10.8 17.8 8.1 16.1 7.4C16.2 5.7 15.1 4.5 13.3 4.5C11.9 4.5 10.6 5.4 10 6.7V13.9M3.9 7.4c1.3-.1 2.4.7 2.6 1.8M16.1 7.4c-1.3-.1-2.4.7-2.6 1.8M3.6 11.8c1.3.4 2.4-.1 2.9-1M16.4 11.8c-1.3.4-2.4-.1-2.9-1"

// ReasoningSummaryText reads both streamed summary text and structured
// summary paragraphs. It leaves the journal text available for search.
func ReasoningSummaryText(item domain.Item) string {
	paragraphs := make([]string, 0, 1+len(item.Parts))
	if text := trimTrailingLineBreaks(item.Text); strings.TrimSpace(text) != "" {
		paragraphs = append(paragraphs, text)
	}
	for _, part := range item.Parts {
		if part.Attachment == nil {
			if text := trimTrailingLineBreaks(part.Text); strings.TrimSpace(text) != "" {
				paragraphs = append(paragraphs, text)
			}
		}
	}
	if item.Truncated {
		paragraphs = append(paragraphs, "内容过长，已截断显示")
	}
	return strings.Join(paragraphs, "\n\n")
}

// ReasoningForDisplay drops trailing line breaks from each rendered summary
// block while leaving the recorded text and its interior line breaks intact.
func ReasoningForDisplay(item domain.Item) domain.Item {
	if !domain.ReasoningSummary(item) {
		return item
	}
	item.Text = trimTrailingLineBreaks(item.Text)
	if len(item.Parts) == 0 {
		return item
	}
	parts := make([]domain.ContentPart, 0, len(item.Parts))
	for _, part := range item.Parts {
		if part.Attachment == nil && (part.Kind == "text" || part.Kind == "") {
			part.Text = trimTrailingLineBreaks(part.Text)
			if part.Text == "" && part.Label == "" {
				continue
			}
		}
		parts = append(parts, part)
	}
	item.Parts = parts
	return item
}

func trimTrailingLineBreaks(text string) string {
	content := strings.TrimRightFunc(text, unicode.IsSpace)
	if content == "" {
		return ""
	}
	lineBreak := strings.IndexAny(text[len(content):], "\r\n")
	if lineBreak < 0 {
		return text
	}
	return text[:len(content)+lineBreak]
}

const reasoningFadeDuration = 180 * time.Millisecond

// ReasoningFadeSpan marks a newly revealed slice of reasoning text while it
// fades in to full opacity.
type ReasoningFadeSpan struct {
	Start, End int
	Alpha      float32
}

// ReasoningTextFrame is the prefix and fading tail of a streaming summary for
// the current frame.
type ReasoningTextFrame struct {
	Visible   string
	Fades     []ReasoningFadeSpan
	Animating bool
}

type reasoningRevealMark struct {
	start, end int
	at         time.Time
}

// ReasoningTextStream paces bursty or incremental plain-text reasoning updates
// character-by-character without exposing trailing line breaks mid-stream.
type ReasoningTextStream struct {
	initialized bool
	source      string
	shown       int
	carry       float64
	last        time.Time
	marks       []reasoningRevealMark
}

func (s *ReasoningTextStream) Advance(now time.Time, text string, live, fresh, reduceMotion bool) ReasoningTextFrame {
	if !live || reduceMotion {
		s.initialized, s.source, s.shown, s.carry, s.last, s.marks = true, text, len(text), 0, now, nil
		return ReasoningTextFrame{Visible: text}
	}
	if !s.initialized {
		s.initialized, s.source = true, text
		if !fresh {
			s.shown, s.last = len(text), now
			return ReasoningTextFrame{Visible: text}
		}
		s.shown, s.last = 0, time.Time{}
	} else if text != s.source {
		if !strings.HasPrefix(text, s.source) {
			s.source, s.shown, s.carry, s.last, s.marks = text, len(text), 0, now, nil
			return ReasoningTextFrame{Visible: text}
		}
		s.source = text
	}
	if s.shown < len(text) {
		dt := now.Sub(s.last)
		if s.last.IsZero() || dt > 100*time.Millisecond {
			dt = 16 * time.Millisecond
		} else if dt < 0 {
			dt = 0
		}
		s.last = now
		remaining := utf8.RuneCountInString(text[s.shown:])
		rate := max(120.0, float64(remaining)/0.45)
		s.carry += rate * dt.Seconds()
		count := min(remaining, int(s.carry))
		if s.shown == 0 && count == 0 && remaining > 0 {
			count = 1
		}
		s.carry -= float64(count)
		next := s.shown
		for i := 0; i < count && next < len(text); i++ {
			_, size := utf8.DecodeRuneInString(text[next:])
			next += size
		}
		if next < len(text) && trimTrailingLineBreaks(text[:next]) != text[:next] {
			for next < len(text) {
				r, size := utf8.DecodeRuneInString(text[next:])
				next += size
				if !unicode.IsSpace(r) {
					break
				}
			}
		}
		if next > s.shown {
			s.marks = append(s.marks, reasoningRevealMark{start: s.shown, end: next, at: now})
			s.shown = next
		}
	} else {
		s.carry = 0
		s.last = now
	}
	active := s.marks[:0]
	var fades []ReasoningFadeSpan
	for _, mark := range s.marks {
		if mark.end > s.shown {
			continue
		}
		elapsed := now.Sub(mark.at)
		if elapsed >= reasoningFadeDuration {
			continue
		}
		progress := max(float32(0), float32(elapsed)/float32(reasoningFadeDuration))
		active = append(active, mark)
		fades = append(fades, ReasoningFadeSpan{Start: mark.start, End: mark.end, Alpha: n.EaseOut(progress)})
	}
	s.marks = active
	return ReasoningTextFrame{
		Visible:   text[:s.shown],
		Fades:     fades,
		Animating: s.shown < len(text) || len(fades) > 0,
	}
}

// ReasoningSpans turns a ReasoningTextFrame into RichText spans that fade only
// the newly revealed tail while keeping earlier bytes settled.
func ReasoningSpans(frame ReasoningTextFrame, color n.Color) []n.Span {
	if len(frame.Fades) == 0 {
		return []n.Span{{Text: frame.Visible}}
	}
	spans := make([]n.Span, 0, len(frame.Fades)*2+1)
	from := 0
	for _, fade := range frame.Fades {
		if fade.Start > from {
			spans = append(spans, n.Span{Text: frame.Visible[from:fade.Start]})
		}
		spans = append(spans, n.Span{Text: frame.Visible[fade.Start:fade.End], Color: color.Alpha(fade.Alpha)})
		from = fade.End
	}
	if from < len(frame.Visible) {
		spans = append(spans, n.Span{Text: frame.Visible[from:]})
	}
	return spans
}

package main

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) sentMessageBody(c *ui.Context, parent *mygo.Window, session domain.Session, runs []mobileclient.SentMessageRun,
	text string, mark *markdown.RangeDecoration) {
	p := palette(c)
	size := float32(a.model.Preferences.ReplySize)
	if len(runs) == 1 && !runs[0].Inline() && !mobileMessageURL.MatchString(runs[0].Text) {
		if runs[0].Text != "" {
			el := label(c, runs[0].Text, size, p.ink).LineHeight(transcriptLeading).Selectable()
			decorateTranscriptText(c, el, text, mark, nil)
		}
		return
	}
	refByOffset := make(map[int]mobileclient.SentMessageRun)
	drawByOffset := make(map[int]func(*ui.Painter, ui.Rect))
	scale := c.Preferences().TextScale
	font := ui.Font{Size: size * scale}
	tagFont := ui.Font{Size: (controlTextSize - 1) * scale}
	column := ui.Column(c).Selectable()
	column.Children(func() {
		body := ui.RichText(c).Label(sentMessageText(runs)).FontSize(font.Size).
			FixedLineHeight(transcriptLeading * font.Size).TextColor(p.ink).Selectable()
		body.Children(func() {
			offset := 0
			for i, run := range runs {
				part := c.Key(i)
				if !run.Inline() {
					a.messageTextLinks(part, parent, session, run.Text)
					offset += utf8.RuneCountInString(run.Text)
					continue
				}
				icon := terminalSymbol
				if run.Goal {
					icon = goalSymbol
				} else if run.Attachment != nil {
					icon = filesSymbol
					if run.Attachment.Kind == domain.DiffCommentResource {
						icon = conversationsSymbol
					}
				}
				span, paint := sentReferenceChip(run.Text, font, tagFont, icon)
				refByOffset[offset] = run
				drawByOffset[offset] = func(painter *ui.Painter, rect ui.Rect) { paint(painter, rect, p) }
				if run.Attachment != nil && parent != nil && session.ID != "" {
					link := ui.Link(part, "", "").Role(ui.RoleButton).Label("预览引用：" + run.Text)
					link.Children(func() { ui.RichText(part, span) })
					if link.Clicked() {
						a.attachmentPage(parent, session, *run.Attachment, false)
					}
				} else {
					ui.RichText(part, span)
				}
				offset++
			}
		})
		body.DrawObjects(func(p *ui.Painter, offset int, rect ui.Rect) {
			if draw := drawByOffset[offset]; draw != nil {
				draw(p, rect)
			}
		})
		decorateTranscriptText(c, body, text, mark, func(offset int, end bool) int {
			source, display := 0, 0
			for _, run := range runs {
				if run.Goal {
					length := utf8.RuneCountInString(run.Text)
					if offset < source+length || end && offset == source+length {
						if end && offset > source {
							return display + 1
						}
						return display
					}
					source += length
					display++
					continue
				}
				if run.Inline() {
					if end && source == offset {
						return display
					}
					display++
					continue
				}
				length := utf8.RuneCountInString(run.Text)
				if offset < source+length || end && offset == source+length {
					return display + offset - source
				}
				source += length
				display += length
			}
			return display
		})
	})
	column.CopyData(func(parts []ui.SelectedText) transfer.Data {
		var out strings.Builder
		for i, part := range parts {
			if i != 0 {
				out.WriteByte('\n')
			}
			offset := part.Start
			text := []rune(part.Text)
			for _, r := range text[part.Start:part.End] {
				if ref, exists := refByOffset[offset]; exists {
					out.WriteString(ref.Text)
				} else {
					out.WriteRune(r)
				}
				offset++
			}
		}
		return transfer.TextData(out.String())
	})
}

func sentReferenceChip(name string, font, tagFont ui.Font, icon *ui.SVG) (ui.Span, func(*ui.Painter, ui.Rect, colors)) {
	const maxLabelWidth = 156
	layout := ui.ShapeText(name, tagFont, 0)
	width, _ := layout.Size()
	if width > maxLabelWidth {
		glyphs := []rune(name)
		end := sort.Search(len(glyphs), func(i int) bool {
			w, _ := ui.ShapeText(string(glyphs[:i+1])+"…", tagFont, 0).Size()
			return w > maxLabelWidth
		})
		layout = ui.ShapeText(string(glyphs[:end])+"…", tagFont, 0)
		width, _ = layout.Size()
	}
	const inset, gap float32 = 5, 4
	height := max(19, tagFont.Size+5)
	iconSize := min(17, tagFont.Size)
	measure := "Hg"
	for _, r := range name {
		if unicode.Is(unicode.Han, r) {
			measure = "国"
			break
		}
	}
	textInk := tagFont.InkBounds(measure)
	messageInk := font.InkBounds(measure)
	textTop := height/2 - (textInk.Y + textInk.H/2) - tagFont.Metrics().Ascent
	descent := max(0, height/2+messageInk.Y+messageInk.H/2)
	span := ui.Span{Text: string(richtext.ObjectReplacement), ObjectWidth: width + iconSize + gap + inset*2,
		ObjectHeight: height - descent, ObjectDescent: descent}
	return span, func(painter *ui.Painter, r ui.Rect, p colors) {
		painter.Fill(r, p.canvas, 5)
		paintCenteredSymbol(painter, icon, ui.Rect{X: r.X + inset, Y: r.Y + (r.H-iconSize)/2, W: iconSize, H: iconSize}, p.secondary)
		painter.TextLayout(layout, r.X+inset+iconSize+gap, r.Y+textTop, p.ink)
	}
}

func sentMessageText(runs []mobileclient.SentMessageRun) string {
	var out strings.Builder
	for _, run := range runs {
		out.WriteString(run.Text)
	}
	return out.String()
}

func queueMessagePreview(c *ui.Context, input domain.Submission) {
	runs := mobileclient.SentMessageRuns(domain.Item{
		Text: input.Text, Attachments: input.Attachments, Skills: input.Skills, ReferencePositions: input.ReferencePositions,
	})
	p := palette(c)
	readable := strings.Join(strings.Fields(sentMessageText(runs)), " ")
	if len(runs) == 1 && runs[0].Attachment == nil && runs[0].Skill == nil {
		oneLine(c, readable, 14, p.ink)
		return
	}
	scale := c.Preferences().TextScale
	font, tagFont := ui.Font{Size: 14 * scale}, ui.Font{Size: 12 * scale}
	drawAt := make(map[int]func(*ui.Painter, ui.Rect))
	body := ui.RichText(c).Label(readable).FontSize(font.Size).TextColor(p.ink).
		SingleLine().Ellipsis("…")
	body.Children(func() {
		offset := 0
		for i, run := range runs {
			if run.Attachment == nil && run.Skill == nil {
				text := strings.NewReplacer("\n", " ", "\r", "", "\t", " ").Replace(run.Text)
				ui.RichText(c.Key(i), ui.Span{Text: text})
				offset += utf8.RuneCountInString(text)
				continue
			}
			symbol := terminalSymbol
			if run.Attachment != nil {
				symbol = filesSymbol
			}
			span, draw := sentReferenceChip(run.Text, font, tagFont, symbol)
			ui.RichText(c.Key(i), span)
			drawAt[offset] = func(painter *ui.Painter, rect ui.Rect) { draw(painter, rect, p) }
			offset++
		}
	})
	body.DrawObjects(func(p *ui.Painter, offset int, rect ui.Rect) {
		if draw := drawAt[offset]; draw != nil {
			draw(p, rect)
		}
	})
}

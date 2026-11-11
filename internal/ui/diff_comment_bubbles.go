package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"path"
	"shioji/internal/app"
	"shioji/internal/domain"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// One progress value drives all actual heights, including reversals.
type commentBubbleGeometry struct{ body, footer, shell, errorHeight float32 }
type commentBubbleMotion struct {
	from, target, shown commentBubbleGeometry
	initialized         bool
}

func commentDraftFromRecord(state *changesState, ref app.DraftAttachment, record domain.DiffCommentRecord) *diffCommentDraft {
	d := &diffCommentDraft{UIKey: ref.Attachment.ID, Original: record.Content.Comment, Text: record.Content.Comment, Snapshot: record.Snapshot, Versions: state.Versions, Editing: ref.Attachment}
	for _, part := range record.Content.Fragments {
		side := editor.DiffNew
		if part.Side == "base" {
			side = editor.DiffOld
		}
		d.Selection.Fragments = append(d.Selection.Fragments, editor.DiffTextFragment{Side: side, Start: part.Start, End: part.End, StartLine: part.StartLine, EndLine: part.EndLine, Text: part.Text})
	}
	return d
}

func commentDraftParts(d *diffCommentDraft) []domain.CodeSelection {
	parts := make([]domain.CodeSelection, 0, len(d.Selection.Fragments))
	for _, p := range d.Selection.Fragments {
		side := "target"
		if p.Side == editor.DiffOld {
			side = "base"
		}
		parts = append(parts, domain.CodeSelection{Side: side, Start: p.Start, End: p.End, Text: p.Text})
	}
	return parts
}

func appendCommentAnnotation(s *changesState, d *diffCommentDraft, height float32) {
	if height <= 0 || len(d.Selection.Fragments) == 0 {
		return
	}
	last := d.Selection.Fragments[len(d.Selection.Fragments)-1]
	s.Diff.Annotations = append(s.Diff.Annotations, editor.DiffAnnotation{Side: last.Side, Line: last.EndLine - 1, Height: height, Key: d.UIKey})
}

func diffCommentBubbleHeight(c *n.Context, t *n.Theme, d *diffCommentDraft, present bool, width float32) float32 {
	if width <= 0 {
		width = 420
	}
	width = max(1, width-50)
	if d.MeasureText != d.Text || d.MeasureWidth != width || d.MeasuredHeight == 0 {
		d.MeasureText, d.MeasureWidth = d.Text, width
		d.MeasuredHeight = float32(max(1, len(n.ShapeText(d.Text, n.Font{Family: t.Font, Size: 13, Weight: 400}, width).Lines()))) * 20
	}
	target := commentBubbleGeometry{}
	if present {
		target = commentBubbleGeometry{body: min(120, d.MeasuredHeight), shell: 52}
		if d.Open {
			target.body = max(40, target.body)
			target.footer = 32
			if d.Error != "" {
				target.footer += 24
				target.errorHeight = 24
			}
		}
	}
	clock := n.Column(c.Key("comment-bubble-motion:"+d.UIKey)).Absolute().Size(0, 0).PassThrough()
	m := &d.Bubble
	if !m.initialized {
		m.initialized = true
		clock.Jump("progress", 0)
	}
	if target != m.target {
		m.from = m.shown
		m.target = target
		clock.Jump("progress", 0)
	}
	p := clock.AnimateTo("progress", float32(1), motionAnimation(c, 220*time.Millisecond))
	mix := func(a, b float32) float32 { return a + (b-a)*p }
	m.shown = commentBubbleGeometry{mix(m.from.body, target.body), mix(m.from.footer, target.footer), mix(m.from.shell, target.shell), mix(m.from.errorHeight, target.errorHeight)}
	return m.shown.body + m.shown.footer + m.shown.shell
}

func cancelDiffComment(d *diffCommentDraft) {
	d.Open = false
	d.Closing = true
	d.Error = ""
	d.Text = d.Original
}

func (v *View) locateVisibleDiffComment(state *changesState, part editor.DiffTextFragment) {
	state.LocateFragment = &part
	state.Locating = true
	state.LocateSequence++
}

func (v *View) diffCommentBubble(c *n.Context, t *n.Theme, state *changesState, d *diffCommentDraft) {
	g := d.Bubble.shown
	// Direct editor callers (including initial layout) use the same geometry.
	if !d.Bubble.initialized {
		diffCommentBubbleHeight(c, t, d, true, state.CommentWidth)
		g = d.Bubble.shown
	}
	n.Column(c.Key("comment-shell:"+d.UIKey)).Fill().Clip().Padding(6, 12).Children(func() {
		panel := n.Column(c.Key("panel")).FillWidth().Height(max(0, g.body+g.footer+40)).Shrink(0).Padding(6, 12).Radius(8).Border(1, t.Border).Background(t.Background).Clip().Label("代码评论编辑器")
		panel.Children(func() {
			parts := commentDraftParts(d)
			file := state.Path
			if d.Location == "" {
				d.Location = diffCommentLocation(file, parts, d.Versions)
			}
			location := d.Location
			name := path.Base(file)
			position := strings.TrimPrefix(location, name)
			side := "目标侧"
			for i, p := range parts {
				if i == 0 && p.Side == "base" {
					side = "基准侧"
				}
				if i > 0 && p.Side != parts[0].Side {
					side = "基准与目标"
				}
			}
			n.Row(c.Key("header")).FillWidth().Height(22).Shrink(0).AlignItems(n.Center).Gap(4).Children(func() {
				locator := n.ButtonBase(c.Key("reference")).MinWidth(0).Shrink(1).Height(22).Padding(2, 6).Radius(4).Background(t.Surface).Label(location).Tooltip(file + position + " · " + side + " · " + diffEndpointShortDescription(state.Comparison.Base) + " → " + diffEndpointShortDescription(state.Comparison.Target))
				color := textHoverColor(c, locator, t.TextMuted)
				locator.Children(func() {
					n.Text(c, name).FontSize(11).FixedLineHeight(18).TextColor(color).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…")
					n.Text(c, position).FontSize(11).FixedLineHeight(18).TextColor(color).Shrink(0)
				})
				if locator.Clicked() && len(d.Selection.Fragments) > 0 {
					v.locateVisibleDiffComment(state, d.Selection.Fragments[0])
				}
				n.Spacer(c).Grow(1)
				n.Row(c.Key("reading-actions")).Width(48).Height(22).Gap(4).Opacity(1 - min(1, g.footer/32)).Children(func() {
					if iconButton(c, queueEditIcon, "编辑评论").Size(22, 22).Disabled(d.Open || d.Editing.ID == "").Clicked() {
						v.editDiffComment(state, app.DraftAttachment{Attachment: d.Editing}, domain.DiffCommentRecord{Content: domain.CodeCommentContent{Comment: d.Original}})
					}
					if iconButton(c, closeIcon, "移除评论").Size(22, 22).Disabled(d.Open || d.Editing.ID == "").Clicked() {
						v.removeCommentReference(c, d.Editing.ID)
					}
				})
			})
			n.Column(c.Key("body")).FillWidth().Height(g.body+4).Shrink(0).Padding(4, 0, 0, 0).Clip().Children(func() {
				field := n.TextAreaBase(c.Key("comment-text"), &d.Text).FillWidth().Height(max(20, g.body)).Padding(0).FontSize(13).FixedLineHeight(20).ReadOnly(!d.Open || d.Saving).FocusRing(false).Label("代码评论内容").Placeholder("添加评论…")
				if !d.Open {
					field.CaretWidth(0)
				}
				if d.Focus {
					field.Focus()
					d.Focus = false
				}
				composing := field.Composing()
				field.OnTextKey(func(k n.TextKey) bool {
					if !d.Open || d.Saving || composing {
						return false
					}
					if k.Key == n.KeyEscape {
						cancelDiffComment(d)
						return true
					}
					if k.Key == n.KeyEnter && k.Mods == n.Cmd {
						v.saveDiffComment(c, state, d)
						return true
					}
					return false
				})
				field.Changed()
			})
			n.Column(c.Key("footer")).FillWidth().Height(g.footer).Shrink(0).Clip().Opacity(min(1, g.footer/32)).Children(func() {
				n.Column(c.Key("error")).FillWidth().Height(g.errorHeight).Shrink(0).Clip().Children(func() { n.Text(c, d.Error).FontSize(11).FixedLineHeight(20).Height(24).TextColor(t.Danger) })
				n.Row(c).FillWidth().Height(32).Padding(4, 0, 0, 0).Gap(6).AlignItems(n.Center).Justify(n.End).Children(func() {
					if ghost(c, "取消").Height(28).Label("取消评论").Disabled(!d.Open || d.Saving).Clicked() {
						cancelDiffComment(d)
					}
					label := "加入消息"
					if d.Editing.ID != "" {
						label = "保存评论"
					}
					if d.Saving {
						label = "保存中…"
					}
					if ghost(c, label).Label("提交代码评论").Height(28).PaddingX(8).Radius(5).Background(t.Text).TextColor(t.Background).Disabled(!d.Open || d.Saving || d.Retaining || strings.TrimSpace(d.Text) == "" || v.Controller == nil).Clicked() {
						v.saveDiffComment(c, state, d)
					}
				})
			})
		})
	})
}

func commentSourceLocation(text string, offset int) (int, int) {
	offset = max(0, min(offset, len(text)))
	prefix := text[:offset]
	start := strings.LastIndexByte(prefix, '\n') + 1
	return strings.Count(prefix, "\n") + 1, utf8.RuneCountInString(prefix[start:]) + 1
}

func diffCommentLocation(file string, parts []domain.CodeSelection, versions domain.FileVersions) string {
	var ranges []string
	for _, part := range parts {
		raw := versions.New
		if part.Side == "base" {
			raw = versions.Old
		}
		sl, sc := commentSourceLocation(raw, part.Start)
		el, ec := commentSourceLocation(raw, part.End)
		ranges = append(ranges, fmt.Sprintf("%d:%d–%d:%d", sl, sc, el, ec))
	}
	return path.Base(file) + ":" + strings.Join(ranges, ", ")
}

func compactDiffCommentTag(name string, font n.Font) string {
	width := func(s string) float32 { w, _ := n.ShapeText(s, font, 0).Size(); return w }
	if width(name) <= 180 {
		return name
	}
	at := strings.IndexByte(name, ':')
	if at < 0 {
		return name
	}
	elide := func(text string, budget float32) string {
		if width(text) <= budget {
			return text
		}
		runes := []rune(text)
		lo, hi := 0, len(runes)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if width(string(runes[:mid])+"…") <= budget {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return string(runes[:lo]) + "…"
	}
	position := elide(name[at:], 150)
	return elide(name[:at], 180-width(position)) + position
}

func diffCommentHighlights(parts []domain.CodeSelection, dark bool) []editor.DiffTextHighlight {
	color := n.RGBA(233, 180, 38, .25)
	if dark {
		color = n.RGBA(233, 180, 38, .28)
	}
	var out []editor.DiffTextHighlight
	for _, part := range parts {
		side := editor.DiffNew
		if part.Side == "base" {
			side = editor.DiffOld
		}
		out = append(out, editor.DiffTextHighlight{Side: side, Start: part.Start, End: part.End, Color: color})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Side != out[j].Side {
			return out[i].Side < out[j].Side
		}
		return out[i].Start < out[j].Start
	})
	merged := out[:0]
	for _, h := range out {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if h.Side == last.Side && h.Start <= last.End {
				last.End = max(last.End, h.End)
				continue
			}
		}
		merged = append(merged, h)
	}
	return merged
}

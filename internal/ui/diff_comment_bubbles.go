package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"path"
	"shioji/internal/domain"
	"sort"
	"strings"
	"unicode/utf8"
)

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

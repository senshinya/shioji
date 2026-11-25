package codex

import (
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

func (n *Normalizer) appendReasoningSummary(key string, p wire.Params) (domain.Item, bool) {
	item, exists := n.items[key]
	if !exists || !domain.ReasoningSummary(item) || item.Final || item.Truncated ||
		p.Delta == "" || p.SummaryIndex == nil || *p.SummaryIndex < 0 || *p.SummaryIndex >= 64 {
		return domain.Item{}, false
	}
	index := *p.SummaryIndex
	remaining := 96 << 10
	for _, part := range item.Parts {
		remaining -= len(part.Text)
	}
	partBudget := 16 << 10
	if index < len(item.Parts) {
		partBudget -= len(item.Parts[index].Text)
	}
	size := max(0, min(len(p.Delta), remaining, partBudget))
	item.Truncated = size < len(p.Delta)
	if size > 0 {
		// Published items can share the prior slice. Copy it before updating a
		// paragraph, and keep the native index for every subsequent fragment.
		item.Parts = slices.Clone(item.Parts)
		if index >= len(item.Parts) {
			item.Parts = append(item.Parts, make([]domain.ContentPart, index+1-len(item.Parts))...)
		}
		part := &item.Parts[index]
		part.Kind = "text"
		part.Text += strings.ToValidUTF8(p.Delta[:size], "")
		limitItem(&item)
	}
	n.items[key] = item
	return item, true
}

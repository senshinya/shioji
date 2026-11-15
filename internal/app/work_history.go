package app

import (
	"context"
	"errors"

	"shioji/internal/domain"
)

// Reuse round-window assembly and its snapshot/progress checks, preserving
// child item revisions and metadata outside the main conversation projection.
func readWorkHistory(ctx context.Context, q domain.WorkHistoryQuery, read func(context.Context, domain.WorkHistoryQuery) (domain.WorkDetail, error)) (domain.WorkDetail, error) {
	var out domain.WorkDetail
	entries := map[string]domain.WorkItem{}
	d, err := readRoundHistory(ctx, q.HistoryQuery, func(ctx context.Context, h domain.HistoryQuery) (domain.SessionDetail, error) {
		page, err := read(ctx, domain.WorkHistoryQuery{HistoryQuery: h, ID: q.ID})
		if err != nil {
			return domain.SessionDetail{}, err
		}
		if page.Work.ID != q.ID || page.SessionID != q.SessionID || q.ThreadID != "" && page.ThreadID != q.ThreadID {
			return domain.SessionDetail{}, errors.New("子 Agent 历史与会话不匹配")
		}
		if out.Work.ID == "" {
			out = page
		}
		converted := domain.SessionDetail{Session: domain.Session{ID: page.SessionID, ThreadID: page.ThreadID}, History: page.History, Sequence: page.Sequence}
		for _, entry := range page.Items {
			entries[entry.Item.ID] = entry
			converted.Items = append(converted.Items, entry.Item)
		}
		return converted, nil
	})
	if err != nil {
		return domain.WorkDetail{}, err
	}
	out.History, out.Items = d.History, nil
	for _, item := range d.Items {
		out.Items = append(out.Items, entries[item.ID])
	}
	return out, nil
}

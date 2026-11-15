package app

import (
	"context"
	"errors"
	"sort"

	"shioji/internal/domain"
)

// Chunks use one authenticated connection and one worker-owned read snapshot.
// Byte boundaries stay below transport limits without becoming UI page edges.
func readRoundHistory(ctx context.Context, q domain.HistoryQuery, read func(context.Context, domain.HistoryQuery) (domain.SessionDetail, error)) (domain.SessionDetail, error) {
	out, err := read(ctx, q)
	if err != nil {
		return out, err
	}
	if out.History == nil || out.Session.ID != q.SessionID {
		return out, errors.New("设备返回了无效的会话历史")
	}
	if out.History.WindowFirst == 0 {
		return out, nil
	}
	h := *out.History
	out.History = &h
	if h.First != h.WindowFirst || h.Last < h.First || h.Last > h.WindowLast {
		return out, errors.New("历史窗口边界无效")
	}
	items := map[string]domain.Item{}
	groups := map[string]domain.HistoryToolGroup{}
	requests := map[string]domain.PendingRequest{}
	turns := map[string]domain.Turn{}
	var requestOrder, turnOrder []string
	collect := func(d domain.SessionDetail) {
		for _, item := range d.Items {
			items[item.ID] = item
		}
		for _, group := range d.History.Groups {
			groups[group.ID] = group
		}
		for _, request := range d.Requests {
			if _, seen := requests[request.ID]; !seen {
				requestOrder = append(requestOrder, request.ID)
			}
			requests[request.ID] = request
		}
		for _, turn := range d.Turns {
			if _, seen := turns[turn.ID]; !seen {
				turnOrder = append(turnOrder, turn.ID)
			}
			turns[turn.ID] = turn
		}
	}
	collect(out)
	q.ThreadID = out.Session.ThreadID
	q.Before, q.AroundID = 0, ""
	q.WindowFirst, q.WindowLast = h.WindowFirst, h.WindowLast
	for h.Last < h.WindowLast {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		q.After = h.Last
		var page domain.SessionDetail
		page, err = read(ctx, q)
		if err != nil {
			return out, err
		}
		if page.History == nil || page.Session.ID != out.Session.ID || page.Session.ThreadID != out.Session.ThreadID || page.Sequence != out.Sequence || page.History.WindowFirst != h.WindowFirst || page.History.WindowLast != h.WindowLast || page.History.Last <= h.Last || page.History.Last > h.WindowLast {
			return out, errors.New("历史快照已改变，请重新加载")
		}
		collect(page)
		h.Last = page.History.Last
	}
	out.Items, h.Groups = nil, nil
	out.Requests, out.Turns = nil, nil
	for _, item := range items {
		out.Items = append(out.Items, item)
	}
	for _, group := range groups {
		h.Groups = append(h.Groups, group)
	}
	for _, id := range requestOrder {
		out.Requests = append(out.Requests, requests[id])
	}
	for _, id := range turnOrder {
		out.Turns = append(out.Turns, turns[id])
	}
	sort.SliceStable(out.Turns, func(i, j int) bool { return out.Turns[i].Order < out.Turns[j].Order })
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Order < out.Items[j].Order })
	sort.Slice(h.Groups, func(i, j int) bool { return h.Groups[i].First < h.Groups[j].First })
	out.HasMore = h.Older
	return out, nil
}

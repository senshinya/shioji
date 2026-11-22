package main

import (
	"slices"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

// The list builds only visible rows. Keep the history-wide projection out of
// each scroll frame, while rebuilding it for a model update or disclosure.
type mobileTranscriptStamp struct {
	sequence, snapshot      int64
	history                 *domain.HistoryWindow
	items                   *domain.Item
	turns                   *domain.Turn
	requests                *domain.PendingRequest
	works                   *domain.WorkActivity
	itemCount, turnCount    int
	requestCount, workCount int
	disclosures             uint64
	latest, observe, fold   bool
	connected, loaded       bool
}

type mobileTranscriptProjection struct {
	stamp          mobileTranscriptStamp
	items          []domain.Item
	rows           []mobileclient.TranscriptRow
	groups         map[int]mobileclient.ToolGroup
	turns          map[string]domain.Turn
	liveCompaction string
	replyOrder     int64
	lastReply      domain.Item
	hasQuestions   bool
}

func firstTranscriptEntry[T any](entries []T) *T {
	if len(entries) == 0 {
		return nil
	}
	return &entries[0]
}

func (a *mobileApp) transcriptProjection(session domain.Session, detail domain.SessionDetail, compaction *mobileCompaction,
	timeline *mobileToolTimeline, latest, observe, cache bool) *mobileTranscriptProjection {
	stamp := mobileTranscriptStamp{
		sequence: a.model.Sequence, snapshot: detail.Sequence, history: detail.History,
		items: firstTranscriptEntry(detail.Items), turns: firstTranscriptEntry(detail.Turns),
		requests: firstTranscriptEntry(detail.Requests), works: firstTranscriptEntry(detail.Works),
		itemCount: len(detail.Items), turnCount: len(detail.Turns), requestCount: len(detail.Requests), workCount: len(detail.Works),
		disclosures: timeline.disclosureVersion, latest: latest, observe: observe, fold: a.model.Preferences.CollapseLatestTools,
		connected: a.model.Connected, loaded: detail.HasLoadedHistory(),
	}
	if cached := timeline.projection; cache && cached != nil && cached.stamp == stamp {
		return cached
	}
	p := &mobileTranscriptProjection{stamp: stamp}
	p.items = compaction.project(mobileclient.ConversationItems(detail), session, latest && stamp.loaded)
	p.liveCompaction = a.currentLiveCompaction(session, detail, p.items)
	p.rows = mobileclient.TranscriptRows(p.items, detail.Turns, latest && p.liveCompaction == "" && observe)
	p.groups = timeline.Groups(p.items, p.rows, detail, latest && stamp.connected, stamp.fold)
	p.turns = make(map[string]domain.Turn, len(detail.Turns))
	for _, turn := range detail.Turns {
		p.turns[turn.ID] = turn
	}
	p.hasQuestions = slices.ContainsFunc(p.items, func(item domain.Item) bool { return len(item.Questions) != 0 })
	for _, item := range p.items {
		p.replyOrder = max(p.replyOrder, item.Order)
		switch item.Kind {
		case "assistant":
			p.lastReply = item
		case "user", "command":
			p.lastReply = domain.Item{}
		}
	}
	if cache {
		timeline.projection = p
	} else {
		timeline.projection = nil
	}
	return p
}

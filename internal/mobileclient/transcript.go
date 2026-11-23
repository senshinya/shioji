package mobileclient

import (
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
)

// ConversationItems keeps work activity in its own detail pages. The
// original items remain available to the activity list and request routing.
func ConversationItems(detail domain.SessionDetail) []domain.Item {
	sources := make(map[string]bool, len(detail.Works))
	for _, work := range detail.Works {
		if work.SourceItemID != "" {
			sources[work.SourceItemID] = true
		}
	}
	var items []domain.Item
	for index, item := range detail.Items {
		if item.WorkID != "" || item.Kind == "plan" || item.Presentation == "agents" ||
			len(item.Agents) != 0 || sources[item.ID] ||
			item.Kind == "activity" && item.Presentation == "reasoning" && domain.HistorySpacer(item) ||
			item.Kind == "assistant" && strings.TrimSpace(item.Text) == "" && len(item.Parts) == 0 && len(item.Attachments) == 0 && len(item.Questions) == 0 {
			if items == nil {
				items = make([]domain.Item, 0, len(detail.Items)-1)
				items = append(items, detail.Items[:index]...)
			}
			continue
		}
		if items != nil {
			items = append(items, item)
		}
	}
	if items == nil {
		items = detail.Items
	}
	items = projectRequestReceipts(detail.Session.ID, items, detail.Requests)
	// A hook or compaction may be reported before the runtime confirms that
	// the turn was automatic. Keep that marker before the first visible item
	// of its own turn, without changing the history window's persisted order.
	var markers map[string]domain.Item
	for _, item := range items {
		if item.Presentation == domain.GoalContinuationPresentation && item.TurnID != "" {
			if markers == nil {
				markers = make(map[string]domain.Item)
			}
			markers[item.TurnID] = item
		}
	}
	if len(markers) == 0 {
		return items
	}
	ordered := make([]domain.Item, 0, len(items))
	for _, item := range items {
		if marker, found := markers[item.TurnID]; found {
			ordered = append(ordered, marker)
			delete(markers, item.TurnID)
		}
		if item.Presentation != domain.GoalContinuationPresentation || item.TurnID == "" {
			ordered = append(ordered, item)
		}
	}
	return ordered
}

func WorkConversationItems(detail domain.WorkDetail, requests []domain.PendingRequest) []domain.Item {
	items := make([]domain.Item, len(detail.Items))
	for i, entry := range detail.Items {
		items[i] = entry.Item
	}
	return projectRequestReceipts(detail.SessionID, items, requests)
}

type requestReceiptKey struct{ id, work, turn string }
type requestReceipt struct {
	kind  string
	parts []domain.ContentPart
}

// Keep the journal order untouched. Older pages and Workers can deliver a
// receipt without its request, so retain that row until there is an anchor.
func projectRequestReceipts(session string, items []domain.Item, requests []domain.PendingRequest) []domain.Item {
	receipts := make(map[requestReceiptKey]requestReceipt)
	for _, request := range requests {
		if request.ID != "" && (request.SessionID == "" || request.SessionID == session) && request.State == domain.RequestAnswered &&
			domain.HasInlineRequestReceipt(request.Kind) && len(request.Receipt) > 0 {
			key := requestReceiptKey{"request:" + request.ID, request.WorkID, request.TurnID}
			receipts[key] = requestReceipt{request.Kind, request.Receipt}
		}
	}
	for _, item := range items {
		if anchor := domain.InlineRequestResultAnchor(item); anchor != "" && len(item.Parts) > 0 {
			key := requestReceiptKey{anchor, item.WorkID, item.TurnID}
			if receipts[key].kind == "" {
				receipts[key] = requestReceipt{item.Presentation, item.Parts}
			}
		}
	}
	inline := make(map[requestReceiptKey]requestReceipt)
	for _, item := range items {
		if item.Kind != "request" || item.Status != string(domain.RequestAnswered) {
			continue
		}
		key := requestReceiptKey{item.ID, item.WorkID, item.TurnID}
		if receipt, ok := receipts[key]; ok {
			inline[key] = receipt
		} else if domain.HasInlineRequestReceipt(item.Presentation) && len(item.Parts) > 0 {
			inline[key] = requestReceipt{item.Presentation, item.Parts}
		}
	}
	if len(inline) == 0 {
		return items
	}
	projected := make([]domain.Item, 0, len(items))
	for _, item := range items {
		key := requestReceiptKey{item.ID, item.WorkID, item.TurnID}
		if item.Kind == "request" {
			if receipt, ok := inline[key]; ok {
				item.Presentation, item.Parts = receipt.kind, receipt.parts
			}
		} else if anchor := domain.InlineRequestResultAnchor(item); anchor != "" {
			key.id = anchor
			if inline[key].kind == item.Presentation {
				continue
			}
		}
		projected = append(projected, item)
	}
	return projected
}

// TranscriptRow covers consecutive items or an independent reply footer.
type TranscriptRow struct {
	First, End int
	Footer     string
	Receipt    ReplyReceipt
}

// ReplyReceipt scopes copy and timing to the response between accepted inputs.
// BeforeInput marks an intermediate footer. AfterInput identifies the same
// footer while it is live, interrupted by another input, or completed.
type ReplyReceipt struct {
	BeforeInput, AfterInput string
	CompletedAt             *time.Time
	DurationMS              *int64
	Reply                   []string
	ToolOutput              []string
}

type replyTurnTail struct {
	last    int
	start   *time.Time
	input   bool
	after   string
	replied bool
	reply   []string
	output  []string
}

func (t replyTurnTail) receipt() ReplyReceipt {
	return ReplyReceipt{AfterInput: t.after, Reply: t.reply, ToolOutput: t.output}
}

func acceptedReplyInput(item domain.Item) bool {
	return item.Kind == "user" && item.WorkID == "" && (item.Status == "sent" || item.Status == "")
}

func TranscriptRows(items []domain.Item, turns []domain.Turn, atLatest bool) []TranscriptRow {
	turnByID := make(map[string]domain.Turn, len(turns))
	for _, turn := range turns {
		if turn.ID != "" && turn.Presentation != "compaction" {
			turnByID[turn.ID] = turn
		}
	}
	tails := make(map[string]replyTurnTail, len(turnByID))
	var beforeInput map[int]TranscriptRow
	for i, item := range items {
		turn, ok := turnByID[item.TurnID]
		if !ok || item.WorkID != "" {
			continue
		}
		tail, seen := tails[turn.ID]
		if !seen {
			tail.start = turn.StartedAt
		}
		switch {
		case acceptedReplyInput(item):
			if tail.replied {
				receipt := tail.receipt()
				receipt.BeforeInput = item.ID
				if end := item.SentAt; end != nil && !end.IsZero() && (turn.StartedAt == nil || end.After(*turn.StartedAt)) {
					receipt.CompletedAt = end
					if tail.start != nil && !tail.start.IsZero() && !end.Before(*tail.start) {
						ms := end.Sub(*tail.start).Milliseconds()
						receipt.DurationMS = &ms
					}
				}
				if beforeInput == nil {
					beforeInput = make(map[int]TranscriptRow)
				}
				beforeInput[tail.last] = TranscriptRow{Footer: turn.ID, Receipt: receipt}
				tail.after, tail.reply, tail.output = item.ID, nil, nil
				tail.replied = false
			}
			if tail.input || tail.start == nil || item.SentAt != nil && item.SentAt.After(*tail.start) {
				tail.start = item.SentAt
			}
			tail.input = true
		case item.Kind == "assistant" && !domain.HistorySpacer(item):
			tail.replied = true
			if strings.TrimSpace(item.Text) != "" {
				tail.reply = append(tail.reply, item.Text)
			}
		case item.Kind == "tool":
			tail.replied = true
			if output := strings.TrimRight(item.Text, "\r\n"); output != "" {
				tail.output = append(tail.output, output)
			} else {
				for _, part := range item.Parts {
					if part.Text != "" {
						tail.output = append(tail.output, part.Text)
					}
				}
			}
		}
		tail.last = i
		tails[turn.ID] = tail
	}
	rows := make([]TranscriptRow, 0, len(items)+len(turns)+len(beforeInput))
	for start := 0; start < len(items); {
		item := items[start]
		end := start + 1
		if item.Kind == "tool" {
			for end < len(items) && items[end].Kind == "tool" && items[end].TurnID == item.TurnID {
				end++
			}
		}
		rows = append(rows, TranscriptRow{First: start, End: end})
		if footer, ok := beforeInput[end-1]; ok {
			rows = append(rows, footer)
		}
		if tail, ok := tails[item.TurnID]; ok && tail.last == end-1 && !turnByID[item.TurnID].Status.Active() {
			rows = append(rows, TranscriptRow{Footer: item.TurnID, Receipt: tail.receipt()})
		}
		start = end
	}
	if atLatest {
		for _, turn := range slices.Backward(turns) {
			if turn.ID != "" && turn.Presentation != "compaction" && turn.Status.Active() {
				return append(rows, TranscriptRow{Footer: turn.ID, Receipt: tails[turn.ID].receipt()})
			}
		}
	}
	return rows
}

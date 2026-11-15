package app

import (
	"shioji/internal/domain"
	"sort"
)

// The worker keeps authoritative history. Retain both open conversations and a
// small recent cache; evicting a projection never removes drafts or requests.
func (m *Model) PruneHistoryCache(visible ...string) {
	if m.historyVisits == nil {
		m.historyVisits = map[string]uint64{}
	}
	keep := map[string]bool{}
	for _, id := range visible {
		if id != "" {
			keep[id] = true
			m.historyClock++
			m.historyVisits[id] = m.historyClock
		}
	}
	type entry struct {
		ID    string
		Visit uint64
		Bytes int
	}
	inactive := []entry{}
	total := 0
	for id, d := range m.Details {
		if keep[id] {
			continue
		}
		if len(d.Items) == 0 && len(d.Turns) == 0 && d.Plan == nil && len(d.Queue.Items) == 0 {
			pending := pendingHistoryRequests(d.Requests)
			if len(pending) == 0 {
				delete(m.Details, id)
				m.evictHistoryMetadata(id)
				delete(m.historyVisits, id)
			} else {
				m.Details[id] = domain.SessionDetail{Requests: pending}
			}
			continue
		}
		size := historyBytes(d)
		total += size
		inactive = append(inactive, entry{id, m.historyVisits[id], size})
	}
	sort.Slice(inactive, func(i, j int) bool {
		if inactive[i].Visit == inactive[j].Visit {
			return inactive[i].ID < inactive[j].ID
		}
		return inactive[i].Visit < inactive[j].Visit
	})
	remaining := len(inactive)
	for _, e := range inactive {
		if remaining <= 6 && total <= 16<<20 {
			break
		}
		pending := pendingHistoryRequests(m.Details[e.ID].Requests)
		if len(pending) > 0 {
			m.Details[e.ID] = domain.SessionDetail{Requests: pending}
		} else {
			delete(m.Details, e.ID)
		}
		m.evictHistoryMetadata(e.ID)
		delete(m.historyVisits, e.ID)
		remaining--
		total -= e.Bytes
	}
}
func historyBytes(d domain.SessionDetail) int {
	size := len(d.Items) * 256
	if d.Plan != nil {
		size += len(d.Plan.Narrative) + len(d.Plan.Steps)*128
	}
	for _, q := range d.Queue.Items {
		size += len(q.Input.Text) + len(q.Input.Attachments)*512
	}
	for _, item := range d.Items {
		size += len(item.Text) + len(item.Name)
		for _, p := range item.Parts {
			size += len(p.Label) + len(p.Text)
		}
		for _, f := range item.Files {
			size += len(f.Path) + len(f.Patch)
		}
		for _, a := range item.Agents {
			size += len(a.Message)
		}
	}
	return size
}

func pendingHistoryRequests(requests []domain.PendingRequest) []domain.PendingRequest {
	var pending []domain.PendingRequest
	for _, r := range requests {
		if r.State.AwaitingResolution() {
			pending = append(pending, r)
		}
	}
	return pending
}

func (m *Model) evictHistoryMetadata(id string) {
	delete(m.HistoryReads, id)
	delete(m.HistoryOutlines, id)
	delete(m.HistoryAnchors, id)
	delete(m.HistoryOpenings, id)
}

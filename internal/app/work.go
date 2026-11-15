package app

import (
	"shioji/internal/domain"
	"sort"
)

type WorkKey struct{ DeviceID, SessionID, ID string }

func (m *Model) WorkKey(sid, id string) WorkKey { return WorkKey{m.Snapshot.DeviceID, sid, id} }

// BeginWorkRead keeps a bounded live merge target before the async snapshot is
// fetched, so a response cannot erase items observed while it was in flight.
func (m *Model) BeginWorkRead(sid, id string) WorkKey {
	return m.BeginWorkReadKey(m.WorkKey(sid, id))
}

// Projected panes reserve their read in the owning client cache.
func (m *Model) BeginWorkReadKey(key WorkKey) WorkKey {
	sid, id := key.SessionID, key.ID
	if m.WorkDetails == nil {
		m.WorkDetails = map[WorkKey]domain.WorkDetail{}
	}
	if _, ok := m.WorkDetails[key]; !ok {
		if len(m.workReads) >= 32 {
			delete(m.WorkDetails, m.workReads[0])
			m.workReads = m.workReads[1:]
		}
		d := domain.WorkDetail{SessionID: sid, Sequence: m.Sequence}
		for _, w := range m.Details[sid].Works {
			if w.ID == id {
				d.Work = w
				break
			}
		}
		m.WorkDetails[key] = d
		m.workReads = append(m.workReads, key)
	}
	return key
}
func (m *Model) MergeWorkDetail(in domain.WorkDetail) {
	m.mergeWorkDetail(in, false)
}

// Hidden children keep the bounded recent projection, not browsed pages.
func (m *Model) ReleaseWorkHistory(key WorkKey) {
	d, ok := m.WorkDetails[key]
	if !ok || d.History == nil {
		return
	}
	d.Truncated = d.Truncated || d.History.Older
	d.History = nil
	trimWorkDetail(&d)
	// Dropping a prefix must also release the original large backing array.
	d.Items = append([]domain.WorkItem(nil), d.Items...)
	m.WorkDetails[key] = d
}

func (m *Model) MergeWorkHistoryPage(in domain.WorkDetail) {
	m.mergeWorkDetail(in, true)
}

func (m *Model) mergeWorkDetail(in domain.WorkDetail, prepend bool) {
	key := m.BeginWorkRead(in.SessionID, in.Work.ID)
	cached := m.WorkDetails[key]
	if prepend && in.History != nil && cached.History != nil && cached.ThreadID == in.ThreadID {
		h := *cached.History
		if in.History.First > 0 && in.History.First < h.First {
			h.First, h.Older = in.History.First, in.History.Older
		}
		in.History = &h
		if cached.Sequence > in.Sequence {
			in.PendingInput = cached.PendingInput
		}
		for _, item := range cached.Items {
			mergeWorkItem(&in, item)
		}
	}
	if cached.Work.Revision > in.Work.Revision {
		in.Work = cached.Work
	}
	for _, item := range cached.Items {
		if item.Revision > in.Sequence {
			mergeWorkItem(&in, item)
		}
	}
	if in.History != nil && len(in.Items) > 0 {
		h := *in.History
		if h.First == 0 {
			h.First = in.Items[0].Item.Order
		}
		h.Last = max(h.Last, in.Items[len(in.Items)-1].Item.Order)
		in.History = &h
	}
	in.Sequence = max(in.Sequence, cached.Sequence)
	trimWorkDetail(&in)
	m.WorkDetails[key] = in
}
func mergeWorkItem(d *domain.WorkDetail, next domain.WorkItem) {
	for i, item := range d.Items {
		if item.Item.ID == next.Item.ID {
			if item.Revision < next.Revision {
				d.Items[i] = next
			}
			return
		}
	}
	d.Items = append(d.Items, next)
	sort.SliceStable(d.Items, func(i, j int) bool { return d.Items[i].Item.Order < d.Items[j].Item.Order })
}
func trimWorkDetail(d *domain.WorkDetail) {
	// Browsed history belongs to the open virtual list. The legacy recent-only
	// projection keeps its existing bound; it must not trim a loaded round.
	if d.History != nil {
		return
	}
	size := 0
	for _, entry := range d.Items {
		i := entry.Item
		size += len(i.Text) + len(i.Name)
		for _, p := range i.Parts {
			size += len(p.Text)
		}
		for _, f := range i.Files {
			size += len(f.Patch)
		}
	}
	for len(d.Items) > 64 || size > 384<<10 && len(d.Items) > 1 {
		i := d.Items[0].Item
		size -= len(i.Text) + len(i.Name)
		for _, p := range i.Parts {
			size -= len(p.Text)
		}
		for _, f := range i.Files {
			size -= len(f.Patch)
		}
		d.Items = d.Items[1:]
		d.Truncated = true
	}
}
func (m *Model) applyWorkEvent(e domain.Event) {
	if e.Work != nil {
		d := m.Details[e.SessionID]
		found := false
		for i, w := range d.Works {
			if w.ID == e.Work.ID {
				if w.Revision <= e.Work.Revision {
					d.Works[i] = *e.Work
				}
				found = true
				break
			}
		}
		if !found {
			d.Works = append(d.Works, *e.Work)
		}
		var truncated bool
		d.Works, truncated = domain.LimitWorkRoster(d.Works)
		d.WorksTruncated = d.WorksTruncated || truncated
		m.Details[e.SessionID] = d
		key := m.WorkKey(e.SessionID, e.Work.ID)
		if cached, ok := m.WorkDetails[key]; ok {
			if cached.Work.Revision <= e.Work.Revision {
				cached.Work = *e.Work
			}
			cached.Sequence = e.Sequence
			m.WorkDetails[key] = cached
		}
	}
	if e.WorkItem != nil {
		key := m.WorkKey(e.SessionID, e.WorkID)
		if cached, ok := m.WorkDetails[key]; ok {
			mergeWorkItem(&cached, domain.WorkItem{Item: *e.WorkItem, Revision: e.Sequence})
			if cached.History != nil {
				h := *cached.History
				if h.First == 0 {
					h.First = e.WorkItem.Order
				}
				h.Last = max(h.Last, e.WorkItem.Order)
				cached.History = &h
			}
			cached.Sequence = e.Sequence
			trimWorkDetail(&cached)
			m.WorkDetails[key] = cached
		}
	}
}

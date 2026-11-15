package app

import (
	"context"
	"fmt"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestChildRoundAssemblyRetainsRevisionsAndRejectsMixedSnapshots(t *testing.T) {
	for _, bad := range []string{"", "work", "session", "source", "sequence", "progress", "cancel"} {
		t.Run(bad, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			got, err := readWorkHistory(ctx, domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: "s", Rounds: 5}}, func(ctx context.Context, q domain.WorkHistoryQuery) (domain.WorkDetail, error) {
				calls++
				first, last := int64(1), int64(2)
				if calls > 1 {
					first, last = q.After+1, min(q.After+2, 5)
					if q.ID != "child" || q.WindowFirst != 1 || q.WindowLast != 5 || q.ThreadID != "source" {
						t.Fatal(q)
					}
				}
				d := domain.WorkDetail{SessionID: "s", ThreadID: "source", Work: domain.WorkActivity{ID: "child"}, Sequence: 20, History: &domain.HistoryWindow{First: first, Last: last, WindowFirst: 1, WindowLast: 5, Older: true}}
				for i := first; i <= last; i++ {
					d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint(i), Order: i}, Revision: 10 + i})
				}
				if calls == 2 {
					switch bad {
					case "work":
						d.Work.ID = "other"
					case "session":
						d.SessionID = "other"
					case "source":
						d.ThreadID = "other"
					case "sequence":
						d.Sequence++
					case "progress":
						d.History.Last = q.After
					}
				}
				if bad == "cancel" {
					cancel()
				}
				return d, nil
			})
			if bad != "" {
				if err == nil {
					t.Fatal("accepted inconsistent read", bad)
				}
				return
			}
			if err != nil || calls != 3 || len(got.Items) != 5 || got.Items[4].Revision != 15 || got.History.Last != 5 {
				t.Fatal(got, calls, err)
			}
		})
	}
}

func TestChildHistoryPrependKeepsBrowsedAndLiveContent(t *testing.T) {
	m := NewModel()
	m.Sequence = 100
	m.Details["s"] = domain.SessionDetail{}
	latest := domain.WorkDetail{SessionID: "s", ThreadID: "source", Work: domain.WorkActivity{ID: "child", Revision: 1}, Sequence: 100, History: &domain.HistoryWindow{First: 81, Last: 100, Older: true}}
	for i := 81; i <= 100; i++ {
		latest.Items = append(latest.Items, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint(i), Order: int64(i), Text: "current"}, Revision: 100})
	}
	m.MergeWorkDetail(latest)
	if err := m.ApplyEvent(domain.Event{Sequence: 101, SessionID: "s", Type: "work.item", WorkID: "child", WorkItem: &domain.Item{ID: "100", Order: 100, Text: "live update"}}); err != nil {
		t.Fatal(err)
	}
	old := domain.WorkDetail{SessionID: "s", ThreadID: "source", Work: domain.WorkActivity{ID: "child", Revision: 1}, Sequence: 99, History: &domain.HistoryWindow{First: 1, Last: 80, Newer: true}}
	for i := 1; i <= 80; i++ {
		old.Items = append(old.Items, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint(i), Order: int64(i)}, Revision: 99})
	}
	m.MergeWorkHistoryPage(old)
	got := m.WorkDetails[m.WorkKey("s", "child")]
	if len(got.Items) != 100 || got.Items[99].Item.Text != "live update" || got.History.First != 1 || got.History.Last != 100 || got.History.Older || got.History.Newer {
		t.Fatal("history was trimmed or live state overwritten", len(got.Items), got.History)
	}
	if len(m.Details["s"].Items) != 0 {
		t.Fatal("child history leaked into main conversation")
	}
}

func TestInactiveChildHistoryReturnsToBoundedRecentCache(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{}
	d := domain.WorkDetail{SessionID: "s", Work: domain.WorkActivity{ID: "child"}, History: &domain.HistoryWindow{First: 1, Last: 5000}}
	for i := 1; i <= 5000; i++ {
		d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint(i), Order: int64(i), Text: strings.Repeat("x", 8192)}})
	}
	m.MergeWorkDetail(d)
	m.ReleaseWorkHistory(m.WorkKey("s", "child"))
	got := m.WorkDetails[m.WorkKey("s", "child")]
	if got.History != nil || !got.Truncated || len(got.Items) > 64 || len(got.Items)*8192 > 384<<10 || cap(got.Items) > 64 || got.Items[len(got.Items)-1].Item.ID != "5000" {
		t.Fatal("inactive child retains browsed transcript", len(got.Items), cap(got.Items), got.History)
	}
	m.Sequence = 1000
	for i := 1; i <= 90; i++ {
		item := domain.Item{ID: fmt.Sprint("new-", i), Order: int64(5000 + i), Text: strings.Repeat("x", 8192)}
		if err := m.ApplyEvent(domain.Event{Sequence: int64(1000 + i), SessionID: "s", Type: "work.item", WorkID: "child", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	got = m.WorkDetails[m.WorkKey("s", "child")]
	if len(got.Items) > 64 || len(got.Items)*8192 > 384<<10 {
		t.Fatal("inactive live events escaped recent cache bound", len(got.Items))
	}
}

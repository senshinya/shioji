package app

import (
	"fmt"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestHistoryCacheEvictionKeepsVisibleConversationsAndDrafts(t *testing.T) {
	m := NewModel()
	for i := 0; i < 14; i++ {
		id := fmt.Sprint(i)
		m.Details[id] = domain.SessionDetail{Items: []domain.Item{{ID: "item", Text: strings.Repeat("x", 2<<20)}}}
		m.Drafts["session:0"] = "draft"
	}
	m.PruneHistoryCache("0", "1")
	if _, ok := m.Details["0"]; !ok {
		t.Fatal("visible left history evicted")
	}
	if _, ok := m.Details["1"]; !ok {
		t.Fatal("visible right history evicted")
	}
	if len(m.Details) > 8 || m.Drafts["session:0"] != "draft" {
		t.Fatal("history cache unbounded or draft removed", len(m.Details))
	}
}

func TestHistoryCacheEvictionKeepsPendingAttention(t *testing.T) {
	m := NewModel()
	for i := 0; i < 12; i++ {
		id := fmt.Sprint(i)
		m.Details[id] = domain.SessionDetail{Items: []domain.Item{{ID: "item", Text: strings.Repeat("x", 2<<20)}}}
	}
	d := m.Details["0"]
	d.Requests = []domain.PendingRequest{{ID: "request", SessionID: "0", State: "pending"}}
	m.Details["0"] = d
	m.PruneHistoryCache("left", "right")
	if len(m.Details["0"].Requests) != 1 || len(m.Details["0"].Items) != 0 {
		t.Fatal("attention was discarded together with heavy history")
	}
}

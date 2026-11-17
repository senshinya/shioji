package store_test

import (
	"encoding/json"
	"testing"

	"shioji/internal/domain"
)

func TestActivityStartOrderSurvivesResultsAndHistory(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "activity", WorkspaceID: w.ID, Text: "check", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(data string) int64 {
		t.Helper()
		var e domain.Event
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatal(err)
		}
		e, err = s.Apply(op.SessionID, e)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(e.Item)
		var item struct {
			ActivityOrder int64 `json:"activityOrder"`
		}
		json.Unmarshal(b, &item)
		return item.ActivityOrder
	}
	tool := apply(`{"type":"item.upsert","activityStart":true,"item":{"id":"tool","kind":"tool","turnId":"t"}}`)
	compact := apply(`{"type":"item.upsert","activityStart":true,"item":{"id":"compact","kind":"activity","presentation":"compaction","turnId":"t"}}`)
	if tool <= 0 || compact <= tool {
		t.Errorf("start order missing: tool=%d compact=%d", tool, compact)
	}
	if got := apply(`{"type":"item.upsert","item":{"id":"result-only","kind":"tool","turnId":"t","final":true}}`); got != 0 {
		t.Error("a result without a start claimed activity", got)
	}
	for _, data := range []string{
		`{"type":"item.upsert","item":{"id":"tool","kind":"tool","turnId":"t","text":"new output"}}`,
		`{"type":"item.upsert","activityStart":true,"item":{"id":"tool","kind":"tool","turnId":"t"}}`,
		`{"type":"item.upsert","item":{"id":"tool","kind":"tool","turnId":"t","final":true}}`,
	} {
		if got := apply(data); got != tool {
			t.Error("result or duplicate start changed ownership", got, tool)
		}
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d.Items)
	var items []struct {
		ID            string
		ActivityOrder int64 `json:"activityOrder"`
	}
	json.Unmarshal(b, &items)
	for _, item := range items {
		if item.ID == "compact" && item.ActivityOrder != compact {
			t.Error("history lost start order")
		}
	}
}

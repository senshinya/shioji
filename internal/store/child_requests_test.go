package store_test

import (
	"encoding/json"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
)

func TestChildRequestPersistsBothSourcesAndLatestReceipt(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, _ := db.Accept(domain.Submission{OperationID: "child-request", WorkspaceID: w.ID, Text: "检查"})
	apply := func(e domain.Event) domain.Event {
		t.Helper()
		out, err := db.Apply(op.SessionID, e)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "turn", Title: "检查边界条件", State: "running"}
	apply(domain.Event{Type: "work.upsert", Work: &work})
	initial, _ := db.Snapshot()
	detail, _ := db.Session(op.SessionID, 0, 100)
	live := app.NewModel()
	live.ApplySnapshot(initial)
	live.Details[op.SessionID] = detail
	live.BeginWorkRead(op.SessionID, "child")
	r := domain.PendingRequest{ID: "request", WorkID: "child", TurnID: "turn", ItemID: "child-item", Kind: "questions", Title: "需要回答", State: "pending", Revision: 1, Questions: []domain.Question{{ID: "q", Text: "检查根节点？"}}}
	opened := apply(domain.Event{Type: "request.open", Request: &r})
	if err := live.ApplyEvent(opened); err != nil {
		t.Fatal(err)
	}
	child, err := db.Work(op.SessionID, "child")
	if err != nil || len(child.Items) != 1 || child.Items[0].Item.Kind != "request" {
		t.Fatal("child detail lacks persistent source", child, err)
	}
	if len(live.WorkDetails[live.WorkKey(op.SessionID, "child")].Items) != 1 {
		t.Fatal("live client missed child source")
	}
	apply(domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "later", TurnID: "turn", Kind: "assistant", Text: "主会话继续", Final: true}})
	apply(domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}})
	r.State = "answered"
	r.Revision = 2
	r.Receipt = []domain.ContentPart{{Label: "检查根节点？", Text: "包含根节点"}}
	apply(domain.Event{Type: "request.resolve", Request: &r})
	result := domain.Item{ID: "request-result:request", WorkID: "child", TurnID: "turn", Kind: "request_result", Presentation: "questions", Text: "已提交回答", Final: true, Parts: r.Receipt}
	apply(domain.Event{Type: "item.upsert", Item: &result})
	child, _ = db.Work(op.SessionID, "child")
	if len(child.Items) != 2 || child.Items[0].Item.Status != "answered" || child.Items[1].Item.Kind != "request_result" {
		t.Fatal("child source or receipt lost", child)
	}
	sourceOrder := child.Items[0].Item.Order
	page, err := db.WorkHistory(domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{
		SessionID: op.SessionID, Rounds: 1, WindowFirst: sourceOrder, WindowLast: sourceOrder, After: sourceOrder - 1,
	}})
	if err != nil || len(page.Items) != 1 || page.Items[0].Item.Presentation != "questions" ||
		len(page.Items[0].Item.Parts) != 1 || page.Items[0].Item.Parts[0].Text != "包含根节点" {
		t.Fatal("the original child history page cannot show its answer without the later result page", page, err)
	}
	detail, _ = db.Session(op.SessionID, 0, 100)
	if detail.Session.Status != "completed" || detail.Items[len(detail.Items)-1].ID != "request-result:request" || detail.Requests[0].WorkID != "child" {
		t.Fatal("receipt changed parent or chronology", detail)
	}
	events, _ := db.Events(initial.Sequence, 100)
	for _, e := range events {
		if err := live.ApplyEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := json.Marshal(live.WorkDetails[live.WorkKey(op.SessionID, "child")].Items)
	want, _ := json.Marshal(child.Items)
	if string(got) != string(want) {
		t.Fatal("live detail differs from saved detail", string(got), string(want))
	}
}
func TestChildRequestCannotMoveAcrossWorkOrRoot(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	a, _ := db.Accept(domain.Submission{OperationID: "a", WorkspaceID: w.ID, Text: "a"})
	b, _ := db.Accept(domain.Submission{OperationID: "b", WorkspaceID: w.ID, Text: "b"})
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "root", State: "running"}
	db.Apply(a.SessionID, domain.Event{Type: "work.upsert", Work: &work})
	r := domain.PendingRequest{ID: "q", WorkID: "child", TurnID: "root", Kind: "approval", State: "pending", Revision: 1}
	if _, err := db.Apply(b.SessionID, domain.Event{Type: "request.open", Request: &r}); err == nil {
		t.Fatal("foreign work accepted")
	}
	r.TurnID = "wrong"
	if _, err := db.Apply(a.SessionID, domain.Event{Type: "request.open", Request: &r}); err == nil {
		t.Fatal("wrong root turn accepted")
	}
	r.TurnID = "root"
	if _, err := db.Apply(a.SessionID, domain.Event{Type: "request.open", Request: &r}); err != nil {
		t.Fatal(err)
	}
	r.WorkID = ""
	if _, err := db.Apply(a.SessionID, domain.Event{Type: "request.open", Request: &r}); err == nil {
		t.Fatal("request silently moved to parent")
	}
}

func TestChildApprovalHistoryIncludesSavedDecision(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, err := db.Accept(domain.Submission{OperationID: "child-approval", WorkspaceID: w.ID, Text: "检查"})
	if err != nil {
		t.Fatal(err)
	}
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "root", State: "running"}
	if _, err := db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	r := domain.PendingRequest{ID: "approval", WorkID: work.ID, TurnID: work.TurnID, Kind: "approval", State: "answered", Revision: 2,
		Receipt: []domain.ContentPart{{Text: "始终允许此命令"}}}
	if _, err := db.Apply(op.SessionID, domain.Event{Type: "request.resolve", Request: &r}); err != nil {
		t.Fatal(err)
	}
	child, err := db.Work(op.SessionID, work.ID)
	if err != nil || len(child.Items) != 1 {
		t.Fatal(child, err)
	}
	order := child.Items[0].Item.Order
	page, err := db.WorkHistory(domain.WorkHistoryQuery{ID: work.ID, HistoryQuery: domain.HistoryQuery{
		SessionID: op.SessionID, Rounds: 1, WindowFirst: order, WindowLast: order, After: order - 1,
	}})
	if err != nil || len(page.Items) != 1 || page.Items[0].Item.Presentation != "approval" ||
		len(page.Items[0].Item.Parts) != 1 || page.Items[0].Item.Parts[0].Text != "始终允许此命令" {
		t.Fatal("child approval page did not load the saved decision with its anchor", page, err)
	}
}

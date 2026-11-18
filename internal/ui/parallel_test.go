package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func parallelModel() *app.Model {
	m := layoutModel()
	m.WorkerMinor = 6
	other := domain.Session{ID: "other", WorkspaceID: "w", Title: "Another session", Status: "completed"}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
	m.Details["other"] = domain.SessionDetail{Session: other, HistoryLoaded: true, Items: []domain.Item{{ID: "other-reply", Kind: "assistant", Text: "Independent response"}}}
	m.Drafts["session:s"] = "left draft"
	m.Drafts["session:other"] = "right draft"
	return m
}

func TestParallelComposersRouteToTheirOwnSessions(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1600, 900)
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "git.status" && job.Apply != nil {
			job.Apply(m, json.RawMessage(`{"applicable":false}`), nil)
		}
	}
	if _, ok := ui.Find("Independent response"); !ok {
		t.Fatal("second conversation missing")
	}
	if err := ui.Click("右侧会话输入"); err != nil {
		t.Fatal(err)
	}
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("right only")
	ui.Key(0, n.KeyEnter)
	var submitted domain.Submission
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "session.submit" {
			submitted = job.Input.(domain.Submission)
		}
	}
	if submitted.SessionID != "other" || submitted.Text != "right only" || m.Drafts["session:s"] != "left draft" {
		t.Fatal("parallel send crossed session boundary", submitted)
	}
}

func TestParallelUsesOneInspectorAndClosingKeepsDrafts(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	m.FocusSecondary = true
	m.Inspectors["s"] = true
	m.Inspectors["other"] = true
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1700, 900)
	count := 0
	for _, label := range ui.Texts() {
		if label == "更改侧栏" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("multiple or missing inspectors")
	}
	if err := ui.RightClick("Another session"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("关闭并排会话"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if m.SecondarySessionID != "" || m.Drafts["session:other"] != "right draft" {
		t.Fatal("closing split deleted draft")
	}
}

func TestParallelSelectionIsPersisted(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	m.FocusSecondary = true
	b, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	json.Unmarshal(b, &state)
	if state["secondarySessionId"] != "other" || state["focusSecondary"] != true {
		t.Fatal("parallel layout not persisted")
	}
}

func TestParallelKeyboardFocusSelectsItsInspector(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1600, 900)
	ui.Click("消息输入")
	for i := 0; i < 80 && !ui.Focused("右侧会话输入"); i++ {
		ui.Key(0, n.KeyTab)
	}
	if !ui.Focused("右侧会话输入") || !m.FocusSecondary {
		t.Fatal("keyboard focus and active pane diverged")
	}
}

func TestFlushCollectsSecondaryBindingsBeforeSavingRoot(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	v := New(m, nil, "")
	v.prepareSecondary()
	v.secondary.draftBindings["session:other"] = &editBinding{Text: "last keystroke", LastModel: "right draft"}
	v.secondary.answerBindings["other/request/q"] = &editBinding{Text: "last answer", LastModel: ""}
	if err := v.Flush(); err != nil {
		t.Fatal(err)
	}
	if m.Drafts["session:other"] != "last keystroke" || m.Answers["other/request/q"] != "last answer" {
		t.Fatal("secondary input lost at quit")
	}
}

func TestUnresolvedSecondaryTargetCannotOperateOldConversation(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1600, 900)
	m.SecondarySessionID = "new-search-result"
	ui.Frame()
	if _, ok := ui.Find("右侧会话输入"); ok {
		t.Fatal("stale secondary composer remained operable")
	}
}

func TestNarrowParallelSwitchKeepsBothDrafts(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 900, 650)
	ui.Click("Another session")
	if _, ok := ui.Find("右侧会话输入"); !ok {
		t.Fatal("narrow switch did not reveal right conversation")
	}
	if err := ui.Click("Layout"); err != nil {
		t.Fatal("right pane has no way to switch back", err)
	}
	if _, ok := ui.Find("消息输入"); !ok {
		t.Fatal("switch back failed")
	}
	if m.SecondarySessionID != "other" || m.Drafts["session:s"] != "left draft" || m.Drafts["session:other"] != "right draft" {
		t.Fatal("narrow switch changed layout or drafts")
	}
}

func TestSecondaryQuestionSubmissionKeepsOtherRequestAndDraft(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	for _, id := range []string{"s", "other"} {
		d := m.Details[id]
		d.Requests = []domain.PendingRequest{{ID: id + "-request", SessionID: id, Kind: "form", State: "pending", Revision: 1, Fields: []domain.FormField{{ID: "answer", Title: "Answer " + id, Type: "string", Required: true}}}}
		m.Details[id] = d
	}
	left := m.Details["s"]
	left.Requests[0].Kind = "questions"
	left.Requests[0].Questions = []domain.Question{{ID: "left", Text: "Left question", Custom: true}}
	left.Requests[0].Fields = nil
	m.Details["s"] = left
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1600, 900)
	ui.Click("Answer other")
	ui.Type("right answer")
	if err := ui.Click("确认表单"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "request.respond" {
			a := job.Input.(protocol.AnswerInput)
			if a.SessionID != "other" || a.Answer.RequestID != "other-request" || a.Answer.Answers["answer"][0] != "right answer" {
				t.Fatal("request crossed pane boundary", a)
			}
			found = true
		}
	}
	if !found || m.Details["s"].Requests[0].State != "pending" || m.Drafts["session:s"] != "left draft" {
		t.Fatal("right form changed left request or composer")
	}
}

func TestAttentionExpandsWorkspaceAfterHeavyHistoryEviction(t *testing.T) {
	m := parallelModel()
	d := m.Details["other"]
	d.Items = nil
	d.Requests = []domain.PendingRequest{{ID: "pending", SessionID: "other", Kind: "questions", State: "pending", Questions: []domain.Question{{ID: "q", Text: "Answer", Custom: true}}}}
	m.Details["other"] = d
	v := New(m, nil, "")
	v.collapsedWorkspaces["w"] = true
	v.nextAttention()
	ui := n.NewTester(v.View, 1440, 900)
	if row, ok := ui.Find("Another session"); !ok || row.X >= 256 {
		t.Fatal("attention left target workspace collapsed")
	}
	if m.SessionID != "other" || m.Drafts["session:s"] != "left draft" {
		t.Fatal("attention navigation lost draft or target")
	}
}

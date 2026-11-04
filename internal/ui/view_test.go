package ui

import (
	"encoding/json"
	"fmt"
	native "github.com/egoist/mygo/ui"
	"math"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestPendingQuestionKeepsOrdinaryComposerVisible(t *testing.T) {
	m := app.NewModel()
	m.Connected = true
	m.WorkspaceID = "w"
	m.SessionID = "s"
	m.Readiness = domain.Readiness{State: "ready", Permissions: []string{"read-only"}, Models: []domain.Model{{ID: "test", Default: true}}}
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Name: "Example"}}, Sessions: []domain.Session{{ID: "s", WorkspaceID: "w", Title: "Task", Status: "running"}}}
	m.Details["s"] = domain.SessionDetail{Session: m.Snapshot.Sessions[0], Requests: []domain.PendingRequest{{ID: "r", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: strings.Repeat("A long question. ", 80), Custom: true}}}}}
	view := New(m, nil, "")
	test := native.NewTester(view.View, 1000, 650)

	rect, ok := test.Find("消息输入")
	if !ok || rect.Y+rect.H > 650 || rect.H < 40 {
		t.Fatalf("ordinary composer hidden: %+v %v", rect, ok)
	}
}
func TestHomeDoesNotCreateSessionWhileRendering(t *testing.T) {
	m := app.NewModel()
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1000, 700)

	if len(m.Snapshot.Sessions) != 0 || len(m.Pending) != 0 {
		t.Fatal("render created an empty session")
	}
	if _, ok := test.Find("创建工作区"); !ok {
		t.Fatal("no workspace setup action")
	}
}

func TestUnknownExecutionIsNotShownAsRunningOrCompleted(t *testing.T) {
	m := app.NewModel()
	m.SessionID = "s"
	m.WorkspaceID = "w"
	s := domain.Session{ID: "s", WorkspaceID: "w", Title: "Needs reconciliation", Status: "unknown"}
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Name: "Test"}}, Sessions: []domain.Session{s}}
	m.Details["s"] = domain.SessionDetail{Session: s, Items: []domain.Item{{ID: "i", TurnID: "t", Kind: "assistant", Text: "Partial"}}, Turns: []domain.Turn{{ID: "t", Status: "unknown"}}}
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1000, 700)
	if test.HasText("工作中") || test.HasText("本轮完成") {
		t.Fatal("unknown execution has a definitive running/completed label")
	}
	if !test.HasText("执行状态待核实") {
		t.Fatal("unknown execution lacks an explicit state")
	}
}

func TestCloseFlushCapturesNativeTextBeforeNextRender(t *testing.T) {
	m := app.NewModel()
	m.Drafts["home"] = "original"
	path := filepath.Join(t.TempDir(), "state.json")
	v := New(m, nil, path)
	defer v.Saver.Close()
	v.draftBindings["home"] = &editBinding{Text: "latest native edit", LastModel: "original"}
	if err := v.Flush(); err != nil {
		t.Fatal(err)
	}
	restored := app.NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if restored.Drafts["home"] != "latest native edit" {
		t.Fatal("quit saved only the previous rendered value")
	}
}

func layoutModel() *app.Model {
	m := app.NewModel()
	m.Connected = true
	m.WorkspaceID = "w"
	m.SessionID = "s"
	m.Readiness = domain.Readiness{State: "ready", Permissions: []string{"read-only"}, Models: []domain.Model{{ID: "gpt-5.4", Default: true}}}
	s := domain.Session{ID: "s", WorkspaceID: "w", Title: "Layout", Status: "completed"}
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Name: "Example"}}, Sessions: []domain.Session{s}}
	m.Details["s"] = domain.SessionDetail{Session: s, Items: []domain.Item{{ID: "i", Kind: "assistant", Text: "Aligned reply"}}}
	return m
}
func TestConversationSharesCenteredReadingColumn(t *testing.T) {
	for _, width := range []int{1440, 950} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			test := native.NewTester(v.View, width, 900)
			reply, _ := test.Find("Aligned reply")
			history, _ := test.Find("对话记录")
			if err := test.Click("消息输入"); err != nil {
				t.Fatal(err)
			}
			caret, ok := test.TextCaret()
			if !ok || math.Abs(float64(caret.X-reply.X-16)) > 1 {
				t.Fatalf("composer text does not retain the 16px reading inset: reply=%+v caret=%+v", reply, caret)
			}
			if history.Y != 0 || history.H != 900 {
				t.Fatalf("scrollport does not span the pane: %+v", history)
			}
			if width == 1440 && math.Abs(float64(reply.X-464)) > 1 {
				t.Fatalf("reading column is not centered at frozen width: %+v", reply)
			}
		})
	}
}
func TestShortQuestionDoesNotReserveMultilineAnswerSpace(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: "Which next?", Custom: true, Options: []domain.Option{{Label: "One"}, {Label: "Two"}}}}}}
	m.Details["s"] = d
	test := native.NewTester(New(m, nil, "").View, 950, 650)
	answer, _ := test.Find("自定义答案")
	input, _ := test.Find("消息输入")
	if answer.H > 34 || answer.H < 30 {
		t.Fatalf("short answer should start compact: %+v", answer)
	}
	if input.Y+input.H > 650 || input.H < 54 {
		t.Fatalf("composer hidden by question: %+v", input)
	}
}

func TestBottomDockReservesLatestReplyWithGrowingDraft(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	for i := 0; i < 40; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: fmt.Sprintf("Reply %d", i)})
	}
	m.Details["s"] = d

	m.Readiness.Models = []domain.Model{{ID: strings.Repeat("a-long-model-name-", 5), Default: true, Efforts: []string{"medium"}}}
	m.Configs[m.DraftKey()] = domain.RunConfig{Model: m.Readiness.Models[0].ID, Effort: "medium", Permission: "read-only"}
	test := native.NewTester(New(m, nil, "").View, 850, 600)
	m.Drafts[m.DraftKey()] = strings.Repeat("A growing multiline draft\n", 12)
	test.Frame()
	reply, ok := test.Find("Reply 39")
	dock, _ := test.Find("会话底部面板")
	send, sent := test.Find("发送")
	if !ok || reply.Y+reply.H > dock.Y {
		t.Fatalf("latest reply covered by growing dock: reply=%+v dock=%+v", reply, dock)
	}
	if test.FramePending() {
		t.Fatal("dock measurements did not settle")
	}
	if !sent || send.X+send.W > 850 || send.Y+send.H > 600 {
		t.Fatalf("run configuration pushes send outside window: %+v", send)
	}
}

func TestHomeKeepsLongWorkspaceHeadingInsidePane(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Snapshot.Workspaces[0].Name = strings.Repeat("Workspace with a long name ", 10)
	test := native.NewTester(New(m, nil, "").View, 850, 600)
	picker, _ := test.Find("选择工作区")
	end, _ := test.Find("做些什么？")
	if picker.X < 224 || end.X+end.W > 832 {
		t.Fatalf("long workspace heading overflows pane: picker=%+v ending=%+v", picker, end)
	}
}

func TestCompactTokenCounts(t *testing.T) {
	for _, tt := range []struct {
		n    int64
		want string
	}{{999, "999"}, {1000, "1000"}, {1001, "1.0k"}, {19676, "19.7k"}, {39561, "39.6k"}} {
		if got := count(&tt.n); got != tt.want {
			t.Errorf("count(%d)=%s want %s", tt.n, got, tt.want)
		}
	}
	if count(nil) != "—" {
		t.Fatal("missing usage became a number")
	}
}
func TestNativeChromeUsesLabeledIconsAndCompactNavigationGap(t *testing.T) {
	test := native.NewTester(New(layoutModel(), nil, "").View, 1440, 900)
	for _, label := range []string{"新建会话", "待我处理", "搜索", "设置", "设备", "Permission"} {
		if _, ok := test.Find(label); !ok {
			t.Errorf("missing native control %s", label)
		}
	}
	first, _ := test.Find("新建会话")
	next, _ := test.Find("待我处理")
	if math.Abs(float64(first.Y-47)) > 1 || math.Abs(float64(next.Y-first.Y-first.H-4)) > 1 {
		t.Errorf("navigation geometry first=%+v next=%+v", first, next)
	}
	if !test.HasText("只读") {
		t.Fatal("permission control lacks visible named permission")
	}
}

func TestBranchStripAndIndependentInspectorGeometry(t *testing.T) {
	m := layoutModel()
	m.GitStatuses["session:s"] = domain.GitStatus{Applicable: true, Branch: "main", Branches: []string{"main", "feature"}}
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1440, 900)
	if _, ok := test.Find("选择分支"); !ok {
		t.Fatal("missing branch control")
	}
	if err := test.Click("展开更改侧栏"); err != nil {
		t.Fatal("missing inspector toggle")
	}
	test.Advance(220 * time.Millisecond)
	inspector, ok := test.Find("更改侧栏")
	history, _ := test.Find("对话记录")
	device, _ := test.Find("当前设备")
	if !ok || inspector.Y != 0 || inspector.H != 900 || inspector.W != 280 || history.X+history.W > inspector.X || device.X+device.W > inspector.X {
		t.Fatalf("inspector does not own a full-height column: inspector=%+v history=%+v device=%+v", inspector, history, device)
	}
}
func TestSearchNavigationPreservesHomeDraft(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Drafts["home"] = "Keep this draft"
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1440, 900)
	if err := test.Click("搜索"); err != nil {
		t.Fatal("no search")
	}
	if _, ok := test.Find("搜索会话"); !ok {
		t.Fatal("no native search field")
	}
	if err := test.Click("搜索结果：Layout，Example"); err != nil {
		t.Fatal("no actual session search result")
	}
	if m.SessionID != "s" || m.Drafts["home"] != "Keep this draft" {
		t.Fatal("search navigation changed the Home draft")
	}
}

func TestHomeBranchSelectionOnlyRecordsIntent(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Drafts["home"] = "Keep draft"
	m.SetGitStatus("workspace:w", domain.GitStatus{Applicable: true, Repository: "/repo", Branch: "main", Head: "abc", DefaultBranch: "main", Branches: []string{"main", "feature"}})
	test := native.NewTester(New(m, nil, "").View, 1440, 900)
	if err := test.Click("选择分支"); err != nil {
		t.Fatal(err)
	}
	if err := test.Click("分支:feature"); err != nil {
		t.Fatal(err)
	}
	if m.GitDrafts["w"].Branch != "feature" || m.Drafts["home"] != "Keep draft" || len(m.Pending) != 0 {
		t.Fatal("branch menu changed execution or draft instead of intent")
	}
}
func TestDiffReaderLeavesConversationAndDeviceControlBesideIt(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	v := New(m, nil, "")
	v.changesFor("s").Path = "file.go"
	test := native.NewTester(v.View, 1440, 900)
	reader, _ := test.Find("文件差异阅读器")
	history, _ := test.Find("对话记录")
	device, _ := test.Find("当前设备")
	if reader.X <= history.X || history.X+history.W > reader.X || device.X+device.W > reader.X {
		t.Fatalf("diff squeezed conversation or stole device control: reader=%+v history=%+v device=%+v", reader, history, device)
	}
}

func TestSearchSendsTypedQueryAndAppliesAsyncResults(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	controller := app.NewController("")
	v := New(m, controller, "")
	test := native.NewTester(v.View, 1440, 900)
	test.Click("搜索")
	test.Click("搜索会话")
	test.Type("Recorded")
	var search *app.Job
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "session.search" {
			copy := job
			search = &copy
		}
	}
	if search == nil {
		t.Fatalf("typing never sent search: text=%q sent=%q", v.searchText, v.searchSent)
	}
	if search.Input.(protocol.SearchQuery).Query != "Recorded" {
		t.Fatalf("query lagged native input: %+v", search.Input)
	}
	test.Command("selectAll")
	test.SetClipboard("NoMatch")
	test.Command("paste")
	search = nil
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "session.search" {
			copy := job
			search = &copy
		}
	}
	if search == nil || search.Input.(protocol.SearchQuery).Query != "NoMatch" {
		t.Fatalf("paste did not issue current query: %+v", search)
	}

	results, _ := json.Marshal([]domain.SearchResult{{SessionID: "s", WorkspaceID: "w", Title: "Found", Snippet: "NoMatch result"}})
	search.Apply(m, results, nil)
	test.Frame()
	if !test.HasText("NoMatch result") {
		t.Fatalf("async result missing: %+v query=%+v", v.searchResults, search.Input)
	}
}

func TestNarrowDiffDrawerStaysInsideConversationPane(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	v := New(m, nil, "")
	v.changesFor("s").Path = "file.go"
	test := native.NewTester(v.View, 850, 650)
	if !test.HasText("file.go") {
		t.Fatal("narrow file reader lacks a file name")
	}
	drawer, _ := test.Find("更改侧栏")
	if drawer.X < 224 || drawer.X+drawer.W > 850 {
		t.Fatalf("diff drawer covers workspace navigation: %+v", drawer)
	}
}

func TestSearchRetriesLatestQueryAfterControllerQueueFills(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	controller := app.NewController("")
	v := New(m, controller, "")
	test := native.NewTester(v.View, 1440, 900)
	test.Click("搜索")
	test.Click("搜索会话")
	for len(controller.Jobs) < cap(controller.Jobs) {
		controller.Jobs <- app.Job{Method: "readiness"}
	}
	test.Type("latest query")
	for len(controller.Jobs) > 0 {
		<-controller.Jobs
	}
	test.Frame()
	found := false
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if q, ok := job.Input.(protocol.SearchQuery); ok && q.Query == "latest query" {
			found = true
		}
	}
	if !found {
		t.Fatal("latest query was marked sent even though queue rejected it")
	}
}

func TestToolGroupClosesOnceOnAgentProseAndKeepsExplicitReopen(t *testing.T) {
	m := layoutModel()
	test := native.NewTester(New(m, nil, "").View, 1440, 900)
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "u", Kind: "user", Text: "Run tools"}, {ID: "tool", Kind: "tool", Name: "command one", Text: "tool output"}}
	m.Details["s"] = d
	test.Frame()
	if !test.HasText("command one") {
		t.Fatal("live tool group should start expanded")
	}
	d.Items = append(d.Items, domain.Item{ID: "answer", Kind: "assistant", Text: "Nonempty agent prose"})
	m.Details["s"] = d
	test.Frame()
	if test.HasText("command one") {
		t.Fatal("agent prose did not collapse the preceding tools")
	}
	if err := test.Click("1 次工具调用"); err != nil {
		t.Fatal(err)
	}
	test.Advance(180 * time.Millisecond)
	d.Items[1].Text = "late tool output"
	m.Details["s"] = d
	test.Frame()
	if !test.HasText("command one") || !test.HasText("late tool output") {
		t.Fatal("late output or rerender overrode explicit disclosure")
	}
}

func TestOpeningSavedSessionCollapsesEveryHistoricalToolGroup(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{
		{ID: "a", TurnID: "one", Kind: "tool", Name: "old command"},
		{ID: "answer", TurnID: "one", Kind: "assistant", Text: "Earlier reply"},
		{ID: "b", TurnID: "two", Kind: "tool", Name: "last command", Text: "saved output"},
		{ID: "c", TurnID: "two", Kind: "tool", Name: "another saved command"},
	}
	m.Details["s"] = d
	// Both groups were manually expanded before the application was closed.
	m.Collapsed["tool-group:s/a"] = false
	m.Collapsed["tool-group:s/a:prose"] = true
	m.Collapsed["tool-group:s/b"] = false
	v := New(m, nil, "")
	ui := native.NewTester(v.View, 1440, 900)
	if ui.HasText("old command") || ui.HasText("last command") {
		t.Fatal("opening saved history restored expanded tool calls")
	}
	if err := ui.Click("2 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(180 * time.Millisecond)
	if !ui.HasText("last command") {
		t.Fatal("manual expansion did not reveal the saved call")
	}
	d.Items[2].Text = "late output"
	m.Details["s"] = d
	ui.Frame()
	if !ui.HasText("late output") {
		t.Fatal("an ordinary update collapsed the manually opened group")
	}
	v.home()
	ui.Frame()
	m.SessionID = "s"
	ui.Frame()
	if ui.HasText("last command") {
		t.Fatal("reopening the saved session did not collapse its tools")
	}
	if err := ui.Click("2 次工具调用"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("设备"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("Layout"); err != nil {
		t.Fatal(err)
	}
	if ui.HasText("last command") {
		t.Fatal("opening the same session from device management retained expanded tools")
	}
}

func TestOpeningSavedSessionWaitsForHistoryBeforeCollapsingTools(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	m.Details["s"] = domain.SessionDetail{Requests: d.Requests}
	v := New(m, nil, "")
	ui := native.NewTester(v.View, 1440, 900)
	d.Items = []domain.Item{{ID: "saved", Kind: "tool", Name: "loaded command"}}
	m.Details["s"] = d
	ui.Frame()
	if ui.HasText("loaded command") {
		t.Fatal("asynchronously loaded history opened with expanded tools")
	}
	d.Items = append(d.Items, domain.Item{ID: "prose", Kind: "assistant", Text: "Continuing"}, domain.Item{ID: "new", Kind: "tool", Name: "live command"})
	m.Details["s"] = d
	ui.Frame()
	if !ui.HasText("live command") {
		t.Fatal("opening history also collapsed newly arriving live tools")
	}
}

func TestExpandedToolChildrenAndFooterShareGroupLeadingEdge(t *testing.T) {
	for _, width := range []int{1440, 950} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = []domain.Item{
			{ID: "a", TurnID: "t", Kind: "tool", Name: "first command", Text: "first result"},
			{ID: "b", TurnID: "t", Kind: "tool", Name: "second command", Text: "second result"},
		}
		d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
		m.Details["s"] = d
		ui := native.NewTester(New(m, nil, "").View, width, 900)
		if err := ui.Click("2 次工具调用"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(180 * time.Millisecond)
		group, _ := ui.Find("2 次工具调用")
		for _, label := range []string{"first command", "second command"} {
			child, ok := ui.Find(label)
			// Moving the disclosure to the trailing edge leaves all tool icons aligned.
			if !ok || math.Abs(float64(child.X-group.X)) > .5 {
				t.Errorf("width %d: %s is not under the group icon: group=%+v child=%+v", width, label, group, child)
			}
		}
		command, _ := ui.Find("first command")
		output, ok := ui.Find("first result")
		if !ok || math.Abs(float64(output.Y-command.Y-command.H-8)) > .5 {
			t.Errorf("width %d: indentation changed the tool-output gap: command=%+v output=%+v", width, command, output)
		}
		footer, ok := ui.Find("本轮完成")
		if !ok || math.Abs(float64(footer.X-group.X)) > .5 {
			t.Errorf("width %d: tool indentation moved the turn footer: %+v", width, footer)
		}
	}
}

func TestUserMessageAndEmptyProseDoNotCollapseTools(t *testing.T) {
	m := layoutModel()
	test := native.NewTester(New(m, nil, "").View, 1440, 900)
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "tool", Kind: "tool", Name: "still live"}, {ID: "empty", Kind: "assistant", Text: "  "}, {ID: "user", Kind: "user", Text: "ordinary followup"}}
	m.Details["s"] = d
	m.Drafts[m.DraftKey()] = "draft"
	test.Frame()
	if !test.HasText("still live") {
		t.Fatal("user message/draft or empty assistant item collapsed tool group")
	}
}

func TestEmptyAssistantEventsKeepToolsInOneVirtualizedGroup(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	items := []domain.Item{{ID: "a", Kind: "tool", TurnID: "turn"}, {ID: "empty", Kind: "assistant", TurnID: "turn", Text: " "}, {ID: "b", Kind: "tool", TurnID: "turn"}}
	rows := v.transcriptRows(items)
	if len(rows) != 3 || rows[0].count != 2 || rows[1].key != "a" || rows[2].key != "b" {
		t.Fatalf("tool children must stay individual virtual rows under one group: %+v", rows)
	}
}

func TestChoiceMenuDoesNotReopenAfterSessionNavigation(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1440, 900)
	if err := test.Click("Permission"); err != nil {
		t.Fatal(err)
	}
	if !test.HasText("仅查看") {
		t.Fatal("permission menu did not open")
	}
	m.SessionID = ""
	test.Frame()
	m.SessionID = "s"
	test.Frame()
	if test.HasText("仅查看") {
		t.Fatal("old menu reopened after navigation")
	}
}

func TestWorkspaceChoiceMenuSupportsDuplicateNamesAndOverflow(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	for i := 0; i < 40; i++ {
		m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: fmt.Sprint(i), Name: "project"})
	}
	test := native.NewTester(New(m, nil, "").View, 950, 650)
	if err := test.Click("选择工作区"); err != nil {
		t.Fatal(err)
	}
	// A menu must be able to show repeated user names without key collisions.
	if !test.HasText("project") {
		t.Fatal("workspace choices missing")
	}
	bounds, ok := test.Find("菜单选项")
	if !ok || bounds.H > 360 || bounds.Y < 0 || bounds.Y+bounds.H > 650 {
		t.Fatalf("unbounded menu: %+v", bounds)
	}
	test.Key(0, native.KeyEnd)
	test.Key(0, native.KeyEnter)
	if m.WorkspaceID != "39" {
		t.Fatalf("last duplicate name chose wrong workspace: %s", m.WorkspaceID)
	}

}

func TestChoiceMenuClosesAcrossDevicePageNavigation(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1440, 900)
	if err := test.Click("Permission"); err != nil {
		t.Fatal(err)
	}
	v.devices = true
	test.Frame()
	v.devices = false
	test.Frame()
	if test.HasText("仅查看") {
		t.Fatal("old menu reopened after device page navigation")
	}
}

func TestPendingGitQuestionKeepsBranchVisibleWithoutAllowingSwitch(t *testing.T) {
	m := layoutModel()
	m.GitStatuses["session:s"] = domain.GitStatus{Applicable: true, Branch: "main", Head: "abc", Branches: []string{"main", "other"}}
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", State: "pending", Kind: "questions", Title: "Need input", Questions: []domain.Question{{ID: "q", Text: "Which next?", Options: []domain.Option{{Label: "One"}, {Label: "Two"}}}}}}
	m.Details["s"] = d
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1440, 900)
	if _, ok := test.Find("选择分支"); !ok {
		t.Fatal("pending question hid the Git branch")
	}
	if v.canSwitchBranch() {
		t.Fatal("pending question allowed a checkout")
	}
	test.Click("选择分支")
	if test.HasText("搜索分支") {
		t.Fatal("disabled branch trigger opened a checkout menu")
	}
}

func TestHomeAndConversationShareComposerLeadingEdge(t *testing.T) {
	for _, home := range []bool{false, true} {
		t.Run(fmt.Sprint(home), func(t *testing.T) {
			m := layoutModel()
			key := "session:s"
			if home {
				m.SessionID = ""
				key = "workspace:w"
			}
			m.GitStatuses[key] = domain.GitStatus{Applicable: true, Branch: "main", Head: "abc", Branches: []string{"main"}}
			m.GitDrafts["w"] = domain.GitChoice{Branch: "main"}
			test := native.NewTester(New(m, nil, "").View, 1440, 900)
			branch, bok := test.Find("选择分支")
			attach, aok := test.Find("添加附件（当前 Codex 适配器暂不支持）")
			if err := test.Click("消息输入"); err != nil {
				t.Fatal(err)
			}
			caret, cok := test.TextCaret()
			if !bok || !aok || !cok || math.Abs(float64(branch.X-attach.X)) > 1 || math.Abs(float64(caret.X-attach.X-9)) > 1 {
				t.Fatalf("composer leading edges differ: branch=%+v attachment=%+v caret=%+v", branch, attach, caret)
			}
		})
	}
}

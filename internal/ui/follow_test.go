package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func progressFollowFixture() (*View, *n.Tester) {
	m := layoutModel()
	d := m.Details["s"]
	d.Session.Status = "running"
	m.Snapshot.Sessions[0].Status = "running"
	d.Items = nil
	for i := 0; i < 36; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("progress-", i), TurnID: "turn", Kind: "activity", Name: fmt.Sprint("进度 ", i), Final: true})
	}
	d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	return v, ui
}
func appendFollowProgress(v *View, label string) {
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: label, TurnID: "turn", Kind: "activity", Name: label, Final: true})
	v.Model.Details["s"] = d
}
func TestLiveFollowGlidesMeasuredProgressInsteadOfJumping(t *testing.T) {
	v, ui := progressFollowFixture()
	before, ok := ui.Find("进度 35")
	if !ok || !v.lists["s"].FollowingEnd() {
		t.Fatal("fixture is not following the live tail")
	}
	appendFollowProgress(v, "新进度")
	ui.Frame()
	first, _ := ui.Find("进度 35")
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("进度 35")
	ui.Advance(time.Second)
	end, _ := ui.Find("进度 35")
	if end.Y >= before.Y-10 {
		t.Fatal("fixture did not grow", before, end)
	}
	if math.Abs(float64(first.Y-before.Y)) > 1 {
		t.Fatal("streamed progress jumps on its first layout", before, first, middle, end)
	}
	if middle.Y >= before.Y || middle.Y <= end.Y {
		t.Fatal("live follow did not interpolate", before, first, middle, end)
	}
	if !v.lists["s"].FollowingEnd() || !v.lists["s"].AtEnd() {
		t.Fatal("follow lost the measured end after settling")
	}
}
func TestLiveFollowStopsOnReaderScrollAndResumesAtLatest(t *testing.T) {
	v, ui := progressFollowFixture()
	appendFollowProgress(v, "新进度")
	ui.Frame()
	ui.Advance(30 * time.Millisecond)
	ui.Scroll(800, 350, 0, -180)
	ui.Frame()
	if v.lists["s"].FollowingEnd() {
		t.Fatal("reader scroll did not interrupt following")
	}
	marker, ok := ui.Find("进度 30")
	if !ok {
		t.Fatal("reading marker not visible")
	}
	appendFollowProgress(v, "后续进度")
	ui.Frame()
	ui.Advance(time.Second)
	after, _ := ui.Find("进度 30")
	if math.Abs(float64(after.Y-marker.Y)) > 1 {
		t.Fatal("live growth pulled the reader away", marker, after)
	}
	if err := ui.Click("回到最新"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !v.lists["s"].FollowingEnd() || !v.lists["s"].AtEnd() {
		t.Fatal("explicit latest did not re-arm follow")
	}
}

func TestLiveMarkdownGrowthRetainsTheReadersPosition(t *testing.T) {
	v, ui := progressFollowFixture()
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "turn", Kind: "assistant", Text: strings.Repeat("较早的段落。\n\n", 30) + "阅读标记\n\n" + strings.Repeat("末尾段落。\n\n", 8)})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(3 * time.Second)
	ui.Wheel(800, 350, 0, -200)
	ui.Advance(180 * time.Millisecond)
	before, ok := ui.Find("阅读标记")
	if !ok || v.lists["s"].FollowingEnd() {
		t.Fatal("fixture did not move into Markdown history", before, ok)
	}
	for i := 0; i < 30; i++ {
		d.Items[len(d.Items)-1].Text += "\n\n新增流式段落。"
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(40 * time.Millisecond)
		after, ok := ui.Find("阅读标记")
		if !ok || math.Abs(float64(after.Y-before.Y)) > 1 || v.lists["s"].FollowingEnd() {
			t.Fatal("streaming Markdown resumed following without reader input", i, before, after, ok)
		}
	}
	d.Items[len(d.Items)-1].Final = true
	d.Session.Status, d.Turns[0].Status = "completed", "completed"
	v.Model.Snapshot.Sessions[0].Status = "completed"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	after, ok := ui.Find("阅读标记")
	if !ok || math.Abs(float64(after.Y-before.Y)) > 1 || v.lists["s"].FollowingEnd() {
		t.Fatal("completion pulled the Markdown reader to the end", before, after, ok)
	}
}

func TestLiveFollowFinishesItsGlideAfterExecutionCompletes(t *testing.T) {
	v, ui := progressFollowFixture()
	appendFollowProgress(v, "最后进度")
	ui.Frame()
	ui.Advance(40 * time.Millisecond)
	before, _ := ui.Find("进度 35")
	d := v.Model.Details["s"]
	d.Session.Status, d.Turns[0].Status = "completed", "completed"
	v.Model.Snapshot.Sessions[0].Status = "completed"
	v.Model.Details["s"] = d
	ui.Frame()
	first, _ := ui.Find("进度 35")
	ui.Advance(time.Second)
	end, _ := ui.Find("进度 35")
	if first.Y > before.Y+.5 || first.Y <= end.Y+1 || !v.lists["s"].AtEnd() {
		t.Fatal("backend completion interrupted the remaining follow motion", before, first, end)
	}
}
func TestReduceMotionFollowsProgressWithoutGliding(t *testing.T) {
	v, ui := progressFollowFixture()
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Frame()
	before, _ := ui.Find("进度 35")
	appendFollowProgress(v, "新进度")
	ui.Frame()
	after, _ := ui.Find("进度 35")
	if after.Y >= before.Y-10 || !v.lists["s"].AtEnd() {
		t.Fatal("reduced motion did not settle the measured end immediately", before, after)
	}
}

func TestReopeningLiveHistoryDoesNotReplayBackgroundProgress(t *testing.T) {
	v, ui := progressFollowFixture()
	d := v.Model.Details["s"]
	other := domain.Session{ID: "other", WorkspaceID: "w", Title: "Other", Status: "completed"}
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, other)
	v.Model.Details["other"] = domain.SessionDetail{Session: other}
	v.navigateSession("other", "w")
	ui.Frame()
	for i := 0; i < 6; i++ {
		appendFollowProgress(v, fmt.Sprint("后台进度 ", i))
	}
	v.navigateSession(d.Session.ID, "w")
	ui.Frame()
	first, ok := ui.Find("后台进度 5")
	if !ok {
		t.Fatal("reopened history did not position at its latest progress")
	}
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("后台进度 5")
	ui.Advance(time.Second)
	end, _ := ui.Find("后台进度 5")
	if math.Abs(float64(first.Y-end.Y)) > 1 || math.Abs(float64(middle.Y-end.Y)) > 1 {
		t.Fatal("reopening replays already received progress", first, middle, end)
	}
}

func TestFollowKeepsProgressAboveAnOpeningQuestionDock(t *testing.T) {
	v, ui := progressFollowFixture()
	d := v.Model.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "question", SessionID: "s", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: "请选择接下来的方向", Custom: true, Options: []domain.Option{{Label: "继续检查"}, {Label: "补充测试"}}}}}}
	v.Model.Details["s"] = d
	ui.Frame()
	for i := 0; i < 30; i++ {
		ui.Advance(time.Second / 60)
		receipt, ok := ui.Find("回复信息")
		dock, dockOK := ui.Find("会话底部面板")
		if !ok || !dockOK {
			t.Fatal("fixture lost progress or dock")
		}
		if receipt.Y+receipt.H > dock.Y+1 {
			t.Fatal("opening dock covers the followed progress", i, receipt, dock)
		}
	}
	if body, ok := ui.Find("请求内容区域"); !ok || body.H < 80 {
		t.Fatal("fixture did not open its question dock", body, ok)
	}
	if !v.lists["s"].FollowingEnd() {
		t.Fatal("dock growth was mistaken for reader navigation")
	}
}

func TestHistoryRestartFollowsTheReplacementReply(t *testing.T) {
	v, ui, _ := historyFixture()
	enableReferenceHistory(v)
	if err := ui.Click("修改并重新开始"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	v.Model.Drafts[v.Model.HistoryDraftKey()] = "修订后的问题"
	ui.Frame()
	v.restartHistory()
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
	in := v.historyExit.in
	before, _ := ui.Find("用户消息气泡")
	keep := int64(-1)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
	ui.Frame()
	replacement := domain.Item{ID: "input:" + in.OperationID, TurnID: "replacement", Kind: "user", Text: in.Text, Status: "sent", Final: true}
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}})
	ui.Frame()
	v.Model.ApplyEvent(domain.Event{Sequence: 3, Type: "turn", SessionID: "s", Turn: &domain.Turn{ID: "replacement", Status: "running"}})
	ui.Frame()
	ui.Advance(600 * time.Millisecond)
	after, _ := ui.Find("用户消息气泡")
	if math.Abs(float64(before.Y-after.Y)) > 1 {
		t.Fatal("replacement handoff moves the edited message", before, after)
	}
	for i := 0; i < 40; i++ {
		v.Model.ApplyEvent(domain.Event{Sequence: int64(4 + i), Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: fmt.Sprint("replacement-progress-", i), TurnID: "replacement", Kind: "activity", Name: fmt.Sprint("重开进度 ", i), Final: true}})
		ui.Frame()
		ui.Advance(50 * time.Millisecond)
	}
	ui.Advance(time.Second)
	footer, ok := ui.Find("回复信息")
	dock, _ := ui.Find("会话底部面板")
	if !v.lists["s"].FollowingEnd() || !v.lists["s"].AtEnd() || !ok || footer.H < 1 || footer.Y+footer.H > dock.Y {
		t.Fatal("replacement reply stays pinned instead of following progress", footer, dock)
	}
	ui.Scroll(800, 350, 0, -180)
	ui.Frame()
	if v.lists["s"].FollowingEnd() {
		t.Fatal("reader cannot interrupt replacement follow")
	}
	reading, ok := ui.Find("重开进度 30")
	if !ok {
		t.Fatal("reading marker missing")
	}
	v.Model.ApplyEvent(domain.Event{Sequence: 44, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "later-progress", TurnID: "replacement", Kind: "activity", Name: "后续进度", Final: true}})
	ui.Frame()
	ui.Advance(time.Second)
	settled, ok := ui.Find("重开进度 30")
	if !ok || math.Abs(float64(settled.Y-reading.Y)) > 1 || v.lists["s"].FollowingEnd() {
		t.Fatal("later replacement output overrides reader scroll", reading, settled)
	}
}

func TestFirstReplyReplacesTheWaitingLineAtTheSamePosition(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Session.Status = "running"
	d.Items = []domain.Item{{ID: "user", TurnID: "turn", Kind: "user", Status: "sent", Text: "解释旋转", Final: true}}
	d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	waiting, ok := ui.Find("进行中")
	if !ok {
		t.Fatal("missing waiting line")
	}
	d.Items = append(d.Items, domain.Item{ID: "first-reply", TurnID: "turn", Kind: "assistant", Text: "首行回复接替等待位置。"})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	first, ok := ui.Find("首行回复接替等待位置。")
	if !ok || math.Abs(float64(first.Y-waiting.Y)) > 1 {
		t.Fatal("first output starts below the previous waiting line", waiting, first)
	}
}

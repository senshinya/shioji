package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func sessionLoadingFixture(cached ...bool) (*View, *n.Tester) {
	m := layoutModel()
	m.WorkerMinor = 37
	m.ConnectionEpoch = "opening-device"
	m.Drafts["session:s"] = "保留的普通草稿"
	d := m.Details["s"]
	d.Session.ThreadID = "opening-thread"
	d.Items = nil
	d.HistoryLoaded = false
	d.History = &domain.HistoryWindow{Invalidated: true}
	if len(cached) > 0 && cached[0] {
		d.HistoryLoaded = true
		d.Items = []domain.Item{{ID: "cached", Kind: "assistant", Text: "缓存会话正文", Final: true, Order: 1}}
		d.History = &domain.HistoryWindow{First: 1, Last: 1, Invalidated: true}
	}
	m.Details["s"] = d
	m.BeginHistoryRead("s")
	v := New(m, app.NewController(""), "")
	return v, n.NewTester(v.View, 1240, 820)
}

func completeSessionLoading(v *View) {
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "loaded-reply", Kind: "assistant", Text: "设备返回的真实消息", Order: 1, Final: true}}
	d.History = &domain.HistoryWindow{First: 1, Last: 1}
	v.Model.Details["s"] = d
	delete(v.Model.HistoryReads, "s")
}

// A stalled initial read must have visible feedback, while the native editor
// retains its draft and cannot send inputs against an unhydrated session.
func TestSessionLoadingDelaysFeedbackAndPreservesDraft(t *testing.T) {
	v, ui := sessionLoadingFixture()
	ui.Advance(150 * time.Millisecond)
	if ui.HasText("正在读取会话") {
		t.Fatal("a quick initial read flashed loading feedback")
	}
	ui.Advance(200 * time.Millisecond)
	if !ui.HasText("正在读取会话") {
		t.Fatal("a stalled initial read leaves a blank conversation")
	}
	if _, ok := ui.Find("发送框加载骨架"); !ok {
		t.Fatal("loading feedback has no bottom composer skeleton")
	}
	ui.Click("消息输入")
	ui.Type("不应写入")
	if v.Model.Drafts["session:s"] != "保留的普通草稿" {
		t.Fatal("invisible loading editor accepted input")
	}
	completeSessionLoading(v)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("正在读取会话") || !ui.HasText("设备返回的真实消息") {
		t.Fatal("initial history did not replace loading feedback")
	}
	ui.Click("消息输入")
	ui.Type("，继续")
	if v.Model.Drafts["session:s"] != "保留的普通草稿，继续" {
		t.Fatal("history handoff lost or replaced the draft", v.Model.Drafts)
	}
}

func TestSessionLoadingSkipsFastAndCachedHistory(t *testing.T) {
	for _, cached := range []bool{false, true} {
		v, ui := sessionLoadingFixture(cached)
		if !cached {
			ui.Advance(80 * time.Millisecond)
			completeSessionLoading(v)
		}
		ui.Frame()
		ui.Advance(400 * time.Millisecond)
		if ui.HasText("正在读取会话") {
			t.Fatalf("fast/cached history displayed an initial loader (cached=%v)", cached)
		}
		ui.Click("消息输入")
		ui.Type("，继续")
		if v.Model.Drafts["session:s"] != "保留的普通草稿，继续" {
			t.Fatalf("fast/cached history blocked the native editor (cached=%v)", cached)
		}
	}
}

func TestSessionLoadingDoesNotRestartForHistoryResetInVisibleSession(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	d := v.Model.Details["s"]
	d.Items = nil
	d.History = &domain.HistoryWindow{Invalidated: true}
	v.Model.Details["s"] = d
	v.Model.HistoryReads["s"] = &app.HistoryRead{Pending: true}
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	if ui.HasText("正在读取会话") {
		t.Fatal("a visible conversation's history reset started an opening animation")
	}
}

func TestSessionLoadingFailureStopsAnimationAndRetriesOnOwningDevice(t *testing.T) {
	v, ui := sessionLoadingFixture()
	ui.Advance(400 * time.Millisecond)
	v.Model.HistoryReads["s"].Pending = false
	v.Model.HistoryReads["s"].Error = "history unavailable"
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	if ui.HasText("正在读取会话") || !ui.HasText("未能读取会话") {
		t.Fatal("failed read kept an indefinite progress animation", ui.Texts())
	}
	if err := ui.Click("重试读取会话"); err != nil {
		t.Fatal(err)
	}
	j := historyJob(t, v, "session.history")
	if q := j.Input.(domain.HistoryQuery); q.SessionID != "s" || q.ThreadID != "opening-thread" {
		t.Fatal("retry did not target the original conversation", q)
	}
	complete := v.Model.Details["s"]
	complete.History = &domain.HistoryWindow{}
	b, _ := json.Marshal(complete)
	j.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("正在读取会话") || ui.HasText("未能读取会话") {
		t.Fatal("an authoritative empty conversation is still treated as loading")
	}
	if v.Model.Drafts["session:s"] != "保留的普通草稿" {
		t.Fatal("retry changed the draft")
	}
}

func TestSessionLoadingRemoteRetryKeepsDeviceAndConnectionEpoch(t *testing.T) {
	m := deviceUIModel()
	m.SessionID, m.WorkspaceID, m.ActiveConnection = "remote-session", "wb", "air"
	m.ApplyDeviceUpdate("air", func(p *app.Model) {
		p.WorkerMinor = 37
		p.Snapshot.Sessions = []domain.Session{{ID: "remote-session", WorkspaceID: "wb", ThreadID: "remote-thread"}}
		p.Details["remote-session"] = domain.SessionDetail{Session: p.Snapshot.Sessions[0], History: &domain.HistoryWindow{Invalidated: true}}
	})
	m.HistoryReads[m.SessionID] = &app.HistoryRead{Error: "remote read failed"}
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(400 * time.Millisecond)
	if err := ui.Click("重试读取会话"); err != nil {
		t.Fatal(err)
	}
	j := historyJob(t, v, "session.history")
	if j.ConnectionID != "air" || j.ConnectionEpoch != "b" {
		t.Fatal("remote history retry escaped its owning connection", j.ConnectionID, j.ConnectionEpoch)
	}
	q := j.Input.(domain.HistoryQuery)
	if q.SessionID != "remote-session" || q.ThreadID != "remote-thread" {
		t.Fatal("remote history retry targeted another conversation", q)
	}
}

func TestSessionLoadingHandoffKeepsComposerAndInputCoordinates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := sessionLoadingFixture()
		ui.SetDark(dark)
		ui.Advance(400 * time.Millisecond)
		before, _ := ui.Find("消息编辑器")
		input, _ := ui.Find("消息输入")
		completeSessionLoading(v)
		ui.Frame()
		for i := 0; i < 30; i++ {
			ui.Advance(time.Second / 60)
			after, _ := ui.Find("消息编辑器")
			current, _ := ui.Find("消息输入")
			if math.Abs(float64(after.Y-before.Y)) > 1 || math.Abs(float64(after.H-before.H)) > 1 || math.Abs(float64(current.Y-input.Y)) > 1 {
				t.Fatalf("handoff moves the composer or its content (dark=%v): %v/%v input %v/%v", dark, before, after, input, current)
			}
		}
	}
}

func TestSessionLoadingPaintsCrossfadeAndStopsWhenDisconnected(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := sessionLoadingFixture()
		ui.SetDark(dark)
		ui.Advance(400 * time.Millisecond)
		box, _ := ui.Find("消息编辑器")
		area := image.Rect(int(box.X)+8, int(box.Y)+8, int(box.X+box.W)-8, int(box.Y+box.H)-8)
		before := copyFrame(ui)
		completeSessionLoading(v)
		ui.Frame()
		if differentPixels(before, ui.Image(), area) != 0 {
			t.Fatal("composer contents flash at the start of handoff")
		}
		ui.Advance(180 * time.Millisecond)
		middle := copyFrame(ui)
		ui.Advance(400 * time.Millisecond)
		if differentPixels(before, middle, area) < 30 || differentPixels(middle, ui.Image(), area) < 30 {
			t.Fatal("composer has no intermediate crossfade frame")
		}
		v, ui = sessionLoadingFixture()
		ui.Advance(400 * time.Millisecond)
		v.Model.Connected, v.Model.ConnectionEpoch = false, ""
		ui.Frame()
		ui.Advance(time.Second)
		if ui.HasText("正在读取会话") || !ui.HasText("设备连接已断开") {
			t.Fatal("disconnect left the initial loader spinning")
		}
	}
}

func TestSessionLoadingKeepsKnownStopAndPendingQuestionsUsable(t *testing.T) {
	v, ui := sessionLoadingFixture()
	v.Model.Readiness.Capabilities.Interrupt = true
	v.Model.Snapshot.Sessions[0].Status = domain.ExecutionRunning
	d := v.Model.Details["s"]
	d.Session.Status = domain.ExecutionRunning
	d.Turns = []domain.Turn{{ID: "active", Status: domain.ExecutionRunning}}
	d.Requests = []domain.PendingRequest{{ID: "question", SessionID: "s", Kind: "questions", State: "pending", Questions: []domain.Question{{ID: "q", Text: "请选择继续方式", Custom: true}}}}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("请选择继续方式") {
		t.Fatal("history hydration concealed a pending request")
	}
	if err := ui.Click("停止当前轮"); err != nil {
		t.Fatal(err)
	}
	j := historyJob(t, v, "turn.interrupt")
	if j.Input.(map[string]string)["turnId"] != "active" {
		t.Fatal("Stop targeted a different execution")
	}
}

func TestSessionLoadingIsScopedToEachVisiblePane(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	other := domain.Session{ID: "other", WorkspaceID: "w", ThreadID: "other-thread", Title: "另一个会话"}
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, other)
	v.Model.Details[other.ID] = domain.SessionDetail{Session: other, History: &domain.HistoryWindow{Invalidated: true}}
	v.Model.HistoryReads[other.ID] = &app.HistoryRead{Pending: true}
	ui := n.NewTester(v.View, 1440, 900)
	v.openParallel(other.ID)
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	if !ui.HasText("正在读取会话") || !ui.HasText("Aligned reply") {
		t.Fatal("opening a secondary pane covered the cached primary conversation")
	}
	v.closeParallel()
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	if ui.HasText("正在读取会话") {
		t.Fatal("hidden pane kept its loading presentation")
	}
	d := v.Model.Details[other.ID]
	d.Items = []domain.Item{{ID: "late", Kind: "assistant", Text: "后台返回的另一会话", Final: true}}
	d.History = &domain.HistoryWindow{}
	v.Model.Details[other.ID] = d
	delete(v.Model.HistoryReads, other.ID)
	ui.Frame()
	if ui.HasText("后台返回的另一会话") || !ui.HasText("Aligned reply") {
		t.Fatal("late hydration replaced the current pane")
	}
}

func TestSessionLoadingMotionUsesNativePaintAndRespectsReducedMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v, ui := sessionLoadingFixture()
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(400 * time.Millisecond)
		r, ok := ui.Find("会话加载曲线")
		if !ok {
			t.Fatal("initial loading has no native curve drawing")
		}
		area := image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
		before := copyFrame(ui)
		ui.Advance(300 * time.Millisecond)
		changed := differentPixels(before, ui.Image(), area)
		if reduced && changed != 0 || !reduced && changed < 100 {
			t.Fatalf("native curve repaint does not respect Reduce Motion: reduced=%v pixels=%d", reduced, changed)
		}
		completeSessionLoading(v)
		ui.Frame()
		if reduced {
			if ui.HasText("正在读取会话") || v.loadingBlocksComposer() {
				t.Fatal("reduced-motion handoff waited for a decorative exit")
			}
		}
	}
}

func TestSessionLoadingNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_SESSION_LOADING_CAPTURE")
	if dir == "" {
		t.Skip("opt-in production native loading recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
			v, ui := sessionLoadingFixture()
			ui.SetDark(dark)
			for frame := 0; frame < 240; frame++ {
				if frame == 207 {
					completeSessionLoading(v)
					ui.Frame()
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
			v, ui = sessionLoadingFixture()
			ui.SetSize(950, 650)
			ui.SetDark(dark)
			ui.Advance(400 * time.Millisecond)
			saveNativeSkillImage(t, ui, "narrow.png")
			ui.SetPreferences(n.Preferences{ReduceMotion: true})
			ui.Advance(400 * time.Millisecond)
			saveNativeSkillImage(t, ui, "reduced.png")
			v.Model.HistoryReads["s"].Pending = false
			v.Model.HistoryReads["s"].Error = "fixture history unavailable"
			ui.Frame()
			ui.Advance(400 * time.Millisecond)
			saveNativeSkillImage(t, ui, "failed.png")
		})
	}
}

func TestInitialHistoryWaitsForReadDespiteLiveTail(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			for _, delay := range []time.Duration{0, 80 * time.Millisecond, 300 * time.Millisecond} {
				t.Run(fmt.Sprintf("dark=%t/reduced=%t/delay=%s", dark, reduced, delay), func(t *testing.T) {
					checkInitialHistoryLiveTail(t, dark, reduced, delay)
				})
			}
		}
	}
}

func checkInitialHistoryLiveTail(t *testing.T, dark, reduced bool, delay time.Duration) {
	v, ui := sessionLoadingFixture()
	ui.SetDark(dark)
	ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
	ui.Advance(delay)
	if dir := os.Getenv("SHIOJI_SKILL_SNAPSHOTS"); dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprintf("%t-%t-%d", dark, reduced, delay)))
	}
	frame := 0
	advance := func() {
		dir := os.Getenv("SHIOJI_INITIAL_HISTORY_RECORDING")
		if dir == "" || reduced || delay != 300*time.Millisecond {
			ui.Advance(time.Second)
			return
		}
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark)))
		for range 30 {
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			frame++
			ui.Advance(time.Second / 30)
		}
	}
	d := v.Model.Details["s"]
	d.Session.Status = domain.ExecutionRunning
	d.Turns = []domain.Turn{{ID: "active", Status: domain.ExecutionRunning}}
	v.Model.Details["s"] = d
	if err := v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "tail", TurnID: "active", Kind: "assistant", Text: "历史尚未到达时收到的实时回复", Order: 100}}); err != nil {
		t.Fatal(err)
	}
	if delay == 0 {
		v.sessionLoad = nil
	} // Event was already queued before the first visible loading frame.
	ui.Frame()
	advance()
	saveNativeSkillImage(t, ui, "pending-history.png")
	before := v.historyRowBounds["tail"]
	opacity := v.loadingHistoryOpacity()
	loader := ui.HasText("正在读取会话")
	d = v.Model.Details["s"]
	tail := d.Items
	d.Items = nil
	for i := 0; i < 20; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("old-", i), Kind: "assistant", Text: "先前的完整对话。\n\n先前的完整对话。", Final: true, Order: int64(i + 1)})
	}
	d.Items = append(d.Items, tail...)
	d.History = &domain.HistoryWindow{First: 1, Last: 100}
	if err := v.Model.MergeHistoryPage(d, v.Model.HistoryReads["s"], true, domain.HistoryQuery{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	v.Model.FoldToolHistory["s"] = true
	ui.Frame()
	advance()
	saveNativeSkillImage(t, ui, "complete-history.png")
	after := v.historyRowBounds["tail"]
	t.Logf("pending read: history opacity=%g loader=%t tail=%v; after history=%v", opacity, loader, before, after)
	if opacity != 0 || !loader {
		t.Fatal("live event exposed an incomplete initial history before the owning worker read completed")
	}
	if v.loadingHistoryOpacity() != 1 || ui.HasText("正在读取会话") || !v.lists["s"].AtEnd() || !ui.HasText("历史尚未到达时收到的实时回复") {
		t.Fatal("completed history did not enter at its natural latest position")
	}
	if v.Model.Drafts["session:s"] != "保留的普通草稿" {
		t.Fatal("initial read changed the draft")
	}
	ui.Scroll(850, 350, 0, -120)
	ui.Frame()
	if v.lists["s"].FollowingEnd() {
		t.Fatal("completed history did not release normal scrolling")
	}
}
